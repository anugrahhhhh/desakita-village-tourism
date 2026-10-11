// Package csrf implements synchronizer-token CSRF protection backed by the session.
//
// Forms send the token in the "csrf_token" field; HTMX sends it in the X-CSRF-Token header
// (set once on <body hx-headers>).
package csrf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
)

const (
	sessionKey = "csrf_token"
	FieldName  = "csrf_token"
	HeaderName = "X-CSRF-Token"
)

// Token returns the session's CSRF token, creating one if needed.
func Token(ctx context.Context, sm *scs.SessionManager) string {
	if tok := sm.GetString(ctx, sessionKey); tok != "" {
		return tok
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	sm.Put(ctx, sessionKey, tok)
	return tok
}

// Rotate discards the current token; call it whenever the user's identity changes.
func Rotate(ctx context.Context, sm *scs.SessionManager) {
	sm.Remove(ctx, sessionKey)
}

// multipartMemory is how much of a multipart body is kept in memory; the rest spills to
// temp files. Bodies are size-limited upstream anyway.
const multipartMemory = 8 << 20

// Protect rejects unsafe requests whose token does not match the session's. Body size must
// already be limited, because reading the form field parses the request body; a body over
// the limit is answered by onTooLarge.
func Protect(sm *scs.SessionManager, onFail, onTooLarge http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				next.ServeHTTP(w, r)
				return
			}
			want := sm.GetString(r.Context(), sessionKey)
			got := r.Header.Get(HeaderName)
			if got == "" {
				var err error
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
					err = r.ParseMultipartForm(multipartMemory)
				} else {
					err = r.ParseForm()
				}
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					onTooLarge.ServeHTTP(w, r)
					return
				}
				got = r.PostFormValue(FieldName)
			}
			if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
				onFail.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

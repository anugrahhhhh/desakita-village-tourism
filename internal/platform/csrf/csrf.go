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
	"net/http"

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

// Protect rejects unsafe requests whose token does not match the session's. Body size must
// already be limited, because reading the form field parses the request body.
func Protect(sm *scs.SessionManager, onFail http.Handler) func(http.Handler) http.Handler {
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

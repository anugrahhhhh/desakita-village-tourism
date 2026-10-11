// Package httpx holds HTTP helpers shared by every module: the current viewer, safe
// redirects, login gating and body limits. It must not import any domain module.
package httpx

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Viewer is the logged-in user as seen by templates and handlers.
type Viewer struct {
	ID      int64
	Name    string
	Email   string
	Phone   string
	IsAdmin bool
}

type viewerKey struct{}

func WithViewer(ctx context.Context, v Viewer) context.Context {
	return context.WithValue(ctx, viewerKey{}, v)
}

func ViewerFrom(ctx context.Context) (Viewer, bool) {
	v, ok := ctx.Value(viewerKey{}).(Viewer)
	return v, ok
}

// SafeRedirect returns next if it is an internal path, otherwise "/".
func SafeRedirect(next string) string {
	if next == "" || next[0] != '/' {
		return "/"
	}
	if strings.HasPrefix(next, "//") || strings.ContainsRune(next, '\\') {
		return "/"
	}
	// Browsers strip tabs and newlines, so "/\t/evil.com" would become "//evil.com".
	for _, c := range next {
		if c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return next
}

// Redirect sends a See Other redirect, or HX-Redirect for HTMX requests so the whole page
// navigates instead of swapping the target.
func Redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// Middleware is a standard net/http middleware.
type Middleware = func(http.Handler) http.Handler

// Pass is a no-op Middleware, the default for optional hooks such as rate limits.
func Pass(next http.Handler) http.Handler { return next }

// LoginPath is where RequireLogin sends anonymous users.
const LoginPath = "/masuk"

// RequireLogin redirects anonymous users to the login page, remembering where they were.
func RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := ViewerFrom(r.Context()); !ok {
			back := r.URL.RequestURI()
			if r.Method != http.MethodGet {
				// Don't bounce back into a POST; return to the page that submitted it.
				back = "/"
				if ref, err := url.Parse(r.Referer()); err == nil && ref.Host == r.Host {
					back = ref.RequestURI()
				}
			}
			Redirect(w, r, LoginPath+"?next="+url.QueryEscape(SafeRedirect(back)))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// BodyLimit caps request bodies: multipart (photo upload) bodies at multipartLimit,
// everything else at formLimit.
func BodyLimit(formLimit, multipartLimit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := formLimit
			if IsMultipart(r) {
				n = multipartLimit
			}
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

func IsMultipart(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}

// ClientIP returns the request's client address. X-Forwarded-For / X-Real-IP are honoured
// only when trustProxy is set (the app runs behind a reverse proxy that overwrites them);
// otherwise any client could spoof them to dodge rate limits.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The proxy appends the address it saw; take the last hop.
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// SecurityHeaders sets defensive response headers. HSTS is only sent when secure is true.
func SecurityHeaders(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("X-Permitted-Cross-Domain-Policies", "none")
			if secure {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin answers 404 to anyone but admins, so the admin area isn't discoverable.
// notFound renders the response. Use after RequireLogin.
func RequireAdmin(notFound http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if v, ok := ViewerFrom(r.Context()); !ok || !v.IsAdmin {
				notFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

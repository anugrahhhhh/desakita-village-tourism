package app

import (
	"net/http"

	"desakita/internal/platform/httpx"
	"desakita/internal/platform/i18n"
)

// detectLang puts the request language in the context (SPEC §15): the visitor's explicit
// choice (cookie), then the logged-in account's language, then Accept-Language, then the
// default. It runs once at the root (so 404 and CSRF error pages are localized) and again
// after LoadViewer, when the account is known.
func detectLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := ""
		if c, err := r.Cookie(i18n.CookieName); err == nil && i18n.IsSupported(c.Value) {
			lang = c.Value
		}
		if v, ok := httpx.ViewerFrom(r.Context()); lang == "" && ok && i18n.IsSupported(v.Locale) {
			lang = v.Locale
		}
		if lang == "" {
			lang = i18n.Negotiate(r.Header.Get("Accept-Language"))
		}
		if lang == "" {
			lang = i18n.Default
		}
		next.ServeHTTP(w, r.WithContext(i18n.WithLang(r.Context(), lang)))
	})
}

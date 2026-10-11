package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSafeRedirect(t *testing.T) {
	cases := map[string]string{
		"":                       "/",
		"/":                      "/",
		"/akun":                  "/akun",
		"/desa/a?tab=paket#x":    "/desa/a?tab=paket#x",
		"//evil.com":             "/",
		"///evil.com":            "/",
		"/\\evil.com":            "/",
		"/a\\b":                  "/",
		"https://evil.com":       "/",
		"http:/evil.com":         "/",
		"javascript:alert(1)":    "/",
		"evil.com":               "/",
		"/\t/evil.com":           "/",
		"/\n/evil.com":           "/",
		" /akun":                 "/",
		"/%2F%2Fevil.com":        "/%2F%2Fevil.com", // stays a path on this host
		"/foo?next=//evil.com":   "/foo?next=//evil.com",
		"/user@evil.com":         "/user@evil.com",
		"\\\\evil.com":           "/",
		"/\x7f/evil.com":         "/",
		"/ok/" + string(rune(0)): "/",
	}
	for in, want := range cases {
		if got := SafeRedirect(in); got != want {
			t.Errorf("SafeRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequireLogin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := RequireLogin(ok)

	t.Run("anonymous GET is sent to login with next", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/akun?x=1", nil))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/masuk?next=%2Fakun%3Fx%3D1" {
			t.Errorf("Location = %q", loc)
		}
	})

	t.Run("anonymous HTMX request gets HX-Redirect", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/akun", nil)
		req.Header.Set("HX-Request", "true")
		h.ServeHTTP(rec, req)
		if rec.Header().Get("HX-Redirect") == "" {
			t.Error("missing HX-Redirect")
		}
	})

	t.Run("anonymous POST returns to same-host referer only", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://example.com/pesan", nil)
		req.Header.Set("Referer", "http://evil.com/x")
		h.ServeHTTP(rec, req)
		if loc := rec.Header().Get("Location"); loc != "/masuk?next=%2F" {
			t.Errorf("Location = %q", loc)
		}
	})

	t.Run("viewer passes through", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/akun", nil)
		req = req.WithContext(WithViewer(req.Context(), Viewer{ID: 1}))
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusTeapot {
			t.Errorf("status = %d", rec.Code)
		}
	})
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:4321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 9.9.9.9")
	if got := ClientIP(r, false); got != "10.0.0.5" {
		t.Errorf("untrusted proxy: %q, want RemoteAddr host", got)
	}
	if got := ClientIP(r, true); got != "9.9.9.9" {
		t.Errorf("trusted proxy: %q, want last hop", got)
	}
	r.Header.Set("X-Real-IP", "8.8.8.8")
	if got := ClientIP(r, true); got != "8.8.8.8" {
		t.Errorf("X-Real-IP: %q", got)
	}
}

func TestRequireAdmin(t *testing.T) {
	notFound := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }
	h := RequireAdmin(notFound)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	for name, v := range map[string]*Viewer{"anonymous": nil, "user": {ID: 1}, "admin": {ID: 2, IsAdmin: true}} {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		if v != nil {
			req = req.WithContext(WithViewer(req.Context(), *v))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := http.StatusNotFound
		if name == "admin" {
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", name, rec.Code, want)
		}
	}
}

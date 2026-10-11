package app_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestHealthAndReadiness(t *testing.T) {
	srv, pool := newApp(t)
	c := newClient(t, srv)

	if r := c.get("/healthz"); r.code != http.StatusOK || r.body != "ok" {
		t.Errorf("/healthz = %d %q", r.code, r.body)
	}
	if r := c.get("/readyz"); r.code != http.StatusOK || r.body != "ready" {
		t.Errorf("/readyz = %d %q", r.code, r.body)
	}

	// With the database gone, the process is still alive but no longer ready.
	pool.Close()
	if r := c.get("/healthz"); r.code != http.StatusOK {
		t.Errorf("/healthz without DB = %d, want 200 (liveness must not depend on the DB)", r.code)
	}
	if r := c.get("/readyz"); r.code != http.StatusServiceUnavailable {
		t.Errorf("/readyz without DB = %d, want 503", r.code)
	}
}

func TestCacheHeaders(t *testing.T) {
	c := newClient(t, newServer(t))
	for _, p := range []string{"/", "/masuk", "/healthz", "/tidak-ada"} {
		if cc := c.get(p).header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", p, cc)
		}
	}
	if cc := c.get("/static/app.css").header.Get("Cache-Control"); !strings.HasPrefix(cc, "public") {
		t.Errorf("static Cache-Control = %q, want public caching", cc)
	}
}

func TestCrossOriginHeaders(t *testing.T) {
	r := newClient(t, newServer(t)).get("/")
	for h, want := range map[string]string{
		"Cross-Origin-Opener-Policy":        "same-origin",
		"Cross-Origin-Resource-Policy":      "same-origin",
		"X-Permitted-Cross-Domain-Policies": "none",
		"Permissions-Policy":                "geolocation=()",
	} {
		if !strings.Contains(r.header.Get(h), want) {
			t.Errorf("%s = %q, want %q", h, r.header.Get(h), want)
		}
	}
}

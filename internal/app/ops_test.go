package app_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"desakita/web"
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

// Pages link CSS/JS with a content hash, so a deploy can never pair new HTML with a cached
// old stylesheet; versioned files are cached for a year.
func TestVersionedAssets(t *testing.T) {
	c := newClient(t, newServer(t))
	body := c.get("/").body
	m := regexp.MustCompile(`href="(/static/app\.css\?v=([0-9a-f]{8}))"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("app.css is not linked with a content hash")
	}
	b, err := fs.ReadFile(web.Static(), "app.css")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if want := hex.EncodeToString(sum[:4]); m[2] != want {
		t.Errorf("hash %s, want %s (hash of the served file)", m[2], want)
	}
	for _, js := range []string{"app.js", "htmx.min.js"} {
		if !regexp.MustCompile(`src="/static/` + regexp.QuoteMeta(js) + `\?v=[0-9a-f]{8}"`).MatchString(body) {
			t.Errorf("%s is not linked with a content hash", js)
		}
	}
	r := c.get(m[1])
	if r.code != 200 || r.header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("versioned asset: %d %q", r.code, r.header.Get("Cache-Control"))
	}
	if strings.Contains(c.get("/peta").body, `href="/static/leaflet/leaflet.css"`) {
		t.Error("map assets are not versioned")
	}
}

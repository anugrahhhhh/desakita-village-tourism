package tiles

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"desakita/internal/platform/geo"
)

var png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 64)...)

// upstream is a fake tile server counting requests.
type upstream struct {
	srv   *httptest.Server
	hits  atomic.Int32
	agent atomic.Value
	body  []byte
	code  int
	delay time.Duration
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{body: png, code: http.StatusOK}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		u.agent.Store(r.UserAgent())
		time.Sleep(u.delay)
		w.WriteHeader(u.code)
		w.Write(u.body) //nolint:errcheck
	}))
	t.Cleanup(u.srv.Close)
	return u
}

type env struct {
	srv   *Server
	up    *upstream
	h     http.Handler
	clock time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	up := newUpstream(t)
	e := &env{up: up, clock: time.Now()}
	e.srv = New(t.TempDir(), up.srv.URL+"/{z}/{x}/{y}.png", "DesaKita-test", slog.New(slog.DiscardHandler))
	e.srv.Now = func() time.Time { return e.clock }
	r := chi.NewRouter()
	r.Get("/tiles/{z}/{x}/{y}", e.srv.ServeHTTP)
	e.h = r
	return e
}

func (e *env) get(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// yogyakarta is a tile over Yogyakarta at zoom 12.
const yogyakarta = "/tiles/12/3303/2137.png"

func TestParseTile(t *testing.T) {
	minX, minY, maxX, maxY := geo.Indonesia.TileRange(12)
	ok := []string{"0/0/0", "4/15/15", fmt.Sprintf("12/%d/%d", minX, minY), fmt.Sprintf("12/%d/%d", maxX, maxY), "12/3303/2137"}
	bad := []string{
		"18/0/0", // beyond MaxZoom
		"2/4/0",  // outside the grid
		"12/0/0", // detailed tile outside Indonesia
		"12/-1/0", "12/+1/0", "12/1e3/0", "x/0/0", "12/3303/", "12/3303/2137.5",
		"12/0003303000000/2137",
	}
	for _, s := range ok {
		parts := bytes.Split([]byte(s), []byte("/"))
		if _, _, _, valid := ParseTile(string(parts[0]), string(parts[1]), string(parts[2])); !valid {
			t.Errorf("%s rejected", s)
		}
	}
	for _, s := range bad {
		parts := bytes.SplitN([]byte(s+"//"), []byte("/"), 4)
		if _, _, _, valid := ParseTile(string(parts[0]), string(parts[1]), string(parts[2])); valid {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestFetchesOnceThenServesFromCache(t *testing.T) {
	e := newEnv(t)
	for range 3 {
		rec := e.get(yogyakarta)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), png) {
			t.Fatalf("status %d", rec.Code)
		}
		if rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("Cache-Control") != "public, max-age=86400" {
			t.Errorf("headers: %v", rec.Header())
		}
	}
	if n := e.up.hits.Load(); n != 1 {
		t.Errorf("upstream hit %d times, want 1", n)
	}
	if ua := e.up.agent.Load(); ua != "DesaKita-test" {
		t.Errorf("user agent %q", ua)
	}
	if _, err := os.Stat(filepath.Join(e.srv.Dir, "12", "3303", "2137.png")); err != nil {
		t.Errorf("tile not cached on disk: %v", err)
	}

	// After MaxAge the tile is refreshed; if the upstream then fails, the stale copy is served.
	os.Chtimes(filepath.Join(e.srv.Dir, "12", "3303", "2137.png"), e.clock.Add(-8*24*time.Hour), e.clock.Add(-8*24*time.Hour)) //nolint:errcheck
	e.up.code = http.StatusServiceUnavailable
	if rec := e.get(yogyakarta); rec.Code != http.StatusOK {
		t.Errorf("stale tile not served during outage: %d", rec.Code)
	}
	if n := e.up.hits.Load(); n != 2 {
		t.Errorf("stale tile not refreshed: %d hits", n)
	}
}

func TestConcurrentMissesShareOneFetch(t *testing.T) {
	e := newEnv(t)
	e.up.delay = 50 * time.Millisecond
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec := e.get(yogyakarta); rec.Code != http.StatusOK {
				t.Errorf("status %d", rec.Code)
			}
		}()
	}
	wg.Wait()
	if n := e.up.hits.Load(); n != 1 {
		t.Errorf("upstream hit %d times for one tile", n)
	}
}

func TestRejectsBadTilesAndUpstreamResponses(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/tiles/12/0/0.png", "/tiles/18/0/0.png", "/tiles/12/3303/2137.jpg", "/tiles/12/3303/2137"} {
		if rec := e.get(p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if n := e.up.hits.Load(); n != 0 {
		t.Errorf("invalid tiles reached upstream %d times", n)
	}

	e.up.body = []byte("<html>not an image</html>")
	if rec := e.get(yogyakarta); rec.Code != http.StatusBadGateway {
		t.Errorf("non-PNG upstream: %d", rec.Code)
	}
	e.up.body = append(append([]byte{}, png...), bytes.Repeat([]byte{0}, maxTileBytes)...)
	if rec := e.get(yogyakarta); rec.Code != http.StatusBadGateway {
		t.Errorf("oversized upstream: %d", rec.Code)
	}
	e.up.body, e.up.code = png, http.StatusNotFound
	if rec := e.get(yogyakarta); rec.Code != http.StatusNotFound {
		t.Errorf("upstream 404: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(e.srv.Dir, "12")); err == nil {
		t.Error("rejected responses were cached")
	}
}

func TestWithoutUpstreamServesOnlyLocalTiles(t *testing.T) {
	e := newEnv(t)
	e.srv.Upstream = ""
	if rec := e.get(yogyakarta); rec.Code != http.StatusNotFound {
		t.Errorf("missing tile: %d", rec.Code)
	}
	p := filepath.Join(e.srv.Dir, "12", "3303", "2137.png")
	os.MkdirAll(filepath.Dir(p), 0o750) //nolint:errcheck
	os.WriteFile(p, png, 0o640)         //nolint:errcheck
	e.clock = e.clock.Add(365 * 24 * time.Hour)
	if rec := e.get(yogyakarta); rec.Code != http.StatusOK {
		t.Errorf("pre-seeded tile: %d", rec.Code)
	}
	if n := e.up.hits.Load(); n != 0 {
		t.Errorf("upstream contacted although disabled")
	}
}

func TestMissLimit(t *testing.T) {
	e := newEnv(t)
	allowed := 1
	e.srv.MissAllowed = func(*http.Request) bool { allowed--; return allowed >= 0 }
	if rec := e.get(yogyakarta); rec.Code != http.StatusOK {
		t.Fatalf("first miss: %d", rec.Code)
	}
	if rec := e.get(yogyakarta); rec.Code != http.StatusOK {
		t.Errorf("cached tile must not count against the limit: %d", rec.Code)
	}
	rec := e.get("/tiles/12/3304/2137.png")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("limited miss: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

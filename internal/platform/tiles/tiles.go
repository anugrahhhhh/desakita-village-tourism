// Package tiles serves map tiles from this server (SPEC §14). Tiles live on disk under
// Dir/z/x/y.png. A missing or stale tile is fetched once from Upstream (if configured) and
// cached, so visitors' browsers only ever talk to us and the CSP stays 'self'.
package tiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"

	"desakita/internal/platform/geo"
)

const (
	// MaxZoom is the deepest zoom served; Leaflet maps are configured with the same limit.
	MaxZoom = 17
	// overviewZoom and below are served worldwide; deeper tiles only around Indonesia.
	overviewZoom = 4
	maxTileBytes = 1 << 20
)

var (
	errNotFound = errors.New("tiles: not found")
	errLimited  = errors.New("tiles: too many uncached tiles")
	pngMagic    = []byte("\x89PNG\r\n\x1a\n")
)

// Server serves and caches tiles.
type Server struct {
	// Dir holds cached (or pre-seeded) tiles.
	Dir string
	// Upstream is a URL template with {z}, {x} and {y}. Empty means serve only what is in Dir.
	Upstream string
	// UserAgent identifies this application to the upstream, as tile usage policies require.
	UserAgent string
	// MaxAge is how long a cached tile is used before it is refreshed from upstream.
	MaxAge time.Duration
	// MissAllowed, if set, rate-limits upstream fetches per request (e.g. per client IP).
	MissAllowed func(*http.Request) bool

	Client *http.Client
	Logger *slog.Logger
	Now    func() time.Time

	fetches singleflight.Group
	slots   chan struct{}
}

// New returns a Server with defaults: 7-day refresh, at most two concurrent upstream fetches.
func New(dir, upstream, userAgent string, logger *slog.Logger) *Server {
	return &Server{
		Dir: dir, Upstream: upstream, UserAgent: userAgent, MaxAge: 7 * 24 * time.Hour,
		Client: &http.Client{Timeout: 10 * time.Second}, Logger: logger, Now: time.Now,
		slots: make(chan struct{}, 2),
	}
}

// ParseTile validates z/x/y: plain decimal integers, z ≤ MaxZoom, x and y inside the grid,
// and (deeper than the overview zooms) inside Indonesia.
func ParseTile(zs, xs, ys string) (z, x, y int, ok bool) {
	num := func(s string) (int, bool) {
		if s == "" || len(s) > 6 || strings.TrimLeft(s, "0123456789") != "" {
			return 0, false
		}
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	var okZ, okX, okY bool
	z, okZ = num(zs)
	x, okX = num(xs)
	y, okY = num(ys)
	if !okZ || !okX || !okY || z > MaxZoom || x >= 1<<z || y >= 1<<z {
		return 0, 0, 0, false
	}
	if z > overviewZoom {
		minX, minY, maxX, maxY := geo.Indonesia.TileRange(z)
		if x < minX || x > maxX || y < minY || y > maxY {
			return 0, 0, 0, false
		}
	}
	return z, x, y, true
}

// ServeHTTP handles GET /tiles/{z}/{x}/{y}.png.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	z, x, y, ok := ParseTile(chi.URLParam(r, "z"), chi.URLParam(r, "x"), strings.TrimSuffix(chi.URLParam(r, "y"), ".png"))
	if !ok || !strings.HasSuffix(chi.URLParam(r, "y"), ".png") {
		http.NotFound(w, r)
		return
	}
	data, err := s.tile(r, z, x, y)
	switch {
	case errors.Is(err, errNotFound):
		w.Header().Set("Cache-Control", "public, max-age=300")
		http.NotFound(w, r)
		return
	case errors.Is(err, errLimited):
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	case err != nil:
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "tile unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		w.Write(data) //nolint:errcheck
	}
}

func (s *Server) path(z, x, y int) string {
	return filepath.Join(s.Dir, strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".png")
}

// tile returns a fresh cached tile, or fetches it. A stale tile is served when the upstream
// fails, so an outage degrades to old imagery rather than a blank map.
func (s *Server) tile(r *http.Request, z, x, y int) ([]byte, error) {
	p := s.path(z, x, y)
	cached, err := os.ReadFile(p)
	fresh := false
	if err == nil {
		if info, err := os.Stat(p); err == nil {
			fresh = s.Now().Sub(info.ModTime()) < s.MaxAge
		}
	}
	if fresh || (cached != nil && s.Upstream == "") {
		return cached, nil
	}
	if cached == nil && s.Upstream == "" {
		return nil, errNotFound
	}
	if s.MissAllowed != nil && !s.MissAllowed(r) {
		if cached != nil {
			return cached, nil
		}
		return nil, errLimited
	}
	v, err, _ := s.fetches.Do(p, func() (any, error) {
		// Detached from the request: one impatient client must not cancel a fetch that
		// other requests are waiting on.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		data, err := s.fetch(ctx, z, x, y)
		if err != nil {
			return nil, err
		}
		if err := s.store(p, data); err != nil {
			s.Logger.Warn("tile cache write failed", "z", z, "x", x, "y", y, "err", err)
		}
		return data, nil
	})
	if err != nil {
		if errors.Is(err, errNotFound) && cached == nil {
			return nil, errNotFound
		}
		s.Logger.Warn("tile fetch failed", "z", z, "x", x, "y", y, "err", err)
		if cached != nil {
			return cached, nil
		}
		return nil, err
	}
	return v.([]byte), nil
}

func (s *Server) fetch(ctx context.Context, z, x, y int) ([]byte, error) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	url := strings.NewReplacer("{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y)).Replace(s.Upstream)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("upstream read: %w", err)
	}
	if len(data) > maxTileBytes {
		return nil, errors.New("upstream tile too large")
	}
	// Only PNG goes into the cache and out under image/png, whatever the upstream claims.
	if !bytes.HasPrefix(data, pngMagic) {
		return nil, errors.New("upstream tile is not a PNG")
	}
	return data, nil
}

// store writes atomically so readers never see a partial tile.
func (s *Server) store(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tile-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

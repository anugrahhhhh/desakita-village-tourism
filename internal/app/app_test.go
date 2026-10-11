package app_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/app"
	"desakita/internal/platform/config"
	"desakita/internal/platform/dbtest"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _ := newApp(t)
	return srv
}

// newApp starts the full application on a fresh database and temp upload dir.
func newApp(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	return newAppWithClock(t, nil)
}

func newAppWithClock(t *testing.T, now func() time.Time) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	uploads := t.TempDir()
	cfg, err := config.Load(func(k string) string {
		switch k {
		case "DATABASE_URL":
			return "unused"
		case "UPLOAD_DIR":
			return uploads
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t)
	a, err := app.New(app.Options{Config: cfg, Pool: pool, Logger: slog.New(slog.DiscardHandler), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	return srv, pool
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, srv *httptest.Server) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: srv.URL, http: &http.Client{
		Jar: jar,
		// Inspect redirects instead of following them.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type response struct {
	code     int
	location string
	body     string
	header   http.Header
}

func (c *client) do(req *http.Request) response {
	c.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if strings.Contains(body, "⟦") {
		c.t.Errorf("%s %s: page contains a missing i18n key: %s", req.Method, req.URL.Path, regexp.MustCompile(`⟦[^⟧]*⟧`).FindString(body))
	}
	return response{code: resp.StatusCode, location: resp.Header.Get("Location"), body: body, header: resp.Header}
}

func (c *client) get(path string) response {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	return c.do(req)
}

var csrfField = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// csrfFrom loads page and returns its CSRF token.
func (c *client) csrfFrom(page string) string {
	c.t.Helper()
	m := csrfField.FindStringSubmatch(c.get(page).body)
	if m == nil {
		c.t.Fatalf("no csrf token on %s", page)
	}
	return m[1]
}

func (c *client) post(path string, vals url.Values) response {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

func (c *client) sessionCookie() *http.Cookie {
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "desakita_session" {
			return ck
		}
	}
	return nil
}

func (c *client) register(email string) {
	c.t.Helper()
	tok := c.csrfFrom("/daftar")
	r := c.post("/daftar", url.Values{"csrf_token": {tok}, "name": {"Sari"}, "email": {email}, "password": {"rahasia123"}})
	if r.code != http.StatusSeeOther {
		c.t.Fatalf("register: status %d\n%s", r.code, r.body)
	}
}

func TestRegisterLoginLogout(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)

	c.register("sari@example.com")
	if r := c.get("/akun"); r.code != http.StatusOK || !strings.Contains(r.body, "sari@example.com") {
		t.Fatalf("account page after register: %d", r.code)
	}

	tok := c.csrfFrom("/akun")
	if r := c.post("/keluar", url.Values{"csrf_token": {tok}}); r.code != http.StatusSeeOther {
		t.Fatalf("logout: %d", r.code)
	}
	if r := c.get("/akun"); r.code != http.StatusSeeOther {
		t.Fatalf("account after logout: %d, want redirect", r.code)
	}

	tok = c.csrfFrom("/masuk")
	r := c.post("/masuk", url.Values{"csrf_token": {tok}, "email": {"sari@example.com"}, "password": {"salah-sandi"}})
	if r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Email atau kata sandi salah") {
		t.Errorf("wrong password: %d", r.code)
	}

	before := c.sessionCookie().Value
	tok = c.csrfFrom("/masuk")
	r = c.post("/masuk", url.Values{"csrf_token": {tok}, "email": {"SARI@example.com"}, "password": {"rahasia123"}, "next": {"/akun"}})
	if r.code != http.StatusSeeOther || r.location != "/akun" {
		t.Fatalf("login: %d → %q", r.code, r.location)
	}
	if after := c.sessionCookie().Value; after == before {
		t.Error("session token was not renewed on login (fixation)")
	}
}

func TestLoginRedirectOnlyInternal(t *testing.T) {
	srv := newServer(t)
	newClient(t, srv).register("r@example.com")

	for next, want := range map[string]string{
		"/akun":             "/akun",
		"//evil.com":        "/",
		"https://evil.com/": "/",
		"/\\evil.com":       "/",
	} {
		c := newClient(t, srv)
		tok := c.csrfFrom("/masuk")
		r := c.post("/masuk", url.Values{"csrf_token": {tok}, "email": {"r@example.com"}, "password": {"rahasia123"}, "next": {next}})
		if r.location != want {
			t.Errorf("next=%q redirected to %q, want %q", next, r.location, want)
		}
	}
}

func TestProtectedPageRedirectsToLogin(t *testing.T) {
	c := newClient(t, newServer(t))
	r := c.get("/akun")
	if r.code != http.StatusSeeOther || r.location != "/masuk?next=%2Fakun" {
		t.Errorf("got %d → %q", r.code, r.location)
	}
}

func TestPostWithoutCSRFIsRejected(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	c.get("/masuk") // establish a session
	r := c.post("/daftar", url.Values{"name": {"X"}, "email": {"x@example.com"}, "password": {"rahasia123"}})
	if r.code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", r.code)
	}
	r = c.post("/daftar", url.Values{"csrf_token": {"forged"}, "name": {"X"}, "email": {"x@example.com"}, "password": {"rahasia123"}})
	if r.code != http.StatusForbidden {
		t.Errorf("forged token: status = %d, want 403", r.code)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	c := newClient(t, newServer(t))
	r := c.get("/masuk")
	var raw string
	for _, v := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(v, "desakita_session=") {
			raw = v
		}
	}
	if raw == "" {
		t.Fatal("no session cookie set")
	}
	for _, attr := range []string{"HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(raw, attr) {
			t.Errorf("cookie %q missing %s", raw, attr)
		}
	}
}

func TestDeleteAccountFlow(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	c.register("hapus@example.com")

	tok := c.csrfFrom("/akun")
	if r := c.post("/akun/hapus", url.Values{"csrf_token": {tok}, "password": {"salah"}}); r.code != http.StatusUnprocessableEntity {
		t.Errorf("wrong password: %d", r.code)
	}
	tok = c.csrfFrom("/akun")
	if r := c.post("/akun/hapus", url.Values{"csrf_token": {tok}, "password": {"rahasia123"}}); r.code != http.StatusSeeOther {
		t.Fatalf("delete: %d", r.code)
	}
	if r := c.get("/akun"); r.code != http.StatusSeeOther {
		t.Errorf("still logged in after deletion: %d", r.code)
	}
	tok = c.csrfFrom("/masuk")
	if r := c.post("/masuk", url.Values{"csrf_token": {tok}, "email": {"hapus@example.com"}, "password": {"rahasia123"}}); r.code != http.StatusUnprocessableEntity {
		t.Errorf("login after deletion: %d, want 422", r.code)
	}
}

func TestPagesRenderAndNotFound(t *testing.T) {
	c := newClient(t, newServer(t))
	for _, p := range []string{"/", "/masuk", "/daftar"} {
		if r := c.get(p); r.code != http.StatusOK {
			t.Errorf("GET %s = %d", p, r.code)
		}
	}
	if r := c.get("/tidak-ada"); r.code != http.StatusNotFound || !strings.Contains(r.body, "Halaman tidak ditemukan") {
		t.Errorf("404 page: %d", r.code)
	}
	if r := c.get("/static/app.css"); r.code != http.StatusOK {
		t.Errorf("static css: %d", r.code)
	}
}

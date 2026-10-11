package app_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (c *client) getWith(path string, header map[string]string) response {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return c.do(req)
}

func (c *client) switchLang(lang, back string) response {
	c.t.Helper()
	return c.post("/bahasa", url.Values{"csrf_token": {c.csrfFrom("/")}, "lang": {lang}, "back": {back}})
}

func TestLanguageNegotiationAndSwitch(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)

	r := c.get("/")
	if !strings.Contains(r.body, `<html lang="id">`) || r.header.Get("Content-Language") != "id" || !strings.Contains(r.body, "Temukan desa wisata") {
		t.Error("default page is not Indonesian")
	}
	r = c.getWith("/", map[string]string{"Accept-Language": "en-GB,en;q=0.9"})
	if !strings.Contains(r.body, `<html lang="en">`) || r.header.Get("Content-Language") != "en" || !strings.Contains(r.body, "Discover tourism villages") {
		t.Error("Accept-Language: en not honoured")
	}
	if r := c.getWith("/tidak-ada", map[string]string{"Accept-Language": "en"}); r.code != http.StatusNotFound || !strings.Contains(r.body, "Page not found") {
		t.Errorf("404 page not localized: %d", r.code)
	}

	// An explicit choice wins over the header and survives on the next requests.
	r = c.switchLang("en", "/peta?x=1")
	if r.code != http.StatusSeeOther || r.location != "/peta?x=1" {
		t.Fatalf("switch: %d %q", r.code, r.location)
	}
	var cookie string
	for _, sc := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "lang=en") {
			cookie = sc
		}
	}
	if cookie == "" || !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Lax") {
		t.Errorf("language cookie: %q", cookie)
	}
	if r := c.getWith("/peta", map[string]string{"Accept-Language": "id"}); !strings.Contains(r.body, "Tourism village map") {
		t.Error("cookie choice not applied")
	}
	if r := c.switchLang("id", "https://evil.example/"); r.location != "/" {
		t.Errorf("external return address followed: %q", r.location)
	}
	if r := c.switchLang("fr", "/"); r.code != http.StatusBadRequest {
		t.Errorf("unsupported language: %d", r.code)
	}
	if r := c.post("/bahasa", url.Values{"lang": {"en"}}); r.code != http.StatusForbidden {
		t.Errorf("switch without CSRF token: %d", r.code)
	}
}

func TestAccountLanguage(t *testing.T) {
	srv, pool := newApp(t)
	ctx := context.Background()
	locale := func(email string) string {
		var l string
		if err := pool.QueryRow(ctx, "SELECT locale FROM users WHERE email = $1", email).Scan(&l); err != nil {
			t.Fatal(err)
		}
		return l
	}

	// Signing up while browsing in English stores English on the account.
	en := newClient(t, srv)
	en.switchLang("en", "/")
	en.register("tourist@example.com")
	if l := locale("tourist@example.com"); l != "en" {
		t.Errorf("locale after English sign-up = %q", l)
	}

	// Switching while logged in updates the account, and a new device without the cookie
	// follows the account.
	id := newClient(t, srv)
	id.register("warga@example.com")
	if l := locale("warga@example.com"); l != "id" {
		t.Errorf("default locale = %q", l)
	}
	id.switchLang("en", "/")
	if l := locale("warga@example.com"); l != "en" {
		t.Errorf("locale after switch = %q", l)
	}
	other := newClient(t, srv)
	tok := other.csrfFrom("/masuk")
	other.post("/masuk", url.Values{"csrf_token": {tok}, "email": {"warga@example.com"}, "password": {"rahasia123"}})
	if r := other.get("/akun"); !strings.Contains(r.body, `<html lang="en">`) {
		t.Error("account language not used on a device without the cookie")
	}
}

func TestEmailsFollowRecipientLanguage(t *testing.T) {
	w := newBookingWorld(t)
	tourist := w.client(t)
	tourist.switchLang("en", "/")
	tourist.register("tourist@example.com")
	drainOutbox(t, w.pool)

	id, _ := tourist.book(w.pkg, dayISO(4), 1)
	if id == 0 {
		t.Fatal("booking failed")
	}
	got := drainOutbox(t, w.pool)
	tr := findMail(t, got, "booking.requested", "tourist@example.com")
	if !strings.Contains(tr.subject, "Your request was sent") || !strings.Contains(tr.body, "1 person") || !strings.Contains(tr.body, "Thursday, 14 March 2030") {
		t.Errorf("traveler email not in English:\n%s\n%s", tr.subject, tr.body)
	}
	m := findMail(t, got, "booking.requested", "pengelola@example.com")
	if !strings.Contains(m.subject, "Permintaan pemesanan baru") || !strings.Contains(m.body, "Kamis, 14 Maret 2030") {
		t.Errorf("manager email not in Indonesian:\n%s\n%s", m.subject, m.body)
	}
}

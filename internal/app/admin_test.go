package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func (w bookingWorld) admin(t *testing.T) *client {
	t.Helper()
	c := w.client(t)
	c.register("admin@example.com")
	if _, err := w.pool.Exec(context.Background(), "UPDATE users SET is_admin = true WHERE email = 'admin@example.com'"); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAdminAreaHiddenFromNonAdmins(t *testing.T) {
	w := newBookingWorld(t)
	for name, c := range map[string]*client{"anonymous": w.client(t), "manager": w.manager} {
		for _, p := range []string{"/admin", "/admin/laporan", fmt.Sprintf("/admin/desa/%d", w.village)} {
			if r := c.get(p); r.code != http.StatusNotFound {
				t.Errorf("%s GET %s: %d, want 404", name, p, r.code)
			}
		}
	}
	tok := w.manager.csrfFrom("/kelola")
	if r := w.manager.post(fmt.Sprintf("/admin/desa/%d/nonaktifkan", w.village), url.Values{"csrf_token": {tok}}); r.code != http.StatusNotFound {
		t.Errorf("manager suspends via admin endpoint: %d", r.code)
	}
}

func TestAdminVerifiesAndSuspends(t *testing.T) {
	w := newBookingWorld(t)
	ctx := context.Background()
	adm := w.admin(t)

	// A second, pending village registered by the manager.
	var district int64
	if err := w.pool.QueryRow(ctx, "SELECT id FROM regions WHERE kind = 'district' LIMIT 1").Scan(&district); err != nil {
		t.Fatal(err)
	}
	pending := w.manager.createVillage("Desa Baru Daftar", district)
	if r := adm.get("/admin"); !strings.Contains(r.body, "Desa Baru Daftar") {
		t.Error("pending village not in admin queue")
	}
	if r := adm.get(fmt.Sprintf("/admin/desa/%d", pending)); !strings.Contains(r.body, "pengelola@example.com") {
		t.Error("manager contact not shown to admin")
	}

	tok := adm.csrfFrom("/admin")
	adm.post(fmt.Sprintf("/admin/desa/%d/tolak", pending), url.Values{"csrf_token": {tok}})
	if r := adm.get(fmt.Sprintf("/admin/desa/%d", pending)); !strings.Contains(r.body, "Alasan wajib") {
		t.Error("reject without reason accepted")
	}
	tok = adm.csrfFrom("/admin")
	adm.post(fmt.Sprintf("/admin/desa/%d/verifikasi", pending), url.Values{"csrf_token": {tok}})
	if r := w.client(t).get("/desa/desa-baru-daftar"); r.code != http.StatusOK {
		t.Errorf("verified village not public: %d", r.code)
	}

	// Suspending the bookable village blocks new bookings but keeps existing ones.
	traveler := w.client(t)
	traveler.register("t@example.com")
	existing, _ := traveler.book(w.pkg, dayISO(5), 1)
	if existing == 0 {
		t.Fatal("booking failed")
	}
	tok = adm.csrfFrom("/admin")
	adm.post(fmt.Sprintf("/admin/desa/%d/nonaktifkan", w.village), url.Values{"csrf_token": {tok}, "note": {"laporan penipuan"}})
	if r := w.client(t).get(fmt.Sprintf("/pesan/paket/%d", w.pkg)); r.code != http.StatusNotFound {
		t.Errorf("suspended village booking page: %d", r.code)
	}
	tok = traveler.csrfFrom("/pemesanan")
	r := traveler.post(fmt.Sprintf("/pesan/paket/%d", w.pkg), url.Values{
		"csrf_token": {tok}, "start_date": {dayISO(6)}, "party_size": {"1"},
		"contact_name": {"T"}, "contact_phone": {"08123456789"},
	})
	if r.code != http.StatusNotFound {
		t.Errorf("booking a suspended village: %d", r.code)
	}
	if r := traveler.get("/pemesanan/" + strconv.FormatInt(existing, 10)); !strings.Contains(r.body, "Menunggu konfirmasi") {
		t.Error("existing booking affected by suspension")
	}
	if r := w.manager.get(fmt.Sprintf("/kelola/desa/%d", w.village)); !strings.Contains(r.body, "dinonaktifkan sementara") {
		t.Error("manager sees no suspension banner")
	}
}

func TestReportFlow(t *testing.T) {
	w := newBookingWorld(t)
	adm := w.admin(t)
	user := w.client(t)
	user.register("pelapor@example.com")

	page := "/desa/desa-pemesanan"
	if r := w.client(t).get(page); strings.Contains(r.body, `action="/lapor"`) {
		t.Error("report form shown to anonymous visitors")
	}
	tok := user.csrfFrom(page)
	r := user.post("/lapor", url.Values{"csrf_token": {tok}, "target_type": {"village"}, "target_id": {strconv.FormatInt(w.village, 10)}, "reason": {"Foto bukan milik desa"}, "back": {page}})
	if r.code != http.StatusSeeOther || r.location != page {
		t.Fatalf("report: %d → %q", r.code, r.location)
	}
	tok = user.csrfFrom(page)
	if r := user.post("/lapor", url.Values{"csrf_token": {tok}, "target_type": {"village"}, "target_id": {"999999"}, "reason": {"x"}, "back": {"//evil.com"}}); r.code != http.StatusNotFound {
		t.Errorf("report on missing target: %d", r.code)
	}

	list := adm.get("/admin/laporan")
	if !strings.Contains(list.body, "Foto bukan milik desa") || !strings.Contains(list.body, "pelapor@example.com") {
		t.Fatal("report not listed for admin")
	}
	var id int64
	if err := w.pool.QueryRow(context.Background(), "SELECT id FROM reports LIMIT 1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	tok = adm.csrfFrom("/admin/laporan")
	adm.post(fmt.Sprintf("/admin/laporan/%d/selesai", id), url.Values{"csrf_token": {tok}})
	if r := adm.get("/admin/laporan"); strings.Contains(r.body, "Foto bukan milik desa") {
		t.Error("resolved report still listed")
	}
}

func TestHomeSearch(t *testing.T) {
	w := newBookingWorld(t)
	c := w.client(t)
	if r := c.get("/"); r.code != http.StatusOK || !strings.Contains(r.body, "Menanam Padi") {
		t.Errorf("home does not list the package: %d", r.code)
	}
	if r := c.get("/?q=padi"); !strings.Contains(r.body, "Menanam Padi") {
		t.Error("text search misses the package")
	}
	if r := c.get("/?q=tidak-ada-yang-cocok"); strings.Contains(r.body, "Menanam Padi") || !strings.Contains(r.body, "Tidak ada paket yang cocok") {
		t.Error("non-matching search shows results")
	}
	if r := c.get("/?harga_maks=1000"); strings.Contains(r.body, "Menanam Padi") {
		t.Error("max price filter ignored")
	}
}

func TestLoginRateLimit(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	c.register("limit@example.com")
	attacker := newClient(t, srv)
	var last response
	for range 6 {
		tok := attacker.csrfFrom("/masuk")
		last = attacker.post("/masuk", url.Values{"csrf_token": {tok}, "email": {"limit@example.com"}, "password": {"salah-terus"}})
	}
	if last.code != http.StatusTooManyRequests || last.header.Get("Retry-After") == "" {
		t.Errorf("6th failed login: %d, want 429 with Retry-After", last.code)
	}
	// Another account from the same IP is limited separately (per IP+email).
	tok := attacker.csrfFrom("/masuk")
	if r := attacker.post("/masuk", url.Values{"csrf_token": {tok}, "email": {"other@example.com"}, "password": {"x"}}); r.code == http.StatusTooManyRequests {
		t.Error("different email throttled by another account's failures")
	}
}

func TestSpoofedForwardedForDoesNotBypassLimit(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	var last response
	for i := range 6 {
		tok := c.csrfFrom("/masuk")
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/masuk", strings.NewReader(url.Values{"csrf_token": {tok}, "email": {"x@example.com"}, "password": {"x"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i)) // TRUST_PROXY is off
		last = c.do(req)
	}
	if last.code != http.StatusTooManyRequests {
		t.Errorf("rotating X-Forwarded-For bypassed the limit: %d", last.code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	r := newClient(t, newServer(t)).get("/")
	for h, want := range map[string]string{
		"Content-Security-Policy": "default-src 'self'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
	} {
		if !strings.Contains(r.header.Get(h), want) {
			t.Errorf("%s = %q, want it to contain %q", h, r.header.Get(h), want)
		}
	}
	if r.header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS sent in development")
	}
}

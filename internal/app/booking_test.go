package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var jakarta, _ = time.LoadLocation("Asia/Jakarta")

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) Set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.t = t }

// The fixed "now" for booking HTTP tests: Sunday 2030-03-10 10:00 WIB.
var bookingNow = time.Date(2030, 3, 10, 10, 0, 0, 0, jakarta)

func dayISO(offset int) string {
	return time.Date(2030, 3, 10, 0, 0, 0, 0, time.UTC).AddDate(0, 0, offset).Format(time.DateOnly)
}

// bookingWorld is a verified village with one package, its manager, and a clock.
type bookingWorld struct {
	srv     *httptest.Server
	pool    *pgxpool.Pool
	clock   *testClock
	manager *client
	village int64
	pkg     int64
}

func newBookingWorld(t *testing.T) bookingWorld {
	t.Helper()
	clock := &testClock{t: bookingNow}
	srv, pool := newAppWithClock(t, clock.Now)
	district := seedDistrict(t, pool)
	manager := newClient(t, srv)
	manager.register("pengelola@example.com")
	vid := manager.createVillage("Desa Pemesanan", district)
	pid := manager.createPackage(vid, "Menanam Padi")
	if _, err := pool.Exec(context.Background(), `UPDATE villages SET status = 'verified' WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	return bookingWorld{srv: srv, pool: pool, clock: clock, manager: manager, village: vid, pkg: pid}
}

func (w bookingWorld) client(t *testing.T) *client { return newClient(t, w.srv) }

var bookingIDFromPath = regexp.MustCompile(`^/pemesanan/(\d+)$`)

func (c *client) book(pkg int64, date string, party int) (int64, response) {
	c.t.Helper()
	page := fmt.Sprintf("/pesan/paket/%d", pkg)
	tok := c.csrfFrom(page)
	r := c.post(page, url.Values{
		"csrf_token": {tok}, "start_date": {date}, "party_size": {strconv.Itoa(party)},
		"contact_name": {"Sari Wisatawan"}, "contact_phone": {"0812-3456-7890"}, "note": {"Datang pagi"},
	})
	m := bookingIDFromPath.FindStringSubmatch(r.location)
	if m == nil {
		return 0, r
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	return id, r
}

func (w bookingWorld) reserved(t *testing.T, date string) int {
	t.Helper()
	var n int
	err := w.pool.QueryRow(context.Background(), `SELECT coalesce((SELECT reserved FROM availability WHERE package_id = $1 AND date = $2), 0)`, w.pkg, date).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTravelerBookingFlow(t *testing.T) {
	w := newBookingWorld(t)
	page := fmt.Sprintf("/pesan/paket/%d", w.pkg)

	anon := w.client(t)
	r := anon.get(page)
	if r.code != http.StatusOK || !strings.Contains(r.body, "Masuk untuk memesan") || strings.Contains(r.body, `name="party_size"`) {
		t.Errorf("anonymous booking page: %d", r.code)
	}
	if !strings.Contains(r.body, `value="`+dayISO(2)+`"`) || strings.Contains(r.body, `value="`+dayISO(1)+`"`) {
		t.Error("calendar should offer the day after tomorrow but not tomorrow")
	}

	traveler := w.client(t)
	traveler.register("wisatawan@example.com")
	if r := traveler.get(page); !strings.Contains(r.body, `name="party_size"`) {
		t.Fatal("logged-in user sees no booking form")
	}

	tok := traveler.csrfFrom(page)
	bad := traveler.post(page, url.Values{"csrf_token": {tok}, "start_date": {dayISO(1)}, "party_size": {"2"}, "contact_name": {"S"}, "contact_phone": {"08123456789"}})
	if bad.code != http.StatusUnprocessableEntity {
		t.Errorf("booking tomorrow: %d, want 422", bad.code)
	}

	id, r := traveler.book(w.pkg, dayISO(5), 2)
	if id == 0 {
		t.Fatalf("booking failed: %d %q\n%s", r.code, r.location, r.body)
	}
	detail := traveler.get(fmt.Sprintf("/pemesanan/%d", id))
	if detail.code != http.StatusOK || !strings.Contains(detail.body, "Menunggu konfirmasi") || !strings.Contains(detail.body, "Rp 300.000") {
		t.Errorf("detail page: %d", detail.code)
	}
	if list := traveler.get("/pemesanan"); !strings.Contains(list.body, "Menanam Padi") {
		t.Error("my bookings does not list the booking")
	}
	if got := w.reserved(t, dayISO(5)); got != 2 {
		t.Errorf("reserved = %d, want 2", got)
	}

	tok = traveler.csrfFrom(fmt.Sprintf("/pemesanan/%d", id))
	if r := traveler.post(fmt.Sprintf("/pemesanan/%d/batal", id), url.Values{"csrf_token": {tok}}); r.code != http.StatusSeeOther {
		t.Fatalf("cancel: %d", r.code)
	}
	if got := w.reserved(t, dayISO(5)); got != 0 {
		t.Errorf("reserved after cancel = %d, want 0", got)
	}
	if r := traveler.get(fmt.Sprintf("/pemesanan/%d", id)); !strings.Contains(r.body, "Dibatalkan") {
		t.Error("cancelled status not shown")
	}
}

func TestBookingPageHiddenForUnbookablePackages(t *testing.T) {
	w := newBookingWorld(t)
	tok := w.manager.csrfFrom(fmt.Sprintf("/kelola/desa/%d/paket", w.village))
	w.manager.post(fmt.Sprintf("/kelola/desa/%d/paket/%d/status", w.village, w.pkg), url.Values{"csrf_token": {tok}, "active": {"0"}})
	if r := w.client(t).get(fmt.Sprintf("/pesan/paket/%d", w.pkg)); r.code != http.StatusNotFound {
		t.Errorf("inactive package booking page: %d", r.code)
	}
	if r := w.client(t).get("/pesan/paket/999999"); r.code != http.StatusNotFound {
		t.Errorf("unknown package: %d", r.code)
	}
}

var waLink = regexp.MustCompile(`href="(https://wa\.me/[^"]+)"`)

func TestManagerHandlesBooking(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.register("tamu@example.com")
	id, _ := traveler.book(w.pkg, dayISO(4), 3)
	if id == 0 {
		t.Fatal("booking failed")
	}

	base := fmt.Sprintf("/kelola/desa/%d/pemesanan", w.village)
	list := w.manager.get(base)
	if !strings.Contains(list.body, "Sari Wisatawan") || !strings.Contains(list.body, "Perlu ditanggapi") || !strings.Contains(list.body, "jam untuk menanggapi") {
		t.Errorf("dashboard missing pending booking")
	}
	panel := w.manager.get(base + "/panel")
	if panel.code != http.StatusOK || strings.Contains(panel.body, "<html") || !strings.Contains(panel.body, "Sari Wisatawan") {
		t.Errorf("panel fragment: %d", panel.code)
	}

	detail := w.manager.get(fmt.Sprintf("%s/%d", base, id))
	m := waLink.FindStringSubmatch(detail.body)
	if m == nil {
		t.Fatal("no WhatsApp link on manager detail")
	}
	link := strings.ReplaceAll(m[1], "&amp;", "&")
	if !strings.HasPrefix(link, "https://wa.me/6281234567890?text=") || strings.Contains(link, " ") {
		t.Errorf("WhatsApp link not normalized/encoded: %s", link)
	}

	tok := w.manager.csrfFrom(fmt.Sprintf("%s/%d", base, id))
	if r := w.manager.post(fmt.Sprintf("%s/%d/konfirmasi", base, id), url.Values{"csrf_token": {tok}}); r.code != http.StatusSeeOther {
		t.Fatalf("confirm: %d", r.code)
	}
	detail = w.manager.get(fmt.Sprintf("%s/%d", base, id))
	if m := waLink.FindStringSubmatch(detail.body); m == nil || !strings.Contains(m[1], "DIKONFIRMASI") {
		t.Error("WhatsApp message should switch to the confirmation text")
	}
	if strings.Contains(detail.body, "/konfirmasi") {
		t.Error("confirm button still offered after confirmation")
	}
	if r := traveler.get(fmt.Sprintf("/pemesanan/%d", id)); !strings.Contains(r.body, "Dikonfirmasi") {
		t.Error("traveler does not see confirmation")
	}

	// Confirming twice reports the changed status instead of failing hard.
	tok = w.manager.csrfFrom(fmt.Sprintf("%s/%d", base, id))
	w.manager.post(fmt.Sprintf("%s/%d/konfirmasi", base, id), url.Values{"csrf_token": {tok}})
	if r := w.manager.get(fmt.Sprintf("%s/%d", base, id)); !strings.Contains(r.body, "Status pemesanan sudah berubah") {
		t.Error("no flash for invalid transition")
	}

	// On the departure day the manager can mark it done.
	w.clock.Set(time.Date(2030, 3, 14, 9, 0, 0, 0, jakarta))
	tok = w.manager.csrfFrom(fmt.Sprintf("%s/%d", base, id))
	w.manager.post(fmt.Sprintf("%s/%d/selesai", base, id), url.Values{"csrf_token": {tok}})
	if r := traveler.get(fmt.Sprintf("/pemesanan/%d", id)); !strings.Contains(r.body, "Selesai") {
		t.Error("booking not completed")
	}
	if got := w.reserved(t, dayISO(4)); got != 3 {
		t.Errorf("completed booking released quota: reserved = %d", got)
	}
}

func TestBookingAccessOverHTTP(t *testing.T) {
	w := newBookingWorld(t)
	alice := w.client(t)
	alice.register("alice@example.com")
	id, _ := alice.book(w.pkg, dayISO(6), 1)
	if id == 0 {
		t.Fatal("booking failed")
	}

	bob := w.client(t)
	bob.register("bob@example.com")
	if r := bob.get(fmt.Sprintf("/pemesanan/%d", id)); r.code != http.StatusNotFound {
		t.Errorf("bob reads alice's booking: %d", r.code)
	}
	tok := bob.csrfFrom("/pemesanan")
	if r := bob.post(fmt.Sprintf("/pemesanan/%d/batal", id), url.Values{"csrf_token": {tok}}); r.code != http.StatusNotFound {
		t.Errorf("bob cancels alice's booking: %d", r.code)
	}

	// Bob runs another village and tries to reach village A's booking and calendar.
	var district int64
	if err := w.pool.QueryRow(context.Background(), `SELECT id FROM regions WHERE kind = 'district' LIMIT 1`).Scan(&district); err != nil {
		t.Fatal(err)
	}
	vidB := bob.createVillage("Desa Bob", district)
	pkgB := bob.createPackage(vidB, "Paket Bob")
	tok = bob.csrfFrom("/kelola")
	gets := []string{
		fmt.Sprintf("/kelola/desa/%d/pemesanan/%d", vidB, id),
		fmt.Sprintf("/kelola/desa/%d/pemesanan/%d", w.village, id),
		fmt.Sprintf("/kelola/desa/%d/pemesanan", w.village),
		fmt.Sprintf("/kelola/desa/%d/pemesanan/panel", w.village),
		fmt.Sprintf("/kelola/desa/%d/kalender/%d", vidB, w.pkg),
		fmt.Sprintf("/kelola/desa/%d/kalender/%d", w.village, w.pkg),
	}
	for _, p := range gets {
		if r := bob.get(p); r.code != http.StatusNotFound {
			t.Errorf("GET %s by other manager: %d, want 404", p, r.code)
		}
	}
	posts := []string{
		fmt.Sprintf("/kelola/desa/%d/pemesanan/%d/konfirmasi", vidB, id),
		fmt.Sprintf("/kelola/desa/%d/pemesanan/%d/tolak", vidB, id),
		fmt.Sprintf("/kelola/desa/%d/pemesanan/%d/batal", vidB, id),
	}
	for _, p := range posts {
		if r := bob.post(p, url.Values{"csrf_token": {tok}}); r.code != http.StatusNotFound {
			t.Errorf("POST %s by other manager: %d, want 404", p, r.code)
		}
	}
	if r := bob.post(fmt.Sprintf("/kelola/desa/%d/kalender/%d/kuota", vidB, w.pkg), url.Values{"csrf_token": {tok}, "date": {dayISO(6)}, "quota": {"0"}}); r.code != http.StatusNotFound {
		t.Errorf("quota change on another village's package: %d", r.code)
	}
	// Bob's own package under his own village works.
	if r := bob.get(fmt.Sprintf("/kelola/desa/%d/kalender/%d", vidB, pkgB)); r.code != http.StatusOK {
		t.Errorf("own calendar: %d", r.code)
	}

	var status string
	w.pool.QueryRow(context.Background(), `SELECT status FROM bookings WHERE id = $1`, id).Scan(&status) //nolint:errcheck
	if status != "pending" {
		t.Errorf("alice's booking changed to %s", status)
	}
}

func TestManagerCalendar(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.register("kal@example.com")
	if id, _ := traveler.book(w.pkg, dayISO(5), 4); id == 0 {
		t.Fatal("booking failed")
	}
	cal := fmt.Sprintf("/kelola/desa/%d/kalender/%d", w.village, w.pkg)
	if r := w.manager.get(cal + "?tanggal=" + dayISO(5)); !strings.Contains(r.body, "4 dari 10 orang") {
		t.Errorf("calendar day panel missing reservation info")
	}

	tok := w.manager.csrfFrom(cal)
	r := w.manager.post(cal+"/kuota", url.Values{"csrf_token": {tok}, "date": {dayISO(5)}, "quota": {"3"}})
	if r.code != http.StatusSeeOther {
		t.Fatalf("set quota: %d", r.code)
	}
	if page := w.manager.get(r.location); !strings.Contains(page.body, "tidak boleh lebih kecil") {
		t.Error("no error for quota below reserved")
	}

	tok = w.manager.csrfFrom(cal)
	w.manager.post(cal+"/tutup", url.Values{"csrf_token": {tok}, "date": {dayISO(7)}, "closed": {"1"}})
	if _, r := traveler.book(w.pkg, dayISO(7), 1); r.code != http.StatusUnprocessableEntity && r.code != http.StatusConflict {
		t.Errorf("booking a closed date: %d", r.code)
	}
}

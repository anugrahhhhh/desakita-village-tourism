package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// completedBooking makes the traveler book, the manager confirm, and (on the departure day)
// complete it. It returns the booking id.
func (w bookingWorld) completedBooking(t *testing.T, traveler *client) int64 {
	t.Helper()
	id, r := traveler.book(w.pkg, dayISO(4), 2)
	if id == 0 {
		t.Fatalf("booking failed: %d", r.code)
	}
	base := fmt.Sprintf("/kelola/desa/%d/pemesanan/%d", w.village, id)
	tok := w.manager.csrfFrom(base)
	w.manager.post(base+"/konfirmasi", url.Values{"csrf_token": {tok}})
	w.clock.Set(time.Date(2030, 3, 14, 9, 0, 0, 0, jakarta))
	tok = w.manager.csrfFrom(base)
	w.manager.post(base+"/selesai", url.Values{"csrf_token": {tok}})
	return id
}

func TestReviewJourney(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.register("pengulas@example.com")

	// Before completion there is no review form.
	id, _ := traveler.book(w.pkg, dayISO(6), 1)
	if r := traveler.get(fmt.Sprintf("/pemesanan/%d/ulasan", id)); !strings.Contains(r.body, "setelah pengelola menandai") || strings.Contains(r.body, `name="rating"`) {
		t.Error("review form offered before completion")
	}
	tok := traveler.csrfFrom(fmt.Sprintf("/pemesanan/%d/ulasan", id))
	if r := traveler.post(fmt.Sprintf("/pemesanan/%d/ulasan", id), url.Values{"csrf_token": {tok}, "rating": {"5"}, "body": {"Seru sekali dan pemandu ramah."}}); r.code != http.StatusSeeOther {
		t.Errorf("posting a review for a pending booking: %d", r.code)
	}

	done := w.completedBooking(t, traveler)
	page := fmt.Sprintf("/pemesanan/%d/ulasan", done)
	if r := traveler.get(fmt.Sprintf("/pemesanan/%d", done)); !strings.Contains(r.body, "Tulis ulasan") {
		t.Error("completed booking has no review link")
	}
	tok = traveler.csrfFrom(page)
	if r := traveler.post(page, url.Values{"csrf_token": {tok}, "rating": {"9"}, "body": {"pendek"}}); r.code != http.StatusUnprocessableEntity {
		t.Errorf("invalid review: %d", r.code)
	}
	tok = traveler.csrfFrom(page)
	if r := traveler.post(page, url.Values{"csrf_token": {tok}, "rating": {"5"}, "body": {"Belajar menanam padi bersama petani, seru sekali!"}}); r.code != http.StatusSeeOther {
		t.Fatalf("review: %d\n%s", r.code, r.body)
	}

	// Someone else can't open or post to that booking's review page.
	other := w.client(t)
	other.register("orang-lain@example.com")
	if r := other.get(page); r.code != http.StatusNotFound {
		t.Errorf("other user GET review page: %d", r.code)
	}

	// Public: package page shows the review and summary; the card shows the rating.
	anon := w.client(t)
	pkgPage := fmt.Sprintf("/desa/desa-pemesanan/paket/%d", w.pkg)
	r := anon.get(pkgPage)
	for _, want := range []string{"Ulasan wisatawan", "Belajar menanam padi bersama petani", "1 ulasan", "5,0"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("package page missing %q", want)
		}
	}
	if r := anon.get("/"); !strings.Contains(r.body, "(1 ulasan)") {
		t.Error("search card has no rating")
	}

	// Report the review, then the admin hides it.
	var reviewID int64
	if err := w.pool.QueryRow(context.Background(), "SELECT id FROM reviews WHERE booking_id = $1", done).Scan(&reviewID); err != nil {
		t.Fatal(err)
	}
	tok = other.csrfFrom(pkgPage)
	if r := other.post("/lapor", url.Values{"csrf_token": {tok}, "target_type": {"review"}, "target_id": {strconv.FormatInt(reviewID, 10)}, "reason": {"Isi tidak sesuai"}, "back": {pkgPage}}); r.code != http.StatusSeeOther {
		t.Errorf("report review: %d", r.code)
	}

	adm := w.admin(t)
	if r := adm.get("/admin/ulasan?dilaporkan=1"); !strings.Contains(r.body, "Belajar menanam padi") || !strings.Contains(r.body, "1 laporan terbuka") {
		t.Error("reported review not in moderation list")
	}
	tok = adm.csrfFrom("/admin/ulasan")
	adm.post(fmt.Sprintf("/admin/ulasan/%d/sembunyikan", reviewID), url.Values{"csrf_token": {tok}, "note": {"Dicek: menyalahi panduan"}})
	if r := anon.get(pkgPage); strings.Contains(r.body, "Belajar menanam padi bersama petani") {
		t.Error("hidden review still public")
	}
	if r := traveler.get(page); !strings.Contains(r.body, "Dicek: menyalahi panduan") {
		t.Error("author doesn't see why the review was hidden")
	}
	if l := auditList(adm.get("/admin/audit?target=review").body); !strings.Contains(l, "Ulasan disembunyikan") {
		t.Error("hide not in audit log")
	}
	if r := adm.get("/admin/laporan"); !strings.Contains(r.body, "Ulasan untuk") {
		t.Error("review report not labelled in reports")
	}

	// Non-admins can't reach moderation.
	for _, c := range []*client{anon, traveler, w.manager} {
		if r := c.get("/admin/ulasan"); r.code != http.StatusNotFound {
			t.Errorf("moderation page visible to non-admin: %d", r.code)
		}
	}
}

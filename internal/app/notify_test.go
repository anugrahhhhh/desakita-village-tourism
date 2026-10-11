package app_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/platform/i18n"
	"desakita/internal/platform/mail"
	"desakita/web"
)

type queued struct{ kind, to, subject, body string }

// drainOutbox returns queued emails in insertion order and empties the table.
func drainOutbox(t *testing.T, pool *pgxpool.Pool) []queued {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, "SELECT kind, to_address, subject, body FROM email_outbox ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []queued
	for rows.Next() {
		var q queued
		if err := rows.Scan(&q.kind, &q.to, &q.subject, &q.body); err != nil {
			t.Fatal(err)
		}
		out = append(out, q)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM email_outbox"); err != nil {
		t.Fatal(err)
	}
	return out
}

func findMail(t *testing.T, all []queued, kind, to string) queued {
	t.Helper()
	for _, q := range all {
		if q.kind == kind && q.to == to {
			return q
		}
	}
	t.Fatalf("no %s email to %s in %v", kind, to, all)
	return queued{}
}

func TestBookingEmailsAreQueued(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.register("tamu@example.com")
	drainOutbox(t, w.pool)

	id, _ := traveler.book(w.pkg, dayISO(4), 3)
	if id == 0 {
		t.Fatal("booking failed")
	}
	got := drainOutbox(t, w.pool)
	if len(got) != 2 {
		t.Fatalf("queued %d emails, want manager + traveler: %v", len(got), got)
	}
	m := findMail(t, got, "booking.requested", "pengelola@example.com")
	managerLink := fmt.Sprintf("http://localhost:8080/kelola/desa/%d/pemesanan/%d", w.village, id)
	for _, want := range []string{"Menanam Padi", "Desa Pemesanan", "Sari Wisatawan", "3 orang", "Rp", managerLink} {
		if !strings.Contains(m.subject+m.body, want) {
			t.Errorf("manager email lacks %q:\n%s\n%s", want, m.subject, m.body)
		}
	}
	tr := findMail(t, got, "booking.requested", "tamu@example.com")
	if !strings.Contains(tr.body, fmt.Sprintf("http://localhost:8080/pemesanan/%d", id)) {
		t.Errorf("traveler email lacks booking link:\n%s", tr.body)
	}
	for _, q := range got {
		// Privacy: the phone number and the traveler's note never go into email.
		if strings.Contains(q.body, "0812") || strings.Contains(q.body, "Datang pagi") {
			t.Errorf("%s email to %s contains private data:\n%s", q.kind, q.to, q.body)
		}
	}

	base := fmt.Sprintf("/kelola/desa/%d/pemesanan/%d", w.village, id)
	tok := w.manager.csrfFrom(base)
	w.manager.post(base+"/konfirmasi", url.Values{"csrf_token": {tok}})
	got = drainOutbox(t, w.pool)
	if len(got) != 1 || got[0].kind != "booking.confirmed" || got[0].to != "tamu@example.com" {
		t.Fatalf("after confirm: %v", got)
	}
	// A repeated confirm is an invalid transition and queues nothing.
	tok = w.manager.csrfFrom(base)
	w.manager.post(base+"/konfirmasi", url.Values{"csrf_token": {tok}})
	if got := drainOutbox(t, w.pool); len(got) != 0 {
		t.Errorf("invalid transition queued %v", got)
	}

	w.clock.Set(time.Date(2030, 3, 14, 9, 0, 0, 0, jakarta))
	tok = w.manager.csrfFrom(base)
	w.manager.post(base+"/selesai", url.Values{"csrf_token": {tok}})
	done := findMail(t, drainOutbox(t, w.pool), "booking.completed", "tamu@example.com")
	if !strings.Contains(done.body, fmt.Sprintf("/pemesanan/%d/ulasan", id)) {
		t.Errorf("completed email lacks review link:\n%s", done.body)
	}
}

func TestRejectAndCancelEmails(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.register("tamu@example.com")

	rejected, _ := traveler.book(w.pkg, dayISO(4), 1)
	cancelled, _ := traveler.book(w.pkg, dayISO(5), 1)
	drainOutbox(t, w.pool)

	base := fmt.Sprintf("/kelola/desa/%d/pemesanan/%d", w.village, rejected)
	tok := w.manager.csrfFrom(base)
	w.manager.post(base+"/tolak", url.Values{"csrf_token": {tok}, "note": {"Kuota hari itu dipakai rombongan sekolah"}})
	r := findMail(t, drainOutbox(t, w.pool), "booking.rejected", "tamu@example.com")
	if !strings.Contains(r.body, "Kuota hari itu dipakai rombongan sekolah") {
		t.Errorf("rejection email lacks the manager's reason:\n%s", r.body)
	}

	page := fmt.Sprintf("/pemesanan/%d", cancelled)
	tok = traveler.csrfFrom(page)
	traveler.post(page+"/batal", url.Values{"csrf_token": {tok}})
	got := drainOutbox(t, w.pool)
	if len(got) != 1 || got[0].kind != "booking.cancelled_by_traveler" || got[0].to != "pengelola@example.com" {
		t.Errorf("after traveler cancel: %v", got)
	}
}

func TestVillageStatusEmails(t *testing.T) {
	w := newBookingWorld(t)
	adm := w.admin(t)
	drainOutbox(t, w.pool)

	tok := adm.csrfFrom("/admin")
	adm.post(fmt.Sprintf("/admin/desa/%d/nonaktifkan", w.village), url.Values{"csrf_token": {tok}, "note": {"laporan penipuan"}})
	s := findMail(t, drainOutbox(t, w.pool), "village.suspended", "pengelola@example.com")
	if !strings.Contains(s.body, "laporan penipuan") || !strings.Contains(s.body, "Desa Pemesanan") {
		t.Errorf("suspension email:\n%s", s.body)
	}
	tok = adm.csrfFrom("/admin")
	adm.post(fmt.Sprintf("/admin/desa/%d/aktifkan", w.village), url.Values{"csrf_token": {tok}})
	tok = adm.csrfFrom("/admin")
	adm.post(fmt.Sprintf("/admin/desa/%d/nonaktifkan", w.village), url.Values{"csrf_token": {tok}, "note": {"lagi"}})
	got := drainOutbox(t, w.pool)
	if len(got) != 2 || got[0].kind != "village.reinstated" || got[1].kind != "village.suspended" {
		t.Errorf("repeated status changes must each notify: %v", got)
	}
}

func TestDeletedAccountGetsNoQueuedMail(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.register("hapus@example.com")
	if id, _ := traveler.book(w.pkg, dayISO(4), 1); id == 0 {
		t.Fatal("booking failed")
	}
	tok := traveler.csrfFrom("/akun")
	traveler.post("/akun/hapus", url.Values{"csrf_token": {tok}, "password": {"rahasia123"}})
	all := drainOutbox(t, w.pool)
	for _, q := range all {
		if q.to == "hapus@example.com" {
			t.Errorf("unsent %s email kept for deleted account", q.kind)
		}
	}
	if len(all) == 0 {
		t.Error("manager's copy of the request should stay queued")
	}
}

// Every template renders with complete data, so a field-name typo fails here rather than
// in the worker.
func TestEmailTemplatesRender(t *testing.T) {
	for _, lang := range i18n.Supported {
		tm, err := mail.LoadTemplates(web.FS, lang)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(tm.Names()); n != 12 {
			t.Errorf("%s: loaded %d templates, want 12", lang, n)
		}
		renderAll(t, tm)
	}
}

func renderAll(t *testing.T, tm *mail.Templates) {
	t.Helper()
	type full struct {
		RecipientName, TravelerName, PackageName, VillageName, Date, Total, Note, RespondBy, Link, PublicLink string
		PartySize                                                                                             int32
	}
	data := full{"Budi", "Sari", "Menanam Padi", "Desa Uji", "Kamis, 14 Maret 2030", "Rp 300.000", "", "Selasa, 12 Maret 2030 00:00 WIB", "https://x/1", "https://x/desa/uji", 3}
	for _, name := range tm.Names() {
		for _, note := range []string{"", "alasan"} {
			data.Note = note
			subject, body, err := tm.Render(name, data)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if subject == "" || strings.ContainsAny(subject, "\r\n") || !strings.Contains(body, "https://x/") {
				t.Errorf("%s: subject %q, body:\n%s", name, subject, body)
			}
		}
	}
}

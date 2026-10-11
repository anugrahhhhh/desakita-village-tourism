package app_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestManagerStatsPage(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.register("tamu@example.com")
	w.completedBooking(t, traveler) // 2 people, completed 2030-03-14

	path := fmt.Sprintf("/kelola/desa/%d/statistik", w.village)
	if r := w.manager.get(fmt.Sprintf("/kelola/desa/%d", w.village)); !strings.Contains(r.body, `href="`+path+`"`) {
		t.Error("overview does not link to statistics")
	}
	body := page(t, w.manager, path)
	for _, want := range []string{"Statistik", "Permintaan masuk", `aria-current="page"`, "90 hari terakhir", `role="img"`, "stats-mark",
		"Maret 2030: 2 pengunjung", "Tampilkan sebagai tabel", "Menanam Padi"} {
		if !strings.Contains(body, want) {
			t.Errorf("stats page lacks %q", want)
		}
	}
	if r := w.manager.get(path + "?periode=30"); !strings.Contains(r.body, `aria-current="page">30 hari terakhir`) {
		t.Error("period 30 not selected")
	}
	if r := w.manager.get(path + "?periode=7"); !strings.Contains(r.body, `aria-current="page">90 hari terakhir`) {
		t.Error("unknown period should fall back to 90 days")
	}

	// English, and access control.
	w.manager.switchLang("en", path)
	if r := w.manager.get(path); !strings.Contains(r.body, "Requests received") || !strings.Contains(r.body, "March 2030: 2 visitors") {
		t.Error("stats page not in English")
	}
	for name, c := range map[string]*client{"traveler": traveler, "anonymous": w.client(t)} {
		r := c.get(path)
		if name == "anonymous" && r.code != http.StatusSeeOther {
			t.Errorf("anonymous: %d, want redirect to login", r.code)
		}
		if name == "traveler" && r.code != http.StatusNotFound {
			t.Errorf("traveler: %d, want 404", r.code)
		}
	}
}

// Every main page renders in English without missing keys (client.do fails on ⟦key⟧).
func TestPagesRenderInEnglish(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.switchLang("en", "/")
	traveler.register("tourist@example.com")
	id := w.completedBooking(t, traveler)
	w.manager.switchLang("en", "/")
	adm := w.admin(t)
	adm.switchLang("en", "/")

	v := fmt.Sprintf("/kelola/desa/%d", w.village)
	pages := map[*client][]string{
		traveler: {"/", "/?q=padi", "/peta", "/desa/desa-pemesanan", fmt.Sprintf("/desa/desa-pemesanan/paket/%d", w.pkg),
			fmt.Sprintf("/pesan/paket/%d", w.pkg), "/pemesanan", fmt.Sprintf("/pemesanan/%d", id), fmt.Sprintf("/pemesanan/%d/ulasan", id), "/akun"},
		w.manager: {"/kelola", v, v + "/profil", v + "/foto", v + "/paket", v + "/pemesanan", v + "/statistik", fmt.Sprintf("%s/pemesanan/%d", v, id),
			fmt.Sprintf("%s/kalender/%d", v, w.pkg), "/kelola/desa/baru"},
		adm: {"/admin", "/admin/desa", "/admin/laporan", "/admin/audit", "/admin/ulasan"},
	}
	for c, paths := range pages {
		for _, p := range paths {
			r := c.get(p)
			if r.code != http.StatusOK {
				t.Errorf("GET %s: %d", p, r.code)
				continue
			}
			if !strings.Contains(r.body, `<html lang="en">`) {
				t.Errorf("GET %s is not English", p)
			}
		}
	}
	for _, p := range []string{"/masuk", "/daftar"} {
		if r := w.client(t).getWith(p, map[string]string{"Accept-Language": "en"}); !strings.Contains(r.body, `<html lang="en">`) {
			t.Errorf("GET %s is not English", p)
		}
	}
}

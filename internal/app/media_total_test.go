package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The total shown in the form is display-only: the server recomputes total_price_idr from
// the package price in the database at submission time and ignores any posted total.
func TestBookingTotalComputedOnServer(t *testing.T) {
	w := newBookingWorld(t)
	traveler := w.client(t)
	traveler.register("total@example.com")

	page := fmt.Sprintf("/pesan/paket/%d", w.pkg)
	tok := traveler.csrfFrom(page) // form rendered at Rp 150.000/orang

	// The manager raises the price after the traveler opened the form.
	if _, err := w.pool.Exec(context.Background(), "UPDATE tour_packages SET price_per_person_idr = 175000 WHERE id = $1", w.pkg); err != nil {
		t.Fatal(err)
	}
	r := traveler.post(page, url.Values{
		"csrf_token": {tok}, "start_date": {dayISO(5)}, "party_size": {"2"},
		"contact_name": {"Sari"}, "contact_phone": {"08123456789"},
		// Forged client-side values must be ignored.
		"total_price_idr": {"1"}, "total": {"1"}, "price_per_person_idr": {"1"},
	})
	if r.code != http.StatusSeeOther {
		t.Fatalf("booking: %d\n%s", r.code, r.body)
	}
	var total int64
	if err := w.pool.QueryRow(context.Background(), "SELECT total_price_idr FROM bookings WHERE package_id = $1", w.pkg).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 350000 {
		t.Errorf("total_price_idr = %d, want 350000 (2 × current DB price)", total)
	}
}

var imgTag = regexp.MustCompile(`<img\b[^>]*>`)

func attr(tag, name string) (string, bool) {
	m := regexp.MustCompile(`\b` + name + `="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Every image carries alt text (caption or village/package name), width, height and lazy loading.
func TestImagesCarryAltAndDimensions(t *testing.T) {
	w := newBookingWorld(t)
	vPhotos := fmt.Sprintf("/kelola/desa/%d/foto", w.village)
	pPhotos := fmt.Sprintf("/kelola/desa/%d/paket/%d/foto", w.village, w.pkg)

	upload := func(path, caption string) {
		t.Helper()
		r := w.manager.uploadWithCaption(path, w.manager.csrfFrom(path), pngImage(t), caption)
		if r.code != http.StatusSeeOther {
			t.Fatalf("upload to %s: %d\n%s", path, r.code, r.body)
		}
	}
	upload(vPhotos, "Sawah terasering saat pagi")
	upload(pPhotos, "")

	anon := w.client(t)
	pages := map[string]string{
		"/desa/desa-pemesanan":                              "Sawah terasering saat pagi",
		fmt.Sprintf("/desa/desa-pemesanan/paket/%d", w.pkg): "Menanam Padi",
		"/": "Menanam Padi",
	}
	for page, wantAlt := range pages {
		body := anon.get(page).body
		tags := imgTag.FindAllString(body, -1)
		if len(tags) == 0 {
			t.Errorf("%s: no images rendered", page)
			continue
		}
		foundAlt := false
		for _, tag := range tags {
			alt, ok := attr(tag, "alt")
			if !ok || alt == "" {
				t.Errorf("%s: image without alt text: %s", page, tag)
			}
			foundAlt = foundAlt || alt == wantAlt
			for _, a := range []string{"width", "height"} {
				if v, ok := attr(tag, a); !ok || v == "" || v == "0" {
					t.Errorf("%s: image missing %s: %s", page, a, tag)
				}
			}
			if v, _ := attr(tag, "loading"); v != "lazy" {
				t.Errorf("%s: image not lazy-loaded: %s", page, tag)
			}
		}
		if !foundAlt {
			t.Errorf("%s: no image with alt %q", page, wantAlt)
		}
	}

	// Caption longer than 200 characters is rejected with a message.
	r := w.manager.uploadWithCaption(vPhotos, w.manager.csrfFrom(vPhotos), pngImage(t), strings.Repeat("x", 201))
	if r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "maksimal 200 karakter") {
		t.Errorf("long caption: %d", r.code)
	}
}

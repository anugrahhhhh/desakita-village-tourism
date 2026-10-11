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

func TestVillageLocationAndMap(t *testing.T) {
	w := newBookingWorld(t)
	ctx := context.Background()
	var district int64
	if err := w.pool.QueryRow(ctx, "SELECT district_id FROM villages WHERE id = $1", w.village).Scan(&district); err != nil {
		t.Fatal(err)
	}
	visitor := w.client(t)

	// Without a location: no map on the village page and nothing on /peta.
	if r := visitor.get("/desa/desa-pemesanan"); strings.Contains(r.body, "leaflet.js") || strings.Contains(r.body, `data-map=`) {
		t.Error("map assets loaded for a village without a location")
	}
	if r := visitor.get("/peta"); r.code != http.StatusOK || !strings.Contains(r.body, "Belum ada desa terverifikasi") {
		t.Errorf("empty map page: %d", r.code)
	}

	profile := fmt.Sprintf("/kelola/desa/%d/profil", w.village)
	form := page(t, w.manager, profile)
	if !strings.Contains(form, `data-map="picker"`) || !strings.Contains(form, `name="lat"`) || !strings.Contains(form, "/static/leaflet/leaflet.js") {
		t.Fatal("profile form has no location picker")
	}
	save := func(lat, lng string) response {
		return w.manager.post(profile, url.Values{
			"csrf_token": {w.manager.csrfFrom(profile)}, "name": {"Desa Pemesanan"}, "district_id": {strconv.FormatInt(district, 10)},
			"contact_phone": {"0812-3456-7890"}, "lat": {lat}, "lng": {lng},
		})
	}
	if r := save("48.85", "2.35"); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "di luar wilayah Indonesia") {
		t.Errorf("coordinates outside Indonesia: %d", r.code)
	}
	if r := save("-7,797068", "110.370529"); r.code != http.StatusSeeOther {
		t.Fatalf("save location: %d", r.code)
	}
	if form := page(t, w.manager, profile); !strings.Contains(form, `value="-7.797068"`) {
		t.Error("saved latitude not shown in the form")
	}

	v := page(t, visitor, "/desa/desa-pemesanan")
	for _, want := range []string{`data-map="point"`, `data-lat="-7.797068"`, `data-lng="110.370529"`, `data-tiles="/tiles/{z}/{x}/{y}.png"`,
		"/static/leaflet/leaflet.css", "/static/map.js", "openstreetmap.org/directions?to=-7.797068%2C110.370529", "OpenStreetMap"} {
		if !strings.Contains(v, want) {
			t.Errorf("village page lacks %q", want)
		}
	}
	m := page(t, visitor, "/peta")
	for _, want := range []string{`data-map="list"`, `href="/desa/desa-pemesanan"`, `data-lat="-7.797068"`, `href="/peta"`} {
		if !strings.Contains(m, want) {
			t.Errorf("map page lacks %q", want)
		}
	}

	// Pending villages never appear on the public map, even with a location.
	if _, err := w.pool.Exec(ctx, "UPDATE villages SET status = 'pending' WHERE id = $1", w.village); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page(t, visitor, "/peta"), "desa-pemesanan") {
		t.Error("pending village on the public map")
	}
}

func TestTileRoute(t *testing.T) {
	srv := newServer(t)
	for _, p := range []string{"/tiles/12/0/0.png", "/tiles/99/0/0.png", "/tiles/a/b/c.png"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
		if resp.Header.Get("Set-Cookie") != "" {
			t.Errorf("%s sets a session cookie", p)
		}
	}
}

func page(t *testing.T, c *client, path string) string {
	t.Helper()
	r := c.get(path)
	if r.code != http.StatusOK {
		t.Fatalf("GET %s: %d", path, r.code)
	}
	return r.body
}

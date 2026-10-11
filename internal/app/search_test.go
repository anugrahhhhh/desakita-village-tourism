package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestHomeSearchSortAndTotal(t *testing.T) {
	w := newBookingWorld(t)
	ctx := context.Background()
	if _, err := w.pool.Exec(ctx, `INSERT INTO tour_packages (village_id, name, description, price_per_person_idr, duration_days, default_quota)
		VALUES ($1, 'Menginap Rumah Petani', 'Bermalam dan ikut menanam padi pagi hari', 400000, 2, 5)`, w.village); err != nil {
		t.Fatal(err)
	}
	c := w.client(t)

	r := c.get("/")
	if !strings.Contains(r.body, "2 paket ditemukan") || !strings.Contains(r.body, `name="urut"`) {
		t.Errorf("home: total or sort menu missing")
	}
	if strings.Contains(r.body, `value="relevan"`) {
		t.Error("relevance offered without a query")
	}
	if i, j := strings.Index(r.body, ">Menanam Padi</h3>"), strings.Index(r.body, ">Menginap Rumah Petani</h3>"); i < 0 || j < 0 || i > j {
		t.Error("default order should be cheapest first")
	}

	r = c.get("/?urut=termahal")
	if i, j := strings.Index(r.body, ">Menanam Padi</h3>"), strings.Index(r.body, ">Menginap Rumah Petani</h3>"); i < 0 || j < 0 || j > i {
		t.Error("termahal should list the expensive package first")
	}

	// "padi" is in one name and one description: both found, the name match ranks first.
	r = c.get("/?q=padi")
	if !strings.Contains(r.body, "2 paket ditemukan") || !strings.Contains(r.body, `value="relevan" selected`) {
		t.Error("query search: total or default relevance missing")
	}
	if i, j := strings.Index(r.body, ">Menanam Padi</h3>"), strings.Index(r.body, ">Menginap Rumah Petani</h3>"); i < 0 || j < 0 || i > j {
		t.Error("name match should rank above description match")
	}

	if r := c.get("/?q=padi&urut=';drop"); r.code != http.StatusOK || !strings.Contains(r.body, `value="relevan" selected`) {
		t.Errorf("invalid sort: %d", r.code)
	}
	if r := c.get(`/?q="ikut+menanam"`); !strings.Contains(r.body, "1 paket ditemukan") {
		t.Error("phrase search failed")
	}
	if r := c.get("/?q=tidakada"); !strings.Contains(r.body, "0 paket ditemukan") || strings.Contains(r.body, `name="urut"`) {
		t.Error("empty result: count or hidden sort menu wrong")
	}
}

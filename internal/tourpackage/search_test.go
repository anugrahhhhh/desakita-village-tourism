package tourpackage_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"desakita/internal/tourpackage"
)

func TestSearch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mk := func(village int64, name, price string, activity int64) int64 {
		in := e.input(name)
		in.Price = price
		in.ActivityTypeIDs = []string{strconv.FormatInt(activity, 10)}
		id, err := e.svc.Create(ctx, village, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	cheap := mk(e.villageA, "Membatik 100% Alami", "50000", e.activities[1])
	mid := mk(e.villageA, "Tracking Sawah", "150000", e.activities[0])
	inactive := mk(e.villageA, "Paket Lama", "10000", e.activities[0])
	if err := e.svc.SetActive(ctx, e.villageA, inactive, false); err != nil {
		t.Fatal(err)
	}
	mk(e.villageB, "Desa B Tidak Terverifikasi", "20000", e.activities[0])
	if _, err := e.pool.Exec(ctx, "UPDATE villages SET status = 'verified' WHERE id = $1", e.villageA); err != nil {
		t.Fatal(err)
	}

	ids := func(f tourpackage.SearchFilter) []int64 {
		t.Helper()
		page, err := e.svc.Search(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, r := range page.Results {
			out = append(out, r.ID)
		}
		return out
	}
	eq := func(name string, got []int64, want ...int64) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	act0 := strconv.FormatInt(e.activities[0], 10)
	var regency int64
	if err := e.pool.QueryRow(ctx, `SELECT d.parent_id FROM villages v JOIN regions d ON d.id = v.district_id WHERE v.id = $1`, e.villageA).Scan(&regency); err != nil {
		t.Fatal(err)
	}

	eq("no filter (verified + active only, cheapest first)", ids(tourpackage.SearchFilter{}), cheap, mid)
	eq("activity", ids(tourpackage.SearchFilter{ActivityType: act0}), mid)
	eq("max price", ids(tourpackage.SearchFilter{MaxPrice: "Rp 100.000"}), cheap)
	eq("min price", ids(tourpackage.SearchFilter{MinPrice: "100000"}), mid)
	eq("regency", ids(tourpackage.SearchFilter{Regency: strconv.FormatInt(regency, 10)}), cheap, mid)
	eq("unknown regency", ids(tourpackage.SearchFilter{Regency: "999999"}))
	eq("text matches package", ids(tourpackage.SearchFilter{Query: "sawah"}), mid)
	eq("text matches village", ids(tourpackage.SearchFilter{Query: "desa a"}), cheap, mid)
	eq("percent sign is literal", ids(tourpackage.SearchFilter{Query: "100%"}), cheap)
	eq("underscore is literal", ids(tourpackage.SearchFilter{Query: "_"}))
	eq("garbage filters ignored", ids(tourpackage.SearchFilter{Regency: "x", MinPrice: "abc", Page: "-3"}), cheap, mid)
}

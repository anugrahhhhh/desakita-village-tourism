package tourpackage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/upload"
	"desakita/internal/tourpackage"
	"desakita/internal/village"
)

func TestFullTextSearch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mk := func(village int64, name, desc, price string) int64 {
		t.Helper()
		in := e.input(name)
		in.Description, in.Price = desc, price
		id, err := e.svc.Create(ctx, village, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	padi := mk(e.villageA, "Menanam Padi", "Turun ke sawah bersama petani, termasuk caping dan sepatu bot.", "150000")
	batik := mk(e.villageA, "Membatik Tulis", "Membuat sapu tangan batik di sanggar warga.", "85000")
	homestay := mk(e.villageA, "Menginap di Rumah Warga", "Dua hari satu malam, sarapan nasi dan jalan pagi ke sawah.", "350000")
	sawahDesc := mk(e.villageA, "Jelajah Kampung", "Bersepeda melewati sawah terasering dan kebun kopi.", "60000")
	if _, err := e.pool.Exec(ctx, "UPDATE villages SET status = 'verified', description = 'Desa penghasil kopi robusta' WHERE id = $1", e.villageA); err != nil {
		t.Fatal(err)
	}

	search := func(f tourpackage.SearchFilter) tourpackage.SearchPage {
		t.Helper()
		page, err := e.svc.Search(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	ids := func(p tourpackage.SearchPage) []int64 {
		var out []int64
		for _, r := range p.Results {
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
	q := func(s string) tourpackage.SearchFilter {
		return tourpackage.SearchFilter{Query: s, Sort: tourpackage.SortPriceAsc}
	}

	eq("word only in a description", ids(search(q("caping"))), padi)
	eq("all words must match", ids(search(q("sawah petani"))), padi)
	eq("quoted phrase", ids(search(q(`"jalan pagi"`))), homestay)
	eq("exclusion", ids(search(q("sawah -petani"))), sawahDesc, homestay)
	eq("either word with or", ids(search(q("caping or sanggar"))), batik, padi)
	eq("partial word in a name (trigram)", ids(search(q("atik"))), batik)
	eq("case-insensitive", ids(search(q("MENANAM"))), padi)
	eq("matches the village description", ids(search(q("robusta"))), sawahDesc, batik, padi, homestay)
	eq("no match", ids(search(q("snorkeling"))))

	// Relevance: a match in the package name outranks a match in a description.
	rel := search(tourpackage.SearchFilter{Query: "sawah"})
	if rel.Sort != tourpackage.SortRelevance {
		t.Errorf("default sort with a query = %q, want relevance", rel.Sort)
	}
	if len(rel.Results) != 3 {
		t.Fatalf("sawah: %d results", len(rel.Results))
	}
	if got := ids(search(tourpackage.SearchFilter{Query: "menanam padi"})); len(got) == 0 || got[0] != padi {
		t.Errorf("exact name should rank first, got %v", got)
	}

	// Sort orders and totals.
	all := tourpackage.SearchFilter{}
	if p := search(all); p.Sort != tourpackage.SortPriceAsc || p.Total != 4 {
		t.Errorf("default without query: sort=%q total=%d", p.Sort, p.Total)
	}
	eq("price descending", ids(search(tourpackage.SearchFilter{Sort: tourpackage.SortPriceDesc})), homestay, padi, batik, sawahDesc)
	eq("newest first", ids(search(tourpackage.SearchFilter{Sort: tourpackage.SortNewest})), sawahDesc, homestay, batik, padi)
	if p := search(tourpackage.SearchFilter{Sort: tourpackage.SortRelevance}); p.Sort != tourpackage.SortPriceAsc {
		t.Errorf("relevance without a query should fall back to price, got %q", p.Sort)
	}
	if p := search(tourpackage.SearchFilter{Sort: "drop table"}); p.Sort != tourpackage.SortPriceAsc {
		t.Errorf("invalid sort accepted: %q", p.Sort)
	}
}

func TestSearchPaginationTotal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range 30 {
		in := e.input(fmt.Sprintf("Paket Sawah %02d", i))
		in.Price = strconv.Itoa(10000 + i*1000)
		if _, err := e.svc.Create(ctx, e.villageA, in); err != nil {
			t.Fatal(err)
		}
	}
	e.pool.Exec(ctx, "UPDATE villages SET status = 'verified' WHERE id = $1", e.villageA) //nolint:errcheck

	p1, _ := e.svc.Search(ctx, tourpackage.SearchFilter{Query: "sawah"})
	p2, _ := e.svc.Search(ctx, tourpackage.SearchFilter{Query: "sawah", Page: "2"})
	if p1.Total != 30 || len(p1.Results) != tourpackage.SearchPageSize || !p1.HasNext {
		t.Errorf("page 1: total=%d len=%d next=%v", p1.Total, len(p1.Results), p1.HasNext)
	}
	if p2.Total != 30 || len(p2.Results) != 30-tourpackage.SearchPageSize || p2.HasNext {
		t.Errorf("page 2: total=%d len=%d next=%v", p2.Total, len(p2.Results), p2.HasNext)
	}
}

// sqlRecorder captures the SQL and arguments of the last query run through the pool.
type sqlRecorder struct {
	mu   sync.Mutex
	sql  string
	args []any
}

func (r *sqlRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.Contains(d.SQL, "name: SearchPackages") && !strings.HasPrefix(d.SQL, "EXPLAIN") {
		r.sql, r.args = d.SQL, d.Args
	}
	return ctx
}

func (r *sqlRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// The real search query, on 30000 packages, uses the GIN full-text and trigram
// indexes rather than scanning: the plan is checked with EXPLAIN on the captured SQL.
func TestSearchUsesIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("bulk data")
	}
	ctx := context.Background()
	url := dbtest.NewURL(t, true)
	rec := &sqlRecorder{}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = rec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// 3000 verified villages × 10 packages, plus a few distinctive rows to find.
	_, err = pool.Exec(ctx, `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', 'P') RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'R' FROM p RETURNING id),
		     d AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'D' FROM r RETURNING id)
		INSERT INTO villages (slug, name, description, district_id, contact_phone, status)
		SELECT 'desa-' || g, 'Desa Nomor ' || g, 'Desa agraris nomor ' || g || ' dengan sawah dan kebun', d.id, '0811', 'verified'
		FROM d, generate_series(1, 3000) g;
		INSERT INTO tour_packages (village_id, name, description, price_per_person_idr, duration_days, default_quota)
		SELECT v.id, 'Paket Kegiatan ' || v.id || '-' || g, 'Kegiatan harian warga nomor ' || g, 50000 + g * 1000, 1, 10
		FROM villages v, generate_series(1, 10) g;
		UPDATE tour_packages SET name = 'Belajar Membatik Tulis', description = 'Sanggar batik' WHERE id = 12345;
		UPDATE villages SET name = 'Kampung Anyaman', description = 'Pengrajin anyaman bambu' WHERE id = 77;
		ANALYZE;`)
	if err != nil {
		t.Fatal(err)
	}

	store, _ := upload.NewLocalStore(t.TempDir())
	logger := slog.New(slog.DiscardHandler)
	svc := tourpackage.NewService(pool, village.NewService(pool, store, logger), store, logger)

	start := time.Now()
	page, err := svc.Search(ctx, tourpackage.SearchFilter{Query: "batik"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("search over 30000 packages: %d result(s) in %v", page.Total, time.Since(start))
	if page.Total != 1 || page.Results[0].ID != 12345 {
		t.Fatalf("expected the one batik package, got total=%d", page.Total)
	}

	// explain returns the JSON plan of the last SearchPackages query the service ran.
	explain := func() string {
		t.Helper()
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		rec.mu.Lock()
		sql, args := rec.sql, rec.args
		rec.mu.Unlock()
		var plan []map[string]any
		if err := conn.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql, args...).Scan(&plan); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(plan)
		return string(b)
	}

	// 1. Package-side text matching: the planner chooses both GIN indexes on its own.
	natural := explain()
	for _, idx := range []string{"tour_packages_search_idx", "tour_packages_name_trgm_idx"} {
		if !strings.Contains(natural, idx) {
			t.Errorf("planner did not choose %s for a text search on 30000 packages", idx)
		}
	}
	if t.Failed() {
		t.Logf("plan: %s", natural)
	}

	// 2. Village-side matching can use the village GIN indexes. With only 3000 villages a
	// sequential scan is genuinely cheaper (and chosen), so usability is checked directly
	// with sequential scans disabled, on the same condition the search query uses.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	conn.Exec(ctx, "SET enable_seqscan = off") //nolint:errcheck
	var plan []map[string]any
	err = conn.QueryRow(ctx, `EXPLAIN (FORMAT JSON) SELECT v.id FROM villages v
		WHERE v.search_vector @@ websearch_to_tsquery('simple', $1) OR v.name ILIKE $2`, "anyaman", "%anyaman%").Scan(&plan)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plan)
	for _, idx := range []string{"villages_search_idx", "villages_name_trgm_idx"} {
		if !strings.Contains(string(b), idx) {
			t.Errorf("village condition cannot use %s: %s", idx, b)
		}
	}

	// And a village-name search returns that village's packages.
	page, err = svc.Search(ctx, tourpackage.SearchFilter{Query: "anyaman"})
	if err != nil || page.Total != 10 {
		t.Errorf("village-name match: total=%d err=%v, want the village's 10 packages", page.Total, err)
	}
}

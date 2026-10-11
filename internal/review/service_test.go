package review_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/platform/audit"
	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/form"
	"desakita/internal/review"
)

// sqlBookings implements review.Bookings with plain SQL, mirroring the real adapter's
// contract (FOR SHARE, ErrNotFound vs ErrNotEligible).
type sqlBookings struct{ pool *pgxpool.Pool }

func (s sqlBookings) Summary(ctx context.Context, bookingID, travelerID int64) (review.BookingInfo, error) {
	var b review.BookingInfo
	err := s.pool.QueryRow(ctx, `SELECT b.id, b.package_id, b.village_id, b.status, p.name, v.name, v.slug, b.start_date
		FROM bookings b JOIN tour_packages p ON p.id = b.package_id JOIN villages v ON v.id = b.village_id
		WHERE b.id = $1 AND b.traveler_id = $2`, bookingID, travelerID).
		Scan(&b.ID, &b.PackageID, &b.VillageID, &b.Status, &b.PackageName, &b.VillageName, &b.VillageSlug, &b.StartDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, review.ErrNotFound
	}
	return b, err
}

func (s sqlBookings) ForReview(ctx context.Context, tx pgx.Tx, bookingID, travelerID int64) (review.BookingInfo, error) {
	var b review.BookingInfo
	err := tx.QueryRow(ctx, `SELECT id, package_id, village_id FROM bookings
		WHERE id = $1 AND traveler_id = $2 AND status = 'completed' FOR SHARE`, bookingID, travelerID).
		Scan(&b.ID, &b.PackageID, &b.VillageID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, serr := s.Summary(ctx, bookingID, travelerID); serr != nil {
			return b, serr
		}
		return b, review.ErrNotEligible
	}
	return b, err
}

type env struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	svc     *review.Service
	pkg     int64
	village int64
	admin   int64
	userSeq int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := dbtest.New(t)
	e := &env{t: t, ctx: context.Background(), pool: pool, svc: review.NewService(pool, sqlBookings{pool})}
	e.admin = e.user("Admin Moderasi")
	e.must(pool.QueryRow(e.ctx, `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', 'P') RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'R' FROM p RETURNING id),
		     d AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'D' FROM r RETURNING id)
		INSERT INTO villages (slug, name, district_id, contact_phone, status) SELECT 'desa-r', 'Desa R', id, '0811', 'verified' FROM d RETURNING id`).Scan(&e.village))
	e.must(pool.QueryRow(e.ctx, `INSERT INTO tour_packages (village_id, name, price_per_person_idr, duration_days, default_quota)
		VALUES ($1, 'Paket R', 100000, 1, 10) RETURNING id`, e.village).Scan(&e.pkg))
	return e
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) user(name string) int64 {
	e.t.Helper()
	e.userSeq++
	var id int64
	e.must(e.pool.QueryRow(e.ctx, "INSERT INTO users (email, password_hash, name) VALUES ($1, 'x', $2) RETURNING id",
		fmt.Sprintf("u%d@example.com", e.userSeq), name).Scan(&id))
	return id
}

// booking inserts a booking for traveler with the given status.
func (e *env) booking(traveler int64, status string) int64 {
	e.t.Helper()
	var id int64
	e.must(e.pool.QueryRow(e.ctx, `INSERT INTO bookings (package_id, village_id, traveler_id, start_date, party_size, total_price_idr,
			contact_name, contact_phone, expires_at, status, completed_at, cancelled_at, cancelled_by)
		VALUES ($1, $2, $3, '2030-01-01', 1, 100000, 'X', '0811', now(), $4,
			CASE WHEN $4 = 'completed' THEN now() END,
			CASE WHEN $4 = 'cancelled' THEN now() END,
			CASE WHEN $4 = 'cancelled' THEN 'traveler' END)
		RETURNING id`, e.pkg, e.village, traveler, status).Scan(&id))
	return id
}

func in(rating, body string) review.Input { return review.Input{Rating: rating, Body: body} }

const goodBody = "Pengalaman menanam padi yang menyenangkan."

func TestOnlyOwnCompletedBookingsCanBeReviewed(t *testing.T) {
	e := newEnv(t)
	author, other := e.user("Sari Wisatawan"), e.user("Budi")
	completed := e.booking(author, "completed")

	for _, status := range []string{"pending", "confirmed", "cancelled", "rejected", "expired"} {
		b := e.booking(author, status)
		if err := e.svc.Create(e.ctx, author, b, in("5", goodBody)); !errors.Is(err, review.ErrNotEligible) {
			t.Errorf("%s booking: %v, want ErrNotEligible", status, err)
		}
	}
	othersBooking := e.booking(other, "completed")
	if err := e.svc.Create(e.ctx, author, othersBooking, in("5", goodBody)); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("someone else's booking: %v, want ErrNotFound", err)
	}
	if err := e.svc.Create(e.ctx, author, completed, in("5", goodBody)); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Create(e.ctx, author, completed, in("4", goodBody)); !errors.Is(err, review.ErrAlreadyReviewed) {
		t.Errorf("second review: %v, want ErrAlreadyReviewed", err)
	}
	if _, own, err := e.svc.ForBooking(e.ctx, author, completed); err != nil || own == nil || own.Rating != 5 {
		t.Errorf("ForBooking: %+v %v", own, err)
	}
	if _, _, err := e.svc.ForBooking(e.ctx, other, completed); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("ForBooking by another user: %v", err)
	}
}

func TestConcurrentSubmissionsCreateOneReview(t *testing.T) {
	e := newEnv(t)
	author := e.user("Sari")
	b := e.booking(author, "completed")

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.svc.Create(e.ctx, author, b, in("5", goodBody))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, review.ErrAlreadyReviewed):
			t.Errorf("unexpected error: %v", err)
		}
	}
	var n int
	e.must(e.pool.QueryRow(e.ctx, "SELECT count(*) FROM reviews WHERE booking_id = $1", b).Scan(&n))
	if ok != 1 || n != 1 {
		t.Errorf("successes = %d, rows = %d; want 1 and 1", ok, n)
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	author := e.user("Sari")
	b := e.booking(author, "completed")
	cases := map[string]struct {
		input review.Input
		field string
	}{
		"zero stars": {in("0", goodBody), "rating"},
		"six stars":  {in("6", goodBody), "rating"},
		"not number": {in("lima", goodBody), "rating"},
		"short body": {in("5", "  bagus  "), "body"},
		"long body":  {in("5", strings.Repeat("a", 2001)), "body"},
	}
	for name, tc := range cases {
		var verr *form.ValidationError
		if err := e.svc.Create(e.ctx, author, b, tc.input); !errors.As(err, &verr) || verr.Fields[tc.field] == "" {
			t.Errorf("%s: %v, want error on %s", name, err, tc.field)
		}
	}
}

func TestSummaryListingAndAnonymisation(t *testing.T) {
	e := newEnv(t)
	sari, budi, ani := e.user("Sari Wisatawan"), e.user("budi"), e.user("Ani Lestari Putri")
	for traveler, rating := range map[int64]string{sari: "5", budi: "4", ani: "3"} {
		e.must(e.svc.Create(e.ctx, traveler, e.booking(traveler, "completed"), in(rating, goodBody)))
	}

	s, err := e.svc.PackageSummary(e.ctx, e.pkg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count != 3 || math.Abs(s.Average-4) > 1e-9 || s.ByRating != [5]int64{0, 0, 1, 1, 1} || s.Share(5) != 33 {
		t.Errorf("summary = %+v share5=%d", s, s.Share(5))
	}

	list, next, err := e.svc.PackageReviews(e.ctx, e.pkg, 1)
	if err != nil || len(list) != 3 || next {
		t.Fatalf("list: %d next=%v err=%v", len(list), next, err)
	}
	names := map[string]bool{}
	for _, r := range list {
		names[r.Author] = true
	}
	for _, want := range []string{"Sari W.", "budi", "Ani P."} {
		if !names[want] {
			t.Errorf("missing display name %q in %v", want, names)
		}
	}

	// Deleting an author's account keeps the review but drops the name.
	e.must(func() error { _, err := e.pool.Exec(e.ctx, "DELETE FROM users WHERE id = $1", sari); return err }())
	list, _, _ = e.svc.PackageReviews(e.ctx, e.pkg, 1)
	anon := 0
	for _, r := range list {
		if r.Author == "" {
			anon++
		}
	}
	if len(list) != 3 || anon != 1 {
		t.Errorf("after account deletion: %d reviews, %d anonymous", len(list), anon)
	}
}

func TestPagination(t *testing.T) {
	e := newEnv(t)
	for i := range 12 {
		u := e.user(fmt.Sprintf("Wisatawan %d", i))
		e.must(e.svc.Create(e.ctx, u, e.booking(u, "completed"), in("4", goodBody)))
	}
	p1, next1, _ := e.svc.PackageReviews(e.ctx, e.pkg, 1)
	p2, next2, _ := e.svc.PackageReviews(e.ctx, e.pkg, 2)
	if len(p1) != review.PageSize || !next1 || len(p2) != 2 || next2 {
		t.Errorf("page1=%d next=%v page2=%d next=%v", len(p1), next1, len(p2), next2)
	}
}

func TestModerationIsAudited(t *testing.T) {
	e := newEnv(t)
	author, reporter := e.user("Sari"), e.user("Pelapor")
	b := e.booking(author, "completed")
	e.must(e.svc.Create(e.ctx, author, b, in("1", "Pengelola kasar dan tidak ramah sama sekali.")))
	_, own, _ := e.svc.ForBooking(e.ctx, author, b)
	e.must(func() error {
		_, err := e.pool.Exec(e.ctx, "INSERT INTO reports (reporter_id, target_type, target_id, reason) VALUES ($1, 'review', $2, 'Menghina')", reporter, own.ID)
		return err
	}())

	reported, _ := e.svc.ForModeration(e.ctx, review.StatusPublished, true)
	if len(reported) != 1 || reported[0].OpenReports != 1 {
		t.Fatalf("reported list: %+v", reported)
	}

	var verr *form.ValidationError
	if err := e.svc.Hide(e.ctx, e.admin, own.ID, " "); !errors.As(err, &verr) {
		t.Errorf("hide without reason: %v", err)
	}
	e.must(e.svc.Hide(e.ctx, e.admin, own.ID, "Berisi kata kasar"))
	if err := e.svc.Hide(e.ctx, e.admin, own.ID, "lagi"); !errors.Is(err, review.ErrInvalidStatusChange) {
		t.Errorf("hiding twice: %v", err)
	}
	if s, _ := e.svc.PackageSummary(e.ctx, e.pkg); s.Count != 0 {
		t.Errorf("hidden review still counted: %+v", s)
	}
	if list, _, _ := e.svc.PackageReviews(e.ctx, e.pkg, 1); len(list) != 0 {
		t.Error("hidden review still listed")
	}
	_, own, _ = e.svc.ForBooking(e.ctx, author, b)
	if own.Status != review.StatusHidden || own.ModerationNote != "Berisi kata kasar" {
		t.Errorf("author view after hide: %+v", own)
	}

	e.must(e.svc.Restore(e.ctx, e.admin, own.ID))
	if s, _ := e.svc.PackageSummary(e.ctx, e.pkg); s.Count != 1 {
		t.Error("restored review not counted")
	}
	if err := e.svc.Restore(e.ctx, e.admin, 999999); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("unknown review: %v", err)
	}

	items, _ := audit.List(e.ctx, e.pool, audit.Filter{TargetType: audit.TargetReview})
	if len(items) != 2 || items[0].Action != audit.ReviewRestore || items[1].Action != audit.ReviewHide || items[1].Note != "Berisi kata kasar" {
		t.Errorf("audit entries: %+v", items)
	}
}

func TestAuthorDeletesOwnReviewOnly(t *testing.T) {
	e := newEnv(t)
	author, other := e.user("Sari"), e.user("Budi")
	b := e.booking(author, "completed")
	e.must(e.svc.Create(e.ctx, author, b, in("5", goodBody)))
	if err := e.svc.Delete(e.ctx, other, b); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("other user deletes: %v", err)
	}
	e.must(e.svc.Delete(e.ctx, author, b))
	if err := e.svc.Delete(e.ctx, author, b); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// Deleting frees the booking for a new review.
	e.must(e.svc.Create(e.ctx, author, b, in("4", goodBody)))
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{"Sari Wisatawan": "Sari W.", "budi": "budi", "  ani  lestari  putri ": "ani P.", "": "", "Éka ägustin": "Éka Ä."} {
		if got := review.DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

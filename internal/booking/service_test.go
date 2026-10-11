package booking_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/booking"
	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/form"
)

var jakarta = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		panic(err)
	}
	return loc
}()

// clock is a settable time source shared by the service under test.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// sqlPackages implements booking.PackageLookup with the same locking contract as the real
// adapter (FOR SHARE on package and village).
type sqlPackages struct{}

func (sqlPackages) LookupForBooking(ctx context.Context, tx pgx.Tx, id int64) (booking.PackageInfo, error) {
	var p booking.PackageInfo
	var status string
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.village_id, p.price_per_person_idr, p.default_quota, p.is_active, v.status
		FROM tour_packages p JOIN villages v ON v.id = p.village_id
		WHERE p.id = $1 FOR SHARE OF p, v`, id).
		Scan(&p.ID, &p.VillageID, &p.PricePerPersonIDR, &p.DefaultQuota, &p.Active, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, booking.ErrNotFound
	}
	p.VillageVerified = status == "verified"
	return p, err
}

type sqlVillages struct{ pool *pgxpool.Pool }

func (s sqlVillages) IsManager(ctx context.Context, userID, villageID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM village_members WHERE village_id = $1 AND user_id = $2)`, villageID, userID).Scan(&ok)
	return ok, err
}

type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	svc   *booking.Service
	clock *clock
	ctx   context.Context
}

// today is 2030-03-10 in Jakarta; bookable start dates are 03-12 … 06-08.
var startTime = time.Date(2030, 3, 10, 10, 0, 0, 0, jakarta)

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := dbtest.New(t)
	c := &clock{t: startTime}
	svc := booking.NewService(pool, sqlPackages{}, sqlVillages{pool}, booking.Config{
		PendingTTL: 48 * time.Hour, Location: jakarta, Now: c.Now,
	})
	return &env{t: t, pool: pool, svc: svc, clock: c, ctx: context.Background()}
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) id(sql string, args ...any) int64 {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

var userSeq int

func (e *env) user() int64 {
	e.t.Helper()
	userSeq++
	return e.id(`INSERT INTO users (email, password_hash, name) VALUES ($1, 'x', 'U') RETURNING id`, fmt.Sprintf("u%d-%d@example.com", userSeq, time.Now().UnixNano()))
}

// village creates a village with the given status managed by manager.
func (e *env) village(status string, manager int64) int64 {
	e.t.Helper()
	district := e.id(`
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', $1) RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'R' FROM p RETURNING id)
		INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'D' FROM r RETURNING id`, fmt.Sprintf("P%d", time.Now().UnixNano()))
	v := e.id(`INSERT INTO villages (slug, name, district_id, contact_phone, status) VALUES ($1, 'V', $2, '0811', $3) RETURNING id`,
		fmt.Sprintf("v%d", time.Now().UnixNano()), district, status)
	e.exec(`INSERT INTO village_members (village_id, user_id) VALUES ($1, $2)`, v, manager)
	return v
}

func (e *env) pkg(village int64, quota int, active bool) int64 {
	e.t.Helper()
	return e.id(`INSERT INTO tour_packages (village_id, name, price_per_person_idr, duration_days, default_quota, is_active)
		VALUES ($1, 'P', 100000, 1, $2, $3) RETURNING id`, village, quota, active)
}

// fixture is a verified village with one active package and its manager.
type fixture struct{ manager, village, pkg int64 }

func (e *env) fixture(quota int) fixture {
	m := e.user()
	v := e.village("verified", m)
	return fixture{manager: m, village: v, pkg: e.pkg(v, quota, true)}
}

func day(offset int) string {
	return booking.Date(2030, 3, 10).AddDate(0, 0, offset).Format(time.DateOnly)
}

func input(pkg int64, date string, party int) booking.CreateInput {
	return booking.CreateInput{PackageID: pkg, StartDate: date, PartySize: fmt.Sprint(party), ContactName: "Sari", ContactPhone: "0812-3456-7890"}
}

func (e *env) book(traveler, pkg int64, date string, party int) booking.Booking {
	e.t.Helper()
	b, err := e.svc.Create(e.ctx, traveler, input(pkg, date, party))
	if err != nil {
		e.t.Fatalf("create booking: %v", err)
	}
	return b
}

func (e *env) reserved(pkg int64, date string) int {
	e.t.Helper()
	var n int
	err := e.pool.QueryRow(e.ctx, `SELECT reserved FROM availability WHERE package_id = $1 AND date = $2`, pkg, date).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) status(id int64) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(e.ctx, `SELECT status FROM bookings WHERE id = $1`, id).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// ---------------------------------------------------------------------------------------
// Creating requests

func TestCreateReservesQuotaAndSnapshotsPrice(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(10)
	traveler := e.user()

	b := e.book(traveler, f.pkg, day(5), 3)
	if b.Status != booking.StatusPending || b.TotalPriceIDR != 300000 || b.VillageID != f.village || b.PartySize != 3 {
		t.Errorf("unexpected booking %+v", b)
	}
	if got := e.reserved(f.pkg, day(5)); got != 3 {
		t.Errorf("reserved = %d, want 3", got)
	}
	// Price changes later don't touch the snapshot.
	e.exec(`UPDATE tour_packages SET price_per_person_idr = 1 WHERE id = $1`, f.pkg)
	got, _ := e.svc.ForTraveler(e.ctx, traveler, b.ID)
	if got.TotalPriceIDR != 300000 {
		t.Errorf("total changed to %d", got.TotalPriceIDR)
	}
}

func TestCreateDateWindow(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(100)
	cases := map[string]struct {
		offset int
		ok     bool
	}{
		"yesterday": {-1, false}, "today": {0, false}, "tomorrow": {1, false},
		"day after tomorrow": {2, true}, "+90": {90, true}, "+91": {91, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(tc.offset), 1))
			if tc.ok && err != nil {
				t.Errorf("err = %v, want success", err)
			}
			if !tc.ok && !errors.Is(err, booking.ErrDateOutOfWindow) {
				t.Errorf("err = %v, want ErrDateOutOfWindow", err)
			}
		})
	}

	// "Today" follows Jakarta, not UTC: at 06:00 WIB on 03-11 it is still 03-10 in UTC.
	e.clock.Set(time.Date(2030, 3, 11, 6, 0, 0, 0, jakarta))
	if _, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(2), 1)); !errors.Is(err, booking.ErrDateOutOfWindow) {
		t.Errorf("03-12 booked on 03-11 WIB: %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(10)
	in := booking.CreateInput{PackageID: f.pkg, StartDate: "12-03-2030", PartySize: "0", ContactName: "", ContactPhone: "abc"}
	_, err := e.svc.Create(e.ctx, e.user(), in)
	var verr *form.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v", err)
	}
	for _, field := range []string{"start_date", "party_size", "contact_name", "contact_phone"} {
		if verr.Fields[field] == "" {
			t.Errorf("no error for %s", field)
		}
	}
}

func TestCreateRejectsUnbookable(t *testing.T) {
	e := newEnv(t)
	m := e.user()
	pending := e.village("pending", m)
	verified := e.village("verified", m)
	cases := map[string]struct {
		pkg  int64
		want error
	}{
		"unverified village": {e.pkg(pending, 10, true), booking.ErrNotBookable},
		"inactive package":   {e.pkg(verified, 10, false), booking.ErrNotBookable},
		"unknown package":    {999999, booking.ErrNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := e.svc.Create(e.ctx, e.user(), input(tc.pkg, day(5), 1)); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestExpiresAt(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(10)
	e.clock.Set(time.Date(2030, 3, 10, 23, 0, 0, 0, jakarta))

	// Departure 03-12: start of day is 25h away, sooner than now+48h.
	b := e.book(e.user(), f.pkg, day(2), 1)
	if want := time.Date(2030, 3, 12, 0, 0, 0, 0, jakarta); !b.ExpiresAt.Equal(want) {
		t.Errorf("near departure: expires_at = %v, want %v", b.ExpiresAt.In(jakarta), want)
	}
	// Departure 03-20: now+48h comes first.
	b = e.book(e.user(), f.pkg, day(10), 1)
	if want := time.Date(2030, 3, 12, 23, 0, 0, 0, jakarta); !b.ExpiresAt.Equal(want) {
		t.Errorf("far departure: expires_at = %v, want %v", b.ExpiresAt.In(jakarta), want)
	}
}

// ---------------------------------------------------------------------------------------
// Required concurrency tests (SPEC §6)

// 1. Many travelers race for the last seats: exactly the seats available are sold.
func TestConcurrentBookingForLastQuota(t *testing.T) {
	for _, tc := range []struct {
		name               string
		quota, party, n    int
		wantOK, wantReserv int
	}{
		{"last single seat", 1, 1, 20, 1, 1},
		{"groups for five seats", 5, 2, 20, 2, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			f := e.fixture(tc.quota)
			travelers := make([]int64, tc.n)
			for i := range travelers {
				travelers[i] = e.user()
			}

			var wg sync.WaitGroup
			start := make(chan struct{})
			errs := make([]error, tc.n)
			for i := range tc.n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, errs[i] = e.svc.Create(e.ctx, travelers[i], input(f.pkg, day(7), tc.party))
				}()
			}
			close(start)
			wg.Wait()

			ok := 0
			for _, err := range errs {
				switch {
				case err == nil:
					ok++
				case errors.Is(err, booking.ErrSoldOut):
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}
			if ok != tc.wantOK {
				t.Errorf("successful bookings = %d, want %d", ok, tc.wantOK)
			}
			if got := e.reserved(f.pkg, day(7)); got != tc.wantReserv {
				t.Errorf("reserved = %d, want %d", got, tc.wantReserv)
			}
			var bookings int
			e.pool.QueryRow(e.ctx, `SELECT count(*) FROM bookings WHERE package_id = $1`, f.pkg).Scan(&bookings) //nolint:errcheck
			if bookings != tc.wantOK {
				t.Errorf("booking rows = %d, want %d", bookings, tc.wantOK)
			}
		})
	}
}

// 2. A manager rejects while the expiry job runs: exactly one wins, quota released once.
func TestRejectRacesExpiry(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(100)
	const n = 30
	ids := make([]int64, n)
	for i := range ids {
		// Spread across travelers to stay under the pending limit.
		ids[i] = e.book(e.user(), f.pkg, day(10), 1).ID
	}
	if got := e.reserved(f.pkg, day(10)); got != n {
		t.Fatalf("reserved = %d, want %d", got, n)
	}
	e.clock.Set(startTime.Add(49 * time.Hour)) // all requests are now past expires_at

	var wg sync.WaitGroup
	start := make(chan struct{})
	rejectErr := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, rejectErr[i] = e.svc.Reject(e.ctx, f.manager, ids[i], "")
		}()
	}
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := e.svc.ExpireDue(e.ctx); err != nil {
				t.Errorf("ExpireDue: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	// Sweep anything the racing workers skipped.
	if _, err := e.svc.ExpireDue(e.ctx); err != nil {
		t.Fatal(err)
	}

	for i, id := range ids {
		st := e.status(id)
		switch {
		case rejectErr[i] == nil && st != booking.StatusRejected:
			t.Errorf("booking %d: reject succeeded but status is %s", id, st)
		case errors.Is(rejectErr[i], booking.ErrInvalidTransition) && st != booking.StatusExpired:
			t.Errorf("booking %d: reject lost but status is %s", id, st)
		case rejectErr[i] != nil && !errors.Is(rejectErr[i], booking.ErrInvalidTransition):
			t.Errorf("booking %d: unexpected reject error %v", id, rejectErr[i])
		}
	}
	if got := e.reserved(f.pkg, day(10)); got != 0 {
		t.Errorf("reserved = %d after all requests ended, want 0 (released exactly once)", got)
	}
}

// 3. Cancelling after confirmation returns the seats.
func TestCancelAfterConfirmReleasesQuota(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(4)
	traveler := e.user()
	b := e.book(traveler, f.pkg, day(5), 4)
	if _, err := e.svc.Confirm(e.ctx, f.manager, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(5), 1)); !errors.Is(err, booking.ErrSoldOut) {
		t.Fatalf("expected sold out, got %v", err)
	}

	got, err := e.svc.CancelByTraveler(e.ctx, traveler, b.ID, "sakit")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != booking.StatusCancelled || got.CancelledBy != booking.CancelledByTraveler || got.CancelledAt == nil {
		t.Errorf("unexpected booking after cancel %+v", got)
	}
	if r := e.reserved(f.pkg, day(5)); r != 0 {
		t.Errorf("reserved = %d, want 0", r)
	}
	if _, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(5), 4)); err != nil {
		t.Errorf("seats not bookable again: %v", err)
	}

	// Manager cancellation of a confirmed booking releases too.
	other := e.book(e.user(), f.pkg, day(6), 2)
	e.svc.Confirm(e.ctx, f.manager, other.ID) //nolint:errcheck
	if _, err := e.svc.CancelByManager(e.ctx, f.manager, other.ID, "cuaca buruk"); err != nil {
		t.Fatal(err)
	}
	if r := e.reserved(f.pkg, day(6)); r != 0 {
		t.Errorf("reserved after manager cancel = %d, want 0", r)
	}
}

// 4. One traveler fires many requests at once: at most three stay pending.
func TestPendingLimitUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(100)
	traveler := e.user()
	const n = 10

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = e.svc.Create(e.ctx, traveler, input(f.pkg, day(3+i), 1))
		}()
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, booking.ErrPendingLimit):
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != booking.MaxPendingPerTraveler {
		t.Errorf("accepted = %d, want %d", ok, booking.MaxPendingPerTraveler)
	}

	// A decided request frees a slot.
	list, _ := e.svc.ListForTraveler(e.ctx, traveler)
	if _, err := e.svc.Confirm(e.ctx, f.manager, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(e.ctx, traveler, input(f.pkg, day(40), 1)); err != nil {
		t.Errorf("after one confirmation: %v", err)
	}
}

// 5. Closing a date stops new bookings but leaves existing ones alone.
func TestClosingDateKeepsExistingBookings(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(10)
	pending := e.book(e.user(), f.pkg, day(5), 2)
	confirmed := e.book(e.user(), f.pkg, day(5), 3)
	e.svc.Confirm(e.ctx, f.manager, confirmed.ID) //nolint:errcheck

	if err := e.svc.SetClosed(e.ctx, f.manager, f.pkg, booking.Date(2030, 3, 15), true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(5), 1)); !errors.Is(err, booking.ErrDateClosed) {
		t.Errorf("booking a closed date: %v, want ErrDateClosed", err)
	}
	if st := e.status(pending.ID); st != booking.StatusPending {
		t.Errorf("pending booking became %s", st)
	}
	if st := e.status(confirmed.ID); st != booking.StatusConfirmed {
		t.Errorf("confirmed booking became %s", st)
	}
	if r := e.reserved(f.pkg, day(5)); r != 5 {
		t.Errorf("reserved = %d, want 5", r)
	}
	// A pending request on a closed date can still be confirmed.
	if _, err := e.svc.Confirm(e.ctx, f.manager, pending.ID); err != nil {
		t.Errorf("confirm on closed date: %v", err)
	}

	// Closing a date nobody booked yet creates the row closed.
	if err := e.svc.SetClosed(e.ctx, f.manager, f.pkg, booking.Date(2030, 3, 20), true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(10), 1)); !errors.Is(err, booking.ErrDateClosed) {
		t.Errorf("fresh closed date: %v", err)
	}
	if err := e.svc.SetClosed(e.ctx, f.manager, f.pkg, booking.Date(2030, 3, 20), false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(10), 1)); err != nil {
		t.Errorf("reopened date: %v", err)
	}
}

// 6. Quota can't drop below what is already reserved.
func TestQuotaCannotGoBelowReserved(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(10)
	e.book(e.user(), f.pkg, day(5), 4)
	date := booking.Date(2030, 3, 15)

	if err := e.svc.SetQuota(e.ctx, f.manager, f.pkg, date, 3); !errors.Is(err, booking.ErrQuotaBelowReserved) {
		t.Errorf("quota 3 < reserved 4: %v", err)
	}
	if err := e.svc.SetQuota(e.ctx, f.manager, f.pkg, date, 4); err != nil {
		t.Errorf("quota == reserved: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(5), 1)); !errors.Is(err, booking.ErrSoldOut) {
		t.Errorf("after lowering quota to reserved: %v", err)
	}
	// Raising quota on an untouched date, and zero quota where nothing is reserved.
	if err := e.svc.SetQuota(e.ctx, f.manager, f.pkg, booking.Date(2030, 3, 16), 0); err != nil {
		t.Errorf("zero quota on empty date: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, e.user(), input(f.pkg, day(6), 1)); !errors.Is(err, booking.ErrSoldOut) {
		t.Errorf("booking a zero-quota date: %v", err)
	}
	if err := e.svc.SetQuota(e.ctx, f.manager, f.pkg, booking.Date(2030, 3, 9), 5); !errors.Is(err, booking.ErrDateOutOfWindow) {
		t.Errorf("past date: %v", err)
	}
	if err := e.svc.SetQuota(e.ctx, f.manager, f.pkg, date, -1); err == nil {
		t.Error("negative quota accepted")
	}
}

// ---------------------------------------------------------------------------------------
// Status transitions

func TestTransitions(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(50)
	newBooking := func(offset int) booking.Booking { return e.book(e.user(), f.pkg, day(offset), 1) }

	t.Run("confirm then confirm again", func(t *testing.T) {
		b := newBooking(5)
		if _, err := e.svc.Confirm(e.ctx, f.manager, b.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Confirm(e.ctx, f.manager, b.ID); !errors.Is(err, booking.ErrInvalidTransition) {
			t.Errorf("second confirm: %v", err)
		}
		if _, err := e.svc.Reject(e.ctx, f.manager, b.ID, ""); !errors.Is(err, booking.ErrInvalidTransition) {
			t.Errorf("reject confirmed: %v", err)
		}
	})

	t.Run("reject releases and is final", func(t *testing.T) {
		b := newBooking(6)
		got, err := e.svc.Reject(e.ctx, f.manager, b.ID, "penuh")
		if err != nil {
			t.Fatal(err)
		}
		if got.DecisionNote != "penuh" || got.DecidedAt == nil {
			t.Errorf("unexpected %+v", got)
		}
		if r := e.reserved(f.pkg, day(6)); r != 0 {
			t.Errorf("reserved = %d", r)
		}
		if _, err := e.svc.Reject(e.ctx, f.manager, b.ID, ""); !errors.Is(err, booking.ErrInvalidTransition) {
			t.Errorf("double reject: %v", err)
		}
		if r := e.reserved(f.pkg, day(6)); r != 0 {
			t.Errorf("reserved after double reject = %d", r)
		}
	})

	t.Run("confirm after expires_at fails", func(t *testing.T) {
		b := newBooking(20)
		defer e.clock.Set(startTime)
		e.clock.Set(b.ExpiresAt)
		if _, err := e.svc.Confirm(e.ctx, f.manager, b.ID); !errors.Is(err, booking.ErrInvalidTransition) {
			t.Errorf("confirm at expires_at: %v", err)
		}
	})

	t.Run("complete only on or after start date", func(t *testing.T) {
		b := newBooking(4)                    // 03-14
		e.svc.Confirm(e.ctx, f.manager, b.ID) //nolint:errcheck
		defer e.clock.Set(startTime)
		e.clock.Set(time.Date(2030, 3, 13, 23, 59, 0, 0, jakarta))
		if _, err := e.svc.Complete(e.ctx, f.manager, b.ID); !errors.Is(err, booking.ErrInvalidTransition) {
			t.Errorf("complete the day before: %v", err)
		}
		e.clock.Set(time.Date(2030, 3, 14, 8, 0, 0, 0, jakarta))
		got, err := e.svc.Complete(e.ctx, f.manager, b.ID)
		if err != nil || got.Status != booking.StatusCompleted || got.CompletedAt == nil {
			t.Fatalf("complete on start date: %+v %v", got, err)
		}
		if r := e.reserved(f.pkg, day(4)); r != 1 {
			t.Errorf("completed booking released seats: reserved = %d", r)
		}
		if _, err := e.svc.CancelByManager(e.ctx, f.manager, b.ID, ""); !errors.Is(err, booking.ErrInvalidTransition) {
			t.Errorf("cancel completed: %v", err)
		}
	})

	t.Run("pending cannot be completed", func(t *testing.T) {
		b := newBooking(3)
		defer e.clock.Set(startTime)
		e.clock.Set(time.Date(2030, 3, 13, 9, 0, 0, 0, jakarta))
		if _, err := e.svc.Complete(e.ctx, f.manager, b.ID); !errors.Is(err, booking.ErrInvalidTransition) {
			t.Errorf("complete pending: %v", err)
		}
	})

	t.Run("cancel allowed on start date, not after", func(t *testing.T) {
		traveler := e.user()
		b1 := e.book(traveler, f.pkg, day(8), 1) // 03-18
		b2 := e.book(traveler, f.pkg, day(9), 1) // 03-19
		e.svc.Confirm(e.ctx, f.manager, b1.ID)   //nolint:errcheck
		e.svc.Confirm(e.ctx, f.manager, b2.ID)   //nolint:errcheck
		defer e.clock.Set(startTime)
		e.clock.Set(time.Date(2030, 3, 19, 7, 0, 0, 0, jakarta))
		if _, err := e.svc.CancelByTraveler(e.ctx, traveler, b1.ID, ""); !errors.Is(err, booking.ErrInvalidTransition) {
			t.Errorf("cancel after start date: %v", err)
		}
		if _, err := e.svc.CancelByTraveler(e.ctx, traveler, b2.ID, ""); err != nil {
			t.Errorf("cancel on start date: %v", err)
		}
	})
}

func TestExpireDue(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(10)
	early := e.book(e.user(), f.pkg, day(2), 2) // expires at 03-12 00:00 WIB
	late := e.book(e.user(), f.pkg, day(30), 3) // expires at now + 48h
	confirmed := e.book(e.user(), f.pkg, day(2), 1)
	e.svc.Confirm(e.ctx, f.manager, confirmed.ID) //nolint:errcheck

	e.clock.Set(time.Date(2030, 3, 12, 0, 0, 0, 0, jakarta))
	n, err := e.svc.ExpireDue(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("expired %d (%v), want 1", n, err)
	}
	if e.status(early.ID) != booking.StatusExpired || e.status(late.ID) != booking.StatusPending || e.status(confirmed.ID) != booking.StatusConfirmed {
		t.Errorf("statuses: early=%s late=%s confirmed=%s", e.status(early.ID), e.status(late.ID), e.status(confirmed.ID))
	}
	if r := e.reserved(f.pkg, day(2)); r != 1 {
		t.Errorf("reserved on 03-12 = %d, want 1 (only the confirmed booking)", r)
	}
	if n, _ := e.svc.ExpireDue(e.ctx); n != 0 {
		t.Errorf("second run expired %d", n)
	}
}

// ---------------------------------------------------------------------------------------
// Ownership: every action checks the booking belongs to the actor.

func TestAccessAcrossVillagesAndTravelers(t *testing.T) {
	e := newEnv(t)
	a := e.fixture(10) // village A
	b := e.fixture(10) // village B, managed by someone else
	alice, bob := e.user(), e.user()
	aliceBooking := e.book(alice, a.pkg, day(5), 1)

	// Traveler-to-traveler.
	if _, err := e.svc.ForTraveler(e.ctx, bob, aliceBooking.ID); !errors.Is(err, booking.ErrNotFound) {
		t.Errorf("bob reads alice's booking: %v", err)
	}
	if _, err := e.svc.CancelByTraveler(e.ctx, bob, aliceBooking.ID, ""); !errors.Is(err, booking.ErrNotFound) {
		t.Errorf("bob cancels alice's booking: %v", err)
	}
	// A traveler is not a manager.
	if _, err := e.svc.Confirm(e.ctx, alice, aliceBooking.ID); !errors.Is(err, booking.ErrNotFound) {
		t.Errorf("traveler confirms own booking: %v", err)
	}

	// Manager of village B against village A's booking and package.
	mB := b.manager
	checks := map[string]func() error{
		"read":          func() error { _, err := e.svc.ForManager(e.ctx, mB, aliceBooking.ID); return err },
		"confirm":       func() error { _, err := e.svc.Confirm(e.ctx, mB, aliceBooking.ID); return err },
		"reject":        func() error { _, err := e.svc.Reject(e.ctx, mB, aliceBooking.ID, ""); return err },
		"cancel":        func() error { _, err := e.svc.CancelByManager(e.ctx, mB, aliceBooking.ID, ""); return err },
		"complete":      func() error { _, err := e.svc.Complete(e.ctx, mB, aliceBooking.ID); return err },
		"list village":  func() error { _, err := e.svc.ListForVillage(e.ctx, mB, a.village); return err },
		"set quota":     func() error { return e.svc.SetQuota(e.ctx, mB, a.pkg, booking.Date(2030, 3, 15), 99) },
		"close date":    func() error { return e.svc.SetClosed(e.ctx, mB, a.pkg, booking.Date(2030, 3, 15), true) },
		"cancel as bob": func() error { _, err := e.svc.CancelByManager(e.ctx, bob, aliceBooking.ID, ""); return err },
	}
	for name, fn := range checks {
		if err := fn(); !errors.Is(err, booking.ErrNotFound) {
			t.Errorf("%s across villages: %v, want ErrNotFound", name, err)
		}
	}
	if st := e.status(aliceBooking.ID); st != booking.StatusPending {
		t.Errorf("alice's booking changed to %s", st)
	}
	if r := e.reserved(a.pkg, day(5)); r != 1 {
		t.Errorf("village A availability changed: reserved = %d", r)
	}

	// The rightful parties still can.
	if _, err := e.svc.ForManager(e.ctx, a.manager, aliceBooking.ID); err != nil {
		t.Errorf("manager A reads: %v", err)
	}
	if _, err := e.svc.ForTraveler(e.ctx, alice, aliceBooking.ID); err != nil {
		t.Errorf("alice reads: %v", err)
	}
	list, err := e.svc.ListForVillage(e.ctx, a.manager, a.village)
	if err != nil || len(list) != 1 {
		t.Errorf("manager A list: %d %v", len(list), err)
	}
}

func TestCalendar(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(8)
	e.book(e.user(), f.pkg, day(3), 5)
	e.svc.SetClosed(e.ctx, f.manager, f.pkg, booking.Date(2030, 3, 14), true) //nolint:errcheck

	days, err := e.svc.Calendar(e.ctx, f.pkg, 8, booking.Date(2030, 3, 12), booking.Date(2030, 3, 14))
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 3 {
		t.Fatalf("days = %d", len(days))
	}
	if days[0].Remaining() != 8 || days[1].Remaining() != 3 || days[2].Remaining() != 0 || !days[2].Closed {
		t.Errorf("remaining: %d %d %d (closed=%v)", days[0].Remaining(), days[1].Remaining(), days[2].Remaining(), days[2].Closed)
	}
}

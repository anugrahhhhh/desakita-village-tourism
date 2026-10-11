package booking_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"desakita/internal/booking"
	"desakita/internal/platform/dbtest"
)

type event struct {
	name string
	id   int64
}

// recorder is a booking.Notifier that records events, or fails when fail is set.
type recorder struct {
	mu     sync.Mutex
	events []event
	fail   error
}

func (r *recorder) BookingEvent(_ context.Context, _ pgx.Tx, name string, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.events = append(r.events, event{name, id})
	return nil
}

func (r *recorder) take() []event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

func newNotifyEnv(t *testing.T) (*env, *recorder) {
	t.Helper()
	pool := dbtest.New(t)
	c := &clock{t: startTime}
	rec := &recorder{}
	svc := booking.NewService(pool, sqlPackages{}, sqlVillages{pool}, booking.Config{
		PendingTTL: 48 * time.Hour, Location: jakarta, Now: c.Now, Notifier: rec,
	})
	return &env{t: t, pool: pool, svc: svc, clock: c, ctx: context.Background()}, rec
}

func TestNotifierSeesEachTransitionOnce(t *testing.T) {
	e, rec := newNotifyEnv(t)
	f := e.fixture(50)
	traveler := e.user()
	expect := func(want ...event) {
		t.Helper()
		got := rec.take()
		if len(got) != len(want) {
			t.Fatalf("events = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("event %d = %v, want %v", i, got[i], want[i])
			}
		}
	}

	b := e.book(traveler, f.pkg, day(4), 1)
	expect(event{booking.EventRequested, b.ID})
	e.svc.Confirm(e.ctx, f.manager, b.ID) //nolint:errcheck
	expect(event{booking.EventConfirmed, b.ID})
	if _, err := e.svc.Confirm(e.ctx, f.manager, b.ID); !errors.Is(err, booking.ErrInvalidTransition) {
		t.Fatal(err)
	}
	expect() // invalid transition: nothing

	r := e.book(traveler, f.pkg, day(5), 1)
	e.svc.Reject(e.ctx, f.manager, r.ID, "penuh") //nolint:errcheck
	c1 := e.book(traveler, f.pkg, day(6), 1)
	e.svc.CancelByTraveler(e.ctx, traveler, c1.ID, "") //nolint:errcheck
	c2 := e.book(traveler, f.pkg, day(7), 1)
	e.svc.CancelByManager(e.ctx, f.manager, c2.ID, "banjir") //nolint:errcheck
	expect(event{booking.EventRequested, r.ID}, event{booking.EventRejected, r.ID},
		event{booking.EventRequested, c1.ID}, event{booking.EventCancelledByTraveler, c1.ID},
		event{booking.EventRequested, c2.ID}, event{booking.EventCancelledByManager, c2.ID})

	x := e.book(e.user(), f.pkg, day(2), 1)
	rec.take()
	e.clock.Set(time.Date(2030, 3, 12, 0, 0, 0, 0, jakarta))
	if n, _ := e.svc.ExpireDue(e.ctx); n != 1 {
		t.Fatalf("expired %d", n)
	}
	expect(event{booking.EventExpired, x.ID})

	e.clock.Set(time.Date(2030, 3, 14, 9, 0, 0, 0, jakarta))
	e.svc.Complete(e.ctx, f.manager, b.ID) //nolint:errcheck
	expect(event{booking.EventCompleted, b.ID})
}

func TestNotifierFailureRollsBackTheChange(t *testing.T) {
	e, rec := newNotifyEnv(t)
	f := e.fixture(5)
	traveler := e.user()
	b := e.book(traveler, f.pkg, day(4), 2)

	rec.fail = errors.New("outbox down")
	if _, err := e.svc.Confirm(e.ctx, f.manager, b.ID); err == nil {
		t.Fatal("confirm succeeded although the notification could not be queued")
	}
	if s := e.status(b.ID); s != booking.StatusPending {
		t.Errorf("status = %s, want pending (rolled back)", s)
	}
	if _, err := e.svc.Create(e.ctx, traveler, input(f.pkg, day(5), 2)); err == nil {
		t.Fatal("create succeeded without its notification")
	}
	if r := e.reserved(f.pkg, day(5)); r != 0 {
		t.Errorf("reserved = %d after rolled-back create", r)
	}
}

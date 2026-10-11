package mail_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/platform/dbtest"
	outbox "desakita/internal/platform/mail"
)

// fakeTransport records deliveries and can fail on demand.
type fakeTransport struct {
	mu    sync.Mutex
	sent  []outbox.Envelope
	fail  error
	delay time.Duration
}

func (f *fakeTransport) Send(_ context.Context, e outbox.Envelope) error {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, e)
	return nil
}

func (f *fakeTransport) count(to string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.sent {
		if e.To == to {
			n++
		}
	}
	return n
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

var from = &mail.Address{Name: "DesaKita", Address: "noreply@desakita.id"}

func newWorker(pool *pgxpool.Pool, tr outbox.Transport, c *clock) *outbox.Worker {
	w := outbox.NewWorker(pool, tr, from, slog.New(slog.DiscardHandler))
	w.Now = c.Now
	return w
}

func msg(i int) outbox.Message {
	return outbox.Message{Kind: "test", DedupeKey: fmt.Sprintf("k%d", i), To: fmt.Sprintf("u%d@example.com", i), Subject: "Halo", Body: "Isi"}
}

func status(t *testing.T, pool *pgxpool.Pool, key string) (st string, attempts int, next time.Time) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), "SELECT status, attempts, next_attempt_at FROM email_outbox WHERE dedupe_key = $1", key).Scan(&st, &attempts, &next); err != nil {
		t.Fatal(err)
	}
	return
}

func TestEnqueueIsTransactionalAndDeduplicated(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()

	tx, _ := pool.Begin(ctx)
	if err := outbox.Enqueue(ctx, tx, msg(1)); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx) //nolint:errcheck
	tx, _ = pool.Begin(ctx)
	for range 3 {
		if err := outbox.Enqueue(ctx, tx, msg(2)); err != nil {
			t.Fatal(err)
		}
	}
	tx.Commit(ctx) //nolint:errcheck

	counts, _ := outbox.Counts(ctx, pool)
	if counts["pending"] != 1 {
		t.Errorf("pending = %d, want 1 (rolled back message gone, duplicates ignored)", counts["pending"])
	}
	if err := outbox.Enqueue(ctx, pool, outbox.Message{Kind: "x", DedupeKey: "y", To: "bad", Subject: "s", Body: "b"}); !errors.Is(err, outbox.ErrInvalidMessage) {
		t.Errorf("invalid message: %v", err)
	}
}

func TestWorkerDeliversRetriesAndGivesUp(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	c := &clock{t: time.Date(2030, 3, 10, 10, 0, 0, 0, time.UTC)}
	tr := &fakeTransport{}
	w := newWorker(pool, tr, c)
	for i := range 3 {
		if err := outbox.Enqueue(ctx, pool, msg(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Messages are due at DB now(); move the worker clock past that.
	c.t = time.Now().Add(time.Second)

	sent, failed, err := w.RunOnce(ctx)
	if err != nil || sent != 3 || failed != 0 {
		t.Fatalf("first run: sent=%d failed=%d err=%v", sent, failed, err)
	}
	if st, _, _ := status(t, pool, "k0"); st != "sent" {
		t.Errorf("status = %s", st)
	}
	if tr.sent[0].MessageID == "" || tr.sent[0].From.Address != "noreply@desakita.id" {
		t.Errorf("envelope: %+v", tr.sent[0])
	}

	// A failing message is retried with backoff, then marked failed.
	if err := outbox.Enqueue(ctx, pool, msg(9)); err != nil {
		t.Fatal(err)
	}
	tr.fail = errors.New("421 try again later")
	for attempt := 1; attempt <= outbox.MaxAttempts; attempt++ {
		_, failed, err := w.RunOnce(ctx)
		if err != nil || failed != 1 {
			t.Fatalf("attempt %d: failed=%d err=%v", attempt, failed, err)
		}
		st, attempts, next := status(t, pool, "k9")
		if attempts != attempt {
			t.Errorf("attempts = %d, want %d", attempts, attempt)
		}
		if attempt < outbox.MaxAttempts {
			want := outbox.Backoff[attempt-1]
			if st != "pending" || next.Sub(c.Now()).Round(time.Second) != want {
				t.Errorf("attempt %d: status=%s next in %v, want pending in %v", attempt, st, next.Sub(c.Now()), want)
			}
			// Not due yet: nothing is claimed.
			if _, f, _ := w.RunOnce(ctx); f != 0 {
				t.Errorf("attempt %d: retried before backoff elapsed", attempt)
			}
			c.Add(want + time.Second)
		} else if st != "failed" {
			t.Errorf("after %d attempts status = %s, want failed", attempt, st)
		}
	}
	var lastErr string
	pool.QueryRow(ctx, "SELECT last_error FROM email_outbox WHERE dedupe_key = 'k9'").Scan(&lastErr) //nolint:errcheck
	if lastErr != "421 try again later" {
		t.Errorf("last_error = %q", lastErr)
	}
}

func TestStaleSendingIsReclaimedWithSameMessageID(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	c := &clock{t: time.Now().Add(time.Second)}
	if err := outbox.Enqueue(ctx, pool, msg(1)); err != nil {
		t.Fatal(err)
	}
	// Simulate a worker that claimed the message and crashed before finishing.
	if _, err := pool.Exec(ctx, "UPDATE email_outbox SET status = 'sending', attempts = 1, locked_until = $1", c.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	tr := &fakeTransport{}
	w := newWorker(pool, tr, c)
	if sent, _, _ := w.RunOnce(ctx); sent != 0 {
		t.Fatal("locked message reclaimed before its lock expired")
	}
	c.Add(2 * time.Minute)
	if sent, _, _ := w.RunOnce(ctx); sent != 1 {
		t.Fatal("stale message not reclaimed")
	}
	var id int64
	pool.QueryRow(ctx, "SELECT id FROM email_outbox").Scan(&id) //nolint:errcheck
	if want := fmt.Sprintf("<outbox-%d@desakita.id>", id); tr.sent[0].MessageID != want {
		t.Errorf("message id = %s, want %s (stable across retries)", tr.sent[0].MessageID, want)
	}
}

func TestConcurrentWorkersSendEachMessageOnce(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	const n = 40
	for i := range n {
		if err := outbox.Enqueue(ctx, pool, msg(i)); err != nil {
			t.Fatal(err)
		}
	}
	c := &clock{t: time.Now().Add(time.Second)}
	tr := &fakeTransport{delay: 5 * time.Millisecond}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := newWorker(pool, tr, c)
			w.BatchSize = 5
			for {
				sent, _, err := w.RunOnce(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if sent == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	for i := range n {
		if got := tr.count(fmt.Sprintf("u%d@example.com", i)); got != 1 {
			t.Errorf("message %d delivered %d times", i, got)
		}
	}
}

func TestPurgeSentAndAccountData(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	c := &clock{t: time.Now().Add(time.Second)}
	for i := range 2 {
		if err := outbox.Enqueue(ctx, pool, msg(i)); err != nil {
			t.Fatal(err)
		}
	}
	w := newWorker(pool, &fakeTransport{}, c)
	w.BatchSize = 1
	w.RunOnce(ctx) //nolint:errcheck // sends message 0 only

	// Unsent mail to a deleted account's address is dropped; sent mail stays until retention.
	if err := outbox.PurgeUnsentTo(ctx, pool, "u1@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := outbox.PurgeUnsentTo(ctx, pool, "u0@example.com"); err != nil {
		t.Fatal(err)
	}
	counts, _ := outbox.Counts(ctx, pool)
	if counts["pending"] != 0 || counts["sent"] != 1 {
		t.Errorf("after purge: %v", counts)
	}
	if n, _ := w.Purge(ctx); n != 0 {
		t.Error("fresh sent mail purged")
	}
	c.Add(31 * 24 * time.Hour)
	if n, _ := w.Purge(ctx); n != 1 {
		t.Errorf("purged %d, want 1 after retention", n)
	}
}

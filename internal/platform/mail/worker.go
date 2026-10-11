package mail

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/db"
)

// Backoff is the wait after the 1st, 2nd, … failed attempt. After the last entry the
// message is marked failed.
var Backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// MaxAttempts is the number of delivery attempts before a message is marked failed.
var MaxAttempts = len(Backoff) + 1

// Worker delivers queued messages.
type Worker struct {
	pool      *pgxpool.Pool
	q         *db.Queries
	transport Transport
	from      *mail.Address
	logger    *slog.Logger
	// Now, BatchSize, Lock and Retention can be overridden (tests).
	Now       func() time.Time
	BatchSize int32
	// Lock is how long a claimed message stays reserved for one worker.
	Lock time.Duration
	// Retention is how long sent messages are kept before deletion.
	Retention time.Duration
}

func NewWorker(pool *pgxpool.Pool, transport Transport, from *mail.Address, logger *slog.Logger) *Worker {
	return &Worker{
		pool: pool, q: db.New(pool), transport: transport, from: from, logger: logger,
		Now: time.Now, BatchSize: 20, Lock: 5 * time.Minute, Retention: 30 * 24 * time.Hour,
	}
}

// RunOnce claims one batch and tries to deliver it. It returns how many messages were sent
// and how many attempts failed.
func (w *Worker) RunOnce(ctx context.Context) (sent, failed int, err error) {
	now := w.Now()
	var claimed []db.ClaimEmailsRow
	err = pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var err error
		claimed, err = w.q.WithTx(tx).ClaimEmails(ctx, db.ClaimEmailsParams{
			Now: now, LockedUntil: now.Add(w.Lock), BatchSize: w.BatchSize,
		})
		return err
	})
	if err != nil {
		return 0, 0, fmt.Errorf("claim emails: %w", err)
	}

	for _, m := range claimed {
		env := Envelope{From: w.from, To: m.ToAddress, Subject: m.Subject, Body: m.Body, MessageID: messageID(m.ID, w.from), Date: now}
		sendErr := w.transport.Send(ctx, env)
		if sendErr == nil {
			if err := w.q.MarkEmailSent(ctx, db.MarkEmailSentParams{ID: m.ID, Now: w.Now()}); err != nil {
				return sent, failed, fmt.Errorf("mark sent: %w", err)
			}
			sent++
			continue
		}
		failed++
		giveUp := int(m.Attempts) >= MaxAttempts
		next := w.Now()
		if !giveUp {
			next = next.Add(Backoff[min(int(m.Attempts)-1, len(Backoff)-1)])
		}
		msg := sendErr.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
		// Log the outbox id and kind only: addresses and bodies are personal data.
		w.logger.WarnContext(ctx, "email delivery failed", "outbox_id", m.ID, "kind", m.Kind, "attempt", m.Attempts, "give_up", giveUp, "err", msg)
		if err := w.q.MarkEmailRetry(ctx, db.MarkEmailRetryParams{ID: m.ID, GiveUp: giveUp, NextAttemptAt: next, LastError: msg}); err != nil {
			return sent, failed, fmt.Errorf("mark retry: %w", err)
		}
	}
	return sent, failed, nil
}

// Purge deletes sent messages older than Retention.
func (w *Worker) Purge(ctx context.Context) (int64, error) {
	n, err := w.q.PurgeSentEmails(ctx, w.Now().Add(-w.Retention))
	if err != nil {
		return 0, fmt.Errorf("purge sent emails: %w", err)
	}
	return n, nil
}

// Run delivers mail every interval and purges old sent mail hourly, until ctx ends.
func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var lastPurge time.Time
	for {
		for {
			sent, failed, err := w.RunOnce(ctx)
			if err != nil {
				if ctx.Err() == nil {
					w.logger.ErrorContext(ctx, "email worker", "err", err)
				}
				break
			}
			if sent > 0 || failed > 0 {
				w.logger.InfoContext(ctx, "email batch", "sent", sent, "failed", failed)
			}
			if sent+failed < int(w.BatchSize) {
				break // queue drained
			}
		}
		if time.Since(lastPurge) > time.Hour {
			if n, err := w.Purge(ctx); err != nil {
				w.logger.ErrorContext(ctx, "email purge", "err", err)
			} else if n > 0 {
				w.logger.InfoContext(ctx, "purged sent emails", "count", n)
			}
			lastPurge = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

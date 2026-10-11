// Package mail implements transactional email with the outbox pattern (SPEC §13).
//
// Enqueue writes a message in the caller's transaction, so it exists if and only if the
// business change commits. A Worker delivers queued messages at least once through a
// Transport (SMTP, or .eml files in development).
package mail

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"desakita/internal/db"
)

// Message is one email to queue.
type Message struct {
	// Kind labels the event, such as booking.confirmed (for metrics and debugging).
	Kind string
	// DedupeKey makes enqueueing idempotent: a second message with the same key is ignored.
	DedupeKey string
	To        string
	Subject   string
	Body      string
}

var ErrInvalidMessage = errors.New("mail: invalid message")

func (m Message) validate() error {
	if m.Kind == "" || m.DedupeKey == "" || m.Body == "" {
		return fmt.Errorf("%w: kind, dedupe key and body are required", ErrInvalidMessage)
	}
	addr, err := mail.ParseAddress(m.To)
	if err != nil || addr.Address != m.To {
		return fmt.Errorf("%w: bad recipient", ErrInvalidMessage)
	}
	if m.Subject == "" || strings.ContainsAny(m.Subject, "\r\n") || utf8.RuneCountInString(m.Subject) > 200 {
		return fmt.Errorf("%w: bad subject", ErrInvalidMessage)
	}
	return nil
}

// Enqueue queues m using q, which must be the transaction making the business change.
func Enqueue(ctx context.Context, q db.DBTX, m Message) error {
	if err := m.validate(); err != nil {
		return err
	}
	err := db.New(q).EnqueueEmail(ctx, db.EnqueueEmailParams{
		Kind: m.Kind, DedupeKey: m.DedupeKey, ToAddress: m.To, Subject: m.Subject, Body: m.Body,
	})
	if err != nil {
		return fmt.Errorf("enqueue email %s: %w", m.Kind, err)
	}
	return nil
}

// PurgeUnsentTo drops undelivered messages for an address (used when an account is deleted).
func PurgeUnsentTo(ctx context.Context, q db.DBTX, address string) error {
	if err := db.New(q).PurgeUnsentEmailsTo(ctx, address); err != nil {
		return fmt.Errorf("purge unsent email: %w", err)
	}
	return nil
}

// Counts returns how many messages are in each status.
func Counts(ctx context.Context, q db.DBTX) (map[string]int64, error) {
	rows, err := db.New(q).CountEmailsByStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("count emails: %w", err)
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Status] = r.Total
	}
	return out, nil
}

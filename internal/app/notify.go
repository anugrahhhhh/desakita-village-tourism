package app

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"desakita/internal/booking"
	"desakita/internal/db"
	"desakita/internal/platform/i18n"
	"desakita/internal/platform/mail"
	"desakita/internal/platform/render"
	"desakita/internal/village"
)

// notifier turns booking and village events into queued emails. It runs inside the
// business transaction (booking.Notifier, village.Notifier), so a rolled-back change queues
// nothing. Each message is deduplicated per event and recipient.
type notifier struct {
	templates *mail.Templates
	tr        *i18n.Translator
	loc       *time.Location
	baseURL   string
}

var (
	_ booking.Notifier = notifier{}
	_ village.Notifier = notifier{}
)

// bookingMail is the data every booking email template receives.
type bookingMail struct {
	RecipientName string
	TravelerName  string
	PackageName   string
	VillageName   string
	Date          string
	PartySize     int32
	Total         string
	Note          string
	RespondBy     string
	Link          string
}

type villageMail struct {
	RecipientName string
	VillageName   string
	Note          string
	Link          string
	PublicLink    string
}

type recipient struct{ email, name string }

func (n notifier) send(ctx context.Context, tx pgx.Tx, kind, dedupe, template string, to recipient, data any) error {
	if to.email == "" {
		return nil // e.g. the traveler deleted their account
	}
	subject, body, err := n.templates.Render(template, data)
	if err != nil {
		return err
	}
	return mail.Enqueue(ctx, tx, mail.Message{
		Kind: kind, DedupeKey: dedupe + ":" + to.email, To: to.email, Subject: subject, Body: body,
	})
}

func managers(ctx context.Context, q *db.Queries, villageID int64) ([]recipient, error) {
	rows, err := q.VillageManagerEmails(ctx, villageID)
	if err != nil {
		return nil, fmt.Errorf("manager emails: %w", err)
	}
	out := make([]recipient, len(rows))
	for i, r := range rows {
		out[i] = recipient{email: r.Email, name: r.Name}
	}
	return out, nil
}

func (n notifier) BookingEvent(ctx context.Context, tx pgx.Tx, event string, bookingID int64) error {
	q := db.New(tx)
	b, err := q.BookingNotificationData(ctx, bookingID)
	if err != nil {
		return fmt.Errorf("booking notification data: %w", err)
	}
	expires := b.ExpiresAt.In(n.loc)
	data := bookingMail{
		TravelerName: b.ContactName, PackageName: b.PackageName, VillageName: b.VillageName,
		Date: render.FormatDate(n.tr, b.StartDate), PartySize: b.PartySize, Total: render.FormatRupiah(b.TotalPriceIdr),
		Note:      b.DecisionNote,
		RespondBy: render.FormatDate(n.tr, expires) + " " + expires.Format("15:04 MST"),
	}
	travelerLink := fmt.Sprintf("%s/pemesanan/%d", n.baseURL, b.ID)
	managerLink := fmt.Sprintf("%s/kelola/desa/%d/pemesanan/%d", n.baseURL, b.VillageID, b.ID)
	traveler := recipient{email: b.TravelerEmail, name: b.ContactName}
	kind, dedupe := "booking."+event, fmt.Sprintf("booking:%d:%s", b.ID, event)

	toTraveler := func(template, link string) error {
		d := data
		d.RecipientName, d.Link = b.ContactName, link
		return n.send(ctx, tx, kind, dedupe, template, traveler, d)
	}
	toManagers := func(template string) error {
		ms, err := managers(ctx, q, b.VillageID)
		if err != nil {
			return err
		}
		for _, m := range ms {
			d := data
			d.RecipientName, d.Link = m.name, managerLink
			if err := n.send(ctx, tx, kind, dedupe, template, m, d); err != nil {
				return err
			}
		}
		return nil
	}

	switch event {
	case booking.EventRequested:
		if err := toManagers("booking_requested_manager"); err != nil {
			return err
		}
		return toTraveler("booking_requested_traveler", travelerLink)
	case booking.EventConfirmed:
		return toTraveler("booking_confirmed", travelerLink)
	case booking.EventRejected:
		return toTraveler("booking_rejected", travelerLink)
	case booking.EventCancelledByTraveler:
		return toManagers("booking_cancelled_by_traveler")
	case booking.EventCancelledByManager:
		return toTraveler("booking_cancelled_by_manager", travelerLink)
	case booking.EventExpired:
		return toTraveler("booking_expired", travelerLink)
	case booking.EventCompleted:
		return toTraveler("booking_completed", travelerLink+"/ulasan")
	}
	return fmt.Errorf("unknown booking event %q", event)
}

var villageTemplates = map[string]string{
	village.EventVerified:   "village_verified",
	village.EventRejected:   "village_rejected",
	village.EventSuspended:  "village_suspended",
	village.EventReinstated: "village_reinstated",
}

func (n notifier) VillageEvent(ctx context.Context, tx pgx.Tx, event string, villageID int64) error {
	template, ok := villageTemplates[event]
	if !ok {
		return fmt.Errorf("unknown village event %q", event)
	}
	q := db.New(tx)
	v, err := q.VillageNotificationData(ctx, villageID)
	if err != nil {
		return fmt.Errorf("village notification data: %w", err)
	}
	ms, err := managers(ctx, q, villageID)
	if err != nil {
		return err
	}
	// Status changes can repeat (suspend, reinstate, suspend…), so the dedupe key includes
	// the transaction's own clock to keep each occurrence while still deduplicating retries
	// of the same transaction.
	var txTime time.Time
	if err := tx.QueryRow(ctx, "SELECT now()").Scan(&txTime); err != nil {
		return err
	}
	dedupe := fmt.Sprintf("village:%d:%s:%d", v.ID, event, txTime.UnixMicro())
	for _, m := range ms {
		data := villageMail{
			RecipientName: m.name, VillageName: v.Name, Note: v.ReviewNote,
			Link: fmt.Sprintf("%s/kelola/desa/%d", n.baseURL, v.ID), PublicLink: fmt.Sprintf("%s/desa/%s", n.baseURL, v.Slug),
		}
		if err := n.send(ctx, tx, "village."+event, dedupe, template, m, data); err != nil {
			return err
		}
	}
	return nil
}

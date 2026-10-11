// Package booking owns availability, quotas and the booking lifecycle (docs/SPEC.md §4).
//
// It never imports tourpackage or village: package data comes through PackageLookup and
// manager checks through VillageAccess, both wired in internal/app.
package booking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/db"
	"desakita/internal/platform/form"
	"desakita/internal/platform/phone"
)

const (
	StatusPending   = "pending"
	StatusConfirmed = "confirmed"
	StatusRejected  = "rejected"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
	StatusCompleted = "completed"

	CancelledByTraveler = "traveler"
	CancelledByManager  = "manager"

	// MinLeadDays: the earliest bookable start date is today + 2 (the day after tomorrow),
	// giving managers roughly a day to respond.
	MinLeadDays = 2
	// WindowDays: the latest bookable start date is today + 90.
	WindowDays = 90
	// MaxPendingPerTraveler stops one account from hoarding quota with open requests.
	MaxPendingPerTraveler = 3

	maxPartySize = 1000
	maxQuota     = 1000
	expireBatch  = 200
)

var (
	ErrNotFound           = errors.New("booking: not found")
	ErrNotBookable        = errors.New("booking: package or village is not bookable")
	ErrDateOutOfWindow    = errors.New("booking: date outside booking window")
	ErrDateClosed         = errors.New("booking: date is closed")
	ErrSoldOut            = errors.New("booking: not enough quota")
	ErrPendingLimit       = errors.New("booking: too many pending requests")
	ErrInvalidTransition  = errors.New("booking: invalid status transition")
	ErrQuotaBelowReserved = errors.New("booking: quota below reserved")
	ErrNotCompleted       = errors.New("booking: not completed")
)

// PackageInfo is what booking needs to know about a package.
type PackageInfo struct {
	ID                int64
	VillageID         int64
	PricePerPersonIDR int64
	DefaultQuota      int32
	Active            bool
	VillageVerified   bool
}

// PackageLookup reads a package inside booking's transaction, share-locking the package and
// its village so neither can change status until the transaction ends. It returns
// ErrNotFound if the package doesn't exist.
type PackageLookup interface {
	LookupForBooking(ctx context.Context, tx pgx.Tx, packageID int64) (PackageInfo, error)
}

// VillageAccess answers whether a user manages a village.
type VillageAccess interface {
	IsManager(ctx context.Context, userID, villageID int64) (bool, error)
}

type Booking struct {
	ID            int64
	PackageID     int64
	VillageID     int64
	TravelerID    *int64
	StartDate     time.Time // a civil date at 00:00 UTC
	PartySize     int32
	Note          string
	TotalPriceIDR int64
	ContactName   string
	ContactPhone  string
	Status        string
	ExpiresAt     time.Time
	DecidedAt     *time.Time
	CompletedAt   *time.Time
	CancelledAt   *time.Time
	CancelledBy   string
	DecisionNote  string
	CreatedAt     time.Time
}

func fromRow(r db.Booking) Booking {
	b := Booking{
		ID: r.ID, PackageID: r.PackageID, VillageID: r.VillageID, TravelerID: r.TravelerID,
		StartDate: r.StartDate, PartySize: r.PartySize, Note: r.Note, TotalPriceIDR: r.TotalPriceIdr,
		ContactName: r.ContactName, ContactPhone: r.ContactPhone, Status: r.Status, ExpiresAt: r.ExpiresAt,
		DecidedAt: r.DecidedAt, CompletedAt: r.CompletedAt, CancelledAt: r.CancelledAt,
		DecisionNote: r.DecisionNote, CreatedAt: r.CreatedAt,
	}
	if r.CancelledBy != nil {
		b.CancelledBy = *r.CancelledBy
	}
	return b
}

type Config struct {
	PendingTTL time.Duration
	Location   *time.Location
	// Now defaults to time.Now; tests inject a fixed clock.
	Now func() time.Time
}

type Service struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	packages PackageLookup
	villages VillageAccess
	cfg      Config
}

func NewService(pool *pgxpool.Pool, packages PackageLookup, villages VillageAccess, cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{pool: pool, q: db.New(pool), packages: packages, villages: villages, cfg: cfg}
}

// Date returns the civil date y-m-d as stored in DATE columns (00:00 UTC).
func Date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// ParseDate parses "2006-01-02".
func ParseDate(s string) (time.Time, error) { return time.Parse(time.DateOnly, strings.TrimSpace(s)) }

// Today is the current civil date in the app's time zone.
func (s *Service) Today() time.Time {
	n := s.cfg.Now().In(s.cfg.Location)
	return Date(n.Year(), n.Month(), n.Day())
}

// BookableRange returns the first and last start dates a traveler may book today.
func (s *Service) BookableRange() (first, last time.Time) {
	today := s.Today()
	return today.AddDate(0, 0, MinLeadDays), today.AddDate(0, 0, WindowDays)
}

// expiresAt is min(now + TTL, start of the departure day in the app's time zone).
func (s *Service) expiresAt(now, start time.Time) time.Time {
	deadline := now.Add(s.cfg.PendingTTL)
	startOfDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, s.cfg.Location)
	if startOfDay.Before(deadline) {
		return startOfDay
	}
	return deadline
}

// CreateInput is a traveler's booking request.
type CreateInput struct {
	PackageID    int64
	StartDate    string // YYYY-MM-DD
	PartySize    string
	Note         string
	ContactName  string
	ContactPhone string
}

type createReq struct {
	start                  time.Time
	party                  int32
	note, name, phoneValue string
}

func validateCreate(in CreateInput) (createReq, error) {
	r := createReq{
		note:       strings.TrimSpace(in.Note),
		name:       strings.TrimSpace(in.ContactName),
		phoneValue: strings.TrimSpace(in.ContactPhone),
	}
	errs := form.Errors{}
	start, err := ParseDate(in.StartDate)
	if err != nil {
		errs.Add("start_date", "validation.date")
	}
	r.start = start
	n, err := strconv.Atoi(strings.TrimSpace(in.PartySize))
	if err != nil || n < 1 || n > maxPartySize {
		errs.Add("party_size", "validation.party_size")
	}
	r.party = int32(n)
	if utf8.RuneCountInString(r.note) > 1000 {
		errs.Add("note", "validation.too_long")
	}
	switch c := utf8.RuneCountInString(r.name); {
	case c == 0:
		errs.Add("contact_name", "validation.required")
	case c > 100:
		errs.Add("contact_name", "validation.too_long")
	}
	switch {
	case r.phoneValue == "":
		errs.Add("contact_phone", "validation.required")
	case !phone.Valid(r.phoneValue):
		errs.Add("contact_phone", "validation.phone")
	}
	return r, errs.Err()
}

// Create reserves quota and records a pending request. All checks and the reservation run in
// one transaction (SPEC §4 "Validasi saat membuat permintaan").
func (s *Service) Create(ctx context.Context, travelerID int64, in CreateInput) (Booking, error) {
	req, err := validateCreate(in)
	if err != nil {
		return Booking{}, err
	}
	first, last := s.BookableRange()
	if req.start.Before(first) || req.start.After(last) {
		return Booking{}, ErrDateOutOfWindow
	}
	now := s.cfg.Now()

	var out Booking
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockTravelerPending(ctx, "booking-pending:"+strconv.FormatInt(travelerID, 10)); err != nil {
			return fmt.Errorf("lock traveler: %w", err)
		}
		pending, err := q.CountPendingForTraveler(ctx, &travelerID)
		if err != nil {
			return fmt.Errorf("count pending: %w", err)
		}
		if pending >= MaxPendingPerTraveler {
			return ErrPendingLimit
		}

		pkg, err := s.packages.LookupForBooking(ctx, tx, in.PackageID)
		if err != nil {
			return err
		}
		if !pkg.Active || !pkg.VillageVerified {
			return ErrNotBookable
		}

		if err := q.EnsureAvailability(ctx, db.EnsureAvailabilityParams{PackageID: pkg.ID, Date: req.start, Quota: pkg.DefaultQuota}); err != nil {
			return fmt.Errorf("ensure availability: %w", err)
		}
		_, err = q.ReserveQuota(ctx, db.ReserveQuotaParams{PackageID: pkg.ID, Date: req.start, PartySize: req.party})
		if errors.Is(err, pgx.ErrNoRows) {
			av, gerr := q.GetAvailability(ctx, db.GetAvailabilityParams{PackageID: pkg.ID, Date: req.start})
			if gerr != nil {
				return fmt.Errorf("get availability: %w", gerr)
			}
			if av.IsClosed {
				return ErrDateClosed
			}
			return ErrSoldOut
		}
		if err != nil {
			return fmt.Errorf("reserve quota: %w", err)
		}

		row, err := q.InsertBooking(ctx, db.InsertBookingParams{
			PackageID: pkg.ID, VillageID: pkg.VillageID, TravelerID: &travelerID, StartDate: req.start,
			PartySize: req.party, Note: req.note, TotalPriceIdr: pkg.PricePerPersonIDR * int64(req.party),
			ContactName: req.name, ContactPhone: req.phoneValue, ExpiresAt: s.expiresAt(now, req.start), Now: now,
		})
		if err != nil {
			return fmt.Errorf("insert booking: %w", err)
		}
		out = fromRow(row)
		return nil
	})
	return out, err
}

func (s *Service) get(ctx context.Context, q *db.Queries, id int64) (Booking, error) {
	row, err := q.GetBooking(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("get booking: %w", err)
	}
	return fromRow(row), nil
}

// ForTraveler returns the booking only if travelerID owns it.
func (s *Service) ForTraveler(ctx context.Context, travelerID, id int64) (Booking, error) {
	b, err := s.get(ctx, s.q, id)
	if err != nil {
		return Booking{}, err
	}
	if b.TravelerID == nil || *b.TravelerID != travelerID {
		return Booking{}, ErrNotFound
	}
	return b, nil
}

// ForManager returns the booking only if managerID manages its village.
func (s *Service) ForManager(ctx context.Context, managerID, id int64) (Booking, error) {
	b, err := s.get(ctx, s.q, id)
	if err != nil {
		return Booking{}, err
	}
	ok, err := s.villages.IsManager(ctx, managerID, b.VillageID)
	if err != nil {
		return Booking{}, fmt.Errorf("check manager: %w", err)
	}
	if !ok {
		return Booking{}, ErrNotFound
	}
	return b, nil
}

func (s *Service) ListForTraveler(ctx context.Context, travelerID int64) ([]Booking, error) {
	rows, err := s.q.ListBookingsForTraveler(ctx, &travelerID)
	if err != nil {
		return nil, fmt.Errorf("list bookings: %w", err)
	}
	return mapRows(rows), nil
}

// ListForVillage returns a village's bookings if managerID manages it, otherwise ErrNotFound.
func (s *Service) ListForVillage(ctx context.Context, managerID, villageID int64) ([]Booking, error) {
	ok, err := s.villages.IsManager(ctx, managerID, villageID)
	if err != nil {
		return nil, fmt.Errorf("check manager: %w", err)
	}
	if !ok {
		return nil, ErrNotFound
	}
	rows, err := s.q.ListBookingsForVillage(ctx, villageID)
	if err != nil {
		return nil, fmt.Errorf("list bookings: %w", err)
	}
	return mapRows(rows), nil
}

func mapRows(rows []db.Booking) []Booking {
	out := make([]Booking, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out
}

// transition runs fn (a conditional status UPDATE) and, if it matched and release is set,
// returns the booking's seats to availability, all in one transaction.
func (s *Service) transition(ctx context.Context, release bool, fn func(q *db.Queries) (db.Booking, error)) (Booking, error) {
	var out Booking
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := fn(q)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidTransition
		}
		if err != nil {
			return fmt.Errorf("update booking: %w", err)
		}
		if release {
			if err := releaseSeats(ctx, q, row); err != nil {
				return err
			}
		}
		out = fromRow(row)
		return nil
	})
	return out, err
}

func releaseSeats(ctx context.Context, q *db.Queries, b db.Booking) error {
	n, err := q.ReleaseQuota(ctx, db.ReleaseQuotaParams{PackageID: b.PackageID, Date: b.StartDate, PartySize: b.PartySize})
	if err != nil {
		return fmt.Errorf("release quota: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("release quota: no availability row for booking %d", b.ID)
	}
	return nil
}

// managed loads the booking and checks managerID manages its village (ErrNotFound otherwise).
func (s *Service) managed(ctx context.Context, managerID, id int64) error {
	_, err := s.ForManager(ctx, managerID, id)
	return err
}

// Confirm accepts a pending, unexpired request. Quota stays reserved.
func (s *Service) Confirm(ctx context.Context, managerID, id int64) (Booking, error) {
	if err := s.managed(ctx, managerID, id); err != nil {
		return Booking{}, err
	}
	now := s.cfg.Now()
	return s.transition(ctx, false, func(q *db.Queries) (db.Booking, error) {
		return q.ConfirmBooking(ctx, db.ConfirmBookingParams{ID: id, Now: now})
	})
}

// Reject declines a pending request and releases its quota.
func (s *Service) Reject(ctx context.Context, managerID, id int64, note string) (Booking, error) {
	if err := s.managed(ctx, managerID, id); err != nil {
		return Booking{}, err
	}
	note, err := decisionNote(note)
	if err != nil {
		return Booking{}, err
	}
	now := s.cfg.Now()
	return s.transition(ctx, true, func(q *db.Queries) (db.Booking, error) {
		return q.RejectBooking(ctx, db.RejectBookingParams{ID: id, Now: now, DecisionNote: note})
	})
}

// CancelByTraveler cancels the traveler's own pending or confirmed booking up to its start
// date and releases the quota.
func (s *Service) CancelByTraveler(ctx context.Context, travelerID, id int64, note string) (Booking, error) {
	if _, err := s.ForTraveler(ctx, travelerID, id); err != nil {
		return Booking{}, err
	}
	return s.cancel(ctx, id, CancelledByTraveler, note)
}

// CancelByManager cancels a pending or confirmed booking of the manager's village up to its
// start date and releases the quota.
func (s *Service) CancelByManager(ctx context.Context, managerID, id int64, note string) (Booking, error) {
	if err := s.managed(ctx, managerID, id); err != nil {
		return Booking{}, err
	}
	return s.cancel(ctx, id, CancelledByManager, note)
}

func (s *Service) cancel(ctx context.Context, id int64, by, note string) (Booking, error) {
	note, err := decisionNote(note)
	if err != nil {
		return Booking{}, err
	}
	now, today := s.cfg.Now(), s.Today()
	return s.transition(ctx, true, func(q *db.Queries) (db.Booking, error) {
		return q.CancelBooking(ctx, db.CancelBookingParams{ID: id, Now: now, Today: today, CancelledBy: by, DecisionNote: note})
	})
}

// Complete marks a confirmed booking as done on or after its start date. Quota stays used.
func (s *Service) Complete(ctx context.Context, managerID, id int64) (Booking, error) {
	if err := s.managed(ctx, managerID, id); err != nil {
		return Booking{}, err
	}
	now, today := s.cfg.Now(), s.Today()
	return s.transition(ctx, false, func(q *db.Queries) (db.Booking, error) {
		return q.CompleteBooking(ctx, db.CompleteBookingParams{ID: id, Now: now, Today: today})
	})
}

func decisionNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 500 {
		return "", form.Errors{"decision_note": "validation.too_long"}.Err()
	}
	return note, nil
}

// ExpireDue expires pending requests past expires_at and releases their quota. Returns how
// many were expired. Safe to run concurrently with itself and with manager actions.
func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	total := 0
	for {
		var n int
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			rows, err := q.ExpireDueBookings(ctx, db.ExpireDueBookingsParams{Now: s.cfg.Now(), BatchSize: expireBatch})
			if err != nil {
				return fmt.Errorf("expire bookings: %w", err)
			}
			for _, r := range rows {
				if err := releaseSeats(ctx, q, r); err != nil {
					return err
				}
			}
			n = len(rows)
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < expireBatch {
			return total, nil
		}
	}
}

// RunExpiry calls ExpireDue every interval until ctx is cancelled.
func (s *Service) RunExpiry(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n, err := s.ExpireDue(ctx); err != nil {
			if ctx.Err() == nil {
				logger.Error("expire bookings", "err", err)
			}
		} else if n > 0 {
			logger.Info("expired bookings", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Day is one date of a package's calendar.
type Day struct {
	Date     time.Time
	Quota    int32
	Reserved int32
	Closed   bool
}

// Remaining is the number of seats still bookable (0 when closed).
func (d Day) Remaining() int32 {
	if d.Closed || d.Reserved >= d.Quota {
		return 0
	}
	return d.Quota - d.Reserved
}

// Calendar returns every date in [from, to], filling dates without a row with the package's
// default quota.
func (s *Service) Calendar(ctx context.Context, packageID int64, defaultQuota int32, from, to time.Time) ([]Day, error) {
	rows, err := s.q.ListAvailability(ctx, db.ListAvailabilityParams{PackageID: packageID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, fmt.Errorf("list availability: %w", err)
	}
	byDate := make(map[time.Time]db.Availability, len(rows))
	for _, r := range rows {
		byDate[r.Date] = r
	}
	var days []Day
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if r, ok := byDate[d]; ok {
			days = append(days, Day{Date: d, Quota: r.Quota, Reserved: r.Reserved, Closed: r.IsClosed})
		} else {
			days = append(days, Day{Date: d, Quota: defaultQuota})
		}
	}
	return days, nil
}

// managePackageDate checks managerID manages the package's village and date is within
// today..today+WindowDays, then runs fn in a transaction with the availability row ensured.
func (s *Service) managePackageDate(ctx context.Context, managerID, packageID int64, date time.Time, fn func(q *db.Queries) error) error {
	today := s.Today()
	if date.Before(today) || date.After(today.AddDate(0, 0, WindowDays)) {
		return ErrDateOutOfWindow
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		pkg, err := s.packages.LookupForBooking(ctx, tx, packageID)
		if err != nil {
			return err
		}
		ok, err := s.villages.IsManager(ctx, managerID, pkg.VillageID)
		if err != nil {
			return fmt.Errorf("check manager: %w", err)
		}
		if !ok {
			return ErrNotFound
		}
		q := s.q.WithTx(tx)
		if err := q.EnsureAvailability(ctx, db.EnsureAvailabilityParams{PackageID: packageID, Date: date, Quota: pkg.DefaultQuota}); err != nil {
			return fmt.Errorf("ensure availability: %w", err)
		}
		return fn(q)
	})
}

// SetQuota changes one date's quota. It refuses to go below the seats already reserved.
func (s *Service) SetQuota(ctx context.Context, managerID, packageID int64, date time.Time, quota int) error {
	if quota < 0 || quota > maxQuota {
		return form.Errors{"quota": "validation.quota"}.Err()
	}
	return s.managePackageDate(ctx, managerID, packageID, date, func(q *db.Queries) error {
		_, err := q.SetQuota(ctx, db.SetQuotaParams{PackageID: packageID, Date: date, Quota: int32(quota)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrQuotaBelowReserved
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" { // CHECK backstop
			return ErrQuotaBelowReserved
		}
		return err
	})
}

// SetClosed opens or closes a date for new bookings. Existing bookings are not affected.
func (s *Service) SetClosed(ctx context.Context, managerID, packageID int64, date time.Time, closed bool) error {
	return s.managePackageDate(ctx, managerID, packageID, date, func(q *db.Queries) error {
		return q.SetClosed(ctx, db.SetClosedParams{PackageID: packageID, Date: date, IsClosed: closed})
	})
}

// CompletedForReview returns the traveler's completed booking inside tx with a share lock,
// for the review module. It returns ErrNotFound if the booking isn't the traveler's and
// ErrNotCompleted if it is theirs but not completed.
func (s *Service) CompletedForReview(ctx context.Context, tx pgx.Tx, id, travelerID int64) (Booking, error) {
	row, err := s.q.WithTx(tx).GetCompletedBookingForReview(ctx, db.GetCompletedBookingForReviewParams{ID: id, TravelerID: &travelerID})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, ferr := s.ForTraveler(ctx, travelerID, id); ferr != nil {
			return Booking{}, ferr
		}
		return Booking{}, ErrNotCompleted
	}
	if err != nil {
		return Booking{}, fmt.Errorf("get completed booking: %w", err)
	}
	return Booking{ID: row.ID, PackageID: row.PackageID, VillageID: row.VillageID, Status: StatusCompleted}, nil
}

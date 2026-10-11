// Package review handles ratings and reviews: one per completed booking, published
// immediately and moderated afterwards (user reports, admin hide/restore). See SPEC §12.
//
// It never imports booking or tourpackage: booking data comes through the Bookings
// interface, wired in internal/app.
package review

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/db"
	"desakita/internal/platform/audit"
	"desakita/internal/platform/form"
)

const (
	StatusPublished = "published"
	StatusHidden    = "hidden"

	// PageSize is how many reviews a package page shows at a time.
	PageSize = 10
)

var (
	ErrNotFound            = errors.New("review: not found")
	ErrNotEligible         = errors.New("review: booking is not completed")
	ErrAlreadyReviewed     = errors.New("review: booking already reviewed")
	ErrInvalidStatusChange = errors.New("review: invalid status change")
)

// BookingInfo is what reviews need to know about a booking.
type BookingInfo struct {
	ID, PackageID, VillageID int64
	Status                   string
	PackageName, VillageName string
	VillageSlug              string
	StartDate                time.Time
}

// Bookings gives access to the traveler's bookings.
type Bookings interface {
	// Summary returns the traveler's booking (any status), or ErrNotFound.
	Summary(ctx context.Context, bookingID, travelerID int64) (BookingInfo, error)
	// ForReview returns the booking inside tx with a share lock if it belongs to the
	// traveler and is completed; ErrNotFound if not theirs, ErrNotEligible otherwise.
	ForReview(ctx context.Context, tx pgx.Tx, bookingID, travelerID int64) (BookingInfo, error)
}

type Service struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	bookings Bookings
}

func NewService(pool *pgxpool.Pool, bookings Bookings) *Service {
	return &Service{pool: pool, q: db.New(pool), bookings: bookings}
}

// Input is the raw review form.
type Input struct {
	Rating, Body string
}

func (in Input) validate() (int16, string, error) {
	errs := form.Errors{}
	r, err := strconv.Atoi(strings.TrimSpace(in.Rating))
	if err != nil || r < 1 || r > 5 {
		errs.Add("rating", "review.error.rating")
	}
	body := strings.TrimSpace(in.Body)
	if n := utf8.RuneCountInString(body); n < 10 || n > 2000 {
		errs.Add("body", "review.error.body_length")
	}
	return int16(r), body, errs.Err()
}

// Create stores the author's review of their completed booking.
func (s *Service) Create(ctx context.Context, authorID, bookingID int64, in Input) error {
	rating, body, err := in.validate()
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		b, err := s.bookings.ForReview(ctx, tx, bookingID, authorID)
		if err != nil {
			return err
		}
		_, err = s.q.WithTx(tx).InsertReview(ctx, db.InsertReviewParams{
			BookingID: b.ID, PackageID: b.PackageID, VillageID: b.VillageID, AuthorID: &authorID,
			Rating: rating, Body: body,
		})
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "reviews_booking_id_key" {
		return ErrAlreadyReviewed
	}
	return err
}

// Own is the author's view of their review.
type Own struct {
	ID             int64
	Rating         int16
	Body           string
	Status         string
	ModerationNote string
	CreatedAt      time.Time
}

// ForBooking returns the traveler's booking and their review of it (nil if none).
func (s *Service) ForBooking(ctx context.Context, authorID, bookingID int64) (BookingInfo, *Own, error) {
	b, err := s.bookings.Summary(ctx, bookingID, authorID)
	if err != nil {
		return BookingInfo{}, nil, err
	}
	r, err := s.q.GetReviewByBooking(ctx, bookingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, nil, nil
	}
	if err != nil {
		return BookingInfo{}, nil, fmt.Errorf("get review: %w", err)
	}
	if r.AuthorID == nil || *r.AuthorID != authorID {
		return b, nil, nil
	}
	return b, &Own{ID: r.ID, Rating: r.Rating, Body: r.Body, Status: r.Status, ModerationNote: r.ModerationNote, CreatedAt: r.CreatedAt}, nil
}

// Delete removes the author's own review.
func (s *Service) Delete(ctx context.Context, authorID, bookingID int64) error {
	n, err := s.q.DeleteOwnReview(ctx, db.DeleteOwnReviewParams{BookingID: bookingID, AuthorID: &authorID})
	if err != nil {
		return fmt.Errorf("delete review: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Summary aggregates a package's published reviews.
type Summary struct {
	Average float64
	Count   int64
	// ByRating[i] is the number of (i+1)-star reviews.
	ByRating [5]int64
}

// Share returns the percentage (0–100) of reviews with the given star rating.
func (s Summary) Share(stars int) int {
	if s.Count == 0 || stars < 1 || stars > 5 {
		return 0
	}
	return int(math.Round(float64(s.ByRating[stars-1]) * 100 / float64(s.Count)))
}

func (s *Service) PackageSummary(ctx context.Context, packageID int64) (Summary, error) {
	rows, err := s.q.PackageRatingDistribution(ctx, packageID)
	if err != nil {
		return Summary{}, fmt.Errorf("rating distribution: %w", err)
	}
	var out Summary
	var sum int64
	for _, r := range rows {
		if r.Rating >= 1 && r.Rating <= 5 {
			out.ByRating[r.Rating-1] = r.Total
			out.Count += r.Total
			sum += int64(r.Rating) * r.Total
		}
	}
	if out.Count > 0 {
		out.Average = float64(sum) / float64(out.Count)
	}
	return out, nil
}

// Public is a published review as shown on a package page.
type Public struct {
	ID        int64
	Rating    int16
	Body      string
	CreatedAt time.Time
	// Author is "First L." or "" for a deleted account.
	Author string
}

// DisplayName shortens a full name to first name + last initial: "Sari Wisatawan" → "Sari W.".
func DisplayName(name string) string {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	last, _ := utf8.DecodeRuneInString(parts[len(parts)-1])
	return parts[0] + " " + strings.ToUpper(string(last)) + "."
}

// PackageReviews returns one page (1-based) of published reviews, newest first.
func (s *Service) PackageReviews(ctx context.Context, packageID int64, page int) ([]Public, bool, error) {
	if page < 1 {
		page = 1
	}
	rows, err := s.q.ListPublishedReviewsForPackage(ctx, db.ListPublishedReviewsForPackageParams{
		PackageID: packageID, PageSize: PageSize + 1, PageOffset: int32((page - 1) * PageSize),
	})
	if err != nil {
		return nil, false, fmt.Errorf("list reviews: %w", err)
	}
	hasNext := len(rows) > PageSize
	if hasNext {
		rows = rows[:PageSize]
	}
	out := make([]Public, len(rows))
	for i, r := range rows {
		out[i] = Public{ID: r.ID, Rating: r.Rating, Body: r.Body, CreatedAt: r.CreatedAt}
		if r.AuthorName != nil {
			out[i].Author = DisplayName(*r.AuthorName)
		}
	}
	return out, hasNext, nil
}

// ModerationItem is a review in the admin list.
type ModerationItem struct {
	ID             int64
	Rating         int16
	Body           string
	Status         string
	ModerationNote string
	ModeratedAt    *time.Time
	CreatedAt      time.Time
	PackageID      int64
	PackageName    string
	VillageName    string
	VillageSlug    string
	AuthorEmail    string
	OpenReports    int64
}

func (s *Service) ForModeration(ctx context.Context, status string, reportedOnly bool) ([]ModerationItem, error) {
	if status != StatusPublished && status != StatusHidden {
		return nil, ErrNotFound
	}
	rows, err := s.q.ListReviewsForModeration(ctx, db.ListReviewsForModerationParams{Status: status, ReportedOnly: reportedOnly})
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	out := make([]ModerationItem, len(rows))
	for i, r := range rows {
		out[i] = ModerationItem{
			ID: r.ID, Rating: r.Rating, Body: r.Body, Status: r.Status, ModerationNote: r.ModerationNote,
			ModeratedAt: r.ModeratedAt, CreatedAt: r.CreatedAt, PackageID: r.PackageID, PackageName: r.PackageName,
			VillageName: r.VillageName, VillageSlug: r.VillageSlug, AuthorEmail: r.AuthorEmail, OpenReports: r.OpenReports,
		}
	}
	return out, nil
}

// Counts returns the number of reviews per status.
func (s *Service) Counts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.q.CountReviewsByStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("count reviews: %w", err)
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Status] = r.Total
	}
	return out, nil
}

func (s *Service) setStatus(ctx context.Context, adminID, id int64, from, to, note, action string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		current, err := q.LockReviewStatus(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock review: %w", err)
		}
		if current != from {
			return ErrInvalidStatusChange
		}
		n, err := q.SetReviewStatus(ctx, db.SetReviewStatusParams{
			ID: id, FromStatus: from, ToStatus: to, ModerationNote: note, AdminID: &adminID,
		})
		if err != nil {
			return fmt.Errorf("set review status: %w", err)
		}
		if n == 0 {
			return ErrInvalidStatusChange
		}
		return audit.Record(ctx, tx, audit.Entry{
			ActorID: &adminID, Action: action, TargetType: audit.TargetReview, TargetID: id,
			From: from, To: to, Note: note,
		})
	})
}

// Hide takes a published review down; the reason is shown to its author and audited.
func (s *Service) Hide(ctx context.Context, adminID, id int64, reason string) error {
	reason = strings.TrimSpace(reason)
	switch n := utf8.RuneCountInString(reason); {
	case n == 0:
		return form.Errors{"note": "validation.required"}.Err()
	case n > 1000:
		return form.Errors{"note": "validation.too_long"}.Err()
	}
	return s.setStatus(ctx, adminID, id, StatusPublished, StatusHidden, reason, audit.ReviewHide)
}

// Restore publishes a hidden review again.
func (s *Service) Restore(ctx context.Context, adminID, id int64) error {
	return s.setStatus(ctx, adminID, id, StatusHidden, StatusPublished, "", audit.ReviewRestore)
}

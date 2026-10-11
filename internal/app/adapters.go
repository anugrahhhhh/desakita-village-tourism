package app

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"desakita/internal/booking"
	"desakita/internal/review"
	"desakita/internal/tourpackage"
	"desakita/internal/village"
)

// packageLookup adapts tourpackage to booking.PackageLookup so booking never imports it.
type packageLookup struct{ packages *tourpackage.Service }

func (a packageLookup) LookupForBooking(ctx context.Context, tx pgx.Tx, packageID int64) (booking.PackageInfo, error) {
	info, err := a.packages.BookingInfo(ctx, tx, packageID)
	if errors.Is(err, tourpackage.ErrNotFound) {
		return booking.PackageInfo{}, booking.ErrNotFound
	}
	if err != nil {
		return booking.PackageInfo{}, err
	}
	return booking.PackageInfo{
		ID:                info.ID,
		VillageID:         info.VillageID,
		PricePerPersonIDR: info.PricePerPersonIDR,
		DefaultQuota:      info.DefaultQuota,
		Active:            info.IsActive,
		VillageVerified:   info.VillageStatus == village.StatusVerified,
	}, nil
}

// village.Service satisfies booking.VillageAccess directly.
var _ booking.VillageAccess = (*village.Service)(nil)

// catalog adapts tourpackage and village to booking.Catalog.
type catalog struct {
	packages *tourpackage.Service
	villages *village.Service
}

func toBookingSummary(s tourpackage.Summary) booking.PackageSummary {
	return booking.PackageSummary{
		ID: s.ID, VillageID: s.VillageID, Name: s.Name, DefaultQuota: s.DefaultQuota,
		PricePerPersonIDR: s.PricePerPersonIDR,
		Bookable:          s.IsActive && s.VillageStatus == village.StatusVerified,
		VillageName:       s.VillageName, VillageSlug: s.VillageSlug,
		VillagePhone: s.VillagePhone, VillageWhatsApp: s.VillageWhatsApp,
	}
}

func (c catalog) Package(ctx context.Context, id int64) (booking.PackageSummary, error) {
	s, err := c.packages.Summary(ctx, id)
	if errors.Is(err, tourpackage.ErrNotFound) {
		return booking.PackageSummary{}, booking.ErrNotFound
	}
	if err != nil {
		return booking.PackageSummary{}, err
	}
	return toBookingSummary(s), nil
}

func (c catalog) VillagePackages(ctx context.Context, villageID int64) ([]booking.PackageSummary, error) {
	list, err := c.packages.VillageSummaries(ctx, villageID)
	if err != nil {
		return nil, err
	}
	out := make([]booking.PackageSummary, len(list))
	for i, s := range list {
		out[i] = toBookingSummary(s)
	}
	return out, nil
}

func (c catalog) Village(ctx context.Context, villageID int64) (booking.VillageSummary, error) {
	v, err := c.villages.Get(ctx, villageID)
	if errors.Is(err, village.ErrNotFound) {
		return booking.VillageSummary{}, booking.ErrNotFound
	}
	if err != nil {
		return booking.VillageSummary{}, err
	}
	return booking.VillageSummary{ID: v.ID, Name: v.Name, Slug: v.Slug}, nil
}

// reviewBookings adapts booking (+ catalog for names) to review.Bookings.
type reviewBookings struct {
	bookings *booking.Service
	catalog  catalog
}

func (a reviewBookings) Summary(ctx context.Context, bookingID, travelerID int64) (review.BookingInfo, error) {
	b, err := a.bookings.ForTraveler(ctx, travelerID, bookingID)
	if errors.Is(err, booking.ErrNotFound) {
		return review.BookingInfo{}, review.ErrNotFound
	}
	if err != nil {
		return review.BookingInfo{}, err
	}
	p, err := a.catalog.Package(ctx, b.PackageID)
	if err != nil {
		return review.BookingInfo{}, err
	}
	return review.BookingInfo{
		ID: b.ID, PackageID: b.PackageID, VillageID: b.VillageID, Status: b.Status,
		PackageName: p.Name, VillageName: p.VillageName, VillageSlug: p.VillageSlug, StartDate: b.StartDate,
	}, nil
}

func (a reviewBookings) ForReview(ctx context.Context, tx pgx.Tx, bookingID, travelerID int64) (review.BookingInfo, error) {
	b, err := a.bookings.CompletedForReview(ctx, tx, bookingID, travelerID)
	switch {
	case errors.Is(err, booking.ErrNotFound):
		return review.BookingInfo{}, review.ErrNotFound
	case errors.Is(err, booking.ErrNotCompleted):
		return review.BookingInfo{}, review.ErrNotEligible
	case err != nil:
		return review.BookingInfo{}, err
	}
	return review.BookingInfo{ID: b.ID, PackageID: b.PackageID, VillageID: b.VillageID, Status: b.Status}, nil
}

// packageReviews adapts review to tourpackage.Reviews.
type packageReviews struct{ reviews *review.Service }

func (a packageReviews) PackageSummary(ctx context.Context, packageID int64) (tourpackage.ReviewSummary, error) {
	s, err := a.reviews.PackageSummary(ctx, packageID)
	if err != nil {
		return tourpackage.ReviewSummary{}, err
	}
	out := tourpackage.ReviewSummary{Average: s.Average, Count: s.Count, ByRating: s.ByRating}
	for i := range out.SharePct {
		out.SharePct[i] = s.Share(i + 1)
	}
	return out, nil
}

func (a packageReviews) PackageReviews(ctx context.Context, packageID int64, page int) ([]tourpackage.ReviewItem, bool, error) {
	items, next, err := a.reviews.PackageReviews(ctx, packageID, page)
	if err != nil {
		return nil, false, err
	}
	out := make([]tourpackage.ReviewItem, len(items))
	for i, r := range items {
		out[i] = tourpackage.ReviewItem{ID: r.ID, Rating: r.Rating, Body: r.Body, CreatedAt: r.CreatedAt, Author: r.Author}
	}
	return out, next, nil
}

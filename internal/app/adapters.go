package app

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"desakita/internal/booking"
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

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

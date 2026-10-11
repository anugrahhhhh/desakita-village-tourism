package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"desakita/internal/db"
)

// StatsPeriods are the selectable periods in days; the first is the default (SPEC §16).
var StatsPeriods = []int{90, 30, 365}

// readOnlySnapshot reads every statistic from one consistent snapshot.
var readOnlySnapshot = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// statsMonths is how many months the visitor chart covers, ending with the current month.
const statsMonths = 12

// Stats summarises a village's bookings for its managers.
type Stats struct {
	Days     int       // period length
	From, To time.Time // civil dates, inclusive
	Requests RequestStats
	Visits   VisitStats
	Months   []MonthStat // statsMonths entries, oldest first, zero-filled
	Packages []PackageStat
}

// RequestStats counts booking requests created in the period.
type RequestStats struct {
	Total, Pending, Accepted, Rejected, Expired, Cancelled, Responded int64
	// MedianResponse is the median time from request to the manager's decision.
	MedianResponse time.Duration
}

// ResponseRate is the share (0–100) of decided-or-expired requests the managers answered.
func (r RequestStats) ResponseRate() int { return percent(r.Responded, r.Responded+r.Expired) }

// AcceptanceRate is the share (0–100) of answered requests that were accepted.
func (r RequestStats) AcceptanceRate() int { return percent(r.Accepted, r.Responded) }

// HasDecisions reports whether any request was answered (rates are meaningless otherwise).
func (r RequestStats) HasDecisions() bool { return r.Responded+r.Expired > 0 }

// VisitStats covers completed visits whose departure date falls in the period.
type VisitStats struct {
	Visits, Visitors, Revenue int64
}

type MonthStat struct {
	Month                     time.Time
	Visits, Visitors, Revenue int64
}

type PackageStat struct {
	ID                          int64
	Name                        string
	Requests, Visitors, Revenue int64
}

func percent(part, whole int64) int {
	if whole == 0 {
		return 0
	}
	return int((part*100 + whole/2) / whole)
}

// VillageStats returns statistics for villageID over the last days days (one of
// StatsPeriods) if managerID manages it, otherwise ErrNotFound.
func (s *Service) VillageStats(ctx context.Context, managerID, villageID int64, days int) (Stats, error) {
	ok, err := s.villages.IsManager(ctx, managerID, villageID)
	if err != nil {
		return Stats{}, fmt.Errorf("check manager: %w", err)
	}
	if !ok {
		return Stats{}, ErrNotFound
	}
	today := s.Today()
	st := Stats{Days: days, From: today.AddDate(0, 0, -(days - 1)), To: today}
	// Business dates are Jakarta days: the period starts at local midnight of From.
	fromTime := time.Date(st.From.Year(), st.From.Month(), st.From.Day(), 0, 0, 0, 0, s.cfg.Location)
	toTime := s.cfg.Now()
	toDate := today.AddDate(0, 0, 1) // exclusive

	// One snapshot, so the figures on the page agree with each other.
	tx, err := s.pool.BeginTx(ctx, readOnlySnapshot)
	if err != nil {
		return Stats{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)

	req, err := q.VillageRequestStats(ctx, db.VillageRequestStatsParams{VillageID: villageID, FromTime: fromTime, ToTime: toTime})
	if err != nil {
		return Stats{}, fmt.Errorf("request stats: %w", err)
	}
	st.Requests = RequestStats{
		Total: req.Total, Pending: req.Pending, Accepted: req.Accepted, Rejected: req.Rejected,
		Expired: req.Expired, Cancelled: req.Cancelled, Responded: req.Responded,
		MedianResponse: time.Duration(req.MedianResponseSeconds * float64(time.Second)).Round(time.Minute),
	}

	vis, err := q.VillageVisitStats(ctx, db.VillageVisitStatsParams{VillageID: villageID, FromDate: st.From, ToDate: toDate})
	if err != nil {
		return Stats{}, fmt.Errorf("visit stats: %w", err)
	}
	st.Visits = VisitStats{Visits: vis.Visits, Visitors: vis.Visitors, Revenue: vis.Revenue}

	firstMonth := Date(today.Year(), today.Month(), 1).AddDate(0, -(statsMonths - 1), 0)
	months, err := q.VillageMonthlyVisitors(ctx, db.VillageMonthlyVisitorsParams{VillageID: villageID, FromDate: firstMonth, ToDate: toDate})
	if err != nil {
		return Stats{}, fmt.Errorf("monthly stats: %w", err)
	}
	byMonth := map[time.Time]db.VillageMonthlyVisitorsRow{}
	for _, m := range months {
		byMonth[Date(m.Month.Year(), m.Month.Month(), 1)] = m
	}
	for i := range statsMonths {
		m := firstMonth.AddDate(0, i, 0)
		row := byMonth[m]
		st.Months = append(st.Months, MonthStat{Month: m, Visits: row.Visits, Visitors: row.Visitors, Revenue: row.Revenue})
	}

	pkgs, err := q.VillagePackageStats(ctx, db.VillagePackageStatsParams{
		VillageID: villageID, FromTime: fromTime, ToTime: toTime, FromDate: st.From, ToDate: toDate,
	})
	if err != nil {
		return Stats{}, fmt.Errorf("package stats: %w", err)
	}
	for _, p := range pkgs {
		st.Packages = append(st.Packages, PackageStat{ID: p.ID, Name: p.Name, Requests: p.Requests, Visitors: p.Visitors, Revenue: p.Revenue})
	}
	return st, tx.Commit(ctx)
}

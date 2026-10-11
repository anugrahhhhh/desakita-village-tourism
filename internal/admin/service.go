// Package admin covers platform moderation: verifying villages and handling user reports.
// Village status changes are delegated to the village module.
package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/db"
	"desakita/internal/platform/form"
)

const (
	TargetVillage = "village"
	TargetPackage = "package"

	StatusResolved  = "resolved"
	StatusDismissed = "dismissed"
)

var ErrNotFound = errors.New("admin: not found")

type Service struct {
	q *db.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: db.New(pool)}
}

// CreateReport records a user's report about a village or package.
func (s *Service) CreateReport(ctx context.Context, reporterID int64, targetType, targetID, reason string) error {
	reason = strings.TrimSpace(reason)
	errs := form.Errors{}
	if targetType != TargetVillage && targetType != TargetPackage {
		errs.Add("target", "validation.invalid_choice")
	}
	id, err := strconv.ParseInt(targetID, 10, 64)
	if err != nil {
		errs.Add("target", "validation.invalid_choice")
	}
	switch n := utf8.RuneCountInString(reason); {
	case n == 0:
		errs.Add("reason", "validation.required")
	case n > 1000:
		errs.Add("reason", "validation.too_long")
	}
	if err := errs.Err(); err != nil {
		return err
	}
	exists, err := s.q.ReportTargetExists(ctx, db.ReportTargetExistsParams{TargetType: targetType, TargetID: id})
	if err != nil {
		return fmt.Errorf("check report target: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := s.q.CreateReport(ctx, db.CreateReportParams{ReporterID: &reporterID, TargetType: targetType, TargetID: id, Reason: reason}); err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	return nil
}

type Report struct {
	ID                       int64
	TargetType               string
	TargetID                 int64
	Reason                   string
	CreatedAt                time.Time
	ReporterEmail            string
	VillageName, VillageSlug string
	PackageName              string
}

func (s *Service) OpenReports(ctx context.Context) ([]Report, error) {
	rows, err := s.q.ListOpenReports(ctx)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	out := make([]Report, len(rows))
	for i, r := range rows {
		out[i] = Report{
			ID: r.ID, TargetType: r.TargetType, TargetID: r.TargetID, Reason: r.Reason, CreatedAt: r.CreatedAt,
			ReporterEmail: r.ReporterEmail, VillageName: r.VillageName, VillageSlug: r.VillageSlug, PackageName: r.PackageName,
		}
	}
	return out, nil
}

func (s *Service) CountOpenReports(ctx context.Context) (int64, error) {
	return s.q.CountOpenReports(ctx)
}

// ResolveReport closes an open report as resolved or dismissed.
func (s *Service) ResolveReport(ctx context.Context, adminID, id int64, status string) error {
	if status != StatusResolved && status != StatusDismissed {
		return fmt.Errorf("admin: invalid report status %q", status)
	}
	n, err := s.q.ResolveReport(ctx, db.ResolveReportParams{ID: id, Status: status, ResolvedBy: &adminID})
	if err != nil {
		return fmt.Errorf("resolve report: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

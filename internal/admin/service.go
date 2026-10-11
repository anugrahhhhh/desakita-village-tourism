// Package admin covers platform moderation: verifying villages, handling user reports and
// reading the audit log. Village status changes are delegated to the village module.
package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/db"
	"desakita/internal/platform/audit"
	"desakita/internal/platform/form"
)

const (
	TargetVillage = "village"
	TargetPackage = "package"
	TargetReview  = "review"

	StatusOpen      = "open"
	StatusResolved  = "resolved"
	StatusDismissed = "dismissed"
)

var ErrNotFound = errors.New("admin: not found")

type Service struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: db.New(pool)}
}

// CreateReport records a user's report about a village or package.
func (s *Service) CreateReport(ctx context.Context, reporterID int64, targetType, targetID, reason string) error {
	reason = strings.TrimSpace(reason)
	errs := form.Errors{}
	if targetType != TargetVillage && targetType != TargetPackage && targetType != TargetReview {
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
	Status                   string
	CreatedAt                time.Time
	ResolvedAt               *time.Time
	ResolutionNote           string
	ReporterEmail            string
	ResolverName             string
	VillageID                int64
	VillageName, VillageSlug string
	PackageName              string
	// For review targets: the reviewed package and the review text.
	ReviewPackageID int64
	ReviewBody      string
}

// ReportStatuses are the report tabs, in display order.
var ReportStatuses = []string{StatusOpen, StatusResolved, StatusDismissed}

// Reports lists reports with the given status: open ones oldest first, closed newest first.
func (s *Service) Reports(ctx context.Context, status string) ([]Report, error) {
	valid := false
	for _, st := range ReportStatuses {
		valid = valid || st == status
	}
	if !valid {
		return nil, ErrNotFound
	}
	rows, err := s.q.ListReports(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	out := make([]Report, len(rows))
	for i, r := range rows {
		out[i] = Report{
			ID: r.ID, TargetType: r.TargetType, TargetID: r.TargetID, Reason: r.Reason, Status: r.Status,
			CreatedAt: r.CreatedAt, ResolvedAt: r.ResolvedAt, ResolutionNote: r.ResolutionNote,
			ReporterEmail: r.ReporterEmail, ResolverName: r.ResolverName, VillageID: r.VillageID,
			VillageName: r.VillageName, VillageSlug: r.VillageSlug, PackageName: r.PackageName,
			ReviewPackageID: r.ReviewPackageID, ReviewBody: r.ReviewBody,
		}
	}
	return out, nil
}

func (s *Service) CountOpenReports(ctx context.Context) (int64, error) {
	return s.q.CountOpenReports(ctx)
}

// CountOpenReportsForVillage counts open reports about the village or its packages.
func (s *Service) CountOpenReportsForVillage(ctx context.Context, villageID int64) (int64, error) {
	return s.q.CountOpenReportsForVillage(ctx, villageID)
}

// ResolveReport closes an open report as resolved or dismissed, with an optional note, and
// audits the decision in the same transaction.
func (s *Service) ResolveReport(ctx context.Context, adminID, id int64, status, note string) error {
	action := map[string]string{StatusResolved: audit.ReportResolve, StatusDismissed: audit.ReportDismiss}[status]
	if action == "" {
		return fmt.Errorf("admin: invalid report status %q", status)
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 1000 {
		return form.Errors{"note": "validation.too_long"}.Err()
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		n, err := s.q.WithTx(tx).ResolveReport(ctx, db.ResolveReportParams{ID: id, Status: status, ResolvedBy: &adminID, ResolutionNote: note})
		if err != nil {
			return fmt.Errorf("resolve report: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, tx, audit.Entry{
			ActorID: &adminID, Action: action, TargetType: audit.TargetReport, TargetID: id,
			From: StatusOpen, To: status, Note: note,
		})
	})
}

// AuditLog reads the audit log newest first.
func (s *Service) AuditLog(ctx context.Context, f audit.Filter) ([]audit.Item, error) {
	return audit.List(ctx, s.pool, f)
}

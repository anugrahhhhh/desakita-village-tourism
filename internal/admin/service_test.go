package admin_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/admin"
	"desakita/internal/platform/audit"
	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/form"
)

type env struct {
	pool                   *pgxpool.Pool
	svc                    *admin.Service
	user, adminID, village int64
}

func newEnv(t *testing.T) env {
	t.Helper()
	pool := dbtest.New(t)
	ctx := context.Background()
	e := env{pool: pool, svc: admin.NewService(pool)}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, name) VALUES ('r@example.com', 'x', 'R') RETURNING id").Scan(&e.user))
	must(pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, name, is_admin) VALUES ('a@example.com', 'x', 'Admin Satu', true) RETURNING id").Scan(&e.adminID))
	must(pool.QueryRow(ctx, `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', 'P') RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'R' FROM p RETURNING id),
		     d AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'D' FROM r RETURNING id)
		INSERT INTO villages (slug, name, district_id, contact_phone) SELECT 'desa-x', 'Desa X', id, '0811' FROM d RETURNING id`).Scan(&e.village))
	return e
}

func TestReports(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	vid := strconv.FormatInt(e.village, 10)

	var verr *form.ValidationError
	if err := e.svc.CreateReport(ctx, e.user, "village", vid, " "); !errors.As(err, &verr) {
		t.Errorf("empty reason: %v", err)
	}
	if err := e.svc.CreateReport(ctx, e.user, "user", vid, "x"); !errors.As(err, &verr) {
		t.Errorf("bad target type: %v", err)
	}
	if err := e.svc.CreateReport(ctx, e.user, "package", "999999", "x"); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("missing target: %v", err)
	}
	if err := e.svc.CreateReport(ctx, e.user, "village", vid, "Nomor kontak palsu"); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.CountOpenReportsForVillage(ctx, e.village); n != 1 {
		t.Errorf("open reports for village = %d", n)
	}

	open, err := e.svc.Reports(ctx, admin.StatusOpen)
	if err != nil || len(open) != 1 || open[0].VillageName != "Desa X" || open[0].VillageID != e.village || open[0].ReporterEmail != "r@example.com" {
		t.Fatalf("open reports: %+v %v", open, err)
	}
	if err := e.svc.ResolveReport(ctx, e.adminID, open[0].ID, admin.StatusDismissed, strings.Repeat("x", 1001)); !errors.As(err, &verr) {
		t.Errorf("1001-char note: %v", err)
	}
	if err := e.svc.ResolveReport(ctx, e.adminID, open[0].ID, admin.StatusDismissed, "Sudah dicek, nomor benar"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ResolveReport(ctx, e.adminID, open[0].ID, admin.StatusResolved, ""); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("resolving a closed report: %v", err)
	}
	closed, _ := e.svc.Reports(ctx, admin.StatusDismissed)
	if len(closed) != 1 || closed[0].ResolutionNote != "Sudah dicek, nomor benar" || closed[0].ResolverName != "Admin Satu" || closed[0].ResolvedAt == nil {
		t.Errorf("dismissed report: %+v", closed)
	}
	if _, err := e.svc.Reports(ctx, "everything"); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("invalid status: %v", err)
	}

	entries, _ := e.svc.AuditLog(ctx, audit.Filter{TargetType: audit.TargetReport})
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1 (failed attempts must not be audited)", len(entries))
	}
	got := entries[0]
	if got.Action != audit.ReportDismiss || got.ActorName != "Admin Satu" || got.From != "open" || got.To != "dismissed" || got.Note != "Sudah dicek, nomor benar" || got.ActionKey() == "" {
		t.Errorf("audit entry: %+v", got)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := audit.Record(ctx, e.pool, audit.Entry{ActorID: &e.adminID, Action: audit.VillageVerify, TargetType: audit.TargetVillage, TargetID: e.village}); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"UPDATE audit_log SET note = 'diubah'", "DELETE FROM audit_log", "TRUNCATE audit_log"} {
		if _, err := e.pool.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v, want append-only rejection", stmt, err)
		}
	}
	// The actor's name is a snapshot: deleting the admin keeps the entry intact.
	if _, err := e.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", e.adminID); err != nil {
		t.Fatal(err)
	}
	items, _ := audit.List(ctx, e.pool, audit.Filter{})
	if len(items) != 1 || items[0].ActorName != "Admin Satu" || items[0].ActorID == nil || *items[0].ActorID != e.adminID {
		t.Errorf("entry after actor deletion: %+v", items)
	}
}

func TestAuditRolledBackWithTransaction(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Record(ctx, tx, audit.Entry{Action: audit.VillageVerify, TargetType: audit.TargetVillage, TargetID: e.village}); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx) //nolint:errcheck
	if items, _ := audit.List(ctx, e.pool, audit.Filter{}); len(items) != 0 {
		t.Errorf("entry survived rollback: %+v", items)
	}
}

func TestAuditFilters(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	rec := func(action, target string, id int64) {
		t.Helper()
		if err := audit.Record(ctx, e.pool, audit.Entry{ActorID: &e.adminID, Action: action, TargetType: target, TargetID: id}); err != nil {
			t.Fatal(err)
		}
	}
	rec(audit.VillageVerify, audit.TargetVillage, e.village)
	rec(audit.VillageSuspend, audit.TargetVillage, e.village)
	rec(audit.ReportResolve, audit.TargetReport, 7)
	rec(audit.UserPromoteAdmin, audit.TargetUser, e.user)

	count := func(f audit.Filter) int {
		items, err := audit.List(ctx, e.pool, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}
	if n := count(audit.Filter{}); n != 4 {
		t.Errorf("all = %d", n)
	}
	if n := count(audit.Filter{Action: audit.VillageSuspend}); n != 1 {
		t.Errorf("by action = %d", n)
	}
	if n := count(audit.Filter{TargetType: audit.TargetVillage, TargetID: e.village}); n != 2 {
		t.Errorf("by target = %d", n)
	}
	if n := count(audit.Filter{Limit: 2, Offset: 2}); n != 2 {
		t.Errorf("second page = %d", n)
	}
	first, _ := audit.List(ctx, e.pool, audit.Filter{Limit: 1})
	if first[0].Action != audit.UserPromoteAdmin {
		t.Errorf("newest first: got %s", first[0].Action)
	}
	if _, err := e.pool.Exec(ctx, "INSERT INTO audit_log (action, target_type, target_id) VALUES ('village.delete', 'village', 1)"); err == nil {
		t.Error("unknown action accepted by the CHECK constraint")
	}
}

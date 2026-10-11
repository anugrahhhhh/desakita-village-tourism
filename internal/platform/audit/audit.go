// Package audit records administrative actions in the append-only audit_log table.
//
// Record must be called with the transaction that performs the change, so an entry exists
// if and only if the change was committed. The table rejects UPDATE, DELETE and TRUNCATE.
package audit

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"desakita/internal/db"
)

// Actions (kept in sync with the CHECK constraint in migrations/000003).
const (
	VillageVerify    = "village.verify"
	VillageReject    = "village.reject"
	VillageSuspend   = "village.suspend"
	VillageReinstate = "village.reinstate"
	ReportResolve    = "report.resolve"
	ReportDismiss    = "report.dismiss"
	UserPromoteAdmin = "user.promote_admin"
)

// Target types.
const (
	TargetVillage = "village"
	TargetReport  = "report"
	TargetUser    = "user"
)

// Entry describes one action. A nil ActorID means the system (e.g. the CLI).
type Entry struct {
	ActorID    *int64
	Action     string
	TargetType string
	TargetID   int64
	From, To   string
	Note       string
}

const maxNote = 1000

// Record writes e using q, which should be the transaction making the change.
func Record(ctx context.Context, q db.DBTX, e Entry) error {
	note := e.Note
	if utf8.RuneCountInString(note) > maxNote {
		note = string([]rune(note)[:maxNote])
	}
	err := db.New(q).InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		ActorID: e.ActorID, Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID,
		FromStatus: e.From, ToStatus: e.To, Note: note,
	})
	if err != nil {
		return fmt.Errorf("audit %s: %w", e.Action, err)
	}
	return nil
}

// Item is a stored entry.
type Item struct {
	ID         int64
	ActorID    *int64
	ActorName  string
	Action     string
	TargetType string
	TargetID   int64
	From, To   string
	Note       string
	CreatedAt  time.Time
}

// actionKeys maps actions to i18n label keys (literal so the i18n test can check them).
var actionKeys = map[string]string{
	VillageVerify:    "audit.action.village_verify",
	VillageReject:    "audit.action.village_reject",
	VillageSuspend:   "audit.action.village_suspend",
	VillageReinstate: "audit.action.village_reinstate",
	ReportResolve:    "audit.action.report_resolve",
	ReportDismiss:    "audit.action.report_dismiss",
	UserPromoteAdmin: "audit.action.user_promote_admin",
}

// ActionKey is the i18n key for the action's label.
func (i Item) ActionKey() string { return actionKeys[i.Action] }

// Actions lists every action, for filter menus.
func Actions() []string {
	return []string{VillageVerify, VillageReject, VillageSuspend, VillageReinstate, ReportResolve, ReportDismiss, UserPromoteAdmin}
}

// ActionKeyOf is the i18n key for an action name.
func ActionKeyOf(action string) string { return actionKeys[action] }

// Filter narrows List; zero values mean "any".
type Filter struct {
	Action     string
	TargetType string
	TargetID   int64
	Limit      int
	Offset     int
}

// List returns entries newest first.
func List(ctx context.Context, q db.DBTX, f Filter) ([]Item, error) {
	p := db.ListAuditEntriesParams{PageSize: int32(f.Limit), PageOffset: int32(f.Offset)}
	if p.PageSize <= 0 {
		p.PageSize = 50
	}
	if f.Action != "" {
		p.Action = &f.Action
	}
	if f.TargetType != "" {
		p.TargetType = &f.TargetType
	}
	if f.TargetID != 0 {
		p.TargetID = &f.TargetID
	}
	rows, err := db.New(q).ListAuditEntries(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	out := make([]Item, len(rows))
	for i, r := range rows {
		out[i] = Item{
			ID: r.ID, ActorID: r.ActorID, ActorName: r.ActorName, Action: r.Action, TargetType: r.TargetType,
			TargetID: r.TargetID, From: r.FromStatus, To: r.ToStatus, Note: r.Note, CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

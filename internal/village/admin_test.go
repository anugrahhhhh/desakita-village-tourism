package village_test

import (
	"context"
	"errors"
	"testing"

	"desakita/internal/platform/form"
	"desakita/internal/village"
)

func TestAdminStatusChanges(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, admin := e.user(t, "own@example.com"), e.user(t, "admin@example.com")
	status := func(id int64) string {
		t.Helper()
		v, err := e.svc.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v.Status
	}

	a, err := e.svc.Create(ctx, owner, e.input("Desa Satu"))
	if err != nil {
		t.Fatal(err)
	}
	var verr *form.ValidationError
	if err := e.svc.Reject(ctx, admin, a, "  "); !errors.As(err, &verr) {
		t.Errorf("reject without reason: %v", err)
	}
	if err := e.svc.Reject(ctx, admin, a, "Data kontak tidak bisa dihubungi"); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.Get(ctx, a); v.Status != village.StatusRejected || v.ReviewNote == "" {
		t.Errorf("after reject: %+v", v)
	}
	if err := e.svc.Suspend(ctx, admin, a, ""); !errors.Is(err, village.ErrInvalidStatusChange) {
		t.Errorf("suspend a rejected village: %v", err)
	}
	if err := e.svc.Verify(ctx, admin, a); err != nil || status(a) != village.StatusVerified {
		t.Errorf("verify after rejection: %v %s", err, status(a))
	}
	var verifiedBy *int64
	if err := e.pool.QueryRow(ctx, "SELECT verified_by FROM villages WHERE id = $1", a).Scan(&verifiedBy); err != nil {
		t.Fatal(err)
	}
	if verifiedBy == nil || *verifiedBy != admin {
		t.Errorf("verified_by = %v, want %d", verifiedBy, admin)
	}
	if err := e.svc.Verify(ctx, admin, a); !errors.Is(err, village.ErrInvalidStatusChange) {
		t.Errorf("verify twice: %v", err)
	}
	if err := e.svc.Suspend(ctx, admin, a, "laporan penipuan"); err != nil || status(a) != village.StatusSuspended {
		t.Errorf("suspend: %v %s", err, status(a))
	}
	if _, _, err := e.svc.Public(ctx, "desa-satu", 0); !errors.Is(err, village.ErrNotFound) {
		t.Errorf("suspended village still public: %v", err)
	}
	if err := e.svc.Reinstate(ctx, admin, a); err != nil || status(a) != village.StatusVerified {
		t.Errorf("reinstate: %v %s", err, status(a))
	}

	if pending, err := e.svc.ListByStatus(ctx, village.StatusPending); err != nil || len(pending) != 0 {
		t.Errorf("pending list: %d %v", len(pending), err)
	}
	managers, err := e.svc.Managers(ctx, a)
	if err != nil || len(managers) != 1 || managers[0].Email != "own@example.com" {
		t.Errorf("managers: %+v %v", managers, err)
	}
}

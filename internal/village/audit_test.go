package village_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"desakita/internal/platform/audit"
	"desakita/internal/village"
)

func TestStatusChangesAreAudited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, admin := e.user(t, "o@example.com"), e.user(t, "admin@example.com")
	id, err := e.svc.Create(ctx, owner, e.input("Desa Audit"))
	if err != nil {
		t.Fatal(err)
	}

	if err := e.svc.Reject(ctx, admin, id, "Kontak tidak aktif"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Suspend(ctx, admin, id, ""); !errors.Is(err, village.ErrInvalidStatusChange) {
		t.Fatalf("suspend rejected village: %v", err)
	}
	if err := e.svc.Verify(ctx, admin, id); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Suspend(ctx, admin, id, "Laporan penipuan"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Reinstate(ctx, admin, id); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Verify(ctx, admin, 999999); !errors.Is(err, village.ErrNotFound) {
		t.Errorf("unknown village: %v", err)
	}

	items, err := audit.List(ctx, e.pool, audit.Filter{TargetType: audit.TargetVillage, TargetID: id})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ action, from, to, note string }{ // newest first
		{audit.VillageReinstate, "suspended", "verified", ""},
		{audit.VillageSuspend, "verified", "suspended", "Laporan penipuan"},
		{audit.VillageVerify, "rejected", "verified", ""},
		{audit.VillageReject, "pending", "rejected", "Kontak tidak aktif"},
	}
	if len(items) != len(want) {
		t.Fatalf("audit entries = %d, want %d (the failed suspend must not be audited): %+v", len(items), len(want), items)
	}
	for i, w := range want {
		got := items[i]
		if got.Action != w.action || got.From != w.from || got.To != w.to || got.Note != w.note || got.ActorID == nil || *got.ActorID != admin || got.ActorName != "U" {
			t.Errorf("entry %d = %+v, want %+v", i, got, w)
		}
	}
}

func TestConcurrentAdminsAuditOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.user(t, "o2@example.com")
	a1, a2 := e.user(t, "a1@example.com"), e.user(t, "a2@example.com")
	id, err := e.svc.Create(ctx, owner, e.input("Desa Rebutan"))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, admin := range []int64{a1, a2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.svc.Verify(ctx, admin, id)
		}()
	}
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, village.ErrInvalidStatusChange):
			t.Errorf("unexpected error: %v", err)
		}
	}
	items, _ := audit.List(ctx, e.pool, audit.Filter{TargetType: audit.TargetVillage, TargetID: id})
	if ok != 1 || len(items) != 1 {
		t.Errorf("successes = %d, audit entries = %d; want exactly one of each", ok, len(items))
	}
}

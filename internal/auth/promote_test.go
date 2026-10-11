package auth_test

import (
	"context"
	"errors"
	"testing"

	"desakita/internal/auth"
	"desakita/internal/platform/audit"
)

func TestPromoteAdmin(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	u := register(t, svc, "calon-admin@example.com")
	if err := svc.PromoteAdmin(ctx, "CALON-ADMIN@example.com"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.UserByID(ctx, u.ID); !got.IsAdmin {
		t.Error("user not promoted")
	}
	if err := svc.PromoteAdmin(ctx, "nobody@example.com"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("unknown email: %v", err)
	}

	items, err := audit.List(ctx, pool, audit.Filter{Action: audit.UserPromoteAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TargetID != u.ID || items[0].ActorID != nil || items[0].To != "admin" {
		t.Errorf("audit entries = %+v, want one system entry for the promoted user", items)
	}
}

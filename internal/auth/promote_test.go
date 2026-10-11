package auth_test

import (
	"context"
	"errors"
	"testing"

	"desakita/internal/auth"
)

func TestPromoteAdmin(t *testing.T) {
	svc, _ := newService(t)
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
}

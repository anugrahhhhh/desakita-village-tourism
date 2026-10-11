package village_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"desakita/internal/village"
)

func TestPhotoCaptionAndDimensions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.user(t, "cap@example.com")
	id, err := e.svc.Create(ctx, owner, e.input("Desa Caption"))
	if err != nil {
		t.Fatal(err)
	}

	if err := e.svc.AddPhoto(ctx, owner, id, pngFile(t), true, strings.Repeat("a", 201)); !errors.Is(err, village.ErrCaptionTooLong) {
		t.Errorf("201-char caption: %v", err)
	}
	if err := e.svc.AddPhoto(ctx, owner, id, pngFile(t), true, "  Warga menanam padi  "); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AddPhoto(ctx, owner, id, pngFile(t), true, ""); err != nil {
		t.Fatal(err)
	}
	photos, err := e.svc.Photos(ctx, id)
	if err != nil || len(photos) != 2 {
		t.Fatalf("photos: %d %v", len(photos), err)
	}
	if photos[0].Caption != "Warga menanam padi" || photos[0].Alt("Desa Caption") != "Warga menanam padi" {
		t.Errorf("caption not stored/trimmed: %+v", photos[0])
	}
	if photos[1].Alt("Desa Caption") != "Desa Caption" {
		t.Errorf("empty caption should fall back to the village name, got %q", photos[1].Alt("Desa Caption"))
	}
	// pngFile is 8x8; stored dimensions come from the processed image.
	if photos[0].Width != 8 || photos[0].Height != 8 {
		t.Errorf("dimensions = %dx%d, want 8x8", photos[0].Width, photos[0].Height)
	}

	// Rows from before dimensions were recorded fall back to 4:3.
	if _, err := e.pool.Exec(ctx, "UPDATE media SET width = NULL, height = NULL WHERE village_id = $1", id); err != nil {
		t.Fatal(err)
	}
	photos, _ = e.svc.Photos(ctx, id)
	if photos[0].Width != 1200 || photos[0].Height != 900 {
		t.Errorf("fallback dimensions = %dx%d, want 1200x900", photos[0].Width, photos[0].Height)
	}
}

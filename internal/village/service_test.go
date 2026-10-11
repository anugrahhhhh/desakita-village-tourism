package village_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"log/slog"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/form"
	"desakita/internal/platform/upload"
	"desakita/internal/village"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Desa Wisata Pentingsari":   "desa-wisata-pentingsari",
		"  Kampung   Édelweiss! ":   "kampung-edelweiss",
		"Nglanggeran (Gunungkidul)": "nglanggeran-gunungkidul",
		"!!!":                       "desa",
	}
	for in, want := range cases {
		if got := village.Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

type env struct {
	pool     *pgxpool.Pool
	svc      *village.Service
	store    *upload.LocalStore
	district int64
}

func newEnv(t *testing.T) env {
	t.Helper()
	pool := dbtest.New(t)
	store, err := upload.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var district int64
	err = pool.QueryRow(context.Background(), `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', 'P') RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'Kab' FROM p RETURNING id)
		INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'Kec' FROM r RETURNING id`).Scan(&district)
	if err != nil {
		t.Fatal(err)
	}
	return env{pool: pool, svc: village.NewService(pool, store, slog.New(slog.DiscardHandler)), store: store, district: district}
}

func (e env) user(t *testing.T, email string) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(), `INSERT INTO users (email, password_hash, name) VALUES ($1, 'x', 'U') RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e env) input(name string) village.ProfileInput {
	return village.ProfileInput{Name: name, DistrictID: strconv.FormatInt(e.district, 10), ContactPhone: "0812-3456-7890"}
}

func TestCreateAndManage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, other := e.user(t, "owner@example.com"), e.user(t, "other@example.com")

	id, err := e.svc.Create(ctx, owner, e.input("Desa Wisata Contoh"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.Managed(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != village.StatusPending || v.Slug != "desa-wisata-contoh" || v.DistrictName != "Kec" || v.RegencyName != "Kab" {
		t.Errorf("unexpected village %+v", v)
	}
	if _, err := e.svc.Managed(ctx, other, id); !errors.Is(err, village.ErrNotFound) {
		t.Errorf("non-manager: err = %v, want ErrNotFound", err)
	}

	id2, err := e.svc.Create(ctx, other, e.input("Desa Wisata Contoh"))
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := e.svc.Get(ctx, id2)
	if v2.Slug != "desa-wisata-contoh-2" {
		t.Errorf("second slug = %q, want desa-wisata-contoh-2", v2.Slug)
	}

	in := e.input("Desa Baru")
	in.ContactWhatsApp = "0812 1111 2222"
	if err := e.svc.UpdateProfile(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	v, _ = e.svc.Get(ctx, id)
	if v.Name != "Desa Baru" || v.Slug != "desa-wisata-contoh" || v.ContactWhatsApp != "0812 1111 2222" {
		t.Errorf("after update: %+v (slug must not change)", v)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	owner := e.user(t, "v@example.com")
	_, err := e.svc.Create(context.Background(), owner, village.ProfileInput{Name: "", DistrictID: "999999", ContactPhone: "abc", ContactWhatsApp: "x"})
	var verr *form.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v", err)
	}
	for _, f := range []string{"name", "district_id", "contact_phone", "contact_whatsapp"} {
		if verr.Fields[f] == "" {
			t.Errorf("missing error for %s", f)
		}
	}
	// A regency id is not a district.
	var regency int64
	if err := e.pool.QueryRow(context.Background(), `SELECT parent_id FROM regions WHERE id = $1`, e.district).Scan(&regency); err != nil {
		t.Fatal(err)
	}
	in := e.input("X")
	in.DistrictID = strconv.FormatInt(regency, 10)
	if _, err := e.svc.Create(context.Background(), owner, in); !errors.As(err, &verr) || verr.Fields["district_id"] == "" {
		t.Errorf("regency accepted as district: %v", err)
	}
}

func TestPublicVisibility(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, other := e.user(t, "o@example.com"), e.user(t, "x@example.com")
	id, err := e.svc.Create(ctx, owner, e.input("Desa Tersembunyi"))
	if err != nil {
		t.Fatal(err)
	}

	for name, viewer := range map[string]int64{"anonymous": 0, "other user": other} {
		if _, _, err := e.svc.Public(ctx, "desa-tersembunyi", viewer); !errors.Is(err, village.ErrNotFound) {
			t.Errorf("%s sees pending village: %v", name, err)
		}
	}
	if _, preview, err := e.svc.Public(ctx, "desa-tersembunyi", owner); err != nil || !preview {
		t.Errorf("owner preview: preview=%v err=%v", preview, err)
	}

	if _, err := e.pool.Exec(ctx, `UPDATE villages SET status = 'verified' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, preview, err := e.svc.Public(ctx, "desa-tersembunyi", 0); err != nil || preview {
		t.Errorf("verified village: preview=%v err=%v", preview, err)
	}
}

func pngFile(t *testing.T) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestPhotos(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.user(t, "p@example.com")
	a, _ := e.svc.Create(ctx, owner, e.input("Desa A"))
	b, _ := e.svc.Create(ctx, owner, e.input("Desa B"))

	if err := e.svc.AddPhoto(ctx, owner, a, pngFile(t), false, ""); !errors.Is(err, village.ErrConsentRequired) {
		t.Errorf("no consent: %v", err)
	}
	if err := e.svc.AddPhoto(ctx, owner, a, bytes.NewReader([]byte("not an image")), true, ""); !errors.Is(err, upload.ErrUnsupported) {
		t.Errorf("text file: %v", err)
	}
	if err := e.svc.AddPhoto(ctx, owner, a, pngFile(t), true, ""); err != nil {
		t.Fatal(err)
	}
	photos, _ := e.svc.Photos(ctx, a)
	if len(photos) != 1 {
		t.Fatalf("photos = %d", len(photos))
	}
	if _, err := os.Stat(e.store.Path(photos[0].Path)); err != nil {
		t.Errorf("file not stored: %v", err)
	}

	if err := e.svc.DeletePhoto(ctx, b, photos[0].ID); !errors.Is(err, village.ErrNotFound) {
		t.Errorf("deleting via another village: %v", err)
	}
	if err := e.svc.DeletePhoto(ctx, a, photos[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.store.Path(photos[0].Path)); !os.IsNotExist(err) {
		t.Error("file not removed")
	}

	for i := range village.MaxPhotos {
		if _, err := e.pool.Exec(ctx, `INSERT INTO media (village_id, path, mime, bytes, consent_confirmed) VALUES ($1, $2, 'image/jpeg', 1, true)`, a, upload.NewName()); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := e.svc.AddPhoto(ctx, owner, a, pngFile(t), true, ""); !errors.Is(err, village.ErrPhotoLimit) {
		t.Errorf("over limit: %v", err)
	}
}

func TestLocation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.user(t, "lokasi@example.com")

	in := e.input("Desa Berlokasi")
	in.Lat, in.Lng = " -7,7970684 ", "110.370529"
	id, err := e.svc.Create(ctx, owner, in)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := e.svc.Get(ctx, id)
	if !v.HasLocation() || *v.Lat != -7.797068 || *v.Lng != 110.370529 {
		t.Errorf("location = %v, %v (comma decimal accepted, rounded to 6 places)", v.Lat, v.Lng)
	}
	if pts, _ := e.svc.Mapped(ctx); len(pts) != 0 {
		t.Errorf("pending village on the public map: %v", pts)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE villages SET status = 'verified' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	pts, err := e.svc.Mapped(ctx)
	if err != nil || len(pts) != 1 || pts[0].Slug != "desa-berlokasi" || pts[0].RegencyName != "Kab" || pts[0].Lat != -7.797068 {
		t.Errorf("mapped = %+v, %v", pts, err)
	}

	// Clearing both fields removes the location (and the village from the map).
	in.Lat, in.Lng = "", ""
	if err := e.svc.UpdateProfile(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.Get(ctx, id); v.HasLocation() {
		t.Error("location not cleared")
	}
	if pts, _ := e.svc.Mapped(ctx); len(pts) != 0 {
		t.Errorf("village without location on the map: %v", pts)
	}

	cases := map[string][2]string{
		"validation.location_pair":    {"-7.8", ""},
		"validation.location_number":  {"-7.8", "abc"},
		"validation.location_outside": {"48.85", "2.35"}, // Paris
	}
	for key, c := range cases {
		in.Lat, in.Lng = c[0], c[1]
		var verr *form.ValidationError
		if err := e.svc.UpdateProfile(ctx, id, in); !errors.As(err, &verr) || verr.Fields["location"] != key {
			t.Errorf("%v: err = %v, want %s", c, err, key)
		}
	}
	for _, bad := range []string{"NaN", "Inf", "1e400"} {
		in.Lat, in.Lng = bad, "110"
		if err := e.svc.UpdateProfile(ctx, id, in); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

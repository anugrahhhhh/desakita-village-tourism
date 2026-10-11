// Package village manages village profiles, their managers and village photos.
package village

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"desakita/internal/db"
	"desakita/internal/platform/form"
	"desakita/internal/platform/phone"
	"desakita/internal/platform/upload"
)

const (
	StatusPending   = "pending"
	StatusVerified  = "verified"
	StatusRejected  = "rejected"
	StatusSuspended = "suspended"

	// MaxPhotos bounds storage per village.
	MaxPhotos = 12
)

var (
	ErrNotFound        = errors.New("village: not found")
	ErrConsentRequired = errors.New("village: photo consent required")
	ErrPhotoLimit      = errors.New("village: photo limit reached")
)

type Village struct {
	ID              int64
	Slug            string
	Name            string
	Description     string
	DistrictID      int64
	DistrictName    string
	RegencyName     string
	Address         string
	ContactPhone    string
	ContactWhatsApp string
	Status          string
	ReviewNote      string
}

func (v Village) IsVerified() bool { return v.Status == StatusVerified }

type Summary struct {
	ID         int64
	Slug       string
	Name       string
	Status     string
	ReviewNote string
}

type District struct {
	ID                                  int64
	DistrictName, RegencyName, Province string
}

type Photo struct {
	ID   int64
	Path string
}

type Service struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	store  upload.Store
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, store upload.Store, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: db.New(pool), store: store, logger: logger}
}

// ProfileInput is the raw form; DistrictID is parsed during validation.
type ProfileInput struct {
	Name, DistrictID, Address, Description, ContactPhone, ContactWhatsApp string
}

type profile struct {
	name, address, description, phone string
	whatsapp                          *string
	districtID                        int64
}

func (s *Service) validate(ctx context.Context, in ProfileInput) (profile, error) {
	p := profile{
		name:        strings.TrimSpace(in.Name),
		address:     strings.TrimSpace(in.Address),
		description: strings.TrimSpace(in.Description),
		phone:       strings.TrimSpace(in.ContactPhone),
	}
	errs := form.Errors{}
	switch n := utf8.RuneCountInString(p.name); {
	case n == 0:
		errs.Add("name", "validation.required")
	case n > 120:
		errs.Add("name", "validation.too_long")
	}
	if utf8.RuneCountInString(p.address) > 300 {
		errs.Add("address", "validation.too_long")
	}
	if utf8.RuneCountInString(p.description) > 5000 {
		errs.Add("description", "validation.too_long")
	}
	switch {
	case p.phone == "":
		errs.Add("contact_phone", "validation.required")
	case !phone.Valid(p.phone):
		errs.Add("contact_phone", "validation.phone")
	}
	if wa := strings.TrimSpace(in.ContactWhatsApp); wa != "" {
		if !phone.Valid(wa) {
			errs.Add("contact_whatsapp", "validation.phone")
		}
		p.whatsapp = &wa
	}
	id, err := strconv.ParseInt(in.DistrictID, 10, 64)
	if err != nil {
		errs.Add("district_id", "validation.required")
	} else {
		ok, err := s.q.DistrictExists(ctx, id)
		if err != nil {
			return p, fmt.Errorf("check district: %w", err)
		}
		if !ok {
			errs.Add("district_id", "validation.required")
		}
		p.districtID = id
	}
	return p, errs.Err()
}

// Create registers a new village (status pending) with userID as its owner.
func (s *Service) Create(ctx context.Context, userID int64, in ProfileInput) (int64, error) {
	p, err := s.validate(ctx, in)
	if err != nil {
		return 0, err
	}
	base := Slugify(p.name)
	var id int64
	// Retry with a numeric suffix if a concurrent registration takes the same slug.
	for attempt := 0; attempt < 5; attempt++ {
		slug, err := s.freeSlug(ctx, base)
		if err != nil {
			return 0, err
		}
		err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			var err error
			id, err = q.CreateVillage(ctx, db.CreateVillageParams{
				Slug: slug, Name: p.name, Description: p.description, DistrictID: p.districtID,
				Address: p.address, ContactPhone: p.phone, ContactWhatsapp: p.whatsapp,
			})
			if err != nil {
				return err
			}
			return q.AddVillageMember(ctx, db.AddVillageMemberParams{VillageID: id, UserID: userID})
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "villages_slug_key" {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("create village: %w", err)
		}
		return id, nil
	}
	return 0, errors.New("create village: could not allocate slug")
}

func (s *Service) freeSlug(ctx context.Context, base string) (string, error) {
	for i := 1; i < 100; i++ {
		slug := base
		if i > 1 {
			slug = fmt.Sprintf("%s-%d", base, i)
		}
		taken, err := s.q.VillageSlugExists(ctx, slug)
		if err != nil {
			return "", fmt.Errorf("check slug: %w", err)
		}
		if !taken {
			return slug, nil
		}
	}
	return "", errors.New("no free slug")
}

// UpdateProfile changes the editable profile fields. The slug never changes, so shared
// links keep working.
func (s *Service) UpdateProfile(ctx context.Context, villageID int64, in ProfileInput) error {
	p, err := s.validate(ctx, in)
	if err != nil {
		return err
	}
	n, err := s.q.UpdateVillageProfile(ctx, db.UpdateVillageProfileParams{
		ID: villageID, Name: p.name, Description: p.description, DistrictID: p.districtID,
		Address: p.address, ContactPhone: p.phone, ContactWhatsapp: p.whatsapp,
	})
	if err != nil {
		return fmt.Errorf("update village: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func fromRow(r db.GetVillageRow) Village {
	v := Village{
		ID: r.ID, Slug: r.Slug, Name: r.Name, Description: r.Description, DistrictID: r.DistrictID,
		DistrictName: r.DistrictName, RegencyName: r.RegencyName, Address: r.Address,
		ContactPhone: r.ContactPhone, Status: r.Status, ReviewNote: r.ReviewNote,
	}
	if r.ContactWhatsapp != nil {
		v.ContactWhatsApp = *r.ContactWhatsapp
	}
	return v
}

func (s *Service) Get(ctx context.Context, id int64) (Village, error) {
	r, err := s.q.GetVillage(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Village{}, ErrNotFound
	}
	if err != nil {
		return Village{}, fmt.Errorf("get village: %w", err)
	}
	return fromRow(r), nil
}

// IsManager reports whether userID manages villageID.
func (s *Service) IsManager(ctx context.Context, userID, villageID int64) (bool, error) {
	return s.q.IsVillageManager(ctx, db.IsVillageManagerParams{VillageID: villageID, UserID: userID})
}

// Managed returns the village if userID manages it, otherwise ErrNotFound.
func (s *Service) Managed(ctx context.Context, userID, villageID int64) (Village, error) {
	ok, err := s.IsManager(ctx, userID, villageID)
	if err != nil {
		return Village{}, fmt.Errorf("check manager: %w", err)
	}
	if !ok {
		return Village{}, ErrNotFound
	}
	return s.Get(ctx, villageID)
}

// Public returns a village for its public page. Unverified villages are visible only to
// their managers (preview == true); everyone else gets ErrNotFound.
func (s *Service) Public(ctx context.Context, slug string, viewerID int64) (v Village, preview bool, err error) {
	r, err := s.q.GetVillageBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Village{}, false, ErrNotFound
	}
	if err != nil {
		return Village{}, false, fmt.Errorf("get village: %w", err)
	}
	v = fromRow(db.GetVillageRow(r))
	if v.IsVerified() {
		return v, false, nil
	}
	if viewerID != 0 {
		ok, err := s.IsManager(ctx, viewerID, v.ID)
		if err != nil {
			return Village{}, false, fmt.Errorf("check manager: %w", err)
		}
		if ok {
			return v, true, nil
		}
	}
	return Village{}, false, ErrNotFound
}

func (s *Service) ListForUser(ctx context.Context, userID int64) ([]Summary, error) {
	rows, err := s.q.ListVillagesForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list villages: %w", err)
	}
	out := make([]Summary, len(rows))
	for i, r := range rows {
		out[i] = Summary{ID: r.ID, Slug: r.Slug, Name: r.Name, Status: r.Status, ReviewNote: r.ReviewNote}
	}
	return out, nil
}

func (s *Service) Districts(ctx context.Context) ([]District, error) {
	rows, err := s.q.ListDistricts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list districts: %w", err)
	}
	out := make([]District, len(rows))
	for i, r := range rows {
		out[i] = District{ID: r.ID, DistrictName: r.DistrictName, RegencyName: r.RegencyName, Province: r.ProvinceName}
	}
	return out, nil
}

func (s *Service) Photos(ctx context.Context, villageID int64) ([]Photo, error) {
	rows, err := s.q.ListVillageMedia(ctx, &villageID)
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	out := make([]Photo, len(rows))
	for i, r := range rows {
		out[i] = Photo{ID: r.ID, Path: r.Path}
	}
	return out, nil
}

// AddPhoto stores a photo for the village. consent must be true: the manager confirms the
// people and cultural content shown agreed to publication. Upload errors are returned as is
// (see upload.MessageKey).
func (s *Service) AddPhoto(ctx context.Context, userID, villageID int64, r io.Reader, consent bool) error {
	if !consent {
		return ErrConsentRequired
	}
	// Cheap pre-check before the expensive decode; re-checked under lock below.
	if n, err := s.q.CountVillageMedia(ctx, &villageID); err != nil {
		return fmt.Errorf("count photos: %w", err)
	} else if n >= MaxPhotos {
		return ErrPhotoLimit
	}
	name, size, err := upload.SaveImage(s.store, r)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockVillage(ctx, villageID); err != nil {
			return err
		}
		n, err := q.CountVillageMedia(ctx, &villageID)
		if err != nil {
			return err
		}
		if n >= MaxPhotos {
			return ErrPhotoLimit
		}
		_, err = q.InsertVillageMedia(ctx, db.InsertVillageMediaParams{
			VillageID: &villageID, Path: name, Mime: upload.OutputMIME, Bytes: int32(size), UploadedBy: &userID,
		})
		return err
	})
	if err != nil {
		if derr := s.store.Delete(name); derr != nil {
			s.logger.Error("delete orphaned upload", "name", name, "err", derr)
		}
		if errors.Is(err, ErrPhotoLimit) {
			return err
		}
		return fmt.Errorf("insert photo: %w", err)
	}
	return nil
}

func (s *Service) DeletePhoto(ctx context.Context, villageID, mediaID int64) error {
	path, err := s.q.DeleteVillageMedia(ctx, db.DeleteVillageMediaParams{ID: mediaID, VillageID: &villageID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete photo: %w", err)
	}
	if err := s.store.Delete(path); err != nil {
		// The row is gone; a leftover file is harmless and only logged.
		s.logger.Error("delete photo file", "name", path, "err", err)
	}
	return nil
}

// Slugify turns a village name into a URL slug: "Desa Wisata Pentingsari" → "desa-wisata-pentingsari".
func Slugify(name string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	ascii, _, _ := transform.String(t, strings.ToLower(name))
	var b strings.Builder
	dash := false
	for _, c := range ascii {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.TrimSuffix(b.String(), "-")
	if len(slug) > 60 {
		slug = strings.TrimSuffix(slug[:60], "-")
	}
	if slug == "" {
		slug = "desa"
	}
	return slug
}

// Package tourpackage manages a village's tour packages, their activity types and photos.
package tourpackage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/db"
	"desakita/internal/platform/form"
	"desakita/internal/platform/upload"
	"desakita/internal/village"
)

const MaxPhotos = 12

var (
	ErrNotFound        = errors.New("tourpackage: not found")
	ErrConsentRequired = errors.New("tourpackage: photo consent required")
	ErrPhotoLimit      = errors.New("tourpackage: photo limit reached")
	ErrCaptionTooLong  = errors.New("tourpackage: photo caption too long")
)

type Package struct {
	ID                int64
	VillageID         int64
	Name              string
	Description       string
	PricePerPersonIDR int64
	DurationDays      int16
	DurationNights    int16
	DefaultQuota      int32
	IsActive          bool
	ActivityTypeIDs   []int64
}

type ActivityType struct {
	ID   int64
	Slug string
	Name string
}

type Photo struct {
	ID            int64
	Path          string
	Caption       string
	Width, Height int
}

// Alt is the image's alternative text: its caption, or fallback (the package name).
func (p Photo) Alt(fallback string) string {
	if p.Caption != "" {
		return p.Caption
	}
	return fallback
}

type Service struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	villages *village.Service
	store    upload.Store
	logger   *slog.Logger
}

func NewService(pool *pgxpool.Pool, villages *village.Service, store upload.Store, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: db.New(pool), villages: villages, store: store, logger: logger}
}

// Input is the raw package form.
type Input struct {
	Name, Description, Price, DurationDays, DurationNights, DefaultQuota string
	ActivityTypeIDs                                                      []string
}

type validated struct {
	name, description   string
	price               int64
	days, nights, quota int64
	activityTypeIDs     []int64
}

// parseRupiah accepts "150000", "150.000" or "Rp 150.000".
func parseRupiah(s string) (int64, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "Rp"))
	s = strings.NewReplacer(".", "", " ", "", ",", "").Replace(s)
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0 && n <= 1_000_000_000
}

func parseRange(s string, min, max int64) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil && n >= min && n <= max
}

func (s *Service) validate(ctx context.Context, in Input) (validated, error) {
	v := validated{name: strings.TrimSpace(in.Name), description: strings.TrimSpace(in.Description)}
	errs := form.Errors{}
	switch n := utf8.RuneCountInString(v.name); {
	case n == 0:
		errs.Add("name", "validation.required")
	case n > 120:
		errs.Add("name", "validation.too_long")
	}
	if utf8.RuneCountInString(v.description) > 5000 {
		errs.Add("description", "validation.too_long")
	}
	var ok bool
	if v.price, ok = parseRupiah(in.Price); !ok {
		errs.Add("price", "validation.price")
	}
	if v.days, ok = parseRange(in.DurationDays, 1, 30); !ok {
		errs.Add("duration_days", "validation.duration_days")
	}
	if v.nights, ok = parseRange(in.DurationNights, 0, 30); !ok {
		errs.Add("duration_nights", "validation.duration_nights")
	} else if v.nights > v.days {
		errs.Add("duration_nights", "package.nights_exceed_days")
	}
	if v.quota, ok = parseRange(in.DefaultQuota, 1, 1000); !ok {
		errs.Add("default_quota", "validation.quota")
	}
	for _, raw := range in.ActivityTypeIDs {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			errs.Add("activity_types", "validation.invalid_choice")
			break
		}
		if !slices.Contains(v.activityTypeIDs, id) {
			v.activityTypeIDs = append(v.activityTypeIDs, id)
		}
	}
	if len(v.activityTypeIDs) > 0 {
		n, err := s.q.CountActivityTypes(ctx, v.activityTypeIDs)
		if err != nil {
			return v, fmt.Errorf("check activity types: %w", err)
		}
		if int(n) != len(v.activityTypeIDs) {
			errs.Add("activity_types", "validation.invalid_choice")
		}
	}
	return v, errs.Err()
}

func setActivityTypes(ctx context.Context, q *db.Queries, packageID int64, ids []int64) error {
	if err := q.ClearPackageActivityTypes(ctx, packageID); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return q.AddPackageActivityTypes(ctx, db.AddPackageActivityTypesParams{PackageID: packageID, ActivityTypeIds: ids})
}

func (s *Service) Create(ctx context.Context, villageID int64, in Input) (int64, error) {
	v, err := s.validate(ctx, in)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		id, err = q.CreatePackage(ctx, db.CreatePackageParams{
			VillageID: villageID, Name: v.name, Description: v.description, PricePerPersonIdr: v.price,
			DurationDays: int16(v.days), DurationNights: int16(v.nights), DefaultQuota: int32(v.quota),
		})
		if err != nil {
			return err
		}
		return setActivityTypes(ctx, q, id, v.activityTypeIDs)
	})
	if err != nil {
		return 0, fmt.Errorf("create package: %w", err)
	}
	return id, nil
}

// Update edits a package. Price changes don't affect existing bookings (they snapshot the
// total); default quota changes apply only to dates without an availability row yet.
func (s *Service) Update(ctx context.Context, villageID, id int64, in Input) error {
	v, err := s.validate(ctx, in)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.UpdatePackage(ctx, db.UpdatePackageParams{
			ID: id, VillageID: villageID, Name: v.name, Description: v.description, PricePerPersonIdr: v.price,
			DurationDays: int16(v.days), DurationNights: int16(v.nights), DefaultQuota: int32(v.quota),
		})
		if err != nil {
			return fmt.Errorf("update package: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return setActivityTypes(ctx, q, id, v.activityTypeIDs)
	})
}

func (s *Service) SetActive(ctx context.Context, villageID, id int64, active bool) error {
	n, err := s.q.SetPackageActive(ctx, db.SetPackageActiveParams{ID: id, VillageID: villageID, IsActive: active})
	if err != nil {
		return fmt.Errorf("set package active: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func fromRow(r db.GetPackageRow) Package {
	return Package{
		ID: r.ID, VillageID: r.VillageID, Name: r.Name, Description: r.Description,
		PricePerPersonIDR: r.PricePerPersonIdr, DurationDays: r.DurationDays, DurationNights: r.DurationNights,
		DefaultQuota: r.DefaultQuota, IsActive: r.IsActive,
	}
}

// Get returns a package only if it belongs to villageID.
func (s *Service) Get(ctx context.Context, villageID, id int64) (Package, error) {
	r, err := s.q.GetPackage(ctx, db.GetPackageParams{ID: id, VillageID: villageID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Package{}, ErrNotFound
	}
	if err != nil {
		return Package{}, fmt.Errorf("get package: %w", err)
	}
	p := fromRow(r)
	if p.ActivityTypeIDs, err = s.q.ListPackageActivityTypeIDs(ctx, id); err != nil {
		return Package{}, fmt.Errorf("list activity types: %w", err)
	}
	return p, nil
}

func (s *Service) List(ctx context.Context, villageID int64) ([]Package, error) {
	rows, err := s.q.ListPackagesByVillage(ctx, villageID)
	if err != nil {
		return nil, fmt.Errorf("list packages: %w", err)
	}
	out := make([]Package, len(rows))
	for i, r := range rows {
		out[i] = fromRow(db.GetPackageRow(r))
	}
	return out, nil
}

func (s *Service) ActivityTypes(ctx context.Context) ([]ActivityType, error) {
	rows, err := s.q.ListActivityTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list activity types: %w", err)
	}
	out := make([]ActivityType, len(rows))
	for i, r := range rows {
		out[i] = ActivityType{ID: r.ID, Slug: r.Slug, Name: r.Name}
	}
	return out, nil
}

// ActivePackageCards implements village.PackageLister.
func (s *Service) ActivePackageCards(ctx context.Context, villageID int64) ([]village.PackageCard, error) {
	rows, err := s.q.ListActivePackageCards(ctx, villageID)
	if err != nil {
		return nil, fmt.Errorf("list package cards: %w", err)
	}
	out := make([]village.PackageCard, len(rows))
	for i, r := range rows {
		out[i] = village.PackageCard{
			ID: r.ID, Name: r.Name, PricePerPersonIDR: r.PricePerPersonIdr,
			DurationDays: r.DurationDays, DurationNights: r.DurationNights, CoverPath: r.CoverPath,
			CoverCaption: r.CoverCaption, CoverWidth: coverDim(r.CoverWidth, 1200), CoverHeight: coverDim(r.CoverHeight, 900),
		}
	}
	return out, nil
}

// PublicView is everything the public package page shows.
type PublicView struct {
	Village       village.Village
	Package       Package
	ActivityTypes []ActivityType
	Photos        []Photo
	// Preview is true when a manager views an unverified village or inactive package.
	Preview bool
}

func (s *Service) Public(ctx context.Context, slug string, packageID, viewerID int64) (PublicView, error) {
	v, preview, err := s.villages.Public(ctx, slug, viewerID)
	if errors.Is(err, village.ErrNotFound) {
		return PublicView{}, ErrNotFound
	}
	if err != nil {
		return PublicView{}, err
	}
	p, err := s.Get(ctx, v.ID, packageID)
	if err != nil {
		return PublicView{}, err
	}
	if !p.IsActive && !preview {
		// Managers of a verified village can still preview their inactive package.
		ok := false
		if viewerID != 0 {
			if ok, err = s.villages.IsManager(ctx, viewerID, v.ID); err != nil {
				return PublicView{}, err
			}
		}
		if !ok {
			return PublicView{}, ErrNotFound
		}
		preview = true
	}
	types, err := s.q.ListPackageActivityTypes(ctx, p.ID)
	if err != nil {
		return PublicView{}, fmt.Errorf("list activity types: %w", err)
	}
	view := PublicView{Village: v, Package: p, Preview: preview}
	for _, t := range types {
		view.ActivityTypes = append(view.ActivityTypes, ActivityType{ID: t.ID, Slug: t.Slug, Name: t.Name})
	}
	if view.Photos, err = s.Photos(ctx, p.ID); err != nil {
		return PublicView{}, err
	}
	return view, nil
}

func (s *Service) Photos(ctx context.Context, packageID int64) ([]Photo, error) {
	rows, err := s.q.ListPackageMedia(ctx, &packageID)
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	out := make([]Photo, len(rows))
	for i, r := range rows {
		out[i] = Photo{ID: r.ID, Path: r.Path, Caption: r.Caption, Width: upload.Dim(r.Width, 1200), Height: upload.Dim(r.Height, 900)}
	}
	return out, nil
}

// AddPhoto stores a photo for a package the caller has already scoped to its village.
func (s *Service) AddPhoto(ctx context.Context, userID, packageID int64, r io.Reader, consent bool, caption string) error {
	if !consent {
		return ErrConsentRequired
	}
	caption, ok := upload.Caption(caption)
	if !ok {
		return ErrCaptionTooLong
	}
	if n, err := s.q.CountPackageMedia(ctx, &packageID); err != nil {
		return fmt.Errorf("count photos: %w", err)
	} else if n >= MaxPhotos {
		return ErrPhotoLimit
	}
	saved, err := upload.SaveImage(s.store, r)
	if err != nil {
		return err
	}
	name := saved.Name
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockPackage(ctx, packageID); err != nil {
			return err
		}
		n, err := q.CountPackageMedia(ctx, &packageID)
		if err != nil {
			return err
		}
		if n >= MaxPhotos {
			return ErrPhotoLimit
		}
		w, h := int32(saved.Width), int32(saved.Height)
		_, err = q.InsertPackageMedia(ctx, db.InsertPackageMediaParams{
			PackageID: &packageID, Path: name, Mime: upload.OutputMIME, Bytes: int32(saved.Bytes), UploadedBy: &userID,
			Caption: caption, Width: &w, Height: &h,
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

func (s *Service) DeletePhoto(ctx context.Context, packageID, mediaID int64) error {
	path, err := s.q.DeletePackageMedia(ctx, db.DeletePackageMediaParams{ID: mediaID, PackageID: &packageID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete photo: %w", err)
	}
	if err := s.store.Delete(path); err != nil {
		s.logger.Error("delete photo file", "name", path, "err", err)
	}
	return nil
}

// BookingInfo is the package data the booking module needs.
type BookingInfo struct {
	ID                int64
	VillageID         int64
	PricePerPersonIDR int64
	DefaultQuota      int32
	IsActive          bool
	VillageStatus     string
}

// BookingInfo reads a package inside the caller's transaction and share-locks the package
// and its village until that transaction ends.
func (s *Service) BookingInfo(ctx context.Context, tx pgx.Tx, id int64) (BookingInfo, error) {
	r, err := s.q.WithTx(tx).GetPackageForBooking(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return BookingInfo{}, ErrNotFound
	}
	if err != nil {
		return BookingInfo{}, fmt.Errorf("get package for booking: %w", err)
	}
	return BookingInfo{
		ID: r.ID, VillageID: r.VillageID, PricePerPersonIDR: r.PricePerPersonIdr,
		DefaultQuota: r.DefaultQuota, IsActive: r.IsActive, VillageStatus: r.VillageStatus,
	}, nil
}

// Summary is a package with its village's display and contact fields.
type Summary struct {
	ID, VillageID     int64
	Name              string
	DefaultQuota      int32
	PricePerPersonIDR int64
	IsActive          bool
	VillageName       string
	VillageSlug       string
	VillageStatus     string
	VillagePhone      string
	VillageWhatsApp   string
}

func summaryFromRow(r db.GetPackageSummaryRow) Summary {
	s := Summary{
		ID: r.ID, VillageID: r.VillageID, Name: r.Name, DefaultQuota: r.DefaultQuota,
		PricePerPersonIDR: r.PricePerPersonIdr, IsActive: r.IsActive, VillageName: r.VillageName,
		VillageSlug: r.VillageSlug, VillageStatus: r.VillageStatus, VillagePhone: r.VillagePhone,
	}
	if r.VillageWhatsapp != nil {
		s.VillageWhatsApp = *r.VillageWhatsapp
	}
	return s
}

func (s *Service) Summary(ctx context.Context, id int64) (Summary, error) {
	r, err := s.q.GetPackageSummary(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	if err != nil {
		return Summary{}, fmt.Errorf("get package summary: %w", err)
	}
	return summaryFromRow(r), nil
}

func (s *Service) VillageSummaries(ctx context.Context, villageID int64) ([]Summary, error) {
	rows, err := s.q.ListPackageSummariesByVillage(ctx, villageID)
	if err != nil {
		return nil, fmt.Errorf("list package summaries: %w", err)
	}
	out := make([]Summary, len(rows))
	for i, r := range rows {
		out[i] = summaryFromRow(db.GetPackageSummaryRow(r))
	}
	return out, nil
}

// SearchPageSize is how many results the home page shows per page.
const SearchPageSize = 24

// SearchFilter holds the raw query-string filters; invalid values are ignored.
type SearchFilter struct {
	Regency, ActivityType, MinPrice, MaxPrice, Query, Page string
	// Sort: SortRelevance, SortPriceAsc, SortPriceDesc or SortNewest. Empty means relevance
	// when there is a query, otherwise cheapest first.
	Sort string
}

// Sort orders (also the values of the "urut" query parameter).
const (
	SortRelevance = "relevan"
	SortPriceAsc  = "termurah"
	SortPriceDesc = "termahal"
	SortNewest    = "terbaru"
)

// SortOptions lists the orders shown in the sort menu.
var SortOptions = []string{SortRelevance, SortPriceAsc, SortPriceDesc, SortNewest}

// sqlSort maps a public sort name to the query's sort key.
var sqlSort = map[string]string{
	SortRelevance: "relevance", SortPriceAsc: "price_asc", SortPriceDesc: "price_desc", SortNewest: "newest",
}

// EffectiveSort resolves an empty or invalid sort to the default for the query.
func (f SearchFilter) EffectiveSort() string {
	if _, ok := sqlSort[f.Sort]; ok && (f.Sort != SortRelevance || strings.TrimSpace(f.Query) != "") {
		return f.Sort
	}
	if strings.TrimSpace(f.Query) != "" {
		return SortRelevance
	}
	return SortPriceAsc
}

type SearchResult struct {
	ID                           int64
	Name                         string
	PricePerPersonIDR            int64
	DurationDays, DurationNights int16
	VillageName, VillageSlug     string
	DistrictName, RegencyName    string
	CoverPath                    string
	CoverCaption                 string
	CoverWidth, CoverHeight      int
}

// Meta is the location line shown on search result cards.
func (r SearchResult) Meta() string {
	return r.VillageName + " · " + r.DistrictName + ", " + r.RegencyName
}

// CoverAlt is the cover photo's caption, or the package name.
func (r SearchResult) CoverAlt() string {
	if r.CoverCaption != "" {
		return r.CoverCaption
	}
	return r.Name
}

func coverDim(v int32, def int) int {
	if v <= 0 {
		return def
	}
	return int(v)
}

type SearchPage struct {
	Results []SearchResult
	Page    int
	HasNext bool
	// Total is the number of matches across all pages.
	Total int64
	// Sort is the order actually applied.
	Sort string
}

func optionalID(s string) *int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
		return &n
	}
	return nil
}

func optionalPrice(s string) *int64 {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if n, ok := parseRupiah(s); ok {
		return &n
	}
	return nil
}

// likePattern escapes LIKE wildcards so user input matches literally.
func likePattern(q string) *string {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	if utf8.RuneCountInString(q) > 100 {
		q = string([]rune(q)[:100])
	}
	q = strings.NewReplacer(`\`, `\`, `%`, `\%`, `_`, `\_`).Replace(q)
	p := "%" + q + "%"
	return &p
}

// searchQuery trims and bounds the free-text query; nil means no text filter.
func searchQuery(q string) *string {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	if utf8.RuneCountInString(q) > 100 {
		q = string([]rune(q)[:100])
	}
	return &q
}

// Search lists active packages of verified villages matching f. Text queries use the
// full-text and trigram indexes (migrations/000004); see queries/packages.sql.
func (s *Service) Search(ctx context.Context, f SearchFilter) (SearchPage, error) {
	page := 1
	if n, err := strconv.Atoi(f.Page); err == nil && n > 1 && n <= 1000 {
		page = n
	}
	rows, err := s.q.SearchPackages(ctx, db.SearchPackagesParams{
		RegencyID:      optionalID(f.Regency),
		ActivityTypeID: optionalID(f.ActivityType),
		MinPrice:       optionalPrice(f.MinPrice),
		MaxPrice:       optionalPrice(f.MaxPrice),
		Query:          searchQuery(f.Query),
		Pattern:        likePattern(f.Query),
		Sort:           sqlSort[f.EffectiveSort()],
		PageSize:       SearchPageSize + 1, // one extra row tells us whether a next page exists
		PageOffset:     int32((page - 1) * SearchPageSize),
	})
	if err != nil {
		return SearchPage{}, fmt.Errorf("search packages: %w", err)
	}
	out := SearchPage{Page: page, HasNext: len(rows) > SearchPageSize, Sort: f.EffectiveSort()}
	if len(rows) > 0 {
		out.Total = rows[0].Total
	}
	if out.HasNext {
		rows = rows[:SearchPageSize]
	}
	for _, r := range rows {
		out.Results = append(out.Results, SearchResult{
			ID: r.ID, Name: r.Name, PricePerPersonIDR: r.PricePerPersonIdr, DurationDays: r.DurationDays,
			DurationNights: r.DurationNights, VillageName: r.VillageName, VillageSlug: r.VillageSlug,
			DistrictName: r.DistrictName, RegencyName: r.RegencyName, CoverPath: r.CoverPath,
			CoverCaption: r.CoverCaption, CoverWidth: coverDim(r.CoverWidth, 1200), CoverHeight: coverDim(r.CoverHeight, 900),
		})
	}
	return out, nil
}

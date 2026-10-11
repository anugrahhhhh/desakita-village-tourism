package tourpackage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"desakita/internal/platform/form"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/render"
	"desakita/internal/platform/session"
	"desakita/internal/platform/upload"
	"desakita/internal/village"
)

type Handler struct {
	svc *Service
	sm  *scs.SessionManager
	rd  *render.Renderer
}

func NewHandler(svc *Service, sm *scs.SessionManager, rd *render.Renderer) *Handler {
	return &Handler{svc: svc, sm: sm, rd: rd}
}

// ManageRoutes mounts /kelola/desa/{villageID}/paket/... (caller applies village.RequireManager).
func (h *Handler) ManageRoutes(r chi.Router) {
	r.Get("/", h.list)
	r.Get("/baru", h.newForm)
	r.Post("/baru", h.create)
	r.Route("/{packageID}", func(r chi.Router) {
		r.Use(h.requirePackage)
		r.Get("/", h.editForm)
		r.Post("/", h.update)
		r.Post("/status", h.setStatus)
		r.Get("/foto", h.photos)
		r.Post("/foto", h.uploadPhoto)
		r.Post("/foto/{mediaID}/hapus", h.deletePhoto)
	})
}

func (h *Handler) PublicRoutes(r chi.Router) {
	r.Get("/", h.home)
	r.Get("/desa/{slug}/paket/{packageID}", h.publicPage)
}

type homeData struct {
	Filter        SearchFilter
	Results       SearchPage
	Regencies     []village.Regency
	ActivityTypes []ActivityType
	// Filtered is true when any filter is set (changes the heading and empty text).
	Filtered bool
	// PrevQuery / NextQuery are the query strings for pagination links.
	PrevQuery, NextQuery string
}

// home is the landing page and package search.
func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := SearchFilter{
		Regency: q.Get("wilayah"), ActivityType: q.Get("kegiatan"),
		MinPrice: q.Get("harga_min"), MaxPrice: q.Get("harga_maks"), Query: q.Get("q"), Page: q.Get("hal"),
	}
	results, err := h.svc.Search(r.Context(), f)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	regencies, err := h.svc.villages.Regencies(r.Context())
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	types, err := h.svc.ActivityTypes(r.Context())
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	d := homeData{
		Filter: f, Results: results, Regencies: regencies, ActivityTypes: types,
		Filtered: f.Regency != "" || f.ActivityType != "" || f.MinPrice != "" || f.MaxPrice != "" || f.Query != "",
	}
	pageQuery := func(page int) string {
		v := url.Values{}
		for k, val := range map[string]string{"wilayah": f.Regency, "kegiatan": f.ActivityType, "harga_min": f.MinPrice, "harga_maks": f.MaxPrice, "q": f.Query} {
			if val != "" {
				v.Set(k, val)
			}
		}
		if page > 1 {
			v.Set("hal", strconv.Itoa(page))
		}
		return v.Encode()
	}
	if results.Page > 1 {
		d.PrevQuery = pageQuery(results.Page - 1)
	}
	if results.HasNext {
		d.NextQuery = pageQuery(results.Page + 1)
	}
	h.rd.Page(w, r, http.StatusOK, "home", d)
}

type ctxKey struct{}

func packageFrom(ctx context.Context) Package {
	p, _ := ctx.Value(ctxKey{}).(Package)
	return p
}

// requirePackage loads {packageID} scoped to the village from RequireManager; a package of
// another village is a 404.
func (h *Handler) requirePackage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := village.FromContext(r.Context())
		id, err := strconv.ParseInt(chi.URLParam(r, "packageID"), 10, 64)
		if err != nil {
			h.rd.NotFound(w, r)
			return
		}
		p, err := h.svc.Get(r.Context(), v.ID, id)
		if errors.Is(err, ErrNotFound) {
			h.rd.NotFound(w, r)
			return
		}
		if err != nil {
			h.rd.ServerError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func managePath(villageID int64, rest string) string {
	return fmt.Sprintf("/kelola/desa/%d/paket%s", villageID, rest)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	v := village.FromContext(r.Context())
	pkgs, err := h.svc.List(r.Context(), v.ID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "manage_packages", map[string]any{"Village": v, "Packages": pkgs})
}

type formData struct {
	Village       village.Village
	Package       Package
	IsNew         bool
	Values        Input
	Selected      map[string]bool
	Errors        form.Errors
	ActivityTypes []ActivityType
}

func (h *Handler) renderForm(w http.ResponseWriter, r *http.Request, status int, d formData) {
	types, err := h.svc.ActivityTypes(r.Context())
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	d.Village = village.FromContext(r.Context())
	d.ActivityTypes = types
	d.Selected = map[string]bool{}
	for _, id := range d.Values.ActivityTypeIDs {
		d.Selected[id] = true
	}
	h.rd.Page(w, r, status, "manage_package_form", d)
}

func inputFromForm(r *http.Request) Input {
	return Input{
		Name:            r.PostFormValue("name"),
		Description:     r.PostFormValue("description"),
		Price:           r.PostFormValue("price"),
		DurationDays:    r.PostFormValue("duration_days"),
		DurationNights:  r.PostFormValue("duration_nights"),
		DefaultQuota:    r.PostFormValue("default_quota"),
		ActivityTypeIDs: r.PostForm["activity_types"],
	}
}

func (h *Handler) newForm(w http.ResponseWriter, r *http.Request) {
	h.renderForm(w, r, http.StatusOK, formData{IsNew: true, Values: Input{DurationDays: "1", DurationNights: "0"}})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	v := village.FromContext(r.Context())
	in := inputFromForm(r)
	id, err := h.svc.Create(r.Context(), v.ID, in)
	var verr *form.ValidationError
	if errors.As(err, &verr) {
		h.renderForm(w, r, http.StatusUnprocessableEntity, formData{IsNew: true, Values: in, Errors: verr.Fields})
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.package_created")
	httpx.Redirect(w, r, managePath(v.ID, fmt.Sprintf("/%d/foto", id)))
}

func (h *Handler) editForm(w http.ResponseWriter, r *http.Request) {
	p := packageFrom(r.Context())
	in := Input{
		Name: p.Name, Description: p.Description, Price: strconv.FormatInt(p.PricePerPersonIDR, 10),
		DurationDays: strconv.Itoa(int(p.DurationDays)), DurationNights: strconv.Itoa(int(p.DurationNights)),
		DefaultQuota: strconv.Itoa(int(p.DefaultQuota)),
	}
	for _, id := range p.ActivityTypeIDs {
		in.ActivityTypeIDs = append(in.ActivityTypeIDs, strconv.FormatInt(id, 10))
	}
	h.renderForm(w, r, http.StatusOK, formData{Package: p, Values: in})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	v := village.FromContext(r.Context())
	p := packageFrom(r.Context())
	in := inputFromForm(r)
	err := h.svc.Update(r.Context(), v.ID, p.ID, in)
	var verr *form.ValidationError
	if errors.As(err, &verr) {
		h.renderForm(w, r, http.StatusUnprocessableEntity, formData{Package: p, Values: in, Errors: verr.Fields})
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.saved")
	httpx.Redirect(w, r, managePath(v.ID, ""))
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request) {
	v := village.FromContext(r.Context())
	p := packageFrom(r.Context())
	active := r.PostFormValue("active") == "1"
	if err := h.svc.SetActive(r.Context(), v.ID, p.ID, active); err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	key := "flash.package_deactivated"
	if active {
		key = "flash.package_activated"
	}
	h.sm.Put(r.Context(), session.KeyFlash, key)
	httpx.Redirect(w, r, managePath(v.ID, ""))
}

type photosData struct {
	Village            village.Village
	Package            Package
	Photos             []Photo
	Max                int
	ErrorKey           string
	Action, DeleteBase string
}

func (h *Handler) renderPhotos(w http.ResponseWriter, r *http.Request, status int, errorKey string) {
	p := packageFrom(r.Context())
	photos, err := h.svc.Photos(r.Context(), p.ID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	v := village.FromContext(r.Context())
	base := managePath(v.ID, fmt.Sprintf("/%d/foto", p.ID))
	h.rd.Page(w, r, status, "manage_package_photos", photosData{
		Village: v, Package: p, Photos: photos, Max: MaxPhotos, ErrorKey: errorKey, Action: base, DeleteBase: base,
	})
}

func (h *Handler) photos(w http.ResponseWriter, r *http.Request) {
	h.renderPhotos(w, r, http.StatusOK, "")
}

func (h *Handler) uploadPhoto(w http.ResponseWriter, r *http.Request) {
	v := village.FromContext(r.Context())
	p := packageFrom(r.Context())
	viewer, _ := httpx.ViewerFrom(r.Context())
	file, _, err := r.FormFile("photo")
	if err != nil {
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, "photo.missing")
		return
	}
	defer file.Close()
	err = h.svc.AddPhoto(r.Context(), viewer.ID, p.ID, file, r.PostFormValue("consent") == "1")
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, "flash.photo_added")
		httpx.Redirect(w, r, managePath(v.ID, fmt.Sprintf("/%d/foto", p.ID)))
	case errors.Is(err, ErrConsentRequired):
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, "photo.consent_required")
	case errors.Is(err, ErrPhotoLimit):
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, "photo.limit")
	case upload.MessageKey(err) != "":
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, upload.MessageKey(err))
	default:
		h.rd.ServerError(w, r, err)
	}
}

func (h *Handler) deletePhoto(w http.ResponseWriter, r *http.Request) {
	v := village.FromContext(r.Context())
	p := packageFrom(r.Context())
	mediaID, err := strconv.ParseInt(chi.URLParam(r, "mediaID"), 10, 64)
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	err = h.svc.DeletePhoto(r.Context(), p.ID, mediaID)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.photo_deleted")
	httpx.Redirect(w, r, managePath(v.ID, fmt.Sprintf("/%d/foto", p.ID)))
}

func (h *Handler) publicPage(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "packageID"), 10, 64)
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	view, err := h.svc.Public(r.Context(), chi.URLParam(r, "slug"), id, viewer.ID)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "package", view)
}

package village

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"desakita/internal/platform/form"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/render"
	"desakita/internal/platform/session"
	"desakita/internal/platform/upload"
)

// PackageCard is a package as listed on a village's public page.
type PackageCard struct {
	ID                           int64
	Name                         string
	PricePerPersonIDR            int64
	DurationDays, DurationNights int16
	CoverPath                    string
	CoverCaption                 string
	CoverWidth, CoverHeight      int
}

// Meta is empty: on a village page the location is already in the heading.
func (c PackageCard) Meta() string { return "" }

// CoverAlt is the cover photo's caption, or the package name.
func (c PackageCard) CoverAlt() string {
	if c.CoverCaption != "" {
		return c.CoverCaption
	}
	return c.Name
}

// PackageLister supplies a village's active packages. Implemented by tourpackage and wired
// in internal/app, so this package never imports tourpackage.
type PackageLister interface {
	ActivePackageCards(ctx context.Context, villageID int64) ([]PackageCard, error)
}

type Handler struct {
	svc      *Service
	packages PackageLister
	sm       *scs.SessionManager
	rd       *render.Renderer
}

func NewHandler(svc *Service, packages PackageLister, sm *scs.SessionManager, rd *render.Renderer) *Handler {
	return &Handler{svc: svc, packages: packages, sm: sm, rd: rd}
}

type ctxKey struct{}

// FromContext returns the village loaded by RequireManager.
func FromContext(ctx context.Context) Village {
	v, _ := ctx.Value(ctxKey{}).(Village)
	return v
}

// RequireManager loads {villageID} and 404s unless the viewer manages it. Use after
// httpx.RequireLogin.
func (h *Handler) RequireManager(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewer, _ := httpx.ViewerFrom(r.Context())
		id, err := strconv.ParseInt(chi.URLParam(r, "villageID"), 10, 64)
		if err != nil {
			h.rd.NotFound(w, r)
			return
		}
		v, err := h.svc.Managed(r.Context(), viewer.ID, id)
		if errors.Is(err, ErrNotFound) {
			h.rd.NotFound(w, r)
			return
		}
		if err != nil {
			h.rd.ServerError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, v)))
	})
}

// DashboardRoutes mounts /kelola pages that aren't tied to one village (caller requires login).
func (h *Handler) DashboardRoutes(r chi.Router) {
	r.Get("/", h.dashboard)
	r.Get("/desa/baru", h.newForm)
	r.Post("/desa/baru", h.create)
}

// ManageRoutes mounts /kelola/desa/{villageID}/... (caller applies RequireManager).
func (h *Handler) ManageRoutes(r chi.Router) {
	r.Get("/", h.overview)
	r.Get("/profil", h.profileForm)
	r.Post("/profil", h.updateProfile)
	r.Get("/foto", h.photos)
	r.Post("/foto", h.uploadPhoto)
	r.Post("/foto/{mediaID}/hapus", h.deletePhoto)
}

func (h *Handler) PublicRoutes(r chi.Router) {
	r.Get("/desa/{slug}", h.publicPage)
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	villages, err := h.svc.ListForUser(r.Context(), viewer.ID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "manage_dashboard", map[string]any{"Villages": villages})
}

type profileData struct {
	Village   Village
	Values    ProfileInput
	Errors    form.Errors
	Districts []District
}

func (h *Handler) renderProfile(w http.ResponseWriter, r *http.Request, status int, page string, d profileData) {
	districts, err := h.svc.Districts(r.Context())
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	d.Districts = districts
	h.rd.Page(w, r, status, page, d)
}

func profileFromForm(r *http.Request) ProfileInput {
	return ProfileInput{
		Name:            r.PostFormValue("name"),
		DistrictID:      r.PostFormValue("district_id"),
		Address:         r.PostFormValue("address"),
		Description:     r.PostFormValue("description"),
		ContactPhone:    r.PostFormValue("contact_phone"),
		ContactWhatsApp: r.PostFormValue("contact_whatsapp"),
	}
}

func (h *Handler) newForm(w http.ResponseWriter, r *http.Request) {
	h.renderProfile(w, r, http.StatusOK, "manage_village_new", profileData{})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	in := profileFromForm(r)
	id, err := h.svc.Create(r.Context(), viewer.ID, in)
	var verr *form.ValidationError
	if errors.As(err, &verr) {
		h.renderProfile(w, r, http.StatusUnprocessableEntity, "manage_village_new", profileData{Values: in, Errors: verr.Fields})
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.village_created")
	httpx.Redirect(w, r, "/kelola/desa/"+strconv.FormatInt(id, 10))
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	h.rd.Page(w, r, http.StatusOK, "manage_village", map[string]any{"Village": FromContext(r.Context())})
}

func (h *Handler) profileForm(w http.ResponseWriter, r *http.Request) {
	v := FromContext(r.Context())
	h.renderProfile(w, r, http.StatusOK, "manage_village_profile", profileData{
		Village: v,
		Values: ProfileInput{
			Name: v.Name, DistrictID: strconv.FormatInt(v.DistrictID, 10), Address: v.Address,
			Description: v.Description, ContactPhone: v.ContactPhone, ContactWhatsApp: v.ContactWhatsApp,
		},
	})
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	v := FromContext(r.Context())
	in := profileFromForm(r)
	err := h.svc.UpdateProfile(r.Context(), v.ID, in)
	var verr *form.ValidationError
	if errors.As(err, &verr) {
		h.renderProfile(w, r, http.StatusUnprocessableEntity, "manage_village_profile", profileData{Village: v, Values: in, Errors: verr.Fields})
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.saved")
	httpx.Redirect(w, r, "/kelola/desa/"+strconv.FormatInt(v.ID, 10))
}

type photosData struct {
	Village  Village
	Photos   []Photo
	Max      int
	ErrorKey string
	// Action is the upload URL; DeleteBase + "/{id}/hapus" deletes a photo.
	Action, DeleteBase string
}

func (h *Handler) renderPhotos(w http.ResponseWriter, r *http.Request, status int, errorKey string) {
	v := FromContext(r.Context())
	photos, err := h.svc.Photos(r.Context(), v.ID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	base := "/kelola/desa/" + strconv.FormatInt(v.ID, 10) + "/foto"
	h.rd.Page(w, r, status, "manage_village_photos", photosData{
		Village: v, Photos: photos, Max: MaxPhotos, ErrorKey: errorKey, Action: base, DeleteBase: base,
	})
}

func (h *Handler) photos(w http.ResponseWriter, r *http.Request) {
	h.renderPhotos(w, r, http.StatusOK, "")
}

func (h *Handler) uploadPhoto(w http.ResponseWriter, r *http.Request) {
	v := FromContext(r.Context())
	viewer, _ := httpx.ViewerFrom(r.Context())
	file, _, err := r.FormFile("photo")
	if err != nil {
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, "photo.missing")
		return
	}
	defer file.Close()
	err = h.svc.AddPhoto(r.Context(), viewer.ID, v.ID, file, r.PostFormValue("consent") == "1", r.PostFormValue("caption"))
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, "flash.photo_added")
		httpx.Redirect(w, r, "/kelola/desa/"+strconv.FormatInt(v.ID, 10)+"/foto")
	case errors.Is(err, ErrConsentRequired):
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, "photo.consent_required")
	case errors.Is(err, ErrPhotoLimit):
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, "photo.limit")
	case errors.Is(err, ErrCaptionTooLong):
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, "photo.caption_too_long")
	case upload.MessageKey(err) != "":
		h.renderPhotos(w, r, http.StatusUnprocessableEntity, upload.MessageKey(err))
	default:
		h.rd.ServerError(w, r, err)
	}
}

func (h *Handler) deletePhoto(w http.ResponseWriter, r *http.Request) {
	v := FromContext(r.Context())
	mediaID, err := strconv.ParseInt(chi.URLParam(r, "mediaID"), 10, 64)
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	err = h.svc.DeletePhoto(r.Context(), v.ID, mediaID)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.photo_deleted")
	httpx.Redirect(w, r, "/kelola/desa/"+strconv.FormatInt(v.ID, 10)+"/foto")
}

func (h *Handler) publicPage(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	v, preview, err := h.svc.Public(r.Context(), chi.URLParam(r, "slug"), viewer.ID)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	photos, err := h.svc.Photos(r.Context(), v.ID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	packages, err := h.packages.ActivePackageCards(r.Context(), v.ID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "village", map[string]any{
		"Village": v, "Preview": preview, "Photos": photos, "Packages": packages,
	})
}

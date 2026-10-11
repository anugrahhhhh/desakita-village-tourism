package review

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"desakita/internal/platform/form"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/render"
	"desakita/internal/platform/session"
)

type Handler struct {
	svc *Service
	sm  *scs.SessionManager
	rd  *render.Renderer
	// CreateLimit wraps review submission (rate limiting); set by internal/app.
	CreateLimit httpx.Middleware
}

func NewHandler(svc *Service, sm *scs.SessionManager, rd *render.Renderer) *Handler {
	return &Handler{svc: svc, sm: sm, rd: rd, CreateLimit: httpx.Pass}
}

// TravelerRoutes mounts the review form under a booking.
func (h *Handler) TravelerRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireLogin)
		r.Get("/pemesanan/{bookingID}/ulasan", h.form)
		r.With(h.CreateLimit).Post("/pemesanan/{bookingID}/ulasan", h.create)
		r.Post("/pemesanan/{bookingID}/ulasan/hapus", h.delete)
	})
}

// AdminRoutes mounts /admin/ulasan; the caller applies RequireAdmin.
func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/ulasan", h.moderation)
	r.Post("/ulasan/{reviewID}/{action}", h.moderate)
}

type formData struct {
	Booking BookingInfo
	Review  *Own
	Values  Input
	Errors  form.Errors
	// Eligible: the booking is completed and has no review yet.
	Eligible      bool
	RatingOptions []RatingOption
}

// RatingOption is one choice of the star rating radio group.
type RatingOption struct{ Value, LabelKey string }

// ratingOptions lists 5 → 1 stars with literal i18n keys (checked by web/i18n_test.go).
var ratingOptions = []RatingOption{
	{"5", "review.rating.5"}, {"4", "review.rating.4"}, {"3", "review.rating.3"},
	{"2", "review.rating.2"}, {"1", "review.rating.1"},
}

func bookingID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "bookingID"), 10, 64)
	return id, err == nil
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, values Input, errs form.Errors) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, ok := bookingID(r)
	if !ok {
		h.rd.NotFound(w, r)
		return
	}
	b, own, err := h.svc.ForBooking(r.Context(), viewer.ID, id)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, status, "review_form", formData{
		Booking: b, Review: own, Values: values, Errors: errs,
		Eligible: own == nil && b.Status == "completed", RatingOptions: ratingOptions,
	})
}

func (h *Handler) form(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, Input{}, nil)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, ok := bookingID(r)
	if !ok {
		h.rd.NotFound(w, r)
		return
	}
	in := Input{Rating: r.PostFormValue("rating"), Body: r.PostFormValue("body")}
	err := h.svc.Create(r.Context(), viewer.ID, id, in)
	var verr *form.ValidationError
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, "flash.review_published")
		httpx.Redirect(w, r, fmt.Sprintf("/pemesanan/%d/ulasan", id))
	case errors.As(err, &verr):
		h.render(w, r, http.StatusUnprocessableEntity, in, verr.Fields)
	case errors.Is(err, ErrNotFound):
		h.rd.NotFound(w, r)
	case errors.Is(err, ErrNotEligible), errors.Is(err, ErrAlreadyReviewed):
		h.sm.Put(r.Context(), session.KeyFlash, "review.error.not_eligible")
		httpx.Redirect(w, r, fmt.Sprintf("/pemesanan/%d/ulasan", id))
	default:
		h.rd.ServerError(w, r, err)
	}
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, ok := bookingID(r)
	if !ok {
		h.rd.NotFound(w, r)
		return
	}
	err := h.svc.Delete(r.Context(), viewer.ID, id)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.review_deleted")
	httpx.Redirect(w, r, fmt.Sprintf("/pemesanan/%d", id))
}

func (h *Handler) moderation(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = StatusPublished
	}
	reported := r.URL.Query().Get("dilaporkan") == "1"
	items, err := h.svc.ForModeration(r.Context(), status, reported)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	counts, err := h.svc.Counts(r.Context())
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "admin_reviews", map[string]any{
		"Items": items, "Status": status, "Reported": reported, "Counts": counts, "Section": "reviews",
	})
}

func (h *Handler) moderate(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "reviewID"), 10, 64)
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	var okKey, back string
	switch chi.URLParam(r, "action") {
	case "sembunyikan":
		err, okKey, back = h.svc.Hide(r.Context(), viewer.ID, id, r.PostFormValue("note")), "flash.review_hidden", "/admin/ulasan"
	case "tampilkan":
		err, okKey, back = h.svc.Restore(r.Context(), viewer.ID, id), "flash.review_restored", "/admin/ulasan?status=hidden"
	default:
		h.rd.NotFound(w, r)
		return
	}
	var verr *form.ValidationError
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, okKey)
	case errors.Is(err, ErrNotFound):
		h.rd.NotFound(w, r)
		return
	case errors.Is(err, ErrInvalidStatusChange):
		h.sm.Put(r.Context(), session.KeyFlash, "admin.error.status_changed")
	case errors.As(err, &verr):
		h.sm.Put(r.Context(), session.KeyFlash, "admin.error.reason_required")
	default:
		h.rd.ServerError(w, r, err)
		return
	}
	httpx.Redirect(w, r, back)
}

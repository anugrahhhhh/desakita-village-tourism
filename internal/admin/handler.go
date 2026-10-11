package admin

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
	"desakita/internal/village"
)

type Handler struct {
	svc      *Service
	villages *village.Service
	sm       *scs.SessionManager
	rd       *render.Renderer
	// ReportLimit wraps report submission (rate limiting); set by internal/app.
	ReportLimit httpx.Middleware
}

func NewHandler(svc *Service, villages *village.Service, sm *scs.SessionManager, rd *render.Renderer) *Handler {
	return &Handler{svc: svc, villages: villages, sm: sm, rd: rd, ReportLimit: httpx.Pass}
}

// ReportRoutes mounts the user-facing report endpoint.
func (h *Handler) ReportRoutes(r chi.Router) {
	r.With(httpx.RequireLogin, h.ReportLimit).Post("/lapor", h.report)
}

// AdminRoutes mounts /admin pages; the caller applies RequireLogin and RequireAdmin.
func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/", h.dashboard)
	r.Get("/desa/{villageID}", h.village)
	r.Post("/desa/{villageID}/{action}", h.villageAction)
	r.Get("/laporan", h.reports)
	r.Post("/laporan/{reportID}/{action}", h.reportAction)
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	back := httpx.SafeRedirect(r.PostFormValue("back"))
	err := h.svc.CreateReport(r.Context(), viewer.ID, r.PostFormValue("target_type"), r.PostFormValue("target_id"), r.PostFormValue("reason"))
	var verr *form.ValidationError
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, "flash.report_sent")
	case errors.As(err, &verr):
		h.sm.Put(r.Context(), session.KeyFlash, "report.error.reason")
	case errors.Is(err, ErrNotFound):
		h.rd.NotFound(w, r)
		return
	default:
		h.rd.ServerError(w, r, err)
		return
	}
	httpx.Redirect(w, r, back)
}

var statusTabs = []string{village.StatusPending, village.StatusVerified, village.StatusSuspended, village.StatusRejected}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = village.StatusPending
	}
	valid := false
	for _, s := range statusTabs {
		valid = valid || s == status
	}
	if !valid {
		h.rd.NotFound(w, r)
		return
	}
	villages, err := h.villages.ListByStatus(r.Context(), status)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	open, err := h.svc.CountOpenReports(r.Context())
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "admin_dashboard", map[string]any{
		"Status": status, "Tabs": statusTabs, "Villages": villages, "OpenReports": open,
	})
}

func (h *Handler) village(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "villageID"), 10, 64)
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	v, err := h.villages.Get(r.Context(), id)
	if errors.Is(err, village.ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	managers, err := h.villages.Managers(r.Context(), id)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	photos, err := h.villages.Photos(r.Context(), id)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "admin_village", map[string]any{"Village": v, "Managers": managers, "Photos": photos})
}

func (h *Handler) villageAction(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "villageID"), 10, 64)
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	ctx, note := r.Context(), r.PostFormValue("note")
	var okKey string
	switch chi.URLParam(r, "action") {
	case "verifikasi":
		err, okKey = h.villages.Verify(ctx, viewer.ID, id), "flash.village_verified"
	case "tolak":
		err, okKey = h.villages.Reject(ctx, viewer.ID, id, note), "flash.village_rejected"
	case "nonaktifkan":
		err, okKey = h.villages.Suspend(ctx, viewer.ID, id, note), "flash.village_suspended"
	case "aktifkan":
		err, okKey = h.villages.Reinstate(ctx, viewer.ID, id), "flash.village_reinstated"
	default:
		h.rd.NotFound(w, r)
		return
	}
	var verr *form.ValidationError
	switch {
	case err == nil:
		h.sm.Put(ctx, session.KeyFlash, okKey)
	case errors.Is(err, village.ErrInvalidStatusChange):
		h.sm.Put(ctx, session.KeyFlash, "admin.error.status_changed")
	case errors.As(err, &verr):
		h.sm.Put(ctx, session.KeyFlash, "admin.error.reason_required")
	default:
		h.rd.ServerError(w, r, err)
		return
	}
	httpx.Redirect(w, r, fmt.Sprintf("/admin/desa/%d", id))
}

func (h *Handler) reports(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.OpenReports(r.Context())
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "admin_reports", map[string]any{"Reports": list})
}

func (h *Handler) reportAction(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "reportID"), 10, 64)
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	status := map[string]string{"selesai": StatusResolved, "abaikan": StatusDismissed}[chi.URLParam(r, "action")]
	if status == "" {
		h.rd.NotFound(w, r)
		return
	}
	err = h.svc.ResolveReport(r.Context(), viewer.ID, id, status)
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, "flash.report_closed")
	case errors.Is(err, ErrNotFound):
		h.sm.Put(r.Context(), session.KeyFlash, "admin.error.report_closed")
	default:
		h.rd.ServerError(w, r, err)
		return
	}
	httpx.Redirect(w, r, "/admin/laporan")
}

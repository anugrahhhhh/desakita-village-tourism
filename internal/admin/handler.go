package admin

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"desakita/internal/platform/audit"
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
	now      func() time.Time
	// ReportLimit wraps report submission (rate limiting); set by internal/app.
	ReportLimit httpx.Middleware
}

func NewHandler(svc *Service, villages *village.Service, sm *scs.SessionManager, rd *render.Renderer) *Handler {
	return &Handler{svc: svc, villages: villages, sm: sm, rd: rd, now: time.Now, ReportLimit: httpx.Pass}
}

// ReportRoutes mounts the user-facing report endpoint.
func (h *Handler) ReportRoutes(r chi.Router) {
	r.With(httpx.RequireLogin, h.ReportLimit).Post("/lapor", h.report)
}

// AdminRoutes mounts /admin pages; the caller applies RequireAdmin.
func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/", h.dashboard)
	r.Get("/desa", h.villageList)
	r.Get("/desa/{villageID}", h.village)
	r.Post("/desa/{villageID}/{action}", h.villageAction)
	r.Get("/laporan", h.reports)
	r.Post("/laporan/{reportID}/{action}", h.reportAction)
	r.Get("/audit", h.auditLog)
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

// QueueItem is a village waiting for verification.
type QueueItem struct {
	village.AdminRow
	DaysWaiting int
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts, err := h.villages.StatusCounts(ctx)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	open, err := h.svc.CountOpenReports(ctx)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	pending, err := h.villages.ListByStatus(ctx, village.StatusPending)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	recent, err := h.svc.AuditLog(ctx, audit.Filter{Limit: 10})
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	queue := make([]QueueItem, len(pending))
	for i, v := range pending {
		queue[i] = QueueItem{AdminRow: v, DaysWaiting: int(math.Floor(h.now().Sub(v.CreatedAt).Hours() / 24))}
	}
	h.rd.Page(w, r, http.StatusOK, "admin_dashboard", map[string]any{
		"Counts": counts, "OpenReports": open, "Queue": queue, "Recent": recent, "Section": "overview",
	})
}

var villageTabs = []string{village.StatusPending, village.StatusVerified, village.StatusSuspended, village.StatusRejected}

func (h *Handler) villageList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = village.StatusPending
	}
	if !slices.Contains(villageTabs, status) {
		h.rd.NotFound(w, r)
		return
	}
	list, err := h.villages.ListByStatus(r.Context(), status)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "admin_villages", map[string]any{
		"Status": status, "Tabs": villageTabs, "Villages": list, "Section": "villages",
	})
}

func (h *Handler) village(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "villageID"), 10, 64)
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	v, err := h.villages.Get(ctx, id)
	if errors.Is(err, village.ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	managers, err := h.villages.Managers(ctx, id)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	photos, err := h.villages.Photos(ctx, id)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	history, err := h.svc.AuditLog(ctx, audit.Filter{TargetType: audit.TargetVillage, TargetID: id, Limit: 20})
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	openReports, err := h.svc.CountOpenReportsForVillage(ctx, id)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "admin_village", map[string]any{
		"Village": v, "Managers": managers, "Photos": photos, "History": history,
		"OpenReports": openReports, "Section": "villages",
	})
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
	case errors.Is(err, village.ErrNotFound):
		h.rd.NotFound(w, r)
		return
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
	status := r.URL.Query().Get("status")
	if status == "" {
		status = StatusOpen
	}
	list, err := h.svc.Reports(r.Context(), status)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "admin_reports", map[string]any{
		"Reports": list, "Status": status, "Tabs": ReportStatuses, "Section": "reports",
	})
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
	err = h.svc.ResolveReport(r.Context(), viewer.ID, id, status, r.PostFormValue("note"))
	var verr *form.ValidationError
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, "flash.report_closed")
	case errors.Is(err, ErrNotFound):
		h.sm.Put(r.Context(), session.KeyFlash, "admin.error.report_closed")
	case errors.As(err, &verr):
		h.sm.Put(r.Context(), session.KeyFlash, "admin.error.note_too_long")
	default:
		h.rd.ServerError(w, r, err)
		return
	}
	httpx.Redirect(w, r, "/admin/laporan")
}

const auditPageSize = 50

var auditTargets = []string{audit.TargetVillage, audit.TargetReport, audit.TargetUser}

// ActionOption is an entry of the audit action filter.
type ActionOption struct{ Value, LabelKey string }

func (h *Handler) auditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := audit.Filter{Limit: auditPageSize + 1}
	if a := q.Get("aksi"); slices.Contains(audit.Actions(), a) {
		f.Action = a
	}
	if t := q.Get("target"); slices.Contains(auditTargets, t) {
		f.TargetType = t
	}
	page := 1
	if n, err := strconv.Atoi(q.Get("hal")); err == nil && n > 1 && n <= 10000 {
		page = n
	}
	f.Offset = (page - 1) * auditPageSize
	items, err := h.svc.AuditLog(r.Context(), f)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	hasNext := len(items) > auditPageSize
	if hasNext {
		items = items[:auditPageSize]
	}
	pageQuery := func(p int) string {
		v := url.Values{}
		if f.Action != "" {
			v.Set("aksi", f.Action)
		}
		if f.TargetType != "" {
			v.Set("target", f.TargetType)
		}
		if p > 1 {
			v.Set("hal", strconv.Itoa(p))
		}
		return v.Encode()
	}
	data := map[string]any{
		"Items": items, "Action": f.Action, "Target": f.TargetType, "Section": "audit",
		"Targets": auditTargets, "Page": page,
	}
	options := make([]ActionOption, 0, len(audit.Actions()))
	for _, a := range audit.Actions() {
		options = append(options, ActionOption{Value: a, LabelKey: audit.ActionKeyOf(a)})
	}
	data["ActionOptions"] = options
	if page > 1 {
		data["PrevQuery"] = pageQuery(page - 1)
	}
	if hasNext {
		data["NextQuery"] = pageQuery(page + 1)
	}
	h.rd.Page(w, r, http.StatusOK, "admin_audit", data)
}

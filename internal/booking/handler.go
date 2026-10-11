package booking

import (
	"context"
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

	"desakita/internal/platform/form"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/i18n"
	"desakita/internal/platform/phone"
	"desakita/internal/platform/render"
	"desakita/internal/platform/session"
)

// PackageSummary is the package and village data booking pages display.
type PackageSummary struct {
	ID, VillageID     int64
	Name              string
	DefaultQuota      int32
	PricePerPersonIDR int64
	// Bookable: package active and village verified.
	Bookable        bool
	VillageName     string
	VillageSlug     string
	VillagePhone    string
	VillageWhatsApp string
}

type VillageSummary struct {
	ID         int64
	Name, Slug string
}

// Catalog supplies display data from tourpackage and village (adapter in internal/app).
type Catalog interface {
	Package(ctx context.Context, id int64) (PackageSummary, error) // ErrNotFound if missing
	VillagePackages(ctx context.Context, villageID int64) ([]PackageSummary, error)
	Village(ctx context.Context, villageID int64) (VillageSummary, error)
}

type Handler struct {
	svc     *Service
	catalog Catalog
	sm      *scs.SessionManager
	rd      *render.Renderer
	tr      *i18n.Translator
}

func NewHandler(svc *Service, catalog Catalog, sm *scs.SessionManager, rd *render.Renderer, tr *i18n.Translator) *Handler {
	return &Handler{svc: svc, catalog: catalog, sm: sm, rd: rd, tr: tr}
}

// TravelerRoutes mounts the booking form and "Pemesanan saya".
func (h *Handler) TravelerRoutes(r chi.Router) {
	r.Get("/pesan/paket/{packageID}", h.bookForm)
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireLogin)
		r.Post("/pesan/paket/{packageID}", h.create)
		r.Get("/pemesanan", h.myBookings)
		r.Get("/pemesanan/{bookingID}", h.myBooking)
		r.Post("/pemesanan/{bookingID}/batal", h.travelerCancel)
	})
}

// ManageRoutes mounts /kelola/desa/{villageID}/... booking pages. The caller applies login
// and village-manager middleware; every service call re-checks ownership anyway.
func (h *Handler) ManageRoutes(r chi.Router) {
	r.Get("/pemesanan", h.manageList)
	r.Get("/pemesanan/panel", h.managePanel)
	r.Get("/pemesanan/{bookingID}", h.manageDetail)
	r.Post("/pemesanan/{bookingID}/{action}", h.manageAction)
	r.Get("/kalender/{packageID}", h.calendar)
	r.Post("/kalender/{packageID}/kuota", h.setQuota)
	r.Post("/kalender/{packageID}/tutup", h.setClosed)
}

func idParam(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return id, err == nil
}

// ---------------------------------------------------------------------------------------
// Traveler

type bookFormData struct {
	Package             PackageSummary
	Month               Month
	FirstDate, LastDate time.Time
	Values              map[string]string
	Errors              form.Errors
	ErrorKey            string
}

func (h *Handler) bookablePackage(w http.ResponseWriter, r *http.Request) (PackageSummary, bool) {
	id, ok := idParam(r, "packageID")
	if !ok {
		h.rd.NotFound(w, r)
		return PackageSummary{}, false
	}
	p, err := h.catalog.Package(r.Context(), id)
	if errors.Is(err, ErrNotFound) || (err == nil && !p.Bookable) {
		h.rd.NotFound(w, r)
		return PackageSummary{}, false
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return PackageSummary{}, false
	}
	return p, true
}

func (h *Handler) renderBookForm(w http.ResponseWriter, r *http.Request, status int, p PackageSummary, d bookFormData) {
	first := h.svc.MonthStart(r.URL.Query().Get("bulan"))
	if sel, err := ParseDate(d.Values["start_date"]); err == nil && r.URL.Query().Get("bulan") == "" {
		first = h.svc.MonthStart(sel.Format("2006-01"))
	}
	from, to := MonthRange(first)
	days, err := h.svc.Calendar(r.Context(), p.ID, p.DefaultQuota, from, to)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	d.Package = p
	d.Month = h.svc.MonthGrid(days, first)
	d.FirstDate, d.LastDate = h.svc.BookableRange()
	h.rd.Page(w, r, status, "booking_new", d)
}

func (h *Handler) bookForm(w http.ResponseWriter, r *http.Request) {
	p, ok := h.bookablePackage(w, r)
	if !ok {
		return
	}
	values := map[string]string{"party_size": "1"}
	if v, ok := httpx.ViewerFrom(r.Context()); ok {
		values["contact_name"], values["contact_phone"] = v.Name, v.Phone
	}
	h.renderBookForm(w, r, http.StatusOK, p, bookFormData{Values: values})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.bookablePackage(w, r)
	if !ok {
		return
	}
	viewer, _ := httpx.ViewerFrom(r.Context())
	in := CreateInput{
		PackageID:    p.ID,
		StartDate:    r.PostFormValue("start_date"),
		PartySize:    r.PostFormValue("party_size"),
		Note:         r.PostFormValue("note"),
		ContactName:  r.PostFormValue("contact_name"),
		ContactPhone: r.PostFormValue("contact_phone"),
	}
	b, err := h.svc.Create(r.Context(), viewer.ID, in)
	if err == nil {
		h.sm.Put(r.Context(), session.KeyFlash, "flash.booking_sent")
		httpx.Redirect(w, r, fmt.Sprintf("/pemesanan/%d", b.ID))
		return
	}
	d := bookFormData{Values: map[string]string{
		"start_date": in.StartDate, "party_size": in.PartySize, "note": in.Note,
		"contact_name": in.ContactName, "contact_phone": in.ContactPhone,
	}}
	var verr *form.ValidationError
	switch {
	case errors.As(err, &verr):
		d.Errors = verr.Fields
		h.renderBookForm(w, r, http.StatusUnprocessableEntity, p, d)
	case errors.Is(err, ErrDateOutOfWindow):
		d.Errors = form.Errors{"start_date": "booking.error.date_window"}
		h.renderBookForm(w, r, http.StatusUnprocessableEntity, p, d)
	case errors.Is(err, ErrDateClosed):
		d.ErrorKey = "booking.error.date_closed"
		h.renderBookForm(w, r, http.StatusConflict, p, d)
	case errors.Is(err, ErrSoldOut):
		d.ErrorKey = "booking.error.sold_out"
		h.renderBookForm(w, r, http.StatusConflict, p, d)
	case errors.Is(err, ErrPendingLimit):
		d.ErrorKey = "booking.error.pending_limit"
		h.renderBookForm(w, r, http.StatusConflict, p, d)
	case errors.Is(err, ErrNotBookable), errors.Is(err, ErrNotFound):
		h.rd.NotFound(w, r)
	default:
		h.rd.ServerError(w, r, err)
	}
}

// Row is a booking with its package for list and detail pages.
type Row struct {
	Booking
	Package PackageSummary
	// ExpiresInHours is the rounded-up time left to respond (pending only); 0 means < 1 hour.
	ExpiresInHours int
}

// withPackages attaches package summaries, fetching each package once.
func (h *Handler) withPackages(ctx context.Context, bookings []Booking) ([]Row, error) {
	cache := map[int64]PackageSummary{}
	now := h.svc.cfg.Now()
	rows := make([]Row, len(bookings))
	for i, b := range bookings {
		p, ok := cache[b.PackageID]
		if !ok {
			var err error
			if p, err = h.catalog.Package(ctx, b.PackageID); err != nil {
				return nil, err
			}
			cache[b.PackageID] = p
		}
		rows[i] = Row{Booking: b, Package: p}
		if b.Status == StatusPending {
			rows[i].ExpiresInHours = int(math.Floor(b.ExpiresAt.Sub(now).Hours()))
		}
	}
	return rows, nil
}

func (h *Handler) myBookings(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	list, err := h.svc.ListForTraveler(r.Context(), viewer.ID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	rows, err := h.withPackages(r.Context(), list)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.rd.Page(w, r, http.StatusOK, "my_bookings", map[string]any{"Rows": rows})
}

type detailData struct {
	Row     Row
	Actions Actions
	WALink  string
	Loc     *time.Location
}

func (h *Handler) myBooking(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, ok := idParam(r, "bookingID")
	if !ok {
		h.rd.NotFound(w, r)
		return
	}
	b, err := h.svc.ForTraveler(r.Context(), viewer.ID, id)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	rows, err := h.withPackages(r.Context(), []Booking{b})
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	row := rows[0]
	contact := row.Package.VillageWhatsApp
	if contact == "" {
		contact = row.Package.VillagePhone
	}
	h.rd.Page(w, r, http.StatusOK, "my_booking", detailData{
		Row:     row,
		Actions: Actions{Cancel: h.svc.ActionsFor(b).Cancel},
		WALink:  phone.WALink(contact, h.waMessage("booking.wa.traveler", row)),
		Loc:     h.svc.cfg.Location,
	})
}

func (h *Handler) travelerCancel(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	id, ok := idParam(r, "bookingID")
	if !ok {
		h.rd.NotFound(w, r)
		return
	}
	_, err := h.svc.CancelByTraveler(r.Context(), viewer.ID, id, r.PostFormValue("note"))
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	h.flashResult(r, err, "flash.booking_cancelled")
	if err != nil && !isUserError(err) {
		h.rd.ServerError(w, r, err)
		return
	}
	httpx.Redirect(w, r, fmt.Sprintf("/pemesanan/%d", id))
}

// waMessage builds a WhatsApp message. Every message key uses indexed verbs over:
// 1 traveler name, 2 village, 3 package, 4 date, 5 party size, 6 total, 7 booking id.
func (h *Handler) waMessage(key string, row Row) string {
	return h.tr.T(key, row.ContactName, row.Package.VillageName, row.Package.Name,
		render.FormatDate(h.tr, row.StartDate), row.PartySize, render.FormatRupiah(row.TotalPriceIDR), row.ID)
}

// waStatusKeys picks the manager-to-traveler WhatsApp message for each status.
var waStatusKeys = map[string]string{
	StatusPending:   "booking.wa.pending",
	StatusConfirmed: "booking.wa.confirmed",
	StatusRejected:  "booking.wa.rejected",
	StatusCancelled: "booking.wa.cancelled",
	StatusExpired:   "booking.wa.expired",
	StatusCompleted: "booking.wa.completed",
}

func isUserError(err error) bool {
	var verr *form.ValidationError
	return errors.Is(err, ErrInvalidTransition) || errors.As(err, &verr)
}

// flashResult sets a success flash, or an error flash for user-facing failures.
func (h *Handler) flashResult(r *http.Request, err error, okKey string) {
	var verr *form.ValidationError
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, okKey)
	case errors.Is(err, ErrInvalidTransition):
		h.sm.Put(r.Context(), session.KeyFlash, "booking.error.invalid_transition")
	case errors.As(err, &verr):
		h.sm.Put(r.Context(), session.KeyFlash, "booking.error.note_too_long")
	}
}

// ---------------------------------------------------------------------------------------
// Manager

type dashboardData struct {
	Village    VillageSummary
	Pending    []Row
	Upcoming   []Row
	ToComplete []Row
	Limit      int // 0 = show all
	Loc        *time.Location
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) (dashboardData, bool) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	villageID, ok := idParam(r, "villageID")
	if !ok {
		h.rd.NotFound(w, r)
		return dashboardData{}, false
	}
	list, err := h.svc.ListForVillage(r.Context(), viewer.ID, villageID)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return dashboardData{}, false
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return dashboardData{}, false
	}
	v, err := h.catalog.Village(r.Context(), villageID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return dashboardData{}, false
	}
	rows, err := h.withPackages(r.Context(), list)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return dashboardData{}, false
	}
	today := h.svc.Today()
	d := dashboardData{Village: v, Loc: h.svc.cfg.Location}
	for _, row := range rows {
		switch {
		case row.Status == StatusPending:
			d.Pending = append(d.Pending, row)
		case row.Status == StatusConfirmed && row.StartDate.Before(today):
			d.ToComplete = append(d.ToComplete, row)
		case row.Status == StatusConfirmed:
			d.Upcoming = append(d.Upcoming, row)
		}
	}
	slices.SortFunc(d.Pending, func(a, b Row) int { return a.ExpiresAt.Compare(b.ExpiresAt) })
	return d, true
}

func (h *Handler) manageList(w http.ResponseWriter, r *http.Request) {
	if d, ok := h.dashboard(w, r); ok {
		h.rd.Page(w, r, http.StatusOK, "manage_bookings", d)
	}
}

// managePanel is the HTMX fragment embedded on the village overview.
func (h *Handler) managePanel(w http.ResponseWriter, r *http.Request) {
	if d, ok := h.dashboard(w, r); ok {
		d.Limit = 5
		h.rd.Partial(w, r, http.StatusOK, "manage_bookings", "bookings_lists", d)
	}
}

// managedBooking loads {bookingID} for the viewer and requires it to belong to {villageID}.
func (h *Handler) managedBooking(w http.ResponseWriter, r *http.Request) (Booking, bool) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	villageID, ok1 := idParam(r, "villageID")
	id, ok2 := idParam(r, "bookingID")
	if !ok1 || !ok2 {
		h.rd.NotFound(w, r)
		return Booking{}, false
	}
	b, err := h.svc.ForManager(r.Context(), viewer.ID, id)
	if errors.Is(err, ErrNotFound) || (err == nil && b.VillageID != villageID) {
		h.rd.NotFound(w, r)
		return Booking{}, false
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return Booking{}, false
	}
	return b, true
}

func (h *Handler) manageDetail(w http.ResponseWriter, r *http.Request) {
	b, ok := h.managedBooking(w, r)
	if !ok {
		return
	}
	rows, err := h.withPackages(r.Context(), []Booking{b})
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	row := rows[0]
	var link string
	if b.TravelerID != nil { // deleted accounts have no contact details left
		link = phone.WALink(b.ContactPhone, h.waMessage(waStatusKeys[b.Status], row))
	}
	h.rd.Page(w, r, http.StatusOK, "manage_booking", detailData{
		Row: row, Actions: h.svc.ActionsFor(b), WALink: link, Loc: h.svc.cfg.Location,
	})
}

func (h *Handler) manageAction(w http.ResponseWriter, r *http.Request) {
	b, ok := h.managedBooking(w, r)
	if !ok {
		return
	}
	viewer, _ := httpx.ViewerFrom(r.Context())
	ctx, note := r.Context(), r.PostFormValue("note")
	var err error
	var okKey string
	switch chi.URLParam(r, "action") {
	case "konfirmasi":
		_, err = h.svc.Confirm(ctx, viewer.ID, b.ID)
		okKey = "flash.booking_confirmed"
	case "tolak":
		_, err = h.svc.Reject(ctx, viewer.ID, b.ID, note)
		okKey = "flash.booking_rejected"
	case "batal":
		_, err = h.svc.CancelByManager(ctx, viewer.ID, b.ID, note)
		okKey = "flash.booking_cancelled"
	case "selesai":
		_, err = h.svc.Complete(ctx, viewer.ID, b.ID)
		okKey = "flash.booking_completed"
	default:
		h.rd.NotFound(w, r)
		return
	}
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil && !isUserError(err) {
		h.rd.ServerError(w, r, err)
		return
	}
	h.flashResult(r, err, okKey)
	httpx.Redirect(w, r, fmt.Sprintf("/kelola/desa/%d/pemesanan/%d", b.VillageID, b.ID))
}

type calendarData struct {
	Village  VillageSummary
	Package  PackageSummary
	Packages []PackageSummary
	Month    Month
	Selected *Cell
}

// managedPackage loads {packageID} and requires it to belong to {villageID}. The service
// re-checks that the viewer manages that village on every write.
func (h *Handler) managedPackage(w http.ResponseWriter, r *http.Request) (PackageSummary, bool) {
	villageID, ok1 := idParam(r, "villageID")
	id, ok2 := idParam(r, "packageID")
	if !ok1 || !ok2 {
		h.rd.NotFound(w, r)
		return PackageSummary{}, false
	}
	p, err := h.catalog.Package(r.Context(), id)
	if errors.Is(err, ErrNotFound) || (err == nil && p.VillageID != villageID) {
		h.rd.NotFound(w, r)
		return PackageSummary{}, false
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return PackageSummary{}, false
	}
	return p, true
}

func (h *Handler) calendar(w http.ResponseWriter, r *http.Request) {
	p, ok := h.managedPackage(w, r)
	if !ok {
		return
	}
	first := h.svc.MonthStart(r.URL.Query().Get("bulan"))
	from, to := MonthRange(first)
	days, err := h.svc.Calendar(r.Context(), p.ID, p.DefaultQuota, from, to)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	pkgs, err := h.catalog.VillagePackages(r.Context(), p.VillageID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	v, err := h.catalog.Village(r.Context(), p.VillageID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	d := calendarData{Village: v, Package: p, Packages: pkgs, Month: h.svc.MonthGrid(days, first)}
	if sel, err := ParseDate(r.URL.Query().Get("tanggal")); err == nil {
		for _, week := range d.Month.Weeks {
			for i := range week {
				if week[i].Date.Equal(sel) && week[i].Editable {
					d.Selected = &week[i]
				}
			}
		}
	}
	h.rd.Page(w, r, http.StatusOK, "manage_calendar", d)
}

func calendarURL(p PackageSummary, date time.Time) string {
	q := url.Values{"bulan": {date.Format("2006-01")}, "tanggal": {date.Format(time.DateOnly)}}
	return fmt.Sprintf("/kelola/desa/%d/kalender/%d?%s", p.VillageID, p.ID, q.Encode())
}

func (h *Handler) calendarWrite(w http.ResponseWriter, r *http.Request, write func(p PackageSummary, date time.Time) error, okKey string) {
	p, ok := h.managedPackage(w, r)
	if !ok {
		return
	}
	date, err := ParseDate(r.PostFormValue("date"))
	if err != nil {
		h.rd.NotFound(w, r)
		return
	}
	err = write(p, date)
	var verr *form.ValidationError
	switch {
	case err == nil:
		h.sm.Put(r.Context(), session.KeyFlash, okKey)
	case errors.Is(err, ErrNotFound):
		h.rd.NotFound(w, r)
		return
	case errors.Is(err, ErrQuotaBelowReserved):
		h.sm.Put(r.Context(), session.KeyFlash, "booking.error.quota_below_reserved")
	case errors.Is(err, ErrDateOutOfWindow):
		h.sm.Put(r.Context(), session.KeyFlash, "booking.error.calendar_window")
	case errors.As(err, &verr):
		h.sm.Put(r.Context(), session.KeyFlash, "validation.quota")
	default:
		h.rd.ServerError(w, r, err)
		return
	}
	httpx.Redirect(w, r, calendarURL(p, date))
}

func (h *Handler) setQuota(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	h.calendarWrite(w, r, func(p PackageSummary, date time.Time) error {
		quota, err := strconv.Atoi(r.PostFormValue("quota"))
		if err != nil {
			return form.Errors{"quota": "validation.quota"}.Err()
		}
		return h.svc.SetQuota(r.Context(), viewer.ID, p.ID, date, quota)
	}, "flash.quota_saved")
}

func (h *Handler) setClosed(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	closed := r.PostFormValue("closed") == "1"
	key := "flash.date_opened"
	if closed {
		key = "flash.date_closed"
	}
	h.calendarWrite(w, r, func(p PackageSummary, date time.Time) error {
		return h.svc.SetClosed(r.Context(), viewer.ID, p.ID, date, closed)
	}, key)
}

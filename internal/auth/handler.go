package auth

import (
	"errors"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"desakita/internal/platform/csrf"
	"desakita/internal/platform/form"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/render"
	"desakita/internal/platform/session"
)

type Handler struct {
	svc *Service
	sm  *scs.SessionManager
	rd  *render.Renderer
}

func NewHandler(svc *Service, sm *scs.SessionManager, rd *render.Renderer) *Handler {
	return &Handler{svc: svc, sm: sm, rd: rd}
}

// Routes mounts the auth pages. The caller applies session, CSRF and LoadViewer first.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/daftar", h.registerForm)
	r.Post("/daftar", h.register)
	r.Get("/masuk", h.loginForm)
	r.Post("/masuk", h.login)
	r.Post("/keluar", h.logout)
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireLogin)
		r.Get("/akun", h.account)
		r.Post("/akun/hapus", h.deleteAccount)
	})
}

// LoadViewer puts the logged-in user, if any, into the request context.
func (h *Handler) LoadViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := h.sm.GetInt64(ctx, session.KeyUserID)
		if id == 0 {
			next.ServeHTTP(w, r)
			return
		}
		u, err := h.svc.UserByID(ctx, id)
		switch {
		case errors.Is(err, ErrNotFound):
			// Account was deleted from another session.
			h.sm.Remove(ctx, session.KeyUserID)
		case err != nil:
			h.rd.ServerError(w, r, err)
			return
		default:
			ctx = httpx.WithViewer(ctx, httpx.Viewer{ID: u.ID, Name: u.Name, Email: u.Email, IsAdmin: u.IsAdmin})
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type formData struct {
	Values   map[string]string
	Errors   form.Errors
	Next     string
	ErrorKey string
}

func (h *Handler) redirectIfLoggedIn(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := httpx.ViewerFrom(r.Context()); ok {
		httpx.Redirect(w, r, httpx.SafeRedirect(r.URL.Query().Get("next")))
		return true
	}
	return false
}

func (h *Handler) registerForm(w http.ResponseWriter, r *http.Request) {
	if h.redirectIfLoggedIn(w, r) {
		return
	}
	h.rd.Page(w, r, http.StatusOK, "register", formData{Next: nextParam(r)})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	in := RegisterInput{
		Name:     r.PostFormValue("name"),
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
		Phone:    r.PostFormValue("phone"),
	}
	u, err := h.svc.Register(r.Context(), in)
	var verr *form.ValidationError
	if errors.As(err, &verr) {
		h.rd.Page(w, r, http.StatusUnprocessableEntity, "register", formData{
			Values: map[string]string{"name": in.Name, "email": in.Email, "phone": in.Phone},
			Errors: verr.Fields,
			Next:   nextParam(r),
		})
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	if err := h.startSession(r, u.ID); err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.registered")
	httpx.Redirect(w, r, httpx.SafeRedirect(nextParam(r)))
}

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	if h.redirectIfLoggedIn(w, r) {
		return
	}
	h.rd.Page(w, r, http.StatusOK, "login", formData{Next: nextParam(r)})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	u, err := h.svc.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, ErrInvalidCredentials) {
		h.rd.Page(w, r, http.StatusUnprocessableEntity, "login", formData{
			Values:   map[string]string{"email": email},
			Next:     nextParam(r),
			ErrorKey: "auth.login.invalid",
		})
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	if err := h.startSession(r, u.ID); err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(r.Context(), session.KeyFlash, "flash.logged_in")
	httpx.Redirect(w, r, httpx.SafeRedirect(nextParam(r)))
}

// startSession issues a fresh session token (prevents fixation) and rotates the CSRF token.
func (h *Handler) startSession(r *http.Request, userID int64) error {
	ctx := r.Context()
	if err := h.sm.RenewToken(ctx); err != nil {
		return err
	}
	csrf.Rotate(ctx, h.sm)
	h.sm.Put(ctx, session.KeyUserID, userID)
	return nil
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.sm.Destroy(ctx); err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(ctx, session.KeyFlash, "flash.logged_out")
	httpx.Redirect(w, r, "/")
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request) {
	h.rd.Page(w, r, http.StatusOK, "account", formData{})
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewer, _ := httpx.ViewerFrom(ctx)
	err := h.svc.DeleteAccount(ctx, viewer.ID, r.PostFormValue("password"))
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		h.rd.Page(w, r, http.StatusUnprocessableEntity, "account", formData{ErrorKey: "account.delete.wrong_password"})
		return
	case errors.Is(err, ErrLastManager):
		h.rd.Page(w, r, http.StatusConflict, "account", formData{ErrorKey: "account.delete.last_manager"})
		return
	case err != nil:
		h.rd.ServerError(w, r, err)
		return
	}
	if err := h.sm.Destroy(ctx); err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	h.sm.Put(ctx, session.KeyFlash, "flash.account_deleted")
	httpx.Redirect(w, r, "/")
}

// nextParam reads "next" from the form (POST) or query (GET); callers must pass it
// through SafeRedirect before redirecting.
func nextParam(r *http.Request) string {
	if v := r.PostFormValue("next"); v != "" {
		return httpx.SafeRedirect(v)
	}
	if v := r.URL.Query().Get("next"); v != "" {
		return httpx.SafeRedirect(v)
	}
	return ""
}

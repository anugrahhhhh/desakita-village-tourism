// Package render executes page templates inside the shared layout.
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5/middleware"

	"desakita/internal/platform/csrf"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/i18n"
	"desakita/internal/platform/session"
)

// View is the root value every template receives.
type View struct {
	Lang   string
	Viewer *httpx.Viewer
	CSRF   string
	Flash  string
	Path   string
	Data   any
}

type Renderer struct {
	pages  map[string]*template.Template
	tr     *i18n.Translator
	sm     *scs.SessionManager
	logger *slog.Logger
}

// New parses templates/layout.html + templates/partials/*.html with each templates/pages/*.html.
// A page named "pages/login.html" is rendered as "login".
func New(fsys fs.FS, tr *i18n.Translator, sm *scs.SessionManager, logger *slog.Logger) (*Renderer, error) {
	funcs := template.FuncMap{
		"t": tr.T,
		// rupiah formats an integer amount as "Rp 150.000".
		"rupiah": formatRupiah,
	}
	pageFiles, err := fs.Glob(fsys, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	shared := []string{"templates/layout.html"}
	if partials, _ := fs.Glob(fsys, "templates/partials/*.html"); len(partials) > 0 {
		shared = append(shared, partials...)
	}
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, pf := range pageFiles {
		tmpl, err := template.New("").Funcs(funcs).ParseFS(fsys, append(shared, pf)...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", pf, err)
		}
		pages[strings.TrimSuffix(path.Base(pf), ".html")] = tmpl
	}
	return &Renderer{pages: pages, tr: tr, sm: sm, logger: logger}, nil
}

// Page renders page with status. data becomes View.Data.
func (rd *Renderer) Page(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	tmpl, ok := rd.pages[page]
	if !ok {
		rd.ServerError(w, r, fmt.Errorf("render: unknown page %q", page))
		return
	}
	ctx := r.Context()
	v := View{
		Lang:  rd.tr.Lang(),
		CSRF:  csrf.Token(ctx, rd.sm),
		Flash: rd.sm.PopString(ctx, session.KeyFlash),
		Path:  r.URL.Path,
		Data:  data,
	}
	if viewer, ok := httpx.ViewerFrom(ctx); ok {
		v.Viewer = &viewer
	}
	// Render to a buffer so a template error never sends a half-written 200.
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", v); err != nil {
		rd.ServerError(w, r, fmt.Errorf("render %s: %w", page, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w) //nolint:errcheck
}

// ErrorData is the payload of the "error" page.
type ErrorData struct {
	TitleKey, MessageKey string
	RequestID            string
}

func (rd *Renderer) errorPage(w http.ResponseWriter, r *http.Request, status int, data ErrorData) {
	tmpl, ok := rd.pages["error"]
	if !ok {
		http.Error(w, http.StatusText(status), status)
		return
	}
	v := View{Lang: rd.tr.Lang(), Path: r.URL.Path, Data: data}
	if viewer, ok := httpx.ViewerFrom(r.Context()); ok {
		v.Viewer = &viewer
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", v); err != nil {
		rd.logger.Error("render error page", "err", err)
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w) //nolint:errcheck
}

func (rd *Renderer) NotFound(w http.ResponseWriter, r *http.Request) {
	rd.errorPage(w, r, http.StatusNotFound, ErrorData{TitleKey: "error.not_found.title", MessageKey: "error.not_found.message"})
}

func (rd *Renderer) Forbidden(w http.ResponseWriter, r *http.Request) {
	rd.errorPage(w, r, http.StatusForbidden, ErrorData{TitleKey: "error.forbidden.title", MessageKey: "error.forbidden.message"})
}

// ServerError logs err with the request ID and shows a generic page; details never reach the user.
func (rd *Renderer) ServerError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := middleware.GetReqID(r.Context())
	rd.logger.Error("server error", "err", err, "request_id", reqID, "method", r.Method, "path", r.URL.Path)
	rd.errorPage(w, r, http.StatusInternalServerError, ErrorData{
		TitleKey: "error.server.title", MessageKey: "error.server.message", RequestID: reqID,
	})
}

func formatRupiah(amount int64) string {
	neg := amount < 0
	if neg {
		amount = -amount
	}
	s := fmt.Sprintf("%d", amount)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-Rp " + b.String()
	}
	return "Rp " + b.String()
}

// Package render executes page templates inside the shared layout.
package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5/middleware"

	"desakita/internal/platform/csrf"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/i18n"
	"desakita/internal/platform/session"
)

// View is the root value every template receives.
type View struct {
	Lang string
	// Langs lists the supported languages for the language switcher.
	Langs []string
	// URI is the current path and query, the switcher's return address.
	URI    string
	Viewer *httpx.Viewer
	CSRF   string
	Flash  string
	Path   string
	Data   any
}

// zone holds the display time zone; templates read it through the "local" func.
type zone struct{ loc *time.Location }

// Crumb is one breadcrumb step; an empty Href is the current page.
type Crumb struct{ Href, Label string }

// MapConfig is what map pages need to set up Leaflet (SPEC §14); templates read it
// through the "mapConfig" func.
type MapConfig struct {
	Tiles           string // URL template served by this app, e.g. /tiles/{z}/{x}/{y}.png
	MaxZoom         int
	AttributionText string
	AttributionURL  string
}

// assets maps a static file path ("app.css") to a short hash of its content, so asset URLs
// change whenever the file does and browsers never mix new pages with cached old CSS/JS.
type assets struct{ hash map[string]string }

// newAssets hashes every file in fsys (the embedded web/static tree).
func newAssets(fsys fs.FS) (*assets, error) {
	a := &assets{hash: map[string]string{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		a.hash[p] = hex.EncodeToString(sum[:4])
		return nil
	})
	return a, err
}

// url returns "/static/<p>?v=<hash>", or "/static/<p>" for an unknown file.
func (a *assets) url(p string) string {
	if a != nil {
		if h, ok := a.hash[p]; ok {
			return "/static/" + p + "?v=" + h
		}
	}
	return "/static/" + p
}

type Renderer struct {
	zone   *zone
	mapCfg *MapConfig
	assets *assets
	// pages[lang][name]: each page is parsed once per language, so "t" is bound at parse
	// time and rendering stays as fast as with one language.
	pages  map[string]map[string]*template.Template
	bundle *i18n.Bundle
	sm     *scs.SessionManager
	logger *slog.Logger
}

// New parses templates/layout.html + templates/partials/*.html with each templates/pages/*.html.
// A page named "pages/login.html" is rendered as "login".
func New(fsys fs.FS, bundle *i18n.Bundle, sm *scs.SessionManager, logger *slog.Logger) (*Renderer, error) {
	z := &zone{loc: time.UTC}
	mc := &MapConfig{}
	as := &assets{}
	pages := map[string]map[string]*template.Template{}
	for _, lang := range i18n.Supported {
		p, err := parsePages(fsys, bundle.For(lang), z, mc, as)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", lang, err)
		}
		pages[lang] = p
	}
	return &Renderer{zone: z, mapCfg: mc, assets: as, pages: pages, bundle: bundle, sm: sm, logger: logger}, nil
}

// SetStatic hashes the static files so templates can link them with {{asset "app.css"}}.
// Call before serving.
func (rd *Renderer) SetStatic(fsys fs.FS) error {
	a, err := newAssets(fsys)
	if err != nil {
		return fmt.Errorf("hash static files: %w", err)
	}
	*rd.assets = *a
	return nil
}

func parsePages(fsys fs.FS, tr *i18n.Translator, z *zone, mc *MapConfig, as *assets) (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		// asset links a static file with a content hash (cache busting).
		"asset":     as.url,
		"mapConfig": func() MapConfig { return *mc },
		// initial is the first letter of a name, for the account avatar.
		"initial": func(name string) string {
			for _, r := range strings.TrimSpace(name) {
				return strings.ToUpper(string(r))
			}
			return "?"
		},
		"year": func() int { return time.Now().In(z.loc).Year() },
		// crumbs builds a breadcrumb trail from href/label pairs; an empty href marks the
		// current page.
		"crumbs": func(pairs ...string) []Crumb {
			out := make([]Crumb, 0, len(pairs)/2)
			for i := 0; i+1 < len(pairs); i += 2 {
				out = append(out, Crumb{Href: pairs[i], Label: pairs[i+1]})
			}
			return out
		},
		// navCurrent reports whether the nav item for prefix is the current section.
		"navCurrent": func(path, prefix string) bool {
			return path == prefix || strings.HasPrefix(path, prefix+"/")
		},
		// activity names a seeded activity type in the page language ("activity.<slug>"),
		// falling back to the stored name for types added later without a translation.
		"activity": func(slug, name string) string {
			if key := "activity." + slug; tr.Has(key) {
				return tr.T(key)
			}
			return name
		},
		// coord formats a coordinate for a data attribute or input ("" when unset).
		"coord": func(f *float64) string {
			if f == nil {
				return ""
			}
			return strconv.FormatFloat(*f, 'f', -1, 64)
		},
		"t": tr.T,
		// stars returns five booleans: star i is filled when i < the rounded rating.
		"stars": func(v any) []bool {
			var r float64
			switch x := v.(type) {
			case float64:
				r = x
			case int16:
				r = float64(x)
			case int:
				r = float64(x)
			}
			out := make([]bool, 5)
			for i := range out {
				out[i] = float64(i) < math.Round(r)
			}
			return out
		},
		// decimal1 formats 4.567 as "4,6" or "4.6" (the language's decimal separator).
		"decimal1": func(v float64) string {
			return strings.Replace(fmt.Sprintf("%.1f", v), ".", tr.T("format.decimal_separator"), 1)
		},
		// local converts a timestamp to the app's time zone (see SetLocation).
		"local": func(t time.Time) time.Time { return t.In(z.loc) },
		// rupiah formats an integer amount as "Rp 150.000".
		"rupiah": FormatRupiah,
		// date: "Sabtu, 15 Maret 2030"; month: "Maret 2030"; iso: "2030-03-15".
		"date":  func(d time.Time) string { return FormatDate(tr, d) },
		"month": func(d time.Time) string { return FormatMonth(tr, d) },
		"iso":   func(d time.Time) string { return d.Format(time.DateOnly) },
		"dateTime": func(t time.Time, loc *time.Location) string {
			lt := t.In(loc)
			return FormatDate(tr, lt) + " " + lt.Format("15:04")
		},
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
	return pages, nil
}

// page returns the template set for name in the request language.
func (rd *Renderer) page(r *http.Request, name string) (*template.Template, bool) {
	t, ok := rd.pages[i18n.LangFrom(r.Context())][name]
	return t, ok
}

// view builds the root template value for r.
func (rd *Renderer) view(r *http.Request, data any) View {
	v := View{Lang: i18n.LangFrom(r.Context()), Langs: i18n.Supported, URI: r.URL.RequestURI(), Path: r.URL.Path, Data: data}
	if viewer, ok := httpx.ViewerFrom(r.Context()); ok {
		v.Viewer = &viewer
	}
	return v
}

// SetLocation sets the time zone used by the "local" template func. Call before serving.
func (rd *Renderer) SetLocation(loc *time.Location) {
	if loc != nil {
		rd.zone.loc = loc
	}
}

// SetMap sets the configuration read by the "mapConfig" template func. Call before serving.
func (rd *Renderer) SetMap(c MapConfig) { *rd.mapCfg = c }

// Page renders page with status. data becomes View.Data.
func (rd *Renderer) Page(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	tmpl, ok := rd.page(r, page)
	if !ok {
		rd.ServerError(w, r, fmt.Errorf("render: unknown page %q", page))
		return
	}
	ctx := r.Context()
	v := rd.view(r, data)
	v.CSRF = csrf.Token(ctx, rd.sm)
	v.Flash = rd.sm.PopString(ctx, session.KeyFlash)
	// Render to a buffer so a template error never sends a half-written 200.
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", v); err != nil {
		rd.ServerError(w, r, fmt.Errorf("render %s: %w", page, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", v.Lang)
	w.WriteHeader(status)
	buf.WriteTo(w) //nolint:errcheck
}

// Partial renders a single named template (defined in page's template set) without the
// layout, for HTMX fragment responses.
func (rd *Renderer) Partial(w http.ResponseWriter, r *http.Request, status int, page, name string, data any) {
	tmpl, ok := rd.page(r, page)
	if !ok {
		rd.ServerError(w, r, fmt.Errorf("render: unknown page %q", page))
		return
	}
	v := rd.view(r, data)
	v.CSRF = csrf.Token(r.Context(), rd.sm)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, v); err != nil {
		rd.ServerError(w, r, fmt.Errorf("render %s/%s: %w", page, name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", v.Lang)
	w.WriteHeader(status)
	buf.WriteTo(w) //nolint:errcheck
}

// FormatDate formats a date with localized day and month names.
func FormatDate(tr *i18n.Translator, d time.Time) string {
	return fmt.Sprintf("%s, %d %s %d",
		tr.T(fmt.Sprintf("date.weekday.%d", int(d.Weekday()))), d.Day(), tr.T(fmt.Sprintf("date.month.%d", int(d.Month()))), d.Year())
}

// FormatMonth formats "Maret 2030".
func FormatMonth(tr *i18n.Translator, d time.Time) string {
	return fmt.Sprintf("%s %d", tr.T(fmt.Sprintf("date.month.%d", int(d.Month()))), d.Year())
}

// ErrorData is the payload of the "error" page.
type ErrorData struct {
	TitleKey, MessageKey string
	RequestID            string
}

func (rd *Renderer) errorPage(w http.ResponseWriter, r *http.Request, status int, data ErrorData) {
	tmpl, ok := rd.page(r, "error")
	if !ok {
		http.Error(w, http.StatusText(status), status)
		return
	}
	v := rd.view(r, data)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", v); err != nil {
		rd.logger.Error("render error page", "err", err)
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", v.Lang)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	buf.WriteTo(w) //nolint:errcheck
}

func (rd *Renderer) NotFound(w http.ResponseWriter, r *http.Request) {
	rd.errorPage(w, r, http.StatusNotFound, ErrorData{TitleKey: "error.not_found.title", MessageKey: "error.not_found.message"})
}

func (rd *Renderer) Forbidden(w http.ResponseWriter, r *http.Request) {
	rd.errorPage(w, r, http.StatusForbidden, ErrorData{TitleKey: "error.forbidden.title", MessageKey: "error.forbidden.message"})
}

func (rd *Renderer) TooLarge(w http.ResponseWriter, r *http.Request) {
	rd.errorPage(w, r, http.StatusRequestEntityTooLarge, ErrorData{TitleKey: "error.too_large.title", MessageKey: "error.too_large.message"})
}

func (rd *Renderer) TooManyRequests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "60")
	rd.errorPage(w, r, http.StatusTooManyRequests, ErrorData{TitleKey: "error.rate_limited.title", MessageKey: "error.rate_limited.message"})
}

// ServerError logs err with the request ID and shows a generic page; details never reach the user.
func (rd *Renderer) ServerError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := middleware.GetReqID(r.Context())
	rd.logger.Error("server error", "err", err, "request_id", reqID, "method", r.Method, "path", r.URL.Path)
	rd.errorPage(w, r, http.StatusInternalServerError, ErrorData{
		TitleKey: "error.server.title", MessageKey: "error.server.message", RequestID: reqID,
	})
}

// FormatRupiah formats an integer amount as "Rp 150.000".
func FormatRupiah(amount int64) string {
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

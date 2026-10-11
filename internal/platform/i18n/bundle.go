package i18n

import (
	"context"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

// Default is the language used when nothing else decides (SPEC §15).
const Default = "id"

// Supported lists the interface languages, default first. Adding one means adding
// web/locales/<lang>.json and web/emails/<lang>/, plus the migration CHECK on users.locale.
var Supported = []string{"id", "en"}

func IsSupported(lang string) bool { return slices.Contains(Supported, lang) }

// CookieName stores a visitor's explicit language choice.
const CookieName = "lang"

// Bundle holds a Translator per supported language.
type Bundle struct{ byLang map[string]*Translator }

// LoadBundle loads every supported language.
func LoadBundle(fsys fs.FS) (*Bundle, error) {
	b := &Bundle{byLang: map[string]*Translator{}}
	for _, lang := range Supported {
		tr, err := Load(fsys, lang)
		if err != nil {
			return nil, err
		}
		b.byLang[lang] = tr
	}
	return b, nil
}

// For returns the translator for lang, or the default language's.
func (b *Bundle) For(lang string) *Translator {
	if tr, ok := b.byLang[lang]; ok {
		return tr
	}
	return b.byLang[Default]
}

// From returns the translator for the request language stored in ctx.
func (b *Bundle) From(ctx context.Context) *Translator { return b.For(LangFrom(ctx)) }

type langKey struct{}

func WithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey{}, lang)
}

// LangFrom returns the request language, or Default.
func LangFrom(ctx context.Context) string {
	if l, ok := ctx.Value(langKey{}).(string); ok && IsSupported(l) {
		return l
	}
	return Default
}

// Negotiate picks the supported language the Accept-Language header prefers most, or ""
// when it names none. Ties keep the header's order; q=0 excludes a language.
func Negotiate(header string) string {
	best, bestQ := "", 0.0
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 1 {
				continue
			}
			q = f
		}
		primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		if primary == "in" { // legacy code for Indonesian
			primary = "id"
		}
		if IsSupported(primary) && q > bestQ {
			best, bestQ = primary, q
		}
	}
	return best
}

// Keys returns every key of t (for parity tests).
func (t *Translator) Keys() []string {
	out := make([]string, 0, len(t.messages))
	for k := range t.messages {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Raw returns the untranslated message for key (for parity tests).
func (t *Translator) Raw(key string) string { return t.messages[key] }

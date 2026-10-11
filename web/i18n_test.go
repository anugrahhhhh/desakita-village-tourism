package web_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"desakita/internal/platform/audit"
	"desakita/internal/platform/i18n"
	"desakita/web"
)

// Every i18n key referenced from templates or Go code must exist in id.json.
func TestI18nKeysExist(t *testing.T) {
	tr, err := i18n.Load(web.FS, "id")
	if err != nil {
		t.Fatal(err)
	}

	tmplKey := regexp.MustCompile(`\{\{-?\s*t\s+"([^"]+)"`)
	err = fs.WalkDir(web.FS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(web.FS, path)
		if err != nil {
			return err
		}
		for _, m := range tmplKey.FindAllStringSubmatch(string(b), -1) {
			if !tr.Has(m[1]) {
				t.Errorf("%s: missing key %q", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Keys passed around Go code as string literals (validation errors, flashes, error pages).
	goKey := regexp.MustCompile(`"((?:validation|auth|account|flash|error|booking|village|package|admin|photo|upload|manage|audit|review|report|stats)\.[a-z0-9_.]+)"`)
	err = filepath.WalkDir(filepath.Join("..", "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range goKey.FindAllStringSubmatch(string(b), -1) {
			if slices.Contains(audit.Actions(), m[1]) {
				continue // audit action names ("village.verify"), not UI keys
			}
			if !tr.Has(m[1]) {
				t.Errorf("%s: missing key %q", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Every language has exactly the keys of id.json (plus optional ".one" singular forms) and
// the same format verbs, so no page or email can miss a translation or misplace an argument.
func TestLocalesMatch(t *testing.T) {
	base, err := i18n.Load(web.FS, i18n.Default)
	if err != nil {
		t.Fatal(err)
	}
	verbs := regexp.MustCompile(`%(\[\d+\])?[a-z]`)
	sig := func(s string) string {
		v := verbs.FindAllString(s, -1)
		slices.Sort(v)
		return strings.Join(v, " ")
	}
	for _, lang := range i18n.Supported[1:] {
		tr, err := i18n.Load(web.FS, lang)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range base.Keys() {
			if !tr.Has(k) {
				t.Errorf("%s.json: missing key %q", lang, k)
			} else if sig(tr.Raw(k)) != sig(base.Raw(k)) {
				t.Errorf("%s.json: %q has verbs %q, id.json has %q", lang, k, sig(tr.Raw(k)), sig(base.Raw(k)))
			}
		}
		for _, k := range tr.Keys() {
			baseKey := strings.TrimSuffix(k, ".one")
			if !base.Has(baseKey) {
				t.Errorf("%s.json: unknown key %q", lang, k)
			} else if baseKey != k && sig(tr.Raw(k)) != sig(base.Raw(baseKey)) {
				t.Errorf("%s.json: %q verbs differ from %q", lang, k, baseKey)
			}
		}
	}
}

// Every language has the same set of email templates.
func TestEmailTemplatesPerLanguage(t *testing.T) {
	names := func(lang string) []string {
		files, _ := fs.Glob(web.FS, "emails/"+lang+"/*.txt")
		out := make([]string, len(files))
		for i, f := range files {
			out[i] = filepath.Base(f)
		}
		return out
	}
	want := names(i18n.Default)
	for _, lang := range i18n.Supported {
		if got := names(lang); !slices.Equal(got, want) {
			t.Errorf("emails/%s = %v, want %v", lang, got, want)
		}
	}
}

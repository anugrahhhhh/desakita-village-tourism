package web_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
	goKey := regexp.MustCompile(`"((?:validation|auth|account|flash|error|booking|village|package|admin)\.[a-z0-9_.]+)"`)
	err = filepath.WalkDir(filepath.Join("..", "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range goKey.FindAllStringSubmatch(string(b), -1) {
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

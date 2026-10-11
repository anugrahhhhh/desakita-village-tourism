// Package i18n looks up UI strings by key (SPEC §15). Each supported language has
// web/locales/<lang>.json; adding one does not touch templates.
package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
)

type Translator struct {
	lang     string
	messages map[string]string
}

// Load reads a flat {"key": "text"} JSON file for one language.
func Load(fsys fs.FS, lang string) (*Translator, error) {
	b, err := fs.ReadFile(fsys, "locales/"+lang+".json")
	if err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("i18n: parse %s: %w", lang, err)
	}
	return &Translator{lang: lang, messages: m}, nil
}

func (t *Translator) Lang() string { return t.lang }

// Has reports whether key exists.
func (t *Translator) Has(key string) bool {
	_, ok := t.messages[key]
	return ok
}

// T returns the text for key, formatted with args (fmt verbs). Missing keys render as
// ⟦key⟧ so they are visible in the page and caught by tests.
//
// Plurals: when the first argument is the integer 1 and "<key>.one" exists, that message is
// used instead ("%d review" vs "%d reviews"). Languages without plural forms (Indonesian)
// simply have no ".one" keys.
func (t *Translator) T(key string, args ...any) string {
	msg, ok := t.messages[key]
	if !ok {
		return "⟦" + key + "⟧"
	}
	if len(args) > 0 && isOne(args[0]) {
		if one, ok := t.messages[key+".one"]; ok {
			msg = one
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

func isOne(v any) bool {
	switch n := v.(type) {
	case int:
		return n == 1
	case int16:
		return n == 1
	case int32:
		return n == 1
	case int64:
		return n == 1
	}
	return false
}

// Package i18n looks up UI strings by key. MVP ships Indonesian only; adding a language
// means adding web/locales/<lang>.json, not touching templates.
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
func (t *Translator) T(key string, args ...any) string {
	msg, ok := t.messages[key]
	if !ok {
		return "⟦" + key + "⟧"
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

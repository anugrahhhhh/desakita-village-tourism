// Package form carries field-level validation errors from services to templates.
// Error values are i18n keys, rendered by the template with {{t ...}}.
package form

import "strings"

// Errors maps a field name to an i18n message key.
type Errors map[string]string

// Add records key for field unless the field already has an error.
func (e Errors) Add(field, key string) {
	if _, ok := e[field]; !ok {
		e[field] = key
	}
}

func (e Errors) Any() bool { return len(e) > 0 }

// ValidationError is returned by services when input is invalid.
type ValidationError struct{ Fields Errors }

func (v *ValidationError) Error() string {
	keys := make([]string, 0, len(v.Fields))
	for f := range v.Fields {
		keys = append(keys, f)
	}
	return "validation failed: " + strings.Join(keys, ", ")
}

// Err returns a *ValidationError if e has entries, otherwise nil.
func (e Errors) Err() error {
	if !e.Any() {
		return nil
	}
	return &ValidationError{Fields: e}
}

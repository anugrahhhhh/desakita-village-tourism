// Package phone validates Indonesian phone numbers as typed by users.
package phone

import "regexp"

var pattern = regexp.MustCompile(`^\+?[0-9][0-9 \-]{6,18}[0-9]$`)

// Valid reports whether s looks like a phone number (digits, spaces, dashes, optional +).
func Valid(s string) bool { return pattern.MatchString(s) }

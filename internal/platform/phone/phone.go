// Package phone validates Indonesian phone numbers and builds WhatsApp links.
package phone

import (
	"net/url"
	"regexp"
	"strings"
)

var pattern = regexp.MustCompile(`^\+?[0-9][0-9 \-]{6,18}[0-9]$`)

// Valid reports whether s looks like a phone number (digits, spaces, dashes, optional +).
func Valid(s string) bool { return pattern.MatchString(s) }

// NormalizeWA converts a number as typed ("0812-3456-7890", "+62 812 …", "812…") to the
// international digits wa.me expects ("6281234567890"). ok is false if the result isn't
// 10–15 digits or doesn't look Indonesian.
func NormalizeWA(s string) (string, bool) {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	d := b.String()
	switch {
	case strings.HasPrefix(d, "62"):
	case strings.HasPrefix(d, "0"):
		d = "62" + d[1:]
	case strings.HasPrefix(d, "8"):
		// Mobile number typed without the leading 0.
		d = "62" + d
	default:
		return "", false
	}
	if len(d) < 10 || len(d) > 15 {
		return "", false
	}
	return d, true
}

// WALink returns a wa.me link that opens a chat with number pre-filled with message, or ""
// if the number can't be normalized. Spaces are encoded as %20 (not "+", which WhatsApp
// may show literally).
func WALink(number, message string) string {
	n, ok := NormalizeWA(number)
	if !ok {
		return ""
	}
	return "https://wa.me/" + n + "?text=" + strings.ReplaceAll(url.QueryEscape(message), "+", "%20")
}

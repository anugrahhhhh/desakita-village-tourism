// Package web embeds templates, locale files and static assets into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates locales emails
var FS embed.FS

//go:embed static
var staticFS embed.FS

// Static is the static asset tree rooted at web/static.
func Static() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

// Package migrations embeds the golang-migrate SQL files so the binary can migrate itself.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

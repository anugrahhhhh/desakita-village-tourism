// Package seeds embeds development seed data.
package seeds

import _ "embed"

//go:embed dev.sql
var DevSQL string

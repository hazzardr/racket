// Package migrations embeds the goose SQL migrations so the server binary
// can apply them without the source tree on disk.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

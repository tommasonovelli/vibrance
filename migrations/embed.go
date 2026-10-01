// Package migrations holds the versioned SQL schema (DESIGN.md §5.2, §10.1),
// embedded so that the binary carries the schema it was built for.
// internal/store applies it, forward only.
package migrations

import "embed"

// FS holds the goose migrations, one file per version.
//
//go:embed *.sql
var FS embed.FS

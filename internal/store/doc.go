// Package store is the SQLite database (DESIGN.md §5): opening it, applying
// the embedded migrations of package migrations, its transactions, and the
// queries that sqlc generates from sql/. It knows no absolute paths and
// nothing of HTTP.
package store

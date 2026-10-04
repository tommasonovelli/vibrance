// Package search maintains the FTS5 tables and builds their queries
// (DESIGN.md §10). It holds the only SQL outside sql/*.sql: sqlc is kept
// away from the full-text tables (T7), so the statements on them are
// written here by hand. None of them is built from input.
package search

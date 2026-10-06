package search

import (
	"context"
	"fmt"

	"vibrance/internal/store"
)

// rebuild drops the three full-text tables, creates them again and fills
// them from the tables they copy.
//
// The tables are dropped, not emptied. A DROP does not read the index it
// removes, so it also works on one that is damaged; a DELETE of every row
// reads the index to take each row out of it, and FTS5 refuses its
// 'delete-all' command on a table that keeps its content, as these do. IF
// EXISTS, so that a table that is gone is made again too.
//
// The CREATE statements are those of migrations/00002_search.sql, byte for
// byte: SQLite keeps the text of a CREATE in sqlite_master, and
// TestRebuildKeepsTheSchema compares that text with the one of a database
// that was only migrated, so the two copies cannot drift apart.
var rebuild = []string{
	`DROP TABLE IF EXISTS search_artists`,
	`DROP TABLE IF EXISTS search_albums`,
	`DROP TABLE IF EXISTS search_tracks`,
	`CREATE VIRTUAL TABLE search_artists USING fts5(name,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3')`,
	`CREATE VIRTUAL TABLE search_albums USING fts5(title, artist,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3')`,
	`CREATE VIRTUAL TABLE search_tracks USING fts5(title, artist, album,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3')`,
	insertAllArtists,
	insertAllAlbums,
	insertAllTracks,
}

// Rebuild makes the three full-text tables again from artists, albums and
// tracks: afterwards they hold what the scanner would have written, whatever
// they held before, rows that are stale, extra or missing and a damaged
// index included. Nothing else changes.
//
// db is one write transaction: the search of every other connection finds
// the old tables until it commits, and the new ones after.
func Rebuild(ctx context.Context, db store.DBTX) error {
	for _, statement := range rebuild {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("search: rebuilding the full-text tables: %w", err)
		}
	}
	return nil
}

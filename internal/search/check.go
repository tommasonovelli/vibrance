package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"vibrance/internal/store"
)

// What can be wrong with a row of a full-text table (§10.1).
const (
	// FaultIndex: the full-text index of the table is damaged. Detail has
	// what SQLite says of it.
	FaultIndex = "index"
	// FaultExtra: a full-text row describes nothing that is available. A
	// search that matches it fails.
	FaultExtra = "extra"
	// FaultMissing: something available has no full-text row. No search
	// finds it.
	FaultMissing = "missing"
	// FaultStale: the full-text row holds other names than the tables it
	// copies. A search finds it by the old words.
	FaultStale = "stale"
)

// Fault is one disagreement between a full-text table and the table it
// copies.
type Fault struct {
	// Table is the full-text table: search_artists, search_albums or
	// search_tracks.
	Table string
	// Kind is one of the Fault constants.
	Kind string
	// Rowid is the rowid of the full-text row, which is the seq of what it
	// describes; 0 for FaultIndex.
	Rowid int64
	// ID is the id of the artist, album or track with that seq; "" when
	// there is none, and for FaultIndex.
	ID string
	// Detail is what SQLite reports, for FaultIndex.
	Detail string
}

// The checks of the three tables. Each has two statements.
//
// The first is the integrity-check of FTS5 on one table, through PRAGMA
// integrity_check: the command of FTS5 itself, an INSERT of the word
// 'integrity-check', is refused on a connection that cannot write, and the
// doctor only reads (§11.4). It compares the index with the rows of the
// full-text table.
//
// The second compares those rows with the tables they copy, as index.go
// writes them: a row for what is available and for nothing else, with the
// same names. It reads no value from its caller.
var checks = []struct {
	table, integrity, rows string
}{
	{"search_artists", `PRAGMA integrity_check(search_artists)`, `
SELECT 'extra' AS kind, s.rowid AS seq, coalesce(a.id, '') AS id
FROM search_artists s LEFT JOIN artists a ON a.seq = s.rowid
WHERE NOT EXISTS (SELECT 1 FROM albums WHERE albums.artist_id = a.id AND albums.available = 1)
UNION ALL
SELECT 'missing', a.seq, a.id
FROM artists a
WHERE EXISTS (SELECT 1 FROM albums WHERE albums.artist_id = a.id AND albums.available = 1)
  AND NOT EXISTS (SELECT 1 FROM search_artists s WHERE s.rowid = a.seq)
UNION ALL
SELECT 'stale', s.rowid, a.id
FROM search_artists s JOIN artists a ON a.seq = s.rowid
WHERE EXISTS (SELECT 1 FROM albums WHERE albums.artist_id = a.id AND albums.available = 1)
  AND s.name IS NOT a.name
ORDER BY 2, 1`},
	{"search_albums", `PRAGMA integrity_check(search_albums)`, `
SELECT 'extra' AS kind, s.rowid AS seq, coalesce(a.id, '') AS id
FROM search_albums s LEFT JOIN albums a ON a.seq = s.rowid
WHERE a.available IS NOT 1
UNION ALL
SELECT 'missing', a.seq, a.id
FROM albums a
WHERE a.available = 1 AND NOT EXISTS (SELECT 1 FROM search_albums s WHERE s.rowid = a.seq)
UNION ALL
SELECT 'stale', s.rowid, a.id
FROM search_albums s JOIN albums a ON a.seq = s.rowid JOIN artists ON artists.id = a.artist_id
WHERE a.available = 1 AND (s.title IS NOT a.title OR s.artist IS NOT artists.name)
ORDER BY 2, 1`},
	{"search_tracks", `PRAGMA integrity_check(search_tracks)`, `
SELECT 'extra' AS kind, s.rowid AS seq, coalesce(t.id, '') AS id
FROM search_tracks s LEFT JOIN tracks t ON t.seq = s.rowid
WHERE t.available IS NOT 1
UNION ALL
SELECT 'missing', t.seq, t.id
FROM tracks t
WHERE t.available = 1 AND NOT EXISTS (SELECT 1 FROM search_tracks s WHERE s.rowid = t.seq)
UNION ALL
SELECT 'stale', s.rowid, t.id
FROM search_tracks s JOIN tracks t ON t.seq = s.rowid JOIN albums ON albums.id = t.album_id
WHERE t.available = 1 AND (s.title IS NOT t.title OR s.artist IS NOT t.artist OR s.album IS NOT albums.title)
ORDER BY 2, 1`},
}

// Check compares the three full-text tables with the tables they copy and
// returns every disagreement, table by table: the damage of its index or,
// when the index is sound, its rows by rowid. It returns none when the
// search can be trusted, and changes nothing.
//
// db is a read transaction: the full-text tables and what they describe
// change together, so only one state of the database can be judged. Its
// connection must be new to the full-text tables: the integrity-check of
// FTS5 reports a checksum mismatch that is not there on a connection that
// read a full-text table before another connection changed it (SQLite
// 3.53.4; a search on that connection is right). The doctor opens one for
// the inspection (NOTES.md N-140).
func Check(ctx context.Context, db store.DBTX) ([]Fault, error) {
	var faults []Fault
	for _, c := range checks {
		lines, err := store.IntegrityLines(ctx, db, c.integrity)
		if err != nil {
			return nil, fmt.Errorf("search: checking %s: %w", c.table, err)
		}
		for _, line := range lines {
			faults = append(faults, Fault{Table: c.table, Kind: FaultIndex, Detail: line})
		}
		if len(lines) > 0 {
			// The rows of a table whose index is damaged cannot be read
			// with confidence.
			continue
		}
		rows, err := checkRows(ctx, db, c.table, c.rows)
		if err != nil {
			return nil, fmt.Errorf("search: checking %s: %w", c.table, err)
		}
		faults = append(faults, rows...)
	}
	return faults, nil
}

func checkRows(ctx context.Context, db store.DBTX, table, statement string) (faults []Fault, err error) {
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()
	for rows.Next() {
		f := Fault{Table: table}
		var id sql.NullString
		if err := rows.Scan(&f.Kind, &f.Rowid, &id); err != nil {
			return nil, err
		}
		f.ID = id.String
		faults = append(faults, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return faults, nil
}

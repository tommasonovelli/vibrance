package search

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"vibrance/internal/store"
)

// rebuildNow runs Rebuild in a write transaction of its own, as the command
// does.
func rebuildNow(t *testing.T, s *store.Store) {
	t.Helper()
	write(t, s, func(q *store.Queries) error {
		return Rebuild(t.Context(), q.Conn())
	})
}

// allRows are the rows of the three full-text tables.
func allRows(t *testing.T, s *store.Store) []string {
	t.Helper()
	return slices.Concat(rows(t, s, artistRows), rows(t, s, albumRows), rows(t, s, trackRows))
}

// wantSound fails unless the full-text tables have no fault, hold exactly
// what the scanner writes for the index as it is now, and answer a search.
func wantSound(t *testing.T, s *store.Store) {
	t.Helper()
	if got := check(t, s); len(got) != 0 {
		t.Fatalf("the faults after the rebuild: %+v", got)
	}
	rebuilt := allRows(t, s)
	// The scanner's own writes, of every album and artist: they change
	// nothing of what Rebuild wrote.
	syncAll(t, s)
	if synced := allRows(t, s); !slices.Equal(rebuilt, synced) {
		t.Fatalf("the rows of the rebuild are not those of the scanner:\n got %q\nwant %q", rebuilt, synced)
	}
	if got := check(t, s); len(got) != 0 {
		t.Fatalf("the faults after the scanner wrote on the rebuilt tables: %+v", got)
	}
	for _, kinds := range []Kinds{{Artists: true}, {Albums: true}, {Tracks: true}} {
		if _, err := findIn(t.Context(), s, "a", kinds, 50); err != nil {
			t.Fatalf("searching %+v after the rebuild: %v", kinds, err)
		}
	}
}

// The erratum R3 to §11.4: Rebuild repairs every disagreement the doctor
// finds between a full-text table and the table it copies, in each of the
// three tables: rows that are stale, extra and missing. Afterwards the tables
// hold what the scanner writes, and the searches that failed answer.
func TestRebuildRepairsEveryFault(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	syncAll(t, s)
	ctx := t.Context()

	// Extra: album B and its track are unavailable, without the full-text
	// rows following, so the row of its artist is extra too. Extra,
	// describing nothing at all, in each table.
	exec(t, s, `UPDATE albums SET available = 0 WHERE id = ?`, albumB)
	exec(t, s, `UPDATE tracks SET available = 0 WHERE album_id = ?`, albumB)
	exec(t, s, `INSERT INTO search_artists (rowid, name) VALUES (997, 'ghost')`)
	exec(t, s, `INSERT INTO search_albums (rowid, title, artist) VALUES (998, 'ghost', 'nobody')`)
	exec(t, s, `INSERT INTO search_tracks (rowid, title, artist, album) VALUES (999, 'ghost', 'nobody', 'none')`)
	// Missing: the row of a track.
	exec(t, s, `DELETE FROM search_tracks WHERE rowid = ?`, seqOf(t, s, "tracks", track2))
	// Stale: the artist and a track take other names, and the rows of the
	// artist, of its album and of its tracks keep the old ones.
	write(t, s, func(q *store.Queries) error {
		return errors.Join(
			q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artistA, Name: "Miles Dewey Davis", SortKey: []byte{1}}),
			q.UpsertTrack(ctx, track(track1, albumA, "So What (Take 2)", "Miles Davis", 1)))
	})

	kinds := map[string][]string{}
	for _, f := range check(t, s) {
		kinds[f.Table] = append(kinds[f.Table], f.Kind)
	}
	for table, want := range map[string][]string{
		"search_artists": {FaultExtra, FaultStale},
		"search_albums":  {FaultExtra, FaultStale},
		"search_tracks":  {FaultExtra, FaultMissing, FaultStale},
	} {
		for _, kind := range want {
			if !slices.Contains(kinds[table], kind) {
				t.Fatalf("the test did not break %s with a row that is %s: %v", table, kind, kinds)
			}
		}
	}
	// A search that meets a row of what is not available fails.
	if _, err := findIn(ctx, s, "homogenic", everything, 50); !errors.Is(err, errNotAvailable) {
		t.Fatalf("the search on the broken tables: %v", err)
	}

	rebuildNow(t, s)
	want(t, s, artistRows, artistA+"|Miles Dewey Davis")
	want(t, s, albumRows, albumA+"|Kind of Blue|Miles Dewey Davis")
	want(t, s, trackRows,
		track1+"|So What (Take 2)|Miles Davis|Kind of Blue",
		track2+"|Freddie Freeloader|Miles Davis feat. Wynton Kelly|Kind of Blue")
	wantSound(t, s)
	if got := findAll(t, s, "homogenic"); got.artists != nil || got.albums != nil || got.tracks != nil {
		t.Fatalf("what is not available is found after the rebuild: %+v", got)
	}
	got := findAll(t, s, "dewey")
	wantFound(t, "dewey", "artists", got.artists, list("Miles Dewey Davis"))
	wantFound(t, "dewey", "albums", got.albums, list("Kind of Blue"))
	wantFound(t, "freeloader", "tracks", findAll(t, s, "freeloader").tracks, list("Freddie Freeloader"))
}

// Rebuild does not read the index it replaces: it repairs one whose content
// no longer matches its inverted index, one whose pages are overwritten or
// gone, and a table that is not there at all.
func TestRebuildRepairsADamagedIndex(t *testing.T) {
	for name, damage := range map[string][]string{
		"content that is not the indexed one": {
			`UPDATE search_artists_content SET c0 = 'nothing like it'`,
			`UPDATE search_albums_content SET c0 = 'nothing like it'`,
			`UPDATE search_tracks_content SET c0 = 'nothing like it'`,
		},
		"overwritten pages": {
			`UPDATE search_artists_data SET block = zeroblob(8)`,
			`UPDATE search_albums_data SET block = randomblob(64)`,
			`UPDATE search_tracks_data SET block = x'ff'`,
		},
		"missing pages and sizes": {
			`DELETE FROM search_artists_data`,
			`DELETE FROM search_albums_data`,
			`DELETE FROM search_albums_idx`,
			`DELETE FROM search_tracks_docsize`,
		},
		"missing tables": {
			`DROP TABLE search_artists`,
			`DROP TABLE search_albums`,
			`DROP TABLE search_tracks`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			seed(t, s)
			syncAll(t, s)
			sound := allRows(t, s)
			for _, statement := range damage {
				exec(t, s, statement)
			}
			// Each of the three tables is damaged: the doctor says so, or
			// cannot even read them.
			if !strings.HasPrefix(name, "missing tables") {
				tables := map[string]bool{}
				for _, f := range check(t, s) {
					tables[f.Table] = true
				}
				if len(tables) != 3 {
					t.Fatalf("the damage is found in %v, want the three tables", tables)
				}
			}
			rebuildNow(t, s)
			if got := allRows(t, s); !slices.Equal(got, sound) {
				t.Fatalf("the rows after the rebuild:\n got %q\nwant %q", got, sound)
			}
			wantSound(t, s)
			wantFound(t, "miles", "artists", findAll(t, s, "miles").artists, list("Miles Davis"))
			wantFound(t, "jo", "tracks", findAll(t, s, "jo").tracks, list("Jóga"))
		})
	}
}

// searchSchema is what sqlite_master says of the full-text tables and of
// the tables FTS5 keeps for them: the text of each CREATE, as it was given.
const searchSchema = `SELECT type || '|' || name || '|' || tbl_name || '|' || coalesce(sql, '')
FROM sqlite_master WHERE name LIKE 'search!_%' ESCAPE '!' ORDER BY name`

// The CREATE statements of Rebuild are a second copy of those of the
// migration. After a rebuild the schema of the three tables is, byte for
// byte, the one of a database that was only migrated: a change of one copy
// without the other fails here.
func TestRebuildKeepsTheSchema(t *testing.T) {
	migrated := rows(t, newStore(t), searchSchema)
	// Three virtual tables, each with the five tables FTS5 keeps for it.
	if len(migrated) != 18 {
		t.Fatalf("a migrated database has %d full-text tables: %q", len(migrated), migrated)
	}
	for _, table := range []string{"search_artists", "search_albums", "search_tracks"} {
		if !slices.ContainsFunc(migrated, func(row string) bool {
			return strings.HasPrefix(row, "table|"+table+"|"+table+"|CREATE VIRTUAL TABLE "+table+" USING fts5(")
		}) {
			t.Fatalf("a migrated database has no %s: %q", table, migrated)
		}
	}
	s := newStore(t)
	seed(t, s)
	syncAll(t, s)
	for i := range 2 {
		rebuildNow(t, s)
		if got := rows(t, s, searchSchema); !slices.Equal(got, migrated) {
			t.Fatalf("the schema after rebuild %d:\n got %q\nwant %q", i+1, got, migrated)
		}
	}
	// Nothing else of the schema is touched.
	const rest = `SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'search!_%' ESCAPE '!'`
	if got, want := rows(t, s, rest), rows(t, newStore(t), rest); !slices.Equal(got, want) {
		t.Fatalf("the rest of the schema has %q objects, want %q", got, want)
	}
}

// Rebuilding a sound index, an empty one, and rebuilding twice change
// nothing.
func TestRebuildIsIdempotent(t *testing.T) {
	s := newStore(t)
	rebuildNow(t, s)
	if got := allRows(t, s); len(got) != 0 {
		t.Fatalf("an empty index rebuilt has the rows %q", got)
	}
	if got := check(t, s); len(got) != 0 {
		t.Fatalf("an empty index rebuilt: %+v", got)
	}
	seed(t, s)
	syncAll(t, s)
	// An album that is not available has no row, before and after.
	exec(t, s, `UPDATE albums SET available = 0 WHERE id = ?`, albumB)
	exec(t, s, `UPDATE tracks SET available = 0 WHERE album_id = ?`, albumB)
	syncAll(t, s)
	sound := allRows(t, s)
	if len(sound) != 4 {
		t.Fatalf("the rows of the index: %q", sound)
	}
	for i := range 3 {
		rebuildNow(t, s)
		if got := allRows(t, s); !slices.Equal(got, sound) {
			t.Fatalf("the rows after rebuild %d:\n got %q\nwant %q", i+1, got, sound)
		}
	}
	wantSound(t, s)
}

// Rebuild is part of the transaction it is given: one that does not commit
// leaves the tables, the damage included, as they were; and a context that
// ended rebuilds nothing.
func TestRebuildIsPartOfTheTransaction(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	syncAll(t, s)
	// Album B is unavailable with its rows left, and the row of album A is
	// gone.
	exec(t, s, `UPDATE albums SET available = 0 WHERE id = ?`, albumB)
	exec(t, s, `DELETE FROM search_albums WHERE rowid = ?`, seqOf(t, s, "albums", albumA))
	broken, faults := allRows(t, s), check(t, s)
	if len(faults) != 3 {
		t.Fatalf("the faults of the test: %+v", faults)
	}
	wantAsBefore := func(where string) {
		t.Helper()
		if got := allRows(t, s); !slices.Equal(got, broken) {
			t.Fatalf("%s: the rows changed:\n got %q\nwant %q", where, got, broken)
		}
		if got := check(t, s); !slices.Equal(got, faults) {
			t.Fatalf("%s: the faults %+v, want %+v", where, got, faults)
		}
	}

	failed := errors.New("the transaction fails after the rebuild")
	err := s.WithWriteTx(t.Context(), func(q *store.Queries) error {
		if err := Rebuild(t.Context(), q.Conn()); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
	wantAsBefore("a transaction rolled back")

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	err = s.WithWriteTx(t.Context(), func(q *store.Queries) error {
		return Rebuild(cancelled, q.Conn())
	})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "search: rebuilding the full-text tables") {
		t.Fatalf("a rebuild with a context that ended: %v", err)
	}
	wantAsBefore("a context that ended")

	rebuildNow(t, s)
	wantSound(t, s)
}

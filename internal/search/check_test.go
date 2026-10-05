package search

import (
	"errors"
	"slices"
	"testing"

	"vibrance/internal/store"
)

// syncAll indexes every artist and album of seed, as the scanner would.
func syncAll(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := t.Context()
	write(t, s, func(q *store.Queries) error {
		db := q.Conn()
		return errors.Join(SyncArtist(ctx, db, artistA), SyncArtist(ctx, db, artistB),
			SyncAlbum(ctx, db, albumA), SyncAlbum(ctx, db, albumB))
	})
}

// storePath is the file of a store of newStore.
func storePath(s *store.Store) string {
	return paths[s]
}

// check runs Check as the doctor does: in a read transaction of a reader
// opened for it, whose connections cannot write and are new to the
// full-text tables (see Check).
func check(t *testing.T, s *store.Store) []Fault {
	t.Helper()
	r, err := store.OpenReader(t.Context(), storePath(s), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}()
	var faults []Fault
	err = r.Read(t.Context(), func(q *store.Queries) error {
		var err error
		faults, err = Check(t.Context(), q.Conn())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return faults
}

// exec runs SQL of the test, which breaks the agreement between the
// full-text tables and the index as the server never does.
func exec(t *testing.T, s *store.Store, query string, args ...any) {
	t.Helper()
	write(t, s, func(q *store.Queries) error {
		_, err := q.Conn().ExecContext(t.Context(), query, args...)
		return err
	})
}

// seqOf is the seq of a row, which is the rowid of its full-text row.
func seqOf(t *testing.T, s *store.Store, table, id string) int64 {
	t.Helper()
	var seq int64
	err := s.Read(t.Context(), func(q *store.Queries) error {
		return q.Conn().QueryRowContext(t.Context(), "SELECT seq FROM "+table+" WHERE id = ?", id).Scan(&seq)
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// DESIGN.md §11.4: the full-text tables of an index that the scanner
// keeps have no fault, and so neither has an empty index.
func TestCheckSoundIndex(t *testing.T) {
	s := newStore(t)
	if got := check(t, s); len(got) != 0 {
		t.Fatalf("an empty index: %+v", got)
	}
	seed(t, s)
	syncAll(t, s)
	if got := check(t, s); len(got) != 0 {
		t.Fatalf("a sound index: %+v", got)
	}
	// Unavailable and synced again, as the scanner does: still sound.
	exec(t, s, `UPDATE albums SET available = 0 WHERE id = ?`, albumB)
	exec(t, s, `UPDATE tracks SET available = 0 WHERE album_id = ?`, albumB)
	syncAll(t, s)
	if got := check(t, s); len(got) != 0 {
		t.Fatalf("an index with an unavailable album: %+v", got)
	}
}

// Every way a full-text row can disagree with what it copies is found, with
// the row and the id it concerns.
func TestCheckFindsEveryFault(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	syncAll(t, s)
	ctx := t.Context()

	// extra: album B is unavailable, without the full-text rows following:
	// its artist has no other album, so its row is extra too. Its track
	// stays available, with its row.
	exec(t, s, `UPDATE albums SET available = 0 WHERE id = ?`, albumB)
	// extra, describing nothing at all.
	exec(t, s, `INSERT INTO search_tracks (rowid, title, artist, album) VALUES (999, 'ghost', 'nobody', 'none')`)
	// missing: the row of album A is gone.
	exec(t, s, `DELETE FROM search_albums WHERE rowid = ?`, seqOf(t, s, "albums", albumA))
	// stale: a track takes another title, and its row keeps the old one.
	write(t, s, func(q *store.Queries) error {
		return q.UpsertTrack(ctx, track(track1, albumA, "So What (Take 2)", "Miles Davis", 1))
	})

	want := []Fault{
		{Table: "search_artists", Kind: FaultExtra, Rowid: seqOf(t, s, "artists", artistB), ID: artistB},
		{Table: "search_albums", Kind: FaultMissing, Rowid: seqOf(t, s, "albums", albumA), ID: albumA},
		{Table: "search_albums", Kind: FaultExtra, Rowid: seqOf(t, s, "albums", albumB), ID: albumB},
		{Table: "search_tracks", Kind: FaultStale, Rowid: seqOf(t, s, "tracks", track1), ID: track1},
		{Table: "search_tracks", Kind: FaultExtra, Rowid: 999},
	}
	if got := check(t, s); !slices.Equal(got, want) {
		t.Fatalf("faults:\n got %+v\nwant %+v", got, want)
	}

	// The missing row of a track, and an artist that is renamed.
	exec(t, s, `DELETE FROM search_tracks WHERE rowid = ?`, seqOf(t, s, "tracks", track2))
	write(t, s, func(q *store.Queries) error {
		return q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artistA, Name: "Miles Dewey Davis", SortKey: []byte{1}})
	})
	got := check(t, s)
	for _, f := range []Fault{
		{Table: "search_artists", Kind: FaultStale, Rowid: seqOf(t, s, "artists", artistA), ID: artistA},
		{Table: "search_tracks", Kind: FaultMissing, Rowid: seqOf(t, s, "tracks", track2), ID: track2},
		// The albums of the artist keep the old name of the artist too.
		{Table: "search_tracks", Kind: FaultStale, Rowid: seqOf(t, s, "tracks", track1), ID: track1},
	} {
		found := false
		for _, g := range got {
			found = found || g == f
		}
		if !found {
			t.Errorf("%+v is not found in %+v", f, got)
		}
	}
}

// A damaged full-text index is found by the integrity-check of FTS5, which
// runs on a connection that cannot write; its rows are not judged then.
func TestCheckFindsADamagedIndex(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	syncAll(t, s)
	// The content of the table no longer matches its inverted index.
	exec(t, s, `UPDATE search_tracks_content SET c0 = 'nothing like it' WHERE id = ?`, seqOf(t, s, "tracks", track1))
	got := check(t, s)
	if len(got) == 0 {
		t.Fatal("no fault for a damaged index")
	}
	for _, f := range got {
		if f.Table != "search_tracks" || f.Kind != FaultIndex || f.Detail == "" {
			t.Fatalf("fault %+v, want only the damage of search_tracks", f)
		}
	}
}

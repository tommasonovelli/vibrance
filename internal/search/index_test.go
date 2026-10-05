package search

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"vibrance/internal/store"
)

// The tests use a real SQLite database in t.TempDir(), which the gate puts
// on an ext4 volume (DESIGN.md §12.1). The rows the full-text tables copy
// are written with the queries of sql/; the SQL written here inspects the
// full-text tables, or makes a row unavailable as only the scanner does.

const (
	artistA = "0192a5f0-0000-5000-8000-0000000000a1"
	artistB = "0192a5f0-0000-5000-8000-0000000000a2"
	albumA  = "0192a5f0-0000-7000-8000-0000000000b1"
	albumB  = "0192a5f0-0000-7000-8000-0000000000b2"
	track1  = "0192a5f0-0000-7000-8000-0000000000c1"
	track2  = "0192a5f0-0000-7000-8000-0000000000c2"
	track3  = "0192a5f0-0000-7000-8000-0000000000c3"
)

func newStore(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "vibrance.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("closing the store: %v", err)
		}
	})
	return s
}

func write(t *testing.T, s *store.Store, fn func(q *store.Queries) error) {
	t.Helper()
	if err := s.WithWriteTx(t.Context(), fn); err != nil {
		t.Fatal(err)
	}
}

func album(id, artistID, title string) store.UpsertAlbumParams {
	return store.UpsertAlbumParams{ID: id, ArtistID: artistID, ArtistKey: []byte{1}, Title: title, TitleKey: []byte{2},
		YearKey: 10000, RelPath: "x/" + title, AlbumRevision: 1, RenderVersion: "r", ReceiptHash: "h", FirstSeenAt: 1, UpdatedAt: 1}
}

func track(id, albumID, title, artist string, occurrence int64) store.UpsertTrackParams {
	return store.UpsertTrackParams{ID: id, AlbumID: albumID, Fingerprint: "f", FpVersion: "v", Occurrence: occurrence,
		Disc: 1, No: occurrence, Title: title, Artist: artist, RelPath: id, FileSize: 1, FileMtimeNs: 1, FileSha256: "s",
		Codec: "flac", SampleRate: 44100, Channels: 2, UpdatedAt: 1}
}

// seed writes two artists; album A of the first with two tracks, album B of
// the second with one. Nothing is in the full-text tables yet.
func seed(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := t.Context()
	write(t, s, func(q *store.Queries) error {
		return errors.Join(
			q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artistA, Name: "Miles Davis", SortKey: []byte{1}}),
			q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artistB, Name: "Björk", SortKey: []byte{2}}),
			q.UpsertAlbum(ctx, album(albumA, artistA, "Kind of Blue")),
			q.UpsertAlbum(ctx, album(albumB, artistB, "Homogenic")),
			q.UpsertTrack(ctx, track(track1, albumA, "So What", "Miles Davis", 1)),
			q.UpsertTrack(ctx, track(track2, albumA, "Freddie Freeloader", "Miles Davis feat. Wynton Kelly", 2)),
			q.UpsertTrack(ctx, track(track3, albumB, "Jóga", "Björk", 1)),
		)
	})
}

// rows returns the rows of a full-text table as text, one per row, with the
// id of the row each describes in place of its rowid: the join is the
// claim of T6, rowid = seq.
func rows(t *testing.T, s *store.Store, query string) []string {
	t.Helper()
	var out []string
	err := s.Read(t.Context(), func(q *store.Queries) error {
		r, err := q.Conn().QueryContext(t.Context(), query)
		if err != nil {
			return err
		}
		columns, err := r.Columns()
		if err != nil {
			return errors.Join(err, r.Close())
		}
		for r.Next() {
			values := make([]string, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := r.Scan(targets...); err != nil {
				return errors.Join(err, r.Close())
			}
			out = append(out, strings.Join(values, "|"))
		}
		return errors.Join(r.Err(), r.Close())
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const (
	artistRows = `SELECT artists.id, search_artists.name FROM search_artists LEFT JOIN artists ON artists.seq = search_artists.rowid ORDER BY 1`
	albumRows  = `SELECT albums.id, search_albums.title, search_albums.artist FROM search_albums LEFT JOIN albums ON albums.seq = search_albums.rowid ORDER BY 1`
	trackRows  = `SELECT tracks.id, search_tracks.title, search_tracks.artist, search_tracks.album FROM search_tracks LEFT JOIN tracks ON tracks.seq = search_tracks.rowid ORDER BY 1`
)

func want(t *testing.T, s *store.Store, query string, want ...string) {
	t.Helper()
	if got := rows(t, s, query); !slices.Equal(got, want) {
		t.Fatalf("the full-text rows:\n got %q\nwant %q", got, want)
	}
}

func sync(t *testing.T, s *store.Store, albums, artists []string) {
	t.Helper()
	ctx := t.Context()
	write(t, s, func(q *store.Queries) error {
		for _, id := range albums {
			if err := SyncAlbum(ctx, q.Conn(), id); err != nil {
				return err
			}
		}
		for _, id := range artists {
			if err := SyncArtist(ctx, q.Conn(), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// SyncAlbum writes the row of an album and those of its tracks, each with
// the seq of what it describes as its rowid; SyncArtist the row of an
// artist. Doing it twice leaves each row once, and another album is not
// touched.
func TestSyncWritesTheRows(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	for range 2 {
		sync(t, s, []string{albumA}, []string{artistA})
		want(t, s, albumRows, albumA+"|Kind of Blue|Miles Davis")
		want(t, s, trackRows,
			track1+"|So What|Miles Davis|Kind of Blue",
			track2+"|Freddie Freeloader|Miles Davis feat. Wynton Kelly|Kind of Blue")
		want(t, s, artistRows, artistA+"|Miles Davis")
	}
	sync(t, s, []string{albumB}, []string{artistB})
	want(t, s, albumRows, albumA+"|Kind of Blue|Miles Davis", albumB+"|Homogenic|Björk")
	want(t, s, artistRows, artistA+"|Miles Davis", artistB+"|Björk")
	if got := rows(t, s, trackRows); len(got) != 3 {
		t.Fatalf("the rows of the tracks: %q", got)
	}

	// The tables are the ones of §10.1: prefixes, no diacritics, no case.
	for query, wantID := range map[string]string{
		`SELECT tracks.id FROM search_tracks JOIN tracks ON tracks.seq = search_tracks.rowid WHERE search_tracks MATCH '"joga"'`:      track3,
		`SELECT tracks.id FROM search_tracks JOIN tracks ON tracks.seq = search_tracks.rowid WHERE search_tracks MATCH '"fred"*'`:     track2,
		`SELECT tracks.id FROM search_tracks JOIN tracks ON tracks.seq = search_tracks.rowid WHERE search_tracks MATCH '"homogenic"'`: track3,
		`SELECT albums.id FROM search_albums JOIN albums ON albums.seq = search_albums.rowid WHERE search_albums MATCH '"bjork"'`:     albumB,
		`SELECT artists.id FROM search_artists JOIN artists ON artists.seq = search_artists.rowid WHERE search_artists MATCH '"mi"*'`: artistA,
	} {
		if got := rows(t, s, query); !slices.Equal(got, []string{wantID}) {
			t.Errorf("%s: %q, want %s", query, got, wantID)
		}
	}
}

// A row follows what it copies: after a change of the names, the next sync
// replaces the row, and the old words find nothing.
func TestSyncReplacesTheRows(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	sync(t, s, []string{albumA, albumB}, []string{artistA, artistB})
	ctx := t.Context()
	write(t, s, func(q *store.Queries) error {
		changed := track(track1, albumA, "Blue in Green", "Bill Evans", 1)
		return errors.Join(
			q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artistA, Name: "MILES DAVIS", SortKey: []byte{1}}),
			q.UpsertAlbum(ctx, album(albumA, artistA, "Kind of Blue (Legacy)")),
			q.UpsertTrack(ctx, changed),
			SyncAlbum(ctx, q.Conn(), albumA),
			SyncArtist(ctx, q.Conn(), artistA),
		)
	})
	want(t, s, albumRows, albumA+"|Kind of Blue (Legacy)|MILES DAVIS", albumB+"|Homogenic|Björk")
	want(t, s, trackRows,
		track1+"|Blue in Green|Bill Evans|Kind of Blue (Legacy)",
		track2+"|Freddie Freeloader|Miles Davis feat. Wynton Kelly|Kind of Blue (Legacy)",
		track3+"|Jóga|Björk|Homogenic")
	want(t, s, artistRows, artistA+"|MILES DAVIS", artistB+"|Björk")
	if got := rows(t, s, `SELECT rowid FROM search_tracks WHERE search_tracks MATCH '"what"'`); len(got) != 0 {
		t.Fatalf("the old title still finds %q", got)
	}
}

// Only what is available is in the tables: a track that became unavailable
// leaves at the next sync of its album, an unavailable album leaves with
// its tracks, and an artist is there while it has an available album.
func TestSyncRemovesWhatIsUnavailable(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	sync(t, s, []string{albumA, albumB}, []string{artistA, artistB})
	ctx := t.Context()

	write(t, s, func(q *store.Queries) error {
		return errors.Join(
			q.SetTrackUnavailable(ctx, store.SetTrackUnavailableParams{ID: track1, UpdatedAt: 2}),
			SyncAlbum(ctx, q.Conn(), albumA),
			SyncArtist(ctx, q.Conn(), artistA),
		)
	})
	want(t, s, trackRows,
		track2+"|Freddie Freeloader|Miles Davis feat. Wynton Kelly|Kind of Blue",
		track3+"|Jóga|Björk|Homogenic")
	want(t, s, albumRows, albumA+"|Kind of Blue|Miles Davis", albumB+"|Homogenic|Björk")
	want(t, s, artistRows, artistA+"|Miles Davis", artistB+"|Björk")

	// The album becomes unavailable with its tracks, as the scanner marks
	// one that left the library.
	write(t, s, func(q *store.Queries) error {
		if _, err := q.Conn().ExecContext(ctx, `UPDATE albums SET available = 0 WHERE id = ?`, albumA); err != nil {
			return err
		}
		if _, err := q.Conn().ExecContext(ctx, `UPDATE tracks SET available = 0 WHERE album_id = ?`, albumA); err != nil {
			return err
		}
		return errors.Join(SyncAlbum(ctx, q.Conn(), albumA), SyncArtist(ctx, q.Conn(), artistA))
	})
	want(t, s, trackRows, track3+"|Jóga|Björk|Homogenic")
	want(t, s, albumRows, albumB+"|Homogenic|Björk")
	want(t, s, artistRows, artistB+"|Björk")

	// And back: the rows return with the album.
	write(t, s, func(q *store.Queries) error {
		return errors.Join(
			q.UpsertAlbum(ctx, album(albumA, artistA, "Kind of Blue")),
			q.UpsertTrack(ctx, track(track2, albumA, "Freddie Freeloader", "Miles Davis", 2)),
			SyncAlbum(ctx, q.Conn(), albumA),
			SyncArtist(ctx, q.Conn(), artistA),
		)
	})
	want(t, s, trackRows, track2+"|Freddie Freeloader|Miles Davis|Kind of Blue", track3+"|Jóga|Björk|Homogenic")
	want(t, s, albumRows, albumA+"|Kind of Blue|Miles Davis", albumB+"|Homogenic|Björk")
	want(t, s, artistRows, artistA+"|Miles Davis", artistB+"|Björk")
}

// An id the index does not know has no rows: syncing it is not an error
// and writes nothing.
func TestSyncOfAnUnknownID(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	sync(t, s, []string{"0192a5f0-0000-7000-8000-00000000ffff", ""}, []string{"0192a5f0-0000-5000-8000-00000000ffff", ""})
	for _, query := range []string{albumRows, trackRows, artistRows} {
		want(t, s, query)
	}
}

// The full-text rows are written in the transaction of their caller: they
// are rolled back with it, and a failure is an error that names what was
// being indexed.
func TestSyncIsPartOfTheTransaction(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	ctx := t.Context()
	boom := errors.New("boom")
	err := s.WithWriteTx(ctx, func(q *store.Queries) error {
		if err := errors.Join(SyncAlbum(ctx, q.Conn(), albumA), SyncArtist(ctx, q.Conn(), artistA)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	for _, query := range []string{albumRows, trackRows, artistRows} {
		want(t, s, query)
	}

	// A read transaction cannot write.
	err = s.Read(ctx, func(q *store.Queries) error { return SyncAlbum(ctx, q.Conn(), albumA) })
	if err == nil || !strings.Contains(err.Error(), "search: indexing the album "+albumA) {
		t.Fatalf("SyncAlbum in a read transaction: %v", err)
	}
	err = s.Read(ctx, func(q *store.Queries) error { return SyncArtist(ctx, q.Conn(), artistA) })
	if err == nil || !strings.Contains(err.Error(), "search: indexing the artist "+artistA) {
		t.Fatalf("SyncArtist in a read transaction: %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = s.WithWriteTx(ctx, func(q *store.Queries) error { return SyncAlbum(cancelled, q.Conn(), albumA) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncAlbum with a context that ended: %v", err)
	}
}

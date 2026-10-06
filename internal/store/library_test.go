package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"modernc.org/sqlite"
)

// The queries of the index of the library (sql/artists.sql, albums.sql,
// tracks.sql), on a real database.

const (
	testArtist  = "0192a5f0-0000-5000-8000-0000000000a1"
	testArtist2 = "0192a5f0-0000-5000-8000-0000000000a2"
	testAlbum   = "0192a5f0-0000-7000-8000-0000000000b1"
	testAlbum2  = "0192a5f0-0000-7000-8000-0000000000b2"
	testTrack   = "0192a5f0-0000-7000-8000-0000000000c1"
	testTrack2  = "0192a5f0-0000-7000-8000-0000000000c2"
	testTrack3  = "0192a5f0-0000-7000-8000-0000000000c3"
)

func albumRow(id, artistID, title string, at int64) UpsertAlbumParams {
	return UpsertAlbumParams{
		ID: id, ArtistID: artistID, ArtistKey: []byte{1}, Title: title, TitleKey: []byte{2},
		YearKey: 10000, RelPath: "Artist/" + title, AlbumRevision: 1, RenderVersion: "r", ReceiptHash: "h",
		FirstSeenAt: at, UpdatedAt: at,
	}
}

func trackRow(id, albumID, fingerprint string, occurrence, durationMS, at int64) UpsertTrackParams {
	return UpsertTrackParams{
		ID: id, AlbumID: albumID, Fingerprint: fingerprint, FpVersion: "v", Occurrence: occurrence,
		Disc: 1, No: 1, Title: "T " + id, Artist: "A", RelPath: id + ".flac", FileSize: 1, FileMtimeNs: 2, FileSha256: "s",
		Codec: "flac", SampleRate: 44100, Channels: 2,
		DurationMs: sql.NullInt64{Int64: durationMS, Valid: durationMS > 0}, UpdatedAt: at,
		TitleKey: []byte{3}, ArtistKey: []byte{4}, FirstSeenAt: at,
	}
}

// write runs fn in a write transaction, which must succeed.
func write(t *testing.T, s *Store, fn func(q *Queries) error) {
	t.Helper()
	if err := s.WithWriteTx(t.Context(), fn); err != nil {
		t.Fatal(err)
	}
}

// seed writes one artist with one album.
func seed(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	write(t, s, func(q *Queries) error {
		return errors.Join(
			q.UpsertArtist(ctx, UpsertArtistParams{ID: testArtist, Name: "Artist", SortKey: []byte{1}}),
			q.UpsertAlbum(ctx, albumRow(testAlbum, testArtist, "Album", 100)),
		)
	})
}

func getAlbum(t *testing.T, s *Store, id string) GetIndexedAlbumRow {
	t.Helper()
	var row GetIndexedAlbumRow
	err := s.Read(t.Context(), func(q *Queries) (err error) {
		row, err = q.GetIndexedAlbum(t.Context(), id)
		return err
	})
	if err != nil {
		t.Fatalf("GetIndexedAlbum: %v", err)
	}
	return row
}

func listTracks(t *testing.T, s *Store, albumID string) []Track {
	t.Helper()
	var rows []Track
	err := s.Read(t.Context(), func(q *Queries) (err error) {
		rows, err = q.ListTracksByAlbum(t.Context(), albumID)
		return err
	})
	if err != nil {
		t.Fatalf("ListTracksByAlbum: %v", err)
	}
	return rows
}

// An artist that is written again keeps its row: only its name and its
// sort key change.
func TestUpsertArtist(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	err := s.Read(ctx, func(q *Queries) error {
		_, err := q.GetArtist(ctx, testArtist)
		return err
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("an unknown artist: %v, want sql.ErrNoRows", err)
	}
	var first, second Artist
	write(t, s, func(q *Queries) (err error) {
		if err := q.UpsertArtist(ctx, UpsertArtistParams{ID: testArtist, Name: "miles davis", SortKey: []byte{1}}); err != nil {
			return err
		}
		if first, err = q.GetArtist(ctx, testArtist); err != nil {
			return err
		}
		if err := q.UpsertArtist(ctx, UpsertArtistParams{ID: testArtist, Name: "Miles Davis", SortKey: []byte{2}}); err != nil {
			return err
		}
		second, err = q.GetArtist(ctx, testArtist)
		return err
	})
	if first.Name != "miles davis" || second.Name != "Miles Davis" || !bytes.Equal(second.SortKey, []byte{2}) || second.Seq != first.Seq {
		t.Fatalf("the artist: %+v, then %+v", first, second)
	}
}

// A new album is available and has no tracks; one that is written again
// keeps its row, the time it was first seen and its counters, and is
// available again.
func TestUpsertAlbum(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	err := s.Read(ctx, func(q *Queries) error {
		_, err := q.GetIndexedAlbum(ctx, testAlbum)
		return err
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("an unknown album: %v, want sql.ErrNoRows", err)
	}
	seed(t, s)
	first := getAlbum(t, s, testAlbum)
	if first.ArtistName != "Artist" || first.Album.Available != 1 || first.Album.TrackCount != 0 || first.Album.DurationMs != 0 ||
		first.Album.FirstSeenAt != 100 || first.Album.UpdatedAt != 100 || first.Album.Year.Valid || first.Album.CoverRel.Valid {
		t.Fatalf("the new album: %+v", first)
	}

	write(t, s, func(q *Queries) error {
		if err := q.UpsertTrack(ctx, trackRow(testTrack, testAlbum, "f", 1, 500, 100)); err != nil {
			return err
		}
		if err := q.UpdateAlbumCounters(ctx, testAlbum); err != nil {
			return err
		}
		if _, err := q.Conn().ExecContext(ctx, `UPDATE albums SET available = 0 WHERE id = ?`, testAlbum); err != nil {
			return err
		}
		if err := q.UpsertArtist(ctx, UpsertArtistParams{ID: testArtist2, Name: "Other", SortKey: []byte{9}}); err != nil {
			return err
		}
		again := albumRow(testAlbum, testArtist2, "Renamed", 200)
		again.Year, again.YearKey = sql.NullInt64{Int64: 1959, Valid: true}, 1959
		again.Genre = sql.NullString{String: "Jazz", Valid: true}
		again.Compilation, again.AlbumRevision, again.RenderVersion, again.ReceiptHash = 1, 2, "r2", "h2"
		again.CoverRel = sql.NullString{String: "cover.png", Valid: true}
		again.CoverSha256 = sql.NullString{String: "c", Valid: true}
		again.CoverMime = sql.NullString{String: "image/png", Valid: true}
		again.CoverSize = sql.NullInt64{Int64: 7, Valid: true}
		again.CoverMtimeNs = sql.NullInt64{Int64: 8, Valid: true}
		again.ArtistKey, again.TitleKey = []byte{9}, []byte{8}
		return q.UpsertAlbum(ctx, again)
	})
	second := getAlbum(t, s, testAlbum)
	a := second.Album
	if a.Seq != first.Album.Seq || a.FirstSeenAt != 100 || a.UpdatedAt != 200 || a.TrackCount != 1 || a.DurationMs != 500 || a.Available != 1 {
		t.Fatalf("what an album keeps when it is written again: %+v", a)
	}
	if second.ArtistName != "Other" || a.ArtistID != testArtist2 || a.Title != "Renamed" || a.RelPath != "Artist/Renamed" ||
		a.Year.Int64 != 1959 || a.YearKey != 1959 || a.Genre.String != "Jazz" || a.Compilation != 1 ||
		a.AlbumRevision != 2 || a.RenderVersion != "r2" || a.ReceiptHash != "h2" ||
		a.CoverRel.String != "cover.png" || a.CoverSha256.String != "c" || a.CoverMime.String != "image/png" ||
		a.CoverSize.Int64 != 7 || a.CoverMtimeNs.Int64 != 8 || !bytes.Equal(a.ArtistKey, []byte{9}) || !bytes.Equal(a.TitleKey, []byte{8}) {
		t.Fatalf("what an album takes when it is written again: %+v", second)
	}
}

// A track that is written again, found by its id, keeps its row and its
// album, takes everything else, and is available. Its rows are listed in
// the order they were created, available or not.
func TestUpsertTrack(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seed(t, s)
	write(t, s, func(q *Queries) error {
		return errors.Join(
			q.UpsertAlbum(ctx, albumRow(testAlbum2, testArtist, "Second", 100)),
			q.UpsertTrack(ctx, trackRow(testTrack2, testAlbum, "f2", 1, 0, 100)),
			q.UpsertTrack(ctx, trackRow(testTrack, testAlbum, "f1", 1, 300, 100)),
			q.SetTrackUnavailable(ctx, SetTrackUnavailableParams{ID: testTrack, UpdatedAt: 150}),
		)
	})
	rows := listTracks(t, s, testAlbum)
	if len(rows) != 2 || rows[0].ID != testTrack2 || rows[1].ID != testTrack || rows[0].Seq >= rows[1].Seq {
		t.Fatalf("the rows of the album: %+v", rows)
	}
	gone := rows[1]
	want := gone
	if gone.Available != 0 || gone.UpdatedAt != 150 || gone.Title != "T "+testTrack || gone.Fingerprint != "f1" || gone.DurationMs.Int64 != 300 {
		t.Fatalf("a track that became unavailable keeps what was known of it: %+v", gone)
	}
	if rows[0].Available != 1 || rows[0].DurationMs.Valid {
		t.Fatalf("the other track: %+v", rows[0])
	}

	changed := UpsertTrackParams{
		ID: testTrack, AlbumID: testAlbum, Fingerprint: "f9", FpVersion: "v2", Occurrence: 2, Disc: 2, No: 3,
		Title: "New", Artist: "Other", Genre: sql.NullString{String: "Jazz", Valid: true},
		RelPath: "Disc 2/03 - New.mp3", FileSize: 10, FileMtimeNs: 11, FileSha256: "s2",
		Codec: "mp3", SampleRate: 48000, Channels: 1,
		BitDepth: sql.NullInt64{Int64: 24, Valid: true}, Bitrate: sql.NullInt64{Int64: 128000, Valid: true},
		DurationMs: sql.NullInt64{Int64: 400, Valid: true},
		LyricsRel:  sql.NullString{String: "Disc 2/03 - New.lrc", Valid: true}, LyricsSha256: sql.NullString{String: "l", Valid: true},
		RgTrackGain: sql.NullFloat64{Float64: -1.5, Valid: true}, RgTrackPeak: sql.NullFloat64{Float64: 0.5, Valid: true},
		RgAlbumGain: sql.NullFloat64{Float64: -2.5, Valid: true}, RgAlbumPeak: sql.NullFloat64{Float64: 0.75, Valid: true},
		UpdatedAt: 200, TitleKey: []byte{5}, ArtistKey: []byte{6}, FirstSeenAt: 999,
	}
	write(t, s, func(q *Queries) error { return q.UpsertTrack(ctx, changed) })
	got := listTracks(t, s, testAlbum)[1]
	want = Track{
		Seq: want.Seq, ID: testTrack, AlbumID: testAlbum, Fingerprint: "f9", FpVersion: "v2", Occurrence: 2, Disc: 2, No: 3,
		Title: "New", Artist: "Other", Genre: changed.Genre, RelPath: changed.RelPath, FileSize: 10, FileMtimeNs: 11, FileSha256: "s2",
		Codec: "mp3", SampleRate: 48000, Channels: 1, BitDepth: changed.BitDepth, Bitrate: changed.Bitrate, DurationMs: changed.DurationMs,
		LyricsRel: changed.LyricsRel, LyricsSha256: changed.LyricsSha256,
		RgTrackGain: changed.RgTrackGain, RgTrackPeak: changed.RgTrackPeak, RgAlbumGain: changed.RgAlbumGain, RgAlbumPeak: changed.RgAlbumPeak,
		Available: 1, UpdatedAt: 200,
		// The keys follow the title and the artist; the copy of the key of
		// the album is not written here, and the moment the row was first
		// seen stays.
		TitleKey: []byte{5}, ArtistKey: []byte{6}, AlbumKey: want.AlbumKey, FirstSeenAt: 100,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the track written again:\n got %+v\nwant %+v", got, want)
	}

	// The id of a track is never the id of a track of another album: such
	// a write changes nothing.
	moved := changed
	moved.AlbumID, moved.Title, moved.UpdatedAt = testAlbum2, "Moved", 300
	write(t, s, func(q *Queries) error { return q.UpsertTrack(ctx, moved) })
	if after := listTracks(t, s, testAlbum)[1]; !reflect.DeepEqual(after, want) {
		t.Fatalf("a track was written through another album:\n got %+v\nwant %+v", after, want)
	}
	if rows := listTracks(t, s, testAlbum2); len(rows) != 0 {
		t.Fatalf("the other album has the rows %+v", rows)
	}

	// (album_id, fingerprint, occurrence) names one row.
	err := s.WithWriteTx(ctx, func(q *Queries) error {
		return q.UpsertTrack(ctx, trackRow(testTrack3, testAlbum, "f9", 2, 0, 300))
	})
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqliteConstraintUnique {
		t.Fatalf("a second row with one fingerprint and occurrence: %v", err)
	}
}

// The counters of an album count its available tracks, and add up the
// durations that are known.
func TestUpdateAlbumCounters(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seed(t, s)
	count := func() (int64, int64) {
		t.Helper()
		write(t, s, func(q *Queries) error { return q.UpdateAlbumCounters(ctx, testAlbum) })
		a := getAlbum(t, s, testAlbum).Album
		return a.TrackCount, a.DurationMs
	}
	if n, d := count(); n != 0 || d != 0 {
		t.Fatalf("an album without tracks: %d tracks, %d ms", n, d)
	}
	write(t, s, func(q *Queries) error {
		return errors.Join(
			q.UpsertAlbum(ctx, albumRow(testAlbum2, testArtist, "Second", 100)),
			q.UpsertTrack(ctx, trackRow(testTrack, testAlbum, "f1", 1, 300, 100)),
			q.UpsertTrack(ctx, trackRow(testTrack2, testAlbum, "f2", 1, 0, 100)), // no duration
			q.UpsertTrack(ctx, trackRow(testTrack3, testAlbum2, "f3", 1, 5000, 100)),
		)
	})
	if n, d := count(); n != 2 || d != 300 {
		t.Fatalf("two tracks, one without a duration: %d tracks, %d ms", n, d)
	}
	write(t, s, func(q *Queries) error {
		return q.SetTrackUnavailable(ctx, SetTrackUnavailableParams{ID: testTrack, UpdatedAt: 200})
	})
	if n, d := count(); n != 1 || d != 0 {
		t.Fatalf("after a track became unavailable: %d tracks, %d ms", n, d)
	}
	if a := getAlbum(t, s, testAlbum2).Album; a.TrackCount != 0 {
		t.Fatalf("the counters of another album changed: %+v", a)
	}
}

// The albums of an artist take its sort key, and only they; the list of
// its albums has the available ones.
func TestAlbumsOfAnArtist(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seed(t, s)
	const third = "0192a5f0-0000-7000-8000-0000000000b3"
	var ids []string
	write(t, s, func(q *Queries) (err error) {
		err = errors.Join(
			q.UpsertArtist(ctx, UpsertArtistParams{ID: testArtist2, Name: "Other", SortKey: []byte{5}}),
			q.UpsertAlbum(ctx, albumRow(testAlbum2, testArtist2, "Second", 100)),
			q.UpsertAlbum(ctx, albumRow(third, testArtist, "Third", 100)),
			q.SetArtistKeyOfAlbums(ctx, SetArtistKeyOfAlbumsParams{ArtistKey: []byte{7, 7}, ArtistID: testArtist}),
		)
		if err != nil {
			return err
		}
		if _, err := q.Conn().ExecContext(ctx, `UPDATE albums SET available = 0 WHERE id = ?`, third); err != nil {
			return err
		}
		ids, err = q.ListAlbumIDsByArtist(ctx, testArtist)
		return err
	})
	if !slices.Equal(ids, []string{testAlbum}) {
		t.Fatalf("the available albums of the artist: %v", ids)
	}
	for id, want := range map[string][]byte{testAlbum: {7, 7}, third: {7, 7}, testAlbum2: {1}} {
		if got := getAlbum(t, s, id).Album.ArtistKey; !bytes.Equal(got, want) {
			t.Errorf("the artist key of the album %s: %x, want %x", id, got, want)
		}
	}
}

// Conn is the transaction of the queries: what is written through it is
// rolled back with them, and in a read transaction it cannot write.
func TestQueriesConn(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	boom := errors.New("boom")
	err := s.WithWriteTx(ctx, func(q *Queries) error {
		if _, err := q.Conn().ExecContext(ctx, `INSERT INTO search_artists (rowid, name) VALUES (1, 'x')`); err != nil {
			return err
		}
		if err := q.SetMeta(ctx, SetMetaParams{Key: "k", Value: "v"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(q *Queries) error {
		var n int
		if err := q.Conn().QueryRowContext(ctx, `SELECT count(*) FROM search_artists`).Scan(&n); err != nil || n != 0 {
			t.Errorf("%d full-text rows after the rollback (%v)", n, err)
		}
		if _, err := q.GetMeta(ctx, "k"); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("the key written in the same transaction: %v", err)
		}
		_, err := q.Conn().ExecContext(ctx, `INSERT INTO search_artists (rowid, name) VALUES (1, 'x')`)
		return err
	})
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqliteReadOnly {
		t.Fatalf("a write through the connection of a read transaction: %v", err)
	}
}

// No query deletes a row of artists, albums or tracks, and none replaces
// one, which deletes it first (DESIGN.md I3): the ids that favorites and
// playlists hold must last. The test reads the sources of the queries.
func TestNoQueryDeletesFromTheIndex(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "sql", "*.sql"))
	if err != nil || len(files) < 4 {
		t.Fatalf("the query files: %v (%v)", files, err)
	}
	forbidden := regexp.MustCompile(`(?i)\b(delete\s+from|replace\s+into|insert\s+or\s+replace\s+into)\s+"?(artists|albums|tracks)\b`)
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if m := forbidden.Find(text); m != nil {
			t.Errorf("%s: %q: the rows of artists, albums and tracks are never deleted", filepath.Base(file), m)
		}
	}
}

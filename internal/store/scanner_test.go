package store

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
)

// The queries of the scanner (sql/albums.sql, artists.sql, tracks.sql and
// references.sql), on a real database.

const (
	testTrack4 = "0192a5f0-0000-7000-8000-0000000000c4"
	testUser   = "0192a5f0-0000-7000-8000-0000000000d1"
	testUser2  = "0192a5f0-0000-7000-8000-0000000000d2"
	testList   = "0192a5f0-0000-7000-8000-0000000000e1"
	testList2  = "0192a5f0-0000-7000-8000-0000000000e2"
)

// seedTwoAlbums writes one artist with two albums: tracks 1 and 2 in the
// first, 3 and 4 in the second. Tracks 1 and 3 have the fingerprint "same",
// and so does 4, as the second occurrence.
func seedTwoAlbums(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	seed(t, s)
	write(t, s, func(q *Queries) error {
		return errors.Join(
			q.UpsertAlbum(ctx, albumRow(testAlbum2, testArtist, "Second", 100)),
			q.UpsertTrack(ctx, trackRow(testTrack, testAlbum, "same", 1, 100, 100)),
			q.UpsertTrack(ctx, trackRow(testTrack2, testAlbum, "other", 1, 100, 100)),
			q.UpsertTrack(ctx, trackRow(testTrack3, testAlbum2, "same", 1, 100, 100)),
			q.UpsertTrack(ctx, trackRow(testTrack4, testAlbum2, "same", 2, 100, 100)),
			q.UpdateAlbumCounters(ctx, testAlbum),
			q.UpdateAlbumCounters(ctx, testAlbum2),
		)
	})
}

// An album that is gone is unavailable with its tracks, and the rows stay;
// the counters of the state say how many rows are available and how many
// are not; the states of the albums list every album.
func TestAlbumBecomesUnavailable(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedTwoAlbums(t, s)
	counts := func() (albums, tracks []int64) {
		t.Helper()
		err := s.Read(ctx, func(q *Queries) error {
			a, err := q.CountAlbums(ctx)
			if err != nil {
				return err
			}
			for _, row := range a {
				albums = append(albums, row.Available, row.Total)
			}
			tr, err := q.CountTracks(ctx)
			for _, row := range tr {
				tracks = append(tracks, row.Available, row.Total)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return albums, tracks
	}
	if a, tr := counts(); !slices.Equal(a, []int64{1, 2}) || !slices.Equal(tr, []int64{1, 4}) {
		t.Fatalf("the counters: albums %v, tracks %v", a, tr)
	}

	write(t, s, func(q *Queries) error {
		return errors.Join(
			q.SetTracksOfAlbumUnavailable(ctx, SetTracksOfAlbumUnavailableParams{UpdatedAt: 300, AlbumID: testAlbum2}),
			q.SetAlbumUnavailable(ctx, SetAlbumUnavailableParams{UpdatedAt: 300, ID: testAlbum2}),
			q.UpdateAlbumCounters(ctx, testAlbum2),
		)
	})
	gone := getAlbum(t, s, testAlbum2).Album
	if gone.Available != 0 || gone.UpdatedAt != 300 || gone.TrackCount != 0 || gone.DurationMs != 0 || gone.Title != "Second" {
		t.Fatalf("the album that is gone: %+v", gone)
	}
	for _, row := range listTracks(t, s, testAlbum2) {
		if row.Available != 0 || row.UpdatedAt != 300 || row.Fingerprint != "same" {
			t.Errorf("a track of the album that is gone: %+v", row)
		}
	}
	if kept := getAlbum(t, s, testAlbum).Album; kept.Available != 1 || kept.TrackCount != 2 || kept.UpdatedAt != 100 {
		t.Fatalf("the other album: %+v", kept)
	}
	for _, row := range listTracks(t, s, testAlbum) {
		if row.Available != 1 || row.UpdatedAt != 100 {
			t.Errorf("a track of the other album: %+v", row)
		}
	}
	if a, tr := counts(); !slices.Equal(a, []int64{0, 1, 1, 1}) || !slices.Equal(tr, []int64{0, 2, 1, 2}) {
		t.Fatalf("the counters: albums %v, tracks %v", a, tr)
	}

	// Gone twice changes nothing: the time is that of the first.
	write(t, s, func(q *Queries) error {
		return errors.Join(
			q.SetTracksOfAlbumUnavailable(ctx, SetTracksOfAlbumUnavailableParams{UpdatedAt: 400, AlbumID: testAlbum2}),
			q.SetAlbumUnavailable(ctx, SetAlbumUnavailableParams{UpdatedAt: 400, ID: testAlbum2}),
		)
	})
	if again := getAlbum(t, s, testAlbum2).Album; again.Available != 0 || again.UpdatedAt != 300 {
		t.Fatalf("the album after it was marked again: %+v", again)
	}

	var states []ListAlbumStatesRow
	if err := s.Read(ctx, func(q *Queries) (err error) {
		states, err = q.ListAlbumStates(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := []ListAlbumStatesRow{
		{ID: testAlbum, ArtistID: testArtist, RelPath: "Artist/Album", ReceiptHash: "h", Available: 1},
		{ID: testAlbum2, ArtistID: testArtist, RelPath: "Artist/Second", ReceiptHash: "h", Available: 0},
	}
	if !slices.Equal(states, want) {
		t.Fatalf("the states of the albums:\n got %+v\nwant %+v", states, want)
	}
}

// The sort keys of the artists and of the titles are written one by one,
// from the names the two lists return.
func TestSortKeyQueries(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedTwoAlbums(t, s)
	var artists []Artist
	var titles []ListAlbumTitlesRow
	write(t, s, func(q *Queries) (err error) {
		if artists, err = q.ListArtists(ctx); err != nil {
			return err
		}
		if titles, err = q.ListAlbumTitles(ctx); err != nil {
			return err
		}
		return errors.Join(
			q.SetArtistSortKey(ctx, SetArtistSortKeyParams{SortKey: []byte{7}, ID: testArtist}),
			q.SetAlbumTitleKey(ctx, SetAlbumTitleKeyParams{TitleKey: []byte{8}, ID: testAlbum2}),
		)
	})
	if len(artists) != 1 || artists[0].ID != testArtist || artists[0].Name != "Artist" {
		t.Fatalf("the artists: %+v", artists)
	}
	if !slices.Equal(titles, []ListAlbumTitlesRow{{ID: testAlbum, Title: "Album"}, {ID: testAlbum2, Title: "Second"}}) {
		t.Fatalf("the titles: %+v", titles)
	}
	first, second := getAlbum(t, s, testAlbum).Album, getAlbum(t, s, testAlbum2).Album
	if !bytes.Equal(first.TitleKey, []byte{2}) || !bytes.Equal(second.TitleKey, []byte{8}) || !bytes.Equal(second.ArtistKey, []byte{1}) {
		t.Fatalf("the keys of the albums: %x, %x, %x", first.TitleKey, second.TitleKey, second.ArtistKey)
	}
	err := s.Read(ctx, func(q *Queries) error {
		a, err := q.GetArtist(ctx, testArtist)
		if err == nil && !bytes.Equal(a.SortKey, []byte{7}) {
			t.Errorf("the key of the artist: %x", a.SortKey)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The fingerprints of another ffmpeg are listed a page at a time, for the
// available tracks only; one is written on its row only while the row is
// what was read; the occurrences of the other rows are those the row
// cannot take.
func TestFingerprintQueries(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedTwoAlbums(t, s)
	write(t, s, func(q *Queries) error {
		// trackRow writes the version "v": tracks 2 and 3 are of "v2".
		for _, id := range []string{testTrack2, testTrack3} {
			if _, err := q.Conn().ExecContext(ctx, `UPDATE tracks SET fp_version = 'v2' WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return q.SetTrackUnavailable(ctx, SetTrackUnavailableParams{ID: testTrack4, UpdatedAt: 100})
	})
	page := func(after, size int64) (ids []string, last int64) {
		t.Helper()
		err := s.Read(ctx, func(q *Queries) error {
			rows, err := q.ListStaleFingerprints(ctx, ListStaleFingerprintsParams{FpVersion: "v2", AfterSeq: after, PageSize: size})
			for _, row := range rows {
				ids, last = append(ids, row.ID), row.Seq
				if row.ID == testTrack && (row.AlbumID != testAlbum || row.AlbumRelPath != "Artist/Album" || row.RelPath != testTrack+".flac" ||
					row.FileSize != 1 || row.FileMtimeNs != 2 || row.FileSha256 != "s" || row.Fingerprint != "same" || row.FpVersion != "v" || row.Occurrence != 1) {
					t.Errorf("the row of the first track: %+v", row)
				}
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return ids, last
	}
	// Track 4 has the version "v" too, and is not available.
	ids, last := page(0, 10)
	if !slices.Equal(ids, []string{testTrack}) {
		t.Fatalf("the tracks to compute again: %v", ids)
	}
	if ids, _ := page(last, 10); len(ids) != 0 {
		t.Fatalf("the page after the last: %v", ids)
	}
	write(t, s, func(q *Queries) error {
		_, err := q.Conn().ExecContext(ctx, `UPDATE tracks SET fp_version = 'v'`)
		return err
	})
	first, last := page(0, 2)
	rest, _ := page(last, 2)
	if !slices.Equal(first, []string{testTrack, testTrack2}) || !slices.Equal(rest, []string{testTrack3}) {
		t.Fatalf("two pages of two: %v, then %v", first, rest)
	}

	occurrences := func(albumID, fingerprint, id string) (out []int64) {
		t.Helper()
		if err := s.Read(ctx, func(q *Queries) (err error) {
			out, err = q.ListOccurrences(ctx, ListOccurrencesParams{AlbumID: albumID, Fingerprint: fingerprint, ID: id})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// The rows of the second album with "same", available or not, but for
	// the row itself.
	if got := occurrences(testAlbum2, "same", testTrack2); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("the occurrences of the other rows: %v", got)
	}
	if got := occurrences(testAlbum2, "same", testTrack3); !slices.Equal(got, []int64{2}) {
		t.Fatalf("the occurrences without the row itself: %v", got)
	}
	if got := occurrences(testAlbum, "new", testTrack); len(got) != 0 {
		t.Fatalf("the occurrences of a fingerprint nobody has: %v", got)
	}

	set := SetTrackFingerprintParams{
		Fingerprint: "new", FpVersion: "v2", Occurrence: 3, UpdatedAt: 500,
		ID: testTrack, OldFingerprint: "same", OldFpVersion: "v", FileSize: 1, FileMtimeNs: 2,
	}
	apply := func(p SetTrackFingerprintParams) (n int64) {
		t.Helper()
		write(t, s, func(q *Queries) (err error) {
			n, err = q.SetTrackFingerprint(ctx, p)
			return err
		})
		return n
	}
	before := listTracks(t, s, testAlbum)[0]
	// Each of these is a row that is no longer the one that was read.
	for name, change := range map[string]func(*SetTrackFingerprintParams){
		"another fingerprint": func(p *SetTrackFingerprintParams) { p.OldFingerprint = "x" },
		"another version":     func(p *SetTrackFingerprintParams) { p.OldFpVersion = "x" },
		"another size":        func(p *SetTrackFingerprintParams) { p.FileSize = 9 },
		"another time":        func(p *SetTrackFingerprintParams) { p.FileMtimeNs = 9 },
		"another row":         func(p *SetTrackFingerprintParams) { p.ID = "nobody" },
	} {
		p := set
		change(&p)
		if n := apply(p); n != 0 {
			t.Errorf("%s: %d rows written", name, n)
		}
	}
	if after := listTracks(t, s, testAlbum)[0]; after != before {
		t.Fatalf("the row was written: %+v", after)
	}
	if n := apply(set); n != 1 {
		t.Fatalf("%d rows written, want 1", n)
	}
	want := before
	want.Fingerprint, want.FpVersion, want.Occurrence, want.UpdatedAt = "new", "v2", 3, 500
	if after := listTracks(t, s, testAlbum)[0]; after != want {
		t.Fatalf("the row:\n got %+v\nwant %+v", after, want)
	}
	// A row that is not available is not written.
	gone := set
	gone.ID, gone.OldFingerprint = testTrack4, "same"
	if n := apply(gone); n != 0 {
		t.Fatalf("%d rows written for a track that is not available", n)
	}
}

// userRows writes two users, a playlist each and their favorites, with the
// test's own SQL: the queries that create them arrive with the API.
func userRows(t *testing.T, s *Store) {
	t.Helper()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id, username, password_hash, role, created_at, password_changed_at) VALUES (?, 'ann', 'x', 'user', 1, 1), (?, 'bob', 'x', 'user', 1, 1)`,
			[]any{testUser, testUser2}},
		{`INSERT INTO playlists (id, user_id, name, revision, created_at, updated_at) VALUES (?, ?, 'A', 1, 10, 10), (?, ?, 'B', 4, 10, 10)`,
			[]any{testList, testUser, testList2, testUser2}},
		{`INSERT INTO playlist_items (id, playlist_id, track_id, position, added_at) VALUES
			('i1', ?, ?, 0, 20), ('i2', ?, ?, 1, 21), ('i3', ?, ?, 2, 22), ('i4', ?, ?, 0, 23)`,
			[]any{testList, testTrack, testList, testTrack2, testList, testTrack, testList2, testTrack2}},
		{`INSERT INTO favorites (user_id, track_id, created_at) VALUES (?, ?, 30), (?, ?, 31), (?, ?, 32)`,
			[]any{testUser, testTrack, testUser2, testTrack, testUser2, testTrack3}},
	} {
		mustExec(t, s.write, stmt.query, stmt.args...)
	}
}

// The references to move are those of the tracks that are not available and
// have an available track with their fingerprint; the tracks of the same
// album come first, then the row created last. Moving them keeps what a
// playlist item and a favorite are, but for their track.
func TestReferenceQueries(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedTwoAlbums(t, s)
	userRows(t, s)
	moved := func() (out []ListMovedReferencesRow) {
		t.Helper()
		if err := s.Read(ctx, func(q *Queries) (err error) {
			out, err = q.ListMovedReferences(ctx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	unavailable := func(ids ...string) {
		t.Helper()
		for _, id := range ids {
			mustExec(t, s.write, `UPDATE tracks SET available = 0 WHERE id = ?`, id)
		}
	}
	// Every track is available.
	if got := moved(); len(got) != 0 {
		t.Fatalf("references to move with every track available: %v", got)
	}
	// Track 2 has references and no twin; track 1 has references and two
	// available twins in the other album, of which track 4 is the newer.
	unavailable(testTrack, testTrack2)
	want := []ListMovedReferencesRow{{OldID: testTrack, NewID: testTrack4}, {OldID: testTrack, NewID: testTrack3}}
	if got := moved(); !slices.Equal(got, want) {
		t.Fatalf("references to move: %v, want %v", got, want)
	}
	// A twin in the album of the track comes before a newer one elsewhere.
	const sameAlbum = "0192a5f0-0000-7000-8000-0000000000c5"
	write(t, s, func(q *Queries) error { return q.UpsertTrack(ctx, trackRow(sameAlbum, testAlbum, "same", 2, 100, 100)) })
	mustExec(t, s.write, `UPDATE tracks SET fp_version = 'another ffmpeg' WHERE id = ?`, sameAlbum)
	want = append([]ListMovedReferencesRow{{OldID: testTrack, NewID: sameAlbum}}, want...)
	if got := moved(); !slices.Equal(got, want) {
		t.Fatalf("references to move with a twin in the same album: %v, want %v", got, want)
	}
	// Twins that are not available are no place to move to, and a track
	// without references is not listed.
	unavailable(sameAlbum, testTrack4)
	if got := moved(); !slices.Equal(got, []ListMovedReferencesRow{{OldID: testTrack, NewID: testTrack3}}) {
		t.Fatalf("references to move with one available twin: %v", got)
	}

	var lists, none []string
	write(t, s, func(q *Queries) (err error) {
		if lists, err = q.ListPlaylistIDsByTrack(ctx, testTrack2); err != nil {
			return err
		}
		if none, err = q.ListPlaylistIDsByTrack(ctx, testTrack3); err != nil {
			return err
		}
		return errors.Join(
			q.MovePlaylistItems(ctx, MovePlaylistItemsParams{NewID: testTrack3, OldID: testTrack}),
			q.TouchPlaylist(ctx, TouchPlaylistParams{UpdatedAt: 900, ID: testList}),
			q.CopyFavorites(ctx, CopyFavoritesParams{NewID: testTrack3, OldID: testTrack}),
			q.DeleteFavoritesOfTrack(ctx, testTrack),
		)
	})
	if !slices.Equal(lists, []string{testList, testList2}) || len(none) != 0 {
		t.Fatalf("the playlists of a track: %v and %v", lists, none)
	}
	items := strings1(t, s.read, `SELECT id || ' ' || playlist_id || ' ' || track_id || ' ' || position || ' ' || added_at FROM playlist_items ORDER BY id`)
	wantItems := []string{
		"i1 " + testList + " " + testTrack3 + " 0 20",
		"i2 " + testList + " " + testTrack2 + " 1 21",
		"i3 " + testList + " " + testTrack3 + " 2 22",
		"i4 " + testList2 + " " + testTrack2 + " 0 23",
	}
	if !slices.Equal(items, wantItems) {
		t.Fatalf("the items:\n got %v\nwant %v", items, wantItems)
	}
	revisions := strings1(t, s.read, `SELECT name || ' ' || revision || ' ' || created_at || ' ' || updated_at FROM playlists ORDER BY name`)
	if !slices.Equal(revisions, []string{"A 2 10 900", "B 4 10 10"}) {
		t.Fatalf("the playlists: %v", revisions)
	}
	// ann's favorite moved with its moment; bob had both, and keeps the one
	// he had on the track that is left.
	favorites := strings1(t, s.read, `SELECT u.username || ' ' || f.track_id || ' ' || f.created_at FROM favorites f JOIN users u ON u.id = f.user_id ORDER BY 1`)
	if !slices.Equal(favorites, []string{"ann " + testTrack3 + " 30", "bob " + testTrack3 + " 32"}) {
		t.Fatalf("the favorites: %v", favorites)
	}
	if got := moved(); len(got) != 0 {
		t.Fatalf("references to move once they moved: %v", got)
	}
	if got := strings1(t, s.read, `PRAGMA foreign_key_check`); len(got) != 0 {
		t.Fatalf("foreign_key_check: %v", got)
	}
}

// Optimize runs between the transactions, and says so when its context is
// over.
func TestOptimize(t *testing.T) {
	s := newStore(t)
	seedTwoAlbums(t, s)
	if err := s.Optimize(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The store still works.
	if a := getAlbum(t, s, testAlbum).Album; a.TrackCount != 2 {
		t.Fatalf("the album after Optimize: %+v", a)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Optimize(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Optimize with a context that is over: %v", err)
	}
}

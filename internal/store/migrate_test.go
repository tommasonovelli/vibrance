package store

import (
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"vibrance/migrations"
)

// Migration 00003 (step W1, the list of the tracks) on a database that
// release 0.1.0 left, with rows: every track takes the moment its album was
// first seen and a copy of the key of the title of its album, available or
// not; the version of the collation is forgotten, so that the startup
// computes the keys of the titles and the artists of the tracks, which SQL
// cannot (DESIGN.md T26); nothing else changes.
func TestMigrationOfTheTrackList(t *testing.T) {
	path := dbPath(t)
	db := rawOpen(t, path)
	p, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO meta ("key", value) VALUES ('collate_version', 'golang.org/x/text v0.41.0'), ('ffmpeg_version', 'f')`,
		`INSERT INTO artists (id, name, sort_key) VALUES ('` + testArtist + `', 'Artist', x'01')`,
		`INSERT INTO albums (id, artist_id, artist_key, title, title_key, year_key, compilation, rel_path, album_revision,
			render_version, receipt_hash, track_count, duration_ms, available, first_seen_at, updated_at) VALUES
			('` + testAlbum + `', '` + testArtist + `', x'01', 'One', x'0a0b', 10000, 0, 'Artist/One', 1, 'r', 'h', 1, 0, 1, 1234, 1300),
			('` + testAlbum2 + `', '` + testArtist + `', x'01', 'Two', x'0c', 10000, 0, 'Artist/Two', 1, 'r', 'h', 0, 0, 0, 5678, 5700)`,
		`INSERT INTO tracks (id, album_id, fingerprint, fp_version, disc, "no", title, artist, rel_path, file_size,
			file_mtime_ns, file_sha256, codec, sample_rate, channels, available, updated_at) VALUES
			('` + testTrack + `', '` + testAlbum + `', 'f1', 'v', 1, 1, 'A', 'X', 'a.flac', 1, 1, 's', 'flac', 44100, 2, 1, 1300),
			('` + testTrack2 + `', '` + testAlbum + `', 'f2', 'v', 1, 2, 'B', 'Y', 'b.flac', 1, 1, 's', 'flac', 44100, 2, 0, 1300),
			('` + testTrack3 + `', '` + testAlbum2 + `', 'f3', 'v', 1, 1, 'C', 'Z', 'c.flac', 1, 1, 's', 'flac', 44100, 2, 0, 5700)`,
	} {
		mustExec(t, db, stmt)
	}

	s := openStore(t, path)
	defer closeStore(t, s)
	got := strings1(t, s.read, `SELECT id || ' ' || hex(album_key) || ' ' || first_seen_at || ' ' || hex(title_key) || ' ' ||
		hex(artist_key) || ' ' || title || ' ' || available || ' ' || updated_at FROM tracks ORDER BY seq`)
	want := []string{
		testTrack + " 0A0B 1234   A 1 1300",
		testTrack2 + " 0A0B 1234   B 0 1300",
		testTrack3 + " 0C 5678   C 0 5700",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the tracks after the migration:\n got %q\nwant %q", got, want)
	}
	if got := strings1(t, s.read, `SELECT "key" || '=' || value FROM meta ORDER BY "key"`); !slices.Equal(got, []string{"ffmpeg_version=f"}) {
		t.Fatalf("meta after the migration: %v", got)
	}
}

// The indexes of the list of the tracks begin with available. A database
// without statistics (a first scan, before the first PRAGMA optimize) must
// not read the tracks of one album through one of them: it would read
// every available track for each album. The queries that read the tracks
// of an album name their index, or keep SQLite from the others.
func TestTracksOfOneAlbumAreFoundByTheAlbum(t *testing.T) {
	s := newStore(t)
	for name, c := range map[string]struct {
		query string
		args  []any
	}{
		"UpdateAlbumCounters":         {updateAlbumCounters, []any{testAlbum}},
		"ListAvailableTracksOfAlbum":  {listAvailableTracksOfAlbum, []any{testUser, testAlbum}},
		"SetTracksOfAlbumUnavailable": {setTracksOfAlbumUnavailable, []any{1, testAlbum}},
		"SetAlbumKeyOfTracks":         {setAlbumKeyOfTracks, []any{[]byte{1}, testAlbum}},
		"ListTracksByAlbum":           {listTracksByAlbum, []any{testAlbum}},
		"ListOccurrences":             {listOccurrences, []any{testAlbum, "f", testTrack}},
		"ListAlbumsWithWrongCounters": {listAlbumsWithWrongCounters, nil},
	} {
		plan := explain(t, s, c.query, c.args...)
		found := false
		for _, line := range plan {
			if !strings.Contains(line, " tracks ") {
				continue
			}
			found = true
			if !strings.Contains(line, "SEARCH tracks USING") || !strings.Contains(line, "(album_id=?") {
				t.Errorf("%s reads the tracks otherwise than by their album: %s", name, line)
			}
		}
		if !found {
			t.Errorf("%s: no line of the plan reads the tracks: %q", name, plan)
		}
	}
}

// The same for the tracks with the audio of a track that is gone (the end
// of every cycle): they are found by their fingerprint.
func TestAudioTwinsAreFoundByTheirFingerprint(t *testing.T) {
	s := newStore(t)
	plan := explain(t, s, listAudioTwins)
	if !slices.ContainsFunc(plan, func(line string) bool {
		return strings.Contains(line, "SEARCH here USING INDEX tracks_fingerprint_idx (fingerprint=?)")
	}) {
		t.Fatalf("the plan of ListAudioTwins: %q", plan)
	}
}

// The same for the list of the tracks of the albums of one artist: the
// tracks of each album by the album, and the rows of the page by their
// key.
func TestTracksOfOneArtistAreFoundByTheirAlbums(t *testing.T) {
	s := newStore(t)
	plan := explain(t, s, listArtistTracksByTitleAsc, testUser, testArtist, 1, []byte{}, testTrack, 51)
	for _, want := range []string{
		"SEARCH a USING INDEX albums_artist_id_idx (artist_id=? AND available=?)",
		"SEARCH t USING INDEX tracks_album_disc_no_idx (album_id=?)",
		"SEARCH tracks USING INTEGER PRIMARY KEY (rowid=?)",
	} {
		if !slices.Contains(plan, want) {
			t.Errorf("the plan has no %q:\n%s", want, strings.Join(plan, "\n"))
		}
	}
}

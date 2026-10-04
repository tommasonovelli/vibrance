package library

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/media"
	"vibrance/internal/names"
	"vibrance/internal/store"
)

// wantTrack is what the index must say of a track of the fixture: what its
// tags say (testdata/FIXTURE.md, and the fixture table of internal/media).
type wantTrack struct {
	path, title, artist, genre string
	disc, no                   int64
	codec                      string
	bitDepth, bitrate          int64 // 0: the column is null
	// gain is the ReplayGain of the track; nil for a track without
	// ReplayGain. The peaks are 0.125 and the gain of the album -7.01.
	gain        *float64
	fingerprint string
	occurrence  int64
}

type wantAlbum struct {
	rel, title, artist, genre string
	year                      int64
	tracks                    []wantTrack
}

// fixtureIndex is the fixture library as the index must hold it.
var fixtureIndex = []wantAlbum{
	{albumA, "Alpha: Light?", "Aurora Sines", "Ambient", 2001, []wantTrack{
		{"01 - First Light.flac", "First Light", "Aurora Sines", "Ambient", 1, 1, "flac", 16, 0, f64(-6.5),
			"e2b19f9a76987131c390ccc590eaf6d6819c8a809fc7ffb0ca7251d29054464c", 1},
		{"02 - Second Wave.flac", "Second Wave", "Aurora Sines feat. Guest", "Ambient", 1, 2, "flac", 16, 0, f64(-7.5),
			"5460de58357ae782e98c5116a5df926c7186877922695d19ae3ef78ba4c27e51", 1},
		{"03 - Third_.flac", "Third?", "Aurora Sines", "Ambient", 1, 3, "flac", 16, 0, f64(-8.5),
			"28041741fdd9bb2ffceb16a8c5f958815c1d1e1972dd40ecdf79a4850535c424", 1},
	}},
	{albumB, "Beta MP3", "Bravo Tones", "Jazz", 2002, []wantTrack{
		{"01 - One.mp3", "One", "Bravo Tones", "Jazz", 1, 1, "mp3", 0, 128000, f64(-6.5),
			"20a49a4b23ab41d7eef89093ac1f9dbff29b8d42a717a77c30dc0f30d89b4053", 1},
		{"02 - Two.mp3", "Two", "Bravo Tones", "Jazz", 1, 2, "mp3", 0, 128000, f64(-7.5),
			"6b051886d465925cfcfb4ab4f690fc85ce472ca15c7ffcaf3ebaf110b4843d7a", 1},
	}},
	{albumC, "Gamma AAC", "Charlie Waves", "Rock", 2003, []wantTrack{
		{"01 - One.m4a", "One", "Charlie Waves", "Rock", 1, 1, "aac", 0, 96392, f64(-6.5),
			"504febaca2eeea30c89b87970b47e7a6eb5c5370c8d0841c1781e9aef169cada", 1},
		{"02 - Two.m4a", "Two", "Charlie Waves", "Rock", 1, 2, "aac", 0, 95930, f64(-7.5),
			"3c1564cfd1889aa04e1b69b7677241e57f8d204507c0d2e4ccbfe5f44e937449", 1},
	}},
	{albumD, "Delta ALAC", "Delta Pulse", "Classical", 2004, []wantTrack{
		{"01 - One.m4a", "One", "Delta Pulse", "Classical", 1, 1, "alac", 16, 136208, f64(-6.5),
			"acf895ce37b840d3d1fe13a178aa9925cc0bb0f834b5bcf71b50fd9b616dff77", 1},
		{"02 - Two.m4a", "Two", "Delta Pulse", "Classical", 1, 2, "alac", 16, 138348, f64(-7.5),
			"1683c6399524b716f47ca75d4226c589b50b02d18765cb70162c92f9e834ae6c", 1},
	}},
	{albumE, "Epsilon Discs", "Écho Café", "Pop", 2005, []wantTrack{
		{"Disc 1/01 - Disc One Track One.flac", "Disc One Track One", "Écho Café", "Pop", 1, 1, "flac", 16, 0, nil,
			"27d802a92566f8eacee14e07330c75aee20acead2f6b5acf8583f8285862ef3b", 1},
		{"Disc 1/02 - Disc One Track Two.flac", "Disc One Track Two", "Écho Café", "Pop", 1, 2, "flac", 16, 0, nil,
			"b65264ad65281ab17f0f1b3898380bb10cd8c0a50b1fac4e81c615d572098158", 1},
		{"Disc 2/01 - Disc Two Track One.flac", "Disc Two Track One", "Écho Café", "Pop", 2, 1, "flac", 16, 0, nil,
			"1016939daf7ed11d08e9e3a462026d17781d990345edb99d7a8d9ed848fb5045", 1},
	}},
	{albumF, "Phi Same Audio", "Foxtrot Twins", "Electronic", 2006, []wantTrack{
		{"01 - Same Audio.flac", "Same Audio", "Foxtrot Twins", "Electronic", 1, 1, "flac", 16, 0, nil,
			"aff7f05f07877bc111dda862c7a0217429b323d1aaa8a2276294096309ea7904", 1},
		{"02 - Same Audio Again.flac", "Same Audio Again", "Foxtrot Twins", "Electronic", 1, 2, "flac", 16, 0, nil,
			"aff7f05f07877bc111dda862c7a0217429b323d1aaa8a2276294096309ea7904", 2},
	}},
}

// The cover and the lyrics of album A, as its receipt lists them.
const (
	coverASHA256  = "8a667cbc03b48ecbe995061ece4187dcd9ab58d96a5879f270c1b4f2d30ffd4e"
	lyricsASHA256 = "a96374149cc0654a8f7f98b8c87010fdc3bf2bcf1d30fcf4e8f3cbb28751ab67"
)

func nullF(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }

// Indexing the six albums of the fixture gives the index what their tags
// say, for the four codecs: titles, artists, year, genre, compilation,
// numbers, durations, codecs and ReplayGain, the files as the receipt and
// the disk have them, and the full-text rows of all of it.
func TestIndexAlbumFixture(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()

	if p, f := e.media.probes.Load(), e.media.fingerprints.Load(); p != 14 || f != 14 {
		t.Errorf("%d probes and %d fingerprints for 14 new tracks", p, f)
	}
	if got := e.warmer.take(); !slices.Equal(got, []string{coverASHA256}) {
		t.Errorf("covers to warm: %v, want the cover of album A once", got)
	}

	ids := map[string]bool{}
	for n, want := range fixtureIndex {
		albumID := fixtureAlbums[want.rel]
		c := e.candidate(want.rel)
		got := e.album(albumID)
		a := got.Album
		// The clock of the test gives one more second at each indexing.
		when := clockStart + int64(n+1)*1000
		if got.ArtistName != want.artist || a.ArtistID != names.ArtistID(want.artist) || !bytes.Equal(a.ArtistKey, names.SortKey(want.artist)) {
			t.Errorf("%s: artist %q (%s), want %q", want.rel, got.ArtistName, a.ArtistID, want.artist)
		}
		if a.Title != want.title || !bytes.Equal(a.TitleKey, names.SortKey(want.title)) {
			t.Errorf("%s: title %q, want %q", want.rel, a.Title, want.title)
		}
		if a.Year != (sql.NullInt64{Int64: want.year, Valid: true}) || a.YearKey != want.year {
			t.Errorf("%s: year %v with key %d, want %d", want.rel, a.Year, a.YearKey, want.year)
		}
		if a.Genre.String != want.genre || a.Compilation != 0 {
			t.Errorf("%s: genre %v, compilation %d; want %q and 0", want.rel, a.Genre, a.Compilation, want.genre)
		}
		if a.RelPath != want.rel || a.AlbumRevision != 1 || a.RenderVersion != c.Receipt.RenderVersion || a.ReceiptHash != c.ReceiptHash {
			t.Errorf("%s: path %q, revision %d, render version %q, receipt hash %q", want.rel, a.RelPath, a.AlbumRevision, a.RenderVersion, a.ReceiptHash)
		}
		if a.TrackCount != int64(len(want.tracks)) || a.DurationMs != 2000*int64(len(want.tracks)) || a.Available != 1 {
			t.Errorf("%s: %d tracks, %d ms, available %d", want.rel, a.TrackCount, a.DurationMs, a.Available)
		}
		if a.FirstSeenAt != when || a.UpdatedAt != when {
			t.Errorf("%s: first seen at %d and updated at %d, want %d", want.rel, a.FirstSeenAt, a.UpdatedAt, when)
		}
		if want.rel == albumA {
			info, err := os.Stat(e.inAlbum(albumA, "cover.jpg"))
			if err != nil {
				t.Fatal(err)
			}
			if a.CoverRel.String != "cover.jpg" || a.CoverSha256.String != coverASHA256 || a.CoverMime.String != "image/jpeg" ||
				a.CoverSize.Int64 != 660 || a.CoverMtimeNs.Int64 != info.ModTime().UnixNano() {
				t.Errorf("the cover of album A: %v %v %v %v %v", a.CoverRel, a.CoverSha256, a.CoverMime, a.CoverSize, a.CoverMtimeNs)
			}
		} else if a.CoverRel.Valid || a.CoverSha256.Valid || a.CoverMime.Valid || a.CoverSize.Valid || a.CoverMtimeNs.Valid {
			t.Errorf("%s has no cover, and the index says %v %v", want.rel, a.CoverRel, a.CoverSha256)
		}

		rows := e.tracks(albumID)
		if len(rows) != len(want.tracks) {
			t.Fatalf("%s: %d rows, want %d", want.rel, len(rows), len(want.tracks))
		}
		for i, w := range want.tracks {
			r := rows[i]
			id, err := uuid.Parse(r.ID)
			if err != nil || id.Version() != 7 || id.String() != r.ID || ids[r.ID] {
				t.Errorf("%s: the id %q is not a new lowercase UUIDv7", w.path, r.ID)
			}
			ids[r.ID] = true
			info, err := os.Stat(e.inAlbum(want.rel, w.path))
			if err != nil {
				t.Fatal(err)
			}
			var file ReceiptFile
			for _, f := range c.Receipt.Files {
				if f.Path == w.path {
					file = f
				}
			}
			wantRow := store.Track{
				Seq: r.Seq, ID: r.ID, AlbumID: albumID,
				Fingerprint: w.fingerprint, FpVersion: media.PinnedVersion, Occurrence: w.occurrence,
				Disc: w.disc, No: w.no, Title: w.title, Artist: w.artist, Genre: sql.NullString{String: w.genre, Valid: true},
				RelPath: w.path, FileSize: file.Size, FileMtimeNs: info.ModTime().UnixNano(), FileSha256: file.SHA256,
				Codec: w.codec, SampleRate: 44100, Channels: 2,
				BitDepth: sql.NullInt64{Int64: w.bitDepth, Valid: w.bitDepth != 0}, Bitrate: sql.NullInt64{Int64: w.bitrate, Valid: w.bitrate != 0},
				DurationMs: sql.NullInt64{Int64: 2000, Valid: true},
				Available:  1, UpdatedAt: when,
			}
			if w.gain != nil {
				wantRow.RgTrackGain, wantRow.RgTrackPeak, wantRow.RgAlbumGain, wantRow.RgAlbumPeak = nullF(*w.gain), nullF(0.125), nullF(-7.01), nullF(0.125)
			}
			if want.rel == albumA && i == 0 {
				wantRow.LyricsRel = sql.NullString{String: "01 - First Light.lrc", Valid: true}
				wantRow.LyricsSha256 = sql.NullString{String: lyricsASHA256, Valid: true}
			}
			if file.Size == 0 || r != wantRow {
				t.Errorf("%s/%s:\n got %+v\nwant %+v", want.rel, w.path, r, wantRow)
			}
			// The full-text row of the track has the seq of the track as
			// its rowid (T6), and is found by its title, its artist and
			// its album.
			for _, words := range []string{w.title, w.artist, want.title} {
				if !slices.Contains(e.match(matchTracks, words), r.Seq) {
					t.Errorf("%s/%s: searching %q does not find the track", want.rel, w.path, words)
				}
			}
		}
		e.wantRows(matchAlbums, want.title, a.Seq)
		if !slices.Contains(e.match(matchAlbums, want.artist), a.Seq) {
			t.Errorf("%s: searching its artist does not find the album", want.rel)
		}
		var artist store.Artist
		e.read(func(q *store.Queries) (err error) {
			artist, err = q.GetArtist(t.Context(), a.ArtistID)
			return err
		})
		if artist.Name != want.artist || !bytes.Equal(artist.SortKey, names.SortKey(want.artist)) {
			t.Errorf("%s: the artist row is %+v", want.rel, artist)
		}
		e.wantRows(matchArtists, want.artist, artist.Seq)
	}
	// And nothing else: one row for each track, album and artist.
	dump := e.dump()
	for table, want := range map[string]int{"artists ": 6, "albums ": 6, "tracks ": 14, "search_artists ": 6, "search_albums ": 6, "search_tracks ": 14} {
		got := 0
		for _, line := range strings.Split(dump, "\n") {
			if strings.HasPrefix(line, table) {
				got++
			}
		}
		if got != want {
			t.Errorf("%d rows in %s, want %d", got, table, want)
		}
	}
	// Prefix search and diacritics, as §10.1 configures the tables.
	if got := e.match(matchArtists, "echo cafe"); len(got) != 1 {
		t.Errorf("searching \"echo cafe\" finds %v, want Écho Café", got)
	}
}

// Indexing again an album that has not changed starts no process and
// changes nothing, not even a timestamp: the files are, byte for byte,
// those the index knows (F1).
func TestIndexAlbumAgainRunsNothingAndChangesNothing(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	before := e.dump()
	e.media.reset()
	e.warmer.take()

	for range 2 {
		e.indexAll()
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("indexing the same albums again ran %d processes", n)
	}
	e.wantUnchanged(before)
	if got := e.warmer.take(); len(got) != 0 {
		t.Fatalf("covers to warm after indexing the same albums: %v", got)
	}
}

// A render of MusicLib that changes no byte of the files (a forced render,
// a new render_version) gives a new receipt and new files with the same
// content. No process runs; the rows keep what they know of the audio and
// take the new receipt and the new times of the files, which the guard of
// §9.1 compares.
func TestIndexAlbumRenderedAgainWithTheSameBytes(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumA]
	before, beforeTracks := e.album(id), e.tracks(id)
	e.media.reset()
	e.warmer.take()

	later := time.Now().Add(time.Hour).Truncate(time.Second)
	for _, f := range []string{"01 - First Light.flac", "02 - Second Wave.flac", "03 - Third_.flac", "cover.jpg"} {
		if err := os.Chtimes(e.inAlbum(albumA, f), later, later); err != nil {
			t.Fatal(err)
		}
	}
	receipt := rerender(t, e.dir, albumA)
	c := e.candidate(albumA)
	if warnings := e.index(albumA); len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for files the index knows", n)
	}
	if got := e.warmer.take(); len(got) != 0 {
		t.Fatalf("covers to warm: %v; the cover is the same", got)
	}

	after := e.album(id)
	want := before
	want.Album.AlbumRevision, want.Album.ReceiptHash = receipt.AlbumRevision, c.ReceiptHash
	want.Album.CoverMtimeNs = sql.NullInt64{Int64: later.UnixNano(), Valid: true}
	want.Album.UpdatedAt = after.Album.UpdatedAt
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("the album:\n got %+v\nwant %+v", after, want)
	}
	if after.Album.UpdatedAt <= before.Album.UpdatedAt || after.Album.FirstSeenAt != before.Album.FirstSeenAt {
		t.Fatalf("updated at %d (was %d), first seen at %d (was %d)", after.Album.UpdatedAt, before.Album.UpdatedAt,
			after.Album.FirstSeenAt, before.Album.FirstSeenAt)
	}
	for i, row := range e.tracks(id) {
		wantRow := beforeTracks[i]
		wantRow.FileMtimeNs, wantRow.UpdatedAt = later.UnixNano(), after.Album.UpdatedAt
		if row != wantRow {
			t.Fatalf("track %d:\n got %+v\nwant %+v", i, row, wantRow)
		}
	}
}

// A track whose title changes in MusicLib is another file, with another
// name and another SHA-256, and the same audio: it keeps its tracks.id
// (F2), for the four codecs. Only the file that changed is examined.
func TestIndexAlbumKeepsTheIDOfARetitledTrack(t *testing.T) {
	for _, tc := range []struct{ album, file, renamed string }{
		{albumA, "01 - First Light.flac", "01 - A New Dawn.flac"},
		{albumB, "02 - Two.mp3", "02 - A New Dawn.mp3"},
		{albumC, "01 - One.m4a", "01 - A New Dawn.m4a"},
		{albumD, "02 - Two.m4a", "02 - A New Dawn.m4a"},
	} {
		t.Run(tc.album, func(t *testing.T) {
			e := newIndexEnv(t)
			e.indexAll()
			id := fixtureAlbums[tc.album]
			before := e.tracks(id)
			old := e.trackAt(id, tc.file)
			e.media.reset()

			retag(t, e.inAlbum(tc.album, tc.file), "title=A New Dawn")
			rename(t, e.inAlbum(tc.album, tc.file), e.inAlbum(tc.album, tc.renamed))
			rerender(t, e.dir, tc.album)
			if warnings := e.index(tc.album); len(warnings) != 0 {
				t.Fatalf("warnings: %v", warnings)
			}

			if p, f := e.media.probes.Load(), e.media.fingerprints.Load(); p != 1 || f != 1 {
				t.Errorf("%d probes and %d fingerprints, want one of each: one file changed", p, f)
			}
			after := e.tracks(id)
			if len(after) != len(before) {
				t.Fatalf("%d rows, want %d: no row is created or lost", len(after), len(before))
			}
			got := e.trackAt(id, tc.renamed)
			if got.ID != old.ID || got.Seq != old.Seq {
				t.Fatalf("the track has the id %s, and it had %s", got.ID, old.ID)
			}
			if got.Title != "A New Dawn" || got.FileSha256 == old.FileSha256 || got.Fingerprint != old.Fingerprint ||
				got.Occurrence != old.Occurrence || got.Available != 1 || got.UpdatedAt <= old.UpdatedAt {
				t.Fatalf("the row after the edit: %+v", got)
			}
			// The other rows are the rows they were, to the last column.
			for i := range before {
				if before[i].ID != old.ID && after[i] != before[i] {
					t.Errorf("a track that did not change:\n got %+v\nwant %+v", after[i], before[i])
				}
			}
			// The full-text row follows the title.
			e.wantRows(matchTracks, "New Dawn", got.Seq)
			if slices.Contains(e.match(matchTracks, old.Title), got.Seq) {
				t.Errorf("searching the old title %q still finds the track", old.Title)
			}
			if a := e.album(id).Album; a.TrackCount != int64(len(before)) || a.AlbumRevision != 2 {
				t.Errorf("the album has %d tracks at revision %d", a.TrackCount, a.AlbumRevision)
			}
		})
	}
}

// Two tracks that swap their numbers in MusicLib keep their ids: the ids
// follow the audio, not the place (scenario A2 of §12.3).
func TestIndexAlbumSwappedNumbers(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumB]
	one, two := e.trackAt(id, "01 - One.mp3"), e.trackAt(id, "02 - Two.mp3")

	retag(t, e.inAlbum(albumB, "01 - One.mp3"), "track=2/2")
	retag(t, e.inAlbum(albumB, "02 - Two.mp3"), "track=1/2")
	rename(t, e.inAlbum(albumB, "01 - One.mp3"), e.inAlbum(albumB, "tmp"))
	rename(t, e.inAlbum(albumB, "02 - Two.mp3"), e.inAlbum(albumB, "01 - Two.mp3"))
	rename(t, e.inAlbum(albumB, "tmp"), e.inAlbum(albumB, "02 - One.mp3"))
	rerender(t, e.dir, albumB)
	e.index(albumB)

	gotOne, gotTwo := e.trackAt(id, "02 - One.mp3"), e.trackAt(id, "01 - Two.mp3")
	if gotOne.ID != one.ID || gotOne.No != 2 || gotOne.Title != "One" {
		t.Errorf("the track One: %+v, want the id %s at number 2", gotOne, one.ID)
	}
	if gotTwo.ID != two.ID || gotTwo.No != 1 || gotTwo.Title != "Two" {
		t.Errorf("the track Two: %+v, want the id %s at number 1", gotTwo, two.ID)
	}
	if n := len(e.tracks(id)); n != 2 {
		t.Errorf("%d rows, want 2", n)
	}
}

// A track that is deleted in MusicLib keeps its row, which becomes
// unavailable with what was last known of it (I3, scenario A7); the
// counters and the full-text tables forget it. When its file comes back,
// the row is available again with the same id (F1, no process).
func TestIndexAlbumTrackDeletedAndBack(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumA]
	const file = "02 - Second Wave.flac"
	before := e.tracks(id)
	old := e.trackAt(id, file)
	saved := readFile(t, e.inAlbum(albumA, file))
	e.media.reset()

	remove(t, e.inAlbum(albumA, file))
	rerender(t, e.dir, albumA)
	e.index(albumA)

	gone := e.trackAt(id, file)
	want := old
	want.Available, want.UpdatedAt = 0, gone.UpdatedAt
	if gone != want || gone.UpdatedAt <= old.UpdatedAt {
		t.Fatalf("the row of the deleted track:\n got %+v\nwant %+v", gone, want)
	}
	after := e.tracks(id)
	if len(after) != 3 {
		t.Fatalf("%d rows, want 3: a row is never deleted", len(after))
	}
	for i := range before {
		if before[i].ID != old.ID && (after[i].ID != before[i].ID || after[i].Available != 1) {
			t.Errorf("a track that is still there: %+v", after[i])
		}
	}
	if a := e.album(id).Album; a.TrackCount != 2 || a.DurationMs != 4000 || a.Available != 1 {
		t.Fatalf("the album has %d tracks and %d ms", a.TrackCount, a.DurationMs)
	}
	e.wantRows(matchTracks, "Second Wave")
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran, and no file changed", n)
	}

	writeFile(t, e.inAlbum(albumA, file), string(saved))
	rerender(t, e.dir, albumA)
	e.index(albumA)

	back := e.trackAt(id, file)
	if back.ID != old.ID || back.Available != 1 || back.Fingerprint != old.Fingerprint {
		t.Fatalf("the row of the track that came back: %+v, want the id %s", back, old.ID)
	}
	if n := len(e.tracks(id)); n != 3 {
		t.Fatalf("%d rows, want 3", n)
	}
	if a := e.album(id).Album; a.TrackCount != 3 || a.DurationMs != 6000 {
		t.Fatalf("the album has %d tracks and %d ms", a.TrackCount, a.DurationMs)
	}
	e.wantRows(matchTracks, "Second Wave", back.Seq)
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for a file the index knows", n)
	}
}

// Two tracks with the same audio are two rows, told apart by their
// occurrence. When the first is deleted the second keeps its own id
// (scenario A14), and keeps it when MusicLib then renumbers it to the place
// of the first: the row of a track on disk is paired before the row of a
// deleted one.
func TestIndexAlbumTwins(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumF]
	first, second := e.trackAt(id, "01 - Same Audio.flac"), e.trackAt(id, "02 - Same Audio Again.flac")
	if first.Fingerprint != second.Fingerprint || first.Occurrence != 1 || second.Occurrence != 2 || first.ID == second.ID {
		t.Fatalf("the twins: %+v and %+v", first, second)
	}

	remove(t, e.inAlbum(albumF, "01 - Same Audio.flac"))
	rerender(t, e.dir, albumF)
	e.index(albumF)
	if left := e.trackAt(id, "02 - Same Audio Again.flac"); left.ID != second.ID || left.Available != 1 {
		t.Fatalf("the twin that is left: %+v, want the id %s", left, second.ID)
	}
	if gone := e.trackAt(id, "01 - Same Audio.flac"); gone.ID != first.ID || gone.Available != 0 {
		t.Fatalf("the deleted twin: %+v", gone)
	}

	retag(t, e.inAlbum(albumF, "02 - Same Audio Again.flac"), "track=1/1")
	rename(t, e.inAlbum(albumF, "02 - Same Audio Again.flac"), e.inAlbum(albumF, "01 - Same Audio Again.flac"))
	rerender(t, e.dir, albumF)
	e.index(albumF)

	rows := e.tracks(id)
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2", len(rows))
	}
	left := e.trackAt(id, "01 - Same Audio Again.flac")
	if left.ID != second.ID || left.Occurrence != 2 || left.No != 1 || left.Available != 1 {
		t.Fatalf("the twin that is left: %+v, want the id %s", left, second.ID)
	}
	if gone := e.trackAt(id, "01 - Same Audio.flac"); gone.ID != first.ID || gone.Available != 0 {
		t.Fatalf("the deleted twin: %+v", gone)
	}
}

// A new track in an album is a new row with a new id; a third file with
// the audio of two rows takes the first free occurrence (F4).
func TestIndexAlbumNewTracks(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumF]
	before := e.tracks(id)

	twin := readFile(t, e.inAlbum(albumF, "01 - Same Audio.flac"))
	writeFile(t, e.inAlbum(albumF, "03 - Same Audio.flac"), string(twin))
	retag(t, e.inAlbum(albumF, "03 - Same Audio.flac"), "title=Third Twin", "track=3/3")
	other := readFile(t, e.inAlbum(albumA, "03 - Third_.flac"))
	writeFile(t, e.inAlbum(albumF, "04 - Guest.flac"), string(other))
	retag(t, e.inAlbum(albumF, "04 - Guest.flac"), "title=Guest", "track=4/4", "album=Phi Same Audio", "album_artist=Foxtrot Twins")
	rerender(t, e.dir, albumF)
	e.index(albumF)

	rows := e.tracks(id)
	if len(rows) != 4 || rows[0] != before[0] || rows[1] != before[1] {
		t.Fatalf("the rows there were changed: %+v", rows)
	}
	third, guest := e.trackAt(id, "03 - Same Audio.flac"), e.trackAt(id, "04 - Guest.flac")
	if third.Fingerprint != before[0].Fingerprint || third.Occurrence != 3 || third.Title != "Third Twin" || third.No != 3 {
		t.Errorf("the third twin: %+v", third)
	}
	if guest.Occurrence != 1 || guest.No != 4 || guest.ID == third.ID {
		t.Errorf("the new track: %+v", guest)
	}
	// The same audio in two albums is two tracks (§5.4).
	if in := e.trackAt(fixtureAlbums[albumA], "03 - Third_.flac"); in.Fingerprint != guest.Fingerprint || in.ID == guest.ID {
		t.Errorf("the audio of the guest in album A: %+v", in)
	}
	if a := e.album(id).Album; a.TrackCount != 4 || a.DurationMs != 8000 {
		t.Errorf("the album has %d tracks and %d ms", a.TrackCount, a.DurationMs)
	}
}

// A fingerprint that an older ffmpeg computed is still the audio it was: a
// row with another fp_version is paired by its fingerprint (F2) and takes
// the current version. If the fingerprints of the two versions differ, the
// row is paired by its place and its duration (F3), and takes the new
// fingerprint: the id stays in both cases.
func TestIndexAlbumAfterAnotherFFmpeg(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumB]
	one, two := e.trackAt(id, "01 - One.mp3"), e.trackAt(id, "02 - Two.mp3")
	// The index as an older ffmpeg left it: the fingerprint of One is the
	// same in the two versions, that of Two is not.
	e.write(`UPDATE tracks SET fp_version = '7.0-old' WHERE album_id = ?`, id)
	e.write(`UPDATE tracks SET fingerprint = 'another' WHERE id = ?`, two.ID)

	retag(t, e.inAlbum(albumB, "01 - One.mp3"), "title=Uno")
	retag(t, e.inAlbum(albumB, "02 - Two.mp3"), "title=Due")
	rerender(t, e.dir, albumB)
	e.index(albumB)

	gotOne, gotTwo := e.trackAt(id, "01 - One.mp3"), e.trackAt(id, "02 - Two.mp3")
	if gotOne.ID != one.ID || gotOne.Title != "Uno" || gotOne.Fingerprint != one.Fingerprint || gotOne.FpVersion != media.PinnedVersion {
		t.Errorf("the row paired by its fingerprint: %+v", gotOne)
	}
	if gotTwo.ID != two.ID || gotTwo.Title != "Due" || gotTwo.Fingerprint != two.Fingerprint || gotTwo.FpVersion != media.PinnedVersion || gotTwo.Occurrence != 1 {
		t.Errorf("the row paired by its place: %+v", gotTwo)
	}
	if n := len(e.tracks(id)); n != 2 {
		t.Errorf("%d rows, want 2", n)
	}
}

// When the folder of an album is renamed, the album is the same album at
// another path, with the same rows (scenario A4).
func TestIndexAlbumRenamedFolder(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumE]
	before := e.tracks(id)
	e.media.reset()

	const moved = "Écho Café/Epsilon Renamed"
	rename(t, inLib(e.dir, albumE), inLib(e.dir, moved))
	rerender(t, e.dir, moved)
	e.index(moved)

	if a := e.album(id).Album; a.RelPath != moved || a.TrackCount != 3 {
		t.Fatalf("the album: %+v", a)
	}
	after := e.tracks(id)
	for i := range before {
		want := before[i]
		want.FileMtimeNs = after[i].FileMtimeNs
		if after[i] != want {
			t.Errorf("a track of the renamed album:\n got %+v\nwant %+v", after[i], want)
		}
	}
	if n := e.media.runs(); n != 0 {
		t.Errorf("%d processes ran for files the index knows", n)
	}
}

// An album that goes to another artist keeps its id and its tracks. The
// artist it had keeps its row (I3) and can no longer be found, having no
// album; the new artist can (scenarios A5 and A6).
func TestIndexAlbumChangesArtist(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumD]
	before := e.album(id)
	tracks := e.tracks(id)

	for _, f := range []string{"01 - One.m4a", "02 - Two.m4a"} {
		retag(t, e.inAlbum(albumD, f), "album_artist=Omega Pulse", "artist=Omega Pulse")
	}
	rerender(t, e.dir, albumD)
	e.index(albumD)

	after := e.album(id)
	if after.ArtistName != "Omega Pulse" || after.Album.ArtistID != names.ArtistID("Omega Pulse") || after.Album.ArtistID == before.Album.ArtistID ||
		!bytes.Equal(after.Album.ArtistKey, names.SortKey("Omega Pulse")) || after.Album.Seq != before.Album.Seq {
		t.Fatalf("the album after the change: %+v", after)
	}
	for i, row := range e.tracks(id) {
		if row.ID != tracks[i].ID || row.Artist != "Omega Pulse" {
			t.Errorf("track %d: %+v", i, row)
		}
	}
	var old, current store.Artist
	e.read(func(q *store.Queries) (err error) {
		if old, err = q.GetArtist(t.Context(), before.Album.ArtistID); err != nil {
			return err
		}
		current, err = q.GetArtist(t.Context(), after.Album.ArtistID)
		return err
	})
	if old.Name != "Delta Pulse" {
		t.Errorf("the row of the artist the album had: %+v", old)
	}
	e.wantRows(matchArtists, "Omega Pulse", current.Seq)
	e.wantRows(matchArtists, "Delta Pulse")
	e.wantRows(matchAlbums, "Omega", after.Album.Seq)
	e.wantRows(matchAlbums, "Delta Pulse")
}

// Two names with one identity key are one artist, which has the name of
// the album indexed last; the sort key and the full-text rows of its other
// albums follow the name.
func TestIndexAlbumArtistWrittenInAnotherWay(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	a, b := fixtureAlbums[albumA], fixtureAlbums[albumB]
	artistA := e.album(a).Album.ArtistID

	// Album B goes to the artist of album A, written in another case.
	for _, f := range []string{"01 - One.mp3", "02 - Two.mp3"} {
		retag(t, e.inAlbum(albumB, f), "album_artist=AURORA  SINES")
	}
	rerender(t, e.dir, albumB)
	e.index(albumB)

	gotA, gotB := e.album(a), e.album(b)
	if gotB.Album.ArtistID != artistA || gotA.Album.ArtistID != artistA {
		t.Fatalf("the two albums have the artists %s and %s, want %s for both", gotA.Album.ArtistID, gotB.Album.ArtistID, artistA)
	}
	const name = "AURORA  SINES"
	key := names.SortKey(name)
	if bytes.Equal(key, names.SortKey("Aurora Sines")) {
		t.Fatal("the two names have one sort key: the test shows nothing")
	}
	if gotA.ArtistName != name || gotB.ArtistName != name || !bytes.Equal(gotA.Album.ArtistKey, key) || !bytes.Equal(gotB.Album.ArtistKey, key) {
		t.Fatalf("the artist of the two albums: %q with key %x, %q with key %x", gotA.ArtistName, gotA.Album.ArtistKey, gotB.ArtistName, gotB.Album.ArtistKey)
	}
	var stored []string
	e.read(func(q *store.Queries) error {
		rows, err := q.Conn().QueryContext(t.Context(), `SELECT artist FROM search_albums WHERE rowid IN (?, ?) ORDER BY rowid`,
			gotA.Album.Seq, gotB.Album.Seq)
		if err != nil {
			return err
		}
		for rows.Next() {
			var artist string
			if err := rows.Scan(&artist); err != nil {
				return errors.Join(err, rows.Close())
			}
			stored = append(stored, artist)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	if !slices.Equal(stored, []string{name, name}) {
		t.Fatalf("the full-text rows of the two albums hold the artists %q", stored)
	}
	var artist store.Artist
	e.read(func(q *store.Queries) (err error) {
		artist, err = q.GetArtist(t.Context(), artistA)
		return err
	})
	e.wantRows(matchArtists, "aurora sines", artist.Seq)
	// The artist album B had has no album left.
	e.wantRows(matchArtists, "Bravo Tones")
}

// The cover of an album is validated when it changes, and the warmer is
// told of it once the album is committed; an album that loses its cover has
// none in the index.
func TestIndexAlbumCoverChanges(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	e.warmer.take()
	id := fixtureAlbums[albumA]

	// A PNG instead of the JPEG: 8000 x 5000 pixels is exactly the limit.
	png := pngHeader(8000, 5000)
	remove(t, e.inAlbum(albumA, "cover.jpg"))
	writeFile(t, e.inAlbum(albumA, "cover.png"), string(png))
	rerender(t, e.dir, albumA)
	if warnings := e.index(albumA); len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	a := e.album(id).Album
	if a.CoverRel.String != "cover.png" || a.CoverMime.String != "image/png" || a.CoverSha256.String != sha256Hex(png) || a.CoverSize.Int64 != int64(len(png)) {
		t.Fatalf("the cover: %v %v %v %v", a.CoverRel, a.CoverMime, a.CoverSha256, a.CoverSize)
	}
	if got := e.warmer.take(); !slices.Equal(got, []string{sha256Hex(png)}) {
		t.Fatalf("covers to warm: %v", got)
	}

	remove(t, e.inAlbum(albumA, "cover.png"))
	rerender(t, e.dir, albumA)
	e.index(albumA)
	a = e.album(id).Album
	if a.CoverRel.Valid || a.CoverSha256.Valid || a.CoverMime.Valid || a.CoverSize.Valid || a.CoverMtimeNs.Valid {
		t.Fatalf("the album has no cover, and the index says %v", a.CoverRel)
	}
	if got := e.warmer.take(); len(got) != 0 {
		t.Fatalf("covers to warm: %v", got)
	}
	if n := len(e.tracks(id)); n != 3 || a.TrackCount != 3 {
		t.Fatalf("%d rows and %d tracks", n, a.TrackCount)
	}
}

// A cover that is not what its name says, or is too large, does not keep
// the album out of the index: the album has no cover, and a warning says
// why (cover_invalid).
func TestIndexAlbumInvalidCover(t *testing.T) {
	jpeg := readFile(t, filepath.Join(fixtureDir, filepath.FromSlash(albumA), "cover.jpg"))
	for _, tc := range []struct {
		name, file string
		content    []byte
	}{
		{"a PNG named cover.jpg", "cover.jpg", pngHeader(16, 16)},
		{"a JPEG named cover.png", "cover.png", jpeg},
		{"text", "cover.jpg", []byte("not an image at all")},
		{"an empty file", "cover.png", nil},
		{"a truncated JPEG", "cover.jpg", jpeg[:20]},
		{"more than 40 megapixels", "cover.png", pngHeader(8000, 5001)},
		{"a huge PNG", "cover.png", pngHeader(1<<31-1, 1<<31-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newIndexEnv(t)
			remove(t, e.inAlbum(albumA, "cover.jpg"))
			writeFile(t, e.inAlbum(albumA, tc.file), string(tc.content))
			rerender(t, e.dir, albumA)

			warnings := e.index(albumA)
			if len(warnings) != 1 || warnings[0].Code != CodeCoverInvalid || warnings[0].RelPath != albumA || warnings[0].Message == "" {
				t.Fatalf("warnings: %+v, want one cover_invalid", warnings)
			}
			if strings.Contains(warnings[0].Message, e.dir) {
				t.Fatalf("the warning holds an absolute path: %q", warnings[0].Message)
			}
			a := e.album(fixtureAlbums[albumA]).Album
			if a.CoverRel.Valid || a.CoverSha256.Valid || a.CoverMime.Valid || a.CoverSize.Valid || a.CoverMtimeNs.Valid || a.TrackCount != 3 {
				t.Fatalf("the album: %+v", a)
			}
			if got := e.warmer.take(); len(got) != 0 {
				t.Fatalf("covers to warm: %v", got)
			}
			// A cover that was refused is looked at again, and warned
			// about again, when the album is.
			if again := e.index(albumA); len(again) != 1 || again[0].Code != CodeCoverInvalid {
				t.Fatalf("warnings of the second indexing: %+v", again)
			}
		})
	}
}

// A track without tags does not keep its album out of the index (§5.3):
// its title and its number come from the name of its file, its disc from
// its folder, its artist from the album, and a warning says so
// (tags_incomplete).
func TestIndexAlbumTrackWithoutTags(t *testing.T) {
	e := newIndexEnv(t)
	const file = "Disc 2/01 - Disc Two Track One.flac"
	const renamed = "Disc 2/07 - Seventh  of Two.flac"
	untagged(t, e.inAlbum(albumE, file))
	rename(t, e.inAlbum(albumE, file), e.inAlbum(albumE, renamed))
	rerender(t, e.dir, albumE)

	warnings := e.index(albumE)
	if len(warnings) != 1 || warnings[0].Code != CodeTagsIncomplete || warnings[0].RelPath != albumE ||
		!strings.Contains(warnings[0].Message, renamed) || !strings.HasPrefix(warnings[0].Message, "1 of the tracks") {
		t.Fatalf("warnings: %+v, want one tags_incomplete that names the file", warnings)
	}
	id := fixtureAlbums[albumE]
	got := e.trackAt(id, renamed)
	if got.Title != "Seventh  of Two" || got.Artist != "Écho Café" || got.Disc != 2 || got.No != 7 || got.Genre.Valid {
		t.Fatalf("the track without tags: %+v", got)
	}
	if got.Fingerprint != "1016939daf7ed11d08e9e3a462026d17781d990345edb99d7a8d9ed848fb5045" {
		t.Fatalf("the fingerprint of the track changed with its tags: %s", got.Fingerprint)
	}
	a := e.album(id)
	if a.Album.Title != "Epsilon Discs" || a.ArtistName != "Écho Café" || a.Album.Year.Int64 != 2005 || a.Album.TrackCount != 3 {
		t.Fatalf("the album: %+v", a)
	}
}

// An album whose tracks say nothing has the names of §5.3 and no year, and
// its tracks have what their paths say.
func TestIndexAlbumWithoutAnyTag(t *testing.T) {
	e := newIndexEnv(t)
	untagged(t, e.inAlbum(albumB, "01 - One.mp3"))
	untagged(t, e.inAlbum(albumB, "02 - Two.mp3"))
	rename(t, e.inAlbum(albumB, "02 - Two.mp3"), e.inAlbum(albumB, "Two.mp3"))
	rerender(t, e.dir, albumB)

	warnings := e.index(albumB)
	if len(warnings) != 1 || warnings[0].Code != CodeTagsIncomplete || !strings.HasPrefix(warnings[0].Message, "2 of the tracks") {
		t.Fatalf("warnings: %+v", warnings)
	}
	id := fixtureAlbums[albumB]
	a := e.album(id)
	if a.Album.Title != "Unknown Album" || a.ArtistName != "Unknown Artist" || a.Album.Year.Valid || a.Album.YearKey != 10000 || a.Album.Genre.Valid {
		t.Fatalf("the album: %+v", a)
	}
	one, two := e.trackAt(id, "01 - One.mp3"), e.trackAt(id, "Two.mp3")
	if one.Title != "One" || one.No != 1 || one.Disc != 1 || one.Artist != "Unknown Artist" {
		t.Errorf("the first track: %+v", one)
	}
	if two.Title != "Two" || two.No != 1 || two.Disc != 1 {
		t.Errorf("the track whose name has no number: %+v", two)
	}
}

// An album whose receipt lists no track is indexed as it is: the rows it
// had become unavailable, and none is lost.
func TestIndexAlbumWithoutTracks(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	id := fixtureAlbums[albumB]
	before := e.tracks(id)
	remove(t, e.inAlbum(albumB, "01 - One.mp3"))
	remove(t, e.inAlbum(albumB, "02 - Two.mp3"))
	rerender(t, e.dir, albumB)

	if warnings := e.index(albumB); len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	after := e.tracks(id)
	if len(after) != 2 || after[0].ID != before[0].ID || after[1].ID != before[1].ID || after[0].Available != 0 || after[1].Available != 0 {
		t.Fatalf("the rows of the album: %+v", after)
	}
	a := e.album(id)
	if a.Album.TrackCount != 0 || a.Album.DurationMs != 0 || a.Album.Available != 1 || a.Album.Title != "Unknown Album" || a.ArtistName != "Unknown Artist" {
		t.Fatalf("the album: %+v", a)
	}
	e.wantRows(matchTracks, "Beta MP3")
	e.wantRows(matchArtists, "Bravo Tones")
}

// The name of a track file, read where a tag is missing.
func TestNameParts(t *testing.T) {
	for _, tc := range []struct {
		path  string
		no    int
		title string
	}{
		{"01 - So What.flac", 1, "So What"},
		{"Disc 2/12 - Title.mp3", 12, "Title"},
		{"999 - Last.m4a", 999, "Last"},
		{"1000 - Too Far.flac", 0, "Too Far"},
		{"00 - Zero.flac", 0, "Zero"},
		{"7 - Seven.flac", 7, "Seven"},
		{"Title.flac", 0, "Title"},
		{"01 Title.flac", 0, "01 Title"},
		{"01 -Title.flac", 0, "01 -Title"},
		{" - Title.flac", 0, "- Title"},
		{"01 - .flac", 1, "01 -"},
		{"01 - A - B.flac", 1, "A - B"},
		{"03 - Cafe\u0301.flac", 3, "Caf\u00e9"},
		{"99999999999999999999 - Huge.flac", 0, "Huge"},
		{".flac", 0, ".flac"},
	} {
		if no, title := nameParts(tc.path); no != tc.no || title != tc.title {
			t.Errorf("nameParts(%q) = %d, %q; want %d, %q", tc.path, no, title, tc.no, tc.title)
		}
	}
}

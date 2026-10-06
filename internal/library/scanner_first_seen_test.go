package library

import (
	"bytes"
	"testing"

	"vibrance/internal/names"
)

// The columns of the list of the tracks (GET /tracks,
// docs/proposals/web-client-api.md A1, step W1 of DESIGN.md): the sort keys
// of each row, and the moment Vibrance first saw its audio, which a track
// that MusicLib moves to another album keeps from the row it replaces (the
// same pairing as "the references follow the audio"), at the end of the
// cycle, with or without references.

// A track moved to another album keeps the moment of the track it replaces,
// whichever album is indexed first; a second cycle writes nothing.
func TestFirstSeenFollowsAMovedTrack(t *testing.T) {
	for _, tc := range []struct{ name, toRel, toFile string }{
		{"the destination is indexed first", albumA, "04 - One.mp3"},
		{"the source is indexed first", albumD, "03 - One.mp3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newScanEnv(t)
			e.scan()
			old := e.track(e.ids(fixtureAlbums[albumB])[bOne])
			e.moveTrack(albumB, bOne, tc.toRel, tc.toFile, "title=Uno")
			wantEvent(t, e.scan(), map[string]any{"indexed": 2, "references_moved": 0})
			now := e.twin(fixtureAlbums[tc.toRel], old.ID)
			if now.FirstSeenAt != old.FirstSeenAt || now.ID == old.ID {
				t.Fatalf("the moved track %s was first seen at %d, the track it replaces %s at %d", now.ID, now.FirstSeenAt, old.ID, old.FirstSeenAt)
			}
			if gone := e.track(old.ID); gone.FirstSeenAt != old.FirstSeenAt || gone.Available != 0 {
				t.Fatalf("the row of the old album: %+v", gone)
			}
			after := e.dumpAll()
			wantEvent(t, e.scan(), map[string]any{"indexed": 0, "references_moved": 0})
			e.wantAllUnchanged(after)
		})
	}
}

// A cycle that sees the audio in both albums gives the new row the moment
// of the cycle, as to any new track: the old row is still there. When the
// old one is gone, the new one takes its moment.
func TestFirstSeenOfATrackSeenInBothAlbums(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	old := e.track(e.ids(fixtureAlbums[albumB])[bOne])
	held := e.outside("copy.mp3")
	writeFile(t, held, string(readFile(t, e.inAlbum(albumB, bOne))))
	e.putTrack(held, albumA, "04 - One.mp3", "title=Uno")
	wantEvent(t, e.scan(), map[string]any{"indexed": 1})
	now := e.twin(fixtureAlbums[albumA], old.ID)
	if now.FirstSeenAt <= old.FirstSeenAt || now.FirstSeenAt <= e.album(fixtureAlbums[albumA]).Album.FirstSeenAt {
		t.Fatalf("a new row of an album seen before: first seen at %d; the old one at %d", now.FirstSeenAt, old.FirstSeenAt)
	}

	e.takeTrack(albumB, bOne)
	wantEvent(t, e.scan(), map[string]any{"indexed": 1})
	if got := e.track(now.ID); got.FirstSeenAt != old.FirstSeenAt {
		t.Fatalf("the track left was first seen at %d, want %d", got.FirstSeenAt, old.FirstSeenAt)
	}
}

// Two copies of an album: when the older one goes, the tracks of the copy
// that is left take its moment; when the newer one goes, the tracks left
// keep theirs, which is earlier.
func TestFirstSeenKeepsTheEarlierOfTwoCopies(t *testing.T) {
	const copyRel, copyID = "Bravo Tones/Beta Copy", "01a0f459-ebc8-7081-991c-2b1c9331e157"
	for _, tc := range []struct {
		name  string
		gone  string
		older bool
	}{
		{"the older copy goes", albumB, true},
		{"the newer copy goes", copyRel, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newScanEnv(t)
			e.scan()
			b := e.ids(fixtureAlbums[albumB])
			copyAlbum(t, e.dir, albumB, copyRel, copyID)
			wantEvent(t, e.scan(), map[string]any{"indexed": 1})
			// The original and the copy of each track, as they were.
			var pairs [][2]int64
			var ids [][2]string
			for _, file := range []string{bOne, bTwo} {
				original, copied := e.track(b[file]), e.twin(copyID, b[file])
				if copied.FirstSeenAt <= original.FirstSeenAt {
					t.Fatalf("%s: the copy was first seen at %d, the original at %d", file, copied.FirstSeenAt, original.FirstSeenAt)
				}
				pairs = append(pairs, [2]int64{original.FirstSeenAt, copied.FirstSeenAt})
				ids = append(ids, [2]string{original.ID, copied.ID})
			}

			e.trash(tc.gone)
			wantEvent(t, e.scan(), map[string]any{"absent": 1})
			for i, id := range ids {
				original, copied := e.track(id[0]), e.track(id[1])
				// The original keeps its moment either way; the copy takes
				// the earlier one only if the original is gone.
				want := pairs[i][1]
				if tc.older {
					want = pairs[i][0]
				}
				if original.FirstSeenAt != pairs[i][0] || copied.FirstSeenAt != want {
					t.Fatalf("track %d: first seen at %d and %d, want %d and %d", i, original.FirstSeenAt, copied.FirstSeenAt, pairs[i][0], want)
				}
			}
		})
	}
}

// Every row of an album has the key of the title of the album, the rows
// that are no longer available too, and each row the keys of its own title
// and artist (the artist of the track, not of the album).
func TestSortKeysOfTheTracks(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumC]
	e.takeTrack(albumC, cTwo)
	retag(t, e.inAlbum(albumC, cOne), "album=Zeta", "title=Uno", "artist=Somebody Else")
	rerender(t, e.dir, albumC)
	wantEvent(t, e.scan(), map[string]any{"indexed": 1})

	album := e.album(id).Album
	if album.Title != "Zeta" || !bytes.Equal(album.TitleKey, names.SortKey("Zeta")) {
		t.Fatalf("the album: %+v", album)
	}
	rows := e.tracks(id)
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, row := range rows {
		if !bytes.Equal(row.AlbumKey, album.TitleKey) || !bytes.Equal(row.TitleKey, names.SortKey(row.Title)) ||
			!bytes.Equal(row.ArtistKey, names.SortKey(row.Artist)) {
			t.Errorf("the keys of %q by %q (available %d): %x %x %x", row.Title, row.Artist, row.Available, row.TitleKey, row.ArtistKey, row.AlbumKey)
		}
		if row.Available == 1 && (row.Title != "Uno" || row.Artist != "Somebody Else") {
			t.Errorf("the track left: %q by %q", row.Title, row.Artist)
		}
	}
}

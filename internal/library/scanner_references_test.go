package library

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"vibrance/internal/store"
)

// The references of the users follow the audio (DESIGN.md, erratum of
// 2026-10-02). MusicLib 1.2.0 moves a track from an album to another: for
// Vibrance the track of the old album is gone and a track with the same
// audio is new in the other album. At the end of every cycle the playlist
// items and the favorites of a track that is not available move to an
// available track with its fingerprint. Each test starts from playlists and
// favorites made before the library changes.

// The files of the fixture these tests move.
const (
	bOne = "01 - One.mp3"
	bTwo = "02 - Two.mp3"
	cOne = "01 - One.m4a"
	cTwo = "02 - Two.m4a"
)

// refEnv is a scanned fixture library with two users, their playlists and
// their favorites on tracks of the albums B and C.
type refEnv struct {
	*scanEnv
	ann, bob string
	// The tracks of B and of C, by their file.
	b, c map[string]string
	// annList has the first track of B between two tracks of C; bobList has
	// it twice, with the second of B.
	annList, bobList   string
	annItems, bobItems []item
	// other is a playlist of ann that has no track of B.
	other string
}

func newRefEnv(t *testing.T) *refEnv {
	t.Helper()
	e := &refEnv{scanEnv: newScanEnv(t)}
	e.scan()
	e.ann, e.bob = e.user("ann"), e.user("bob")
	e.b, e.c = e.ids(fixtureAlbums[albumB]), e.ids(fixtureAlbums[albumC])
	e.annList = e.playlist(e.ann, "Ann's", e.c[cOne], e.b[bOne], e.c[cTwo])
	e.bobList = e.playlist(e.bob, "Bob's", e.b[bOne], e.b[bTwo], e.b[bOne])
	e.other = e.playlist(e.ann, "Other", e.c[cTwo])
	e.annItems, e.bobItems = e.items(e.annList), e.items(e.bobList)
	e.favorite(e.ann, e.b[bOne], 100)
	e.favorite(e.ann, e.c[cOne], 110)
	e.favorite(e.bob, e.b[bOne], 200)
	e.favorite(e.bob, e.b[bTwo], 210)
	return e
}

// twin is the available track of an album with the fingerprint of a track,
// which must be there once.
func (e *scanEnv) twin(albumID, trackID string) store.Track {
	e.t.Helper()
	fingerprint := e.track(trackID).Fingerprint
	var found []store.Track
	for _, row := range e.tracks(albumID) {
		if row.Fingerprint == fingerprint && row.Available == 1 && row.ID != trackID {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		e.t.Fatalf("the album %s has %d other available tracks with the audio of %s, want 1", albumID, len(found), trackID)
	}
	return found[0]
}

// wantFirstOfBMoved checks what the users have once the first track of B
// is the track now: the items kept their id, their position and added_at,
// each playlist that had the track got one revision, and the favorites kept
// their moment.
func (e *refEnv) wantFirstOfBMoved(now string) {
	e.t.Helper()
	e.wantItems(e.annList, e.annItems, e.c[cOne], now, e.c[cTwo])
	e.wantItems(e.bobList, e.bobItems, now, e.b[bTwo], now)
	e.wantRevision(e.annList, 2)
	e.wantRevision(e.bobList, 2)
	e.wantRevision(e.other, 1)
	e.wantFavorites(e.ann, sorted(now+"@100", e.c[cOne]+"@110")...)
	e.wantFavorites(e.bob, sorted(now+"@200", e.b[bTwo]+"@210")...)
}

// wantNothingMoved checks that the users have what they had.
func (e *refEnv) wantNothingMoved() {
	e.t.Helper()
	e.wantItems(e.annList, e.annItems, e.c[cOne], e.b[bOne], e.c[cTwo])
	e.wantItems(e.bobList, e.bobItems, e.b[bOne], e.b[bTwo], e.b[bOne])
	for _, playlist := range []string{e.annList, e.bobList, e.other} {
		e.wantRevision(playlist, 1)
	}
	e.wantFavorites(e.ann, sorted(e.b[bOne]+"@100", e.c[cOne]+"@110")...)
	e.wantFavorites(e.bob, sorted(e.b[bOne]+"@200", e.b[bTwo]+"@210")...)
}

func sorted(s ...string) []string {
	slices.Sort(s)
	return s
}

// A track moved to another album, with another title and another artist:
// the playlist item stays where it was and shows the new data, and the
// favorite stays (scenario A17). The row of the old album stays, unavailable
// and without references; a second cycle writes nothing.
func TestReferencesFollowATrackMovedToAnotherAlbum(t *testing.T) {
	// The destination is indexed before the source (album A comes before B
	// in the order of the work) or after it (album C after B): the rule
	// reads the index when both are written, so the order does not matter.
	for _, tc := range []struct{ name, toRel, toFile string }{
		{"the destination is indexed first", albumA, "04 - One.mp3"},
		{"the source is indexed first", albumD, "03 - One.mp3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRefEnv(t)
			e.sc = e.scanner(1, time.Hour)
			old := e.track(e.b[bOne])
			e.moveTrack(albumB, bOne, tc.toRel, tc.toFile, "title=Uno", "artist=Somebody Else")

			wantEvent(t, e.scan(), map[string]any{"indexed": 2, "absent": 0, "references_moved": 1})
			now := e.twin(fixtureAlbums[tc.toRel], old.ID)
			if now.Title != "Uno" || now.Artist != "Somebody Else" || now.AlbumID != fixtureAlbums[tc.toRel] || now.ID == old.ID {
				t.Fatalf("the track in its new album: %+v", now)
			}
			e.wantFirstOfBMoved(now.ID)
			// The old row is still there, as it was but for its availability
			// (I3): its id can still be read.
			gone := e.track(old.ID)
			want := old
			want.Available, want.UpdatedAt = 0, gone.UpdatedAt
			if gone != want {
				t.Fatalf("the row of the old album:\n got %+v\nwant %+v", gone, want)
			}
			// The scanner gave the revisions the time of its clock.
			if _, at := e.revision(e.annList); at < clockStart {
				t.Fatalf("the playlist was updated at %d", at)
			}

			after := e.dumpAll()
			wantEvent(t, e.scan(), map[string]any{"indexed": 0, "references_moved": 0})
			e.wantAllUnchanged(after)
		})
	}
}

// MusicLib publishes the two albums of a move one after the other, in no
// promised order: a cycle in between sees the track in both albums, or in
// neither. In both cases the references end on the track of the new album,
// and nothing moves before the old one is gone.
func TestReferencesFollowATrackSeenInBothAlbumsOrInNeither(t *testing.T) {
	t.Run("in both", func(t *testing.T) {
		e := newRefEnv(t)
		held := e.outside("copy.mp3")
		writeFile(t, held, string(readFile(t, e.inAlbum(albumB, bOne))))
		e.putTrack(held, albumA, "04 - One.mp3", "title=Uno")
		wantEvent(t, e.scan(), map[string]any{"indexed": 1, "references_moved": 0})
		// The track of B is still there: its references are its own.
		e.wantNothingMoved()
		now := e.twin(fixtureAlbums[albumA], e.b[bOne])

		e.takeTrack(albumB, bOne)
		wantEvent(t, e.scan(), map[string]any{"indexed": 1, "references_moved": 1})
		e.wantFirstOfBMoved(now.ID)
	})
	t.Run("in neither", func(t *testing.T) {
		e := newRefEnv(t)
		held := e.takeTrack(albumB, bOne)
		wantEvent(t, e.scan(), map[string]any{"indexed": 1, "references_moved": 0})
		// The track is nowhere: the references stay on its row, in grey.
		e.wantNothingMoved()
		if row := e.track(e.b[bOne]); row.Available != 0 {
			t.Fatalf("the row of the track that is nowhere: %+v", row)
		}

		e.putTrack(held, albumA, "04 - One.mp3", "title=Uno")
		wantEvent(t, e.scan(), map[string]any{"indexed": 1, "references_moved": 1})
		e.wantFirstOfBMoved(e.twin(fixtureAlbums[albumA], e.b[bOne]).ID)
	})
}

// Every track of an album moved: the album leaves the library, and each of
// its tracks is followed (scenario A18).
func TestReferencesFollowEveryTrackOfAnAlbum(t *testing.T) {
	e := newRefEnv(t)
	e.moveTrack(albumB, bOne, albumA, "04 - One.mp3", "title=Uno")
	e.moveTrack(albumB, bTwo, albumA, "05 - Two.mp3", "title=Due")

	wantEvent(t, e.scan(), map[string]any{"discovered": 5, "indexed": 1, "absent": 1, "references_moved": 2})
	if a := e.album(fixtureAlbums[albumB]).Album; a.Available != 0 {
		t.Fatalf("the album left without tracks: %+v", a)
	}
	one, two := e.twin(fixtureAlbums[albumA], e.b[bOne]), e.twin(fixtureAlbums[albumA], e.b[bTwo])
	if one.Title != "Uno" || two.Title != "Due" {
		t.Fatalf("the tracks in their new album: %+v and %+v", one, two)
	}
	e.wantItems(e.annList, e.annItems, e.c[cOne], one.ID, e.c[cTwo])
	e.wantItems(e.bobList, e.bobItems, one.ID, two.ID, one.ID)
	// One revision for a playlist, however many of its items moved.
	e.wantRevision(e.annList, 2)
	e.wantRevision(e.bobList, 2)
	e.wantRevision(e.other, 1)
	e.wantFavorites(e.ann, sorted(one.ID+"@100", e.c[cOne]+"@110")...)
	e.wantFavorites(e.bob, sorted(one.ID+"@200", two.ID+"@210")...)
	if got := e.rows(); got != (rowCounts{6, 6, 16}) {
		t.Fatalf("rows %+v: the rows of the old album stay", got)
	}
}

// Two tracks swapped between two albums: both are followed (scenario A19).
func TestReferencesFollowASwapBetweenTwoAlbums(t *testing.T) {
	e := newRefEnv(t)
	fromB, fromC := e.takeTrack(albumB, bOne), e.takeTrack(albumC, cOne)
	e.putTrack(fromB, albumC, "01 - One.mp3")
	e.putTrack(fromC, albumB, "01 - One.m4a")

	wantEvent(t, e.scan(), map[string]any{"indexed": 2, "absent": 0, "references_moved": 2})
	inC, inB := e.twin(fixtureAlbums[albumC], e.b[bOne]), e.twin(fixtureAlbums[albumB], e.c[cOne])
	e.wantItems(e.annList, e.annItems, inB.ID, inC.ID, e.c[cTwo])
	e.wantItems(e.bobList, e.bobItems, inC.ID, e.b[bTwo], inC.ID)
	e.wantRevision(e.annList, 2)
	e.wantRevision(e.bobList, 2)
	e.wantRevision(e.other, 1)
	e.wantFavorites(e.ann, sorted(inC.ID+"@100", inB.ID+"@110")...)
	e.wantFavorites(e.bob, sorted(inC.ID+"@200", e.b[bTwo]+"@210")...)
}

// An album that is deleted for good and imported again comes back with
// another album_id: other rows, and the references move to them (scenario
// A20). It does not matter which ffmpeg computed the fingerprint of the old
// rows: two equal fingerprints are the same audio.
func TestReferencesFollowAnAlbumThatComesBackWithAnotherID(t *testing.T) {
	e := newRefEnv(t)
	e.write(`UPDATE tracks SET fp_version = '7.0-old' WHERE album_id = ?`, fixtureAlbums[albumB])
	trash := e.trash(albumB)
	wantEvent(t, e.scan(), map[string]any{"absent": 1, "references_moved": 0})
	// Nothing has that audio: the references stay where they are, in grey.
	e.wantNothingMoved()

	const again, againID = "Bravo Tones/Beta MP3", "01a0f459-ebc8-7081-991c-2b1c9331e199"
	rename(t, trash, inLib(e.dir, again))
	receipt, err := ParseReceipt(readFile(t, inLib(e.dir, again+"/"+ReceiptName)))
	if err != nil {
		t.Fatal(err)
	}
	receipt.AlbumID = againID
	writeFile(t, inLib(e.dir, again+"/"+ReceiptName), string(encodeReceipt(receipt)))

	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "absent": 0, "references_moved": 2})
	one, two := e.twin(againID, e.b[bOne]), e.twin(againID, e.b[bTwo])
	e.wantItems(e.annList, e.annItems, e.c[cOne], one.ID, e.c[cTwo])
	e.wantItems(e.bobList, e.bobItems, one.ID, two.ID, one.ID)
	e.wantFavorites(e.ann, sorted(one.ID+"@100", e.c[cOne]+"@110")...)
	e.wantFavorites(e.bob, sorted(one.ID+"@200", two.ID+"@210")...)
	if a := e.album(fixtureAlbums[albumB]).Album; a.Available != 0 {
		t.Fatalf("the old album: %+v", a)
	}

	// The old album restored later does not take the references back: they
	// stay on the same audio, where they are.
	copyAlbum(t, e.dir, again, "Bravo Tones/Beta Restored", fixtureAlbums[albumB])
	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "references_moved": 0})
	if row := e.track(e.b[bOne]); row.Available != 1 {
		t.Fatalf("the old row after the album was restored: %+v", row)
	}
	e.wantItems(e.annList, e.annItems, e.c[cOne], one.ID, e.c[cTwo])
	e.wantRevision(e.annList, 2)
	e.wantFavorites(e.ann, sorted(one.ID+"@100", e.c[cOne]+"@110")...)
}

// The same audio in two albums, and one of them is deleted: the references
// go to the copy that is left. A user that already had the copy among its
// favorites keeps that favorite, one, with its own moment.
func TestReferencesFollowTheCopyThatIsLeft(t *testing.T) {
	e := newRefEnv(t)
	const copyRel, copyID = "Bravo Tones/Beta Copy", "01a0f459-ebc8-7081-991c-2b1c9331e157"
	copyAlbum(t, e.dir, albumB, copyRel, copyID)
	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "references_moved": 0})
	one, two := e.twin(copyID, e.b[bOne]), e.twin(copyID, e.b[bTwo])
	// Both copies are available: nobody's references move.
	e.wantNothingMoved()
	// ann has both copies of the first track among her favorites, bob has
	// both copies of the second.
	e.favorite(e.ann, one.ID, 300)
	e.favorite(e.bob, two.ID, 5)

	e.trash(albumB)
	wantEvent(t, e.scan(), map[string]any{"absent": 1, "references_moved": 2})
	e.wantItems(e.annList, e.annItems, e.c[cOne], one.ID, e.c[cTwo])
	e.wantItems(e.bobList, e.bobItems, one.ID, two.ID, one.ID)
	e.wantFavorites(e.ann, sorted(one.ID+"@300", e.c[cOne]+"@110")...)
	e.wantFavorites(e.bob, sorted(one.ID+"@200", two.ID+"@5")...)
	var left int
	e.read(func(q *store.Queries) error {
		return q.Conn().QueryRowContext(t.Context(), `SELECT count(*) FROM favorites WHERE track_id IN (?, ?)`, e.b[bOne], e.b[bTwo]).Scan(&left)
	})
	if left != 0 {
		t.Fatalf("%d favorites still point to the tracks that are gone", left)
	}
}

// With several available tracks of that audio, the references go to the one
// of the same album; without one, to the track created last, which is the
// one that was just moved. The choice does not depend on anything else.
func TestReferencesChooseAmongSeveralTracks(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	idF := fixtureAlbums[albumF]
	const first, second = "01 - Same Audio.flac", "02 - Same Audio Again.flac"
	twins := e.ids(idF)
	// The same audio in an older album and in a newer one.
	const olderRel, olderID = "Alfa/Older", "01a0f459-ec9d-7dc9-825a-074f90185a01"
	const newerRel, newerID = "Zulu/Newer", "01a0f459-ec9d-7dc9-825a-074f90185a02"
	copyAlbum(t, e.dir, albumF, olderRel, olderID)
	e.scan()
	copyAlbum(t, e.dir, albumF, newerRel, newerID)
	e.scan()
	ann := e.user("ann")
	list := e.playlist(ann, "Twins", twins[first], twins[second])
	items := e.items(list)
	e.favorite(ann, twins[first], 100)

	// The first twin is deleted: its references go to its twin of the same
	// album, although the tracks of the other albums are newer.
	remove(t, e.inAlbum(albumF, first))
	rerender(t, e.dir, albumF)
	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "references_moved": 1})
	e.wantItems(list, items, twins[second], twins[second])
	e.wantRevision(list, 2)
	e.wantFavorites(ann, twins[second]+"@100")

	// The album is deleted: the references go to the track created last,
	// in whatever album it is.
	var last string
	e.read(func(q *store.Queries) error {
		return q.Conn().QueryRowContext(t.Context(),
			`SELECT id FROM tracks WHERE fingerprint = ? AND available = 1 ORDER BY seq DESC LIMIT 1`, e.track(twins[first]).Fingerprint).Scan(&last)
	})
	if e.track(last).AlbumID != newerID {
		t.Fatalf("the track created last is of the album %s", e.track(last).AlbumID)
	}
	e.trash(albumF)
	wantEvent(t, e.scan(), map[string]any{"absent": 1, "references_moved": 1})
	e.wantItems(list, items, last, last)
	e.wantRevision(list, 3)
	e.wantFavorites(ann, last+"@100")

	// The newest album is deleted too: the older one is what is left.
	e.trash(newerRel)
	wantEvent(t, e.scan(), map[string]any{"absent": 1, "references_moved": 1})
	var left string
	e.read(func(q *store.Queries) error {
		return q.Conn().QueryRowContext(t.Context(),
			`SELECT id FROM tracks WHERE album_id = ? ORDER BY seq DESC LIMIT 1`, olderID).Scan(&left)
	})
	e.wantItems(list, items, left, left)
	e.wantRevision(list, 4)
	e.wantFavorites(ann, left+"@100")
}

// Without an available track of that audio nothing changes, even when
// another track that is not available has it: the references stay on the
// row they are on, which shows in grey.
func TestReferencesStayWithoutAnAvailableTrack(t *testing.T) {
	e := newRefEnv(t)
	const copyRel, copyID = "Bravo Tones/Beta Copy", "01a0f459-ebc8-7081-991c-2b1c9331e157"
	copyAlbum(t, e.dir, albumB, copyRel, copyID)
	e.scan()
	e.trash(copyRel)
	e.scan()
	e.wantNothingMoved()
	before := e.dumpAll()

	// Now both albums with that audio are gone.
	e.trash(albumB)
	wantEvent(t, e.scan(), map[string]any{"absent": 1, "references_moved": 0})
	e.wantNothingMoved()
	if row := e.track(e.b[bOne]); row.Available != 0 {
		t.Fatalf("the row: %+v", row)
	}
	// Only the index changed.
	after := e.dumpAll()
	if users := func(dump string) string { return dump[indexOf(dump, "playlists "):] }; users(after) != users(before) {
		t.Fatalf("the tables of the users changed:\n%s\nwere:\n%s", users(after), users(before))
	}
	e.scan()
	e.wantAllUnchanged(after)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub && (i == 0 || s[i-1] == '\n') {
			return i
		}
	}
	return len(s)
}

// A stop between the commit of the albums and the end of the cycle leaves
// the references on the old rows; the first cycle after the restart moves
// them, although it has nothing to index.
func TestReferencesMoveAfterAStopBeforeTheEndOfTheCycle(t *testing.T) {
	e := newRefEnv(t)
	e.moveTrack(albumB, bOne, albumA, "04 - One.mp3", "title=Uno")
	// An album that comes last in the order of the work: the cycle is
	// stopped while it is examined, after A and B were committed.
	copyAlbum(t, e.dir, albumC, "Zulu/Last", "01a0f459-ebc2-7821-a442-bd56dc181ce4")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e.media.reset()
	e.media.setHook(func(kind string, n int64) {
		// The first probe is the file that came to A.
		if kind == "probe" && n == 2 {
			cancel()
		}
	})
	sc := e.scanner(1, time.Hour)
	sc.cycle(ctx, ReasonRequest)
	if st := sc.Status(); st.LastScan.OK || st.LastScan.Error != messageInterrupted {
		t.Fatalf("the cycle that was stopped: %s", statusText(st))
	}
	now := e.twin(fixtureAlbums[albumA], e.b[bOne])
	if row := e.track(e.b[bOne]); row.Available != 0 || now.Available != 1 {
		t.Fatalf("the two albums were not committed before the stop: %+v, %+v", row, now)
	}
	e.wantNothingMoved()

	e.media.setHook(nil)
	e.sc = e.scanner(testWorkers, time.Hour)
	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "references_moved": 1})
	e.wantFirstOfBMoved(now.ID)

	// The same when the albums were committed and the cycle never came: a
	// cycle that changes nothing still moves the references.
	e2 := newRefEnv(t)
	e2.moveTrack(albumB, bOne, albumA, "04 - One.mp3", "title=Uno")
	e2.index(albumA)
	e2.index(albumB)
	e2.wantNothingMoved()
	wantEvent(t, e2.scan(), map[string]any{"indexed": 0, "references_moved": 1})
	e2.wantFirstOfBMoved(e2.twin(fixtureAlbums[albumA], e2.b[bOne]).ID)
}

// The rule is one short write transaction that only reads and writes the
// database, and it is all or nothing: when it fails nothing has moved, and
// the cycle says that it did not go to its end.
func TestReferencesMoveAllOrNothing(t *testing.T) {
	e := newRefEnv(t)
	e.moveTrack(albumB, bOne, albumA, "04 - One.mp3")
	e.moveTrack(albumB, bTwo, albumA, "05 - Two.mp3")
	// The database refuses the last write of the transaction: the new
	// revision of a playlist.
	e.write(`CREATE TRIGGER refuse BEFORE UPDATE OF revision ON playlists WHEN old.id = '` + e.bobList + `'
		BEGIN SELECT RAISE(ABORT, 'refused by the test'); END`)

	st := e.cycle()
	if st.LastScan.OK || st.LastScan.Error != messageInternal || st.State != StateIdle {
		t.Fatalf("the cycle whose last step failed: %s", statusText(st))
	}
	e.wantNothingMoved()
	failures := e.logs.events(t, "the scan failed: moving the references of the users")
	if len(failures) != 1 || failures[0]["level"] != "ERROR" {
		t.Fatalf("the log of the failure: %v", failures)
	}

	e.write(`DROP TRIGGER refuse`)
	wantEvent(t, e.scan(), map[string]any{"references_moved": 2})
	one, two := e.twin(fixtureAlbums[albumA], e.b[bOne]), e.twin(fixtureAlbums[albumA], e.b[bTwo])
	e.wantItems(e.bobList, e.bobItems, one.ID, two.ID, one.ID)
	e.wantRevision(e.bobList, 2)
}

// Every table that points to tracks(id) is one the rule moves: a new table
// of references that the scanner does not know would leave its rows on the
// tracks that are gone. The test asks SQLite which tables point to tracks,
// and after a move none of them may still point to the old track.
func TestReferencesCoverEveryTableThatPointsToTracks(t *testing.T) {
	e := newRefEnv(t)
	// The tables the rule moves, and their column.
	covered := map[string]string{"playlist_items": "track_id", "favorites": "track_id"}
	type reference struct{ table, column string }
	var found []reference
	e.read(func(q *store.Queries) error {
		rows, err := q.Conn().QueryContext(t.Context(), `
			SELECT m.name, f."from" FROM sqlite_master AS m JOIN pragma_foreign_key_list(m.name) AS f
			WHERE m.type = 'table' AND f."table" = 'tracks' AND f."to" = 'id' ORDER BY m.name`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r reference
			if err := rows.Scan(&r.table, &r.column); err != nil {
				return errors.Join(err, rows.Close())
			}
			found = append(found, r)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	if len(found) != len(covered) {
		t.Fatalf("the tables that point to tracks(id): %v; the rule of the references covers %v", found, covered)
	}
	for _, r := range found {
		if covered[r.table] != r.column {
			t.Fatalf("%s.%s points to tracks(id) and the rule of the references does not move it", r.table, r.column)
		}
	}

	old := e.b[bOne]
	count := func(r reference) (n int) {
		t.Helper()
		e.read(func(q *store.Queries) error {
			// The names come from the schema, not from a user.
			query := fmt.Sprintf(`SELECT count(*) FROM %q WHERE %q = ?`, r.table, r.column)
			return q.Conn().QueryRowContext(t.Context(), query, old).Scan(&n)
		})
		return n
	}
	for _, r := range found {
		if count(r) == 0 {
			t.Fatalf("the test has no row of %s that points to the track", r.table)
		}
	}
	e.moveTrack(albumB, bOne, albumA, "04 - One.mp3")
	wantEvent(t, e.scan(), map[string]any{"references_moved": 1})
	for _, r := range found {
		if n := count(r); n != 0 {
			t.Errorf("%d rows of %s still point to the track that is gone", n, r.table)
		}
	}
}

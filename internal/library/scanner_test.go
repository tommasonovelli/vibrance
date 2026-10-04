package library

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vibrance/internal/store"
)

// The first cycle brings the whole fixture library into the index, a few
// albums at once; the state says what it did. A second cycle finds every
// album as it was indexed: it runs no process and writes nothing.
func TestScanFixture(t *testing.T) {
	e := newScanEnv(t)
	if st := e.sc.Status(); st.State != StateIdle || st.LastScan != nil || st.Progress != nil || len(st.Problems) != 0 || st.Maintenance {
		t.Fatalf("before the first cycle: %s", statusText(st))
	}

	ev := e.scan()
	wantEvent(t, ev, map[string]any{"reason": "request", "ok": true, "state": "idle", "discovered": 6, "indexed": 6, "absent": 0,
		"references_moved": 0, "problems": 0, "optimized": false, "level": "INFO"})
	st := e.sc.Status()
	if st.Albums != (Counts{Available: 6}) || st.Tracks != (Counts{Available: 14}) || len(st.Problems) != 0 || st.Maintenance {
		t.Fatalf("after the first cycle: %s", statusText(st))
	}
	if !st.LastScan.FinishedAt.After(st.LastScan.StartedAt) {
		t.Fatalf("the cycle finished at %v and started at %v", st.LastScan.FinishedAt, st.LastScan.StartedAt)
	}
	if p, f := e.media.probes.Load(), e.media.fingerprints.Load(); p != 14 || f != 14 {
		t.Errorf("%d probes and %d fingerprints for 14 new tracks", p, f)
	}
	for _, want := range fixtureIndex {
		id := fixtureAlbums[want.rel]
		a, rows := e.album(id), e.tracks(id)
		if a.Album.Title != want.title || a.ArtistName != want.artist || a.Album.Available != 1 || a.Album.RelPath != want.rel ||
			a.Album.TrackCount != int64(len(want.tracks)) || len(rows) != len(want.tracks) {
			t.Fatalf("%s: %+v with %d rows", want.rel, a, len(rows))
		}
		for i, w := range want.tracks {
			if rows[i].RelPath != w.path || rows[i].Title != w.title || rows[i].Fingerprint != w.fingerprint ||
				rows[i].Occurrence != w.occurrence || rows[i].Available != 1 {
				t.Errorf("%s/%s: %+v", want.rel, w.path, rows[i])
			}
		}
	}
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v", got)
	}

	before := e.dumpAll()
	e.media.reset()
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 0, "absent": 0})
	if n := e.media.runs(); n != 0 {
		t.Fatalf("a cycle on an unchanged library ran %d processes", n)
	}
	e.wantAllUnchanged(before)
	// The status is the caller's own copy.
	st = e.sc.Status()
	st.LastScan.OK, st.Problems = false, append(st.Problems, Problem{})
	if again := e.sc.Status(); !again.LastScan.OK || len(again.Problems) != 0 {
		t.Fatalf("a caller changed the status of the scanner: %s", statusText(again))
	}
}

// An album that appears in the library is indexed by the next cycle, and
// nothing else is touched (scenario A12).
func TestScanAlbumAdded(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	before := e.dump()
	const added, addedID = "Bravo Tones/Beta Copy", "01a0f459-ebc8-7081-991c-2b1c9331e157"
	copyAlbum(t, e.dir, albumB, added, addedID)
	e.media.reset()

	wantEvent(t, e.scan(), map[string]any{"discovered": 7, "indexed": 1, "absent": 0})
	if a := e.album(addedID); a.Album.RelPath != added || a.Album.TrackCount != 2 || a.Album.Available != 1 {
		t.Fatalf("the new album: %+v", a)
	}
	if p := e.media.probes.Load(); p != 2 {
		t.Fatalf("%d probes, want those of the two tracks of the new album", p)
	}
	if after := e.dump(); !strings.HasPrefix(after, strings.SplitAfter(before, "\nalbums ")[0]) {
		t.Fatalf("the artists changed:\n%s", after)
	}
	if got := e.rows(); got != (rowCounts{6, 7, 16}) {
		t.Fatalf("rows %+v", got)
	}
	if st := e.sc.Status(); st.Albums.Available != 7 || st.Tracks.Available != 16 {
		t.Fatalf("the counters: %s", statusText(st))
	}
}

// An album that MusicLib writes again with a change is indexed again: its
// tracks keep their ids, and only the file that changed is examined
// (scenario A1).
func TestScanAlbumModified(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumA]
	ids := e.ids(id)
	retag(t, e.inAlbum(albumA, "01 - First Light.flac"), "title=Dawn")
	rerender(t, e.dir, albumA)
	e.media.reset()

	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "absent": 0})
	if got := e.trackAt(id, "01 - First Light.flac"); got.ID != ids["01 - First Light.flac"] || got.Title != "Dawn" {
		t.Fatalf("the track that changed: %+v", got)
	}
	if !maps.Equal(e.ids(id), ids) {
		t.Fatalf("the ids of the tracks changed: %v, were %v", e.ids(id), ids)
	}
	if p, f := e.media.probes.Load(), e.media.fingerprints.Load(); p != 1 || f != 1 {
		t.Fatalf("%d probes and %d fingerprints, want one of each: only one file changed", p, f)
	}
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v", got)
	}
}

// A folder that is renamed is the same album at another path: the same
// rows, and no process, whether the receipt is the same or MusicLib wrote a
// new one (scenario A4). The album is never there twice, and never gone.
func TestScanFolderRenamed(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumA]
	ids := e.ids(id)
	e.media.reset()

	const second = "Aurora Sines/Alpha Renamed"
	rename(t, inLib(e.dir, albumA), inLib(e.dir, second))
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 1, "absent": 0})
	if a := e.album(id).Album; a.RelPath != second || a.Available != 1 || a.TrackCount != 3 {
		t.Fatalf("the album after the rename: %+v", a)
	}

	// As MusicLib renames: another folder of another artist, a new receipt.
	const third = "Zulu Sines/Alpha Again"
	mkdir(t, filepath.Dir(inLib(e.dir, third)))
	rename(t, inLib(e.dir, second), inLib(e.dir, third))
	rerender(t, e.dir, third)
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 1, "absent": 0})
	if a := e.album(id).Album; a.RelPath != third || a.Available != 1 || a.TrackCount != 3 {
		t.Fatalf("the album after the second rename: %+v", a)
	}
	if !maps.Equal(e.ids(id), ids) {
		t.Fatalf("the ids of the tracks changed: %v, were %v", e.ids(id), ids)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for files the index knows", n)
	}
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v", got)
	}
}

// An album that leaves the library becomes unavailable with its tracks and
// leaves the full-text tables; no row is lost. When it comes back it is the
// same album, with the same rows and the same ids, and no process runs
// (scenarios A8 and A9).
func TestScanAlbumRemovedAndBack(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumE]
	whole := e.lasting()
	artistSeq := e.match(matchArtists, "Écho Café")
	if len(artistSeq) != 1 || len(e.match(matchTracks, "Disc One Track One")) != 1 || len(e.match(matchAlbums, "Epsilon Discs")) != 1 {
		t.Fatal("the album is not in the full-text tables")
	}
	e.media.reset()

	trash := e.trash(albumE)
	wantEvent(t, e.scan(), map[string]any{"discovered": 5, "indexed": 0, "absent": 1})
	if a := e.album(id).Album; a.Available != 0 || a.TrackCount != 0 || a.DurationMs != 0 || a.Title != "Epsilon Discs" || a.RelPath != albumE {
		t.Fatalf("the album that is gone: %+v", a)
	}
	rows := e.tracks(id)
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3: a row is never deleted", len(rows))
	}
	for _, row := range rows {
		if row.Available != 0 || row.Title == "" || row.Fingerprint == "" {
			t.Errorf("a track of the album that is gone: %+v", row)
		}
	}
	e.wantRows(matchArtists, "Écho Café")
	e.wantRows(matchAlbums, "Epsilon Discs")
	e.wantRows(matchTracks, "Disc One Track One")
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v: none may be lost", got)
	}
	if st := e.sc.Status(); st.Albums != (Counts{5, 1}) || st.Tracks != (Counts{11, 3}) {
		t.Fatalf("the counters: %s", statusText(st))
	}
	// The other albums were not touched.
	for _, rel := range []string{albumA, albumB, albumC, albumD, albumF} {
		if a := e.album(fixtureAlbums[rel]).Album; a.Available != 1 {
			t.Errorf("%s became unavailable", rel)
		}
	}
	// Gone is gone once: the next cycle writes nothing.
	gone := e.dumpAll()
	wantEvent(t, e.scan(), map[string]any{"absent": 0})
	e.wantAllUnchanged(gone)

	rename(t, trash, inLib(e.dir, albumE))
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 1, "absent": 0})
	if got := e.lasting(); got != whole {
		t.Fatalf("the index after the album came back:\n%s\nwant:\n%s", got, whole)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for files the index knows", n)
	}
}

// During a rename in MusicLib two folders have the same album_id. The one
// with the higher album_revision is the album; at the same revision, the one
// the index has. The album is one row, and the other folder is no problem.
func TestScanTwoFoldersWithOneAlbumID(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumA]
	ids := e.ids(id)

	// The same receipt in a folder that comes first: the index keeps the
	// folder it has.
	const first = "Aurora Sines/A First"
	if err := os.CopyFS(inLib(e.dir, first), os.DirFS(inLib(e.dir, albumA))); err != nil {
		t.Fatal(err)
	}
	before := e.dumpAll()
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 0, "absent": 0, "problems": 0})
	e.wantAllUnchanged(before)

	// A newer revision of the album in another folder is the album.
	const newer = "Aurora Sines/Omega"
	if err := os.CopyFS(inLib(e.dir, newer), os.DirFS(inLib(e.dir, albumA))); err != nil {
		t.Fatal(err)
	}
	retag(t, e.inAlbum(newer, "01 - First Light.flac"), "album=Omega", "title=Dawn")
	retag(t, e.inAlbum(newer, "02 - Second Wave.flac"), "album=Omega")
	retag(t, e.inAlbum(newer, "03 - Third_.flac"), "album=Omega")
	rerender(t, e.dir, newer)
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 1, "absent": 0, "problems": 0})
	if a := e.album(id).Album; a.RelPath != newer || a.Title != "Omega" || a.Available != 1 || a.TrackCount != 3 {
		t.Fatalf("the album: %+v", a)
	}
	if !maps.Equal(e.ids(id), ids) {
		t.Fatalf("the ids of the tracks changed: %v, were %v", e.ids(id), ids)
	}
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v: two folders are one album", got)
	}

	// MusicLib removes the old folders: nothing changes.
	for _, old := range []string{first, albumA} {
		if err := os.RemoveAll(inLib(e.dir, old)); err != nil {
			t.Fatal(err)
		}
	}
	before = e.dumpAll()
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 0, "absent": 0})
	e.wantAllUnchanged(before)
}

// A receipt that cannot be read is a problem of its folder. The album it
// was is not seen, so it becomes unavailable, with every row kept; when the
// receipt is whole again the album is back with the same ids and no process
// runs.
func TestScanCorruptReceiptThenRepaired(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumC]
	whole := e.lasting()
	receipt := filepath.Join(inLib(e.dir, albumC), ReceiptName)
	saved := readFile(t, receipt)
	writeFile(t, receipt, string(saved[:len(saved)/2]))
	e.media.reset()

	wantEvent(t, e.scan(), map[string]any{"discovered": 5, "indexed": 0, "absent": 1, "problems": 1})
	st := e.sc.Status()
	wantStatusProblems(t, st, albumC+" "+CodeReceiptInvalid)
	if st.Problems[0].Message == "" || strings.Contains(st.Problems[0].Message, e.dir) {
		t.Fatalf("the message of the problem: %q", st.Problems[0].Message)
	}
	if a := e.album(id).Album; a.Available != 0 {
		t.Fatalf("the album with a broken receipt: %+v", a)
	}
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v", got)
	}
	// The problem is there as long as the receipt is broken.
	e.scan()
	wantStatusProblems(t, e.sc.Status(), albumC+" "+CodeReceiptInvalid)

	writeFile(t, receipt, string(saved))
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 1, "absent": 0, "problems": 0})
	wantStatusProblems(t, e.sc.Status())
	if got := e.lasting(); got != whole {
		t.Fatalf("the index after the receipt was repaired:\n%s\nwant:\n%s", got, whole)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for files the index knows", n)
	}
}

// While the marker .maintenance exists MusicLib is rebuilding its library,
// which may be empty: the scanner does not touch the index, whatever the
// library looks like, and says so (scenario A11). Once the marker is gone
// the cycles go on.
func TestScanMaintenance(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	last := *e.sc.Status().LastScan
	before := e.dumpAll()

	writeFile(t, filepath.Join(e.dir, maintenanceMarker), "")
	trashA, trashB := e.trash(albumA), e.trash(albumB)
	copyAlbum(t, e.dir, albumC, "Charlie Waves/Gamma Copy", "01a0f459-ebc2-7821-a442-bd56dc181ce4")
	e.media.reset()
	for range 2 {
		st := e.cycle()
		if st.State != StateMaintenance || !st.Maintenance || st.Progress != nil {
			t.Fatalf("with the marker: %s", statusText(st))
		}
		// The last scan is still the one that went through the library.
		if st.LastScan == nil || !st.LastScan.StartedAt.Equal(last.StartedAt) || !st.LastScan.OK {
			t.Fatalf("the last scan with the marker: %s", statusText(st))
		}
		if st.Albums != (Counts{Available: 6}) || st.Tracks != (Counts{Available: 14}) {
			t.Fatalf("the counters with the marker: %s", statusText(st))
		}
	}
	e.wantAllUnchanged(before)
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran during the maintenance", n)
	}
	skipped := e.logs.events(t, "scan skipped")
	if len(skipped) != 2 || skipped[0]["state"] != "maintenance" || skipped[0]["level"] != "INFO" {
		t.Fatalf("the log of the skipped cycles: %v", skipped)
	}

	// The marker goes: the library is what it is now.
	remove(t, filepath.Join(e.dir, maintenanceMarker))
	wantEvent(t, e.scan(), map[string]any{"discovered": 5, "indexed": 1, "absent": 2})
	if st := e.sc.Status(); st.Maintenance || st.Albums != (Counts{5, 2}) {
		t.Fatalf("after the maintenance: %s", statusText(st))
	}
	rename(t, trashA, inLib(e.dir, albumA))
	rename(t, trashB, inLib(e.dir, albumB))
	wantEvent(t, e.scan(), map[string]any{"discovered": 7, "indexed": 2, "absent": 0})
	if got := e.rows(); got != (rowCounts{6, 7, 16}) {
		t.Fatalf("rows %+v", got)
	}
}

// A rebuild of MusicLib that begins while a cycle is running may empty the
// library under it: what the cycle did not find is not gone. The marker is
// looked at again before any album is declared absent.
func TestScanMaintenanceBeginsDuringACycle(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	copyAlbum(t, e.dir, albumC, "Charlie Waves/Gamma Copy", "01a0f459-ebc2-7821-a442-bd56dc181ce4")
	// The album B is gone when the cycle lists the library, and the marker
	// appears only later, while the new album is being indexed.
	e.trash(albumB)
	var once sync.Once
	e.media.setHook(func(string, int64) {
		once.Do(func() { writeFile(t, filepath.Join(e.dir, maintenanceMarker), "") })
	})

	st := e.cycle()
	if st.State != StateMaintenance || !st.Maintenance || st.Progress != nil {
		t.Fatalf("after the cycle: %s", statusText(st))
	}
	if st.LastScan.OK || !strings.Contains(st.LastScan.Error, "maintenance") || st.LastScan.FinishedAt == nil {
		t.Fatalf("the scan that met the maintenance: %s", statusText(st))
	}
	if a := e.album(fixtureAlbums[albumB]).Album; a.Available != 1 || a.TrackCount != 2 {
		t.Fatalf("an album was declared absent during the maintenance: %+v", a)
	}
	if got := e.rows(); got != (rowCounts{6, 7, 16}) {
		t.Fatalf("rows %+v", got)
	}
}

// Without the marker .musiclib-store the folder is not MusicLib's volume: a
// wrong or an empty mount. The scanner does nothing and the index stays.
func TestScanWithoutTheStoreMarker(t *testing.T) {
	e := newScanEnv(t)
	marker := filepath.Join(e.dir, storeMarker)
	remove(t, marker)
	st := e.cycle()
	if st.State != StateUnavailable || st.LastScan != nil || st.Maintenance || st.Albums != (Counts{}) {
		t.Fatalf("a first cycle without the marker: %s", statusText(st))
	}
	e.wantUnchanged("")
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran", n)
	}

	writeFile(t, marker, "store_id=0192a5f0-0000-7000-8000-000000000001\n")
	e.scan()
	before := e.dumpAll()
	remove(t, marker)
	e.trash(albumA)
	if st := e.cycle(); st.State != StateUnavailable || st.Albums != (Counts{Available: 6}) || !st.LastScan.OK {
		t.Fatalf("without the marker: %s", statusText(st))
	}
	e.wantAllUnchanged(before)
	if skipped := e.logs.events(t, "scan skipped"); len(skipped) != 2 || skipped[1]["state"] != "unavailable" {
		t.Fatalf("the log of the skipped cycles: %v", skipped)
	}

	writeFile(t, marker, "store_id=0192a5f0-0000-7000-8000-000000000001\n")
	wantEvent(t, e.scan(), map[string]any{"discovered": 5, "absent": 1})
}

// A library that cannot be listed says nothing of its albums: the cycle
// stops and the index stays. An empty library is another thing: every album
// is gone, and becomes unavailable, but no row is lost, and everything is
// back, with the same ids, when the files are (scenario A16).
func TestScanLibraryMissingThenEmptyThenBack(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	whole := e.lasting()
	before := e.dumpAll()
	away := e.outside("library-away")
	e.media.reset()

	rename(t, filepath.Join(e.dir, libraryDir), away)
	st := e.cycle()
	if st.State != StateUnavailable || st.LastScan.OK || st.LastScan.FinishedAt == nil || st.Progress != nil ||
		!strings.Contains(st.LastScan.Error, "the library folder cannot be listed") || strings.Contains(st.LastScan.Error, e.dir) {
		t.Fatalf("without library/: %s", statusText(st))
	}
	e.wantAllUnchanged(before)

	mkdir(t, filepath.Join(e.dir, libraryDir))
	wantEvent(t, e.scan(), map[string]any{"discovered": 0, "indexed": 0, "absent": 6})
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v: none may be lost", got)
	}
	if st := e.sc.Status(); st.Albums != (Counts{0, 6}) || st.Tracks != (Counts{0, 14}) {
		t.Fatalf("with an empty library: %s", statusText(st))
	}
	for _, table := range []string{"search_artists ", "search_albums ", "search_tracks "} {
		if strings.Contains(e.dump(), "\n"+table) {
			t.Errorf("%sstill has rows", table)
		}
	}

	remove(t, filepath.Join(e.dir, libraryDir))
	rename(t, away, filepath.Join(e.dir, libraryDir))
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 6, "absent": 0})
	if got := e.lasting(); got != whole {
		t.Fatalf("the index after the library came back:\n%s\nwant:\n%s", got, whole)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for files the index knows", n)
	}
}

// An artist folder that cannot be listed is a problem, and it protects the
// albums below it: they were not looked for, so they are not absent. An
// album that is really gone is still found gone in the same cycle.
func TestScanArtistFolderThatCannotBeListed(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	artist := filepath.Dir(inLib(e.dir, albumA))
	if err := os.Chmod(artist, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(artist, 0o755); err != nil {
			t.Error(err)
		}
	})
	if os.Geteuid() == 0 {
		t.Fatal("the tests must not run as root: permissions would not apply")
	}
	e.trash(albumB)

	wantEvent(t, e.scan(), map[string]any{"discovered": 4, "indexed": 0, "absent": 1, "problems": 1})
	wantStatusProblems(t, e.sc.Status(), "Aurora Sines "+CodeListingFailed)
	if a := e.album(fixtureAlbums[albumA]).Album; a.Available != 1 || a.TrackCount != 3 {
		t.Fatalf("the album below the folder that cannot be listed: %+v", a)
	}
	if a := e.album(fixtureAlbums[albumB]).Album; a.Available != 0 {
		t.Fatalf("the album that is gone: %+v", a)
	}

	if err := os.Chmod(artist, 0o755); err != nil {
		t.Fatal(err)
	}
	wantEvent(t, e.scan(), map[string]any{"discovered": 5, "indexed": 0, "absent": 0, "problems": 0})
	wantStatusProblems(t, e.sc.Status())
}

// A cycle that is cut short leaves what it committed, album by album, and
// nothing half written. A scanner that starts again goes on from there: the
// albums that were indexed keep their rows, the others are indexed, and no
// row is there twice (crash-only, scenario A15).
func TestScanStoppedInTheMiddleAndStartedAgain(t *testing.T) {
	e := newScanEnv(t)
	sc := e.scanner(1, time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// One album at a time, in the order of their paths: A is committed, and
	// the stop comes at the second file of B.
	e.media.setHook(func(kind string, n int64) {
		if kind == "probe" && n == 5 {
			cancel()
		}
	})
	sc.cycle(ctx, ReasonStartup)
	st := sc.Status()
	if st.State != StateIdle || st.LastScan.OK || st.LastScan.Error != messageInterrupted || st.LastScan.FinishedAt == nil || st.Progress != nil {
		t.Fatalf("after the stop: %s", statusText(st))
	}
	if got := e.rows(); got != (rowCounts{1, 1, 3}) {
		t.Fatalf("rows %+v after the stop, want album A alone and whole", got)
	}
	ids := e.ids(fixtureAlbums[albumA])
	for _, ev := range e.logs.events(t, "indexing an album failed") {
		t.Errorf("a stop was logged as a failure: %v", ev)
	}

	// The same stop for a scanner that runs as in the server: Run returns.
	e.media.reset()
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	e.media.setHook(func(kind string, n int64) {
		if kind == "fingerprint" && n == 2 {
			cancel2()
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.scanner(1, time.Hour).Run(ctx2)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if got := e.rows(); got != (rowCounts{1, 1, 3}) {
		t.Fatalf("rows %+v after the second stop", got)
	}

	// The next start.
	e.media.setHook(nil)
	e.media.reset()
	e.sc = e.scanner(testWorkers, time.Hour)
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 5, "absent": 0})
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v after the restart: no duplicate and nothing lost", got)
	}
	if !maps.Equal(e.ids(fixtureAlbums[albumA]), ids) {
		t.Fatalf("the ids of the album indexed before the stop changed")
	}
	if p := e.media.probes.Load(); p != 11 {
		t.Fatalf("%d probes after the restart, want those of the 11 tracks that were not indexed", p)
	}
	for _, want := range fixtureIndex {
		rows := e.tracks(fixtureAlbums[want.rel])
		if len(rows) != len(want.tracks) {
			t.Fatalf("%s has %d rows, want %d", want.rel, len(rows), len(want.tracks))
		}
		for i, w := range want.tracks {
			if rows[i].RelPath != w.path || rows[i].Fingerprint != w.fingerprint || rows[i].Occurrence != w.occurrence || rows[i].Available != 1 {
				t.Errorf("%s/%s: %+v", want.rel, w.path, rows[i])
			}
		}
	}
}

// Triggers coalesce: however many are asked for while a cycle runs, one
// cycle waits, and it runs when the first ends. While a cycle runs the
// status says so, with how far it has come.
func TestTriggersCoalesce(t *testing.T) {
	e := newScanEnv(t)
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.media.setHook(func(string, int64) {
		once.Do(func() {
			close(reached)
			<-release
		})
	})
	e.run(e.sc)
	select {
	case <-reached:
	case <-time.After(60 * time.Second):
		t.Fatal("the cycle of the startup did not begin")
	}

	st := e.sc.Status()
	if st.State != StateScanning || st.LastScan == nil || st.LastScan.FinishedAt != nil || st.LastScan.OK || st.LastScan.Error != "" {
		t.Fatalf("while the first cycle runs: %s", statusText(st))
	}
	if st.Progress == nil || st.Progress.Discovered != 6 || st.Progress.Pending+st.Progress.Indexed != 6 || st.Progress.Pending < 1 {
		t.Fatalf("the progress of the first cycle: %s", statusText(st))
	}
	var askers sync.WaitGroup
	for i := range 100 {
		askers.Go(func() {
			if i%2 == 0 {
				e.sc.Trigger(ReasonRequest)
			} else {
				e.sc.Trigger(ReasonFileReplaced)
			}
		})
	}
	askers.Wait()
	close(release)

	eventually(t, "two cycles", func() bool { return len(e.logs.events(t, "scan finished")) >= 2 })
	// Nothing else is waiting: the next cycle is an hour away.
	time.Sleep(300 * time.Millisecond)
	finished := e.logs.events(t, "scan finished")
	if len(finished) != 2 {
		t.Fatalf("%d cycles after 100 triggers during one, want that one and one more", len(finished))
	}
	wantEvent(t, finished[0], map[string]any{"reason": "startup", "ok": true, "indexed": 6})
	wantEvent(t, finished[1], map[string]any{"ok": true, "indexed": 0})
	if r := finished[1]["reason"]; r != "request" && r != "file_replaced" {
		t.Fatalf("the reason of the second cycle: %v", r)
	}
	if st := e.sc.Status(); st.State != StateIdle || st.Progress != nil || !st.LastScan.OK {
		t.Fatalf("after the cycles: %s", statusText(st))
	}

	// A trigger while nothing runs starts a cycle at once.
	e.sc.Trigger(ReasonFileReplaced)
	eventually(t, "a third cycle", func() bool { return len(e.logs.events(t, "scan finished")) >= 3 })
	wantEvent(t, e.logs.events(t, "scan finished")[2], map[string]any{"reason": "file_replaced"})
}

// With nothing asking, a cycle runs at the startup and then after every
// interval.
func TestScanAtEveryInterval(t *testing.T) {
	e := newScanEnv(t)
	sc := e.scanner(testWorkers, 20*time.Millisecond)
	r := e.run(sc)
	eventually(t, "four cycles", func() bool { return len(e.logs.events(t, "scan finished")) >= 4 })
	r.stop(t)
	finished := e.logs.events(t, "scan finished")
	wantEvent(t, finished[0], map[string]any{"reason": "startup", "indexed": 6})
	for _, ev := range finished[1:] {
		// The last one may have been cut by the stop.
		if ev["ok"] == true {
			wantEvent(t, ev, map[string]any{"reason": "interval", "indexed": 0})
		}
	}
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v", got)
	}
	// Once Run has returned nothing of the scanner is running.
	n := len(e.logs.events(t, "scan finished"))
	time.Sleep(100 * time.Millisecond)
	if after := len(e.logs.events(t, "scan finished")); after != n {
		t.Fatalf("%d cycles ended after Run returned", after-n)
	}
}

// A cycle that changes much of the index asks SQLite to refresh its
// statistics at its end; the edit of a few albums does not.
func TestScanOptimizesAfterALargeChange(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	// A hundred albums without tracks cost no process.
	for i := range optimizeAfter {
		r := albumReceipt(newID(t), 1)
		r.Files = nil
		putAlbum(t, e.dir, "Many/"+strings.Repeat("x", 1+i%7)+"-"+newID(t), r)
	}
	wantEvent(t, e.scan(), map[string]any{"indexed": optimizeAfter, "optimized": true})
	wantEvent(t, e.scan(), map[string]any{"indexed": 0, "optimized": false})
	if err := os.RemoveAll(inLib(e.dir, "Many")); err != nil {
		t.Fatal(err)
	}
	wantEvent(t, e.scan(), map[string]any{"absent": optimizeAfter, "optimized": true})
	if got := e.rows(); got.albums != 6+optimizeAfter {
		t.Fatalf("rows %+v", got)
	}
}

// No query the scanner runs ever makes a row of the index fewer, whatever
// happens to the library (I3): the test goes through a library that
// shrinks, empties and comes back, and counts the rows after every cycle.
func TestScanNeverLosesARow(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	want := e.rows()
	var ids []string
	e.read(func(q *store.Queries) error {
		rows, err := q.Conn().QueryContext(t.Context(), `SELECT id FROM tracks UNION ALL SELECT id FROM albums UNION ALL SELECT id FROM artists ORDER BY 1`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Close()
	})
	check := func(step string) {
		t.Helper()
		e.scan()
		got := e.rows()
		if got.artists < want.artists || got.albums < want.albums || got.tracks < want.tracks {
			t.Fatalf("%s: rows %+v, were %+v", step, got, want)
		}
		dump := e.dump()
		for _, id := range ids {
			if !strings.Contains(dump, "id="+id+" ") {
				t.Fatalf("%s: the row %s is gone", step, id)
			}
		}
	}
	remove(t, e.inAlbum(albumA, "02 - Second Wave.flac"))
	rerender(t, e.dir, albumA)
	check("a track deleted")
	retag(t, e.inAlbum(albumB, "01 - One.mp3"), "artist=Somebody Else", "album_artist=Somebody Else")
	retag(t, e.inAlbum(albumB, "02 - Two.mp3"), "artist=Somebody Else", "album_artist=Somebody Else")
	rerender(t, e.dir, albumB)
	check("an artist renamed")
	trash := e.trash(albumC)
	check("an album gone")
	if err := os.RemoveAll(filepath.Join(e.dir, libraryDir)); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(e.dir, libraryDir))
	check("everything gone")
	mkdir(t, filepath.Dir(inLib(e.dir, albumC)))
	rename(t, trash, inLib(e.dir, albumC))
	check("an album back")
	if e.album(fixtureAlbums[albumC]).Album.Available != 1 {
		t.Fatal("the album that came back is not available")
	}
}

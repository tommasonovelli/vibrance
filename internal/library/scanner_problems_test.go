package library

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// An album the tools cannot read is a problem, and nothing of it is
// written. With the same receipt it is not examined again at every cycle:
// the problem stays listed and no process runs. It is tried again when its
// receipt changes, and when the server starts again.
func TestScanDoesNotExamineABrokenAlbumAgain(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumA]
	before := e.dumpAll()
	const broken = "03 - Third_.flac"
	saved := readFile(t, e.inAlbum(albumA, broken))
	zeros(t, e.inAlbum(albumA, broken))
	rerender(t, e.dir, albumA)
	e.media.reset()

	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 0, "absent": 0, "problems": 1})
	st := e.sc.Status()
	wantStatusProblems(t, st, albumA+" "+CodeProbeFailed)
	if msg := st.Problems[0].Message; !strings.Contains(msg, broken) || strings.Contains(msg, e.dir) {
		t.Fatalf("the message of the problem: %q", msg)
	}
	if n := e.media.runs(); n == 0 {
		t.Fatal("the album was not examined")
	}
	// The album stays as it was indexed, and is not absent.
	e.wantAllUnchanged(before)

	for range 3 {
		e.media.reset()
		wantEvent(t, e.scan(), map[string]any{"indexed": 0, "problems": 1})
		wantStatusProblems(t, e.sc.Status(), albumA+" "+CodeProbeFailed)
		if n := e.media.runs(); n != 0 {
			t.Fatalf("%d processes ran on an album that failed with the same receipt", n)
		}
	}
	e.wantAllUnchanged(before)

	// A server that starts again tries it once more.
	e.sc = e.scanner(testWorkers, time.Hour)
	wantEvent(t, e.scan(), map[string]any{"indexed": 0, "problems": 1})
	if n := e.media.runs(); n == 0 {
		t.Fatal("after a restart the album was not examined again")
	}
	e.media.reset()
	e.scan()
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran at the second cycle after the restart", n)
	}

	// A new receipt: MusicLib wrote the album again, whole.
	writeFile(t, e.inAlbum(albumA, broken), string(saved))
	rerender(t, e.dir, albumA)
	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "problems": 0})
	wantStatusProblems(t, e.sc.Status())
	if a := e.album(id).Album; a.Available != 1 || a.TrackCount != 3 {
		t.Fatalf("the album after it was repaired: %+v", a)
	}
}

// A new receipt that fails again is examined again, once.
func TestScanExaminesABrokenAlbumAgainWhenItsReceiptChanges(t *testing.T) {
	e := newScanEnv(t)
	zeros(t, e.inAlbum(albumB, "02 - Two.mp3"))
	rerender(t, e.dir, albumB)
	wantEvent(t, e.scan(), map[string]any{"indexed": 5, "problems": 1})
	wantStatusProblems(t, e.sc.Status(), albumB+" "+CodeProbeFailed)
	if got := e.rows(); got != (rowCounts{5, 5, 12}) {
		t.Fatalf("rows %+v: the broken album was written", got)
	}
	e.media.reset()
	e.scan()
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran", n)
	}

	rerender(t, e.dir, albumB)
	wantEvent(t, e.scan(), map[string]any{"indexed": 0, "problems": 1})
	if p := e.media.probes.Load(); p != 2 {
		t.Fatalf("%d probes after the receipt changed, want 2: the album is examined again up to its broken file", p)
	}
}

// A file that is missing costs nothing to look for again: the album is
// tried at every cycle, and is indexed as soon as the file is there, with
// the same receipt.
func TestScanTriesAnAlbumWithAMissingFileAgain(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	const added, addedID = "Bravo Tones/Beta Copy", "01a0f459-ebc8-7081-991c-2b1c9331e157"
	copyAlbum(t, e.dir, albumB, added, addedID)
	saved := readFile(t, e.inAlbum(added, "02 - Two.mp3"))
	remove(t, e.inAlbum(added, "02 - Two.mp3"))
	e.media.reset()

	for range 2 {
		wantEvent(t, e.scan(), map[string]any{"discovered": 7, "indexed": 0, "problems": 1})
		wantStatusProblems(t, e.sc.Status(), added+" "+CodeFileMissing)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for an album with a missing file", n)
	}
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v: an album with a problem was written", got)
	}

	// Another size is the other problem of step 2.
	writeFile(t, e.inAlbum(added, "02 - Two.mp3"), "short")
	e.scan()
	wantStatusProblems(t, e.sc.Status(), added+" "+CodeFileSizeMismatch)

	writeFile(t, e.inAlbum(added, "02 - Two.mp3"), string(saved))
	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "problems": 0})
	if a := e.album(addedID).Album; a.Available != 1 || a.TrackCount != 2 {
		t.Fatalf("the album once its file is back: %+v", a)
	}
}

// The warnings of an album that was indexed are listed at every cycle while
// the album stays as it is, although the album is not looked at again; they
// go when the album changes.
func TestScanKeepsTheWarningsOfAnAlbum(t *testing.T) {
	e := newScanEnv(t)
	writeFile(t, e.inAlbum(albumB, "cover.png"), "not an image")
	untagged(t, e.inAlbum(albumC, "01 - One.m4a"))
	rerender(t, e.dir, albumB)
	rerender(t, e.dir, albumC)

	wantEvent(t, e.scan(), map[string]any{"indexed": 6, "problems": 2})
	want := []string{albumB + " " + CodeCoverInvalid, albumC + " " + CodeTagsIncomplete}
	wantStatusProblems(t, e.sc.Status(), want...)
	before := e.dumpAll()
	e.media.reset()
	for range 2 {
		wantEvent(t, e.scan(), map[string]any{"indexed": 0, "problems": 2})
		wantStatusProblems(t, e.sc.Status(), want...)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran", n)
	}
	e.wantAllUnchanged(before)

	remove(t, e.inAlbum(albumB, "cover.png"))
	rerender(t, e.dir, albumB)
	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "problems": 1})
	wantStatusProblems(t, e.sc.Status(), albumC+" "+CodeTagsIncomplete)
}

// The problems of the folders that are not albums are listed with those of
// the albums, in the order of their paths, and the list is made again at
// every cycle.
func TestScanListsTheProblemsOfACycle(t *testing.T) {
	e := newScanEnv(t)
	mkdir(t, inLib(e.dir, "Zulu/No Receipt"))
	putAlbum(t, e.dir, "Alfa/Future", albumReceipt(newID(t), 1))
	writeFile(t, inLib(e.dir, "Alfa/Future/"+ReceiptName), `{"schema_version":2}`)
	writeFile(t, e.inAlbum(albumB, "cover.png"), "not an image")
	rerender(t, e.dir, albumB)

	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 6, "problems": 3})
	wantStatusProblems(t, e.sc.Status(),
		"Alfa/Future "+CodeReceiptSchemaUnsupported, albumB+" "+CodeCoverInvalid, "Zulu/No Receipt "+CodeReceiptMissing)

	if err := os.RemoveAll(inLib(e.dir, "Zulu")); err != nil {
		t.Fatal(err)
	}
	e.scan()
	wantStatusProblems(t, e.sc.Status(), "Alfa/Future "+CodeReceiptSchemaUnsupported, albumB+" "+CodeCoverInvalid)
}

// The list of the problems has at most MaxProblems entries.
func TestScanListsAtMostTwoHundredProblems(t *testing.T) {
	e := newScanEnv(t)
	for i := range MaxProblems + 7 {
		mkdir(t, inLib(e.dir, fmt.Sprintf("Zulu/%04d", i)))
	}
	wantEvent(t, e.scan(), map[string]any{"indexed": 6, "problems": MaxProblems + 7})
	st := e.sc.Status()
	if len(st.Problems) != MaxProblems {
		t.Fatalf("%d problems listed, want %d", len(st.Problems), MaxProblems)
	}
	for i, p := range st.Problems {
		if want := fmt.Sprintf("Zulu/%04d", i); p.RelPath != want || p.Code != CodeReceiptMissing {
			t.Fatalf("problem %d: %+v, want %s", i, p, want)
		}
	}
}

// An album the database refuses does not stop the cycle: the other albums
// are indexed, the cycle goes to its end and says that it was not whole,
// and the album is tried again at the next cycle. The error is in the log,
// not in the state.
func TestScanGoesOnAfterAnInternalErrorOfOneAlbum(t *testing.T) {
	e := newScanEnv(t)
	e.write(`CREATE TRIGGER refuse BEFORE INSERT ON albums WHEN new.id = '` + fixtureAlbums[albumB] + `'
		BEGIN SELECT RAISE(ABORT, 'refused by the test'); END`)
	trashed := e.trash(albumF)

	st := e.cycle()
	if st.State != StateIdle || st.LastScan.OK || st.LastScan.FinishedAt == nil ||
		!strings.Contains(st.LastScan.Error, "1 albums could not be indexed") || strings.Contains(st.LastScan.Error, "refused by the test") {
		t.Fatalf("after the cycle: %s", statusText(st))
	}
	wantStatusProblems(t, st)
	if got := e.rows(); got != (rowCounts{4, 4, 10}) {
		t.Fatalf("rows %+v, want the four other albums", got)
	}
	failures := e.logs.events(t, "indexing an album failed")
	if len(failures) != 1 || failures[0]["level"] != "ERROR" || failures[0]["rel_path"] != albumB ||
		!strings.Contains(fmt.Sprint(failures[0]["err"]), "refused by the test") {
		t.Fatalf("the log of the failure: %v", failures)
	}

	// The steps after the indexing ran all the same: an album that is gone
	// is found gone.
	rename(t, trashed, inLib(e.dir, albumF))
	e.cycle()
	e.trash(albumF)
	st = e.cycle()
	if a := e.album(fixtureAlbums[albumF]).Album; a.Available != 0 || st.LastScan.OK {
		t.Fatalf("the album that is gone: %+v (%s)", a, statusText(st))
	}

	e.write(`DROP TRIGGER refuse`)
	wantEvent(t, e.scan(), map[string]any{"indexed": 1})
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v", got)
	}
}

// A cover that cannot be opened keeps its album out of the index for that
// cycle, and the album is tried again at every cycle: as soon as the cover
// can be read the album is indexed with it, with the same receipt.
func TestScanTriesAnAlbumWithACoverItCannotOpenAgain(t *testing.T) {
	e := newScanEnv(t)
	cover := e.inAlbum(albumA, "cover.jpg")
	if err := os.Chmod(cover, 0); err != nil {
		t.Fatal(err)
	}
	for _, indexed := range []int{5, 0} {
		wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": indexed, "problems": 1})
		wantStatusProblems(t, e.sc.Status(), albumA+" "+CodeFileMissing)
		if got := e.rows(); got != (rowCounts{5, 5, 11}) {
			t.Fatalf("rows %+v: the album was written without its cover", got)
		}
		// The cycles after the first try only that album, and trying it
		// starts no process (DESIGN.md §6.2).
		if got := e.media.runs(); indexed == 0 && got != 0 {
			t.Fatalf("%d processes were started for an album whose cover cannot be opened", got)
		}
		e.media.reset()
	}
	if got := e.warmer.take(); len(got) != 0 {
		t.Fatalf("covers to warm: %v", got)
	}

	if err := os.Chmod(cover, 0o644); err != nil {
		t.Fatal(err)
	}
	wantEvent(t, e.scan(), map[string]any{"indexed": 1, "problems": 0})
	wantStatusProblems(t, e.sc.Status())
	if a := e.album(fixtureAlbums[albumA]).Album; a.Available != 1 || a.TrackCount != 3 || a.CoverSha256.String != coverASHA256 {
		t.Fatalf("the album once its cover could be read: %+v", a)
	}
	if got := e.warmer.take(); len(got) != 1 || got[0] != coverASHA256 {
		t.Fatalf("covers to warm: %v", got)
	}
}

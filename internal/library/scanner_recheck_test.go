package library

import (
	"context"
	"maps"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

// Recheck (DESIGN.md §6.2, the erratum of step S16e): the guard of the
// media endpoints must be able to heal. An album whose receipt and folder
// are those it was indexed from is skipped by P3, so a file of it that only
// has another time would be refused for ever.

// touch gives a file another modification time, and returns it in
// nanoseconds.
func touch(t *testing.T, path string) int64 {
	t.Helper()
	at := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, time.Time{}, at); err != nil {
		t.Fatal(err)
	}
	return at.UnixNano()
}

// pendingRechecks is how many albums wait for a cycle.
func (e *scanEnv) pendingRechecks() int {
	e.sc.mu.Lock()
	defer e.sc.mu.Unlock()
	return len(e.sc.rechecks)
}

// A file and the cover of an album get another time, with the same bytes
// and the same receipt. A cycle alone does not look at the album; after
// Recheck one cycle writes the new times, starts no process and changes
// nothing else, and the cycle after it skips the album again.
func TestRecheckHealsTheTimesOfAnUnchangedAlbum(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumA]
	const file = "03 - Third_.flac"
	ids := e.ids(id)
	was, wasAlbum := e.trackAt(id, file), e.album(id).Album
	others := e.dumpOther(id)
	mtime := touch(t, e.inAlbum(albumA, file))
	coverMtime := touch(t, e.inAlbum(albumA, "cover.jpg"))
	e.media.reset()

	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 0, "problems": 0})
	if got := e.trackAt(id, file).FileMtimeNs; got != was.FileMtimeNs {
		t.Fatalf("a cycle nobody asked a recheck of wrote the time %d", got)
	}

	e.sc.Recheck(id)
	if len(e.sc.wake) != 1 {
		t.Fatal("Recheck did not ask for a cycle")
	}
	wantEvent(t, e.scan(), map[string]any{"discovered": 6, "indexed": 1, "absent": 0, "problems": 0})
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for an album whose files are those of the index", n)
	}
	now := e.trackAt(id, file)
	if now.FileMtimeNs != mtime {
		t.Fatalf("the time of the file in the index is %d, want %d", now.FileMtimeNs, mtime)
	}
	// Nothing else of the row changed (I3).
	now.FileMtimeNs, now.UpdatedAt = was.FileMtimeNs, was.UpdatedAt
	if now != was {
		t.Fatalf("the row changed beyond its time:\n got %+v\nwant %+v", now, was)
	}
	a := e.album(id).Album
	if a.CoverMtimeNs.Int64 != coverMtime {
		t.Fatalf("the time of the cover in the index is %d, want %d", a.CoverMtimeNs.Int64, coverMtime)
	}
	a.CoverMtimeNs, a.UpdatedAt = wasAlbum.CoverMtimeNs, wasAlbum.UpdatedAt
	if !reflect.DeepEqual(a, wasAlbum) {
		t.Fatalf("the album changed beyond the time of its cover:\n got %+v\nwant %+v", a, wasAlbum)
	}
	if !maps.Equal(e.ids(id), ids) {
		t.Fatal("the ids of the tracks changed")
	}
	if got := e.dumpOther(id); got != others {
		t.Fatalf("the other albums changed:\n%s\nwant\n%s", got, others)
	}

	// The set was taken: the album is skipped again.
	if n := e.pendingRechecks(); n != 0 {
		t.Fatalf("%d albums still wait for a cycle", n)
	}
	wantEvent(t, e.scan(), map[string]any{"indexed": 0})
}

// dumpOther writes out the tracks of every album but one.
func (e *scanEnv) dumpOther(albumID string) string {
	e.t.Helper()
	out := ""
	for _, rel := range slices.Sorted(maps.Keys(fixtureAlbums)) {
		id := fixtureAlbums[rel]
		if id == albumID {
			continue
		}
		for _, tr := range e.tracks(id) {
			out += rel + " " + tr.ID + " " + tr.RelPath + " " + tr.FileSha256 + " " + time.Unix(0, tr.FileMtimeNs).UTC().String() + "\n"
		}
	}
	return out
}

// An album the tools could not read is not examined again because a client
// asked for one of its files: Recheck starts no process on it (§6.2).
func TestRecheckDoesNotExamineABrokenAlbumAgain(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumA]
	before := e.dumpAll()
	zeros(t, e.inAlbum(albumA, "03 - Third_.flac"))
	rerender(t, e.dir, albumA)
	wantEvent(t, e.scan(), map[string]any{"indexed": 0, "problems": 1})
	wantStatusProblems(t, e.sc.Status(), albumA+" "+CodeProbeFailed)

	for range 2 {
		e.media.reset()
		e.sc.Recheck(id)
		wantEvent(t, e.scan(), map[string]any{"indexed": 0, "problems": 1})
		wantStatusProblems(t, e.sc.Status(), albumA+" "+CodeProbeFailed)
		if n := e.media.runs(); n != 0 {
			t.Fatalf("%d processes ran on a failed album that Recheck named", n)
		}
		if n := e.pendingRechecks(); n != 0 {
			t.Fatalf("%d albums still wait for a cycle", n)
		}
	}
	e.wantAllUnchanged(before)
}

// The id of an album that is no longer in the library, or that never was,
// is dropped: the cycle goes to its end and the album becomes unavailable
// as it would have.
func TestRecheckOfAnAlbumThatIsGone(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumB]
	e.trash(albumB)
	e.sc.Recheck(id)
	e.sc.Recheck("01a0f459-0000-7000-8000-000000000000")
	e.media.reset()

	wantEvent(t, e.scan(), map[string]any{"discovered": 5, "indexed": 0, "absent": 1, "problems": 0})
	if a := e.album(id).Album; a.Available != 0 {
		t.Fatal("the album that is gone is still available")
	}
	if n := e.pendingRechecks(); n != 0 {
		t.Fatalf("%d ids still wait for a cycle", n)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran", n)
	}
	wantEvent(t, e.scan(), map[string]any{"discovered": 5, "indexed": 0, "absent": 0})
}

// The known limit: a file whose size is not the one of the receipt is a
// problem of the album, which stays as it was indexed. No process runs.
func TestRecheckOfAFileOfAnotherSize(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumA]
	before := e.dumpAll()
	const file = "03 - Third_.flac"
	writeFile(t, e.inAlbum(albumA, file), string(readFile(t, e.inAlbum(albumA, file)))+"x")
	e.media.reset()

	for range 2 {
		e.sc.Recheck(id)
		wantEvent(t, e.scan(), map[string]any{"indexed": 0, "problems": 1})
		wantStatusProblems(t, e.sc.Status(), albumA+" "+CodeFileSizeMismatch)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran", n)
	}
	e.wantAllUnchanged(before)
}

// Many requests ask for rechecks while the scanner runs its cycles, as the
// media endpoints do. Every album asked for is healed, whichever cycle
// took its id, and no process runs.
func TestRecheckConcurrently(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	want := map[string]int64{}
	for rel, id := range fixtureAlbums {
		first := e.tracks(id)[0]
		want[first.ID] = touch(t, e.inAlbum(rel, first.RelPath))
	}
	e.media.reset()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.sc.Run(ctx)
	}()
	var callers sync.WaitGroup
	for range 8 {
		callers.Go(func() {
			for range 50 {
				for _, id := range fixtureAlbums {
					e.sc.Recheck(id)
				}
				e.sc.Recheck("01a0f459-0000-7000-8000-000000000000")
				_ = e.sc.Status()
			}
		})
	}
	callers.Wait()

	deadline := time.Now().Add(60 * time.Second)
	for {
		healed := 0
		for id, mtime := range want {
			if e.track(id).FileMtimeNs == mtime {
				healed++
			}
		}
		if healed == len(want) && e.pendingRechecks() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d files healed within 60s; logs:\n%s", healed, len(want), e.logs)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran", n)
	}
}

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const watchedAlbum = "Bravo Tones/Beta MP3"

// newLibrary makes a library with one album of the fixture, at rel.
func newLibrary(t *testing.T, rel string) string {
	t.Helper()
	lib := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(filepath.Join(lib, filepath.Dir(filepath.FromSlash(rel))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(fixtureRoot, filepath.FromSlash(watchedAlbum)), filepath.Join(lib, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestWatchPassReportsAnIncompleteFolder(t *testing.T) {
	lib := newLibrary(t, watchedAlbum)
	if err := os.Remove(filepath.Join(lib, filepath.FromSlash(watchedAlbum), "02 - Two.mp3")); err != nil {
		t.Fatal(err)
	}
	var res watchResult
	res.MaxFolders, res.Revisions = map[string]int{}, map[string][]int64{}
	if err := watchPass(lib, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Partial) != 1 || !strings.Contains(res.Partial[0], "02 - Two.mp3") {
		t.Fatalf("partial %q", res.Partial)
	}
}

func TestWatchPassCountsFoldersOfOneAlbum(t *testing.T) {
	lib := newLibrary(t, watchedAlbum)
	if err := copyTree(filepath.Join(lib, filepath.FromSlash(watchedAlbum)), filepath.Join(lib, "Bravo Tones", "Beta MP3 Renamed")); err != nil {
		t.Fatal(err)
	}
	var res watchResult
	res.MaxFolders, res.Revisions = map[string]int{}, map[string][]int64{}
	if err := watchPass(lib, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Partial) != 0 || len(res.MaxFolders) != 1 {
		t.Fatalf("result %+v", res)
	}
	for id, n := range res.MaxFolders {
		if n != 2 || len(res.Revisions[id]) != 2 {
			t.Fatalf("result %+v", res)
		}
	}
}

// A writer replaces the album the way MusicLib does (the complete new
// folder renamed into place, the old one moved away and later deleted)
// while the watcher runs: the watcher must never call that incomplete,
// even when it reads a folder whose files are being deleted.
func TestWatchLibraryDuringReplacements(t *testing.T) {
	lib := newLibrary(t, watchedAlbum)
	work := filepath.Join(filepath.Dir(lib), "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	type out struct {
		res watchResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := watchLibrary(ctx, lib)
		done <- out{res, err}
	}()
	names := []string{"Beta MP3", "Beta MP3 Renamed"}
	deadline := time.Now().Add(2 * time.Second)
	var werr error
	for i := 0; werr == nil && (i < 20 || time.Now().Before(deadline)); i++ {
		werr = replace(lib, work, filepath.Join("Bravo Tones", names[i%2]), filepath.Join("Bravo Tones", names[(i+1)%2]), i)
	}
	cancel()
	o := <-done
	if werr != nil {
		t.Fatal(werr)
	}
	if o.err != nil {
		t.Fatal(o.err)
	}
	if len(o.res.Partial) != 0 {
		t.Fatalf("incomplete folders reported during atomic replacements: %q", o.res.Partial)
	}
	if o.res.Scans == 0 {
		t.Fatal("the watcher made no pass")
	}
}

// replace moves the album from old to new: a complete copy is built in
// work, renamed to new, then old is renamed into work and deleted.
func replace(lib, work, old, new string, i int) error {
	staging := filepath.Join(work, fmt.Sprintf("staging-%d", i))
	if err := copyTree(filepath.Join(lib, old), staging); err != nil {
		return err
	}
	if err := os.Rename(staging, filepath.Join(lib, new)); err != nil {
		return err
	}
	retired := filepath.Join(work, fmt.Sprintf("retired-%d", i))
	if err := os.Rename(filepath.Join(lib, old), retired); err != nil {
		return err
	}
	return os.RemoveAll(retired)
}

// A folder opened, then moved away (a new version taking its place) and
// emptied before it is checked, as MusicLib does with the old version of an
// album, is not incomplete; the same folder emptied in place is.
func TestCheckOpenedFolderMovedAway(t *testing.T) {
	for _, moved := range []bool{true, false} {
		t.Run(fmt.Sprintf("moved=%v", moved), func(t *testing.T) {
			lib := newLibrary(t, watchedAlbum)
			dir := filepath.Join(lib, filepath.FromSlash(watchedAlbum))
			r, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			}()
			if moved {
				if err := os.Rename(dir, filepath.Join(filepath.Dir(lib), "retired")); err != nil {
					t.Fatal(err)
				}
				// The new version of the album takes its place.
				if err := copyTree(filepath.Join(fixtureRoot, filepath.FromSlash(watchedAlbum)), dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Remove("01 - One.mp3"); err != nil {
				t.Fatal(err)
			}
			_, _, problem := checkOpened(dir, r)
			if moved && problem != "" {
				t.Fatalf("a folder moved away is reported: %s", problem)
			}
			if !moved && !strings.Contains(problem, "01 - One.mp3") {
				t.Fatalf("problem %q for a folder emptied in place", problem)
			}
		})
	}
}

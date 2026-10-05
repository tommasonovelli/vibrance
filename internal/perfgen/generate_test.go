//go:build perf

package perfgen

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"vibrance/internal/library"
	"vibrance/internal/search"
	"vibrance/internal/store"
)

// testPassword is the password of the accounts of the datasets the tests
// generate. It protects nothing: the servers they start answer only on the
// loopback interface of the test container, and are gone with it.
const testPassword = "the password of the perf accounts"

var small = Size{Artists: 30, Albums: 120, Tracks: 1300, Users: 3, Playlists: 4, PlaylistItems: 25, Favorites: 90}

// generate makes a dataset of that size in a new state folder, with the
// folder of MusicLib when library is true, and returns the two folders.
func generate(t *testing.T, size Size, withLibrary bool) (stateDir, musiclibDir string) {
	t.Helper()
	stateDir, musiclibDir = t.TempDir(), t.TempDir()
	began := time.Now()
	st, err := store.Open(t.Context(), filepath.Join(stateDir, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	lib := ""
	if withLibrary {
		lib = musiclibDir
	}
	if err := Generate(t.Context(), st, size, testPassword, lib); err != nil {
		t.Fatal(err)
	}
	// What a cycle of the scanner that wrote or found gone at least 100
	// albums does at its end (NOTES.md N-073), here in its worst case: no
	// table has been analyzed yet.
	optimizing := time.Now()
	if err := st.Optimize(t.Context()); err != nil {
		t.Fatal(err)
	}
	optimized := time.Since(optimizing)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if size != small {
		fmt.Printf("PERF dataset: %+v generated in %s; PRAGMA optimize of the new index, as after a first scan: %s\n",
			size, time.Since(began).Round(time.Second), ms(optimized))
	}
	return stateDir, musiclibDir
}

// The dataset is an index the server could have written: it has the rows
// asked for, sound counters and full-text tables, and a library folder
// whose receipts are those the index was made from, so that a scan finds
// nothing to do.
func TestPerfGenerate(t *testing.T) {
	ctx := t.Context()
	stateDir, musiclibDir := generate(t, small, true)
	st, err := store.Open(ctx, filepath.Join(stateDir, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	}()

	var states []store.ListAlbumStatesRow
	err = st.Read(ctx, func(q *store.Queries) error {
		rows, err := q.CountRows(ctx)
		if err != nil {
			return err
		}
		want := store.CountRowsRow{Users: 3, Playlists: 4, PlaylistItems: 100, Favorites: 90, Artists: 30, Albums: 120, Tracks: 1300}
		if rows != want {
			t.Errorf("the dataset has %+v, want %+v", rows, want)
		}
		if wrong, err := q.ListAlbumsWithWrongCounters(ctx); err != nil || len(wrong) != 0 {
			t.Errorf("albums with wrong counters: %v, %v", wrong, err)
		}
		if lines, err := q.IntegrityCheck(ctx); err != nil || len(lines) != 0 {
			t.Errorf("integrity check: %v, %v", lines, err)
		}
		if broken, err := q.ForeignKeyCheck(ctx); err != nil || len(broken) != 0 {
			t.Errorf("foreign keys: %v, %v", broken, err)
		}
		if faults, err := search.Check(ctx, q.Conn()); err != nil || len(faults) != 0 {
			t.Errorf("full-text tables: %v, %v", faults, err)
		}
		found, err := search.Find(ctx, q, "", search.Parse("love"), search.Kinds{Artists: true, Albums: true, Tracks: true}, 5)
		if err != nil || len(found.Tracks) == 0 {
			t.Errorf("searching love: %d tracks, %v", len(found.Tracks), err)
		}
		states, err = q.ListAlbumStates(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	root, err := library.OpenRoot(musiclibDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	if present, err := root.StorePresent(); err != nil || !present {
		t.Errorf("the marker of MusicLib's volume: %v, %v", present, err)
	}
	d, err := library.Discover(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Problems) != 0 || len(d.Candidates) != small.Albums {
		t.Fatalf("the library has %d albums and the problems %v, want %d and none", len(d.Candidates), d.Problems, small.Albums)
	}
	onDisk := map[string]library.Candidate{}
	for _, c := range d.Candidates {
		onDisk[c.Receipt.AlbumID] = c
		if _, err := library.Classify(c.Receipt); err != nil {
			t.Errorf("%s: %v", c.RelPath, err)
		}
	}
	for _, a := range states {
		if c := onDisk[a.ID]; a.Available != 1 || c.RelPath != a.RelPath || c.ReceiptHash != a.ReceiptHash {
			t.Errorf("the album %s is %+v in the index and %s %s on disk", a.ID, a, c.RelPath, c.ReceiptHash)
		}
	}
}

// The same size gives the same rows: two runs of the suite measure the same
// data.
func TestPerfGenerateIsRepeatable(t *testing.T) {
	read := func() []store.ListAlbumStatesRow {
		stateDir, _ := generate(t, small, false)
		st, err := store.Open(t.Context(), filepath.Join(stateDir, "vibrance.db"))
		if err != nil {
			t.Fatal(err)
		}
		var rows []store.ListAlbumStatesRow
		err = st.Read(t.Context(), func(q *store.Queries) (err error) {
			rows, err = q.ListAlbumStates(t.Context())
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	a, b := read(), read()
	if len(a) != small.Albums || len(a) != len(b) {
		t.Fatalf("%d and %d albums", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("the album %d differs: %+v, %+v", i, a[i], b[i])
		}
	}
}

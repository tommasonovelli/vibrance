package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"vibrance/internal/library"
	"vibrance/internal/names"
	"vibrance/internal/store"
)

// Steps 6 and 7 of the startup (DESIGN.md §11.2), and step 3 of the stop,
// with the real things: the fixture library on disk, a real database and
// the pinned tools.

// fixtureLibrary makes a folder as /musiclib is in a healthy installation,
// with a copy of the fixture library, and returns it.
func fixtureLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "library"), os.DirFS(filepath.Join("..", "..", "testdata", "library-v1"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".musiclib-store"), []byte("store_id=0192a5f0-0000-7000-8000-000000000001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// waitScanned waits until the scanner of the server has finished a cycle
// that went through the library, and returns its status.
func waitScanned(t *testing.T, r *running) library.Status {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		// The server has its scanner once it has logged that it started it.
		if len(eventsOf(t, r.logs, "scanner started")) == 1 {
			if st := r.s.scanner.Status(); st.LastScan != nil && st.LastScan.FinishedAt != nil {
				return st
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no scan finished within 60s; logs:\n%s", r.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// eventsOf are the log events with that message.
func eventsOf(t *testing.T, logs *syncBuffer, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range logs.events(t) {
		if ev["msg"] == msg {
			out = append(out, ev)
		}
	}
	return out
}

// The server starts the scanner as the last step before it is ready, with
// the workers and the interval of its configuration, and the scanner brings
// the library into the index while the server serves. The stop cancels it
// after the HTTP server and before the database is closed. A second start
// finds the index and has nothing to index.
func TestStartupStartsTheScanner(t *testing.T) {
	musiclib, state := fixtureLibrary(t), t.TempDir()
	prepare := func(s *server) { s.stateDir, s.musiclibDir = state, musiclib }
	r := startServer(t, prepare)
	waitReady(t, "http://"+r.addr)
	st := waitScanned(t, r)
	if st.State != library.StateIdle || !st.LastScan.OK || st.Albums != (library.Counts{Available: 6}) ||
		st.Tracks != (library.Counts{Available: 14}) || len(st.Problems) != 0 {
		t.Fatalf("the state of the library after the first scan: %+v (last scan %+v)", st, *st.LastScan)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}

	msgs := r.logs.messages(t)
	want := []string{"http listening", "database open", "media tools verified", "scanner started", "ready",
		"stopping", "http server stopped", "scanner stopped", "database closed"}
	if !slices.Equal(msgs, want) {
		t.Fatalf("log events %q, want %q", msgs, want)
	}
	if started := eventsOf(t, r.logs, "scanner started"); started[0]["scan_interval"] != testScanInterval.String() {
		t.Fatalf("the event of the start: %v", started)
	}
	finished := eventsOf(t, r.logs, "scan finished")
	if len(finished) != 1 || finished[0]["reason"] != "startup" || finished[0]["indexed"] != float64(6) || finished[0]["ok"] != true {
		t.Fatalf("the cycles of the first run: %v", finished)
	}
	for _, ev := range r.logs.events(t) {
		if ev["level"] != "INFO" {
			t.Fatalf("the run logged above INFO: %v", ev)
		}
	}

	again := startServer(t, prepare)
	waitReady(t, "http://"+again.addr)
	st = waitScanned(t, again)
	if !st.LastScan.OK || st.Albums != (library.Counts{Available: 6}) || st.Tracks != (library.Counts{Available: 14}) {
		t.Fatalf("the state of the library after the restart: %+v", st)
	}
	if err := again.stop(t); err != nil {
		t.Fatal(err)
	}
	finished = eventsOf(t, again.logs, "scan finished")
	if len(finished) != 1 || finished[0]["indexed"] != float64(0) || finished[0]["absent"] != float64(0) {
		t.Fatalf("the cycles of the second run: %v", finished)
	}
}

// The server is ready whatever the folder of MusicLib holds (I14): an empty
// folder is a library that is not there, which the scanner says, and
// nothing else depends on it.
func TestStartupWithoutALibrary(t *testing.T) {
	r := startServer(t, nil)
	waitReady(t, "http://"+r.addr)
	deadline := time.Now().Add(60 * time.Second)
	for len(eventsOf(t, r.logs, "scan skipped")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no cycle within 60s; logs:\n%s", r.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := r.s.scanner.Status()
	if st.State != library.StateUnavailable || st.LastScan != nil || st.Albums != (library.Counts{}) {
		t.Fatalf("the state of a library that is not there: %+v", st)
	}
	wantJSON(t, do(t, "GET", "http://"+r.addr+"/health/ready"), 200, readyJSON)
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	if skipped := eventsOf(t, r.logs, "scan skipped"); skipped[0]["state"] != "unavailable" || skipped[0]["reason"] != "startup" {
		t.Fatalf("the cycle of the startup: %v", skipped)
	}
}

// A folder of MusicLib that is not there at all is a broken installation:
// the image always has it. The startup refuses, and the server was never
// ready.
func TestStartupRefusesAMissingMusicLibFolder(t *testing.T) {
	logs := &syncBuffer{}
	missing := filepath.Join(t.TempDir(), "missing")
	s := newServer(newLogger(logs), t.TempDir(), missing, testWorkers, testScanInterval)
	err := s.run(t.Context(), listen(t))
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeMusicLibFolder || Code(err) != CodeMusicLibFolder {
		t.Fatalf("run returned %v (code %q), want code %q", err, Code(err), CodeMusicLibFolder)
	}
	if !strings.Contains(err.Error(), "opening the folder of MusicLib "+missing) {
		t.Fatalf("the error does not name the folder: %v", err)
	}
	want := []string{"http listening", "database open", "media tools verified", "http server stopped", "database closed"}
	if msgs := logs.messages(t); !slices.Equal(msgs, want) {
		t.Fatalf("log events %q, want %q", msgs, want)
	}
	if s.scanner != nil {
		t.Fatal("the server has a scanner it could not start")
	}
}

// Step 6: sort keys that another version of the collation computed are
// computed again before the scanner starts and before the server is ready.
func TestStartupComputesTheSortKeysAgain(t *testing.T) {
	musiclib, state := fixtureLibrary(t), t.TempDir()
	prepare := func(s *server) { s.stateDir, s.musiclibDir = state, musiclib }
	first := startServer(t, prepare)
	waitReady(t, "http://"+first.addr)
	waitScanned(t, first)
	if err := first.stop(t); err != nil {
		t.Fatal(err)
	}
	if got := eventsOf(t, first.logs, "sort keys computed again"); len(got) != 0 {
		t.Fatalf("a new database logged %v", got)
	}

	ctx := t.Context()
	path := filepath.Join(state, databaseFile)
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithWriteTx(ctx, func(q *store.Queries) error {
		version, err := q.GetMeta(ctx, "collate_version")
		if err != nil || version != names.CollateVersion {
			t.Errorf("meta.collate_version after the first run: %q, %v", version, err)
		}
		if _, err := q.Conn().ExecContext(ctx, `UPDATE artists SET sort_key = x'00'`); err != nil {
			return err
		}
		return q.SetMeta(ctx, store.SetMetaParams{Key: "collate_version", Value: "golang.org/x/text v0.1.0"})
	})
	if err := errors.Join(err, db.Close()); err != nil {
		t.Fatal(err)
	}

	second := startServer(t, prepare)
	waitReady(t, "http://"+second.addr)
	waitScanned(t, second)
	// Ready means that the keys are those of this binary.
	err = second.s.store.Read(ctx, func(q *store.Queries) error {
		artists, err := q.ListArtists(ctx)
		if err != nil {
			return err
		}
		if len(artists) != 6 {
			t.Errorf("%d artists, want 6", len(artists))
		}
		for _, a := range artists {
			if string(a.SortKey) != string(names.SortKey(a.Name)) {
				t.Errorf("the sort key of %q is %x", a.Name, a.SortKey)
			}
		}
		version, err := q.GetMeta(ctx, "collate_version")
		if err != nil || version != names.CollateVersion {
			t.Errorf("meta.collate_version: %q, %v", version, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.stop(t); err != nil {
		t.Fatal(err)
	}
	msgs := second.logs.messages(t)
	i := slices.Index(msgs, "sort keys computed again")
	if i < 0 || i > slices.Index(msgs, "scanner started") || i < slices.Index(msgs, "media tools verified") {
		t.Fatalf("log events %q: the keys must be computed after the tools are verified and before the scanner starts", msgs)
	}
}

// A database whose index cannot be read stops the startup at step 6, with
// its own code.
func TestStartupRefusesAnIndexItCannotPrepare(t *testing.T) {
	state := t.TempDir()
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(state, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	// A database that lost a table: no migration brings it back.
	err = db.WithWriteTx(ctx, func(q *store.Queries) error {
		_, err := q.Conn().ExecContext(ctx, `DROP TABLE meta`)
		return err
	})
	if err := errors.Join(err, db.Close()); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	s := newServer(newLogger(logs), state, t.TempDir(), testWorkers, testScanInterval)
	err = s.run(t.Context(), listen(t))
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeLibraryIndex {
		t.Fatalf("run returned %v (code %q), want code %q", err, Code(err), CodeLibraryIndex)
	}
	if msgs := logs.messages(t); slices.Contains(msgs, "ready") || slices.Contains(msgs, "scanner started") {
		t.Fatalf("log events %q: the server went on after the refusal", msgs)
	}
}

// A stop while the scanner is examining a file ends the tool and the
// cycle: the server does not wait for the scan of a library, and the
// database is closed only after the scanner has stopped.
func TestStopCancelsTheScanner(t *testing.T) {
	musiclib := fixtureLibrary(t)
	started := filepath.Join(t.TempDir(), "started")
	// An ffprobe that reports the pinned version and then never ends.
	stuck := fakeTool(t, "ffprobe", `case "$*" in
*-version*) echo "ffprobe version 8.1.3-musiclib1 Copyright (c) the test" ;;
*) : > "`+started+`"; exec sleep 300 ;;
esac`)
	r := startServer(t, func(s *server) { s.musiclibDir, s.ffprobePath = musiclib, stuck })
	waitReady(t, "http://"+r.addr)
	deadline := time.Now().Add(60 * time.Second)
	for {
		// The server has its scanner once it has logged that it started it.
		if _, err := os.Stat(started); err == nil && len(eventsOf(t, r.logs, "scanner started")) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the scanner examined no file within 60s; logs:\n%s", r.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := r.s.scanner.Status(); st.State != library.StateScanning || st.Progress == nil || st.Progress.Discovered != 6 {
		t.Fatalf("the state during the scan: %+v", st)
	}

	begin := time.Now()
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(begin); elapsed > shutdownGrace/2 {
		t.Fatalf("the stop took %s with a scan running", elapsed)
	}
	msgs := r.logs.messages(t)
	i, j, k := slices.Index(msgs, "http server stopped"), slices.Index(msgs, "scanner stopped"), slices.Index(msgs, "database closed")
	if i < 0 || j < i || k < j {
		t.Fatalf("log events %q: the scanner must stop after the HTTP server and before the database is closed", msgs)
	}
	finished := eventsOf(t, r.logs, "scan finished")
	if len(finished) != 1 || finished[0]["ok"] != false {
		t.Fatalf("the cycle that was cut: %v", finished)
	}
	for _, ev := range r.logs.events(t) {
		if ev["level"] == "ERROR" {
			t.Fatalf("the stop logged an error: %v", ev)
		}
	}
	// Nothing is left of the scan: the store is closed and usable by the
	// next start.
	if err := r.s.store.Read(context.Background(), func(*store.Queries) error { return nil }); err == nil {
		t.Fatal("the store is still open after the stop")
	}
}

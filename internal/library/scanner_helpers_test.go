package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/store"
)

// The tests of the scanner run it on a copy of the fixture library, with a
// real database and the pinned tools, and change the library between two
// cycles as MusicLib does (DESIGN.md §12.1). Most of them run the cycles one
// by one, in the goroutine of the test: what a cycle does is then the same
// at every run. The tests of Run, of Trigger and of the timer start the
// scanner as the server does.

// logBuffer collects the JSON lines the scanner logs.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// events are the log events with that message, in order.
func (l *logBuffer) events(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(l.String()) {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		if ev["msg"] == msg {
			out = append(out, ev)
		}
	}
	return out
}

// scanEnv is a scanner on a copy of the fixture library and an empty
// index.
type scanEnv struct {
	*indexEnv
	logs *logBuffer
	sc   *Scanner
}

// testWorkers is how many albums the scanners of the tests index at once.
const testWorkers = 3

func newScanEnv(t *testing.T) *scanEnv {
	t.Helper()
	return scanEnvOf(newIndexEnv(t))
}

func scanEnvOf(ix *indexEnv) *scanEnv {
	e := &scanEnv{indexEnv: ix, logs: &logBuffer{}}
	e.sc = e.scanner(testWorkers, time.Hour)
	return e
}

// scanner makes another scanner of the same library and index, as a server
// that starts again does: it remembers nothing.
func (e *scanEnv) scanner(workers int, interval time.Duration) *Scanner {
	return NewScanner(e.ix, workers, interval, slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// cycle runs one cycle to its end and returns the status after it.
func (e *scanEnv) cycle() Status {
	e.t.Helper()
	e.sc.cycle(e.t.Context(), ReasonRequest)
	return e.sc.Status()
}

// scan runs one cycle, which must go through the library to its end
// without an error, and returns its event in the log.
func (e *scanEnv) scan() map[string]any {
	e.t.Helper()
	st := e.cycle()
	if st.State != StateIdle || st.LastScan == nil || !st.LastScan.OK || st.LastScan.Error != "" || st.LastScan.FinishedAt == nil || st.Progress != nil {
		e.t.Fatalf("the cycle did not end well: %s", statusText(st))
	}
	finished := e.logs.events(e.t, "scan finished")
	return finished[len(finished)-1]
}

// statusText writes a status out for a failure message.
func statusText(st Status) string {
	scan := "none"
	if st.LastScan != nil {
		scan = fmt.Sprintf("%+v", *st.LastScan)
	}
	progress := "none"
	if st.Progress != nil {
		progress = fmt.Sprintf("%+v", *st.Progress)
	}
	return fmt.Sprintf("state %s, last scan %s, progress %s, albums %+v, tracks %+v, maintenance %v, problems %v",
		st.State, scan, progress, st.Albums, st.Tracks, st.Maintenance, st.Problems)
}

// wantEvent checks the counters of the event of a finished cycle.
func wantEvent(t *testing.T, ev map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if n, ok := value.(int); ok {
			value = float64(n)
		}
		if ev[key] != value {
			t.Fatalf("the cycle logged %s = %v, want %v (event %v)", key, ev[key], value, ev)
		}
	}
}

// wantStatusProblems checks the problems of a status, as "path code".
func wantStatusProblems(t *testing.T, st Status, want ...string) {
	t.Helper()
	var got []string
	for _, p := range st.Problems {
		got = append(got, p.RelPath+" "+p.Code)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("problems:\n%s\nwant:\n%s\n(%v)", strings.Join(got, "\n"), strings.Join(want, "\n"), st.Problems)
	}
}

// The rows of each table of the index, which a scan must never make fewer
// (I3).
const countRows = `SELECT (SELECT count(*) FROM artists), (SELECT count(*) FROM albums), (SELECT count(*) FROM tracks)`

type rowCounts struct{ artists, albums, tracks int }

func (e *scanEnv) rows() rowCounts {
	e.t.Helper()
	var c rowCounts
	e.read(func(q *store.Queries) error {
		return q.Conn().QueryRowContext(e.t.Context(), countRows).Scan(&c.artists, &c.albums, &c.tracks)
	})
	return c
}

// ids are the ids of the tracks of an album by their path.
func (e *scanEnv) ids(albumID string) map[string]string {
	e.t.Helper()
	out := map[string]string{}
	for _, row := range e.tracks(albumID) {
		out[row.RelPath] = row.ID
	}
	return out
}

// track is a row of tracks by its id.
func (e *scanEnv) track(id string) store.Track {
	e.t.Helper()
	var albumID string
	e.read(func(q *store.Queries) error {
		return q.Conn().QueryRowContext(e.t.Context(), `SELECT album_id FROM tracks WHERE id = ?`, id).Scan(&albumID)
	})
	for _, row := range e.tracks(albumID) {
		if row.ID == id {
			return row
		}
	}
	e.t.Fatalf("no track %s", id)
	return store.Track{}
}

// meta is the value of a key of meta, "" if it was never set.
func (e *scanEnv) meta(key string) string {
	e.t.Helper()
	var value string
	e.read(func(q *store.Queries) error {
		err := q.Conn().QueryRowContext(e.t.Context(), `SELECT coalesce((SELECT value FROM meta WHERE "key" = ?), '')`, key).Scan(&value)
		return err
	})
	return value
}

// ---------------------------------------------------------------------------
// Changing the library as MusicLib does.

// outside is a folder next to the MusicLib folder, on the same filesystem:
// where an album goes when it leaves the library.
func (e *scanEnv) outside(name string) string {
	return filepath.Join(filepath.Dir(e.dir), name)
}

// trash moves an album folder out of the library, and returns where it is.
func (e *scanEnv) trash(rel string) string {
	e.t.Helper()
	to := e.outside("trash-" + strings.ReplaceAll(rel, "/", "-"))
	rename(e.t, inLib(e.dir, rel), to)
	return to
}

// moveTrack moves a track file from an album folder to another, as a move
// of tracks in MusicLib 1.2.0 leaves the two folders (NOTES.md N-054): the
// file has other tags, so other bytes, and the same audio, and both albums
// have a new receipt. An album left without tracks leaves the library.
func (e *scanEnv) moveTrack(fromRel, file, toRel, toFile string, tags ...string) {
	e.t.Helper()
	e.putTrack(e.takeTrack(fromRel, file), toRel, toFile, tags...)
}

// takeTrack takes a track file out of its album, which is rendered again
// without it, or leaves the library if it has no track left. It returns
// where the file is.
func (e *scanEnv) takeTrack(fromRel, file string) string {
	e.t.Helper()
	held := filepath.Join(e.t.TempDir(), filepath.Base(file))
	rename(e.t, e.inAlbum(fromRel, file), held)
	receipt := rerender(e.t, e.dir, fromRel)
	files, err := Classify(receipt)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(files.Audio) == 0 {
		if err := os.RemoveAll(inLib(e.dir, fromRel)); err != nil {
			e.t.Fatal(err)
		}
	}
	return held
}

// putTrack puts a track file into an album, with other tags if any are
// given, and renders the album again.
func (e *scanEnv) putTrack(held, toRel, toFile string, tags ...string) {
	e.t.Helper()
	rename(e.t, held, e.inAlbum(toRel, toFile))
	if len(tags) > 0 {
		retag(e.t, e.inAlbum(toRel, toFile), tags...)
	}
	rerender(e.t, e.dir, toRel)
}

// ---------------------------------------------------------------------------
// The data of the users, which only the API writes: here the tests write
// them with their own SQL.

func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// user creates a user and returns its id.
func (e *scanEnv) user(name string) string {
	e.t.Helper()
	id := newID(e.t)
	e.write(`INSERT INTO users (id, username, password_hash, role, created_at, password_changed_at) VALUES (?, ?, 'x', 'user', 1, 1)`, id, name)
	return id
}

// playlistCreated is created_at and updated_at of the playlists the tests
// make: long before the clock of the scanner.
const playlistCreated = int64(1_000)

// playlist creates a playlist of a user with those tracks, in that order,
// and returns its id.
func (e *scanEnv) playlist(userID, name string, trackIDs ...string) string {
	e.t.Helper()
	id := newID(e.t)
	e.write(`INSERT INTO playlists (id, user_id, name, revision, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)`,
		id, userID, name, playlistCreated, playlistCreated)
	for position, track := range trackIDs {
		e.write(`INSERT INTO playlist_items (id, playlist_id, track_id, position, added_at) VALUES (?, ?, ?, ?, ?)`,
			newID(e.t), id, track, position, 2_000+int64(position))
	}
	return id
}

// favorite makes a track a favorite of a user since the moment at.
func (e *scanEnv) favorite(userID, trackID string, at int64) {
	e.t.Helper()
	e.write(`INSERT INTO favorites (user_id, track_id, created_at) VALUES (?, ?, ?)`, userID, trackID, at)
}

// item is an item of a playlist.
type item struct {
	id, trackID       string
	position, addedAt int64
}

// items are the items of a playlist, in their order.
func (e *scanEnv) items(playlistID string) []item {
	e.t.Helper()
	var out []item
	e.read(func(q *store.Queries) error {
		rows, err := q.Conn().QueryContext(e.t.Context(),
			`SELECT id, track_id, position, added_at FROM playlist_items WHERE playlist_id = ? ORDER BY position, id`, playlistID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.trackID, &it.position, &it.addedAt); err != nil {
				return errors.Join(err, rows.Close())
			}
			out = append(out, it)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	return out
}

// wantItems checks that a playlist has the items it had, each with its id,
// its position and added_at, and that they point to those tracks.
func (e *scanEnv) wantItems(playlistID string, before []item, trackIDs ...string) {
	e.t.Helper()
	after := e.items(playlistID)
	if len(after) != len(before) || len(after) != len(trackIDs) {
		e.t.Fatalf("the playlist has %d items, had %d, want %d", len(after), len(before), len(trackIDs))
	}
	for i, it := range after {
		want := before[i]
		want.trackID = trackIDs[i]
		if it != want {
			e.t.Fatalf("item %d of the playlist: %+v, want %+v", i, it, want)
		}
	}
}

// revision is the revision of a playlist and when it was last changed.
func (e *scanEnv) revision(playlistID string) (revision, updatedAt int64) {
	e.t.Helper()
	e.read(func(q *store.Queries) error {
		return q.Conn().QueryRowContext(e.t.Context(), `SELECT revision, updated_at FROM playlists WHERE id = ?`, playlistID).Scan(&revision, &updatedAt)
	})
	return revision, updatedAt
}

// wantRevision checks the revision of a playlist; one above 1 was given by
// the scanner, with its clock.
func (e *scanEnv) wantRevision(playlistID string, want int64) {
	e.t.Helper()
	revision, updatedAt := e.revision(playlistID)
	if revision != want || (want == 1) != (updatedAt == playlistCreated) || updatedAt < playlistCreated {
		e.t.Fatalf("the playlist has revision %d, updated at %d; want revision %d", revision, updatedAt, want)
	}
}

// favorites are the favorites of a user, as "track_id@created_at", in the
// order of their ids.
func (e *scanEnv) favorites(userID string) []string {
	e.t.Helper()
	var out []string
	e.read(func(q *store.Queries) error {
		rows, err := q.Conn().QueryContext(e.t.Context(), `SELECT track_id, created_at FROM favorites WHERE user_id = ? ORDER BY track_id`, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var track string
			var at int64
			if err := rows.Scan(&track, &at); err != nil {
				return errors.Join(err, rows.Close())
			}
			out = append(out, fmt.Sprintf("%s@%d", track, at))
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	return out
}

func (e *scanEnv) wantFavorites(userID string, want ...string) {
	e.t.Helper()
	got := e.favorites(userID)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		e.t.Fatalf("the favorites of the user: %v, want %v", got, want)
	}
}

// The tables of the users, whole.
var userDumpQueries = []struct{ table, query string }{
	{"playlists", `SELECT * FROM playlists ORDER BY id`},
	{"playlist_items", `SELECT * FROM playlist_items ORDER BY id`},
	{"favorites", `SELECT * FROM favorites ORDER BY user_id, track_id`},
}

// dumpAll writes out the index and the tables of the users: two equal
// dumps are a database in which nothing was written.
func (e *scanEnv) dumpAll() string {
	e.t.Helper()
	out := e.dump()
	e.read(func(q *store.Queries) error {
		for _, d := range userDumpQueries {
			rows, err := q.Conn().QueryContext(e.t.Context(), d.query)
			if err != nil {
				return err
			}
			columns, err := rows.Columns()
			if err != nil {
				return errors.Join(err, rows.Close())
			}
			for rows.Next() {
				values := make([]any, len(columns))
				targets := make([]any, len(columns))
				for i := range values {
					targets[i] = &values[i]
				}
				if err := rows.Scan(targets...); err != nil {
					return errors.Join(err, rows.Close())
				}
				out += d.table
				for i, v := range values {
					out += fmt.Sprintf(" %s=%v", columns[i], v)
				}
				out += "\n"
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
		}
		return nil
	})
	return out
}

func (e *scanEnv) wantAllUnchanged(before string) {
	e.t.Helper()
	if after := e.dumpAll(); after != before {
		e.t.Fatalf("the database changed:\n--- before\n%s--- after\n%s", before, after)
	}
}

// ---------------------------------------------------------------------------
// Running the scanner as the server does.

// running is a scanner started with Run.
type running struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// run starts sc as the server does, and stops it when the test ends.
func (e *scanEnv) run(sc *Scanner) *running {
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		sc.Run(ctx)
	}()
	e.t.Cleanup(func() { r.stop(e.t) })
	return r
}

// stop cancels the scanner and waits for Run to return.
func (r *running) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(60 * time.Second):
		t.Fatal("the scanner did not stop within 60s")
	}
}

// eventually waits until ok is true, for at most 60 seconds.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("after 60s: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// updatedAt is the column of a dump that says when a row was last written.
var updatedAt = regexp.MustCompile(` updated_at=\d+`)

// lasting is the index without the times its rows were last written: what
// must be the same when a library goes away and comes back as it was.
func (e *scanEnv) lasting() string {
	e.t.Helper()
	return updatedAt.ReplaceAllString(e.dump(), "")
}

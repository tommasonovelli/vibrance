package library

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/media"
	"vibrance/internal/store"
)

// The tests of the indexer use the real things (DESIGN.md §12.1): a copy of
// the fixture library on disk, a real SQLite database, and the pinned
// ffprobe and ffmpeg, which are real processes. The SQL they need beyond
// the queries of sql/ is written here: it inspects, or breaks on purpose,
// what the product code must never do itself.

// errInsideWriteTx is what a guard answers when a tool is asked for while
// a write transaction is open.
var errInsideWriteTx = errors.New("a tool was run while a write transaction was open")

// guard is the Media of the tests: the real tools, behind a counter and a
// check of DESIGN.md I11. Before a tool runs, the guard takes the write
// connection of the store itself, for an empty transaction. There is one
// write connection: if the caller holds it, because it asked for a tool
// from inside a write transaction, the guard cannot have it, and the call
// fails without running anything.
type guard struct {
	tools Media
	store *store.Store
	// wait is how long the guard waits for the write connection: longer
	// than the commit of another album can take.
	wait time.Duration

	probes       atomic.Int64
	fingerprints atomic.Int64
	violations   atomic.Int64

	mu sync.Mutex
	// before, if not nil, is called before each call with its kind
	// ("probe" or "fingerprint") and its number among those of its kind,
	// from 1: the hook that lets a test change the library or the index
	// in the middle of step 4 of an indexing.
	before func(kind string, n int64)
}

func (g *guard) free() error {
	ctx, cancel := context.WithTimeout(context.Background(), g.wait)
	defer cancel()
	if err := g.store.WithWriteTx(ctx, func(*store.Queries) error { return nil }); err != nil {
		g.violations.Add(1)
		return fmt.Errorf("%w: %v", errInsideWriteTx, err)
	}
	return nil
}

func (g *guard) hook(kind string, n int64) {
	g.mu.Lock()
	before := g.before
	g.mu.Unlock()
	if before != nil {
		before(kind, n)
	}
}

func (g *guard) setHook(before func(kind string, n int64)) {
	g.mu.Lock()
	g.before = before
	g.mu.Unlock()
}

func (g *guard) Probe(ctx context.Context, f *os.File, c media.Container) (media.Info, error) {
	if err := g.free(); err != nil {
		return media.Info{}, err
	}
	g.hook("probe", g.probes.Add(1))
	return g.tools.Probe(ctx, f, c)
}

func (g *guard) Fingerprint(ctx context.Context, f *os.File, c media.Container) (string, string, error) {
	if err := g.free(); err != nil {
		return "", "", err
	}
	g.hook("fingerprint", g.fingerprints.Add(1))
	return g.tools.Fingerprint(ctx, f, c)
}

func (g *guard) Version() string { return g.tools.Version() }

// runs is how many processes the guard has started since reset.
func (g *guard) runs() int64 { return g.probes.Load() + g.fingerprints.Load() }

func (g *guard) reset() {
	g.probes.Store(0)
	g.fingerprints.Store(0)
}

// warmer records the covers the indexer asks thumbnails for.
type warmer struct {
	mu     sync.Mutex
	covers []string
}

func (w *warmer) Warm(sha string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.covers = append(w.covers, sha)
}

func (w *warmer) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	covers := w.covers
	w.covers = nil
	return covers
}

// clock gives a later millisecond at every reading, from a fixed start.
type clock struct{ ticks atomic.Int64 }

const clockStart = int64(1_790_000_000_000)

func (c *clock) now() time.Time {
	return time.UnixMilli(clockStart + c.ticks.Add(1)*1000)
}

// realTools is the adapter of the pinned ffprobe and ffmpeg.
func realTools(t *testing.T) *media.Tools {
	t.Helper()
	tools, err := media.NewTools(t.Context(), media.NewRunner(4), media.FFmpegPath, media.FFprobePath)
	if err != nil {
		t.Fatalf("NewTools: %v", err)
	}
	return tools
}

// indexEnv is an indexer on a copy of the fixture library and an empty
// index.
type indexEnv struct {
	t      *testing.T
	dir    string // the MusicLib folder
	root   *Root
	store  *store.Store
	media  *guard
	warmer *warmer
	clock  *clock
	ix     *Indexer
}

func newIndexEnv(t *testing.T) *indexEnv {
	t.Helper()
	return newIndexEnvWith(t, realTools(t))
}

func newIndexEnvWith(t *testing.T, tools Media) *indexEnv {
	t.Helper()
	e := &indexEnv{t: t, dir: copyFixture(t), warmer: &warmer{}, clock: &clock{}}
	e.root = openRoot(t, e.dir)
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "vibrance.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("closing the store: %v", err)
		}
	})
	e.store = st
	e.media = &guard{tools: tools, store: st, wait: 30 * time.Second}
	// Every test of the indexer is also a test of I11.
	t.Cleanup(func() {
		if n := e.media.violations.Load(); n != 0 {
			t.Errorf("%d tools were run while a write transaction was open (I11)", n)
		}
	})
	e.ix = NewIndexer(e.root, st, e.media, e.warmer, e.clock.now)
	return e
}

// candidate reads the album folder rel as the discovery does.
func (e *indexEnv) candidate(rel string) Candidate {
	e.t.Helper()
	c, err := readCandidate(e.root, rel)
	if err != nil {
		e.t.Fatalf("reading the candidate %q: %v", rel, err)
	}
	return c
}

// index indexes the album folder rel, which must succeed.
func (e *indexEnv) index(rel string) []Problem {
	e.t.Helper()
	warnings, err := e.ix.IndexAlbum(e.t.Context(), e.candidate(rel))
	if err != nil {
		e.t.Fatalf("IndexAlbum(%q): %v", rel, err)
	}
	return warnings
}

// indexAll indexes the six albums of the fixture.
func (e *indexEnv) indexAll() {
	e.t.Helper()
	for _, rel := range fixtureOrder {
		if warnings := e.index(rel); len(warnings) != 0 {
			e.t.Fatalf("IndexAlbum(%q): warnings %v", rel, warnings)
		}
	}
}

// fixtureOrder are the albums of the fixture, A to F.
var fixtureOrder = []string{albumA, albumB, albumC, albumD, albumE, albumF}

// read runs fn in a read transaction.
func (e *indexEnv) read(fn func(q *store.Queries) error) {
	e.t.Helper()
	if err := e.store.Read(e.t.Context(), fn); err != nil {
		e.t.Fatalf("reading the index: %v", err)
	}
}

// write runs the test's own SQL in a write transaction.
func (e *indexEnv) write(query string, args ...any) {
	e.t.Helper()
	err := e.store.WithWriteTx(e.t.Context(), func(q *store.Queries) error {
		_, err := q.Conn().ExecContext(e.t.Context(), query, args...)
		return err
	})
	if err != nil {
		e.t.Fatalf("%s: %v", query, err)
	}
}

// tracks are the rows of an album, in the order they were created.
func (e *indexEnv) tracks(albumID string) []store.Track {
	e.t.Helper()
	var rows []store.Track
	e.read(func(q *store.Queries) (err error) {
		rows, err = q.ListTracksByAlbum(e.t.Context(), albumID)
		return err
	})
	return rows
}

// trackAt is the row of the album with that path, which must be there
// once.
func (e *indexEnv) trackAt(albumID, relPath string) store.Track {
	e.t.Helper()
	var found []store.Track
	for _, row := range e.tracks(albumID) {
		if row.RelPath == relPath {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		e.t.Fatalf("the album %s has %d rows at %q, want 1", albumID, len(found), relPath)
	}
	return found[0]
}

// album is the row of an album, which must be there.
func (e *indexEnv) album(albumID string) store.GetIndexedAlbumRow {
	e.t.Helper()
	var row store.GetIndexedAlbumRow
	e.read(func(q *store.Queries) (err error) {
		row, err = q.GetIndexedAlbum(e.t.Context(), albumID)
		return err
	})
	return row
}

// The tables an indexing writes, and the statement that reads each of them
// whole. Table names cannot be parameters, so each statement is written
// out.
var dumpQueries = []struct{ table, query string }{
	{"artists", `SELECT * FROM artists ORDER BY seq`},
	{"albums", `SELECT * FROM albums ORDER BY seq`},
	{"tracks", `SELECT * FROM tracks ORDER BY seq`},
	{"search_artists", `SELECT rowid, * FROM search_artists ORDER BY rowid`},
	{"search_albums", `SELECT rowid, * FROM search_albums ORDER BY rowid`},
	{"search_tracks", `SELECT rowid, * FROM search_tracks ORDER BY rowid`},
}

// dumpIndex writes out every row of the tables an indexing writes, as one
// state of the database: two equal dumps are two equal indexes.
func dumpIndex(ctx context.Context, q *store.Queries) (string, error) {
	var b strings.Builder
	for _, d := range dumpQueries {
		rows, err := q.Conn().QueryContext(ctx, d.query)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			return "", errors.Join(err, rows.Close())
		}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				return "", errors.Join(err, rows.Close())
			}
			b.WriteString(d.table)
			for i, v := range values {
				if raw, ok := v.([]byte); ok {
					fmt.Fprintf(&b, " %s=%x", columns[i], raw)
				} else {
					fmt.Fprintf(&b, " %s=%v", columns[i], v)
				}
			}
			b.WriteByte('\n')
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func (e *indexEnv) dump() string {
	e.t.Helper()
	var out string
	e.read(func(q *store.Queries) (err error) {
		out, err = dumpIndex(e.t.Context(), q)
		return err
	})
	return out
}

// The full-text queries of the tests: the rowids of the rows that match.
const (
	matchArtists = `SELECT rowid FROM search_artists WHERE search_artists MATCH ? ORDER BY rowid`
	matchAlbums  = `SELECT rowid FROM search_albums WHERE search_albums MATCH ? ORDER BY rowid`
	matchTracks  = `SELECT rowid FROM search_tracks WHERE search_tracks MATCH ? ORDER BY rowid`
)

// match returns the rowids a full-text query finds. The words of the test
// are quoted: they are text, not the syntax of MATCH.
func (e *indexEnv) match(query, words string) []int64 {
	e.t.Helper()
	var ids []int64
	e.read(func(q *store.Queries) error {
		rows, err := q.Conn().QueryContext(e.t.Context(), query, `"`+words+`"`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return errors.Join(err, rows.Close())
			}
			ids = append(ids, id)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	return ids
}

// wantRows checks the rowids a full-text query finds.
func (e *indexEnv) wantRows(query, words string, want ...int64) {
	e.t.Helper()
	if got := e.match(query, words); !slices.Equal(got, want) {
		e.t.Fatalf("searching %q: rows %v, want %v", words, got, want)
	}
}

// wantUnchanged fails the test unless the index is exactly what the dump
// says.
func (e *indexEnv) wantUnchanged(before string) {
	e.t.Helper()
	if after := e.dump(); after != before {
		e.t.Fatalf("the index changed:\n--- before\n%s--- after\n%s", before, after)
	}
}

// ---------------------------------------------------------------------------
// Changing an album on disk, as MusicLib does (§12.2).

// inAlbum is the path on disk of a file of the album folder rel.
func (e *indexEnv) inAlbum(rel, file string) string {
	return filepath.Join(inLib(e.dir, rel), filepath.FromSlash(file))
}

// rerender writes the receipt of the album folder rel again for the files
// that are in it now, with a new build_id and the next album_revision: what
// a render of MusicLib leaves. It returns the new receipt.
func rerender(t *testing.T, dir, rel string) Receipt {
	t.Helper()
	folder := inLib(dir, rel)
	receipt, err := ParseReceipt(readFile(t, filepath.Join(folder, ReceiptName)))
	if err != nil {
		t.Fatal(err)
	}
	receipt.Files = nil
	err = filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || d.Name() == ReceiptName {
			return err
		}
		name, err := filepath.Rel(folder, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		receipt.Files = append(receipt.Files, ReceiptFile{Path: filepath.ToSlash(name), Size: int64(len(data)), SHA256: sha256Hex(data)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(receipt.Files, func(a, b ReceiptFile) int { return strings.Compare(a.Path, b.Path) })
	build, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	receipt.BuildID = build.String()
	receipt.AlbumRevision++
	writeFile(t, filepath.Join(folder, ReceiptName), string(encodeReceipt(receipt)))
	return receipt
}

// ffmpeg runs the real ffmpeg to make a test file. A path is fine here: the
// names are the test's own.
func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, media.FFmpegPath, append([]string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %q: %v\n%s", args, err, out)
	}
}

// retag replaces the file at path with a copy that has other tags and the
// same audio packets: another SHA-256, the same fingerprint, as after an
// edit in MusicLib. tags are "key=value"; an empty value removes the tag.
func retag(t *testing.T, path string, tags ...string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "retagged"+filepath.Ext(path))
	args := []string{"-i", path, "-map", "0", "-c", "copy"}
	for _, tag := range tags {
		args = append(args, "-metadata", tag)
	}
	ffmpeg(t, append(args, out)...)
	data := readFile(t, out)
	if sha256Hex(data) == sha256Hex(readFile(t, path)) {
		t.Fatalf("retagging %s changed nothing", path)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// untagged replaces the file at path with a copy of its audio that has no
// tag at all and no cover.
func untagged(t *testing.T, path string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "untagged"+filepath.Ext(path))
	ffmpeg(t, "-i", path, "-map", "0:a:0", "-c", "copy", "-map_metadata", "-1", out)
	if err := os.WriteFile(path, readFile(t, out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pngHeader is the start of a PNG image of that size: its signature and
// its IHDR chunk, which is all that reading the header of an image reads.
func pngHeader(width, height uint32) []byte {
	ihdr := make([]byte, 0, 17)
	ihdr = append(ihdr, "IHDR"...)
	ihdr = binary.BigEndian.AppendUint32(ihdr, width)
	ihdr = binary.BigEndian.AppendUint32(ihdr, height)
	ihdr = append(ihdr, 8, 0, 0, 0, 0) // 8 bits, grayscale
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, ihdr...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
}

// remove deletes a file of the test's library.
func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// f64 is a pointer to v.
func f64(v float64) *float64 { return &v }

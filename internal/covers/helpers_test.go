package covers

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/library"
	"vibrance/internal/store"
)

// The tests of this package use the real things (DESIGN.md §12.1): cover
// files in a MusicLib folder on disk, read through a real library.Root, a
// real SQLite database with the album rows the indexer would write, and a
// real cache folder. t.TempDir() is on the ext4 volume of the gate.

// logBuffer collects the JSON lines of the service's log.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// events are the log events with that message.
func (l *logBuffer) events(t *testing.T, msg string) []map[string]any {
	t.Helper()
	l.mu.Lock()
	text := l.buf.String()
	l.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("a log line that is not JSON: %q", line)
		}
		if ev["msg"] == msg {
			out = append(out, ev)
		}
	}
	return out
}

// The messages of the service's log.
const (
	logNotMade  = "the thumbnail cannot be made: serving the original cover"
	logDamaged  = "the thumbnail in the cache is damaged: making it again"
	logNotAhead = "the thumbnails were not made ahead"
	logChanged  = "the thumbnails were not made ahead: the album changed"
)

// env is a covers service on an empty library and an empty index.
type env struct {
	t      *testing.T
	dir    string // the MusicLib folder
	thumbs string // the cache folder
	store  *store.Store
	logs   *logBuffer
	s      *Service
	// made counts the thumbnails the service began to make.
	made counter
}

type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) add() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("the tests must not run as root: permissions would not apply")
	}
	e := &env{t: t, dir: filepath.Join(t.TempDir(), "musiclib"), thumbs: filepath.Join(t.TempDir(), "thumbs"), logs: &logBuffer{}}
	if err := os.MkdirAll(filepath.Join(e.dir, "library"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := library.OpenRoot(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("closing the root: %v", err)
		}
	})
	e.store, err = store.Open(t.Context(), filepath.Join(t.TempDir(), "vibrance.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := e.store.Close(); err != nil {
			t.Errorf("closing the store: %v", err)
		}
	})
	e.s = New(root, e.store, e.thumbs, slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	e.s.decoding = e.made.add
	return e
}

// testAlbum is an album of the test's library and index.
type testAlbum struct {
	id   string
	rel  string // its folder, relative to library/
	file string // the path of its cover on disk; "" without a cover
	sha  string
	data []byte
}

// addAlbum writes the cover file of a new album, if cover is not "", and
// the row the indexer would write for it: the cover columns from the file
// as it is on disk.
func (e *env) addAlbum(name, cover string, data []byte) testAlbum {
	e.t.Helper()
	a := testAlbum{id: newID(e.t), rel: "Artist/" + name, data: data}
	folder := filepath.Join(e.dir, "library", "Artist", name)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		e.t.Fatal(err)
	}
	params := store.UpsertAlbumParams{ID: a.id, ArtistID: artistID, ArtistKey: []byte{1}, Title: name, TitleKey: []byte{1},
		YearKey: 10000, RelPath: a.rel, AlbumRevision: 1, RenderVersion: "test", ReceiptHash: sha256Hex([]byte(name)),
		FirstSeenAt: 1, UpdatedAt: 1}
	if cover != "" {
		a.file, a.sha = filepath.Join(folder, cover), sha256Hex(data)
		if err := os.WriteFile(a.file, data, 0o644); err != nil {
			e.t.Fatal(err)
		}
		info, err := os.Stat(a.file)
		if err != nil {
			e.t.Fatal(err)
		}
		mime := "image/jpeg"
		if cover == "cover.png" {
			mime = "image/png"
		}
		params.CoverRel, params.CoverSha256, params.CoverMime = nullString(cover), nullString(a.sha), nullString(mime)
		params.CoverSize = sql.NullInt64{Int64: info.Size(), Valid: true}
		params.CoverMtimeNs = sql.NullInt64{Int64: info.ModTime().UnixNano(), Valid: true}
	}
	e.write(func(ctx context.Context, q *store.Queries) error {
		if err := q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artistID, Name: "Artist", SortKey: []byte{1}}); err != nil {
			return err
		}
		return q.UpsertAlbum(ctx, params)
	})
	return a
}

// artistID is the artist of every album of the tests.
const artistID = "5f2d8f4e-93a5-5a43-8d0c-2a6f0f5b9d11"

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func (e *env) write(fn func(ctx context.Context, q *store.Queries) error) {
	e.t.Helper()
	ctx := e.t.Context()
	if err := e.store.WithWriteTx(ctx, func(q *store.Queries) error { return fn(ctx, q) }); err != nil {
		e.t.Fatalf("writing the index: %v", err)
	}
}

// setUnavailable marks the album as the scanner does when its folder is
// gone.
func (e *env) setUnavailable(a testAlbum) {
	e.t.Helper()
	e.write(func(ctx context.Context, q *store.Queries) error {
		return q.SetAlbumUnavailable(ctx, store.SetAlbumUnavailableParams{UpdatedAt: 2, ID: a.id})
	})
}

// thumbFile is where the cache must keep the thumbnail of a cover
// (§9.2): <hash[0:2]>/<hash>_<size>.jpg.
func (e *env) thumbFile(sha string, size Size) string {
	name := sha + "_256.jpg"
	if size == Thumb640 {
		name = sha + "_640.jpg"
	}
	return filepath.Join(e.thumbs, sha[:2], name)
}

// open opens a cover, which must succeed, and returns its bytes and its
// type; the file is closed.
func (e *env) open(a testAlbum, size Size) ([]byte, string) {
	e.t.Helper()
	c, err := e.s.Open(e.t.Context(), a.id, size)
	if err != nil {
		e.t.Fatalf("Open(%s, %d): %v", a.rel, size, err)
	}
	return e.read(c, a)
}

// read reads an open cover to its end and closes it.
func (e *env) read(c *Cover, a testAlbum) ([]byte, string) {
	e.t.Helper()
	if c.SHA256 != a.sha {
		e.t.Errorf("the cover of %s has SHA256 %q, want %q", a.rel, c.SHA256, a.sha)
	}
	if pos, err := c.File.Seek(0, io.SeekCurrent); err != nil || pos != 0 {
		e.t.Errorf("the file of the cover is at %d (%v), want its start", pos, err)
	}
	data, err := io.ReadAll(c.File)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := c.File.Close(); err != nil {
		e.t.Fatal(err)
	}
	return data, c.MIME
}

// wantOriginal opens a thumbnail and checks that the original was served
// in its place, and that the cache has nothing of it.
func (e *env) wantOriginal(a testAlbum, size Size, mime string) {
	e.t.Helper()
	data, got := e.open(a, size)
	if got != mime || !bytes.Equal(data, a.data) {
		e.t.Fatalf("Open(%s, %d) served %d bytes of %s, want the original: %d bytes of %s", a.rel, size, len(data), got, len(a.data), mime)
	}
	e.wantNoThumb(a.sha, size)
}

func (e *env) wantNoThumb(sha string, size Size) {
	e.t.Helper()
	if _, err := os.Lstat(e.thumbFile(sha, size)); !os.IsNotExist(err) {
		e.t.Fatalf("the cache has %s (%v), want nothing", e.thumbFile(sha, size), err)
	}
}

// wantCode checks that err is an *Error with that code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || Code(err) != code {
		t.Fatalf("error %v (code %q), want code %q", err, Code(err), code)
	}
}

// decodeJPEG decodes a thumbnail, which must be a whole JPEG.
func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the thumbnail is not a valid JPEG: %v", err)
	}
	return img
}

// near reports whether the pixel of img at (x, y) is that color, give or
// take what JPEG loses.
func near(img image.Image, x, y int, want color.RGBA) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	diff := func(got uint32, want uint8) bool {
		d := int(got>>8) - int(want)
		return d > 24 || d < -24
	}
	return !diff(r, want.R) && !diff(g, want.G) && !diff(b, want.B)
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The colors of the test images.
var (
	red   = color.RGBA{R: 220, G: 30, B: 30, A: 255}
	blue  = color.RGBA{R: 30, G: 30, B: 220, A: 255}
	white = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	clear = color.RGBA{}
)

// halves is an image of width x height whose left half is one color and
// whose right half another.
func halves(width, height int, left, right color.RGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			c := left
			if x >= width/2 {
				c = right
			}
			img.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A})
		}
	}
	return img
}

// pngChunk is one chunk of a PNG file.
func pngChunk(kind string, data []byte) []byte {
	body := append([]byte(kind), data...)
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, body...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(body))
}

// blackPNG is a valid PNG image of width x height pixels, all black, with
// one bit per pixel: a few kilobytes on disk whatever its size, and width x
// height bytes in memory once decoded. With whole false it is cut after its
// header, which is all that reading its size needs.
func blackPNG(t *testing.T, width, height int, whole bool) []byte {
	t.Helper()
	ihdr := binary.BigEndian.AppendUint32(nil, uint32(width))
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(height))
	ihdr = append(ihdr, 1, 0, 0, 0, 0) // 1 bit, grayscale
	out := append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", ihdr)...)
	if !whole {
		return out
	}
	var pixels bytes.Buffer
	z := zlib.NewWriter(&pixels)
	row := make([]byte, 1+(width+7)/8) // the filter byte, then the bits
	for range height {
		if _, err := z.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	out = append(out, pngChunk("IDAT", pixels.Bytes())...)
	return append(out, pngChunk("IEND", nil)...)
}

// eventually waits until ok is true, and fails the test after 30 seconds.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 30s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lock gives path that mode until the test ends.
func lock(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	// Before t.TempDir removes the folder, which it could not otherwise.
	t.Cleanup(func() {
		if err := os.Chmod(path, info.Mode().Perm()); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
}

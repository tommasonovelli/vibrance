package covers

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vibrance/internal/store"
)

// fixtureCover is the cover MusicLib wrote for album A of the fixture
// library (testdata/FIXTURE.md).
var fixtureCover = filepath.Join("..", "..", "testdata", "library-v1", "Aurora Sines", "Alpha_ Light_", "cover.jpg")

// The original is the file of the library, byte for byte, with the type
// the index has of it.
func TestOpenOriginal(t *testing.T) {
	e := newEnv(t)
	fixture, err := os.ReadFile(fixtureCover)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, cover, mime string
		data              []byte
	}{
		{"jpeg", "cover.jpg", "image/jpeg", encodeJPEG(t, halves(300, 200, red, blue))},
		{"png", "cover.png", "image/png", encodePNG(t, halves(300, 200, clear, blue))},
		{"fixture", "cover.jpg", "image/jpeg", fixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := e.addAlbum(tc.name, tc.cover, tc.data)
			data, mime := e.open(a, Original)
			if mime != tc.mime || !bytes.Equal(data, tc.data) {
				t.Fatalf("the original: %d bytes of %s, want %d bytes of %s", len(data), mime, len(tc.data), tc.mime)
			}
		})
	}
	if n := e.made.get(); n != 0 {
		t.Fatalf("%d thumbnails were made for the originals", n)
	}
	if _, err := os.Lstat(e.thumbs); !os.IsNotExist(err) {
		t.Fatalf("the cache folder exists after serving only originals: %v", err)
	}
}

// What Open refuses, and with which code.
func TestOpenRefusals(t *testing.T) {
	e := newEnv(t)
	jpeg := encodeJPEG(t, halves(300, 200, red, blue))
	with := e.addAlbum("with", "cover.jpg", jpeg)
	without := e.addAlbum("without", "", nil)
	gone := e.addAlbum("gone", "cover.jpg", jpeg)
	e.setUnavailable(gone)

	for _, size := range []Size{Original, Thumb256, Thumb640} {
		_, err := e.s.Open(t.Context(), newID(t), size)
		wantCode(t, err, CodeAlbumNotFound)
		_, err = e.s.Open(t.Context(), "", size)
		wantCode(t, err, CodeAlbumNotFound)
		// An album that is not available has no cover to serve, also when
		// its file is still there.
		_, err = e.s.Open(t.Context(), gone.id, size)
		wantCode(t, err, CodeAlbumNotFound)
		_, err = e.s.Open(t.Context(), without.id, size)
		wantCode(t, err, CodeCoverNotFound)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := e.s.Open(ctx, with.id, size); !errors.Is(err, context.Canceled) || Code(err) != "" {
			t.Fatalf("Open with an ended context: %v (code %q)", err, Code(err))
		}
		// The end of the context is never taken for a missing album.
		if _, err := e.s.Open(ctx, newID(t), size); !errors.Is(err, context.Canceled) || Code(err) != "" {
			t.Fatalf("Open of no album with an ended context: %v (code %q)", err, Code(err))
		}
	}
	for _, size := range []Size{-1, 1, 255, 257, 512, 641, 1024} {
		if _, err := e.s.Open(t.Context(), with.id, size); err == nil || Code(err) != "" {
			t.Fatalf("Open with the size %d: %v", size, err)
		}
	}
	if n := e.made.get(); n != 0 {
		t.Fatalf("%d thumbnails were made", n)
	}
}

// The guard of the original (T13): a file that has not the size and the
// time the scanner saw is not served.
func TestOpenOriginalRefusesAReplacedFile(t *testing.T) {
	e := newEnv(t)
	data := encodeJPEG(t, halves(300, 200, red, blue))
	for _, tc := range []struct {
		name string
		harm func(t *testing.T, a testAlbum, seen os.FileInfo)
	}{
		{"another time", func(t *testing.T, a testAlbum, seen os.FileInfo) {
			later := seen.ModTime().Add(time.Second)
			if err := os.Chtimes(a.file, later, later); err != nil {
				t.Fatal(err)
			}
		}},
		{"another size, at the same time", func(t *testing.T, a testAlbum, seen os.FileInfo) {
			if err := os.WriteFile(a.file, append(bytes.Clone(data), 0), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(a.file, seen.ModTime(), seen.ModTime()); err != nil {
				t.Fatal(err)
			}
		}},
		{"gone", func(t *testing.T, a testAlbum, _ os.FileInfo) {
			if err := os.Remove(a.file); err != nil {
				t.Fatal(err)
			}
		}},
		{"its folder is gone", func(t *testing.T, a testAlbum, _ os.FileInfo) {
			if err := os.RemoveAll(filepath.Dir(a.file)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := e.addAlbum(strings.ReplaceAll(tc.name, ",", ""), "cover.jpg", data)
			seen, err := os.Stat(a.file)
			if err != nil {
				t.Fatal(err)
			}
			e.open(a, Original)
			tc.harm(t, a, seen)
			c, err := e.s.Open(t.Context(), a.id, Original)
			wantCode(t, err, CodeStale)
			if c != nil {
				t.Fatal("a cover was returned with the refusal")
			}
			if strings.Contains(err.Error(), e.dir) {
				t.Fatalf("the message holds an absolute path: %v", err)
			}
		})
	}
}

// A cover that is a symbolic link is never followed, wherever it points
// (I1): the Root refuses it.
func TestOpenFollowsNoLink(t *testing.T) {
	e := newEnv(t)
	data := encodeJPEG(t, halves(300, 200, red, blue))
	a := e.addAlbum("linked", "cover.jpg", data)
	outside := filepath.Join(t.TempDir(), "outside.jpg")
	if err := os.WriteFile(outside, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a.file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, a.file); err != nil {
		t.Fatal(err)
	}
	for _, size := range []Size{Original, Thumb256} {
		if c, err := e.s.Open(t.Context(), a.id, size); err == nil {
			t.Fatalf("Open(%d) followed a symbolic link: %+v", size, c)
		}
	}
	e.wantNoThumb(a.sha, Thumb256)
}

// The thumbnails: a whole JPEG image that fits in the square, with the
// proportions of the cover, never larger than the cover, kept in the cache
// under the hash of the cover and served from there the next time.
func TestThumbnails(t *testing.T) {
	fixture, err := os.ReadFile(fixtureCover)
	if err != nil {
		t.Fatal(err)
	}
	fixtureSize, _, err := image.DecodeConfig(bytes.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if fixtureSize.Width > 256 || fixtureSize.Height > 256 {
		t.Fatalf("the fixture cover has %d x %d pixels: the test expects one that fits in every thumbnail", fixtureSize.Width, fixtureSize.Height)
	}
	gray := image.NewGray(image.Rect(0, 0, 700, 700))
	for i := range gray.Pix {
		gray.Pix[i] = 200
	}
	paletted := image.NewPaletted(image.Rect(0, 0, 900, 600), color.Palette{color.NRGBA{}, color.NRGBA{R: 30, G: 30, B: 220, A: 255}})
	for y := range 600 {
		for x := 450; x < 900; x++ {
			paletted.SetColorIndex(x, y, 1)
		}
	}
	deep := image.NewNRGBA64(image.Rect(0, 0, 800, 800))
	for y := range 800 {
		for x := 400; x < 800; x++ {
			deep.SetNRGBA64(x, y, color.NRGBA64{R: 220 << 8, G: 30 << 8, B: 30 << 8, A: 0xffff})
		}
	}
	deepGray := image.NewGray16(image.Rect(0, 0, 500, 1000))
	for y := range 1000 {
		for x := range 500 {
			deepGray.SetGray16(x, y, color.Gray16{Y: 200 << 8})
		}
	}
	faint := image.NewNRGBA(image.Rect(0, 0, 600, 600))
	for y := range 600 {
		for x := range 600 {
			faint.SetNRGBA(x, y, color.NRGBA{R: 220, G: 30, B: 30, A: 128})
		}
	}
	lightGray := color.RGBA{R: 200, G: 200, B: 200, A: 255}
	// Red at half opacity over white.
	pink := color.RGBA{R: 237, G: 142, B: 142, A: 255}

	type dims struct{ w, h int }
	for _, tc := range []struct {
		name, cover string
		data        []byte
		at256       dims
		at640       dims
		// left and right are the colors of the middle of the two halves.
		left, right color.RGBA
	}{
		{"JPEG landscape", "cover.jpg", encodeJPEG(t, halves(1200, 800, red, blue)), dims{256, 171}, dims{640, 427}, red, blue},
		{"JPEG portrait", "cover.jpg", encodeJPEG(t, halves(300, 900, red, blue)), dims{85, 256}, dims{213, 640}, red, blue},
		{"JPEG square", "cover.jpg", encodeJPEG(t, halves(1000, 1000, blue, red)), dims{256, 256}, dims{640, 640}, blue, red},
		{"JPEG grayscale", "cover.jpg", encodeJPEG(t, gray), dims{256, 256}, dims{640, 640}, lightGray, lightGray},
		{"PNG opaque", "cover.png", encodePNG(t, halves(1000, 500, red, blue)), dims{256, 128}, dims{640, 320}, red, blue},
		{"PNG transparent", "cover.png", encodePNG(t, halves(800, 800, clear, red)), dims{256, 256}, dims{640, 640}, white, red},
		{"PNG half transparent", "cover.png", encodePNG(t, faint), dims{256, 256}, dims{600, 600}, pink, pink},
		{"PNG palette with a transparent color", "cover.png", encodePNG(t, paletted), dims{256, 171}, dims{640, 427}, white, blue},
		{"PNG 16 bit with transparency", "cover.png", encodePNG(t, deep), dims{256, 256}, dims{640, 640}, white, red},
		{"PNG 16 bit grayscale", "cover.png", encodePNG(t, deepGray), dims{128, 256}, dims{320, 640}, lightGray, lightGray},
		// Never enlarged: a cover that fits keeps its size.
		{"JPEG smaller than both", "cover.jpg", encodeJPEG(t, halves(100, 60, red, blue)), dims{100, 60}, dims{100, 60}, red, blue},
		{"PNG transparent and smaller than both", "cover.png", encodePNG(t, halves(64, 64, clear, blue)), dims{64, 64}, dims{64, 64}, white, blue},
		{"JPEG between the two", "cover.jpg", encodeJPEG(t, halves(400, 300, red, blue)), dims{256, 192}, dims{400, 300}, red, blue},
		{"JPEG of exactly 256", "cover.jpg", encodeJPEG(t, halves(256, 256, red, blue)), dims{256, 256}, dims{256, 256}, red, blue},
		{"JPEG of exactly 640", "cover.jpg", encodeJPEG(t, halves(640, 640, red, blue)), dims{256, 256}, dims{640, 640}, red, blue},
		{"JPEG one pixel too wide", "cover.jpg", encodeJPEG(t, halves(641, 320, red, blue)), dims{256, 128}, dims{640, 320}, red, blue},
		// The short side never becomes zero.
		{"PNG very wide", "cover.png", encodePNG(t, halves(3000, 2, red, blue)), dims{256, 1}, dims{640, 1}, red, blue},
		{"PNG very tall", "cover.png", encodePNG(t, halves(2, 3000, red, red)), dims{1, 256}, dims{1, 640}, red, red},
		{"the cover of the fixture", "cover.jpg", fixture, dims{fixtureSize.Width, fixtureSize.Height}, dims{fixtureSize.Width, fixtureSize.Height}, clear, clear},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			a := e.addAlbum("album", tc.cover, tc.data)
			for i, size := range []Size{Thumb256, Thumb640} {
				want := tc.at256
				if size == Thumb640 {
					want = tc.at640
				}
				data, mime := e.open(a, size)
				if mime != "image/jpeg" {
					t.Fatalf("the thumbnail of %d is %s", size, mime)
				}
				img := decodeJPEG(t, data)
				if got := img.Bounds(); got.Dx() != want.w || got.Dy() != want.h || got.Min != (image.Point{}) {
					t.Fatalf("the thumbnail of %d is %v, want %d x %d", size, got, want.w, want.h)
				}
				if tc.left != clear {
					if x, y := want.w/4, want.h/2; want.w >= 4 && !near(img, x, y, tc.left) {
						t.Errorf("the thumbnail of %d is %v at (%d, %d), want about %v", size, img.At(x, y), x, y, tc.left)
					}
					if x, y := want.w*3/4, want.h/2; !near(img, x, y, tc.right) {
						t.Errorf("the thumbnail of %d is %v at (%d, %d), want about %v", size, img.At(x, y), x, y, tc.right)
					}
				}
				cached, err := os.ReadFile(e.thumbFile(a.sha, size))
				if err != nil || !bytes.Equal(cached, data) {
					t.Fatalf("the cache does not hold what was served for %d: %v", size, err)
				}
				if n := e.made.get(); n != i+1 {
					t.Fatalf("%d thumbnails made after the first request of %d, want %d", n, size, i+1)
				}
				// The second time it comes from the cache.
				again, mime := e.open(a, size)
				if mime != "image/jpeg" || !bytes.Equal(again, data) {
					t.Fatalf("the second request of %d served something else", size)
				}
				if n := e.made.get(); n != i+1 {
					t.Fatalf("the second request of %d made a thumbnail again", size)
				}
			}
			// The cache holds the two thumbnails and nothing else.
			entries, err := os.ReadDir(filepath.Join(e.thumbs, a.sha[:2]))
			if err != nil || len(entries) != 2 {
				t.Fatalf("the folder of the cache: %v, %v", entries, err)
			}
			if events := e.logs.events(t, logNotMade); len(events) != 0 {
				t.Fatalf("the service logged %v", events)
			}
		})
	}
}

// Two albums with the same cover share its thumbnails.
func TestThumbnailIsSharedByTheAlbumsOfOneCover(t *testing.T) {
	e := newEnv(t)
	data := encodeJPEG(t, halves(800, 800, red, blue))
	first := e.addAlbum("first", "cover.jpg", data)
	second := e.addAlbum("second", "cover.jpg", data)
	one, _ := e.open(first, Thumb256)
	two, _ := e.open(second, Thumb256)
	if !bytes.Equal(one, two) || e.made.get() != 1 {
		t.Fatalf("%d thumbnails made for one cover in two albums", e.made.get())
	}
}

// The bytes a thumbnail is made of are those the index names (§9.2 step
// b): a cover replaced by another image of the same size, at the same
// time, passes every other check, and its thumbnail would stay in the
// cache under the hash of the old image.
func TestThumbnailRefusesAReplacedCover(t *testing.T) {
	e := newEnv(t)
	data := encodeJPEG(t, halves(800, 800, red, blue))
	a := e.addAlbum("replaced", "cover.jpg", data)
	seen, err := os.Stat(a.file)
	if err != nil {
		t.Fatal(err)
	}
	// Another image, as long as the first to the byte: a JPEG decoder
	// stops at the end of the image, so zeros after it are only weight.
	other := encodeJPEG(t, halves(800, 800, blue, white))
	if len(other) > len(data) {
		t.Fatalf("the second test image has %d bytes, more than the %d of the first", len(other), len(data))
	}
	other = append(other, make([]byte, len(data)-len(other))...)
	decodeJPEG(t, other)
	if len(other) != len(data) {
		t.Fatalf("the two test images have %d and %d bytes: the test needs the same size", len(data), len(other))
	}
	if err := os.WriteFile(a.file, other, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(a.file, seen.ModTime(), seen.ModTime()); err != nil {
		t.Fatal(err)
	}
	for _, size := range []Size{Thumb256, Thumb640} {
		c, err := e.s.Open(t.Context(), a.id, size)
		wantCode(t, err, CodeStale)
		if c != nil {
			t.Fatal("a cover was returned with the refusal")
		}
		e.wantNoThumb(a.sha, size)
	}
	if events := e.logs.events(t, logNotMade); len(events) != 0 {
		t.Fatalf("a stale cover was logged as a failure: %v", events)
	}

	// A cover that is gone is stale too.
	if err := os.Remove(a.file); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.Open(t.Context(), a.id, Thumb256)
	wantCode(t, err, CodeStale)

	// A thumbnail made before the replacement is the thumbnail of the
	// cover the index names: it is served.
	b := e.addAlbum("cached", "cover.jpg", data)
	before, _ := e.open(b, Thumb256)
	if err := os.WriteFile(b.file, other, 0o644); err != nil {
		t.Fatal(err)
	}
	after, _ := e.open(b, Thumb256)
	if !bytes.Equal(before, after) {
		t.Fatal("the thumbnail of the cache changed")
	}
}

// A cover of more than 40 megapixels is served as it is, and is never
// decoded: only its header is read (T20). The image of the test is a whole,
// valid PNG, which would decode into 40 MB.
func TestThumbnailOfTooManyPixelsDecodesNothing(t *testing.T) {
	e := newEnv(t)
	data := blackPNG(t, 8000, 5001, true)
	a := e.addAlbum("huge", "cover.png", data)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, size := range []Size{Thumb256, Thumb640} {
		e.wantOriginal(a, size, "image/png")
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("serving a cover of 40 megapixels allocated %d bytes: it was decoded", allocated)
	}
	// It is not a failure, and nothing is logged.
	if events := e.logs.events(t, logNotMade); len(events) != 0 {
		t.Fatalf("the service logged %v", events)
	}
}

// The limit is on the pixels the header declares, whatever the file holds:
// a header alone, of an image that could never be allocated, is refused the
// same way.
func TestThumbnailOfAnImpossibleSize(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{"one pixel over", 40_001, 1000},
		{"sixteen bits a side", 65_535, 65_535},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := e.addAlbum(tc.name, "cover.png", blackPNG(t, tc.width, tc.height, false))
			e.wantOriginal(a, Thumb256, "image/png")
		})
	}
	if events := e.logs.events(t, logNotMade); len(events) != 0 {
		t.Fatalf("the service logged %v", events)
	}
}

// A cover of exactly 40 megapixels is within the limit.
func TestThumbnailAtThePixelLimit(t *testing.T) {
	e := newEnv(t)
	a := e.addAlbum("limit", "cover.png", blackPNG(t, 8000, 5000, true))
	data, mime := e.open(a, Thumb256)
	if got := decodeJPEG(t, data).Bounds(); mime != "image/jpeg" || got.Dx() != 256 || got.Dy() != 160 {
		t.Fatalf("the thumbnail: %v of %s", got, mime)
	}
}

// A cover file of more than 20 MiB is served as it is; one of exactly 20
// MiB gets its thumbnail.
func TestThumbnailByteLimit(t *testing.T) {
	e := newEnv(t)
	small := encodePNG(t, halves(400, 400, red, blue))
	// A PNG decoder stops at the end of the image: what follows is only
	// weight.
	padded := func(n int) []byte { return append(bytes.Clone(small), make([]byte, n-len(small))...) }

	over := e.addAlbum("over", "cover.png", padded(maxCoverBytes+1))
	e.wantOriginal(over, Thumb256, "image/png")
	e.wantOriginal(over, Thumb640, "image/png")
	if events := e.logs.events(t, logNotMade); len(events) != 0 {
		t.Fatalf("the service logged %v", events)
	}

	at := e.addAlbum("at", "cover.png", padded(20<<20))
	data, mime := e.open(at, Thumb256)
	if got := decodeJPEG(t, data).Bounds(); mime != "image/jpeg" || got.Dx() != 256 || got.Dy() != 256 {
		t.Fatalf("the thumbnail of a cover of 20 MiB: %v of %s", got, mime)
	}
}

// A cover whose header is fine and whose pixels are not cannot become a
// thumbnail: the failure is logged and the original is served (§9.2).
func TestThumbnailOfADamagedCover(t *testing.T) {
	e := newEnv(t)
	jpeg := encodeJPEG(t, halves(800, 800, red, blue))
	for _, tc := range []struct {
		name, cover, mime string
		data              []byte
	}{
		{"a PNG cut after its header", "cover.png", "image/png", blackPNG(t, 64, 64, false)},
		{"a JPEG cut in its pixels", "cover.jpg", "image/jpeg", jpeg[:len(jpeg)/2]},
		{"not an image", "cover.jpg", "image/jpeg", []byte("not an image at all")},
		{"an empty file", "cover.png", "image/png", []byte{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := e.addAlbum(tc.name, tc.cover, tc.data)
			logged := len(e.logs.events(t, logNotMade))
			e.wantOriginal(a, Thumb256, tc.mime)
			events := e.logs.events(t, logNotMade)
			if len(events) != logged+1 {
				t.Fatalf("%d failures logged, want one more than %d", len(events), logged)
			}
			last := events[len(events)-1]
			if last["level"] != "WARN" || last["cover"] != a.sha || last["size"] != float64(256) || last["error"] == "" {
				t.Fatalf("the event of the failure: %v", last)
			}
		})
	}
}

// Twenty requests of one thumbnail at the same moment make it once (T20).
func TestThumbnailIsMadeOnceForConcurrentRequests(t *testing.T) {
	e := newEnv(t)
	a := e.addAlbum("wanted", "cover.jpg", encodeJPEG(t, halves(1200, 800, red, blue)))
	release := make(chan struct{})
	e.s.decoding = func() {
		e.made.add()
		<-release
	}
	const requests = 20
	results := make([][]byte, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Go(func() {
			c, err := e.s.Open(context.Background(), a.id, Thumb256)
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				return
			}
			results[i], _ = e.read(c, a)
		})
	}
	eventually(t, "the first request begins the thumbnail", func() bool { return e.made.get() >= 1 })
	// The others have the time to begin theirs, if anything let them.
	time.Sleep(300 * time.Millisecond)
	made := e.made.get()
	close(release)
	wg.Wait()
	if made != 1 || e.made.get() != 1 {
		t.Fatalf("%d thumbnails begun while the first was being made, %d in all: want 1", made, e.made.get())
	}
	for i, data := range results {
		if len(data) == 0 || !bytes.Equal(data, results[0]) {
			t.Fatalf("request %d was served %d bytes, not the thumbnail of the others", i, len(data))
		}
	}
	decodeJPEG(t, results[0])
}

// At most two covers are being decoded at once, whatever is asked for
// (T20).
func TestAtMostTwoCoversAreDecodedAtOnce(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.s.decoding = func() {
		e.made.add()
		<-release
	}
	const albums = 6
	var wg sync.WaitGroup
	for i := range albums {
		// Each album has a cover of its own: nothing is shared.
		a := e.addAlbum(string(rune('a'+i)), "cover.jpg", encodeJPEG(t, halves(300+i, 300, red, blue)))
		wg.Go(func() {
			if _, mime := e.open(a, Thumb640); mime != "image/jpeg" {
				t.Errorf("album %d was served %s", i, mime)
			}
		})
	}
	eventually(t, "two covers are being decoded", func() bool { return e.made.get() >= decodeSlots })
	time.Sleep(300 * time.Millisecond)
	inside := e.made.get()
	close(release)
	wg.Wait()
	if inside != 2 {
		t.Fatalf("%d covers were being decoded at once, want 2", inside)
	}
	if n := e.made.get(); n != albums {
		t.Fatalf("%d thumbnails made, want %d", n, albums)
	}
}

// The decode slots a cover takes are read from its header alone (§9.2): all
// of them over 16 megapixels, one otherwise, and one when the header cannot
// be read.
func TestSlotsOfACover(t *testing.T) {
	jpeg := encodeJPEG(t, halves(1200, 800, red, blue))
	for _, tc := range []struct {
		name string
		data []byte
		want int64
	}{
		{"a small JPEG", jpeg, 1},
		{"exactly 16 megapixels", blackPNG(t, 4000, 4000, false), 1},
		{"one row over 16 megapixels", blackPNG(t, 4000, 4001, false), decodeSlots},
		{"40 megapixels", blackPNG(t, 8000, 5000, false), decodeSlots},
		{"more than a thumbnail is made of", blackPNG(t, 65_535, 65_535, false), decodeSlots},
		{"a JPEG cut before its size", jpeg[:10], 1},
		{"not an image", []byte("not an image at all"), 1},
		{"an empty file", nil, 1},
	} {
		if got := slotsFor(bytes.NewReader(tc.data)); got != tc.want {
			t.Errorf("%s: %d decode slots, want %d", tc.name, got, tc.want)
		}
	}
}

// A cover of more than 16 megapixels is decoded alone: while it is, no other
// cover is, and the two that waited are then decoded together.
func TestALargeCoverIsDecodedAlone(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.s.decoding = func() {
		e.made.add()
		<-release
	}
	var wg sync.WaitGroup
	request := func(a testAlbum) {
		wg.Go(func() {
			if _, mime := e.open(a, Thumb256); mime != "image/jpeg" {
				t.Errorf("%s was served %s", a.rel, mime)
			}
		})
	}
	request(e.addAlbum("large", "cover.png", blackPNG(t, 4000, 4001, true)))
	eventually(t, "the large cover is being decoded", func() bool { return e.made.get() == 1 })
	for i := range 2 {
		request(e.addAlbum(string(rune('a'+i)), "cover.jpg", encodeJPEG(t, halves(300+i, 300, red, blue))))
	}
	// The others have the time to begin, if anything let them.
	time.Sleep(300 * time.Millisecond)
	if n := e.made.get(); n != 1 {
		t.Errorf("%d covers were being decoded with the large one, want it alone", n-1)
	}
	release <- struct{}{}
	eventually(t, "the two small covers are being decoded together", func() bool { return e.made.get() == 3 })
	close(release)
	wg.Wait()
}

// A large cover waits for both slots, and the covers asked for after it do
// not pass it: the slots are given in the order they were asked for.
func TestALargeCoverWaitsForBothSlotsAndIsNotPassed(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.s.decoding = func() {
		e.made.add()
		<-release
	}
	var wg sync.WaitGroup
	request := func(name string, data []byte) {
		a := e.addAlbum(name, "cover.png", data)
		wg.Go(func() {
			if _, mime := e.open(a, Thumb256); mime != "image/jpeg" {
				t.Errorf("%s was served %s", a.rel, mime)
			}
		})
	}
	// still fails the test if more than want covers have begun after a pause
	// in which anything that could begin would have.
	still := func(want int, when string) {
		t.Helper()
		time.Sleep(300 * time.Millisecond)
		if n := e.made.get(); n != want {
			t.Fatalf("%s: %d covers have begun to be decoded, want %d", when, n, want)
		}
	}
	small := func(i int) []byte { return encodePNG(t, halves(300+i, 300, red, blue)) }

	request("first", small(1))
	request("second", small(2))
	eventually(t, "two small covers are being decoded", func() bool { return e.made.get() == 2 })
	request("large", blackPNG(t, 4001, 4000, true))
	still(2, "a large cover asked for while two are being decoded")
	request("third", small(3))
	still(2, "a small cover asked for after the large one")
	release <- struct{}{}
	still(2, "one slot is free, behind a large cover that waits for both")
	release <- struct{}{}
	eventually(t, "the large cover is being decoded", func() bool { return e.made.get() == 3 })
	still(3, "the large cover is being decoded")
	release <- struct{}{}
	eventually(t, "the last small cover is being decoded", func() bool { return e.made.get() == 4 })
	close(release)
	wg.Wait()
}

// Two covers of exactly 16 megapixels are decoded together: the limit is
// "more than".
func TestTwoCoversAtTheLimitAreDecodedTogether(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.s.decoding = func() {
		e.made.add()
		<-release
	}
	var wg sync.WaitGroup
	for i, name := range []string{"wide", "tall"} {
		// Two covers, not one twice: 3200 x 5000 and 5000 x 3200.
		a := e.addAlbum(name, "cover.png", blackPNG(t, 3200+1800*i, 5000-1800*i, true))
		wg.Go(func() {
			if _, mime := e.open(a, Thumb256); mime != "image/jpeg" {
				t.Errorf("%s was served %s", a.rel, mime)
			}
		})
	}
	eventually(t, "the two covers are being decoded together", func() bool { return e.made.get() == 2 })
	close(release)
	wg.Wait()
}

// The slots are chosen from the header of the file as it was before the
// wait, and the cover is decoded from the bytes read after it. A file that
// was a small image then and is the large cover of the index now is not
// decoded with one slot: it is refused as a file that is changing, and the
// next request makes its thumbnail.
func TestACoverThatBecameLargeWhileItWaitedIsNotDecoded(t *testing.T) {
	e := newEnv(t)
	a := e.addAlbum("album", "cover.png", blackPNG(t, 4000, 4001, true))
	if err := os.WriteFile(a.file, encodePNG(t, halves(300, 300, red, blue)), 0o644); err != nil {
		t.Fatal(err)
	}
	e.s.decoding = func() {
		e.made.add()
		if err := os.WriteFile(a.file, a.data, 0o644); err != nil {
			t.Error(err)
		}
	}
	_, err := e.s.Open(t.Context(), a.id, Thumb256)
	wantCode(t, err, CodeStale)
	e.wantNoThumb(a.sha, Thumb256)
	if n := e.made.get(); n != 1 {
		t.Fatalf("%d thumbnails begun, want 1", n)
	}

	if _, mime := e.open(a, Thumb256); mime != "image/jpeg" {
		t.Fatalf("the next request was served %s", mime)
	}
}

// A request that gives up while it waits for its turn to decode does not
// take with it the requests that were waiting for the same thumbnail.
func TestARequestThatEndsDoesNotFailTheOthers(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.s.decoding = func() {
		e.made.add()
		<-release
	}
	// Two other covers take the two slots.
	var busy sync.WaitGroup
	for i := range decodeSlots {
		a := e.addAlbum(string(rune('a'+i)), "cover.jpg", encodeJPEG(t, halves(300+i, 300, red, blue)))
		busy.Go(func() { e.open(a, Thumb256) })
	}
	eventually(t, "the slots are taken", func() bool { return e.made.get() == decodeSlots })

	a := e.addAlbum("wanted", "cover.jpg", encodeJPEG(t, halves(800, 800, red, blue)))
	ctx, cancel := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() {
		_, err := e.s.Open(ctx, a.id, Thumb256)
		first <- err
	}()
	// The first request is waiting for a slot when the second joins it.
	time.Sleep(200 * time.Millisecond)
	second := make(chan []byte, 1)
	go func() {
		data, mime := e.open(a, Thumb256)
		if mime != "image/jpeg" {
			t.Errorf("the request that did not end was served %s", mime)
		}
		second <- data
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) || Code(err) != "" {
		t.Fatalf("the request that ended: %v", err)
	}
	close(release)
	busy.Wait()
	decodeJPEG(t, <-second)
	if events := e.logs.events(t, logNotMade); len(events) != 0 {
		t.Fatalf("the service logged %v", events)
	}
}

// A file of the cache that is not a whole thumbnail is made again, also
// when it cannot be written: the new one takes its place with a rename.
func TestDamagedThumbnailIsMadeAgain(t *testing.T) {
	e := newEnv(t)
	a := e.addAlbum("album", "cover.jpg", encodeJPEG(t, halves(800, 600, red, blue)))
	good, _ := e.open(a, Thumb256)
	large, _ := e.open(a, Thumb640)
	path := e.thumbFile(a.sha, Thumb256)

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"cut in half", good[:len(good)/2]},
		{"cut after its header", good[:200]},
		{"without its last two bytes", good[:len(good)-2]},
		{"without its last byte", good[:len(good)-1]},
		{"empty", nil},
		{"one byte", []byte{0xFF}},
		{"text", []byte("this is not a thumbnail")},
		{"zeros", make([]byte, len(good))},
		{"a PNG image", encodePNG(t, halves(100, 100, red, blue))},
		{"the thumbnail of another size", large},
	} {
		t.Run(tc.name, func(t *testing.T) {
			made, logged := e.made.get(), len(e.logs.events(t, logDamaged))
			// The damaged file is read-only: writing over it in place
			// would fail.
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.data, 0o400); err != nil {
				t.Fatal(err)
			}
			data, mime := e.open(a, Thumb256)
			if mime != "image/jpeg" || !bytes.Equal(data, good) {
				t.Fatalf("served %d bytes of %s, want the thumbnail made again", len(data), mime)
			}
			if cached, err := os.ReadFile(path); err != nil || !bytes.Equal(cached, good) {
				t.Fatalf("the cache was not repaired: %v", err)
			}
			if n := e.made.get(); n != made+1 {
				t.Fatalf("%d thumbnails made, want one more than %d", n, made)
			}
			events := e.logs.events(t, logDamaged)
			if len(events) != logged+1 || events[len(events)-1]["level"] != "WARN" || events[len(events)-1]["cover"] != a.sha {
				t.Fatalf("the events of the damaged cache: %v", events)
			}
			// What was repaired is served from the cache.
			e.open(a, Thumb256)
			if n := e.made.get(); n != made+1 {
				t.Fatal("the repaired thumbnail was made once more")
			}
		})
	}
	// The other size was never touched.
	if cached, err := os.ReadFile(e.thumbFile(a.sha, Thumb640)); err != nil || !bytes.Equal(cached, large) {
		t.Fatalf("the thumbnail of 640 changed: %v", err)
	}
	// The cache can be removed at any time (§9.2).
	if err := os.RemoveAll(e.thumbs); err != nil {
		t.Fatal(err)
	}
	if data, _ := e.open(a, Thumb256); !bytes.Equal(data, good) {
		t.Fatal("the thumbnail was not made again after the cache was removed")
	}
}

// When the thumbnail cannot be written the original is served, nothing
// partial is left in the cache, and the thumbnail is made as soon as the
// cache can be written again (§9.2).
func TestThumbnailThatCannotBeWritten(t *testing.T) {
	data := encodeJPEG(t, halves(400, 300, red, blue))
	leftovers := func(t *testing.T, dir string) []string {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}
	wantLogged := func(t *testing.T, e *env, n int) {
		t.Helper()
		if events := e.logs.events(t, logNotMade); len(events) != n {
			t.Fatalf("%d failures logged, want %d: %v", len(events), n, events)
		}
	}

	t.Run("the folder of the thumbnail is read-only", func(t *testing.T) {
		e := newEnv(t)
		a := e.addAlbum("album", "cover.jpg", data)
		shard := filepath.Join(e.thumbs, a.sha[:2])
		if err := os.MkdirAll(shard, 0o755); err != nil {
			t.Fatal(err)
		}
		lock(t, shard, 0o500)
		e.wantOriginal(a, Thumb256, "image/jpeg")
		e.wantOriginal(a, Thumb640, "image/jpeg")
		wantLogged(t, e, 2)
		if names := leftovers(t, shard); len(names) != 0 {
			t.Fatalf("left in the cache: %v", names)
		}
		if err := os.Chmod(shard, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, mime := e.open(a, Thumb256); mime != "image/jpeg" || e.made.get() != 3 {
			t.Fatalf("after the folder became writable: %s, %d thumbnails begun", mime, e.made.get())
		}
		decodeJPEG(t, mustRead(t, e.thumbFile(a.sha, Thumb256)))
	})

	t.Run("the cache folder is read-only", func(t *testing.T) {
		e := newEnv(t)
		a := e.addAlbum("album", "cover.jpg", data)
		if err := os.MkdirAll(e.thumbs, 0o755); err != nil {
			t.Fatal(err)
		}
		lock(t, e.thumbs, 0o500)
		e.wantOriginal(a, Thumb256, "image/jpeg")
		wantLogged(t, e, 1)
	})

	t.Run("the cache folder is a file", func(t *testing.T) {
		e := newEnv(t)
		a := e.addAlbum("album", "cover.jpg", data)
		if err := os.WriteFile(e.thumbs, []byte("in the way"), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := e.s.Open(t.Context(), a.id, Thumb256)
		if err != nil {
			t.Fatal(err)
		}
		if got, mime := e.read(c, a); mime != "image/jpeg" || !bytes.Equal(got, data) {
			t.Fatalf("served %d bytes of %s, want the original", len(got), mime)
		}
		wantLogged(t, e, 1)
	})

	t.Run("a folder is in the place of the thumbnail", func(t *testing.T) {
		e := newEnv(t)
		a := e.addAlbum("album", "cover.jpg", data)
		in := filepath.Join(e.thumbFile(a.sha, Thumb256), "in the way")
		if err := os.MkdirAll(in, 0o755); err != nil {
			t.Fatal(err)
		}
		c, err := e.s.Open(t.Context(), a.id, Thumb256)
		if err != nil {
			t.Fatal(err)
		}
		if got, mime := e.read(c, a); mime != "image/jpeg" || !bytes.Equal(got, data) {
			t.Fatalf("served %d bytes of %s, want the original", len(got), mime)
		}
		wantLogged(t, e, 1)
		// The temporary file of the attempt is gone.
		if names := leftovers(t, filepath.Join(e.thumbs, a.sha[:2])); len(names) != 1 {
			t.Fatalf("left in the cache: %v", names)
		}
		// The other size is not in the way of anything.
		if _, mime := e.open(a, Thumb640); mime != "image/jpeg" {
			t.Fatalf("the thumbnail of 640 was served as %s", mime)
		}
	})
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The hash of a cover becomes a path of the cache: one that is not a
// SHA-256 is refused before anything is written, whatever the index says.
func TestThumbnailRefusesAHashThatIsNotOne(t *testing.T) {
	e := newEnv(t)
	data := encodeJPEG(t, halves(300, 300, red, blue))
	for i, hash := range []string{
		"../../../../escape",
		strings.Repeat("a", 63),
		strings.Repeat("a", 65),
		strings.Repeat("A", 64),
		strings.Repeat("a", 62) + "/.",
		strings.Repeat("a", 60) + "/../",
		"",
	} {
		a := e.addAlbum(string(rune('a'+i)), "cover.jpg", data)
		e.write(func(ctx context.Context, q *store.Queries) error {
			_, err := q.Conn().ExecContext(ctx, `UPDATE albums SET cover_sha256 = ? WHERE id = ?`, hash, a.id)
			return err
		})
		c, err := e.s.Open(t.Context(), a.id, Thumb256)
		if err == nil || c != nil || Code(err) != "" {
			t.Fatalf("Open with the cover hash %q: %v, %v", hash, c, err)
		}
	}
	if _, err := os.Lstat(e.thumbs); !os.IsNotExist(err) {
		t.Fatalf("the cache folder was created: %v", err)
	}
	if n := e.made.get(); n != 0 {
		t.Fatalf("%d thumbnails begun", n)
	}
}

// The size of a thumbnail, for every size of cover: within the square, at
// least one pixel, never larger than the cover, and with one side equal to
// the square when the cover is larger than it.
func TestFit(t *testing.T) {
	for _, size := range []int{256, 640} {
		for _, width := range []int{1, 2, 255, 256, 257, 639, 640, 641, 1000, 6325, 40_000_000} {
			for _, height := range []int{1, 2, 255, 256, 257, 639, 640, 641, 1000, 6325, 40_000_000} {
				w, h := fit(width, height, size)
				switch {
				case w < 1 || h < 1 || w > size || h > size || w > width || h > height:
					t.Errorf("fit(%d, %d, %d) = %d x %d", width, height, size, w, h)
				case width <= size && height <= size && (w != width || h != height):
					t.Errorf("fit(%d, %d, %d) = %d x %d: a cover that fits was resized", width, height, size, w, h)
				case (width > size || height > size) && w != size && h != size:
					t.Errorf("fit(%d, %d, %d) = %d x %d: no side fills the square", width, height, size, w, h)
				case width >= height && w < h, width <= height && w > h:
					t.Errorf("fit(%d, %d, %d) = %d x %d: the longer side changed", width, height, size, w, h)
				}
			}
		}
	}
}

// warming runs the background work of the service until the test ends.
func (e *env) warming() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.s.Run(ctx)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				e.t.Error("Run did not return within 30s of the end of its context")
			}
		})
	}
	e.t.Cleanup(stop)
	return stop
}

func (e *env) hasThumb(sha string, size Size) bool {
	f, err := openThumb(e.thumbFile(sha, size), size)
	if err != nil {
		return false
	}
	if err := f.Close(); err != nil {
		e.t.Error(err)
	}
	return true
}

// Warm makes the two thumbnails of a cover in the background, one cover at
// a time, and never makes the caller wait.
func TestWarm(t *testing.T) {
	e := newEnv(t)
	var inside atomic.Bool
	var overlaps atomic.Int64
	e.s.decoding = func() {
		e.made.add()
		if !inside.CompareAndSwap(false, true) {
			overlaps.Add(1)
		}
		time.Sleep(20 * time.Millisecond)
		inside.Store(false)
	}
	const albums = 8
	var all []testAlbum
	for i := range albums {
		all = append(all, e.addAlbum(string(rune('a'+i)), "cover.jpg", encodeJPEG(t, halves(320+i, 300, red, blue))))
	}
	// Nothing runs yet: Warm only takes note.
	for _, a := range all[:albums/2] {
		e.s.Warm(a.sha)
	}
	time.Sleep(50 * time.Millisecond)
	if n := e.made.get(); n != 0 {
		t.Fatalf("%d thumbnails made before Run", n)
	}
	e.warming()
	for _, a := range all[albums/2:] {
		e.s.Warm(a.sha)
	}
	for _, a := range all {
		eventually(t, "the thumbnails of "+a.rel, func() bool { return e.hasThumb(a.sha, Thumb256) && e.hasThumb(a.sha, Thumb640) })
	}
	if n := e.made.get(); n != 2*albums {
		t.Fatalf("%d thumbnails made, want %d", n, 2*albums)
	}
	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d thumbnails were made while another was: Warm works on one at a time", n)
	}
	// A request finds them made.
	for _, a := range all {
		data, mime := e.open(a, Thumb640)
		if got := decodeJPEG(t, data).Bounds(); mime != "image/jpeg" || got.Dy() != 300 {
			t.Fatalf("the thumbnail of %s: %v of %s", a.rel, got, mime)
		}
	}
	// A cover that has its thumbnails is not made again; one more album
	// tells when the queue has been gone through.
	for _, a := range all {
		e.s.Warm(a.sha)
	}
	last := e.addAlbum("last", "cover.png", encodePNG(t, halves(300, 400, clear, blue)))
	e.s.Warm(last.sha)
	eventually(t, "the thumbnails of the last album", func() bool { return e.hasThumb(last.sha, Thumb256) && e.hasThumb(last.sha, Thumb640) })
	if n := e.made.get(); n != 2*albums+2 {
		t.Fatalf("%d thumbnails made, want %d", n, 2*albums+2)
	}
	if events := append(e.logs.events(t, logNotAhead), e.logs.events(t, logNotMade)...); len(events) != 0 {
		t.Fatalf("the service logged %v", events)
	}
}

// What Warm is told and cannot make leaves nothing behind and stops
// nothing: the work goes on with the next cover.
func TestWarmOfCoversItCannotMake(t *testing.T) {
	e := newEnv(t)
	jpeg := encodeJPEG(t, halves(400, 400, red, blue))

	gone := e.addAlbum("gone", "cover.jpg", encodeJPEG(t, halves(401, 400, red, blue)))
	e.setUnavailable(gone)
	replaced := e.addAlbum("replaced", "cover.jpg", jpeg)
	if err := os.WriteFile(replaced.file, encodeJPEG(t, halves(400, 400, blue, red)), 0o644); err != nil {
		t.Fatal(err)
	}
	removed := e.addAlbum("removed", "cover.jpg", encodeJPEG(t, halves(402, 400, red, blue)))
	if err := os.Remove(removed.file); err != nil {
		t.Fatal(err)
	}
	huge := e.addAlbum("huge", "cover.png", blackPNG(t, 8000, 5001, true))
	damaged := e.addAlbum("damaged", "cover.png", blackPNG(t, 64, 64, false))
	good := e.addAlbum("good", "cover.jpg", encodeJPEG(t, halves(403, 400, red, blue)))

	e.warming()
	for _, hash := range []string{
		strings.Repeat("0", 64), // of no album
		"", "../../../../escape",
		gone.sha, replaced.sha, removed.sha, huge.sha, damaged.sha,
		good.sha,
	} {
		e.s.Warm(hash)
	}
	eventually(t, "the thumbnails of the last cover", func() bool { return e.hasThumb(good.sha, Thumb256) && e.hasThumb(good.sha, Thumb640) })

	for _, a := range []testAlbum{gone, replaced, removed, huge, damaged} {
		e.wantNoThumb(a.sha, Thumb256)
		e.wantNoThumb(a.sha, Thumb640)
	}
	// An album that changed after its commit is not a failure.
	if events := e.logs.events(t, logChanged); len(events) != 2 || events[0]["level"] != "INFO" ||
		events[0]["cover"] != replaced.sha || events[1]["cover"] != removed.sha {
		t.Fatalf("the events of the albums that changed: %v", events)
	}
	// A cover that cannot be decoded is, once for each size.
	events := e.logs.events(t, logNotAhead)
	if len(events) != 2 || events[0]["level"] != "WARN" || events[0]["cover"] != damaged.sha ||
		events[0]["size"] != float64(256) || events[1]["size"] != float64(640) {
		t.Fatalf("the events of the failures: %v", events)
	}
}

// Run ends with its context, also in the middle of its queue, and Warm
// never waits, whether Run is there or not.
func TestWarmAndRunEnd(t *testing.T) {
	e := newEnv(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	e.s.decoding = func() {
		e.made.add()
		entered <- struct{}{}
		<-release
	}
	var all []testAlbum
	for i := range 5 {
		all = append(all, e.addAlbum(string(rune('a'+i)), "cover.jpg", encodeJPEG(t, halves(300+i, 300, red, blue))))
	}
	stop := e.warming()
	for _, a := range all {
		e.s.Warm(a.sha)
	}
	<-entered
	// The first thumbnail is being made, and Warm still returns at once,
	// however many times it is called.
	for range 10_000 {
		e.s.Warm(all[4].sha)
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stop()
	}()
	// The thumbnail that is being made is finished, and nothing after it
	// is begun.
	time.Sleep(100 * time.Millisecond)
	close(release)
	<-stopped
	if n := e.made.get(); n != 1 {
		t.Fatalf("%d thumbnails begun, want only the one that was being made at the stop", n)
	}
	if !e.hasThumb(all[0].sha, Thumb256) {
		t.Fatal("the thumbnail that was being made at the stop was not finished")
	}
	// After Run has returned Warm is still safe to call.
	e.s.Warm(all[1].sha)
}

// The cache is written only with a temporary file and a rename (T20), and
// the library is read only through the Root (I1): the test reads the
// sources, tests excluded, and refuses every other use of os.
func TestSourcesWriteTheCacheOnlyAtomically(t *testing.T) {
	// What the package may use of os: opening a thumbnail of the cache for
	// reading, and the steps of writeAtomic.
	allowed := map[string]bool{"File": true, "Open": true, "MkdirAll": true, "CreateTemp": true, "Rename": true, "Remove": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range file.Imports {
			if imp.Path.Value == `"io/ioutil"` || (imp.Name != nil && imp.Path.Value == `"os"`) {
				t.Errorf("%s: import %s", fset.Position(imp.Pos()), imp.Path.Value)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" && !allowed[sel.Sel.Name] {
				t.Errorf("%s uses os.%s: the cache is written by writeAtomic alone, and the library is read through the Root",
					fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source file was checked")
	}
}

package covers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // the other format of a cover (§4.4)
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/image/draw"
)

// The limits of a cover a thumbnail is made of (§9.2). A larger one is
// served as it is.
const (
	maxCoverBytes  = 20 << 20
	maxCoverPixels = 40_000_000
)

// alonePixels is the size over which a cover is decoded alone. Measured
// (TestPerfThumbnailMemory): one cover of 40 megapixels takes 260 to 440 MB
// while its thumbnail is made, by its shape and the depth of its samples,
// so two of them together would take twice that; two of 16 megapixels
// together take 285 to 410 MB, no more than the one of 40.
const alonePixels = 16_000_000

// jpegQuality is the quality of the thumbnails.
const jpegQuality = 85

// thumbMIME is the type of every thumbnail.
const thumbMIME = "image/jpeg"

// errTooLarge is why a thumbnail is not made of a cover beyond the limits.
// It is not a failure.
var errTooLarge = errors.New("the cover is beyond the limits of a thumbnail")

// thumbnail opens the thumbnail of the cover of a, from the cache or just
// made. When it cannot be made the original is served instead, unless the
// cover file is stale or cannot be read, or ctx ended.
func (s *Service) thumbnail(ctx context.Context, a album, size Size) (*Cover, error) {
	path, err := s.thumbPath(a.sha256, size)
	if err != nil {
		return nil, err
	}
	f, err := openThumb(path, size)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("the thumbnail in the cache is damaged: making it again", "cover", a.sha256, "size", int(size), "error", err.Error())
		}
		if err = s.generate(ctx, a, size, path); err == nil {
			f, err = openThumb(path, size)
		}
	}
	switch {
	case err == nil:
		return &Cover{File: f, MIME: thumbMIME, SHA256: a.sha256}, nil
	case ctx.Err() != nil:
		return nil, fmt.Errorf("covers: making a thumbnail: %w", ctx.Err())
	// A refusal of the cover file itself: the original would get the same.
	case Code(err) != "":
		return nil, err
	case !errors.Is(err, errTooLarge):
		s.log.Warn("the thumbnail cannot be made: serving the original cover", "cover", a.sha256, "size", int(size), "error", err.Error())
	}
	return s.original(a)
}

// thumbPath is where the cache keeps the thumbnail of the cover with that
// SHA-256: <hash[0:2]>/<hash>_<size>.jpg. The hash comes from the index,
// and is still refused unless it is one: it becomes a path that is written.
func (s *Service) thumbPath(hash string, size Size) (string, error) {
	if len(hash) != sha256.Size*2 {
		return "", fmt.Errorf("covers: %q is not the SHA-256 of a cover", hash)
	}
	for _, c := range []byte(hash) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("covers: %q is not the SHA-256 of a cover", hash)
		}
	}
	return filepath.Join(s.dir, hash[:2], flightKey(hash, size)+".jpg"), nil
}

// flightKey names one thumbnail: <hash>_<size>.
func flightKey(hash string, size Size) string {
	return hash + "_" + strconv.Itoa(int(size))
}

// openThumb opens a thumbnail of the cache, and refuses a file that is not
// a whole thumbnail of that size: it must begin as a JPEG that fits in the
// square and end with the end-of-image marker. The cache can be damaged
// from outside, and a file cut short by a crash is recognized this way. An
// absent thumbnail is fs.ErrNotExist.
func openThumb(path string, size Size) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := checkThumb(f, size); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

func checkThumb(f *os.File, size Size) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	config, err := jpeg.DecodeConfig(f)
	if err != nil {
		return err
	}
	if config.Width < 1 || config.Height < 1 || config.Width > int(size) || config.Height > int(size) {
		return fmt.Errorf("an image of %d x %d pixels", config.Width, config.Height)
	}
	var end [2]byte
	if _, err := f.ReadAt(end[:], info.Size()-int64(len(end))); err != nil {
		return err
	}
	if end != [2]byte{0xFF, 0xD9} {
		return errors.New("a JPEG image without its end")
	}
	_, err = f.Seek(0, io.SeekStart)
	return err
}

// generate makes the thumbnail at path, once for all the callers that ask
// for the same one at the same moment (singleflight on <hash>_<size>).
func (s *Service) generate(ctx context.Context, a album, size Size, path string) error {
	for {
		_, err, _ := s.flights.Do(flightKey(a.sha256, size), func() (any, error) {
			return nil, s.build(ctx, a, size, path)
		})
		// A shared flight waits for its decode slot with the context of the
		// caller that began it. When that context ended and this one did
		// not, the thumbnail is asked for again.
		if err == nil || ctx.Err() != nil || !(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return err
		}
	}
}

// build is the pipeline of §9.2, with at most decodeSlots covers in memory
// at once, and a cover of more than alonePixels alone.
func (s *Service) build(ctx context.Context, a album, size Size, path string) error {
	// Another flight may have made it since the caller looked.
	if f, err := openThumb(path, size); err == nil {
		return f.Close()
	}
	// The slots are asked for before the file is read whole, so that only
	// the covers that have them are in memory: the header is enough to know
	// how many.
	slots := s.slotsOf(a)
	if err := s.decodes.Acquire(ctx, slots); err != nil {
		return err
	}
	defer s.decodes.Release(slots)
	if s.decoding != nil {
		s.decoding()
	}
	data, err := s.readCover(a)
	if err != nil {
		return err
	}
	// The header was read from the file as it was then, the bytes are those
	// of the index: if they ask for more slots than are held, the file was
	// another one a moment ago, and a large cover is not decoded next to
	// another for that.
	if slotsFor(bytes.NewReader(data)) > slots {
		return stale(a, "changed while its thumbnail was being made")
	}
	thumb, err := render(data, int(size))
	if err != nil {
		return err
	}
	return writeAtomic(path, thumb)
}

// slotsOf is how many decode slots the cover of a takes, from the header of
// its file: all of them over alonePixels, otherwise one. A file whose header
// cannot be read takes one: readCover, which reads the same file next, says
// what is wrong with it.
func (s *Service) slotsOf(a album) int64 {
	f, err := s.root.Open(a.file())
	if err != nil {
		return 1
	}
	slots := slotsFor(io.LimitReader(f, maxCoverBytes))
	if err := f.Close(); err != nil {
		s.log.Warn("a cover file cannot be closed", "path", a.file(), "err", err.Error())
	}
	return slots
}

// slotsFor reads the header of an image and returns the decode slots it
// takes.
func slotsFor(r io.Reader) int64 {
	config, _, err := image.DecodeConfig(r)
	if err == nil && int64(config.Width)*int64(config.Height) > alonePixels {
		return decodeSlots
	}
	return 1
}

// readCover reads the whole cover file, at most maxCoverBytes of it, and
// checks that its bytes are those the index names: if their SHA-256 is
// another, MusicLib replaced the album since the last scan, and a
// thumbnail of them would be kept under the hash of another image.
func (s *Service) readCover(a album) ([]byte, error) {
	f, err := s.openLibrary(a)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCoverBytes+1))
	if err := errors.Join(err, f.Close()); err != nil {
		return nil, fmt.Errorf("covers: reading the cover %q: %w", a.file(), err)
	}
	if len(data) > maxCoverBytes {
		return nil, errTooLarge
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != a.sha256 {
		return nil, stale(a, "has changed since the last scan")
	}
	return data, nil
}

// render makes the JPEG thumbnail of a cover: the image scaled to fit in a
// square of size pixels, whole and never enlarged, on white where it is
// transparent.
func render(data []byte, size int) ([]byte, error) {
	// The header first: the pixels of a cover of 40 megapixels would take
	// 160 MB (T20).
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading the header of the cover: %w", err)
	}
	if config.Width < 1 || config.Height < 1 {
		return nil, fmt.Errorf("a cover of %d x %d pixels", config.Width, config.Height)
	}
	if int64(config.Width)*int64(config.Height) > maxCoverPixels {
		return nil, errTooLarge
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding the cover: %w", err)
	}
	bounds := src.Bounds()
	width, height := fit(bounds.Dx(), bounds.Dy(), size)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	// The image is laid over white: JPEG has no transparency, and the black
	// that a transparent pixel would become is never what the cover looks
	// like. An opaque image covers the white.
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("encoding the thumbnail: %w", err)
	}
	return out.Bytes(), nil
}

// fit returns the size of an image of width x height scaled to fit in a
// square of size pixels, with its proportions. An image that fits already
// keeps its size.
func fit(width, height, size int) (int, int) {
	if width <= size && height <= size {
		return width, height
	}
	if width >= height {
		return size, max(1, (height*size+width/2)/width)
	}
	return max(1, (width*size+height/2)/height), size
}

// writeAtomic writes a file of the cache so that a reader sees it whole or
// not at all: a temporary file in the same folder, then a rename (T20).
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err = errors.Join(err, tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		if rerr := os.Remove(tmp.Name()); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}
	return err
}

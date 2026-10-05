package covers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync"

	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"vibrance/internal/library"
	"vibrance/internal/store"
)

// Size is which form of a cover is asked for.
type Size int

// The three sizes of §9.2. A thumbnail fits in a square of that many
// pixels.
const (
	Original Size = 0
	Thumb256 Size = 256
	Thumb640 Size = 640
)

// The stable codes of the refusals of Open. They are the codes the API
// answers with (§8.4).
const (
	// CodeAlbumNotFound: no such album, or the album is not available.
	CodeAlbumNotFound = "album_not_found"
	// CodeCoverNotFound: the album has no cover, or its cover file cannot be
	// read now.
	CodeCoverNotFound = "cover_not_found"
	// CodeStale: the cover file is not the one the index describes. MusicLib
	// replaced the album after the last scan: nothing of the file is
	// served, and a scan will bring the index up to date (T13).
	CodeStale = "library_changing"
)

// Error is a refusal of Open, with its stable code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Code returns the code of the first *Error in err's tree, otherwise "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Cover is an open cover, ready to be served.
type Cover struct {
	// File is the image, a regular file read from its start. The caller
	// closes it.
	File *os.File
	// MIME is the type of File: "image/jpeg" or "image/png".
	MIME string
	// SHA256 is the cover_sha256 of the album: the identity of its cover,
	// whatever the size that was asked for.
	SHA256 string
}

// decodeSlots is how many covers are decoded at once (T20): a decoded cover
// of 40 megapixels takes 160 MB.
const decodeSlots = 2

// Service serves the covers of the albums of the index. It is safe for
// concurrent use.
type Service struct {
	root  *library.Root
	store *store.Store
	// dir is the folder of the cache of the thumbnails.
	dir string
	log *slog.Logger

	// flights makes one thumbnail once, however many ask for it at the
	// same moment. decodes bounds the covers held in memory.
	flights singleflight.Group
	decodes *semaphore.Weighted

	// queue are the covers Warm was told of and Run has not made yet.
	mu    sync.Mutex
	queue []string
	wake  chan struct{}

	// decoding, if not nil, is called each time a thumbnail is about to be
	// made, with a decode slot held. Only the tests set it.
	decoding func()
}

// New returns the service of the covers of the library at root and of the
// index of st. dir is the folder of the cache: it is created when the first
// thumbnail is written, and may be removed at any time.
func New(root *library.Root, st *store.Store, dir string, log *slog.Logger) *Service {
	return &Service{root: root, store: st, dir: dir, log: log,
		decodes: semaphore.NewWeighted(decodeSlots), wake: make(chan struct{}, 1)}
}

// album is what the index says of the cover of an album.
type album struct {
	relPath  string // the folder of the album, relative to library/
	coverRel string // "cover.jpg" or "cover.png"
	sha256   string
	mime     string
	size     int64
	mtimeNS  int64
}

// file is the path of the cover relative to library/. It is made only of
// columns the scanner wrote (I2).
func (a album) file() string { return a.relPath + "/" + a.coverRel }

// Open opens the cover of the album with that id, in that size (§9.2).
//
// For Original it is the file of the library, which must still have the
// size and the time the scanner saw. For a thumbnail it is the file of the
// cache, made first if it is not there or is damaged. A thumbnail that
// cannot be made is not an error: the cover is larger than the limits, or
// the image or the cache failed, and the original is served in its place.
//
// The refusals are an *Error: CodeAlbumNotFound, CodeCoverNotFound, and
// CodeStale when the file in the library is no longer the one of the
// index. Anything else is a failure: ctx ended, the database, the disk.
func (s *Service) Open(ctx context.Context, albumID string, size Size) (*Cover, error) {
	if size != Original && size != Thumb256 && size != Thumb640 {
		return nil, fmt.Errorf("covers: %d is not a size of a cover", size)
	}
	a, err := s.album(ctx, albumID)
	if err != nil {
		return nil, err
	}
	if size == Original {
		return s.original(a)
	}
	return s.thumbnail(ctx, a, size)
}

// album reads the cover of an album from the index.
func (s *Service) album(ctx context.Context, id string) (album, error) {
	var row store.GetAlbumCoverRow
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		row, err = q.GetAlbumCover(ctx, id)
		return err
	})
	switch {
	case err == nil && row.Available == 1 && row.CoverSha256.Valid:
		return album{relPath: row.RelPath, coverRel: row.CoverRel.String, sha256: row.CoverSha256.String,
			mime: row.CoverMime.String, size: row.CoverSize.Int64, mtimeNS: row.CoverMtimeNs.Int64}, nil
	case err == nil && row.Available == 1:
		return album{}, &Error{Code: CodeCoverNotFound, Msg: "the album has no cover"}
	// A read that the end of ctx interrupted can also look like a missing
	// row: the context is looked at first.
	case err == nil || (ctx.Err() == nil && errors.Is(err, sql.ErrNoRows)):
		return album{}, &Error{Code: CodeAlbumNotFound, Msg: "no such album"}
	}
	return album{}, fmt.Errorf("covers: reading the album: %w", err)
}

// stale is the refusal of a cover file that is not the one of the index.
func stale(a album, why string) error {
	return &Error{Code: CodeStale, Msg: fmt.Sprintf("the cover %q %s", a.file(), why)}
}

// original opens the cover file of the library, and refuses it unless it
// has the size and the time the scanner recorded: after a replacement the
// bytes would not be those the SHA-256 of the index names (T13).
func (s *Service) original(a album) (*Cover, error) {
	f, err := s.openLibrary(a)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && (info.Size() != a.size || info.ModTime().UnixNano() != a.mtimeNS) {
		err = stale(a, "has changed since the last scan")
	}
	if err != nil {
		if Code(err) == "" {
			err = fmt.Errorf("covers: reading the cover %q: %w", a.file(), err)
		}
		return nil, errors.Join(err, f.Close())
	}
	return &Cover{File: f, MIME: a.mime, SHA256: a.sha256}, nil
}

// openLibrary opens the cover file of the album through the Root, which
// follows no link and never leaves the library (I1). A file that is not
// there while library/ is, or that was replaced while it was being opened,
// is a replaced album (CodeStale). Any other file that cannot be opened,
// library/ that is not there among them, is CodeCoverNotFound: a scan
// would not repair it, and the index is kept as it is (I14).
func (s *Service) openLibrary(a album) (*os.File, error) {
	f, err := s.root.Open(a.file())
	switch {
	case err == nil:
		return f, nil
	case errors.Is(err, library.ErrReplaced):
		return nil, stale(a, "was replaced while it was being opened")
	case errors.Is(err, fs.ErrNotExist):
		present, perr := s.root.LibraryPresent()
		if perr == nil && present {
			return nil, stale(a, "is no longer there")
		}
		if perr != nil {
			err = errors.Join(err, perr)
		}
	}
	s.log.Warn("a cover file cannot be read", "path", a.file(), "err", err.Error())
	return nil, &Error{Code: CodeCoverNotFound, Msg: "the cover file cannot be read"}
}

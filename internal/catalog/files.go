package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"vibrance/internal/httpx"
	"vibrance/internal/library"
	"vibrance/internal/store"
)

// The codes of the refusals of the files of the tracks (DESIGN.md §8.4, §9).
const (
	CodeTrackUnavailable   = "track_unavailable"
	CodeUnsupportedProfile = "unsupported_profile"
	CodeLyricsNotFound     = "lyrics_not_found"
	// CodeLibraryChanging: a file of the library is not the one the index
	// describes, because MusicLib replaced its album after the last scan.
	// Nothing of it is served, and a scan brings the index up to date
	// (T13).
	CodeLibraryChanging = "library_changing"
)

// retryAfterSeconds is how long a client waits after library_changing
// before it asks again (§9.1): about the time a scan of the album takes.
const retryAfterSeconds = 5

// MaxLyricsBytes is the largest lyrics file that is served (§9.3).
const MaxLyricsBytes = 2 << 20

// originalProfile is the one profile of the audio of this version: the
// file as it is (D10).
const originalProfile = "original"

// Changing is the refusal of a file of the album AlbumID that is not the
// one the index describes. It is answered as 503 library_changing with
// Retry-After (its Unwrap), and the caller asks the scanner to look at the
// album again: AlbumID is read from the index, never from the request.
type Changing struct {
	AlbumID string
}

func (e *Changing) Error() string {
	return "a file of the album " + e.AlbumID + " is not the one of the index"
}

// Unwrap is the answer of the API.
func (e *Changing) Unwrap() error {
	return &httpx.Error{Status: http.StatusServiceUnavailable, Code: CodeLibraryChanging,
		Message: "The library is changing. Try again shortly.", RetryAfter: retryAfterSeconds}
}

// LibraryChanging is the refusal of a file of the album albumID that is not
// the one of the index.
func LibraryChanging(albumID string) error {
	return &Changing{AlbumID: albumID}
}

func trackUnavailable() *httpx.Error {
	return &httpx.Error{Status: http.StatusNotFound, Code: CodeTrackUnavailable, Message: "The track is not available."}
}

func unsupportedProfile() *httpx.Error {
	return &httpx.Error{Status: http.StatusBadRequest, Code: CodeUnsupportedProfile,
		Message: "This server sends the original file only: leave out profile, or send original."}
}

func lyricsNotFound() *httpx.Error {
	return &httpx.Error{Status: http.StatusNotFound, Code: CodeLyricsNotFound, Message: "The track has no lyrics."}
}

// audioTypes is the Content-Type of each codec of a track (§9.1).
var audioTypes = map[string]string{"flac": "audio/flac", "mp3": "audio/mpeg", "aac": "audio/mp4", "alac": "audio/mp4"}

// Files opens the files of the tracks of the index: their audio and their
// lyrics (DESIGN.md §9.1, §9.3). A file is read only from the path the
// index has for it, made of columns the scanner wrote, and only through
// the Root, which never leaves the library and follows no link (I1, I2,
// T11). It is safe for concurrent use.
type Files struct {
	root  *library.Root
	store *store.Store
	log   *slog.Logger
}

// NewFiles returns the files of the tracks of the index of st, in the
// library at root.
func NewFiles(root *library.Root, st *store.Store, log *slog.Logger) *Files {
	return &Files{root: root, store: st, log: log}
}

// Audio is the open audio file of a track, ready to be served.
type Audio struct {
	// File is the file, read from its start. The caller closes it.
	File *os.File
	// MIME is its Content-Type, from the codec of the track.
	MIME string
	// SHA256 is the file_sha256 of the track: the SHA-256 of File.
	SHA256 string
}

// OpenAudio opens the audio file of the track with that id, in this order
// (§9.1): 404 track_not_found when there is no such track, 404
// track_unavailable when it is not available, 400 unsupported_profile when
// profile is given and is not "original". Then the file is opened and must
// still be a regular file with the size and the time the scanner saw:
// otherwise MusicLib replaced it, and the answer is 503 library_changing
// (T13), a *Changing for the caller to ask the scanner to look at the album
// again. So is a file that is not there while library/ is, and one that was
// replaced while it was being opened.
//
// A file that cannot be read for another reason, library/ that is not there
// among them, is 404 track_unavailable: the index is kept as it is, and the
// track comes back when its file does (I14).
func (f *Files) OpenAudio(ctx context.Context, id string, profile *string) (*Audio, error) {
	row, err := f.track(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.Available != 1 {
		return nil, trackUnavailable()
	}
	if profile != nil && *profile != originalProfile {
		return nil, unsupportedProfile()
	}
	mime, ok := audioTypes[row.Codec]
	if !ok {
		// The schema allows no other codec.
		return nil, fmt.Errorf("catalog: the codec %q has no type", row.Codec)
	}
	rel := row.AlbumRelPath + "/" + row.RelPath
	file, err := f.root.Open(rel)
	if err != nil {
		return nil, f.unreadable(row.AlbumID, rel, err)
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Size() != row.FileSize || info.ModTime().UnixNano() != row.FileMtimeNs) {
		err = LibraryChanging(row.AlbumID)
	}
	if err != nil {
		var refusal *httpx.Error
		if !errors.As(err, &refusal) {
			err = fmt.Errorf("catalog: reading the audio file %q: %w", rel, err)
		}
		if cerr := file.Close(); cerr != nil {
			return nil, fmt.Errorf("catalog: closing the audio file %q: %w", rel, cerr)
		}
		return nil, err
	}
	return &Audio{File: file, MIME: mime, SHA256: row.FileSha256}, nil
}

// unreadable answers an audio file of the album albumID that cannot be
// opened. A file that is not there while library/ is, or that was replaced
// while it was being opened, means that MusicLib replaced or moved its
// album; anything else means that nothing can be read now.
func (f *Files) unreadable(albumID, rel string, err error) error {
	if errors.Is(err, library.ErrReplaced) {
		return LibraryChanging(albumID)
	}
	if errors.Is(err, fs.ErrNotExist) {
		present, perr := f.root.LibraryPresent()
		if perr == nil && present {
			return LibraryChanging(albumID)
		}
		if perr != nil {
			err = errors.Join(err, perr)
		}
	}
	f.log.Warn("an audio file cannot be read", "path", rel, "err", err.Error())
	return trackUnavailable()
}

// Lyrics is the lyrics file of a track, as it is on disk.
type Lyrics struct {
	Data []byte
	// SHA256 is the lyrics_sha256 of the track: the SHA-256 of Data.
	SHA256 string
}

// ReadLyrics reads the lyrics file of the track with that id (§9.3): 404
// track_not_found when there is no such track; 404 lyrics_not_found when
// it has no lyrics, is not available, or its file is not there, cannot be
// read or is over MaxLyricsBytes; 503 library_changing when its SHA-256 is
// not the one of the index (a *Changing). A lyrics file replaced while it
// was being opened is answered like one that is gone, as §9.3 wants of the
// lyrics.
func (f *Files) ReadLyrics(ctx context.Context, id string) (Lyrics, error) {
	row, err := f.track(ctx, id)
	if err != nil {
		return Lyrics{}, err
	}
	if row.Available != 1 || !row.LyricsRel.Valid {
		return Lyrics{}, lyricsNotFound()
	}
	rel := row.AlbumRelPath + "/" + row.LyricsRel.String
	data, err := f.read(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Lyrics{}, lyricsNotFound()
	case err != nil:
		f.log.Warn("a lyrics file cannot be read", "path", rel, "err", err.Error())
		return Lyrics{}, lyricsNotFound()
	case len(data) > MaxLyricsBytes:
		return Lyrics{}, lyricsNotFound()
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != row.LyricsSha256.String {
		return Lyrics{}, LibraryChanging(row.AlbumID)
	}
	return Lyrics{Data: data, SHA256: row.LyricsSha256.String}, nil
}

// read reads the file at rel, at most one byte more than MaxLyricsBytes.
func (f *Files) read(rel string) ([]byte, error) {
	file, err := f.root.Open(rel)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxLyricsBytes+1))
	if err := errors.Join(err, file.Close()); err != nil {
		return nil, err
	}
	return data, nil
}

// track reads what the index says of the files of a track.
func (f *Files) track(ctx context.Context, id string) (store.GetTrackFileRow, error) {
	var row store.GetTrackFileRow
	err := f.store.Read(ctx, func(q *store.Queries) (err error) {
		row, err = q.GetTrackFile(ctx, id)
		return err
	})
	switch {
	case err == nil:
		return row, nil
	case noRows(ctx, err):
		return row, trackNotFound()
	}
	return row, fmt.Errorf("catalog: reading the files of a track: %w", err)
}

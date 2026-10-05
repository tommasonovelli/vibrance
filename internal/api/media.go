package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"vibrance/internal/catalog"
	"vibrance/internal/covers"
	"vibrance/internal/httpx"
	"vibrance/internal/lyrics"
)

// The operations of the files of the library: the audio, the covers and
// the lyrics (DESIGN.md §9). What they open and refuse is the services';
// these choose the headers and serve.

// Media is what the operations of the files of the library need.
type Media struct {
	// Files opens the audio and the lyrics of the tracks.
	Files *catalog.Files
	// Covers opens the covers of the albums and their thumbnails.
	Covers *covers.Service
	// Rescan asks the scanner for a cycle and returns at once: a file of
	// the library is not the one the index describes (T13). Requests for
	// cycles coalesce.
	Rescan func()
}

// The Cache-Control of the files (§8.1, §9.2). A file whose URL names its
// content may be kept for a year; any other is kept only if the client
// asks again with If-None-Match.
const (
	revalidate = "private, no-cache"
	immutable  = "private, max-age=31536000, immutable"
)

// GetTrackAudio serves the audio file of a track as it is, with ranges and
// conditions (§9.1).
func (s Server) GetTrackAudio(ctx context.Context, req GetTrackAudioRequestObject) (GetTrackAudioResponseObject, error) {
	m := s.media.Load()
	a, err := m.Files.OpenAudio(ctx, req.Id.String(), req.Params.Profile)
	if err != nil {
		return nil, m.refuse(err)
	}
	return newFileResponse(ctx, a.File, a.MIME, `"`+a.SHA256+`"`, revalidate)
}

// coverSizes are the values of the parameter size, with the size of the
// covers service each one asks for (§9.2).
var coverSizes = map[GetAlbumCoverParamsSize]covers.Size{
	GetAlbumCoverParamsSizeN256:     covers.Thumb256,
	GetAlbumCoverParamsSizeN640:     covers.Thumb640,
	GetAlbumCoverParamsSizeOriginal: covers.Original,
}

// GetAlbumCover serves the cover of an album, or one of its thumbnails
// (§9.2). The cover served is always the current one; v, the hash the
// client knows, only chooses how long it may be kept.
func (s Server) GetAlbumCover(ctx context.Context, req GetAlbumCoverRequestObject) (GetAlbumCoverResponseObject, error) {
	m := s.media.Load()
	name := GetAlbumCoverParamsSizeN640
	if req.Params.Size != nil {
		name = *req.Params.Size
	}
	size, ok := coverSizes[name]
	if !ok {
		// The validation of the parameters refuses any other value.
		return nil, httpx.InvalidParameter("size")
	}
	c, err := m.Covers.Open(ctx, req.Id.String(), size)
	if err != nil {
		return nil, m.refuse(coverRefusal(err))
	}
	cache := revalidate
	if req.Params.V != nil && *req.Params.V == c.SHA256 {
		cache = immutable
	}
	return newFileResponse(ctx, c.File, c.MIME, `"`+c.SHA256+"-"+string(name)+`"`, cache)
}

// coverRefusal is the answer of the API to a refusal of the covers service;
// any other error is left as it is.
func coverRefusal(err error) error {
	switch covers.Code(err) {
	case covers.CodeAlbumNotFound:
		return &httpx.Error{Status: http.StatusNotFound, Code: catalog.CodeAlbumNotFound, Message: "There is no such album."}
	case covers.CodeCoverNotFound:
		return &httpx.Error{Status: http.StatusNotFound, Code: covers.CodeCoverNotFound, Message: "The album has no cover."}
	case covers.CodeStale:
		return catalog.LibraryChanging()
	}
	return err
}

// GetTrackLyrics returns the lyrics of a track, read from its LRC file
// (§9.3).
func (s Server) GetTrackLyrics(ctx context.Context, req GetTrackLyricsRequestObject) (GetTrackLyricsResponseObject, error) {
	m := s.media.Load()
	l, err := m.Files.ReadLyrics(ctx, req.Id.String())
	if err != nil {
		return nil, m.refuse(err)
	}
	parsed := lyrics.Parse(l.Data)
	body := Lyrics{Synced: parsed.Synced, Lines: make([]LyricsLine, 0, len(parsed.Lines))}
	for _, line := range parsed.Lines {
		body.Lines = append(body.Lines, LyricsLine{TimeMs: line.TimeMS, Text: line.Text})
	}
	return GetTrackLyrics200JSONResponse{Body: body, Headers: GetTrackLyrics200ResponseHeaders{ETag: `"` + l.SHA256 + `"`}}, nil
}

// refuse starts a scan when err says that a file of the library is not the
// one of the index (§9.1 step 4), and returns err.
func (m *Media) refuse(err error) error {
	var e *httpx.Error
	if errors.As(err, &e) && e.Code == catalog.CodeLibraryChanging {
		m.Rescan()
	}
	return err
}

// fileResponse is an open file of the library, or of the cache of the
// thumbnails, and the headers it is served with. It is served with
// http.ServeContent, which answers Range (206, 416), If-Range,
// If-None-Match (304) and HEAD; the generated responses would copy the
// whole file and know nothing of the request.
type fileResponse struct {
	r                 *http.Request
	file              *os.File
	mime, etag, cache string
}

// newFileResponse serves file, which it owns from now on, to the request of
// ctx.
func newFileResponse(ctx context.Context, file *os.File, mime, etag, cache string) (fileResponse, error) {
	r := requestOf(ctx)
	if r == nil {
		// Register puts the request in the context of every operation.
		return fileResponse{}, errors.Join(errors.New("api: an operation was reached without its request"), file.Close())
	}
	return fileResponse{r: r, file: file, mime: mime, etag: etag, cache: cache}, nil
}

func (f fileResponse) VisitGetTrackAudioResponse(w http.ResponseWriter) error { return f.serve(w) }

func (f fileResponse) VisitGetAlbumCoverResponse(w http.ResponseWriter) error { return f.serve(w) }

// serve writes the file, and closes it.
func (f fileResponse) serve(w http.ResponseWriter) (err error) {
	defer func() {
		if cerr := f.file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("api: closing a served file: %w", cerr)
		}
	}()
	// A track lasts longer than any reasonable WriteTimeout of the server
	// (T12): this response has none. Every wrapper of the ResponseWriter
	// has Unwrap, so the controller reaches the connection; only a writer
	// that is no connection (a recorder of the tests) has no deadline to
	// lift.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return fmt.Errorf("api: lifting the write deadline: %w", err)
	}
	// Set before ServeContent, which would otherwise guess the type from
	// the bytes. No name and no time: the ETag is the only validator, and
	// no Content-Disposition tells the name of the file (§9.1).
	h := w.Header()
	h.Set("Content-Type", f.mime)
	h.Set("ETag", f.etag)
	h.Set("Cache-Control", f.cache)
	http.ServeContent(w, f.r, "", time.Time{}, f.file)
	return nil
}

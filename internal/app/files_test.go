package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/catalog"
	"vibrance/internal/lyrics"
	"vibrance/internal/media"
	"vibrance/internal/names"
	"vibrance/internal/store"
)

// The operations of the files of the library (DESIGN.md §9, step S16): the
// audio, the covers and the lyrics, on the fixture library indexed by the
// real indexer with the pinned tools, and on files the tests change on
// disk. Every answer is also checked against the specification (I10).

// The albums of the fixture library that catalog_test.go does not name.
const (
	albumC = "01a0f459-ebc2-7821-a442-bd56dc181ce3" // M4A with AAC
	albumD = "01a0f459-ebc1-7b68-9fa3-85c82e63e1b1" // M4A with ALAC
)

// fixtureTrack is the id of the n-th track (1 is the first) of an album of
// the fixture library, which it indexes in w the first time.
func (w *world) fixtureTrack(albumID string, n int) string {
	w.t.Helper()
	if !w.indexed {
		w.indexFixture()
	}
	a, err := catalog.New(w.store).GetAlbum(w.t.Context(), w.admin.id, albumID)
	if err != nil || len(a.Tracks) < n {
		w.t.Fatalf("track %d of the album %s: %v", n, albumID, err)
	}
	return a.Tracks[n-1].ID
}

// filePath is where the audio file of a track is on disk, and lyricsPath
// its lyrics file, from what the index says.
func (w *world) filePath(trackID string) string {
	w.t.Helper()
	row := w.trackFile(trackID)
	return w.inLibrary(row.AlbumRelPath + "/" + row.RelPath)
}

func (w *world) lyricsPath(trackID string) string {
	w.t.Helper()
	row := w.trackFile(trackID)
	return w.inLibrary(row.AlbumRelPath + "/" + row.LyricsRel.String)
}

func (w *world) trackFile(trackID string) store.GetTrackFileRow {
	w.t.Helper()
	var row store.GetTrackFileRow
	err := w.store.Read(w.t.Context(), func(q *store.Queries) (err error) {
		row, err = q.GetTrackFile(w.t.Context(), trackID)
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return row
}

// inLibrary is the path on disk of rel, a path relative to library/.
func (w *world) inLibrary(rel string) string {
	return filepath.Join(w.musiclib, "library", filepath.FromSlash(rel))
}

// fetch sends a GET (or another method) of a file operation with the
// headers given ("Name: value"), as the session c; the answer conforms.
func (w *world) fetch(method, path string, c credential, headers ...string) *httptest.ResponseRecorder {
	w.t.Helper()
	req := w.request(method, path, nil)
	for _, h := range headers {
		name, value, _ := strings.Cut(h, ": ")
		req.Header.Set(name, value)
	}
	c(req)
	rec := send(w.s.http.Handler, req)
	assertConforms(w.t, method+" "+path+" "+strings.Join(headers, ", "), req, rec)
	return rec
}

// setLyricsSHA256 writes in the index the SHA-256 of the lyrics file of a
// track as it is now on disk, as a scan would.
func (w *world) setLyricsSHA256(trackID string) {
	w.t.Helper()
	sum := sha256.Sum256(readFile(w.t, w.lyricsPath(trackID)))
	err := w.store.WithWriteTx(w.t.Context(), func(q *store.Queries) error {
		_, err := q.Conn().ExecContext(w.t.Context(), `UPDATE tracks SET lyrics_sha256 = ? WHERE id = ?`, hex.EncodeToString(sum[:]), trackID)
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// wantHeader checks the value of a header of a response.
func wantHeader(t *testing.T, where string, rec *httptest.ResponseRecorder, name, want string) {
	t.Helper()
	if got := rec.Header().Values(name); (want == "" && len(got) != 0) || (want != "" && (len(got) != 1 || got[0] != want)) {
		t.Errorf("%s: %s %q, want %q", where, name, got, want)
	}
}

// wantBody checks the status and the body of a response.
func wantBody(t *testing.T, where string, rec *httptest.ResponseRecorder, status int, body []byte) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("%s: status %d, want %d (%s)", where, rec.Code, status, redacted(rec))
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("%s: a body of %d bytes, want %d", where, rec.Body.Len(), len(body))
	}
}

// wantChanging checks the answer to a file that is not the one of the
// index (§9.1 step 4): 503 library_changing with Retry-After, nothing of
// the file, and one more scan asked for.
func (w *world) wantChanging(where string, rec *httptest.ResponseRecorder, rescans int32) {
	w.t.Helper()
	wantCode(w.t, where, rec, http.StatusServiceUnavailable, catalog.CodeLibraryChanging)
	wantHeader(w.t, where, rec, "Retry-After", "5")
	for _, name := range []string{"ETag", "Content-Range", "Accept-Ranges"} {
		wantHeader(w.t, where, rec, name, "")
	}
	if got := w.rescans.Load(); got != rescans {
		w.t.Errorf("%s: %d scans asked for, want %d", where, got, rescans)
	}
}

// §9.1: the file as it is, with the type of its codec, its SHA-256 as the
// ETag, revalidated before use, and no name of the file; HEAD has the same
// headers and no body.
func TestTrackAudio(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	for album, mime := range map[string]string{albumA: "audio/flac", albumB: "audio/mpeg", albumC: "audio/mp4", albumD: "audio/mp4"} {
		id := w.fixtureTrack(album, 1)
		data := readFile(t, w.filePath(id))
		path := "/tracks/" + id + "/audio"
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			where := method + " " + mime
			rec := w.fetch(method, path, anna)
			body := data
			if method == http.MethodHead {
				body = nil
			}
			wantBody(t, where, rec, http.StatusOK, body)
			wantHeader(t, where, rec, "Content-Type", mime)
			wantHeader(t, where, rec, "ETag", `"`+sha256Hex(data)+`"`)
			wantHeader(t, where, rec, "Cache-Control", "private, no-cache")
			wantHeader(t, where, rec, "Accept-Ranges", "bytes")
			wantHeader(t, where, rec, "Content-Length", strconv.Itoa(len(data)))
			wantHeader(t, where, rec, "Content-Disposition", "")
			wantHeader(t, where, rec, "Last-Modified", "")
		}
	}
	if w.rescans.Load() != 0 {
		t.Errorf("%d scans asked for, want none", w.rescans.Load())
	}
	// A cookie is enough: <audio src> needs no code.
	wantBody(t, "with the cookie", w.fetch(http.MethodGet, "/tracks/"+w.fixtureTrack(albumB, 2)+"/audio",
		sessionCookie(w.cookie(w.anna).Token)), http.StatusOK, readFile(t, w.filePath(w.fixtureTrack(albumB, 2))))
}

// §9.1 step 6: ranges, If-Range and the conditions are those of
// http.ServeContent, with the ETag of the file.
func TestTrackAudioRanges(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	id := w.fixtureTrack(albumA, 1)
	data := readFile(t, w.filePath(id))
	n := len(data)
	path := "/tracks/" + id + "/audio"
	etag := `"` + sha256Hex(data) + `"`

	for _, tc := range []struct {
		name    string
		headers []string
		status  int
		body    []byte
		rangeH  string
	}{
		{"the start", []string{"Range: bytes=0-99"}, http.StatusPartialContent, data[:100], fmt.Sprintf("bytes 0-99/%d", n)},
		{"to the end", []string{"Range: bytes=100-"}, http.StatusPartialContent, data[100:], fmt.Sprintf("bytes 100-%d/%d", n-1, n)},
		{"a suffix", []string{"Range: bytes=-50"}, http.StatusPartialContent, data[n-50:], fmt.Sprintf("bytes %d-%d/%d", n-50, n-1, n)},
		{"the last byte", []string{fmt.Sprintf("Range: bytes=%d-", n-1)}, http.StatusPartialContent, data[n-1:], fmt.Sprintf("bytes %d-%d/%d", n-1, n-1, n)},
		{"If-Range of the file", []string{"Range: bytes=0-99", "If-Range: " + etag}, http.StatusPartialContent, data[:100], fmt.Sprintf("bytes 0-99/%d", n)},
		// The range belongs to another version: the whole file, never bytes
		// of two versions (§9.1, A13).
		{"If-Range of another file", []string{"Range: bytes=0-99", `If-Range: "` + strings.Repeat("0", 64) + `"`}, http.StatusOK, data, ""},
		{"If-None-Match of the file", []string{"If-None-Match: " + etag}, http.StatusNotModified, nil, ""},
		{"If-None-Match of another file", []string{`If-None-Match: "other"`}, http.StatusOK, data, ""},
		{"If-None-Match of the file, weak", []string{"If-None-Match: W/" + etag}, http.StatusNotModified, nil, ""},
	} {
		rec := w.fetch(http.MethodGet, path, anna, tc.headers...)
		wantBody(t, tc.name, rec, tc.status, tc.body)
		wantHeader(t, tc.name, rec, "Content-Range", tc.rangeH)
		wantHeader(t, tc.name, rec, "ETag", etag)
	}

	// A range beyond the end of the file.
	rec := w.fetch(http.MethodGet, path, anna, fmt.Sprintf("Range: bytes=%d-", n))
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("a range beyond the file: %d", rec.Code)
	}
	wantHeader(t, "416", rec, "Content-Range", fmt.Sprintf("bytes */%d", n))

	// Two ranges come in two parts.
	rec = w.fetch(http.MethodGet, path, anna, "Range: bytes=0-1,10-11")
	if rec.Code != http.StatusPartialContent || !strings.HasPrefix(rec.Header().Get("Content-Type"), "multipart/byteranges; boundary=") {
		t.Fatalf("two ranges: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	// If-Match of another file.
	rec = w.fetch(http.MethodGet, path, anna, `If-Match: "other"`)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-Match of another file: %d", rec.Code)
	}

	// HEAD of a range.
	rec = w.fetch(http.MethodHead, path, anna, "Range: bytes=0-99")
	wantBody(t, "HEAD of a range", rec, http.StatusPartialContent, nil)
	wantHeader(t, "HEAD of a range", rec, "Content-Length", "100")
}

// §9.1 steps 1 and 2, in their order: the track, its availability, then the
// profile.
func TestTrackAudioRefusals(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	id := w.fixtureTrack(albumB, 1)
	data := readFile(t, w.filePath(id))

	wantCode(t, "an id that is not a UUID", w.fetch(http.MethodGet, "/tracks/x/audio", anna), http.StatusBadRequest, "invalid_request")
	wantCode(t, "no such track", w.fetch(http.MethodGet, "/tracks/"+someID+"/audio", anna), http.StatusNotFound, catalog.CodeTrackNotFound)
	wantCode(t, "no such track, another profile", w.fetch(http.MethodGet, "/tracks/"+someID+"/audio?profile=opus", anna),
		http.StatusNotFound, catalog.CodeTrackNotFound)
	wantBody(t, "the profile original", w.fetch(http.MethodGet, "/tracks/"+id+"/audio?profile=original", anna), http.StatusOK, data)
	for _, profile := range []string{"opus", "", "Original", "original%20"} {
		wantCode(t, "the profile "+profile, w.fetch(http.MethodGet, "/tracks/"+id+"/audio?profile="+profile, anna),
			http.StatusBadRequest, catalog.CodeUnsupportedProfile)
	}

	w.unavailable(albumB)
	wantCode(t, "a track that is not available", w.fetch(http.MethodGet, "/tracks/"+id+"/audio", anna),
		http.StatusNotFound, catalog.CodeTrackUnavailable)
	wantCode(t, "a track that is not available, another profile", w.fetch(http.MethodGet, "/tracks/"+id+"/audio?profile=opus", anna),
		http.StatusNotFound, catalog.CodeTrackUnavailable)
	// The track itself is still there (§8.2).
	wantStatus(t, "the track", w.get("/tracks/"+id, w.anna), http.StatusOK)
	if w.rescans.Load() != 0 {
		t.Errorf("%d scans asked for, want none", w.rescans.Load())
	}
}

// §9.1 step 4, T13: a file that MusicLib replaced since the last scan is
// not served, not even a range of it, and a scan is asked for. A file that
// cannot be read for another reason, library/ that is gone among them,
// makes the track unavailable without a scan (I14).
func TestTrackAudioOfAReplacedFile(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	audio := func(album string, n int) string { return "/tracks/" + w.fixtureTrack(album, n) + "/audio" }
	rescans := int32(0)
	changing := func(where, path string, change func(file string)) {
		t.Helper()
		change(w.inLibraryOf(path))
		rescans++
		w.wantChanging(where, w.fetch(http.MethodGet, path, anna), rescans)
		rescans++
		w.wantChanging(where+", a range", w.fetch(http.MethodGet, path, anna, "Range: bytes=0-9"), rescans)
	}
	changing("other bytes of the same size", audio(albumA, 1), func(file string) {
		data := readFile(t, file)
		data[len(data)/2] ^= 0xff
		writeFile(t, file, data)
	})
	changing("the same bytes at another time", audio(albumA, 2), func(file string) {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, time.Time{}, info.ModTime().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	})
	changing("another size at the same time", audio(albumA, 3), func(file string) {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, file, append(readFile(t, file), 0))
		if err := os.Chtimes(file, time.Time{}, info.ModTime()); err != nil {
			t.Fatal(err)
		}
	})
	changing("a file that is gone", audio(albumB, 1), func(file string) {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	})
	changing("an album folder renamed", audio(albumC, 1), func(file string) {
		folder := filepath.Dir(file)
		if err := os.Rename(folder, folder+" (renamed)"); err != nil {
			t.Fatal(err)
		}
	})

	// The other track of B is still served: only what changed is refused.
	other := w.fixtureTrack(albumB, 2)
	wantBody(t, "a file that did not change", w.fetch(http.MethodGet, "/tracks/"+other+"/audio", anna), http.StatusOK,
		readFile(t, w.filePath(other)))

	unreadable := func(where, path string) {
		t.Helper()
		wantCode(t, where, w.fetch(http.MethodGet, path, anna), http.StatusNotFound, catalog.CodeTrackUnavailable)
		if got := w.rescans.Load(); got != rescans {
			t.Errorf("%s: %d scans asked for, want %d", where, got, rescans)
		}
	}
	// A symbolic link is never followed (I1, T11).
	link := w.inLibraryOf(audio(albumD, 1))
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(w.filePath(other), link); err != nil {
		t.Fatal(err)
	}
	unreadable("a symbolic link", audio(albumD, 1))
	if os.Geteuid() != 0 {
		file := w.inLibraryOf(audio(albumD, 2))
		if err := os.Chmod(file, 0); err != nil {
			t.Fatal(err)
		}
		unreadable("a file that cannot be read", audio(albumD, 2))
	}
	library := filepath.Join(w.musiclib, "library")
	if err := os.Rename(library, library+".off"); err != nil {
		t.Fatal(err)
	}
	unreadable("library/ is gone", "/tracks/"+other+"/audio")
	if ev := eventsOf(t, w.logs, "an audio file cannot be read"); len(ev) == 0 || ev[0]["level"] != "WARN" {
		t.Errorf("the files that cannot be read are not logged: %v", ev)
	}
	if err := os.Rename(library+".off", library); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, "library/ is back", w.fetch(http.MethodGet, "/tracks/"+other+"/audio", anna), http.StatusOK)
}

// inLibraryOf is the file on disk of the audio operation at path.
func (w *world) inLibraryOf(path string) string {
	w.t.Helper()
	return w.filePath(strings.TrimSuffix(strings.TrimPrefix(path, "/tracks/"), "/audio"))
}

// §9.2: the cover of an album and its thumbnails, the cache headers that v
// chooses, and the ETag of each size.
func TestAlbumCover(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	w.fixtureTrack(albumA, 1)
	cover := readFile(t, w.inLibrary("Aurora Sines/Alpha_ Light_/cover.jpg"))
	hash := sha256Hex(cover)
	coverPath := "/albums/" + albumA + "/cover"
	original, err := jpeg.DecodeConfig(bytes.NewReader(cover))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ query, size string }{{"", "640"}, {"?size=256", "256"}, {"?size=640", "640"}, {"?size=original", "original"}} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			where := method + " " + tc.query
			rec := w.fetch(method, coverPath+tc.query, anna)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d (%s)", where, rec.Code, redacted(rec))
			}
			wantHeader(t, where, rec, "Content-Type", "image/jpeg")
			wantHeader(t, where, rec, "ETag", `"`+hash+"-"+tc.size+`"`)
			wantHeader(t, where, rec, "Cache-Control", "private, no-cache")
			if method == http.MethodHead {
				if rec.Body.Len() != 0 {
					t.Errorf("%s: a body", where)
				}
				continue
			}
			if tc.size == "original" {
				wantBody(t, where, rec, http.StatusOK, cover)
				continue
			}
			// The cover is smaller than both squares: never enlarged.
			if got, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes())); err != nil || got.Width != original.Width || got.Height != original.Height {
				t.Errorf("%s: a thumbnail of %dx%d (%v), want %dx%d", where, got.Width, got.Height, err, original.Width, original.Height)
			}
		}
	}

	// v names the current cover: it may be kept for ever. Any other v, or
	// none, gets the current cover too, to revalidate.
	rec := w.fetch(http.MethodGet, coverPath+"?size=original&v="+hash, anna)
	wantBody(t, "the v of the cover", rec, http.StatusOK, cover)
	wantHeader(t, "the v of the cover", rec, "Cache-Control", "private, max-age=31536000, immutable")
	rec = w.fetch(http.MethodGet, coverPath+"?size=original&v="+strings.Repeat("0", 64), anna)
	wantBody(t, "the v of another cover", rec, http.StatusOK, cover)
	wantHeader(t, "the v of another cover", rec, "Cache-Control", "private, no-cache")
	// The URL of the catalog is the one that may be kept.
	album := decode[api.AlbumDetail](t, w.get("/albums/"+albumA, w.anna))
	if album.Cover == nil || album.Cover.Hash != hash {
		t.Fatalf("the cover of the album: %+v", album.Cover)
	}
	rec = w.fetch(http.MethodGet, strings.TrimPrefix(album.Cover.Url, api.BasePath), anna)
	wantHeader(t, "the URL of the catalog", rec, "Cache-Control", "private, max-age=31536000, immutable")

	// If-None-Match is about one size.
	wantBody(t, "If-None-Match of the size", w.fetch(http.MethodGet, coverPath+"?size=256", anna, `If-None-Match: "`+hash+`-256"`),
		http.StatusNotModified, nil)
	wantStatus(t, "If-None-Match of another size", w.fetch(http.MethodGet, coverPath+"?size=256", anna, `If-None-Match: "`+hash+`-640"`),
		http.StatusOK)
	// A range, as for the audio.
	wantBody(t, "a range", w.fetch(http.MethodGet, coverPath+"?size=original", anna, "Range: bytes=0-9"), http.StatusPartialContent, cover[:10])

	wantCode(t, "an album without a cover", w.fetch(http.MethodGet, "/albums/"+albumB+"/cover", anna), http.StatusNotFound, "cover_not_found")
	wantCode(t, "no such album", w.fetch(http.MethodGet, "/albums/"+someID+"/cover", anna), http.StatusNotFound, catalog.CodeAlbumNotFound)
	wantCode(t, "a size that does not exist", w.fetch(http.MethodGet, coverPath+"?size=128", anna), http.StatusBadRequest, "invalid_request")
	w.unavailable(albumA)
	wantCode(t, "an album that is not available", w.fetch(http.MethodGet, coverPath, anna), http.StatusNotFound, catalog.CodeAlbumNotFound)
	if w.rescans.Load() != 0 {
		t.Errorf("%d scans asked for, want none", w.rescans.Load())
	}
}

// §9.2, T13: a cover that MusicLib replaced is not served, and a scan is
// asked for, whether the original is asked for or a thumbnail that is not
// in the cache yet. A thumbnail in the cache is of the bytes its hash
// names: it is still served.
func TestAlbumCoverReplaced(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	w.fixtureTrack(albumA, 1)
	coverPath := "/albums/" + albumA + "/cover"
	wantStatus(t, "the thumbnail of 640", w.fetch(http.MethodGet, coverPath+"?size=640", anna), http.StatusOK)

	var other bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	if err := jpeg.Encode(&other, img, nil); err != nil {
		t.Fatal(err)
	}
	file := w.inLibrary("Aurora Sines/Alpha_ Light_/cover.jpg")
	writeFile(t, file, other.Bytes())

	w.wantChanging("the original", w.fetch(http.MethodGet, coverPath+"?size=original", anna), 1)
	w.wantChanging("a thumbnail not in the cache", w.fetch(http.MethodGet, coverPath+"?size=256", anna), 2)
	wantStatus(t, "a thumbnail in the cache", w.fetch(http.MethodGet, coverPath+"?size=640", anna), http.StatusOK)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	w.wantChanging("a cover that is gone", w.fetch(http.MethodGet, coverPath+"?size=original", anna), 3)
}

// §9.3: the lyrics of a track as lines, from its LRC file, which must still
// be the one of the index.
func TestTrackLyrics(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	id := w.fixtureTrack(albumA, 1)
	path := "/tracks/" + id + "/lyrics"
	file := w.lyricsPath(id)
	two := int64(1000)
	zero := int64(0)

	rec := w.fetch(http.MethodGet, path, anna)
	wantStatus(t, "synced lyrics", rec, http.StatusOK)
	wantHeader(t, "synced lyrics", rec, "ETag", `"`+sha256Hex(readFile(t, file))+`"`)
	got := decode[api.Lyrics](t, rec)
	want := api.Lyrics{Synced: true, Lines: []api.LyricsLine{{TimeMs: &zero, Text: "The first line"}, {TimeMs: &two, Text: "The second line"}}}
	if b, _ := json.Marshal(got); string(b) != mustJSON(t, want) {
		t.Errorf("synced lyrics: %s, want %s", b, mustJSON(t, want))
	}
	// net/http sends no body with HEAD; the headers are those of GET.
	head := w.fetch(http.MethodHead, path, anna)
	wantStatus(t, "HEAD", head, http.StatusOK)
	wantHeader(t, "HEAD", head, "ETag", rec.Header().Get("ETag"))

	// Lyrics without a time.
	writeFile(t, file, []byte("\xef\xbb\xbfFirst words\r\n\r\nSecond words\r\n"))
	w.setLyricsSHA256(id)
	got = decode[api.Lyrics](t, w.fetch(http.MethodGet, path, anna))
	want = api.Lyrics{Synced: false, Lines: []api.LyricsLine{{Text: "First words"}, {Text: "Second words"}}}
	if b, _ := json.Marshal(got); string(b) != mustJSON(t, want) {
		t.Errorf("lyrics without a time: %s, want %s", b, mustJSON(t, want))
	}

	// The largest file that is read, and one byte more.
	large := bytes.Repeat([]byte("la\n"), catalog.MaxLyricsBytes/3+1)[:catalog.MaxLyricsBytes]
	writeFile(t, file, large)
	w.setLyricsSHA256(id)
	wantStatus(t, "2 MiB of lyrics", w.fetch(http.MethodGet, path, anna), http.StatusOK)
	writeFile(t, file, append(large, '\n'))
	w.setLyricsSHA256(id)
	wantCode(t, "more than 2 MiB of lyrics", w.fetch(http.MethodGet, path, anna), http.StatusNotFound, catalog.CodeLyricsNotFound)

	// A file that is not the one of the index.
	writeFile(t, file, []byte("[00:01.00]Other words\n"))
	w.wantChanging("lyrics replaced", w.fetch(http.MethodGet, path, anna), 1)

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "a lyrics file that is gone", w.fetch(http.MethodGet, path, anna), http.StatusNotFound, catalog.CodeLyricsNotFound)
	wantCode(t, "a track without lyrics", w.fetch(http.MethodGet, "/tracks/"+w.fixtureTrack(albumA, 2)+"/lyrics", anna),
		http.StatusNotFound, catalog.CodeLyricsNotFound)
	wantCode(t, "no such track", w.fetch(http.MethodGet, "/tracks/"+someID+"/lyrics", anna), http.StatusNotFound, catalog.CodeTrackNotFound)
	wantCode(t, "an id that is not a UUID", w.fetch(http.MethodGet, "/tracks/x/lyrics", anna), http.StatusBadRequest, "invalid_request")
	writeFile(t, file, []byte("[00:01.00]Other words\n"))
	w.setLyricsSHA256(id)
	wantStatus(t, "lyrics back", w.fetch(http.MethodGet, path, anna), http.StatusOK)
	w.unavailable(albumA)
	wantCode(t, "a track that is not available", w.fetch(http.MethodGet, path, anna), http.StatusNotFound, catalog.CodeLyricsNotFound)
	if w.rescans.Load() != 1 {
		t.Errorf("%d scans asked for, want 1", w.rescans.Load())
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The lyrics the API answers are those of the parser of internal/lyrics.
func TestTrackLyricsAreThoseOfTheParser(t *testing.T) {
	w := newWorld(t, apiOrigin)
	id := w.fixtureTrack(albumA, 1)
	file := w.lyricsPath(id)
	writeFile(t, file, []byte("[offset:+500]\n[00:02.00][00:01.00]Twice <00:01.50>word\n[00:03.00]\nno time\n"))
	w.setLyricsSHA256(id)
	parsed := lyrics.Parse(readFile(t, file))
	got := decode[api.Lyrics](t, w.fetch(http.MethodGet, "/tracks/"+id+"/lyrics", w.as(w.anna)))
	if got.Synced != parsed.Synced || len(got.Lines) != len(parsed.Lines) || len(got.Lines) != 3 {
		t.Fatalf("got %+v, the parser %+v", got, parsed)
	}
	for i, line := range parsed.Lines {
		if got.Lines[i].Text != line.Text || *got.Lines[i].TimeMs != *line.TimeMS {
			t.Errorf("line %d: %+v, the parser %+v", i, got.Lines[i], line)
		}
	}
}

// bigTrack writes, by hand as the indexer would, an available track whose
// file is size bytes of noise, and returns its id and the file.
func (w *world) bigTrack(size int) (id string, data []byte) {
	w.t.Helper()
	ctx := w.t.Context()
	data = make([]byte, size)
	rng := rand.NewChaCha8([32]byte{1})
	if _, err := rng.Read(data); err != nil {
		w.t.Fatal(err)
	}
	folder := w.inLibrary("Big/Album")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		w.t.Fatal(err)
	}
	file := filepath.Join(folder, "01 - Big.flac")
	writeFile(w.t, file, data)
	info, err := os.Stat(file)
	if err != nil {
		w.t.Fatal(err)
	}
	const artist, album, track = "0199a5c0-0000-5000-8000-0000000000a2", "0199a5c0-0000-7000-8000-0000000000b2", "0199a5c0-0000-7000-8000-0000000000c2"
	err = w.store.WithWriteTx(ctx, func(q *store.Queries) error {
		key := names.SortKey("Big")
		if err := q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artist, Name: "Big", SortKey: key}); err != nil {
			return err
		}
		if err := q.UpsertAlbum(ctx, store.UpsertAlbumParams{ID: album, ArtistID: artist, ArtistKey: key,
			Title: "Album", TitleKey: names.SortKey("Album"), YearKey: 10000, RelPath: "Big/Album", AlbumRevision: 1,
			RenderVersion: "r", ReceiptHash: "h", FirstSeenAt: 1, UpdatedAt: 1}); err != nil {
			return err
		}
		if err := q.UpsertTrack(ctx, store.UpsertTrackParams{ID: track, AlbumID: album, Fingerprint: "f", FpVersion: "v",
			Occurrence: 1, Disc: 1, No: 1, Title: "Big", Artist: "Big", RelPath: "01 - Big.flac", FileSize: info.Size(),
			FileMtimeNs: info.ModTime().UnixNano(), FileSha256: sha256Hex(data), Codec: "flac", SampleRate: 44100, Channels: 2,
			UpdatedAt: 1}); err != nil {
			return err
		}
		return q.UpdateAlbumCounters(ctx, album)
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return track, data
}

// served is a server of net/http on a loopback port, with the handler of w
// and a write timeout far shorter than a stream.
func (w *world) served(writeTimeout time.Duration) *httptest.Server {
	w.t.Helper()
	srv := httptest.NewUnstartedServer(w.s.http.Handler)
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	w.t.Cleanup(srv.Close)
	return srv
}

// streamRequest is a GET of the audio of a track, to srv, as a.
func (w *world) streamRequest(ctx context.Context, srv *httptest.Server, id string, token string) *http.Request {
	w.t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+api.BasePath+"/tracks/"+id+"/audio", nil)
	if err != nil {
		w.t.Fatal(err)
	}
	req.Host = w.host
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// waitGoroutines waits until no more goroutines run than before, and fails
// the test if that does not happen within 10 seconds.
func waitGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			t.Fatalf("%d goroutines, %d before:\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// openFiles counts the descriptors of the process open on path.
func openFiles(t *testing.T, path string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && target == path {
			n++
		}
	}
	return n
}

// T12: a track takes longer to send than the write timeout of the server.
// The operation lifts the deadline for its response, so that a slow client
// gets the whole file; once it is over no goroutine and no file is left.
func TestTrackAudioToASlowClient(t *testing.T) {
	w := newWorld(t, apiOrigin)
	id, data := w.bigTrack(50 << 20)
	token := w.token(w.anna).Token
	before := runtime.NumGoroutine()
	const writeTimeout = 200 * time.Millisecond
	srv := w.served(writeTimeout)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	started := time.Now()
	resp, err := client.Do(w.streamRequest(t.Context(), srv, id, token))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	hash := sha256.New()
	buf := make([]byte, 256<<10)
	total := 0
	for {
		n, err := resp.Body.Read(buf)
		hash.Write(buf[:n])
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("after %d bytes and %v: %v", total, time.Since(started), err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 4*writeTimeout {
		t.Fatalf("the stream took %v: not slow enough to prove anything", elapsed)
	}
	if total != len(data) || hex.EncodeToString(hash.Sum(nil)) != sha256Hex(data) {
		t.Fatalf("%d bytes received, want the %d of the file", total, len(data))
	}
	srv.Close()
	client.CloseIdleConnections()
	waitGoroutines(t, before)
	if n := openFiles(t, w.filePath(id)); n != 0 {
		t.Fatalf("%d descriptors of the file are open", n)
	}
}

// A client that goes away in the middle of a stream: the operation ends,
// closes the file, and leaves no goroutine.
func TestTrackAudioClientGoesAway(t *testing.T) {
	w := newWorld(t, apiOrigin)
	id, _ := w.bigTrack(50 << 20)
	token := w.token(w.anna).Token
	before := runtime.NumGoroutine()
	srv := w.served(time.Minute)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	ctx, cancel := context.WithCancel(t.Context())
	resp, err := client.Do(w.streamRequest(ctx, srv, id, token))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, resp.Body, 1<<20); err != nil {
		t.Fatal(err)
	}
	if n := openFiles(t, w.filePath(id)); n != 1 {
		t.Fatalf("%d descriptors of the file are open during the stream, want 1", n)
	}
	cancel()
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	// The access log has its line when the operation has returned.
	deadline := time.Now().Add(10 * time.Second)
	for len(eventsOf(t, w.logs, "request")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the operation did not end; logs:\n%s", w.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := openFiles(t, w.filePath(id)); n != 0 {
		t.Fatalf("%d descriptors of the file are open after the client went away", n)
	}
	srv.Close()
	client.CloseIdleConnections()
	waitGoroutines(t, before)
}

// Many clients at once ask for ranges of the same file and for the
// thumbnails of the same cover: each gets its own bytes (-race).
func TestFilesConcurrently(t *testing.T) {
	w := newWorld(t, apiOrigin)
	id := w.fixtureTrack(albumA, 1)
	data := readFile(t, w.filePath(id))
	token := w.token(w.anna).Token
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := range 16 {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(g), 1))
			for range 10 {
				start := rng.IntN(len(data))
				end := start + rng.IntN(len(data)-start)
				req := w.request(http.MethodGet, "/tracks/"+id+"/audio", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
				rec := send(w.s.http.Handler, req)
				if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), data[start:end+1]) {
					errs <- fmt.Sprintf("bytes %d-%d: %d with %d bytes", start, end, rec.Code, rec.Body.Len())
					return
				}
				req = w.request(http.MethodGet, "/albums/"+albumA+"/cover?size="+[]string{"256", "640"}[rng.IntN(2)], nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec = send(w.s.http.Handler, req)
				if _, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes())); rec.Code != http.StatusOK || err != nil {
					errs <- fmt.Sprintf("a thumbnail: %d, %v", rec.Code, err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// testReceipt is a receipt of MusicLib (§4.2), with its keys in the order
// MusicLib writes them.
type testReceipt struct {
	SchemaVersion int               `json:"schema_version"`
	AlbumID       string            `json:"album_id"`
	BuildID       string            `json:"build_id"`
	AlbumRevision int64             `json:"album_revision"`
	RenderVersion string            `json:"render_version"`
	Files         []testReceiptFile `json:"files"`
}

type testReceiptFile struct {
	Path   string `json:"relative_path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// rerender writes the receipt of the album folder again for the files that
// are in it now, with the next album_revision: what a render of MusicLib
// leaves.
func rerender(t *testing.T, folder string) {
	t.Helper()
	const name = ".musiclib.json"
	var receipt testReceipt
	if err := json.Unmarshal(readFile(t, filepath.Join(folder, name)), &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Files = nil
	err := filepath.WalkDir(folder, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || d.Name() == name {
			return err
		}
		rel, err := filepath.Rel(folder, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		receipt.Files = append(receipt.Files, testReceiptFile{Path: filepath.ToSlash(rel), Size: int64(len(data)), SHA256: sha256Hex(data)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt.AlbumRevision++
	writeFile(t, filepath.Join(folder, name), []byte(mustJSON(t, receipt)))
}

// retag replaces the file at path with a copy that has another title and
// the same audio packets, as an edit in MusicLib does.
func retag(t *testing.T, path, title string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "retagged"+filepath.Ext(path))
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	// A path is fine here: the names are the test's own.
	cmd := exec.CommandContext(ctx, media.FFmpegPath, "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-i", path, "-map", "0", "-c", "copy", "-metadata", "title="+title, out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, b)
	}
	writeFile(t, path, readFile(t, out))
}

// client is a client of a running server, signed in as its first admin.
type client struct {
	t     *testing.T
	base  string
	token string
}

func newClient(t *testing.T, r *running) *client {
	t.Helper()
	in, err := r.s.sessions.Load().CreateToken(t.Context(), adminName, adminPassword, "tests", "")
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, base: "http://" + r.addr + api.BasePath, token: in.Token}
}

// get asks for path, with the headers given ("Name: value").
func (c *client) get(path string, headers ...string) (*http.Response, []byte) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodGet, c.base+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	for _, h := range headers {
		name, value, _ := strings.Cut(h, ": ")
		req.Header.Set(name, value)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	body, rerr := io.ReadAll(resp.Body)
	if err := errors.Join(rerr, resp.Body.Close()); err != nil {
		c.t.Fatal(err)
	}
	return resp, body
}

// waitRescans waits until the scanner of r has finished n cycles asked for
// by the operations of the files.
func waitRescans(t *testing.T, r *running, n int) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		done := 0
		for _, ev := range eventsOf(t, r.logs, "scan finished") {
			if ev["reason"] == "file_replaced" {
				done++
			}
		}
		if done >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d cycles asked for by the files finished within 60s, want %d; logs:\n%s", done, n, r.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// §9.1, T13, A13, with the real server, scanner and tools: MusicLib
// replaces a file, renames an album and deletes a track. Until a scan has
// seen it the file is not served and a scan starts by itself; after it, the
// new file is served under its new ETag, and a range of the old one gets
// the whole new file.
func TestTrackAudioThroughAScan(t *testing.T) {
	musiclib := fixtureLibrary(t)
	r := startServer(t, func(s *server) { s.musiclibDir = musiclib })
	waitReady(t, "http://"+r.addr)
	waitScanned(t, r)
	c := newClient(t, r)
	trackOf := func(album string, n int) string {
		resp, body := c.get("/albums/" + album)
		var a api.AlbumDetail
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &a) != nil {
			t.Fatalf("the album %s: %d", album, resp.StatusCode)
		}
		return a.Tracks[n-1].Id
	}
	folderB := filepath.Join(musiclib, "library", "Bravo Tones", "Beta MP3")
	id := trackOf(albumB, 1)
	resp, old := c.get("/tracks/" + id + "/audio")
	oldETag := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusOK || oldETag != `"`+sha256Hex(old)+`"` {
		t.Fatalf("before: %d, ETag %q", resp.StatusCode, oldETag)
	}

	file := filepath.Join(folderB, "01 - One.mp3")
	retag(t, file, "One, edited")
	rerender(t, folderB)
	resp, _ = c.get("/tracks/"+id+"/audio", "Range: bytes=0-99", "If-Range: "+oldETag)
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("a replaced file: %d, Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	waitRescans(t, r, 1)
	now := readFile(t, file)
	resp, body := c.get("/tracks/"+id+"/audio", "Range: bytes=0-99", "If-Range: "+oldETag)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, now) || resp.Header.Get("ETag") != `"`+sha256Hex(now)+`"` {
		t.Fatalf("after the scan, a range of the old file: %d with %d bytes, ETag %q", resp.StatusCode, len(body), resp.Header.Get("ETag"))
	}
	if trackOf(albumB, 1) != id {
		t.Fatal("the track has another id after the edit")
	}

	// An album folder renamed.
	idC := trackOf(albumC, 1)
	folderC := filepath.Join(musiclib, "library", "Charlie Waves", "Gamma AAC")
	if err := os.Rename(folderC, folderC+" (renamed)"); err != nil {
		t.Fatal(err)
	}
	if resp, _ := c.get("/tracks/" + idC + "/audio"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("an album folder renamed: %d", resp.StatusCode)
	}
	waitRescans(t, r, 2)
	if resp, _ := c.get("/tracks/" + idC + "/audio"); resp.StatusCode != http.StatusOK {
		t.Fatalf("an album folder renamed, after the scan: %d", resp.StatusCode)
	}

	// A track deleted.
	id2 := trackOf(albumB, 2)
	if err := os.Remove(filepath.Join(folderB, "02 - Two.mp3")); err != nil {
		t.Fatal(err)
	}
	rerender(t, folderB)
	if resp, _ := c.get("/tracks/" + id2 + "/audio"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a track deleted: %d", resp.StatusCode)
	}
	waitRescans(t, r, 3)
	resp, body = c.get("/tracks/" + id2 + "/audio")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"track_unavailable"`) {
		t.Fatalf("a track deleted, after the scan: %d %s", resp.StatusCode, body)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

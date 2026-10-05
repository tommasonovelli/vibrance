package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/catalog"
	"vibrance/internal/media"
)

// The guard of the files must be able to heal (DESIGN.md §6.2 and §9, the
// erratum of step S16e): a file that is not the one of the index asks the
// scanner to index its album again, and an album whose files only have
// another time is served again after one cycle.

// countedTools makes the server run ffmpeg and ffprobe through a script
// that counts each start and then becomes the pinned tool, and returns the
// count of the processes started so far.
func countedTools(t *testing.T, s *server) func() int64 {
	t.Helper()
	count := filepath.Join(t.TempDir(), "count")
	writeFile(t, count, nil)
	// One byte for each process, appended in one write.
	wrap := func(name, tool string) string {
		return fakeTool(t, name, "printf x >> '"+count+"'\nexec "+tool+` "$@"`)
	}
	s.ffmpegPath, s.ffprobePath = wrap("ffmpeg", media.FFmpegPath), wrap("ffprobe", media.FFprobePath)
	return func() int64 {
		t.Helper()
		info, err := os.Stat(count)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
}

// longAgo is a modification time no file of the tests has.
var longAgo = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// The case of the erratum, on the real server with the real scanner and
// the pinned tools: `touch -d 2001-01-01` on an audio file whose receipt
// did not change, as a copy of the volume that does not keep the times
// leaves it. The request answers 503 with Retry-After, one cycle indexes
// the album again without starting a process, and the next request gets
// the file under the same ETag. The same for the original of a cover. A
// file of another size than its receipt says stays refused: the known
// limit.
func TestFilesHealAfterATouch(t *testing.T) {
	musiclib := fixtureLibrary(t)
	var processes func() int64
	r := startServer(t, func(s *server) {
		s.musiclibDir = musiclib
		processes = countedTools(t, s)
	})
	waitReady(t, "http://"+r.addr)
	waitScanned(t, r)
	c := newClient(t, r)
	started := processes()
	if started == 0 {
		t.Fatal("the first scan started no counted process: the count does not work")
	}
	indexedBy := func(cycle int) any {
		t.Helper()
		var asked []map[string]any
		for _, ev := range eventsOf(t, r.logs, "scan finished") {
			if ev["reason"] == "file_replaced" {
				asked = append(asked, ev)
			}
		}
		return asked[cycle-1]["indexed"]
	}
	heals := func(where, path, file string, cycle int) {
		t.Helper()
		resp, body := c.get(path)
		etag := resp.Header.Get("ETag")
		if resp.StatusCode != http.StatusOK || etag == "" {
			t.Fatalf("%s, before: %d, ETag %q", where, resp.StatusCode, etag)
		}
		if err := os.Chtimes(file, time.Time{}, longAgo); err != nil {
			t.Fatal(err)
		}
		resp, refused := c.get(path)
		if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" ||
			!strings.Contains(string(refused), `"`+catalog.CodeLibraryChanging+`"`) {
			t.Fatalf("%s, touched: %d, Retry-After %q, %s", where, resp.StatusCode, resp.Header.Get("Retry-After"), refused)
		}
		waitRescans(t, r, cycle)
		if got := indexedBy(cycle); got != float64(1) {
			t.Fatalf("%s: the cycle indexed %v albums, want the one of the file", where, got)
		}
		resp, healed := c.get(path)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") != etag || !bytes.Equal(healed, body) {
			t.Fatalf("%s, after the cycle: %d, ETag %q (want %q), %d bytes (want %d)",
				where, resp.StatusCode, resp.Header.Get("ETag"), etag, len(healed), len(body))
		}
		if n := processes(); n != started {
			t.Fatalf("%s: %d processes started to heal a time", where, n-started)
		}
	}
	trackOf := func(album string, n int) string {
		t.Helper()
		resp, body := c.get("/albums/" + album)
		var a api.AlbumDetail
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &a) != nil || len(a.Tracks) < n {
			t.Fatalf("the album %s: %d", album, resp.StatusCode)
		}
		return a.Tracks[n-1].Id
	}
	folderA := filepath.Join(musiclib, "library", "Aurora Sines", "Alpha_ Light_")
	id := trackOf(albumA, 3)
	heals("the audio", "/tracks/"+id+"/audio", filepath.Join(folderA, "03 - Third_.flac"), 1)
	if trackOf(albumA, 3) != id {
		t.Fatal("the track has another id after the cycle")
	}
	heals("the original of the cover", "/albums/"+albumA+"/cover?size=original", filepath.Join(folderA, "cover.jpg"), 2)

	// Another size than the receipt says: the album is a problem of the
	// library and stays as it was, so the file stays refused.
	file := filepath.Join(musiclib, "library", "Bravo Tones", "Beta MP3", "01 - One.mp3")
	writeFile(t, file, append(readFile(t, file), 0))
	path := "/tracks/" + trackOf(albumB, 1) + "/audio"
	for cycle := 3; cycle <= 4; cycle++ {
		if resp, _ := c.get(path); resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
			t.Fatalf("a file of another size, cycle %d: %d", cycle, resp.StatusCode)
		}
		waitRescans(t, r, cycle)
		if got := indexedBy(cycle); got != float64(0) {
			t.Fatalf("a file of another size: the cycle indexed %v albums", got)
		}
	}
	if n := processes(); n != started {
		t.Fatalf("%d processes started for a file of another size", n-started)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// §9.2 with the rule of the audio (NOTES.md N-115): a cover file that
// cannot be read, library/ that is not there among them, answers 404
// cover_not_found and asks the scanner for nothing, whether the original is
// asked for or a thumbnail that is not in the cache. A scan cannot repair a
// volume that is not mounted.
func TestAlbumCoverThatCannotBeRead(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	w.fixtureTrack(albumA, 1)
	coverPath := "/albums/" + albumA + "/cover"
	wantStatus(t, "the thumbnail of 640", w.fetch(http.MethodGet, coverPath+"?size=640", anna), http.StatusOK)
	notFound := func(where string) {
		t.Helper()
		for _, size := range []string{"original", "256"} {
			wantCode(t, where+", "+size, w.fetch(http.MethodGet, coverPath+"?size="+size, anna), http.StatusNotFound, "cover_not_found")
		}
		// A thumbnail in the cache is of the bytes its hash names.
		wantStatus(t, where+", a thumbnail in the cache", w.fetch(http.MethodGet, coverPath+"?size=640", anna), http.StatusOK)
		if got := w.rescans.Load(); got != 0 {
			t.Errorf("%s: %d rechecks asked for, want none", where, got)
		}
	}
	file := w.inLibrary("Aurora Sines/Alpha_ Light_/cover.jpg")
	saved := filepath.Join(t.TempDir(), "cover.jpg")
	writeFile(t, saved, readFile(t, file))
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	// A symbolic link is never followed (I1, T11).
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(saved, file); err != nil {
		t.Fatal(err)
	}
	notFound("a symbolic link")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o755); err != nil {
		t.Fatal(err)
	}
	notFound("a folder")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	writeFile(t, file, readFile(t, saved))
	if err := os.Chtimes(file, time.Time{}, info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(file, 0); err != nil {
			t.Fatal(err)
		}
		notFound("a file that cannot be read")
		if err := os.Chmod(file, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	library := filepath.Join(w.musiclib, "library")
	if err := os.Rename(library, library+".off"); err != nil {
		t.Fatal(err)
	}
	notFound("library/ is gone")
	if ev := eventsOf(t, w.logs, "a cover file cannot be read"); len(ev) == 0 || ev[0]["level"] != "WARN" {
		t.Errorf("the covers that cannot be read are not logged: %v", ev)
	}
	if err := os.Rename(library+".off", library); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, "library/ is back", w.fetch(http.MethodGet, coverPath+"?size=original", anna), http.StatusOK)
	wantStatus(t, "library/ is back, a thumbnail", w.fetch(http.MethodGet, coverPath+"?size=256", anna), http.StatusOK)
	if got := w.rescans.Load(); got != 0 {
		t.Errorf("%d rechecks asked for, want none", got)
	}
}

// swap keeps replacing the file at path with one of two copies of it, each
// with the size and the time of the original, until stop is closed. The
// path always names a whole file, by turns one inode and the other: what a
// rename of MusicLib does to a file that a request is opening.
func swap(t *testing.T, path string, stop <-chan struct{}) *sync.WaitGroup {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var copies [2]string
	for i := range copies {
		copies[i] = path + ".copy" + string(rune('0'+i))
		writeFile(t, copies[i], readFile(t, path))
		if err := os.Chtimes(copies[i], time.Time{}, info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	var running sync.WaitGroup
	running.Go(func() {
		tmp := path + ".next"
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// A link has the inode of the copy: the copy stays for the
			// next turn.
			if err := os.Link(copies[i%2], tmp); err != nil {
				t.Error(err)
				return
			}
			if err := os.Rename(tmp, path); err != nil {
				t.Error(err)
				return
			}
		}
	})
	return &running
}

// T13 between Lstat and Open: a file that is replaced while the Root opens
// it is a replaced file, 503 library_changing and a recheck of its album,
// not a file that cannot be read (404). The file is replaced by renames all
// the time, always by one with the size and the time of the index: a
// request either opens one of them whole (200) or meets the replacement
// (503), and nothing else.
func TestFilesReplacedWhileTheyAreOpened(t *testing.T) {
	w := newWorld(t, apiOrigin)
	anna := w.as(w.anna)
	id := w.fixtureTrack(albumA, 1)
	for _, tc := range []struct{ name, path, file string }{
		{"the audio", "/tracks/" + id + "/audio", w.filePath(id)},
		{"the cover", "/albums/" + albumA + "/cover?size=original", w.inLibrary("Aurora Sines/Alpha_ Light_/cover.jpg")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := w.rescans.Load()
			stop := make(chan struct{})
			swapping := swap(t, tc.file, stop)
			ended := sync.OnceFunc(func() {
				close(stop)
				swapping.Wait()
			})
			defer ended()
			met := false
			for deadline := time.Now().Add(60 * time.Second); !met && time.Now().Before(deadline) && !t.Failed(); {
				rec := w.fetch(http.MethodGet, tc.path, anna)
				switch rec.Code {
				case http.StatusOK:
				case http.StatusServiceUnavailable:
					met = true
					wantCode(t, "replaced while it was opened", rec, http.StatusServiceUnavailable, catalog.CodeLibraryChanging)
					wantHeader(t, "replaced while it was opened", rec, "Retry-After", "5")
				default:
					t.Fatalf("status %d (%s), want 200 or 503", rec.Code, redacted(rec))
				}
			}
			ended()
			if !met {
				t.Fatal("no request met the replacement within 60s")
			}
			if got := w.rescans.Load(); got != before+1 || w.rescans.last() != albumA {
				t.Fatalf("%d rechecks asked for, the last of the album %q; want one, of the album %s", got-before, w.rescans.last(), albumA)
			}
		})
	}
}

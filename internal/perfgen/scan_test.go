//go:build perf

package perfgen

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibrance/internal/store"
)

// scanSize is the dataset of the budget of the scanner: 10,000 album
// folders. The users have what they have in the full dataset.
var scanSize = Size{Artists: 1000, Albums: 10000, Tracks: 100000, Users: 20, Playlists: 500, PlaylistItems: 200, Favorites: 50000}

const (
	budgetScanSec = 10
	// budgetReadDuringScan is the budget of the two pages that are read
	// while the scanner writes: they are a page of a playlist and a page of
	// the favorites, which have the same.
	budgetReadDuringScan = 30 * time.Millisecond
)

// TestPerfScan measures the scanner on 10,000 album folders with real receipts
// and no track file: a cycle that finds nothing changed, the memory of the
// server at rest after it, and what a client waits for while the scanner
// writes for a long time, which is when the whole library is gone and
// every album becomes unavailable, each in its transaction. It also
// measures how fast the audio of a track is sent.
func TestPerfScan(t *testing.T) {
	stateDir, musiclibDir := generate(t, scanSize, true)
	trackID, size := putTrackFile(t, stateDir, musiclibDir)
	bin := build(t, stateDir, musiclibDir)
	albums := thousands(scanSize.Albums)
	r := &report{t: t}
	defer r.print()

	// The first start writes the sort keys of the new database; the cycle
	// of the second start is already one without changes.
	srv := start(t, bin)
	unchanged := func(ev scanEvent) {
		t.Helper()
		if !ev.OK || ev.State != "idle" || ev.Discovered != scanSize.Albums || ev.Indexed != 0 || ev.Absent != 0 || ev.Problems != 0 {
			t.Fatalf("a cycle on an unchanged library: %+v", ev)
		}
	}
	unchanged(srv.waitScans(1, 5*time.Minute))
	srv.stop()
	srv = start(t, bin)
	admin := srv.signIn(Admin)
	slowest := 0.0
	const cycles = 10
	for n := 1; n <= cycles; n++ {
		ev := srv.waitScans(n, 5*time.Minute)
		unchanged(ev)
		slowest = max(slowest, float64(ev.DurationMS)/1000)
		if n < cycles {
			admin.must(http.StatusAccepted, nil, http.MethodPost, "/api/v1/admin/library/scan", nil)
		}
	}
	r.value(fmt.Sprintf("scan without changes of %s album folders, the slowest of %d", albums, cycles), slowest, budgetScanSec, "s")
	time.Sleep(rest)
	r.value(fmt.Sprintf("memory %s after %d scans of %s albums (RSS)", rest, cycles, albums), float64(srv.memory("VmRSS"))/1024, budgetIdleMB, "MB")

	measureAudio(r, srv, admin, trackID, size)

	// The two pages, with nothing else going on and then while the scanner
	// marks 10,000 albums unavailable.
	var listed struct {
		Playlists []struct {
			ID string `json:"id"`
		} `json:"playlists"`
	}
	admin.get("/api/v1/playlists", &listed)
	items := "/api/v1/playlists/" + listed.Playlists[0].ID + "/items?limit=50"
	const favorites = "/api/v1/me/favorites/tracks?limit=50"
	idleItems := r.measure("playlist page of 50, scanner idle", budgetItemsPage)
	idleFavorites := r.measure("favorites page of 50, scanner idle", budgetFavorites)
	for range 200 {
		idleItems.add(admin.get(items, nil))
		idleFavorites.add(admin.get(favorites, nil))
	}

	lib := filepath.Join(musiclibDir, libraryFolder)
	if err := os.Rename(lib, lib+".gone"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	busyItems := r.measure("playlist page of 50, while the scanner writes "+albums+" albums", budgetReadDuringScan)
	busyFavorites := r.measure("favorites page of 50, while the scanner writes "+albums+" albums", budgetReadDuringScan)
	admin.must(http.StatusAccepted, nil, http.MethodPost, "/api/v1/admin/library/scan", nil)
	for len(srv.scans()) == cycles {
		busyItems.add(admin.get(items, nil))
		busyFavorites.add(admin.get(favorites, nil))
	}
	gone := srv.scans()[cycles]
	if !gone.OK || gone.Absent != scanSize.Albums {
		t.Fatalf("the cycle on the empty library: %+v", gone)
	}
	r.value("the cycle that marks "+albums+" albums unavailable", float64(gone.DurationMS)/1000, 0, "s")

	// A cycle more, on an index where every track is unavailable and has
	// references: P6 looks for the audio of each elsewhere.
	admin.must(http.StatusAccepted, nil, http.MethodPost, "/api/v1/admin/library/scan", nil)
	after := srv.waitScans(cycles+2, 5*time.Minute)
	r.value("scan without changes, every album unavailable", float64(after.DurationMS)/1000, budgetScanSec, "s")
	r.value("memory, the most the process ever held (peak RSS)", float64(srv.memory("VmHWM"))/1024, 0, "MB")
	srv.stop()
}

// putTrackFile writes the file of one track of the dataset, with the size
// and the time the index has for it, so that its audio can be asked for.
// The bytes are zeros: the server sends them as they are. It returns the id
// of the track and the size of the file.
func putTrackFile(t *testing.T, stateDir, musiclibDir string) (id string, size int64) {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, filepath.Join(stateDir, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	var (
		album store.ListAlbumStatesRow
		track store.Track
	)
	err = st.Read(ctx, func(q *store.Queries) error {
		albums, err := q.ListAlbumStates(ctx)
		if err != nil {
			return err
		}
		album = albums[0]
		tracks, err := q.ListTracksByAlbum(ctx, album.ID)
		if err != nil {
			return err
		}
		track = tracks[0]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(musiclibDir, libraryFolder, filepath.FromSlash(album.RelPath), filepath.FromSlash(track.RelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, track.FileSize), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Unix(0, track.FileMtimeNs)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return track.ID, track.FileSize
}

// measureAudio downloads the file of a track again and again: how fast the
// server sends it, and how much processor it uses for each gigabyte.
func measureAudio(r *report, srv *server, c *client, trackID string, size int64) {
	r.t.Helper()
	const times = 40
	m := r.measure(fmt.Sprintf("audio: the whole file of a track (%.0f MB)", float64(size)/(1<<20)), 0)
	cpu := srv.cpu()
	began := time.Now()
	for range times {
		req, err := http.NewRequestWithContext(r.t.Context(), http.MethodGet, c.base+"/api/v1/tracks/"+trackID+"/audio", nil)
		if err != nil {
			r.t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		one := time.Now()
		resp, err := c.http.Do(req)
		if err != nil {
			r.t.Fatal(err)
		}
		n, err := io.Copy(io.Discard, resp.Body)
		if cerr := resp.Body.Close(); err != nil || cerr != nil || resp.StatusCode != http.StatusOK || n != size {
			r.t.Fatalf("the audio: status %d, %d bytes of %d, %v, %v", resp.StatusCode, n, size, err, cerr)
		}
		m.add(time.Since(one))
	}
	gigabytes := float64(times*size) / (1 << 30)
	r.value("audio: throughput on the loopback interface", gigabytes*1024/time.Since(began).Seconds(), 0, "MB/s")
	r.value("audio: processor time of the server for each gigabyte", (srv.cpu()-cpu).Seconds()/gigabytes, 0, "s")
}

// TestPerfMemoryAtRest measures the memory at rest of the server where the
// budget is stated: on the dataset of the step with its library, 20,000
// album folders with real receipts, which the scanner reads at every cycle,
// as in a real installation. The memory is read 30 s after the last cycle,
// as in the other tests, and again after 4 minutes, past the collection
// the Go runtime forces when two minutes pass without one: without the
// scanner giving the heap of a cycle back at its end, that collection is
// what would (NOTES.md N-163). The log of the collections
// (GODEBUG=gctrace=1) tells the heap the server keeps in use from the
// memory the process holds.
func TestPerfMemoryAtRest(t *testing.T) {
	stateDir, musiclibDir := generate(t, Full, true)
	checkRows(t, stateDir, Full)
	bin := build(t, stateDir, musiclibDir)
	r := &report{t: t}
	defer r.print()

	const trace = "GODEBUG=gctrace=1"
	srv := start(t, bin, trace)
	unchanged := func(ev scanEvent) {
		t.Helper()
		if !ev.OK || ev.Discovered != Full.Albums || ev.Indexed != 0 || ev.Absent != 0 || ev.Problems != 0 {
			t.Fatalf("a cycle on an unchanged library: %+v", ev)
		}
	}
	unchanged(srv.waitScans(1, 5*time.Minute))
	srv.stop()
	srv = start(t, bin, trace)
	admin := srv.signIn(Admin)
	const cycles = 5
	for n := 1; n <= cycles; n++ {
		unchanged(srv.waitScans(n, 5*time.Minute))
		if n < cycles {
			admin.must(http.StatusAccepted, nil, http.MethodPost, "/api/v1/admin/library/scan", nil)
		}
	}
	ended := time.Now()
	for _, after := range []time.Duration{rest, 4 * time.Minute} {
		time.Sleep(time.Until(ended.Add(after)))
		r.value(fmt.Sprintf("memory %s after %d scans of %s albums (RSS)", after, cycles, thousands(Full.Albums)),
			float64(srv.memory("VmRSS"))/1024, budgetIdleMB, "MB")
		at, live := srv.lastGC()
		r.value(fmt.Sprintf("  Go heap in use after the last collection (at %.0f s)", at), float64(live), 0, "MB")
	}
	r.value("memory, the most the process ever held (peak RSS)", float64(srv.memory("VmHWM"))/1024, 0, "MB")
	srv.stop()
}

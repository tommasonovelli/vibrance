package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/httpx"
	"vibrance/internal/media"
)

// The operations of the state of the library (DESIGN.md §6.5, §8.7, step
// S20), on the real server with its scanner, the fixture library on disk
// and the pinned tools. Every answer is also checked against the
// specification (I10).

// operator asks a running server as its first admin, through the whole
// handler of the server.
type operator struct {
	t     *testing.T
	r     *running
	token string
}

func newOperator(t *testing.T, r *running) *operator {
	t.Helper()
	return &operator{t: t, r: r, token: adminToken(t, r.s.sessions.Load())}
}

// ask sends a request without a body; the answer conforms.
func (o *operator) ask(method, path string) *httptest.ResponseRecorder {
	o.t.Helper()
	req := httptest.NewRequest(method, api.BasePath+path, nil)
	req.Host = o.r.addr
	req.Header.Set("Authorization", "Bearer "+o.token)
	if method != http.MethodGet {
		req.Header.Set(httpx.RequestHeader, "1")
	}
	rec := send(o.r.s.http.Handler, req)
	assertConforms(o.t, method+" "+path, req, rec)
	return rec
}

// status reads the state of the library.
func (o *operator) status() api.LibraryStatus {
	o.t.Helper()
	rec := o.ask(http.MethodGet, "/admin/library")
	wantStatus(o.t, "the state of the library", rec, http.StatusOK)
	return decode[api.LibraryStatus](o.t, rec)
}

// scan asks for a cycle and returns the state the answer carries.
func (o *operator) scan() api.LibraryStatus {
	o.t.Helper()
	rec := o.ask(http.MethodPost, "/admin/library/scan")
	wantStatus(o.t, "asking for a scan", rec, http.StatusAccepted)
	return decode[api.LibraryStatus](o.t, rec)
}

// waitEvents waits until the log of r has n events with that message.
func waitEvents(t *testing.T, r *running, msg string, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if events := eventsOf(t, r.logs, msg); len(events) >= n {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d %q events within 60s; logs:\n%s", n, msg, r.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// gatedProbe is an ffprobe that waits, before it examines a file, until
// open is called: a cycle of the scanner that lasts as long as the test
// wants. started is the file it creates when the first one waits.
func gatedProbe(t *testing.T) (path, started string, open func()) {
	t.Helper()
	dir := t.TempDir()
	started, gate := filepath.Join(dir, "started"), filepath.Join(dir, "open")
	path = fakeTool(t, "ffprobe", `case "$*" in
*-version*) ;;
*) : > "`+started+`"; while [ ! -e "`+gate+`" ]; do sleep 0.05; done ;;
esac
exec `+media.FFprobePath+` "$@"`)
	var once sync.Once
	open = func() { once.Do(func() { writeFile(t, gate, nil) }) }
	// A test that fails leaves no process waiting.
	t.Cleanup(open)
	return path, started, open
}

// The state while a cycle runs and after it, and the requests for a scan:
// each one answers 202 with the state as it is, and however many arrive
// while a cycle runs, one more cycle follows, not one each (§6.1, §8.7).
func TestLibraryStatusThroughACycle(t *testing.T) {
	musiclib := fixtureLibrary(t)
	probe, started, open := gatedProbe(t)
	r := startServer(t, func(s *server) { s.musiclibDir, s.ffprobePath = musiclib, probe })
	waitReady(t, "http://"+r.addr)
	o := newOperator(t, r)
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the scanner examined no file within 60s; logs:\n%s", r.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// While the first cycle runs.
	during := o.status()
	if during.State != api.LibraryStatusStateScanning || during.LastScan == nil || during.LastScan.FinishedAt != nil ||
		during.LastScan.Ok || during.LastScan.Error != nil {
		t.Fatalf("during the cycle: state %s, last scan %+v", during.State, during.LastScan)
	}
	if during.Progress == nil || *during.Progress != (api.LibraryProgress{Discovered: 6, Pending: 6, Indexed: 0}) {
		t.Fatalf("during the cycle: progress %+v", during.Progress)
	}
	if during.Albums != (api.LibraryCounts{}) || during.Tracks != (api.LibraryCounts{}) || during.MusiclibMaintenance ||
		during.Problems == nil || len(during.Problems) != 0 {
		t.Fatalf("during the cycle: %+v", during)
	}
	for i := range 5 {
		if got := o.scan(); got.State != api.LibraryStatusStateScanning || got.LastScan == nil ||
			got.LastScan.StartedAt != during.LastScan.StartedAt {
			t.Fatalf("request %d for a scan during the cycle: state %s, last scan %+v", i, got.State, got.LastScan)
		}
	}
	if finished := eventsOf(t, r.logs, "scan finished"); len(finished) != 0 {
		t.Fatalf("a cycle finished behind the closed gate: %v", finished)
	}

	// After it: the five requests are one cycle.
	open()
	waitEvents(t, r, "scan finished", 2)
	// Were the requests a queue, its other cycles would follow at once: they
	// have nothing to index.
	time.Sleep(300 * time.Millisecond)
	finished := eventsOf(t, r.logs, "scan finished")
	if len(finished) != 2 || finished[0]["reason"] != "startup" || finished[1]["reason"] != "request" ||
		finished[0]["indexed"] != float64(6) || finished[1]["indexed"] != float64(0) {
		t.Fatalf("the cycles after five requests during one: %v", finished)
	}
	after := o.status()
	if after.State != api.LibraryStatusStateIdle || after.Progress != nil || after.LastScan == nil || after.LastScan.FinishedAt == nil ||
		!after.LastScan.Ok || after.LastScan.Error != nil || after.LastScan.StartedAt <= during.LastScan.StartedAt ||
		*after.LastScan.FinishedAt < after.LastScan.StartedAt {
		t.Fatalf("after the cycles: state %s, progress %v, last scan %+v", after.State, after.Progress, after.LastScan)
	}
	if after.Albums != (api.LibraryCounts{Available: 6}) || after.Tracks != (api.LibraryCounts{Available: 14}) || len(after.Problems) != 0 {
		t.Fatalf("after the cycles: %+v", after)
	}

	// A request when no cycle runs starts one, and the state says what it
	// found: a folder MusicLib did not write, and an album that is gone.
	if err := os.MkdirAll(filepath.Join(musiclib, "library", "Zulu", "No Receipt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(musiclib, "library", "Bravo Tones")); err != nil {
		t.Fatal(err)
	}
	o.scan()
	waitEvents(t, r, "scan finished", 3)
	found := o.status()
	if found.State != api.LibraryStatusStateIdle || !found.LastScan.Ok || found.Albums != (api.LibraryCounts{Available: 5, Unavailable: 1}) ||
		found.Tracks != (api.LibraryCounts{Available: 12, Unavailable: 2}) {
		t.Fatalf("after an album left: %+v (last scan %+v)", found, found.LastScan)
	}
	if len(found.Problems) != 1 || found.Problems[0].RelPath != "Zulu/No Receipt" || found.Problems[0].Code != "receipt_missing" ||
		found.Problems[0].Message == "" || strings.Contains(found.Problems[0].Message, musiclib) {
		t.Fatalf("the problems: %+v", found.Problems)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// §4.5, §6.1, the erratum to A16: with the maintenance marker of MusicLib,
// or without the marker of its volume, a cycle goes nowhere. The state says
// which, the index is left as it was, and the last scan is still the last
// one that went through the library. When the markers are right again, so
// is the state, and nothing was indexed again.
func TestLibraryStatusWithTheMarkers(t *testing.T) {
	musiclib := fixtureLibrary(t)
	r := startServer(t, func(s *server) { s.musiclibDir = musiclib })
	waitReady(t, "http://"+r.addr)
	waitScanned(t, r)
	o := newOperator(t, r)
	before := o.status()
	if before.State != api.LibraryStatusStateIdle || before.Albums.Available != 6 || before.LastScan == nil {
		t.Fatalf("before: %+v", before)
	}
	// same checks that a cycle the markers stopped touched nothing.
	same := func(what string, got api.LibraryStatus) {
		t.Helper()
		if got.Albums != before.Albums || got.Tracks != before.Tracks || got.Progress != nil || len(got.Problems) != 0 ||
			got.LastScan == nil || got.LastScan.StartedAt != before.LastScan.StartedAt || got.LastScan.FinishedAt == nil ||
			*got.LastScan.FinishedAt != *before.LastScan.FinishedAt || !got.LastScan.Ok {
			t.Fatalf("%s: %+v (last scan %+v), before %+v (last scan %+v)", what, got, got.LastScan, before, before.LastScan)
		}
	}

	maintenance, store := filepath.Join(musiclib, ".maintenance"), filepath.Join(musiclib, ".musiclib-store")
	writeFile(t, maintenance, nil)
	o.scan()
	skipped := waitEvents(t, r, "scan skipped", 1)
	if skipped[0]["state"] != "maintenance" || skipped[0]["reason"] != "request" {
		t.Fatalf("the cycle in maintenance: %v", skipped)
	}
	got := o.status()
	if got.State != api.LibraryStatusStateMaintenance || !got.MusiclibMaintenance {
		t.Fatalf("in maintenance: state %s, musiclib_maintenance %v", got.State, got.MusiclibMaintenance)
	}
	same("in maintenance", got)

	if err := os.Remove(maintenance); err != nil {
		t.Fatal(err)
	}
	mark := readFile(t, store)
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	o.scan()
	if skipped = waitEvents(t, r, "scan skipped", 2); skipped[1]["state"] != "unavailable" {
		t.Fatalf("the cycle without the volume: %v", skipped)
	}
	got = o.status()
	if got.State != api.LibraryStatusStateUnavailable || got.MusiclibMaintenance {
		t.Fatalf("without the volume: state %s, musiclib_maintenance %v", got.State, got.MusiclibMaintenance)
	}
	same("without the volume", got)
	// The catalog still answers (I14).
	if rec := o.ask(http.MethodGet, "/albums"); rec.Code != http.StatusOK || len(decode[api.AlbumList](t, rec).Albums) != 6 {
		t.Fatalf("the albums without the volume: %d", rec.Code)
	}

	writeFile(t, store, mark)
	o.scan()
	finished := waitEvents(t, r, "scan finished", 2)
	if finished[1]["indexed"] != float64(0) || finished[1]["absent"] != float64(0) || finished[1]["ok"] != true {
		t.Fatalf("the cycle after the markers: %v", finished)
	}
	got = o.status()
	if got.State != api.LibraryStatusStateIdle || got.MusiclibMaintenance || got.Albums != before.Albums || got.Tracks != before.Tracks ||
		!got.LastScan.Ok || got.LastScan.StartedAt <= before.LastScan.StartedAt {
		t.Fatalf("after the markers: %+v (last scan %+v)", got, got.LastScan)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// Before the first cycle there is no last scan, and the list of problems is
// an empty list, never null. Only an admin reads the state or asks for a
// scan (§8.3).
func TestLibraryStatusBeforeTheFirstCycle(t *testing.T) {
	w := newWorld(t, apiOrigin)
	rec := w.do(http.MethodGet, "/admin/library", nil, w.as(w.admin))
	wantStatus(t, "the state", rec, http.StatusOK)
	const want = `{"albums":{"available":0,"unavailable":0},"last_scan":null,"musiclib_maintenance":false,"problems":[],` +
		`"progress":null,"state":"idle","tracks":{"available":0,"unavailable":0}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("the state before the first cycle:\n got %s\nwant %s", got, want)
	}
	rec = w.do(http.MethodPost, "/admin/library/scan", nil, w.as(w.admin))
	wantStatus(t, "asking for a scan", rec, http.StatusAccepted)
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("the state a request for a scan answers:\n got %s\nwant %s", got, want)
	}
	for _, c := range []struct{ method, path string }{{http.MethodGet, "/admin/library"}, {http.MethodPost, "/admin/library/scan"}} {
		wantCode(t, c.path+" as a user", w.do(c.method, c.path, nil, w.as(w.anna)), http.StatusForbidden, "forbidden")
		wantCode(t, c.path+" as nobody", w.do(c.method, c.path, nil, nobody), http.StatusUnauthorized, "login_required")
	}
}

// Many admins read the state and ask for scans while the scanner runs its
// cycles: every answer is whole and conforms, and the cycles stay one at a
// time (the race detector watches the state the scanner and the requests
// share).
func TestLibraryStatusConcurrently(t *testing.T) {
	musiclib := fixtureLibrary(t)
	r := startServer(t, func(s *server) { s.musiclibDir = musiclib })
	waitReady(t, "http://"+r.addr)
	o := newOperator(t, r)
	var clients sync.WaitGroup
	for range 8 {
		clients.Go(func() {
			for range 25 {
				if got := o.scan(); !got.State.Valid() {
					t.Errorf("a request for a scan answered the state %q", got.State)
				}
				got := o.status()
				if scanning := got.State == api.LibraryStatusStateScanning; scanning != (got.Progress != nil) ||
					scanning && (got.LastScan == nil || got.LastScan.FinishedAt != nil) {
					t.Errorf("state %s with progress %v and last scan %+v", got.State, got.Progress, got.LastScan)
				}
			}
		})
	}
	clients.Wait()
	// The last request is served by a cycle that starts after it.
	asked := len(eventsOf(t, r.logs, "scan finished"))
	o.scan()
	waitEvents(t, r, "scan finished", asked+1)
	deadline := time.Now().Add(60 * time.Second)
	for o.status().State != api.LibraryStatusStateIdle {
		if time.Now().After(deadline) {
			t.Fatalf("the scanner is not idle within 60s; logs:\n%s", r.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := o.status()
	if got.Albums != (api.LibraryCounts{Available: 6}) || got.Tracks != (api.LibraryCounts{Available: 14}) || !got.LastScan.Ok {
		t.Fatalf("after the requests: %+v (last scan %+v)", got, got.LastScan)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	for i, ev := range eventsOf(t, r.logs, "scan finished") {
		if i > 0 && ev["indexed"] != float64(0) {
			t.Fatalf("cycle %d indexed again: %v", i, ev)
		}
	}
}

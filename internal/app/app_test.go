package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"vibrance/internal/buildinfo"
	"vibrance/internal/config"
)

// syncBuffer is an io.Writer safe for the concurrent writes of a logger
// and the reads of a test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// events parses the JSON log lines.
func (s *syncBuffer) events(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(s.String()) {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

// messages returns the "msg" of every log event, in order.
func (s *syncBuffer) messages(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, ev := range s.events(t) {
		msg, _ := ev["msg"].(string)
		out = append(out, msg)
	}
	return out
}

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// listen returns a listener on a free loopback port. It is closed at the
// end of the test, if the server it was given to has not closed it.
func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeOnce(t, ln) })
	return ln
}

// dial opens a raw connection to the server, closed at the end of the
// test.
func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeOnce(t, conn) })
	return conn
}

// closeOnce closes c; that it is closed already is not an error.
func closeOnce(t *testing.T, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
}

// running is a server started by run on a loopback port.
type running struct {
	s      *server
	logs   *syncBuffer
	addr   string
	cancel context.CancelFunc
	done   chan error // receives the result of run
}

// startServer runs a server as Run does once it has a listener. prepare,
// if not nil, adjusts the server before it serves.
func startServer(t *testing.T, prepare func(*server)) *running {
	t.Helper()
	logs := &syncBuffer{}
	s := newServer(newLogger(logs), t.TempDir())
	if prepare != nil {
		prepare(s)
	}
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{s: s, logs: logs, addr: ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- s.run(ctx, ln) }()
	t.Cleanup(cancel)
	return r
}

// stop cancels the run and returns its result.
func (r *running) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	return r.wait(t)
}

func (r *running) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatalf("the server did not stop within 30s; logs:\n%s", r.logs)
		return nil
	}
}

// response is what a test looks at in an HTTP response.
type response struct {
	status      int
	contentType string
	location    string
	allow       string
	body        string
}

// do sends one request without following redirects and without reusing
// connections, so that no idle connection outlives the test.
func do(t *testing.T, method, url string) response {
	t.Helper()
	client := &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, rerr := io.ReadAll(resp.Body)
	if err := errors.Join(rerr, resp.Body.Close()); err != nil {
		t.Fatal(err)
	}
	return response{
		status:      resp.StatusCode,
		contentType: resp.Header.Get("Content-Type"),
		location:    resp.Header.Get("Location"),
		allow:       resp.Header.Get("Allow"),
		body:        string(body),
	}
}

// wantJSON checks that the response is the given status and exactly the
// given JSON document: no other key, no other value.
func wantJSON(t *testing.T, got response, status int, want string) {
	t.Helper()
	if got.status != status || got.contentType != "application/json" {
		t.Fatalf("response %d %q %q, want %d application/json", got.status, got.contentType, got.body, status)
	}
	if got.body != want+"\n" {
		t.Fatalf("body %q, want %q", got.body, want)
	}
}

const (
	liveJSON         = `{"status":"live"}`
	readyJSON        = `{"status":"ready"}`
	notReadyJSON     = `{"code":"not_ready","message":"the server is starting","details":{}}`
	shuttingDownJSON = `{"code":"shutting_down","message":"the server is shutting down","details":{}}`
)

// The startup of §11.2 seen from outside: from the moment HTTP answers
// until the startup ends, liveness is positive and readiness is 503
// not_ready; then readiness is 200; once the stop begins it is negative
// again.
func TestReadinessFollowsTheStartup(t *testing.T) {
	var logs syncBuffer
	s := newServer(newLogger(&logs), t.TempDir())
	ln := listen(t)
	base := "http://" + ln.Addr().String()

	served := s.serveHTTP(ln) // step 2
	wantJSON(t, do(t, "GET", base+"/health/live"), 200, liveJSON)
	wantJSON(t, do(t, "GET", base+"/health/ready"), 503, notReadyJSON)

	if err := s.startup(t.Context()); err != nil { // steps 3 to 7
		t.Fatal(err)
	}
	wantJSON(t, do(t, "GET", base+"/health/live"), 200, liveJSON)
	wantJSON(t, do(t, "GET", base+"/health/ready"), 200, readyJSON)

	if err := s.shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve returned %v", err)
	}
	if conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		closeOnce(t, conn)
		t.Fatal("the server still accepts connections after the shutdown")
	}
	// Once the stop has begun net/http serves no request it has not
	// already read, so only the handler can be asked.
	wantJSON(t, serveDirectly(s, "/health/live"), 200, liveJSON)
	wantJSON(t, serveDirectly(s, "/health/ready"), 503, shuttingDownJSON)
	want := []string{"http listening", "database open", "ready", "http server stopped", "database closed"}
	if got := logs.messages(t); !slices.Equal(got, want) {
		t.Fatalf("log events %q, want %q", got, want)
	}
}

// The health endpoints answer GET and HEAD and nothing else; `/` leads to
// the API documentation; every other path is unknown.
func TestRoutes(t *testing.T) {
	r := startServer(t, nil)
	base := "http://" + r.addr
	waitReady(t, base)

	for _, path := range []string{"/health/live", "/health/ready"} {
		if got := do(t, "HEAD", base+path); got.status != 200 || got.contentType != "application/json" || got.body != "" {
			t.Fatalf("HEAD %s: %+v", path, got)
		}
		for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			if got := do(t, method, base+path); got.status != 405 || got.allow != "GET, HEAD" {
				t.Fatalf("%s %s: %+v, want 405 with Allow: GET, HEAD", method, path, got)
			}
		}
	}

	for _, method := range []string{"GET", "HEAD"} {
		if got := do(t, method, base+"/"); got.status != 302 || got.location != "/api/docs" {
			t.Fatalf("%s /: %+v, want 302 to /api/docs", method, got)
		}
	}
	if got := do(t, "POST", base+"/"); got.status != 405 {
		t.Fatalf("POST /: %+v, want 405", got)
	}
	// A query string does not make `/` another path.
	if got := do(t, "GET", base+"/?x=1"); got.status != 302 || got.location != "/api/docs" {
		t.Fatalf("GET /?x=1: %+v, want 302 to /api/docs", got)
	}

	for _, path := range []string{"/health", "/health/", "/health/live/", "/health/ready/x", "/index.html", "/api/v1/server"} {
		if got := do(t, "GET", base+path); got.status != 404 {
			t.Fatalf("GET %s: %+v, want 404", path, got)
		}
	}

	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// waitReady waits until /health/ready answers 200.
func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if got := do(t, "GET", base+"/health/ready"); got.status == 200 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the server is not ready after 30s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A normal stop: with nothing open the server stops at once, well within
// the grace period, without an error and without a warning.
func TestStopWithNothingOpen(t *testing.T) {
	r := startServer(t, nil)
	waitReady(t, "http://"+r.addr)

	start := time.Now()
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > shutdownGrace/2 {
		t.Fatalf("the stop took %s with nothing open", elapsed)
	}
	want := []string{"http listening", "database open", "ready", "stopping", "http server stopped", "database closed"}
	if got := r.logs.messages(t); !slices.Equal(got, want) {
		t.Fatalf("log events %q, want %q", got, want)
	}
	for _, ev := range r.logs.events(t) {
		if ev["level"] != "INFO" {
			t.Fatalf("a clean run logged %v", ev)
		}
	}
}

// serveDirectly calls the server's handler without the network.
func serveDirectly(s *server, path string) response {
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return response{status: rec.Code, contentType: rec.Header().Get("Content-Type"), body: rec.Body.String()}
}

// notifyActive makes the server tell when it has read the head of a
// request: from then on the connection is busy, not idle.
func notifyActive(s *server) <-chan struct{} {
	active := make(chan struct{}, 1)
	s.http.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateActive {
			select {
			case active <- struct{}{}:
			default:
			}
		}
	}
	return active
}

// openRequest sends a request whose 10 bytes of body do not arrive, and
// waits until the server has read its head. The server answers such a
// request only once it has the body, or gives up at the read timeout.
func openRequest(t *testing.T, addr string, active <-chan struct{}) net.Conn {
	t.Helper()
	conn := dial(t, addr)
	if _, err := io.WriteString(conn, "GET /health/live HTTP/1.1\r\nHost: vibrance.test\r\nContent-Length: 10\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-active:
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not read the request within 30s")
	}
	return conn
}

// A request that is open when the stop begins is completed, not cut, if
// it ends within the grace period; meanwhile no new connection is
// accepted, and the stop ends as soon as the request does.
func TestStopWaitsForAnOpenRequest(t *testing.T) {
	var active <-chan struct{}
	r := startServer(t, func(s *server) { active = notifyActive(s) })
	conn := openRequest(t, r.addr, active)

	start := time.Now()
	r.cancel()
	waitRefused(t, r.addr)
	select {
	case err := <-r.done:
		t.Fatalf("the server stopped while a request was open: %v", err)
	default:
	}

	if _, err := io.WriteString(conn, "0123456789"); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, rerr := io.ReadAll(resp.Body)
	if err := errors.Join(rerr, resp.Body.Close()); err != nil {
		t.Fatal(err)
	}
	wantJSON(t, response{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: string(body)},
		200, liveJSON)

	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > shutdownGrace/2 {
		t.Fatalf("the stop took %s after the open request ended", elapsed)
	}
	if strings.Contains(r.logs.String(), "grace period") {
		t.Fatalf("the open request was cut instead of completed:\n%s", r.logs)
	}
}

// waitRefused waits until nothing accepts connections on addr.
func waitRefused(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return
		}
		closeOnce(t, conn)
		if time.Now().After(deadline) {
			t.Fatalf("%s still accepts connections after 30s", addr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// DESIGN.md T27: Shutdown waits for the open connections, and a request
// may stay open far longer than any stop should take. After the grace
// period the server closes the connections and the stop completes, without
// an error.
func TestStopCutsARequestThatOutlivesTheGracePeriod(t *testing.T) {
	const grace = 300 * time.Millisecond
	var active <-chan struct{}
	r := startServer(t, func(s *server) {
		s.grace = grace
		active = notifyActive(s)
	})
	conn := openRequest(t, r.addr, active)

	start := time.Now()
	if err := r.stop(t); err != nil {
		t.Fatalf("a stop that cuts a request is not a failure: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < grace {
		t.Fatalf("the stop took %s: the open request did not get the grace period of %s", elapsed, grace)
	}
	// Without the forced close the request would hold the connection until
	// the read timeout.
	if elapsed > readTimeout/3 {
		t.Fatalf("the stop took %s: the open request was not cut after the grace period of %s", elapsed, grace)
	}

	// The connection is closed, not left open: reading reaches its end.
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ne net.Error
	if _, err := io.Copy(io.Discard, conn); errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("the connection is still open after the stop: %v", err)
	}

	msgs := r.logs.messages(t)
	i := slices.Index(msgs, "requests still open after the grace period: closing their connections")
	if i < 0 || !slices.Contains(msgs[i:], "http server stopped") {
		t.Fatalf("log events %q do not report the cut and then the stop", msgs)
	}
	for _, ev := range r.logs.events(t) {
		if ev["level"] == "ERROR" {
			t.Fatalf("the stop logged an error: %v", ev)
		}
	}
}

// If the HTTP server stops by itself the run ends with http_listen, so
// that the process exits and is restarted.
func TestRunEndsWhenTheHTTPServerStops(t *testing.T) {
	logs := &syncBuffer{}
	s := newServer(newLogger(logs), t.TempDir())
	ln := listen(t)
	done := make(chan error, 1)
	go func() { done <- s.run(t.Context(), ln) }()
	waitReady(t, "http://"+ln.Addr().String())

	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != CodeHTTPListen || Code(err) != CodeHTTPListen {
			t.Fatalf("run returned %v, want an *Error with code %s", err, CodeHTTPListen)
		}
		if !strings.Contains(err.Error(), "the HTTP server stopped: ") {
			t.Fatalf("the error does not say what happened: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not end within 30s")
	}
	if got := s.state.Load(); got != stateStopping {
		t.Fatalf("state %d, want stopping", got)
	}
}

// The limits of §11.1 are those of the server.
func TestHTTPServerLimits(t *testing.T) {
	s := newServer(newLogger(io.Discard), t.TempDir())
	got := [...]time.Duration{s.http.ReadHeaderTimeout, s.http.ReadTimeout, s.http.WriteTimeout, s.http.IdleTimeout}
	want := [...]time.Duration{10 * time.Second, 30 * time.Second, 30 * time.Second, 120 * time.Second}
	if got != want {
		t.Fatalf("read header, read, write and idle timeouts %v, want %v", got, want)
	}
	if s.http.MaxHeaderBytes != 64<<10 {
		t.Fatalf("MaxHeaderBytes %d, want 64 KiB", s.http.MaxHeaderBytes)
	}
	if s.grace != 10*time.Second {
		t.Fatalf("grace period %s, want 10s", s.grace)
	}
}

// A request head over the limit is refused with 431; one under it is
// served. net/http allows 4096 bytes over MaxHeaderBytes.
func TestRequestHeadLimit(t *testing.T) {
	r := startServer(t, nil)
	waitReady(t, "http://"+r.addr)

	for _, tc := range []struct {
		headerBytes int
		want        int
	}{
		{60 << 10, 200},
		{72 << 10, 431},
	} {
		conn := dial(t, r.addr)
		head := "GET /health/live HTTP/1.1\r\nHost: vibrance.test\r\nConnection: close\r\nX-Filler: " +
			strings.Repeat("a", tc.headerBytes) + "\r\n\r\n"
		// The server may answer and close before it has read everything:
		// a failed write is then not the point, the response is.
		_, werr := io.WriteString(conn, head)
		if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("%d header bytes: no response: %v (write: %v)", tc.headerBytes, err, werr)
		}
		if resp.StatusCode != tc.want {
			t.Fatalf("%d header bytes: status %d, want %d", tc.headerBytes, resp.StatusCode, tc.want)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// validEnv is a complete, valid environment that listens on addr.
func validEnv(addr string) map[string]string {
	return map[string]string{
		"VIBRANCE_PUBLIC_ORIGIN": "http://127.0.0.1:8090",
		"VIBRANCE_HTTP_ADDR":     addr,
		"VIBRANCE_SCAN_INTERVAL": "45s",
		"VIBRANCE_WORKERS":       "3",
	}
}

// Step 1 refuses before anything is opened. The address is one where
// listening would fail: a refusal for any other reason than the expected
// one would show.
func TestRunRefusesBeforeListening(t *testing.T) {
	busy := listen(t)
	addr := busy.Addr().String()

	for _, tc := range []struct {
		name string
		euid int
		set  map[string]string
		code string
		want []string // substrings of the error
	}{
		{name: "root", euid: 0, code: CodeRunAsRoot, want: []string{"must not run as root"}},
		{name: "root with an invalid configuration", euid: 0, set: map[string]string{"VIBRANCE_WORKERS": "0"},
			code: CodeRunAsRoot, want: []string{"must not run as root"}},
		{name: "one invalid variable", euid: 1000, set: map[string]string{"VIBRANCE_PUBLIC_ORIGIN": ""},
			code: config.Code, want: []string{"VIBRANCE_PUBLIC_ORIGIN: required"}},
		{name: "several invalid variables", euid: 1000,
			set:  map[string]string{"VIBRANCE_PUBLIC_ORIGIN": "http://host/", "VIBRANCE_SCAN_INTERVAL": "1s", "VIBRANCE_WORKERS": "17"},
			code: config.Code, want: []string{"VIBRANCE_PUBLIC_ORIGIN: ", "VIBRANCE_SCAN_INTERVAL: ", "VIBRANCE_WORKERS: "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := validEnv(addr)
			for k, v := range tc.set {
				e[k] = v
			}
			var logs syncBuffer
			stateDir := t.TempDir()
			err := Run(t.Context(), env(e), tc.euid, 4, stateDir, newLogger(&logs))
			if err == nil || Code(err) != tc.code {
				t.Fatalf("Run returned %v (code %s), want code %s", err, Code(err), tc.code)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the error does not contain %q: %v", want, err)
				}
			}
			// Nothing started: nothing was logged or written, either.
			if logs.String() != "" {
				t.Fatalf("a refused startup logged:\n%s", &logs)
			}
			wantEmptyDir(t, stateDir)
		})
	}
}

// An address that cannot be listened on is http_listen, not a refusal of
// the configuration.
func TestRunCannotListen(t *testing.T) {
	busy := listen(t)

	var logs syncBuffer
	stateDir := t.TempDir()
	err := Run(t.Context(), env(validEnv(busy.Addr().String())), 1000, 4, stateDir, newLogger(&logs))
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeHTTPListen || ae.Err == nil {
		t.Fatalf("Run returned %v, want an *Error with code %s and a cause", err, CodeHTTPListen)
	}
	if !strings.Contains(err.Error(), "cannot listen on "+busy.Addr().String()+": ") {
		t.Fatalf("the error does not name the address: %v", err)
	}
	if got := logs.messages(t); !slices.Equal(got, []string{"starting"}) {
		t.Fatalf("log events %q, want only starting", got)
	}
	// The database comes after HTTP (step 3): it was not created.
	wantEmptyDir(t, stateDir)
}

// Run from the environment to the stop: it logs what it starts with,
// serves on VIBRANCE_HTTP_ADDR, and ends without an error when cancelled.
func TestRun(t *testing.T) {
	// A port that was free a moment ago.
	ln := listen(t)
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	var logs syncBuffer
	stateDir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, env(validEnv(addr)), 1000, 4, stateDir, newLogger(&logs)) }()

	deadline := time.Now().Add(30 * time.Second)
	for !slices.Contains(logs.messages(t), "ready") {
		select {
		case err := <-done:
			t.Fatalf("Run ended before it was ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("not ready after 30s; logs:\n%s", &logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	wantJSON(t, do(t, "GET", "http://"+addr+"/health/ready"), 200, readyJSON)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("Run did not end within 30s; logs:\n%s", &logs)
	}

	want := []string{"starting", "http listening", "database open", "ready", "stopping", "http server stopped", "database closed", "stopped"}
	if got := logs.messages(t); !slices.Equal(got, want) {
		t.Fatalf("log events %q, want %q", got, want)
	}
	if got := dirNames(t, stateDir); !slices.Equal(got, []string{databaseFile}) {
		t.Fatalf("the state folder holds %q after the run, want only the database", got)
	}
	first := logs.events(t)[0]
	for key, want := range map[string]any{
		"version":       buildinfo.Version,
		"public_origin": "http://127.0.0.1:8090",
		"http_addr":     addr,
		"scan_interval": "45s",
		"workers":       float64(3),
	} {
		if first[key] != want {
			t.Fatalf("starting event: %s = %v, want %v", key, first[key], want)
		}
	}
}

func TestCheckNotRoot(t *testing.T) {
	if err := checkNotRoot(0); Code(err) != CodeRunAsRoot {
		t.Fatalf("uid 0: %v", err)
	}
	for _, euid := range []int{1, 1000, 65534} {
		if err := checkNotRoot(euid); err != nil {
			t.Fatalf("uid %d: %v", euid, err)
		}
	}
}

func TestCode(t *testing.T) {
	_, cfgErr := config.Load(env(nil), 1)
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&Error{Code: CodeRunAsRoot, Msg: "x"}, "run_as_root"},
		{&Error{Code: CodeHTTPListen, Msg: "x", Err: errors.New("y")}, "http_listen"},
		{cfgErr, "config_invalid"},
		{errors.Join(errors.New("first"), &Error{Code: CodeHTTPListen, Msg: "x"}), "http_listen"},
		{errors.New("anything else"), "internal"},
	} {
		if got := Code(tc.err); got != tc.want {
			t.Fatalf("Code(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	err := &Error{Code: CodeHTTPListen, Msg: "cannot listen", Err: io.ErrUnexpectedEOF}
	if err.Error() != "cannot listen: unexpected EOF" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Error() = %q, Unwrap = %v", err.Error(), errors.Unwrap(err))
	}
}

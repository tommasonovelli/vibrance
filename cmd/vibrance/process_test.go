package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The tests of this file run the real program as a child process: the
// binary of this package, built once with the race detector, with real
// signals and real exit codes.

// binary is the vibrance executable the process tests run. It is the
// program as it is released but for one string: its state folder is state,
// a temporary folder, instead of /var/lib/vibrance.
var binary struct {
	once  sync.Once
	dir   string
	path  string
	state string
	err   error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if binary.dir != "" {
		if err := os.RemoveAll(binary.dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	os.Exit(code)
}

// vibranceBinary builds the program on first use.
func vibranceBinary(t *testing.T) string {
	t.Helper()
	binary.once.Do(func() {
		goTool, err := exec.LookPath("go")
		if err != nil {
			binary.err = fmt.Errorf("the go command is required: %w", err)
			return
		}
		if binary.dir, err = os.MkdirTemp("", "vibrance-bin-"); err != nil {
			binary.err = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		path := filepath.Join(binary.dir, "vibrance")
		state := filepath.Join(binary.dir, "state")
		if binary.err = os.Mkdir(state, 0o755); binary.err != nil {
			return
		}
		if out, err := exec.CommandContext(ctx, goTool, "build", "-race", "-ldflags", "-X main.stateDir="+state, "-o", path, ".").CombinedOutput(); err != nil {
			binary.err = fmt.Errorf("go build -race: %w\n%s", err, out)
			return
		}
		binary.path, binary.state = path, state
	})
	if binary.err != nil {
		t.Fatal(binary.err)
	}
	return binary.path
}

// resetState empties the state folder of the binary, so that the test
// starts where a new installation does, and returns it.
func resetState(t *testing.T) string {
	t.Helper()
	vibranceBinary(t)
	if err := os.RemoveAll(binary.state); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(binary.state, 0o755); err != nil {
		t.Fatal(err)
	}
	return binary.state
}

// stateFiles are the names of the files in the state folder, sorted.
func stateFiles(t *testing.T, state string) []string {
	t.Helper()
	entries, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// process is `vibrance serve` running as a child process.
type process struct {
	cmd    *exec.Cmd
	stdout *syncBuffer
	stderr *syncBuffer
	addr   string
	done   chan struct{} // closed when the child has been waited for
}

// startServe starts the server on addr with only the variables it needs,
// plus extraEnv, which wins. The child inherits umask.
func startServe(t *testing.T, addr string, umask int, extraEnv ...string) *process {
	t.Helper()
	cmd := exec.Command(vibranceBinary(t), "serve")
	// Later entries win (os/exec keeps the last value of a duplicate).
	cmd.Env = append([]string{"VIBRANCE_PUBLIC_ORIGIN=http://127.0.0.1:8090", "VIBRANCE_HTTP_ADDR=" + addr}, extraEnv...)
	p := &process{cmd: cmd, stdout: &syncBuffer{}, stderr: &syncBuffer{}, addr: addr, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p.stdout, p.stderr
	// Tests of this package do not run in parallel, so changing the umask
	// of the test process around the fork is safe.
	old := syscall.Umask(umask)
	err := cmd.Start()
	syscall.Umask(old)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		// The exit status is read from cmd.ProcessState after done.
		_ = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			<-p.done
		}
	})
	return p
}

func (p *process) output() string {
	return fmt.Sprintf("stdout:\n%s\nstderr:\n%s", p.stdout, p.stderr)
}

// wait waits for the child to end and returns how it ended.
func (p *process) wait(t *testing.T) syscall.WaitStatus {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(60 * time.Second):
		t.Fatalf("the server did not exit within 60s; %s", p.output())
	}
	ws, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no wait status: %v", p.cmd.ProcessState)
	}
	return ws
}

// wantExit waits for the child to exit by itself with the given code.
func (p *process) wantExit(t *testing.T, code int) {
	t.Helper()
	if ws := p.wait(t); !ws.Exited() || ws.ExitStatus() != code {
		t.Fatalf("the server ended with %v, want exit code %d; %s", p.cmd.ProcessState, code, p.output())
	}
}

func (p *process) signal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
}

// get sends one GET, without reusing connections.
func (p *process) get(t *testing.T, path string) (int, string, error) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get("http://" + p.addr + path)
	if err != nil {
		return 0, "", err
	}
	body, rerr := io.ReadAll(resp.Body)
	if err := errors.Join(rerr, resp.Body.Close()); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), nil
}

// waitReady waits until /health/ready answers 200.
func (p *process) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if status, _, err := p.get(t, "/health/ready"); err == nil && status == 200 {
			return
		}
		select {
		case <-p.done:
			t.Fatalf("the server ended before it was ready: %v; %s", p.cmd.ProcessState, p.output())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server is not ready after 30s; %s", p.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// runHealthcheck runs `vibrance healthcheck` as a process, as Docker does,
// and returns its exit code and its output.
func runHealthcheck(t *testing.T, addr string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, vibranceBinary(t), "healthcheck")
	cmd.Env = []string{"VIBRANCE_HTTP_ADDR=" + addr}
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, out
	case errors.As(err, &exitErr) && exitErr.Exited():
		if len(exitErr.Stderr) != 0 {
			t.Fatalf("vibrance healthcheck wrote to stderr: %s", exitErr.Stderr)
		}
		return exitErr.ExitCode(), out
	default:
		t.Fatalf("vibrance healthcheck: %v", err)
		return 0, nil
	}
}

// wantCleanRun checks the output of a server that started and stopped
// without incident: nothing on stderr (the race detector reports there),
// and on stdout the events of the startup and of the stop, in order, as
// JSON lines at level INFO.
func wantCleanRun(t *testing.T, p *process) {
	t.Helper()
	if p.stderr.String() != "" {
		t.Fatalf("the server wrote to stderr:\n%s", p.stderr)
	}
	want := []string{"starting", "http listening", "database open", "media tools verified", "ready", "stopping", "http server stopped", "database closed", "stopped"}
	if got := p.stdout.messages(t); !slices.Equal(got, want) {
		t.Fatalf("log events %q, want %q", got, want)
	}
	for _, ev := range p.stdout.events(t) {
		if ev["level"] != "INFO" {
			t.Fatalf("a clean run logged %v", ev)
		}
		if _, err := time.Parse(time.RFC3339Nano, fmt.Sprint(ev["time"])); err != nil {
			t.Fatalf("event %v has no valid time: %v", ev, err)
		}
	}
}

// A real server process: it starts, answers the health endpoints and the
// healthcheck subcommand, sets umask 022, and stops on SIGTERM with exit
// code 0. Once it is gone the healthcheck fails.
func TestProcessLifecycle(t *testing.T) {
	state := resetState(t)
	p := startServe(t, freeAddr(t), 0o077, "VIBRANCE_SCAN_INTERVAL=45s", "VIBRANCE_WORKERS=3")
	p.waitReady(t)

	// The database is created before the server is ready. The umask the
	// process was started with (077) is not the one its files get (022).
	info, err := os.Stat(filepath.Join(state, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("the database has mode %#o, want 0644", got)
	}

	if status, body, err := p.get(t, "/health/live"); err != nil || status != 200 || body != `{"status":"live"}`+"\n" {
		t.Fatalf("/health/live: %d %q %v", status, body, err)
	}
	if status, body, err := p.get(t, "/health/ready"); err != nil || status != 200 || body != `{"status":"ready"}`+"\n" {
		t.Fatalf("/health/ready: %d %q %v", status, body, err)
	}
	if code, out := runHealthcheck(t, p.addr); code != exitOK || len(out) != 0 {
		t.Fatalf("healthcheck with the server up: exit %d, output %q", code, out)
	}
	if got := processUmask(t, p.cmd.Process.Pid); got != 0o022 {
		t.Fatalf("server umask %#o, want 022", got)
	}

	p.signal(t, syscall.SIGTERM)
	p.wantExit(t, exitOK)
	wantCleanRun(t, p)
	// A clean stop leaves the database as one complete file.
	if got := stateFiles(t, state); !slices.Equal(got, []string{"vibrance.db"}) {
		t.Fatalf("the state folder holds %q after the stop, want only the database", got)
	}
	first := p.stdout.events(t)[0]
	for key, want := range map[string]any{
		"version":       "devel",
		"public_origin": "http://127.0.0.1:8090",
		"http_addr":     p.addr,
		"scan_interval": "45s",
		"workers":       float64(3),
	} {
		if first[key] != want {
			t.Fatalf("starting event: %s = %v, want %v", key, first[key], want)
		}
	}

	code, out := runHealthcheck(t, p.addr)
	if code != exitFailure {
		t.Fatalf("healthcheck with the server gone: exit %d, want %d", code, exitFailure)
	}
	wantLog(t, out, "unhealthy")
}

// SIGINT stops the server like SIGTERM.
func TestProcessSIGINT(t *testing.T) {
	resetState(t)
	p := startServe(t, freeAddr(t), 0o022)
	p.waitReady(t)
	p.signal(t, syscall.SIGINT)
	p.wantExit(t, exitOK)
	wantCleanRun(t, p)
}

// Crash-only (DESIGN.md §2.1): a killed server leaves nothing behind that
// the next one has to undo by a protocol of its own. The database it was
// killed with, still spread over the file and its write-ahead log, is the
// one the next process opens: it is ready on the same address and stops
// cleanly, leaving one complete file.
func TestProcessKilledAndStartedAgain(t *testing.T) {
	state := resetState(t)
	addr := freeAddr(t)
	killed := startServe(t, addr, 0o022)
	killed.waitReady(t)
	killed.signal(t, syscall.SIGKILL)
	if ws := killed.wait(t); !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("the server ended with %v, want killed", killed.cmd.ProcessState)
	}
	if got := killed.stdout.messages(t); !slices.Equal(got, []string{"starting", "http listening", "database open", "media tools verified", "ready"}) {
		t.Fatalf("log events of the killed server: %q", got)
	}
	if code, _ := runHealthcheck(t, addr); code != exitFailure {
		t.Fatalf("healthcheck with the server killed: exit %d, want %d", code, exitFailure)
	}
	// The killed server never moved its write-ahead log into the database.
	if info, err := os.Stat(filepath.Join(state, "vibrance.db-wal")); err != nil || info.Size() == 0 {
		t.Fatalf("the killed server left no write-ahead log to recover from: %v", err)
	}

	again := startServe(t, addr, 0o022)
	again.waitReady(t)
	if code, out := runHealthcheck(t, addr); code != exitOK {
		t.Fatalf("healthcheck with the server started again: exit %d, output %q", code, out)
	}
	again.signal(t, syscall.SIGTERM)
	again.wantExit(t, exitOK)
	wantCleanRun(t, again)
	if got := stateFiles(t, state); !slices.Equal(got, []string{"vibrance.db"}) {
		t.Fatalf("the state folder holds %q after the stop, want only the database", got)
	}
	// What the killed server had committed is there: its migrations.
	db, err := sql.Open("sqlite", filepath.Join(state, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	var tables, check string
	queryErr := errors.Join(
		db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('users', 'tracks', 'search_tracks')`).Scan(&tables),
		db.QueryRowContext(t.Context(), `PRAGMA integrity_check`).Scan(&check))
	if err := errors.Join(queryErr, db.Close()); err != nil {
		t.Fatal(err)
	}
	if tables != "3" || check != "ok" {
		t.Fatalf("the recovered database: %s of 3 tables, integrity_check %q", tables, check)
	}
}

// lastEvent is the last log event of a server that has ended.
func lastEvent(t *testing.T, p *process) map[string]any {
	t.Helper()
	events := p.stdout.events(t)
	if len(events) == 0 {
		t.Fatalf("the server logged nothing; %s", p.output())
	}
	return events[len(events)-1]
}

// wantRefusedStartup checks how a server ended that step 3 of the startup
// refused: exit code 1, one error line with the stable code, and before it
// only the events of a server that listened, was never ready, and stopped.
func wantRefusedStartup(t *testing.T, p *process, code string) string {
	t.Helper()
	p.wantExit(t, exitFailure)
	if p.stderr.String() != "" {
		t.Fatalf("the server wrote to stderr:\n%s", p.stderr)
	}
	msgs := p.stdout.messages(t)
	if len(msgs) != 4 || !slices.Equal(msgs[:3], []string{"starting", "http listening", "http server stopped"}) {
		t.Fatalf("log events %q, want starting, http listening, http server stopped and the error", msgs)
	}
	last := lastEvent(t, p)
	if last["level"] != "ERROR" || last["code"] != code {
		t.Fatalf("the server logged %v, want an error with code %s", last, code)
	}
	return msgs[3]
}

// A database written by a newer Vibrance stops the startup with exit code
// 1 and store_schema_too_new (DESIGN.md §11.2, I13). The server is never
// ready, and the database is left as it was.
func TestProcessRefusesANewerDatabase(t *testing.T) {
	state := resetState(t)
	first := startServe(t, freeAddr(t), 0o022)
	first.waitReady(t)
	first.signal(t, syscall.SIGTERM)
	first.wantExit(t, exitOK)

	path := filepath.Join(state, "vibrance.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := db.ExecContext(t.Context(), `INSERT INTO goose_db_version (version_id, is_applied) VALUES (9999, 1)`)
	if err := errors.Join(execErr, db.Close()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	p := startServe(t, freeAddr(t), 0o022)
	msg := wantRefusedStartup(t, p, "store_schema_too_new")
	if !strings.Contains(msg, "schema version 9999") {
		t.Fatalf("the message does not name the version: %s", msg)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the refused database was changed")
	}
}

// A state folder the server cannot write to stops the startup with exit
// code 1 and state_unwritable, before SQLite is asked anything.
func TestProcessRefusesAnUnwritableStateFolder(t *testing.T) {
	state := resetState(t)
	if err := os.Chmod(state, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(state, 0o755); err != nil {
			t.Error(err)
		}
	})

	p := startServe(t, freeAddr(t), 0o022)
	msg := wantRefusedStartup(t, p, "state_unwritable")
	if !strings.Contains(msg, state) || !strings.Contains(msg, "VIBRANCE_UID") {
		t.Fatalf("the message does not say what to check: %s", msg)
	}
	if got := stateFiles(t, state); len(got) != 0 {
		t.Fatalf("the state folder holds %q", got)
	}
}

// An invalid configuration ends the process with exit code 2 and one error
// line that names every invalid variable.
func TestProcessRefusesInvalidConfiguration(t *testing.T) {
	p := startServe(t, freeAddr(t), 0o022,
		"VIBRANCE_PUBLIC_ORIGIN=https://vibrance.example.net/", "VIBRANCE_HTTP_ADDR=localhost",
		"VIBRANCE_SCAN_INTERVAL=10", "VIBRANCE_WORKERS=32")
	p.wantExit(t, exitUsage)
	if p.stderr.String() != "" {
		t.Fatalf("the server wrote to stderr:\n%s", p.stderr)
	}
	msg := wantLog(t, []byte(p.stdout.String()), "config_invalid")
	for _, want := range []string{"VIBRANCE_PUBLIC_ORIGIN: ", "VIBRANCE_HTTP_ADDR: ", "VIBRANCE_SCAN_INTERVAL: ", "VIBRANCE_WORKERS: "} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the message does not name %q: %s", want, msg)
		}
	}
}

// A second server on the same address ends with exit code 1 and
// http_listen, and does not disturb the first.
func TestProcessAddressInUse(t *testing.T) {
	resetState(t)
	first := startServe(t, freeAddr(t), 0o022)
	first.waitReady(t)

	second := startServe(t, first.addr, 0o022)
	second.wantExit(t, exitFailure)
	events := second.stdout.events(t)
	if last := events[len(events)-1]; last["level"] != "ERROR" || last["code"] != "http_listen" {
		t.Fatalf("the second server logged %v, want an error with code http_listen", last)
	}

	if code, out := runHealthcheck(t, first.addr); code != exitOK {
		t.Fatalf("healthcheck of the first server: exit %d, output %q", code, out)
	}
	first.signal(t, syscall.SIGTERM)
	first.wantExit(t, exitOK)
	wantCleanRun(t, first)
}

// DESIGN.md T27 with the real grace period: a request that stays open
// does not keep the server from stopping. After 10 seconds its connection
// is closed and the process exits with code 0. The request is one whose
// body never arrives; the read timeout would free it only after 30
// seconds.
func TestProcessStopCutsAnOpenRequest(t *testing.T) {
	resetState(t)
	p := startServe(t, freeAddr(t), 0o022)
	p.waitReady(t)

	conn, err := net.Dial("tcp", p.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	// One complete exchange first: the server is serving this connection.
	reader := bufio.NewReader(conn)
	if _, err := io.WriteString(conn, "GET /health/live HTTP/1.1\r\nHost: vibrance.test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := io.Copy(io.Discard, resp.Body)
	if err := errors.Join(rerr, resp.Body.Close()); err != nil || resp.StatusCode != 200 {
		t.Fatalf("first request: %d %v", resp.StatusCode, err)
	}
	// Then the request that stays open. The server answers nothing until
	// it has the body, so give it a moment to read the head.
	if _, err := io.WriteString(conn, "GET /health/live HTTP/1.1\r\nHost: vibrance.test\r\nContent-Length: 10\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	start := time.Now()
	p.signal(t, syscall.SIGTERM)
	p.wantExit(t, exitOK)
	elapsed := time.Since(start)
	if elapsed < 10*time.Second-200*time.Millisecond {
		t.Fatalf("the server stopped after %s: the open request did not get 10s", elapsed)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("the server stopped after %s: the open request was not cut after 10s", elapsed)
	}

	if p.stderr.String() != "" {
		t.Fatalf("the server wrote to stderr:\n%s", p.stderr)
	}
	want := []string{"starting", "http listening", "database open", "media tools verified", "ready", "stopping",
		"requests still open after the grace period: closing their connections", "http server stopped", "database closed", "stopped"}
	if got := p.stdout.messages(t); !slices.Equal(got, want) {
		t.Fatalf("log events %q, want %q", got, want)
	}
	for _, ev := range p.stdout.events(t) {
		if ev["level"] == "ERROR" {
			t.Fatalf("the stop logged an error: %v", ev)
		}
	}

	// The connection was closed by the server.
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ne net.Error
	if _, err := io.Copy(io.Discard, reader); errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("the connection is still open after the stop: %v", err)
	}
}

// processUmask reads the Umask line of /proc/<pid>/status.
func processUmask(t *testing.T, pid int) int {
	t.Helper()
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(status)) {
		if v, ok := strings.CutPrefix(line, "Umask:"); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 8, 32)
			if err != nil {
				t.Fatal(err)
			}
			return int(n)
		}
	}
	t.Fatalf("no Umask line in /proc/%d/status", pid)
	return 0
}

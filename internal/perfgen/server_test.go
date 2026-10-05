//go:build perf

package perfgen

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The suite measures the real program: the binary of the release (static,
// without the race detector), started as a child process on a generated
// database, and asked over HTTP by one client on one connection, as the
// budgets of the step S24 are stated.

// build compiles the program as the Dockerfile does for the image, but for
// its three folders, which are temporary ones instead of the fixed paths of
// the container.
func build(t *testing.T, stateDir, musiclibDir string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "vibrance")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-ldflags",
		"-X main.stateDir="+stateDir+" -X main.musiclibDir="+musiclibDir+" -X main.backupDir="+t.TempDir(),
		"-o", bin, "vibrance/cmd/vibrance")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// server is `vibrance serve` running as a child process.
type server struct {
	t    *testing.T
	cmd  *exec.Cmd
	base string
	log  string
	done chan struct{}
	// startup is the time from the start of the process to the first 200
	// of /health/ready.
	startup time.Duration
	// logAt is how much of the log scans has read.
	logAt int64
	seen  []scanEvent
}

// start starts the server and waits until it is ready. No cycle of the
// scanner begins by itself after the one of the startup: the tests ask for
// the ones they measure. env is added to its environment.
func start(t *testing.T, bin string, env ...string) *server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "serve-*.log")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{t: t, base: "http://" + addr, log: logFile.Name(), done: make(chan struct{})}
	s.cmd = exec.Command(bin, "serve")
	s.cmd.Env = append([]string{"VIBRANCE_PUBLIC_ORIGIN=" + s.base, "VIBRANCE_HTTP_ADDR=" + addr, "VIBRANCE_SCAN_INTERVAL=24h"}, env...)
	s.cmd.Stdout, s.cmd.Stderr = logFile, logFile
	began := time.Now()
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = s.cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		select {
		case <-s.done:
		default:
			if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			<-s.done
		}
	})
	probe := &http.Client{Timeout: 5 * time.Second}
	for {
		resp, err := probe.Get(s.base + "/health/ready")
		if err == nil {
			status := resp.StatusCode
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if status == http.StatusOK {
				s.startup = time.Since(began)
				return s
			}
		}
		select {
		case <-s.done:
			t.Fatalf("the server ended before it was ready: %v\n%s", s.cmd.ProcessState, s.logTail())
		default:
		}
		if time.Since(began) > 2*time.Minute {
			t.Fatalf("the server is not ready after two minutes\n%s", s.logTail())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// stop ends the server as `docker stop` does, and checks that it ended
// well.
func (s *server) stop() {
	s.t.Helper()
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		s.t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-time.After(time.Minute):
		s.t.Fatalf("the server did not stop in a minute\n%s", s.logTail())
	}
	if code := s.cmd.ProcessState.ExitCode(); code != 0 {
		s.t.Fatalf("the server exited with %d\n%s", code, s.logTail())
	}
}

// logTail is the end of the log of the server, which holds no secret (I5,
// T28).
func (s *server) logTail() string {
	data, err := os.ReadFile(s.log)
	if err != nil {
		return err.Error()
	}
	return string(data[max(0, len(data)-4000):])
}

// scanEvent is what the server logs when a cycle of the scanner ends.
type scanEvent struct {
	Msg        string `json:"msg"`
	OK         bool   `json:"ok"`
	State      string `json:"state"`
	Discovered int    `json:"discovered"`
	Indexed    int    `json:"indexed"`
	Absent     int    `json:"absent"`
	Problems   int    `json:"problems"`
	DurationMS int64  `json:"duration_ms"`
}

// scans returns the cycles of the scanner that have ended so far, read
// from the log of the server.
func (s *server) scans() []scanEvent {
	s.t.Helper()
	f, err := os.Open(s.log)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			s.t.Error(err)
		}
	}()
	if _, err := f.Seek(s.logAt, io.SeekStart); err != nil {
		s.t.Fatal(err)
	}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// A line that is not whole yet is read again the next time.
			break
		}
		s.logAt += int64(len(line))
		if !bytes.Contains(line, []byte(`"scan finished"`)) {
			continue
		}
		var ev scanEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			s.t.Fatalf("a line of the log is not JSON: %v", err)
		}
		if ev.Msg == "scan finished" {
			s.seen = append(s.seen, ev)
		}
	}
	return s.seen
}

// waitScans waits until n cycles of the scanner have ended, and returns the
// last.
func (s *server) waitScans(n int, limit time.Duration) scanEvent {
	s.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		if seen := s.scans(); len(seen) >= n {
			return seen[n-1]
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("%d cycles of the scanner have not ended in %s\n%s", n, limit, s.logTail())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// status reads a field of /proc/<pid>/status, in kB: VmRSS is the memory
// the process holds now, VmHWM the most it ever held.
func (s *server) memory(field string) int64 {
	s.t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(s.cmd.Process.Pid) + "/status")
	if err != nil {
		s.t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, field+":"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				s.t.Fatal(err)
			}
			return kb
		}
	}
	s.t.Fatalf("no %s in the status of the process", field)
	return 0
}

// gcLine is the line the Go runtime writes for each collection when the
// server runs with GODEBUG=gctrace=1: when it ran, and the heap it left in
// use, in MB.
var gcLine = regexp.MustCompile(`^gc \d+ @([0-9.]+)s .* \d+->\d+->(\d+) MB, `)

// lastGC returns the last collection the log of the server records: when it
// ran, in seconds since the start of the process, and the heap it left in
// use, in MB.
func (s *server) lastGC() (at float64, live int) {
	s.t.Helper()
	data, err := os.ReadFile(s.log)
	if err != nil {
		s.t.Fatal(err)
	}
	found := false
	for line := range strings.SplitSeq(string(data), "\n") {
		m := gcLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var errAt, errLive error
		at, errAt = strconv.ParseFloat(m[1], 64)
		live, errLive = strconv.Atoi(m[2])
		if err := errors.Join(errAt, errLive); err != nil {
			s.t.Fatal(err)
		}
		found = true
	}
	if !found {
		s.t.Fatal("the log of the server records no collection: GODEBUG=gctrace=1 is not set")
	}
	return at, live
}

// cpu is the processor time the server has used so far, in user and in
// kernel mode.
func (s *server) cpu() time.Duration {
	s.t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(s.cmd.Process.Pid) + "/stat")
	if err != nil {
		s.t.Fatal(err)
	}
	// The fields after the name of the program, which is in brackets: the
	// 12th and the 13th of them are utime and stime, in ticks of 10 ms.
	fields := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
	utime, err1 := strconv.ParseInt(fields[11], 10, 64)
	stime, err2 := strconv.ParseInt(fields[12], 10, 64)
	if err := errors.Join(err1, err2); err != nil {
		s.t.Fatal(err)
	}
	return time.Duration(utime+stime) * 10 * time.Millisecond
}

// client is one client of the API: one connection, kept alive, and the
// token of one account.
type client struct {
	t     *testing.T
	base  string
	token string
	http  *http.Client
}

// signIn starts a session of an account of the dataset.
func (s *server) signIn(username string) *client {
	s.t.Helper()
	c := &client{t: s.t, base: s.base, http: &http.Client{Timeout: 2 * time.Minute}}
	var result struct {
		Token string `json:"token"`
	}
	c.must(http.StatusCreated, &result, http.MethodPost, "/api/v1/auth/tokens",
		map[string]string{"username": username, "password": testPassword, "device_name": "perf"})
	c.token = result.Token
	return c
}

// do sends one request and reads the whole answer. The time is that of
// both: what a client waits for.
func (c *client) do(method, path string, body any, header ...string) (status int, answer []byte, took time.Duration) {
	c.t.Helper()
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.base+path, payload)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Vibrance-Request", "1")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	began := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	answer, err = io.ReadAll(resp.Body)
	took = time.Since(began)
	if err := errors.Join(err, resp.Body.Close()); err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp.StatusCode, answer, took
}

// must is do for a request that has to succeed with that status; the answer
// is decoded into out when out is not nil.
func (c *client) must(want int, out any, method, path string, body any, header ...string) time.Duration {
	c.t.Helper()
	status, answer, took := c.do(method, path, body, header...)
	if status != want {
		// Not the body: a refused sign-in echoes nothing secret, but no
		// answer is worth the risk (I5).
		c.t.Fatalf("%s %s: status %d, want %d", method, path, status, want)
	}
	if out != nil {
		if err := json.Unmarshal(answer, out); err != nil {
			c.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return took
}

// get is must for a GET that answers 200.
func (c *client) get(path string, out any) time.Duration {
	c.t.Helper()
	return c.must(http.StatusOK, out, http.MethodGet, path, nil)
}

// measure is the times of one kind of request, with its budget.
type measure struct {
	name string
	// budget is what the 95th percentile must not exceed; 0 for a time
	// that is only reported.
	budget time.Duration
	// open, when not "", is the entry of NOTES.md that says why this
	// measure is known to miss its budget: over the budget it is reported
	// as over and open, and it fails the test only over ceiling, which a
	// regression of what was measured would pass.
	open    string
	ceiling time.Duration
	samples []time.Duration
}

func (m *measure) add(d time.Duration) { m.samples = append(m.samples, d) }

// percentile is the nearest-rank percentile of the samples.
func (m *measure) percentile(p int) time.Duration {
	sorted := slices.Sorted(slices.Values(m.samples))
	return sorted[max(0, (len(sorted)*p+99)/100-1)]
}

// report is the table of the measures of a test.
type report struct {
	t        *testing.T
	measures []*measure
	lines    []string
}

func (r *report) measure(name string, budget time.Duration) *measure {
	m := &measure{name: name, budget: budget}
	r.measures = append(r.measures, m)
	return m
}

// value reports a quantity that is not a time of requests, and fails the
// test when it is over its limit; a limit of 0 only reports.
func (r *report) value(name string, value, limit float64, unit string) {
	r.t.Helper()
	verdict := "-"
	if limit > 0 {
		verdict = "ok"
		if value > limit {
			verdict = "OVER"
			r.t.Errorf("%s: %.1f %s, over the budget of %.0f %s", name, value, unit, limit, unit)
		}
	}
	bound := "-"
	if limit > 0 {
		bound = fmt.Sprintf("%.0f %s", limit, unit)
	}
	r.lines = append(r.lines, fmt.Sprintf("PERF | %-58s | %5s | %10s | %10.1f %-2s | %10s | %9s | %s", name, "1", "", value, unit, "", bound, verdict))
}

func ms(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d.Microseconds())/1000) }

// thousands writes n with a comma between its thousands, as the names of
// the measures do.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// print writes the table and fails the test for every measure whose 95th
// percentile is over its budget.
func (r *report) print() {
	r.t.Helper()
	fmt.Printf("PERF | %-58s | %5s | %10s | %13s | %10s | %9s | %s\n", "measure", "n", "p50", "p95", "max", "budget", "")
	for _, m := range r.measures {
		if len(m.samples) == 0 {
			r.t.Errorf("%s: no sample", m.name)
			continue
		}
		p95 := m.percentile(95)
		budget, verdict := "-", "-"
		if m.budget > 0 {
			budget, verdict = ms(m.budget), "ok"
			switch {
			case m.open != "" && m.ceiling <= m.budget:
				r.t.Errorf("%s: open (%s) without a ceiling above its budget", m.name, m.open)
			case p95 > m.budget && m.open != "" && p95 <= m.ceiling:
				verdict = "OVER, open: " + m.open + ", ceiling " + ms(m.ceiling)
			case p95 > m.budget && m.open != "":
				verdict = "OVER the ceiling"
				r.t.Errorf("%s: p95 %s, over the ceiling of %s of a measure open in %s", m.name, ms(p95), ms(m.ceiling), m.open)
			case p95 > m.budget:
				verdict = "OVER"
				r.t.Errorf("%s: p95 %s, over the budget of %s", m.name, ms(p95), ms(m.budget))
			}
		}
		fmt.Printf("PERF | %-58s | %5d | %10s | %13s | %10s | %9s | %s\n", m.name, len(m.samples),
			ms(m.percentile(50)), ms(p95), ms(m.percentile(100)), budget, verdict)
	}
	for _, line := range r.lines {
		fmt.Println(line)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// noEnv is an empty environment, and nonRoot the uid of an ordinary user.
func noEnv(string) string { return "" }

const nonRoot = 1000

// noInput is an empty standard input.
func noInput() io.Reader { return strings.NewReader("") }

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, nil))
}

func TestVersion(t *testing.T) {
	var stdout, logs bytes.Buffer
	code := run([]string{"version"}, noEnv, nonRoot, noInput(), &stdout, newLogger(&logs))
	if code != exitOK {
		t.Fatalf("exit code %d, want %d", code, exitOK)
	}
	if got, want := stdout.String(), "version: devel\n"; got != want {
		t.Fatalf("stdout %q, want %q", got, want)
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected log output: %s", logs.String())
	}
}

// Anything but exactly one known subcommand is refused before doing
// anything, with exit code 2 and one error line with the stable code
// "usage" (§11.4).
func TestUsage(t *testing.T) {
	for i, args := range [][]string{
		nil,
		{},
		{""},
		{"Version"},
		{"--version"},
		{"--help"},
		{"version", "extra"},
		{"extra", "version"},
		{"Serve"},
		{"serve", "extra"},
		{"serve", "--help"},
		{"healthcheck", "extra"},
		{"healthcheck", "serve"},
		{"usr", "create", "--username", "anna", "--password", usageSecret},
		{"serve", "--password", usageSecret},
		{"--password=" + usageSecret},
	} {
		var stdout, logs bytes.Buffer
		code := run(args, noEnv, nonRoot, noInput(), &stdout, newLogger(&logs))
		if code != exitUsage {
			t.Errorf("case %d: exit code %d, want %d", i, code, exitUsage)
		}
		if stdout.Len() != 0 {
			t.Errorf("case %d: unexpected stdout", i)
		}
		// A mistyped command line can hold a password: the log never
		// repeats an argument (I5).
		if strings.Contains(logs.String(), usageSecret) {
			t.Fatalf("case %d: the usage error logs an argument", i)
		}
		wantLog(t, logs.Bytes(), "usage")
	}
}

// usageSecret stands for a password typed on a mistyped command line.
const usageSecret = "typed-secret-0123"

// A version that cannot be written is a failure (exit 1), never a success.
func TestVersionWriteFails(t *testing.T) {
	var logs bytes.Buffer
	code := run([]string{"version"}, noEnv, nonRoot, noInput(), failingWriter{}, newLogger(&logs))
	if code != exitFailure {
		t.Fatalf("exit code %d, want %d", code, exitFailure)
	}
	wantLog(t, logs.Bytes(), "version_output")
}

// The real binary, stamped the way the Dockerfile's build-app stage stamps
// it, prints the stamped version: a mistyped -X path would silently leave
// "devel".
func TestStampedBinary(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is required: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	bin := filepath.Join(t.TempDir(), "vibrance")
	const version = "1.2.3-test+stamp"
	build := exec.CommandContext(ctx, goTool, "build",
		"-ldflags", "-X vibrance/internal/buildinfo.Version="+version, "-o", bin, ".")
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		t.Fatalf("vibrance version: %v", err)
	}
	if got, want := string(out), "version: "+version+"\n"; got != want {
		t.Fatalf("vibrance version printed %q, want %q", got, want)
	}

	var exitErr *exec.ExitError
	out, err = exec.CommandContext(ctx, bin, "no-such-command").Output()
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != exitUsage {
		t.Fatalf("vibrance no-such-command: %v, want exit code %d", err, exitUsage)
	}
	wantLog(t, out, "usage")
}

// wantLog checks that logs is exactly one JSON line at level ERROR with the
// given code, and returns its message.
func wantLog(t *testing.T, logs []byte, code string) string {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(logs, []byte("\n")), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("want one log line, got %d: %q", len(lines), logs)
	}
	var entry struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(lines[0], &entry); err != nil {
		t.Fatalf("log line %q is not JSON: %v", lines[0], err)
	}
	if entry.Level != "ERROR" || entry.Code != code {
		t.Fatalf("log line %q: level %q code %q, want ERROR %q", lines[0], entry.Level, entry.Code, code)
	}
	return entry.Msg
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// syncBuffer is an io.Writer safe for the concurrent writes of a child
// process and the reads of a test.
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

// messages returns the "msg" of every log event, in order, but for the
// events of the cycles of the scanner ("scan skipped", "scan finished"):
// they come from the goroutine of the scanner, at any moment between
// "scanner started" and "scanner stopped", so they have no place in the
// order of the startup and of the stop. The tests of the scanner look at
// them through events.
func (s *syncBuffer) messages(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, ev := range s.events(t) {
		msg, _ := ev["msg"].(string)
		if msg == "scan skipped" || msg == "scan finished" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

// freeAddr returns a loopback address with nothing listening on it.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

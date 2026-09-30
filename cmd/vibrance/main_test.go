package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestVersion(t *testing.T) {
	var stdout, logs bytes.Buffer
	code := run([]string{"version"}, &stdout, slog.New(slog.NewJSONHandler(&logs, nil)))
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

// Anything but exactly `version` is refused before doing anything, with exit
// code 2 and one error line with the stable code "usage" (§11.4).
func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{},
		{""},
		{"serve"},
		{"Version"},
		{"--version"},
		{"version", "extra"},
		{"extra", "version"},
	} {
		var stdout, logs bytes.Buffer
		code := run(args, &stdout, slog.New(slog.NewJSONHandler(&logs, nil)))
		if code != exitUsage {
			t.Errorf("%q: exit code %d, want %d", args, code, exitUsage)
		}
		if stdout.Len() != 0 {
			t.Errorf("%q: unexpected stdout %q", args, stdout.String())
		}
		wantLog(t, logs.Bytes(), "usage")
	}
}

// A version that cannot be written is a failure (exit 1), never a success.
func TestVersionWriteFails(t *testing.T) {
	var logs bytes.Buffer
	code := run([]string{"version"}, failingWriter{}, slog.New(slog.NewJSONHandler(&logs, nil)))
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
// given code.
func wantLog(t *testing.T, logs []byte, code string) {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(logs, []byte("\n")), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("want one log line, got %d: %q", len(lines), logs)
	}
	var entry struct {
		Level string `json:"level"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(lines[0], &entry); err != nil {
		t.Fatalf("log line %q is not JSON: %v", lines[0], err)
	}
	if entry.Level != "ERROR" || entry.Code != code {
		t.Fatalf("log line %q: level %q code %q, want ERROR %q", lines[0], entry.Level, entry.Code, code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

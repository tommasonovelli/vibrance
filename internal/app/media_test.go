package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"vibrance/internal/media"
)

// testWorkers is the VIBRANCE_WORKERS of the servers the tests make.
const testWorkers = 2

// fakeTool writes an executable shell script: a stand-in for an ffmpeg or
// ffprobe that is of another version, broken or stuck. The real tools are
// the pinned ones of the image.
func fakeTool(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Step 4 of the startup (DESIGN.md §11.2): the server reads the version of
// ffmpeg and ffprobe before it is ready, after the database is open, and
// keeps the adapter of the tools it verified.
func TestStartupVerifiesTheTools(t *testing.T) {
	r := startServer(t, nil)
	waitReady(t, "http://"+r.addr)
	if r.s.tools == nil || r.s.tools.Version() != media.PinnedVersion {
		t.Fatalf("the server has the tools %v, want those at version %s", r.s.tools, media.PinnedVersion)
	}
	if r.s.ffmpegPath != "/usr/local/bin/ffmpeg" || r.s.ffprobePath != "/usr/local/bin/ffprobe" {
		t.Fatalf("the server runs %s and %s, want the tools of the image", r.s.ffmpegPath, r.s.ffprobePath)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}

	msgs := r.logs.messages(t)
	i := slices.Index(msgs, "media tools verified")
	if i < 0 || slices.Index(msgs, "database open") > i || slices.Index(msgs, "ready") < i {
		t.Fatalf("log events %q: the tools must be verified after the database is open and before the server is ready", msgs)
	}
	if ev := r.logs.events(t)[i]; ev["version"] != media.PinnedVersion || ev["level"] != "INFO" {
		t.Fatalf("the event does not report the version: %v", ev)
	}
}

// A server whose ffmpeg or ffprobe is missing, or is not the pinned
// version, refuses to start: the run ends with the code of the refusal,
// the server was never ready, and it leaves nothing open.
func TestStartupRefusesWrongTools(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	older := fakeTool(t, "ffmpeg", `echo "ffmpeg version 7.1.2 Copyright (c) 2000-2025 the FFmpeg developers"`)
	otherBuild := fakeTool(t, "ffprobe", `echo "ffprobe version 8.1.3 Copyright (c) 2007-2026 the FFmpeg developers"`)
	broken := fakeTool(t, "ffmpeg", `echo "cannot run" >&2; exit 127`)

	for _, tc := range []struct {
		name            string
		ffmpeg, ffprobe string
		code            string
		want            string // substring of the error
	}{
		{"ffmpeg is missing", missing, media.FFprobePath, media.CodeToolUnavailable, "checking ffmpeg and ffprobe: media_tool_unavailable (missing): cannot start the tool"},
		{"ffprobe is missing", media.FFmpegPath, missing, media.CodeToolUnavailable, "checking ffmpeg and ffprobe: media_tool_unavailable (missing): cannot start the tool"},
		{"ffmpeg does not run", broken, media.FFprobePath, media.CodeToolUnavailable, "media_tool_unavailable (ffmpeg): exit status 127"},
		{"ffmpeg is another release", older, media.FFprobePath, media.CodeToolVersion, `media_tool_version (ffmpeg): version "7.1.2" is not the pinned 8.1.3-musiclib1`},
		{"ffprobe is another build", media.FFmpegPath, otherBuild, media.CodeToolVersion, `media_tool_version (ffprobe): version "8.1.3" is not the pinned 8.1.3-musiclib1`},
		{"ffprobe is ffmpeg", media.FFmpegPath, media.FFmpegPath, media.CodeToolUnavailable, "media_tool_unavailable (ffprobe): it does not print the version line of ffprobe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &syncBuffer{}
			s := newServer(newLogger(logs), t.TempDir(), testWorkers)
			s.ffmpegPath, s.ffprobePath = tc.ffmpeg, tc.ffprobe
			ln := listen(t)

			done := make(chan error, 1)
			go func() { done <- s.run(t.Context(), ln) }()
			var err error
			select {
			case err = <-done:
			case <-time.After(30 * time.Second):
				t.Fatalf("the refused startup did not end the run within 30s; logs:\n%s", logs)
			}
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != tc.code || Code(err) != tc.code {
				t.Fatalf("run returned %v (code %q), want code %q", err, Code(err), tc.code)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the error does not contain %q: %v", tc.want, err)
			}

			want := []string{"http listening", "database open", "http server stopped", "database closed"}
			if msgs := logs.messages(t); !slices.Equal(msgs, want) {
				t.Fatalf("log events %q, want %q", msgs, want)
			}
			if s.tools != nil {
				t.Fatal("the server kept tools it refused")
			}
			if got := s.state.Load(); got != stateStopping {
				t.Fatalf("state %d, want stopping", got)
			}
			wantJSON(t, serveDirectly(s, "/health/ready"), 503, shuttingDownJSON)
			if conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
				closeOnce(t, conn)
				t.Fatal("the server still accepts connections after the refused startup")
			}
		})
	}
}

// A stop asked for while a tool is being checked interrupts the check, and
// the tool with it: it is a stop like any other, and no process is left.
func TestStopDuringTheToolCheck(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	stuck := fakeTool(t, "ffmpeg", `echo $$ > "`+pidFile+`.tmp" && mv "`+pidFile+`.tmp" "`+pidFile+`" && exec sleep 300`)
	logs := &syncBuffer{}
	s := newServer(newLogger(logs), t.TempDir(), testWorkers)
	s.ffmpegPath = stuck
	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.run(ctx, ln) }()

	var pid string
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid = strings.TrimSpace(string(data))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the tool did not start within 30s; logs:\n%s", logs)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The server answers while the check runs, and is not ready.
	wantJSON(t, do(t, "GET", "http://"+ln.Addr().String()+"/health/ready"), 503, notReadyJSON)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v, want a normal stop", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the run did not end within 30s; logs:\n%s", logs)
	}
	want := []string{"http listening", "database open", "stopping", "http server stopped", "database closed"}
	if got := logs.messages(t); !slices.Equal(got, want) {
		t.Fatalf("log events %q, want %q", got, want)
	}
	if interrupted, _ := logs.events(t)[2]["interrupted"].(string); !strings.Contains(interrupted, "media_canceled") {
		t.Fatalf("the stopping event does not say what was interrupted: %v", logs.events(t)[2])
	}
	// The tool was killed: it is gone, or a zombie that waits to be
	// collected ("Z" is the third field of its stat line).
	deadline = time.Now().Add(5 * time.Second)
	for {
		stat, err := os.ReadFile("/proc/" + pid + "/stat")
		if errors.Is(err, os.ErrNotExist) || strings.Contains(string(stat), ") Z ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the tool %s is still running after the stop: %s", pid, stat)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

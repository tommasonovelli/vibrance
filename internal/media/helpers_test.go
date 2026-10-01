package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests of this package run the real pinned ffmpeg and ffprobe on the
// files of the fixture library, and real processes on the real kernel
// (DESIGN.md §12.1). /bin/sh is used only as a tool that starts other
// processes or misbehaves on purpose; the adapter never runs a shell.

// fixtureDir is testdata/library-v1, a real library/ written by MusicLib
// 1.1.0 (testdata/FIXTURE.md), from this package's folder.
var fixtureDir = filepath.Join("..", "..", "testdata", "library-v1")

const sh = "/bin/sh"

// helperEnv makes the test binary run as a helper process instead of
// running the tests (TestMain).
const helperEnv = "VIBRANCE_MEDIA_TEST_HELPER"

func TestMain(m *testing.M) {
	if pidFile := os.Getenv(helperEnv); pidFile != "" {
		os.Exit(helperServer(pidFile))
	}
	os.Exit(m.Run())
}

// helperServer stands for the server in TestRunToolDiesWithTheServer: a
// process that runs a tool through the Runner, and is then killed. The
// tool writes its pid to pidFile and waits.
func helperServer(pidFile string) int {
	_, err := NewRunner(1).Run(context.Background(), Command{
		Path:    sh,
		Args:    []string{"-c", `echo $$ > "$1.tmp" && mv "$1.tmp" "$1" && exec sleep 300`, "sh", pidFile},
		Timeout: time.Hour,
	})
	fmt.Fprintln(os.Stderr, "the run returned:", err)
	return 1
}

// newTools returns the adapter of the real tools.
func newTools(t *testing.T) *Tools {
	t.Helper()
	tools, err := NewTools(t.Context(), NewRunner(4), FFmpegPath, FFprobePath)
	if err != nil {
		t.Fatalf("NewTools: %v", err)
	}
	return tools
}

// open opens the file at path for reading, and closes it when the test
// ends.
func open(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

// inFixture is the path of rel, a path relative to the fixture library.
func inFixture(rel string) string {
	return filepath.Join(fixtureDir, filepath.FromSlash(rel))
}

// containerOf is the container of a file of the library, by its
// extension, as the caller of the adapter names it.
func containerOf(t *testing.T, name string) Container {
	t.Helper()
	switch filepath.Ext(name) {
	case ".flac":
		return ContainerFLAC
	case ".mp3":
		return ContainerMP3
	case ".m4a":
		return ContainerM4A
	}
	t.Fatalf("%s is not a track file", name)
	return ""
}

// wantCode checks that err is an *Error with the given code.
func wantCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v (%T), want a *media.Error with code %s", err, err, code)
	}
	if e.Code != code || Code(err) != code {
		t.Fatalf("error %v has code %s, want %s; stderr: %s", err, e.Code, code, e.Stderr)
	}
	return e
}

// writeFile creates a file with the given content and mode.
func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) string {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// script writes an executable shell script: a stand-in for a tool that is
// missing, of another version or broken.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	return writeFile(t, filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755)
}

// ffmpegMake runs the real ffmpeg with args to make a test file. It is the
// helper of §12.2: tests that need a file MusicLib did not write (other
// tags on the same audio, streams in another order) make it from the
// fixture or from a synthetic source. Paths are fine here: the names are
// the test's own.
func ffmpegMake(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, FFmpegPath, append([]string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %q: %v\n%s", args, err, out)
	}
}

// streamTypes are the codec_type of the streams of the file at path, in
// order, as a plain ffprobe run reports them.
func streamTypes(t *testing.T, path string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, FFprobePath, "-hide_banner", "-loglevel", "error",
		"-show_entries", "stream=codec_type", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return strings.Fields(string(out))
}

// ---------------------------------------------------------------------------
// Processes, seen through /proc.

// procState returns the state letter of pid ("R", "S", "Z", ...) and its
// process group; ok is false if the process does not exist.
func procState(pid int) (state string, pgrp int, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	// pid (comm) state ppid pgrp ...; comm may hold spaces and parentheses.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return "", 0, false
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 3 {
		return "", 0, false
	}
	pgrp, err = strconv.Atoi(f[2])
	if err != nil {
		return "", 0, false
	}
	return f[0], pgrp, true
}

// alive reports whether pid exists and still runs. A zombie does not: it
// is dead, and only waits for its parent to collect it.
func alive(pid int) bool {
	state, _, ok := procState(pid)
	return ok && state != "Z" && state != "X"
}

// groupMembers returns the processes of a process group that still run.
func groupMembers(t *testing.T, pgid int) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if _, pg, ok := procState(pid); ok && pg == pgid && alive(pid) {
			out = append(out, pid)
		}
	}
	return out
}

// wantDead fails the test if pid still runs after a moment. SIGKILL
// cannot be ignored, but the kernel delivers it when it schedules the
// process.
func wantDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still running", pid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readPids waits for the file of pids that a test tool writes once it has
// started its children, and returns them.
func readPids(t *testing.T, path string) []int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var pids []int
			for _, f := range strings.Fields(string(data)) {
				pid, err := strconv.Atoi(f)
				if err != nil {
					t.Fatalf("the tool wrote %q, not pids", data)
				}
				pids = append(pids, pid)
			}
			return pids
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the tool did not start within 30s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

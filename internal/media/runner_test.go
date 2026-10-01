package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// family is a tool that starts two other processes and waits for them
// forever. Once all three run it writes their pids, its own first, to the
// file named by its argument.
const family = `sleep 300 & a=$!; sleep 300 & b=$!; echo "$$ $a $b" > "$1.tmp" && mv "$1.tmp" "$1"; wait`

// A tool that does not end is killed at its timeout, with everything it
// started: no process of its group is left running, as /proc shows
// (DESIGN.md I8, T2).
func TestRunTimeoutLeavesNoProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	const timeout = time.Second
	start := time.Now()
	res, err := NewRunner(1).Run(t.Context(), Command{Path: sh, Args: []string{"-c", family, "sh", pidFile}, Timeout: timeout})
	elapsed := time.Since(start)

	e := wantCode(t, err, CodeTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a timeout must wrap context.DeadlineExceeded: %v", err)
	}
	if !strings.Contains(e.Msg, timeout.String()) {
		t.Fatalf("the error does not name the timeout: %v", err)
	}
	if elapsed < timeout || elapsed > timeout+5*time.Second {
		t.Fatalf("Run returned after %s, with a timeout of %s", elapsed, timeout)
	}
	if len(res.Stdout) != 0 || len(res.Stderr) != 0 {
		t.Fatalf("output of a silent tool: %q, %q", res.Stdout, res.Stderr)
	}

	pids := readPids(t, pidFile)
	if len(pids) != 3 {
		t.Fatalf("the tool wrote %v, want its pid and two children", pids)
	}
	for _, pid := range pids {
		wantDead(t, pid)
	}
	if members := groupMembers(t, pids[0]); len(members) != 0 {
		t.Fatalf("processes of the tool's group are still running: %v", members)
	}
}

// When the context is cancelled the whole process group of the tool is
// killed, not only the tool.
func TestRunCancelKillsTheGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewRunner(1).Run(ctx, Command{Path: sh, Args: []string{"-c", family, "sh", pidFile}, Timeout: time.Hour})
		done <- err
	}()

	pids := readPids(t, pidFile)
	if len(pids) != 3 {
		t.Fatalf("the tool wrote %v, want its pid and two children", pids)
	}
	// The tool leads a group of its own, which is not the test's, and its
	// children are in it.
	pgid := pids[0]
	if pgid == syscall.Getpgrp() {
		t.Fatal("the tool is in the process group of the test")
	}
	for _, pid := range pids {
		if state, pg, ok := procState(pid); !ok || pg != pgid || state == "Z" {
			t.Fatalf("process %d: state %q, group %d, exists %v; want it running in group %d", pid, state, pg, ok, pgid)
		}
	}
	members := groupMembers(t, pgid)
	slices.Sort(members)
	slices.Sort(pids)
	if !slices.Equal(members, pids) {
		t.Fatalf("the group %d has %v, want %v", pgid, members, pids)
	}

	cancel()
	select {
	case err := <-done:
		wantCode(t, err, CodeCanceled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancellation must wrap context.Canceled: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after the cancellation")
	}
	for _, pid := range pids {
		wantDead(t, pid)
	}
	if members := groupMembers(t, pgid); len(members) != 0 {
		t.Fatalf("processes of the tool's group are still running: %v", members)
	}
}

// A server that is killed, even with SIGKILL, leaves no tool running: the
// kernel kills the tool when its parent dies (T2). The parent here is a
// real process that runs a tool through the Runner.
func TestRunToolDiesWithTheServer(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	server := exec.Command(os.Args[0])
	server.Env = []string{helperEnv + "=" + pidFile}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = server.Process.Kill() // only when the test failed before
			_ = server.Wait()
		}
	})

	pids := readPids(t, pidFile)
	if len(pids) != 1 {
		t.Fatalf("the tool wrote %v, want its pid", pids)
	}
	tool := pids[0]
	t.Cleanup(func() {
		// Only matters if the test fails: do not leave the tool behind.
		if alive(tool) {
			_ = syscall.Kill(tool, syscall.SIGKILL)
		}
	})
	if !alive(tool) {
		t.Fatalf("the tool %d is not running", tool)
	}
	// It must still be there after a while: the kernel sends the signal
	// when the thread that started the tool ends, and the Runner keeps
	// that thread for as long as the tool runs.
	time.Sleep(200 * time.Millisecond)
	if !alive(tool) {
		t.Fatalf("the tool %d died while the server was running", tool)
	}

	if err := server.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	err := server.Wait()
	waited = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the server ended with %v, want killed", err)
	}
	wantDead(t, tool)
}

// The kernel sends the parent-death signal when the thread that started
// the tool ends, and the Go runtime ends a thread when a goroutine that
// locked it returns. A tool must outlive that: here goroutines keep ending
// threads while tools run, and no tool is killed.
func TestRunToolOutlivesTheThreadsOfTheServer(t *testing.T) {
	stop := make(chan struct{})
	var churn sync.WaitGroup
	for range 4 {
		churn.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				ended := make(chan struct{})
				go func() {
					// Never unlocked: the thread ends with the goroutine.
					runtime.LockOSThread()
					close(ended)
				}()
				<-ended
			}
		})
	}

	const callers, runs = 8, 6
	r := NewRunner(callers)
	var tools sync.WaitGroup
	for range callers {
		tools.Go(func() {
			for range runs {
				if _, err := r.Run(t.Context(), Command{Path: "/usr/bin/sleep", Args: []string{"0.1"}, Timeout: time.Minute}); err != nil {
					t.Errorf("a tool did not run to its end: %v", err)
				}
			}
		})
	}
	tools.Wait()
	close(stop)
	churn.Wait()
}

// The semaphore: at most `slots` tools run at once, however many callers
// there are. Each tool registers itself in a folder while it runs and
// reports how many it saw there, so the bound is measured on the processes.
func TestRunSemaphoreBoundsTheTools(t *testing.T) {
	const slots, callers = 2, 8
	dir := t.TempDir()
	r := NewRunner(slots)
	const register = `mkdir "$1/$$" && n=$(ls "$1" | wc -l) && sleep 0.2 && rmdir "$1/$$" && echo "$n"`
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen []int
	)
	for range callers {
		wg.Go(func() {
			res, err := r.Run(t.Context(), Command{Path: sh, Args: []string{"-c", register, "sh", dir}, Timeout: time.Minute})
			if err != nil {
				t.Error(err)
				return
			}
			n, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
			if err != nil {
				t.Errorf("output %q", res.Stdout)
				return
			}
			mu.Lock()
			seen = append(seen, n)
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(seen) != callers {
		t.Fatalf("%d tools reported, want %d", len(seen), callers)
	}
	if peak := slices.Max(seen); peak != slots {
		t.Fatalf("at most %d tools ran at once, want exactly %d: the semaphore has %d slots and %d callers", peak, slots, slots, callers)
	}
}

// A caller that waits for a slot is released by its context, and its tool
// never starts. The slot of a cancelled run is free again.
func TestRunCancelWhileWaitingForASlot(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(1)
	busyCtx, stopBusy := context.WithCancel(t.Context())
	defer stopBusy()
	busy := make(chan error, 1)
	go func() {
		_, err := r.Run(busyCtx, Command{Path: sh, Timeout: time.Hour,
			Args: []string{"-c", `echo $$ > "$1.tmp" && mv "$1.tmp" "$1" && exec sleep 300`, "sh", filepath.Join(dir, "busy")}})
		busy <- err
	}()
	readPids(t, filepath.Join(dir, "busy")) // the only slot is taken

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := filepath.Join(dir, "started")
	_, err := r.Run(ctx, Command{Path: "/usr/bin/touch", Args: []string{started}, Timeout: time.Minute})
	wantCode(t, err, CodeCanceled)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the error must wrap the error of the caller's context: %v", err)
	}
	if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the tool started without a slot (stat: %v)", err)
	}

	stopBusy()
	wantCode(t, <-busy, CodeCanceled)
	if _, err := r.Run(t.Context(), Command{Path: "/usr/bin/touch", Args: []string{started}, Timeout: time.Minute}); err != nil {
		t.Fatalf("the slot is not free again: %v", err)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal(err)
	}
}

// A run whose context is over already does not start its tool.
func TestRunWithAContextThatIsOver(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := filepath.Join(t.TempDir(), "started")
	for range 20 { // a free slot and a finished context are both ready: either may be seen first
		_, err := NewRunner(1).Run(ctx, Command{Path: "/usr/bin/touch", Args: []string{started}, Timeout: time.Minute})
		wantCode(t, err, CodeCanceled)
	}
	if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the tool started (stat: %v)", err)
	}
}

// The standard error is kept up to 64 KiB and the rest is dropped: a tool
// that floods it neither blocks nor fills the memory (I8).
func TestRunStderrIsBounded(t *testing.T) {
	if stderrLimit != 64<<10 {
		t.Fatalf("stderrLimit = %d, DESIGN.md I8 and T2 say 64 KiB", stderrLimit)
	}
	res, err := NewRunner(1).Run(t.Context(), Command{
		Path: sh, Args: []string{"-c", `head -c 5000000 /dev/zero | tr '\0' x >&2; echo done`}, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stderr) != stderrLimit || !res.StderrTruncated {
		t.Fatalf("stderr: %d bytes kept, truncated %v; want %d bytes, truncated", len(res.Stderr), res.StderrTruncated, stderrLimit)
	}
	if strings.Trim(string(res.Stderr), "x") != "" {
		t.Fatal("the bytes kept are not the first the tool wrote")
	}
	if string(res.Stdout) != "done\n" {
		t.Fatalf("stdout %q: the tool did not run to its end", res.Stdout)
	}

	// Below the limit it is kept whole, also when the tool fails; the
	// error carries it, but not in its text.
	res, err = NewRunner(1).Run(t.Context(), Command{Path: sh, Args: []string{"-c", "echo secret title >&2; exit 4"}, Timeout: time.Minute})
	e := wantCode(t, err, CodeToolFailed)
	if string(res.Stderr) != "secret title\n" || res.StderrTruncated || string(e.Stderr) != "secret title\n" {
		t.Fatalf("stderr %q (truncated %v), in the error %q", res.Stderr, res.StderrTruncated, e.Stderr)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("the text of the error quotes the standard error: %v", err)
	}
}

// A tool that writes more to its standard output than the adapter reads
// fails, and what is kept is bounded.
func TestRunStdoutIsBounded(t *testing.T) {
	res, err := NewRunner(1).Run(t.Context(), Command{Path: "/usr/bin/head", Args: []string{"-c", "5000000", "/dev/zero"}, Timeout: time.Minute})
	wantCode(t, err, CodeOutputTooLarge)
	if len(res.Stdout) != stdoutLimit {
		t.Fatalf("%d bytes of stdout kept, want %d", len(res.Stdout), stdoutLimit)
	}
	// Exactly at the limit is fine.
	res, err = NewRunner(1).Run(t.Context(), Command{Path: "/usr/bin/head", Args: []string{"-c", strconv.Itoa(stdoutLimit), "/dev/zero"}, Timeout: time.Minute})
	if err != nil || len(res.Stdout) != stdoutLimit {
		t.Fatalf("a tool that writes %d bytes: %d kept, %v", stdoutLimit, len(res.Stdout), err)
	}
}

// Only the exit status says whether a tool succeeded: never what it
// printed (T2).
func TestRunOnlyTheExitStatusCounts(t *testing.T) {
	r := NewRunner(1)
	res, err := r.Run(t.Context(), Command{Path: sh, Timeout: time.Minute,
		Args: []string{"-c", `printf '{"streams": []}'; echo "completed successfully" >&2; exit 3`}})
	e := wantCode(t, err, CodeToolFailed)
	if e.Msg != "exit status 3" || len(res.Stdout) == 0 {
		t.Fatalf("error %v, output %q", err, res.Stdout)
	}

	res, err = r.Run(t.Context(), Command{Path: sh, Args: []string{"-c", `echo "Error: fatal" >&2; echo ok`}, Timeout: time.Minute})
	if err != nil || string(res.Stdout) != "ok\n" || string(res.Stderr) != "Error: fatal\n" {
		t.Fatalf("a tool that exits with 0: %v, %q, %q", err, res.Stdout, res.Stderr)
	}

	_, err = r.Run(t.Context(), Command{Path: sh, Args: []string{"-c", "kill -SEGV $$"}, Timeout: time.Minute})
	if e := wantCode(t, err, CodeToolFailed); !strings.Contains(e.Msg, "signal") {
		t.Fatalf("a tool killed by a signal: %v", err)
	}
}

// Tools that cannot start, and calls that cannot run one.
func TestRunToolsThatCannotStart(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(1)

	_, err := r.Run(t.Context(), Command{Path: filepath.Join(dir, "missing"), Timeout: time.Minute})
	wantCode(t, err, CodeToolUnavailable)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the cause is lost: %v", err)
	}

	notExecutable := writeFile(t, filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o644)
	_, err = r.Run(t.Context(), Command{Path: notExecutable, Timeout: time.Minute})
	wantCode(t, err, CodeToolUnavailable)

	// A bare name is never looked up in PATH: "sh" is there.
	_, err = r.Run(t.Context(), Command{Path: "sh", Args: []string{"-c", "true"}, Timeout: time.Minute})
	wantCode(t, err, CodeToolUnavailable)

	// No process without a timeout (I8): a command without one times out
	// before it starts.
	started := filepath.Join(dir, "started")
	_, err = r.Run(t.Context(), Command{Path: "/usr/bin/touch", Args: []string{started}})
	wantCode(t, err, CodeTimeout)
	if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the tool started without a timeout (stat: %v)", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("NewRunner(0) did not panic")
		}
	}()
	NewRunner(0)
}

// The tool gets descriptors 0, 1, 2 and the file it is given as 3, and
// nothing else the server has open; an empty environment; "/" as its
// working directory; a process group of its own.
func TestRunWhatTheToolInherits(t *testing.T) {
	dir := t.TempDir()
	input := open(t, writeFile(t, filepath.Join(dir, "input"), []byte("x"), 0o644))
	other := open(t, writeFile(t, filepath.Join(dir, "other"), []byte("y"), 0o644))
	t.Setenv("VIBRANCE_TEST_SECRET", "must not reach the tool")

	// ls, wc, readlink and cut are children of the shell: what they print
	// is about the shell, which is the tool.
	const inspect = `ls -l /proc/$$/fd
echo "environ $(wc -c < /proc/$$/environ)"
echo "cwd $(readlink /proc/$$/cwd)"
echo "pid $$"
echo "pgrp $(cut -d' ' -f5 /proc/$$/stat)"`
	res, err := NewRunner(1).Run(t.Context(), Command{Path: sh, Args: []string{"-c", inspect}, File: input, Timeout: time.Minute})
	if err != nil {
		t.Fatalf("%v; stderr: %s", err, res.Stderr)
	}
	fds, props := map[string]string{}, map[string]string{}
	for line := range strings.Lines(string(res.Stdout)) {
		f := strings.Fields(line)
		switch {
		case len(f) > 3 && f[len(f)-2] == "->":
			fds[f[len(f)-3]] = f[len(f)-1]
		case len(f) == 2 && f[0] != "total":
			props[f[0]] = f[1]
		}
	}
	if len(fds) != 4 {
		t.Fatalf("the tool has the descriptors %v, want exactly 0, 1, 2 and 3", fds)
	}
	if fds["0"] != "/dev/null" || !strings.HasPrefix(fds["1"], "pipe:") || !strings.HasPrefix(fds["2"], "pipe:") {
		t.Fatalf("descriptors 0, 1, 2 are %q, %q, %q; want /dev/null and two pipes", fds["0"], fds["1"], fds["2"])
	}
	if fds["3"] != input.Name() {
		t.Fatalf("descriptor 3 is %q, want the file given, %q", fds["3"], input.Name())
	}
	for fd, target := range fds {
		if target == other.Name() {
			t.Fatalf("the tool inherited a file it was not given, as descriptor %s", fd)
		}
	}
	if props["environ"] != "0" {
		t.Fatalf("the environment of the tool has %s bytes, want none", props["environ"])
	}
	if props["cwd"] != "/" {
		t.Fatalf("the working directory of the tool is %q, want /", props["cwd"])
	}
	if props["pgrp"] != props["pid"] || props["pgrp"] == strconv.Itoa(syscall.Getpgrp()) {
		t.Fatalf("the tool %s is in the group %s (the test's is %d), want a group of its own", props["pid"], props["pgrp"], syscall.Getpgrp())
	}
}

// No descriptor of the server is left open by a run, however it ends.
func TestRunLeaksNoDescriptor(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(2)
	input := open(t, writeFile(t, filepath.Join(dir, "input"), []byte("x"), 0o644))
	calls := []Command{
		{Path: sh, Args: []string{"-c", "echo out; echo err >&2"}, File: input, Timeout: time.Minute},
		{Path: sh, Args: []string{"-c", "exit 7"}, Timeout: time.Minute},
		{Path: sh, Args: []string{"-c", "sleep 300 & sleep 300"}, Timeout: 20 * time.Millisecond},
		{Path: "/usr/bin/head", Args: []string{"-c", "5000000", "/dev/zero"}, Timeout: time.Minute},
		{Path: filepath.Join(dir, "missing"), Timeout: time.Minute},
		{Path: sh},
	}
	runAll := func() {
		for _, c := range calls {
			_, _ = r.Run(t.Context(), c) // the outcomes are tested above
		}
		ctx, cancel := context.WithCancel(t.Context())
		go func() { time.Sleep(20 * time.Millisecond); cancel() }()
		_, _ = r.Run(ctx, Command{Path: sh, Args: []string{"-c", "sleep 300"}, Timeout: time.Minute})
		cancel()
	}
	openFDs := func() map[string]string {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		fds := map[string]string{}
		for _, e := range entries {
			target, _ := os.Readlink("/proc/self/fd/" + e.Name())
			fds[e.Name()] = target
		}
		return fds
	}

	runAll() // the descriptors the runtime opens once, on first use
	before := openFDs()
	for range 5 {
		runAll()
	}
	for fd, target := range openFDs() {
		if _, ok := before[fd]; !ok {
			t.Errorf("descriptor %s (%s) is left open", fd, target)
		}
	}
}

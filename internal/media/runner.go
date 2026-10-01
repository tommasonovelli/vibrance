package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// The limits of every run of a tool (DESIGN.md I8, T2, §6.3 step 4).
const (
	// ProbeTimeout bounds one ffprobe run.
	ProbeTimeout = 30 * time.Second
	// FingerprintTimeout bounds the fingerprint of one file, which reads
	// the whole file.
	FingerprintTimeout = 10 * time.Minute

	// stderrLimit is how much of a tool's standard error is kept. The rest
	// is read and dropped, so that the tool never blocks on a full pipe.
	stderrLimit = 64 << 10
	// stdoutLimit is how much of a tool's standard output is read: the
	// tools print a short report or one line. More than that is a failure
	// of the run, not something to keep in memory.
	stdoutLimit = 1 << 20
	// pipeGrace is how long a run waits, once its tool is gone, for the
	// pipes of the tool to close. Only a process that left the group of
	// the tool and kept a pipe can make it pass.
	pipeGrace = 10 * time.Second
)

// Runner runs the external tools. It is the only way Vibrance starts a
// process, and it carries the one semaphore of the server: at most `slots`
// tools run at once, whoever calls. There is one Runner per process, built
// at startup with VIBRANCE_WORKERS slots (§11.1). It has no package state
// and is safe for concurrent use.
type Runner struct {
	slots chan struct{}
}

// NewRunner returns a Runner that lets at most slots tools run at once.
// slots comes from the validated configuration: a value below 1 is a
// programming error, and panics.
func NewRunner(slots int) *Runner {
	if slots < 1 {
		panic("media: NewRunner needs at least one slot")
	}
	return &Runner{slots: make(chan struct{}, slots)}
}

// Command is one run of a tool.
//
// The tool is executed directly, never through a shell. It gets an empty
// environment, "/" as its working directory and these descriptors only:
// /dev/null as 0, pipes that the Runner reads as 1 and 2, and File as 3.
type Command struct {
	// Path is the absolute path of the executable.
	Path string
	Args []string
	// File, if not nil, is the tool's descriptor 3: the way a tool is
	// given a file to read, never a path in Args (T2). The tool shares its
	// offset.
	File *os.File
	// Timeout bounds the run, from the moment the tool has a slot. A
	// command without one times out at once.
	Timeout time.Duration
}

// Result is what a run leaves, also when it fails.
type Result struct {
	// Stdout is the standard output, at most stdoutLimit bytes of it.
	Stdout []byte
	// Stderr is the first stderrLimit bytes of the standard error. It says
	// why a tool failed; it never says that it succeeded.
	Stderr          []byte
	StderrTruncated bool
}

// Run waits for a slot, runs the tool and waits for it. It succeeds only
// if the tool exits with status 0 and wrote no more than stdoutLimit bytes.
// Otherwise the error is an *Error: CodeTimeout past c.Timeout,
// CodeCanceled when ctx ends (also while waiting for a slot),
// CodeToolUnavailable for a tool that cannot be started, CodeToolFailed for
// any other exit, CodeOutputTooLarge, CodeIO.
//
// On a timeout or a cancellation the whole process group of the tool is
// sent SIGKILL, and Run returns once the tool is gone and its pipes are
// closed.
func (r *Runner) Run(ctx context.Context, c Command) (Result, error) {
	op := filepath.Base(c.Path)
	if !filepath.IsAbs(c.Path) {
		// os/exec would look a bare name up in PATH.
		return Result{}, newErr(CodeToolUnavailable, op, "the path of the tool is not absolute", nil)
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return Result{}, newErr(CodeCanceled, op, "the context ended while waiting for a slot", ctx.Err())
	}
	defer func() { <-r.slots }()

	runCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, c.Path, c.Args...)
	cmd.Env = []string{} // not nil: the tool never inherits the environment
	cmd.Dir = "/"
	stdout := &limitedBuffer{limit: stdoutLimit}
	stderr := &limitedBuffer{limit: stderrLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if c.File != nil {
		cmd.ExtraFiles = []*os.File{c.File}
	}
	cmd.WaitDelay = pipeGrace
	confine(cmd)

	// The kernel sends the parent-death signal of confine when the thread
	// that started the tool ends, not the process. This goroutine keeps
	// its thread until the tool has been waited for, so the two are the
	// same.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	started := false
	err := cmd.Start()
	if err == nil {
		started = true
		err = cmd.Wait()
	}
	res := Result{Stdout: stdout.buf, Stderr: stderr.buf, StderrTruncated: stderr.truncated}
	fail := func(code, msg string, cause error) (Result, error) {
		return res, &Error{Code: code, Op: op, Msg: msg, Stderr: res.Stderr, Err: cause}
	}
	var exit *exec.ExitError
	switch {
	case err == nil && stdout.truncated:
		return fail(CodeOutputTooLarge, "the tool wrote more than the adapter reads", nil)
	case err == nil:
		return res, nil
	case ctx.Err() != nil:
		return fail(CodeCanceled, "the context ended", ctx.Err())
	case runCtx.Err() != nil:
		return fail(CodeTimeout, "killed after "+c.Timeout.String(), runCtx.Err())
	case !started:
		return fail(CodeToolUnavailable, "cannot start the tool", err)
	case errors.As(err, &exit):
		// "exit status 1", "signal: killed".
		return fail(CodeToolFailed, exit.Error(), nil)
	default:
		return fail(CodeIO, "waiting for the tool", err)
	}
}

// limitedBuffer keeps the first limit bytes written to it and drops the
// rest: the writer never fails and never blocks.
type limitedBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	keep := min(len(p), b.limit-len(b.buf))
	b.buf = append(b.buf, p[:keep]...)
	if keep < len(p) {
		b.truncated = true
	}
	return len(p), nil
}

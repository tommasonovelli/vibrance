package media

import "errors"

// The stable codes of the failures of the media adapter. The first two
// refuse the startup of the server (DESIGN.md §11.2 step 4); the others
// are failures of one call, which the caller turns into what they mean to
// it.
const (
	// CodeToolUnavailable: ffmpeg or ffprobe cannot be started, or does not
	// print the version line of the tool it should be.
	CodeToolUnavailable = "media_tool_unavailable"
	// CodeToolVersion: the tool runs but is not the pinned version.
	CodeToolVersion = "media_tool_version"

	// CodeTimeout: the tool ran longer than the timeout of the call and was
	// killed, with its whole process group.
	CodeTimeout = "media_timeout"
	// CodeCanceled: the caller's context ended; the tool, if it had
	// started, was killed with its whole process group.
	CodeCanceled = "media_canceled"
	// CodeToolFailed: the tool exited with a status other than 0, or was
	// killed by a signal. That is a failure whatever the tool printed.
	CodeToolFailed = "media_tool_failed"
	// CodeOutputTooLarge: the tool wrote more to its standard output than
	// the adapter reads.
	CodeOutputTooLarge = "media_output_too_large"
	// CodeOutputInvalid: the tool succeeded, but its output is not what the
	// adapter expects.
	CodeOutputInvalid = "media_output_invalid"
	// CodeNotSupported: the file is not audio that Vibrance reads: the
	// demuxer of its container refuses it, it has no audio stream, or its
	// codec is not the one of that container.
	CodeNotSupported = "media_not_supported"
	// CodeIO: a failure of the adapter around the tool: the input
	// descriptor, the pipes.
	CodeIO = "media_io"
)

// Error is the typed error of the package.
//
// Stderr holds at most the first 64 KiB of the standard error of the tool.
// It is not part of Error(): the messages of a demuxer can quote what is in
// the file, and the text of an error ends in the logs.
type Error struct {
	Code string
	// Op is what failed: the name of the tool.
	Op     string
	Msg    string
	Stderr []byte
	Err    error
}

func (e *Error) Error() string {
	msg := e.Code + " (" + e.Op + ")"
	if e.Msg != "" {
		msg += ": " + e.Msg
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// Code returns the code of the first *Error in err's tree, otherwise "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func newErr(code, op, msg string, err error) *Error {
	return &Error{Code: code, Op: op, Msg: msg, Err: err}
}

package media

import (
	"context"
	"os"
	"regexp"
)

// fingerprintArgs is the ffmpeg command line of DESIGN.md §5.4 for the
// file on descriptor 3: the packets of the first audio stream, copied and
// not decoded, into the hash muxer, which prints their SHA-256 on the
// standard output. "-map 0:a:0" is the first audio stream whatever its
// position: the embedded cover is a video stream (T1, T29), and it is not
// part of the fingerprint. Only the "fd" protocol is allowed for the input,
// as in probeArgs.
func fingerprintArgs(demuxer string) []string {
	return []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-protocol_whitelist", "fd",
		"-fd", "3",
		"-f", demuxer,
		"-i", "fd:",
		"-map", "0:a:0",
		"-c", "copy",
		"-f", "hash",
		"-hash", "sha256",
		"-",
	}
}

// hashLine is the whole output of the hash muxer.
var hashLine = regexp.MustCompile(`^SHA256=([0-9a-f]{64})\n$`)

// Fingerprint returns the fingerprint of f, a file of the container c: the
// SHA-256, in lower-case hexadecimal, of the compressed packets of its
// first audio stream (§5.4). The packets do not change when MusicLib
// rewrites the tags or the cover of the file, so the fingerprint follows
// the audio through every change of its metadata. It costs one read of the
// file and no decoding.
//
// version is the version of the ffmpeg that computed it, to be kept with
// it (tracks.fp_version): a fingerprint is compared only with those of the
// same version.
//
// f follows the rules of Probe. A file ffmpeg cannot read, or one without
// an audio stream, fails with CodeToolFailed.
func (t *Tools) Fingerprint(ctx context.Context, f *os.File, c Container) (fingerprint, version string, err error) {
	const op = "ffmpeg"
	demux, err := demuxer(c, op)
	if err != nil {
		return "", "", err
	}
	if err := rewind(f, op); err != nil {
		return "", "", err
	}
	res, err := t.run.Run(ctx, Command{Path: t.ffmpeg, Args: fingerprintArgs(demux), File: f, Timeout: FingerprintTimeout})
	if err != nil {
		return "", "", err
	}
	m := hashLine.FindSubmatch(res.Stdout)
	if m == nil {
		return "", "", &Error{Code: CodeOutputInvalid, Op: op, Msg: "the output is not the line of the hash muxer", Stderr: res.Stderr}
	}
	return string(m[1]), t.version, nil
}

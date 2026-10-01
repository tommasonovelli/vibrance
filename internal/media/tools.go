package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
)

// The fixed places of the tools in the image, and the version both must
// report (DESIGN.md D18, §3.6): FFmpeg 8.1.3 with the --extra-version of
// MusicLib's build, whose static binaries the Dockerfile copies from the
// pinned MusicLib image. They are the tools that verified the audio when
// MusicLib wrote the library.
const (
	FFmpegPath  = "/usr/local/bin/ffmpeg"
	FFprobePath = "/usr/local/bin/ffprobe"

	PinnedVersion = "8.1.3-musiclib1"
)

// Container is the container of a track file. MusicLib writes three, each
// with its own extension: .flac, .mp3 and .m4a (§4.1). The caller names
// the container; the tools are told which demuxer to use and never guess
// it from the content.
type Container string

const (
	ContainerFLAC Container = "flac"
	ContainerMP3  Container = "mp3"
	ContainerM4A  Container = "m4a"
)

// demuxers is the ffmpeg demuxer of each container.
var demuxers = map[Container]string{
	ContainerFLAC: "flac",
	ContainerMP3:  "mp3",
	ContainerM4A:  "mov",
}

// demuxer returns the ffmpeg demuxer of c. A container that MusicLib does
// not write is not supported, like a file that is not audio.
func demuxer(c Container, op string) (string, error) {
	d, ok := demuxers[c]
	if !ok {
		return "", newErr(CodeNotSupported, op, "unknown container "+strconv.Quote(string(c)), nil)
	}
	return d, nil
}

// Tools runs ffprobe and ffmpeg on the files of the library. A tool never
// gets the name of a file: the caller opens the file and the tool reads
// that descriptor, through the "fd" protocol and with every other protocol
// refused. So a file name is never an option or a protocol, and a file
// cannot make the tool open another one (T2).
//
// The package knows the tools and the formats, not the domain: what a
// failure means to an album is the caller's business.
//
// It is safe for concurrent use; every call takes a slot of the Runner.
type Tools struct {
	run     *Runner
	ffmpeg  string
	ffprobe string
	version string
}

// NewTools reads the version of the two tools and refuses any other than
// the pinned one (§11.2 step 4). The error has the code
// CodeToolUnavailable when a tool cannot run or does not print the version
// line of the tool it should be, CodeToolVersion when it is another
// version, and CodeCanceled when ctx ends first.
func NewTools(ctx context.Context, run *Runner, ffmpegPath, ffprobePath string) (*Tools, error) {
	t := &Tools{run: run, ffmpeg: ffmpegPath, ffprobe: ffprobePath}
	for _, tool := range []struct{ name, path string }{{"ffmpeg", ffmpegPath}, {"ffprobe", ffprobePath}} {
		version, err := readVersion(ctx, run, tool.path, tool.name)
		if err != nil {
			return nil, err
		}
		if version != PinnedVersion {
			return nil, newErr(CodeToolVersion, tool.name,
				"version "+strconv.Quote(version)+" is not the pinned "+PinnedVersion, nil)
		}
		t.version = version
	}
	return t, nil
}

// Version is the version that ffmpeg and ffprobe reported to NewTools.
func (t *Tools) Version() string { return t.version }

// versionLine is the first line of `ffmpeg -version` and of
// `ffprobe -version`: "<tool> version <version> Copyright ...".
var versionLine = regexp.MustCompile(`^(ffmpeg|ffprobe) version (\S+)`)

// readVersion runs `<path> -version` and returns the version of its first
// line, which must name the tool.
func readVersion(ctx context.Context, run *Runner, path, tool string) (string, error) {
	res, err := run.Run(ctx, Command{Path: path, Args: []string{"-hide_banner", "-version"}, Timeout: ProbeTimeout})
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code != CodeCanceled {
			// A tool that cannot print its version is as good as absent.
			e.Code = CodeToolUnavailable
		}
		return "", err
	}
	first, _, _ := bytes.Cut(res.Stdout, []byte("\n"))
	m := versionLine.FindSubmatch(first)
	if m == nil || string(m[1]) != tool {
		return "", newErr(CodeToolUnavailable, tool, "it does not print the version line of "+tool, nil)
	}
	return string(m[2]), nil
}

// rewind puts the offset of f, which the tool shares, back to the start.
func rewind(f *os.File, op string) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return newErr(CodeIO, op, "cannot rewind the input", err)
	}
	return nil
}

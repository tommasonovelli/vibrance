package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The pinned tools, as in the product (DESIGN.md §3.6).
const (
	ffmpegPath  = "/usr/local/bin/ffmpeg"
	ffprobePath = "/usr/local/bin/ffprobe"
)

// Limits of one tool run (DESIGN.md I8, T2).
const (
	toolTimeout = 10 * time.Minute
	stderrLimit = 64 << 10
)

// demuxerOf is the ffmpeg demuxer of each audio extension of the library
// (DESIGN.md §4.1). Vibrance classifies by content (S5); for the spike the
// extensions MusicLib writes are enough.
var demuxerOf = map[string]string{".flac": "flac", ".mp3": "mp3", ".m4a": "mov"}

func demuxer(path string) (string, error) {
	d, ok := demuxerOf[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return "", fmt.Errorf("%s: not an audio file of the library", path)
	}
	return d, nil
}

// toolRun is one finished run of ffmpeg or ffprobe.
type toolRun struct {
	// Cmdline is the command as a shell line, with the input file as the
	// redirection 3<'path'.
	Cmdline string
	Stdout  []byte
	Stderr  []byte
	Elapsed time.Duration
}

// limitedBuffer keeps the first max bytes written and discards the rest, so
// a tool never blocks on a full pipe.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// runTool runs tool with args, and with the file at path on descriptor 3
// (never as a path argument: DESIGN.md T2), without a shell, with a timeout
// and a bounded stderr. A non-zero exit is an error that carries stderr.
func runTool(ctx context.Context, tool string, args []string, path string) (toolRun, error) {
	run := toolRun{Cmdline: shellLine(append([]string{filepath.Base(tool)}, args...)) + " 3<" + shellQuote(path)}
	f, err := os.Open(path)
	if err != nil {
		return run, fmt.Errorf("opening %s: %w", path, err)
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.ExtraFiles = []*os.File{f}
	cmd.Env = []string{}
	var stdout bytes.Buffer
	stderr := &limitedBuffer{max: stderrLimit}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	start := time.Now()
	runErr := cmd.Run()
	run.Elapsed = time.Since(start)
	closeErr := f.Close()
	run.Stdout, run.Stderr = stdout.Bytes(), stderr.buf.Bytes()
	if runErr != nil {
		return run, fmt.Errorf("%s: %w: %s", run.Cmdline, runErr, bytes.TrimSpace(run.Stderr))
	}
	if closeErr != nil {
		return run, fmt.Errorf("closing %s: %w", path, closeErr)
	}
	return run, nil
}

// fingerprintArgs is the audio fingerprint of DESIGN.md §5.4: the SHA-256
// of the compressed packets of the first audio stream, with the input on
// descriptor 3.
func fingerprintArgs(demux string) []string {
	return []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-protocol_whitelist", "fd", "-fd", "3", "-f", demux, "-i", "fd:",
		"-map", "0:a:0", "-c", "copy", "-f", "hash", "-hash", "sha256", "-"}
}

var hashLine = regexp.MustCompile(`^SHA256=([0-9a-f]{64})\n$`)

// fingerprint returns the fingerprint of the audio file at path, and the
// run that computed it.
func fingerprint(ctx context.Context, path string) (string, toolRun, error) {
	d, err := demuxer(path)
	if err != nil {
		return "", toolRun{}, err
	}
	return hashRun(ctx, fingerprintArgs(d), path)
}

// hashRun runs an ffmpeg command whose output is the hash muxer's line.
func hashRun(ctx context.Context, args []string, path string) (string, toolRun, error) {
	run, err := runTool(ctx, ffmpegPath, args, path)
	if err != nil {
		return "", run, err
	}
	m := hashLine.FindSubmatch(run.Stdout)
	if m == nil {
		return "", run, fmt.Errorf("%s: unexpected output %q", run.Cmdline, run.Stdout)
	}
	return string(m[1]), run, nil
}

// probeArgs asks ffprobe for everything the spike reads: container, streams
// with their disposition, tags and side data, and the format tags.
func probeArgs(demux string) []string {
	return []string{"-hide_banner", "-loglevel", "error", "-print_format", "json",
		"-show_entries", "format=format_name,duration:format_tags" +
			":stream=index,codec_type,codec_name,sample_rate,channels,bits_per_raw_sample,bit_rate,time_base,duration_ts,duration" +
			":stream_disposition=attached_pic:stream_tags:stream_side_data",
		"-protocol_whitelist", "fd", "-fd", "3", "-f", demux, "fd:"}
}

type probeStream struct {
	Index            int               `json:"index"`
	CodecType        string            `json:"codec_type"`
	CodecName        string            `json:"codec_name"`
	SampleRate       string            `json:"sample_rate"`
	Channels         int               `json:"channels"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	BitRate          string            `json:"bit_rate"`
	TimeBase         string            `json:"time_base"`
	DurationTS       *int64            `json:"duration_ts"`
	Duration         string            `json:"duration"`
	Disposition      map[string]int    `json:"disposition"`
	Tags             map[string]string `json:"tags"`
	SideData         []map[string]any  `json:"side_data_list"`
}

type probeResult struct {
	Streams []probeStream `json:"streams"`
	Format  struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

// audio returns the audio stream chosen as Vibrance does, by codec_type
// (DESIGN.md T1), and how many audio streams there are.
func (p probeResult) audio() (probeStream, int) {
	var first probeStream
	n := 0
	for _, s := range p.Streams {
		if s.CodecType == "audio" {
			if n == 0 {
				first = s
			}
			n++
		}
	}
	return first, n
}

func probe(ctx context.Context, path string) (probeResult, toolRun, error) {
	d, err := demuxer(path)
	if err != nil {
		return probeResult{}, toolRun{}, err
	}
	run, err := runTool(ctx, ffprobePath, probeArgs(d), path)
	if err != nil {
		return probeResult{}, run, err
	}
	var p probeResult
	if err := json.Unmarshal(run.Stdout, &p); err != nil {
		return probeResult{}, run, fmt.Errorf("%s: the JSON output does not parse: %w", run.Cmdline, err)
	}
	return p, run, nil
}

// durationMS is DESIGN.md §4.6: duration_ts × time_base of the audio
// stream, in milliseconds, rounded down; ok is false when the stream
// declares no duration (NULL, never 0).
func durationMS(s probeStream) (ms int64, ok bool) {
	num, den, found := strings.Cut(s.TimeBase, "/")
	if !found || s.DurationTS == nil || *s.DurationTS <= 0 {
		return 0, false
	}
	n, err1 := strconv.ParseInt(num, 10, 64)
	d, err2 := strconv.ParseInt(den, 10, 64)
	if err1 != nil || err2 != nil || n <= 0 || d <= 0 {
		return 0, false
	}
	v := new(big.Int).Mul(big.NewInt(*s.DurationTS), big.NewInt(n*1000))
	v.Quo(v, big.NewInt(d))
	if !v.IsInt64() {
		return 0, false
	}
	return v.Int64(), true
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=,+@%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellLine(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

// errNotFound marks a lookup that found nothing.
var errNotFound = errors.New("not found")

// execOutput runs a tool without input files and returns its stdout.
func execOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{}
	var stdout bytes.Buffer
	stderr := &limitedBuffer{max: stderrLimit}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", shellLine(append([]string{name}, args...)), err, bytes.TrimSpace(stderr.buf.Bytes()))
	}
	return stdout.String(), nil
}

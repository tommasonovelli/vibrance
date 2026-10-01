package media

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"strconv"
	"strings"
)

// The codecs of the tracks of a library (tracks.codec, DESIGN.md §5.2), as
// ffprobe names them.
const (
	CodecFLAC = "flac"
	CodecMP3  = "mp3"
	CodecAAC  = "aac"
	CodecALAC = "alac"
)

// Info is what ffprobe reports of a track file: its audio stream, and the
// tags of the file.
type Info struct {
	// Codec is CodecFLAC, CodecMP3, CodecAAC or CodecALAC.
	Codec      string
	SampleRate int
	Channels   int
	// BitDepth is the number of bits of a sample; 0 for a codec that has
	// none (MP3, AAC).
	BitDepth int
	// Bitrate is in bits per second; 0 when the stream declares none
	// (FLAC).
	Bitrate int
	// DurationMS is the duration of the audio stream in milliseconds,
	// rounded down; nil when the container declares none (§4.6: unknown,
	// never 0).
	DurationMS *int64
	// Tags are the format tags of tagKeys, with the keys as ffprobe prints
	// them: the input of MapTags.
	Tags map[string]string
}

// probeArgs is the ffprobe command line for the file on descriptor 3.
//
// Only the "fd" protocol is allowed, so the demuxer can open nothing else,
// and the demuxer is the one of the container, not a guess. Only the tags
// of tagKeys are printed (ffprobe matches their names whatever the case):
// a file may carry tags of any size that Vibrance does not read, like
// embedded lyrics. -show_error makes ffprobe say, in its output, that it
// could not read the file.
func probeArgs(demuxer string) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "error",
		"-show_error",
		"-print_format", "json",
		"-show_entries", "stream=codec_type,codec_name,sample_rate,channels,bits_per_raw_sample,bit_rate,time_base,duration_ts" +
			":format_tags=" + strings.Join(tagKeys, ","),
		"-protocol_whitelist", "fd",
		"-fd", "3",
		"-f", demuxer,
		"fd:",
	}
}

// Probe reads the audio stream and the tags of f, a file of the container
// c, with ffprobe. f must be open for reading; its offset is set to 0 and
// is unspecified afterwards, and f must not be used by anything else while
// Probe runs.
//
// A file that is not audio Vibrance reads fails with CodeNotSupported: the
// demuxer refuses it, it has no audio stream, or the codec is not one of
// the container (flac in FLAC, mp3 in MP3, aac or alac in M4A). The other
// codes are failures of the run (Runner.Run) or of the output
// (CodeOutputInvalid).
func (t *Tools) Probe(ctx context.Context, f *os.File, c Container) (Info, error) {
	const op = "ffprobe"
	demux, err := demuxer(c, op)
	if err != nil {
		return Info{}, err
	}
	if err := rewind(f, op); err != nil {
		return Info{}, err
	}
	res, err := t.run.Run(ctx, Command{Path: t.ffprobe, Args: probeArgs(demux), File: f, Timeout: ProbeTimeout})
	if err != nil {
		return Info{}, refused(err, res.Stdout)
	}
	return parseProbe(res.Stdout, c)
}

// probeOutput is the part of ffprobe's JSON that the adapter reads.
type probeOutput struct {
	Streams []probeStream `json:"streams"`
	Format  struct {
		Tags map[string]string `json:"tags"`
	} `json:"format"`
	Error *struct {
		Code   int    `json:"code"`
		String string `json:"string"`
	} `json:"error"`
}

type probeStream struct {
	CodecType        string `json:"codec_type"`
	CodecName        string `json:"codec_name"`
	SampleRate       string `json:"sample_rate"`
	Channels         int    `json:"channels"`
	BitsPerRawSample string `json:"bits_per_raw_sample"`
	BitRate          string `json:"bit_rate"`
	TimeBase         string `json:"time_base"`
	DurationTS       *int64 `json:"duration_ts"`
}

// FFmpeg's error codes (AVERROR(errno) is -errno) that say that the
// machine failed, not the file.
const (
	averrorEIO    = -5
	averrorENOMEM = -12
)

// refused turns the failure of an ffprobe run into CodeNotSupported when
// ffprobe ran and reported, in its output, that it cannot read the file.
// Every other failure stays what it is: a tool that was killed, an option
// it does not know, a disk that cannot be read.
func refused(runErr error, stdout []byte) error {
	var e *Error
	if !errors.As(runErr, &e) || e.Code != CodeToolFailed {
		return runErr
	}
	var out probeOutput
	if json.Unmarshal(stdout, &out) != nil || out.Error == nil ||
		out.Error.Code == averrorEIO || out.Error.Code == averrorENOMEM {
		return runErr
	}
	return &Error{Code: CodeNotSupported, Op: e.Op, Stderr: e.Stderr,
		Msg: "the demuxer cannot read the file: " + out.Error.String + " (" + strconv.Itoa(out.Error.Code) + ")"}
}

// parseProbe reads the report of a successful ffprobe run on a file of the
// container c. It does no I/O.
func parseProbe(stdout []byte, c Container) (Info, error) {
	const op = "ffprobe"
	var out probeOutput
	if err := json.Unmarshal(stdout, &out); err != nil {
		return Info{}, newErr(CodeOutputInvalid, op, "the output is not the JSON report", err)
	}
	// The audio stream is found by its type, never by its position: the
	// embedded cover is a video stream, and nothing says it comes after
	// the audio (T1).
	var audio *probeStream
	for i := range out.Streams {
		if out.Streams[i].CodecType == "audio" {
			audio = &out.Streams[i]
			break
		}
	}
	if audio == nil {
		return Info{}, newErr(CodeNotSupported, op, "the file has no audio stream", nil)
	}
	if !codecOf(c, audio.CodecName) {
		return Info{}, newErr(CodeNotSupported, op,
			"codec "+strconv.Quote(audio.CodecName)+" in a "+string(c)+" file", nil)
	}
	rate := positive(audio.SampleRate)
	if rate == 0 || audio.Channels <= 0 {
		return Info{}, newErr(CodeNotSupported, op,
			"sample rate "+strconv.Quote(audio.SampleRate)+", "+strconv.Itoa(audio.Channels)+" channels", nil)
	}
	return Info{
		Codec:      audio.CodecName,
		SampleRate: rate,
		Channels:   audio.Channels,
		BitDepth:   positive(audio.BitsPerRawSample),
		Bitrate:    positive(audio.BitRate),
		DurationMS: durationMS(*audio),
		Tags:       out.Format.Tags,
	}, nil
}

// codecOf reports whether codec is a codec of the container c.
func codecOf(c Container, codec string) bool {
	switch c {
	case ContainerFLAC:
		return codec == CodecFLAC
	case ContainerMP3:
		return codec == CodecMP3
	case ContainerM4A:
		return codec == CodecAAC || codec == CodecALAC
	}
	return false
}

// positive reads a decimal number greater than 0, as ffprobe prints the
// numbers it gives as strings. Anything else, an absent value too, is 0.
func positive(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// durationMS is the duration of §4.6: duration_ts × time_base of the audio
// stream, in milliseconds, rounded down. It is nil when the stream declares
// no duration: unknown, which is not 0.
func durationMS(s probeStream) *int64 {
	num, den, ok := timeBase(s.TimeBase)
	if s.DurationTS == nil || *s.DurationTS <= 0 || !ok {
		return nil
	}
	ms := new(big.Int).Mul(big.NewInt(*s.DurationTS), big.NewInt(num))
	ms.Mul(ms, big.NewInt(1000))
	ms.Quo(ms, big.NewInt(den))
	if !ms.IsInt64() {
		return nil
	}
	v := ms.Int64()
	return &v
}

// timeBase reads ffprobe's "num/den".
func timeBase(s string) (num, den int64, ok bool) {
	a, b, found := strings.Cut(s, "/")
	if !found {
		return 0, 0, false
	}
	num, err1 := strconv.ParseInt(a, 10, 64)
	den, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || num <= 0 || den <= 0 {
		return 0, 0, false
	}
	return num, den, true
}

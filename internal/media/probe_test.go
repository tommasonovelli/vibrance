package media

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The command line of the probe (DESIGN.md T2): the file is descriptor 3,
// read through the "fd" protocol and no other, with the demuxer named. The
// only thing that varies is the demuxer: nothing of a file, least of all
// its name, is ever an argument.
func TestProbeArgs(t *testing.T) {
	want := []string{
		"-hide_banner", "-loglevel", "error", "-show_error", "-print_format", "json",
		"-show_entries", "stream=codec_type,codec_name,sample_rate,channels,bits_per_raw_sample,bit_rate,time_base,duration_ts" +
			":format_tags=title,artist,album_artist,album,track,disc,date,genre,compilation," +
			"replaygain_track_gain,replaygain_track_peak,replaygain_album_gain,replaygain_album_peak",
		"-protocol_whitelist", "fd", "-fd", "3", "-f", "mov", "fd:",
	}
	if got := probeArgs("mov"); !slices.Equal(got, want) {
		t.Fatalf("probeArgs:\n got %q\nwant %q", got, want)
	}
	for c, demux := range map[Container]string{ContainerFLAC: "flac", ContainerMP3: "mp3", ContainerM4A: "mov"} {
		if got, err := demuxer(c, "ffprobe"); err != nil || got != demux {
			t.Fatalf("the demuxer of %s is %q (%v), want %q", c, got, err, demux)
		}
	}
}

// Every track of the fixture library: the codec, the parameters of the
// audio stream, the duration and the tags are those MusicLib wrote
// (§4.3, §4.6). Album A has the cover embedded in each track: its stream is
// not the audio (T1, T29).
func TestProbeFixtureLibrary(t *testing.T) {
	tools := newTools(t)
	for _, tr := range fixtureTracks {
		t.Run(tr.path, func(t *testing.T) {
			info, err := tools.Probe(t.Context(), open(t, inFixture(tr.path)), containerOf(t, tr.path))
			if err != nil {
				t.Fatal(err)
			}
			wantInfo(t, info, tr.info)
			wantTags(t, MapTags(info.Tags), tr.tags)
			// Only the tags that are read were asked for: not the totals,
			// not the encoder, not the brands of an M4A.
			for key := range info.Tags {
				if !slices.Contains(tagKeys, asciiLower(key)) {
					t.Errorf("ffprobe reported the tag %q, which Vibrance does not read", key)
				}
			}
		})
	}
}

// retagged makes a copy of a fixture track with another title and the
// compilation flag: the same audio packets in a different file, as after
// an edit in MusicLib (§12.2).
func retagged(t *testing.T, rel string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "retagged"+filepath.Ext(rel))
	ffmpegMake(t, "-i", inFixture(rel), "-map", "0", "-c", "copy",
		"-metadata", "title=Changed: a new title", "-metadata", "compilation=1", out)
	return out
}

// The compilation flag and a changed title, read from real files of the
// four codecs; everything else is what the fixture track has.
func TestProbeRetaggedFiles(t *testing.T) {
	tools := newTools(t)
	for _, rel := range []string{fixtureFLAC, fixtureMP3, fixtureAAC, fixtureALAC} {
		t.Run(rel, func(t *testing.T) {
			tr := track(t, rel)
			info, err := tools.Probe(t.Context(), open(t, retagged(t, rel)), containerOf(t, rel))
			if err != nil {
				t.Fatal(err)
			}
			wantInfo(t, info, tr.info)
			want := tr.tags
			want.Title, want.Compilation = "Changed: a new title", true
			if containerOf(t, rel) == ContainerM4A {
				// ffmpeg's M4A muxer, which made the copy, drops the
				// ReplayGain atoms (NOTES.md N-011). MusicLib keeps them.
				want.TrackGain, want.TrackPeak, want.AlbumGain, want.AlbumPeak = nil, nil, nil, nil
			}
			wantTags(t, MapTags(info.Tags), want)
		})
	}
}

// videoFirst makes an M4A whose first stream is a video stream and whose
// second is the audio of a fixture track: the audio stream is not always
// streams[0] (T1).
func videoFirst(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "video-first.m4a")
	ffmpegMake(t, "-f", "lavfi", "-i", "color=c=red:s=16x16:d=1", "-i", inFixture(fixtureALAC),
		"-map", "0:v", "-map", "1:a", "-c:v", "mjpeg", "-c:a", "copy", "-f", "mp4", out)
	if got := streamTypes(t, out); !slices.Equal(got, []string{"video", "audio"}) {
		t.Fatalf("the streams of the test file are %q, want video first", got)
	}
	return out
}

func TestProbePicksTheAudioStreamByType(t *testing.T) {
	info, err := newTools(t).Probe(t.Context(), open(t, videoFirst(t)), ContainerM4A)
	if err != nil {
		t.Fatal(err)
	}
	wantInfo(t, info, track(t, fixtureALAC).info)
}

// A FLAC written to a pipe has no total of samples in its header: the
// container declares no duration, which is unknown and not zero (§4.6).
func TestProbeDurationNotDeclared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "streamed.flac")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, FFmpegPath, "-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "flac", "-f", "flac", "pipe:1")
	cmd.Stdout = out
	if err := errors.Join(cmd.Run(), out.Close()); err != nil {
		t.Fatal(err)
	}

	info, err := newTools(t).Probe(t.Context(), open(t, path), ContainerFLAC)
	if err != nil {
		t.Fatal(err)
	}
	wantInfo(t, info, Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 1, BitDepth: 16})
	if info.DurationMS != nil {
		t.Fatalf("duration %d, want none", *info.DurationMS)
	}
	wantTags(t, MapTags(info.Tags), Tags{})
}

// Files that are not audio Vibrance reads fail with a typed error, whatever
// the container they are said to be.
func TestProbeNotAudio(t *testing.T) {
	tools := newTools(t)
	dir := t.TempDir()
	empty := writeFile(t, filepath.Join(dir, "empty"), nil, 0o644)
	noise := writeFile(t, filepath.Join(dir, "noise"), []byte(strings.Repeat("not audio at all\n", 4096)), 0o644)
	truncated := filepath.Join(dir, "truncated.flac")
	whole, err := os.ReadFile(inFixture(fixtureFLAC))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, truncated, whole[:3000], 0o644)
	mp3InM4A := filepath.Join(dir, "mp3.m4a")
	ffmpegMake(t, "-i", inFixture(fixtureMP3), "-c", "copy", "-f", "mp4", mp3InM4A)
	const album = "Aurora Sines/Alpha_ Light_/"

	for _, tc := range []struct {
		name string
		path string
		c    Container
	}{
		{"an empty file as FLAC", empty, ContainerFLAC},
		{"an empty file as MP3", empty, ContainerMP3},
		{"an empty file as M4A", empty, ContainerM4A},
		{"text as FLAC", noise, ContainerFLAC},
		{"text as MP3", noise, ContainerMP3},
		{"text as M4A", noise, ContainerM4A},
		{"a cover as FLAC", inFixture(album + "cover.jpg"), ContainerFLAC},
		{"a cover as MP3", inFixture(album + "cover.jpg"), ContainerMP3},
		{"a cover as M4A", inFixture(album + "cover.jpg"), ContainerM4A},
		{"lyrics as MP3", inFixture(album + "01 - First Light.lrc"), ContainerMP3},
		{"a receipt as M4A", inFixture(album + ".musiclib.json"), ContainerM4A},
		{"a FLAC as MP3", inFixture(fixtureFLAC), ContainerMP3},
		{"a FLAC as M4A", inFixture(fixtureFLAC), ContainerM4A},
		{"an MP3 as FLAC", inFixture(fixtureMP3), ContainerFLAC},
		{"an MP3 as M4A", inFixture(fixtureMP3), ContainerM4A},
		{"an M4A as FLAC", inFixture(fixtureAAC), ContainerFLAC},
		{"an M4A as MP3", inFixture(fixtureAAC), ContainerMP3},
		{"a FLAC cut short", truncated, ContainerFLAC},
		{"MP3 audio in an M4A", mp3InM4A, ContainerM4A},
		{"a container MusicLib does not write", inFixture(fixtureFLAC), Container("ogg")},
		{"no container", inFixture(fixtureFLAC), Container("")},
		{"the demuxer instead of the container", inFixture(fixtureAAC), Container("mov")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := tools.Probe(t.Context(), open(t, tc.path), tc.c)
			wantCode(t, err, CodeNotSupported)
			if !reflect.DeepEqual(info, Info{}) {
				t.Fatalf("a refused file has the info %s", showInfo(info))
			}
		})
	}
}

// The name of a file never reaches the tools: files whose names would be
// an option, a protocol or a shell command read exactly like the same file
// under a plain name (T2).
func TestHostileFileNames(t *testing.T) {
	tools := newTools(t)
	tr := track(t, fixtureFLAC)
	audio, err := os.ReadFile(inFixture(tr.path))
	if err != nil {
		t.Fatal(err)
	}
	// The files are opened by their bare names, from the folder they are
	// in: the name of the open file is then the hostile name itself, with
	// no folder before it to make it harmless.
	dir := t.TempDir()
	t.Chdir(dir)
	for i, name := range []string{
		"-version",
		"-i",
		"-h.flac",
		"-f lavfi -i anullsrc.flac",
		"-y -f null out.flac",
		"file:other.flac",
		"concat:a.flac|b.flac",
		"pipe:0",
		"fd:",
		"http:host.flac",
		"subfile,,start,0,end,1,,:x.flac",
		"a\nb.flac",
		"$(touch pwned).flac",
		"; touch pwned ;.flac",
		"`touch pwned`.flac",
		" leading and trailing ",
		"%d-%03d.flac",
		"*.flac",
		"quote'\".flac",
	} {
		t.Run(name, func(t *testing.T) {
			f := open(t, writeFile(t, name, audio, 0o644))
			if f.Name() != name {
				t.Fatalf("the file is open as %q, want %q", f.Name(), name)
			}
			info, err := tools.Probe(t.Context(), f, ContainerFLAC)
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			wantInfo(t, info, tr.info)
			wantTags(t, MapTags(info.Tags), tr.tags)
			fp, _, err := tools.Fingerprint(t.Context(), f, ContainerFLAC)
			if err != nil || fp != tr.fingerprint {
				t.Fatalf("Fingerprint: %q, %v; want %q", fp, err, tr.fingerprint)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			// The tools run in "/" and are given no path: nothing but
			// the files of the test can be here.
			if len(entries) != i+1 {
				t.Fatalf("the folder holds %d entries, want the %d files of the test: a tool wrote a file", len(entries), i+1)
			}
		})
	}
}

// The tools share the offset of the file they are given. Probe and
// Fingerprint set it themselves, so one open file serves any number of
// calls, wherever a previous reader left it.
func TestOneOpenFileForSeveralCalls(t *testing.T) {
	tools := newTools(t)
	for _, rel := range []string{fixtureFLAC, fixtureMP3, fixtureAAC, fixtureALAC} {
		tr := track(t, rel)
		f := open(t, inFixture(rel))
		if _, err := io.Copy(io.Discard, f); err != nil { // the offset is at the end
			t.Fatal(err)
		}
		for range 3 {
			info, err := tools.Probe(t.Context(), f, containerOf(t, rel))
			if err != nil {
				t.Fatal(err)
			}
			wantInfo(t, info, tr.info)
			fp, _, err := tools.Fingerprint(t.Context(), f, containerOf(t, rel))
			if err != nil || fp != tr.fingerprint {
				t.Fatalf("%s: fingerprint %q, %v", rel, fp, err)
			}
		}
	}
}

// Many callers at once, on a Runner with fewer slots: every answer is the
// right one.
func TestProbeAndFingerprintConcurrently(t *testing.T) {
	tools, err := NewTools(t.Context(), NewRunner(3), FFmpegPath, FFprobePath)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 3 {
		for _, tr := range fixtureTracks {
			wg.Go(func() {
				f, err := os.Open(inFixture(tr.path))
				if err != nil {
					t.Error(err)
					return
				}
				defer func() {
					if err := f.Close(); err != nil {
						t.Error(err)
					}
				}()
				info, err := tools.Probe(t.Context(), f, containerOf(t, tr.path))
				if err != nil {
					t.Errorf("%s: %v", tr.path, err)
					return
				}
				info.Tags = nil
				if !reflect.DeepEqual(info, tr.info) {
					t.Errorf("%s: info %s, want %s", tr.path, showInfo(info), showInfo(tr.info))
				}
				fp, _, err := tools.Fingerprint(t.Context(), f, containerOf(t, tr.path))
				if err != nil || fp != tr.fingerprint {
					t.Errorf("%s: fingerprint %q, %v", tr.path, fp, err)
				}
			})
		}
	}
	wg.Wait()
}

// A probe whose context ends is a cancellation, not a file that cannot be
// read.
func TestProbeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	tools := newTools(t)
	cancel()
	_, err := tools.Probe(ctx, open(t, inFixture(fixtureFLAC)), ContainerFLAC)
	wantCode(t, err, CodeCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the error does not wrap context.Canceled: %v", err)
	}
	_, _, err = tools.Fingerprint(ctx, open(t, inFixture(fixtureFLAC)), ContainerFLAC)
	wantCode(t, err, CodeCanceled)
}

// A file that cannot be rewound is a failure of the adapter, before any
// tool runs.
func TestProbeFileThatCannotSeek(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := errors.Join(r.Close(), w.Close()); err != nil {
			t.Error(err)
		}
	}()
	tools := newTools(t)
	_, err = tools.Probe(t.Context(), r, ContainerFLAC)
	wantCode(t, err, CodeIO)
	_, _, err = tools.Fingerprint(t.Context(), r, ContainerFLAC)
	wantCode(t, err, CodeIO)
}

// stream is the JSON of an audio stream as ffprobe prints it.
const (
	flacStream = `{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/44100","duration_ts":88200,"bits_per_raw_sample":"16"}`
	coverPic   = `{"codec_name":"mjpeg","codec_type":"video","time_base":"1/90000","duration_ts":180000,"bits_per_raw_sample":"8"}`
)

// The report of ffprobe, read without running it: the choice of the
// stream, the numbers, and the duration of §4.6.
func TestParseProbe(t *testing.T) {
	flac := Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)}
	for _, tc := range []struct {
		name string
		c    Container
		json string
		want Info
	}{
		{"audio then cover", ContainerFLAC, `{"streams":[` + flacStream + `,` + coverPic + `],"format":{"tags":{"TITLE":"x"}}}`, flac},
		{"cover then audio", ContainerFLAC, `{"streams":[` + coverPic + `,` + flacStream + `]}`, flac},
		{"two covers then audio", ContainerFLAC, `{"streams":[` + coverPic + `,` + coverPic + `,` + flacStream + `]}`, flac},
		{"the first of two audio streams", ContainerM4A, `{"streams":[` + coverPic + `,
			{"codec_name":"alac","codec_type":"audio","sample_rate":"96000","channels":6,"time_base":"1/96000","duration_ts":96000,"bit_rate":"900000","bits_per_raw_sample":"24"},
			{"codec_name":"aac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/44100","duration_ts":44100}]}`,
			Info{Codec: CodecALAC, SampleRate: 96000, Channels: 6, BitDepth: 24, Bitrate: 900000, DurationMS: ms(1000)}},
		{"a data stream before the audio", ContainerM4A, `{"streams":[{"codec_type":"data"},{"codec_type":"subtitle","codec_name":"mov_text"},
			{"codec_name":"aac","codec_type":"audio","sample_rate":"48000","channels":1,"time_base":"1/48000","duration_ts":48000,"bit_rate":"64000"}]}`,
			Info{Codec: CodecAAC, SampleRate: 48000, Channels: 1, Bitrate: 64000, DurationMS: ms(1000)}},
		{"MP3: its time base, no bit depth", ContainerMP3,
			`{"streams":[{"codec_name":"mp3","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/14112000","duration_ts":28224000,"bit_rate":"128000"}]}`,
			Info{Codec: CodecMP3, SampleRate: 44100, Channels: 2, Bitrate: 128000, DurationMS: ms(2000)}},
		{"the duration is rounded down", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/44100","duration_ts":88199}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, DurationMS: ms(1999)}},
		{"a duration below one millisecond is 0, not unknown", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/44100","duration_ts":44}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, DurationMS: ms(0)}},
		{"a time base with a numerator", ContainerMP3,
			`{"streams":[{"codec_name":"mp3","codec_type":"audio","sample_rate":"48000","channels":1,"time_base":"3/1000","duration_ts":1001}]}`,
			Info{Codec: CodecMP3, SampleRate: 48000, Channels: 1, DurationMS: ms(3003)}},
		{"a product beyond 64 bits that still fits in milliseconds", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1000000/1000000000","duration_ts":9223372036854775}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, DurationMS: ms(9223372036854775)}},
		{"no duration_ts", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/44100","bits_per_raw_sample":"16"}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16}},
		{"duration_ts 0", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/44100","duration_ts":0}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2}},
		{"duration_ts negative", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/44100","duration_ts":-9223372036854775808}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2}},
		{"no time base", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"duration_ts":88200}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2}},
		{"a time base that is not one", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"0/1","duration_ts":88200}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2}},
		{"a time base with a zero denominator", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"1/0","duration_ts":88200}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2}},
		{"a duration that does not fit in 64 bits", ContainerFLAC,
			`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"9223372036854775807/1","duration_ts":9223372036854775807}]}`,
			Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2}},
		{"numbers that are not numbers", ContainerM4A,
			`{"streams":[{"codec_name":"aac","codec_type":"audio","sample_rate":"44100","channels":2,"time_base":"x/y","duration_ts":1,"bit_rate":"N/A","bits_per_raw_sample":"-16"}]}`,
			Info{Codec: CodecAAC, SampleRate: 44100, Channels: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProbe([]byte(tc.json), tc.c)
			if err != nil {
				t.Fatal(err)
			}
			wantInfo(t, got, tc.want)
		})
	}

	for _, tc := range []struct {
		name string
		c    Container
		json string
		code string
	}{
		{"no streams", ContainerFLAC, `{"streams":[],"format":{}}`, CodeNotSupported},
		{"no stream section", ContainerFLAC, `{"format":{"tags":{"title":"x"}}}`, CodeNotSupported},
		{"only a cover", ContainerFLAC, `{"streams":[` + coverPic + `]}`, CodeNotSupported},
		{"a codec type in another case", ContainerFLAC, `{"streams":[{"codec_name":"flac","codec_type":"Audio","sample_rate":"44100","channels":2}]}`, CodeNotSupported},
		{"FLAC audio in an M4A", ContainerM4A, `{"streams":[` + flacStream + `]}`, CodeNotSupported},
		{"AAC audio in a FLAC", ContainerFLAC, `{"streams":[{"codec_name":"aac","codec_type":"audio","sample_rate":"44100","channels":2}]}`, CodeNotSupported},
		{"ALAC audio in an MP3", ContainerMP3, `{"streams":[{"codec_name":"alac","codec_type":"audio","sample_rate":"44100","channels":2}]}`, CodeNotSupported},
		{"Opus in an M4A", ContainerM4A, `{"streams":[{"codec_name":"opus","codec_type":"audio","sample_rate":"48000","channels":2}]}`, CodeNotSupported},
		{"no codec name", ContainerMP3, `{"streams":[{"codec_type":"audio","sample_rate":"44100","channels":2}]}`, CodeNotSupported},
		{"the first audio stream is not the supported one", ContainerM4A, `{"streams":[
			{"codec_name":"ac3","codec_type":"audio","sample_rate":"48000","channels":6},
			{"codec_name":"aac","codec_type":"audio","sample_rate":"44100","channels":2}]}`, CodeNotSupported},
		{"sample rate 0", ContainerFLAC, `{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"0","channels":0,"time_base":"1/90000"}]}`, CodeNotSupported},
		{"no sample rate", ContainerFLAC, `{"streams":[{"codec_name":"flac","codec_type":"audio","channels":2}]}`, CodeNotSupported},
		{"a negative sample rate", ContainerFLAC, `{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"-44100","channels":2}]}`, CodeNotSupported},
		{"no channels", ContainerFLAC, `{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":0}]}`, CodeNotSupported},
		{"negative channels", ContainerFLAC, `{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":-2}]}`, CodeNotSupported},
		{"an unknown container", Container("wav"), `{"streams":[` + flacStream + `]}`, CodeNotSupported},
		{"empty output", ContainerFLAC, ``, CodeOutputInvalid},
		{"not JSON", ContainerFLAC, `streams: none`, CodeOutputInvalid},
		{"truncated JSON", ContainerFLAC, `{"streams":[` + flacStream, CodeOutputInvalid},
		{"channels that are not a number", ContainerFLAC, `{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"44100","channels":"2"}]}`, CodeOutputInvalid},
		{"a tag that is not a string", ContainerFLAC, `{"streams":[` + flacStream + `],"format":{"tags":{"title":1}}}`, CodeOutputInvalid},
		{"streams that are not a list", ContainerFLAC, `{"streams":{}}`, CodeOutputInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProbe([]byte(tc.json), tc.c)
			wantCode(t, err, tc.code)
			if !reflect.DeepEqual(got, Info{}) {
				t.Fatalf("a refused report has the info %s", showInfo(got))
			}
		})
	}
}

// The tags of the report are passed on as they are, for MapTags.
func TestParseProbeTags(t *testing.T) {
	got, err := parseProbe([]byte(`{"streams":[`+flacStream+`],"format":{"tags":{"TITLE":"So What","track":"1","DATE":"1959-08-17"}}}`), ContainerFLAC)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"TITLE": "So What", "track": "1", "DATE": "1959-08-17"}
	if !reflect.DeepEqual(got.Tags, want) {
		t.Fatalf("tags %q, want %q", got.Tags, want)
	}
	wantTags(t, MapTags(got.Tags), Tags{Title: "So What", Track: 1, Year: 1959})
}

// ffprobe says in its output that it cannot read a file. Only that turns
// the failure of its run into "not supported": a tool that was killed or
// that failed for another reason did not judge the file, and an I/O error
// is the machine's.
func TestRefused(t *testing.T) {
	failed := func() error {
		return &Error{Code: CodeToolFailed, Op: "ffprobe", Msg: "exit status 1", Stderr: []byte("x")}
	}
	const invalid = `{"error":{"code":-1094995529,"string":"Invalid data found when processing input"}}`

	err := refused(failed(), []byte(invalid))
	e := wantCode(t, err, CodeNotSupported)
	if !strings.Contains(e.Msg, "Invalid data found when processing input (-1094995529)") || string(e.Stderr) != "x" {
		t.Fatalf("the error does not carry ffprobe's reason and its stderr: %v, %q", err, e.Stderr)
	}
	wantCode(t, refused(failed(), []byte(`{"error":{"code":-541478725,"string":"End of file"}}`)), CodeNotSupported)

	for name, stdout := range map[string]string{
		"an I/O error":        `{"error":{"code":-5,"string":"Input/output error"}}`,
		"out of memory":       `{"error":{"code":-12,"string":"Cannot allocate memory"}}`,
		"no error object":     `{"streams":[]}`,
		"a null error object": `{"error":null}`,
		"no output":           ``,
		"not JSON":            `Unrecognized option 'x'.`,
	} {
		t.Run(name, func(t *testing.T) {
			wantCode(t, refused(failed(), []byte(stdout)), CodeToolFailed)
		})
	}
	for _, code := range []string{CodeTimeout, CodeCanceled, CodeToolUnavailable, CodeOutputTooLarge, CodeIO} {
		wantCode(t, refused(&Error{Code: code, Op: "ffprobe"}, []byte(invalid)), code)
	}
	plain := errors.New("not of this package")
	if got := refused(plain, []byte(invalid)); got != plain {
		t.Fatalf("refused changed an error that is not its own: %v", got)
	}
}

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// runAnalyze reads the library as MusicLib published it after the import:
// H4 (streams of every file), H6 (durations), H9 (ReplayGain) and H8
// (timings, also on a synthetic 50 MB FLAC).
func runAnalyze(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	var h4, h6, h9 fragment
	h4.b.WriteString("## H4: the video streams are the cover; the audio stream is found by codec_type\n\n")
	h4.para("Claim (`DESIGN.md` §4.3, T1, T29): the only video stream a track can have is the embedded cover (an attached picture), and the audio stream is recognized by `codec_type`, not by position. Every file of the library after the import (only album A has a cover at this point; H4 per state, further down, covers A–D with JPEG and PNG covers):")
	h6.b.WriteString("## H6: the duration\n\n")
	h6.para("Claim (`DESIGN.md` §4.6): the duration is `duration_ts × time_base` of the audio stream in milliseconds, rounded down, and it is declared for the four codecs. The value is compared with the `duration_ms` that MusicLib's API reports for the same track (MusicLib uses the same rule).")
	h9.b.WriteString("## H9: ReplayGain survives the render\n\n")
	h9.para("Claim (`DESIGN.md` §4.3): tags MusicLib does not manage, ReplayGain among them, are kept by the render. The inputs of albums A–D carry ReplayGain (Vibrance reads it into `rg_*`); the tables compare what ffprobe reports for the input file and for the published file.")

	streamsOK, durOK, rgOK := true, true, true
	var h4rows, h6rows [][]string
	var samples []string // one file per codec, for H8
	seenCodec := map[string]bool{}
	for _, spec := range albumSpecs {
		a, err := c.albumByTitle(ctx, all, spec.Title)
		if err != nil {
			return err
		}
		p, err := c.waitPublished(ctx, a.ID, "")
		if err != nil {
			return err
		}
		s, err := capture(ctx, "after import", p)
		if err != nil {
			return err
		}
		for i, t := range s.Tracks {
			file := filepath.Join(libraryRoot, filepath.FromSlash(s.Dir.Rel), filepath.FromSlash(t.Rel))
			if t.coverBad {
				streamsOK = false
			}
			h4rows = append(h4rows, []string{spec.Key, t.Rel, t.Streams, t.CoverOK})

			pr, run, err := probe(ctx, file)
			if err != nil {
				return err
			}
			st, _ := pr.audio()
			ms, ok := durationMS(st)
			api := p.Album.Tracks[i].DurationMS
			res := "equal"
			switch {
			case !ok:
				durOK, res = false, "no duration declared"
			case api == nil:
				durOK, res = false, "MusicLib reports none"
			case *api != ms:
				durOK, res = false, fmt.Sprintf("MusicLib says %d", *api)
			}
			ts := "-"
			if st.DurationTS != nil {
				ts = fmt.Sprint(*st.DurationTS)
			}
			h6rows = append(h6rows, []string{spec.Key, t.Rel, st.CodecName, st.TimeBase, ts, fmt.Sprint(ms), apiMS(api), res})
			if !seenCodec[st.CodecName] {
				seenCodec[st.CodecName] = true
				samples = append(samples, file)
				h6.para("The probe of the first %s file:", st.CodecName)
				h6.command(run.Cmdline, string(run.Stdout))
			}
			if spec.RG {
				ok, err := compareReplayGain(ctx, &h9, spec, t, file)
				if err != nil {
					return err
				}
				rgOK = rgOK && ok
			}
		}
	}
	h4.table([]string{"album", "file", "streams (ffprobe)", "video streams vs. the cover file"}, h4rows)
	h6.table([]string{"album", "file", "codec", "time_base", "duration_ts", "computed ms", "MusicLib duration_ms", "result"}, h6rows)
	if err := verdict("H4.1 streams", streamsOK,
		"every file has exactly one audio stream; a video stream exists only with a cover, is an attached picture and is the cover file byte for byte"); err != nil {
		return err
	}
	if err := verdict("H6 duration", durOK,
		"duration_ts and time_base are declared for FLAC, MP3, AAC and ALAC, and floor(duration_ts × time_base) in ms equals MusicLib's duration_ms"); err != nil {
		return err
	}
	if err := verdict("H9 ReplayGain", rgOK,
		"the four ReplayGain tags of the inputs are reported by ffprobe, with the same values, in the published FLAC, MP3, AAC and ALAC files"); err != nil {
		return err
	}
	for name, f := range map[string]*fragment{"H4a": &h4, "H6": &h6, "H9": &h9} {
		if err := f.save(name); err != nil {
			return err
		}
	}
	return runTimings(ctx, samples)
}

func apiMS(v *int64) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprint(*v)
}

// replayGainTags returns the tags of p whose key mentions ReplayGain,
// format and stream tags together, as "key=value" (the stream's with a
// "stream:" prefix).
func replayGainTags(p probeResult) map[string]string {
	out := map[string]string{}
	add := func(prefix string, tags map[string]string) {
		for k, v := range tags {
			if strings.Contains(strings.ToLower(k), "replaygain") {
				out[prefix+k] = v
			}
		}
	}
	add("", p.Format.Tags)
	for _, s := range p.Streams {
		add("stream:", s.Tags)
	}
	return out
}

// compareReplayGain probes the input file of t and its published file,
// writes both runs and a table into f, and reports whether every
// ReplayGain tag of the input is in the output with the same value, and
// whether all four are there (under whatever key ffprobe uses).
func compareReplayGain(ctx context.Context, f *fragment, spec albumSpec, t trackState, published string) (bool, error) {
	i := slices.IndexFunc(spec.Tracks, func(ts trackSpec) bool { return ts.Disc == t.Disc && ts.No == t.No })
	if i < 0 {
		return false, fmt.Errorf("album %s: no input for track %d-%d", spec.Key, t.Disc, t.No)
	}
	input := spec.trackFile(spec.Tracks[i])
	pin, runIn, err := probe(ctx, input)
	if err != nil {
		return false, err
	}
	pout, runOut, err := probe(ctx, published)
	if err != nil {
		return false, err
	}
	in, out := replayGainTags(pin), replayGainTags(pout)
	ok := len(in) == 4
	var keys []string
	for k := range in {
		keys = append(keys, k)
	}
	for k := range out {
		if _, dup := in[k]; !dup {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var rows [][]string
	for _, k := range keys {
		iv, iok := in[k]
		ov, ook := out[k]
		res := "kept"
		if !iok || !ook || iv != ov {
			ok, res = false, "differs"
		}
		rows = append(rows, []string{k, orDash(iv, iok), orDash(ov, ook), res})
	}
	var side []string
	st, _ := pout.audio()
	for _, sd := range st.SideData {
		side = append(side, fmt.Sprint(sd))
	}
	f.heading("Album %s, %s", spec.Key, t.Rel)
	f.table([]string{"ffprobe key", "input", "published", "result"}, rows)
	if len(side) > 0 {
		f.para("Side data ffprobe also reports on the published audio stream: `%s`.", strings.Join(side, "; "))
	}
	f.b.WriteString("<details><summary>The two probes</summary>\n\n")
	f.command(runIn.Cmdline, string(runIn.Stdout))
	f.command(runOut.Cmdline, string(runOut.Stdout))
	f.b.WriteString("</details>\n\n")
	return ok, nil
}

func orDash(v string, ok bool) string {
	if !ok {
		return "(absent)"
	}
	return v
}

// timingRuns is how many times each command is timed.
const timingRuns = 5

// runTimings is H8: the time of ffprobe and of the fingerprint on one file
// of each codec of the library and on a synthetic FLAC of about 50 MB,
// with, for that one, a full decode for comparison (DESIGN.md §5.4: the
// fingerprint costs a read of the file, not a decode).
func runTimings(ctx context.Context, samples []string) error {
	big := filepath.Join(workRoot, "big.flac")
	// Two independent noise channels: FLAC cannot compress them, so about
	// 290 s of 44.1 kHz 16-bit stereo make about 50 MB.
	gen := []string{"-filter_complex",
		"anoisesrc=color=white:sample_rate=44100:amplitude=0.5:seed=1[l];anoisesrc=color=white:sample_rate=44100:amplitude=0.5:seed=2[r];[l][r]amerge=inputs=2",
		"-t", "290", "-c:a", "flac", "-sample_fmt", "s16"}
	if err := ffmpegGenerate(ctx, gen, big); err != nil {
		return err
	}
	var f fragment
	f.b.WriteString("## H8: timings\n\n")
	f.para("Wall-clock time of each command, %d runs each (median, minimum, maximum), inside the tools container (%d CPUs, Linux kernel %s), the library on a Docker named volume. The synthetic FLAC is white noise (two independent channels, %s): `ffmpeg %s`. The full decode to `null` is there for comparison only. The fingerprint decodes nothing (`-c copy`): it reads the packets and hashes them with SHA-256, so its cost follows the size of the file; a decoder's cost depends on the codec (white noise makes a FLAC of almost verbatim frames, which decodes very fast).", timingRuns, runtime.NumCPU(), kernelRelease(), "290 s", shellLine(append(gen, big)))
	var rows [][]string
	for _, file := range append(samples, big) {
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		demux, err := demuxer(file)
		if err != nil {
			return err
		}
		probeT, probeRun, err := timeRuns(ctx, ffprobePath, probeArgs(demux), file)
		if err != nil {
			return err
		}
		fpT, fpRun, err := timeRuns(ctx, ffmpegPath, fingerprintArgs(demux), file)
		if err != nil {
			return err
		}
		decode := "-"
		if file == big {
			args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-protocol_whitelist", "fd", "-fd", "3",
				"-f", demux, "-i", "fd:", "-map", "0:a:0", "-f", "null", "-"}
			decT, decRun, err := timeRuns(ctx, ffmpegPath, args, file)
			if err != nil {
				return err
			}
			decode = decT.String()
			f.para("The commands on the synthetic FLAC:")
			f.command(probeRun.Cmdline+" >/dev/null", "")
			f.command(fpRun.Cmdline, string(fpRun.Stdout))
			f.command(decRun.Cmdline, string(decRun.Stdout))
		}
		rows = append(rows, []string{strings.TrimPrefix(file, libraryRoot+"/"), fmt.Sprint(info.Size()), probeT.String(), fpT.String(), decode})
	}
	f.table([]string{"file", "bytes", "ffprobe", "fingerprint", "full decode"}, rows)
	// H8 is a measurement: it holds once every command ran.
	if err := verdict("H8 timings", true,
		"measured: ffprobe and the fingerprint on one file per codec and on a 50 MB FLAC (table in H8)"); err != nil {
		return err
	}
	return f.save("H8")
}

type timing struct{ median, low, high time.Duration }

func (t timing) String() string {
	return fmt.Sprintf("%s (%s–%s)", t.median.Round(time.Millisecond), t.low.Round(time.Millisecond), t.high.Round(time.Millisecond))
}

func timeRuns(ctx context.Context, tool string, args []string, file string) (timing, toolRun, error) {
	var d []time.Duration
	var last toolRun
	for range timingRuns {
		run, err := runTool(ctx, tool, args, file)
		if err != nil {
			return timing{}, run, err
		}
		d, last = append(d, run.Elapsed), run
	}
	slices.Sort(d)
	return timing{median: d[len(d)/2], low: d[0], high: d[len(d)-1]}, last, nil
}

// kernelRelease is the running kernel, for the context of the timings.
func kernelRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

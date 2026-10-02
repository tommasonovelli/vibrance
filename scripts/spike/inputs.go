package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The input albums of DESIGN.md §12.2: tiny synthetic audio (a 2-second
// sine per track, a different frequency per track), so the fixture can be
// redistributed. Every managed tag is set, so that MusicLib imports them as
// they are; A–D, one per codec, also carry ReplayGain (H9).
type albumSpec struct {
	Key     string // the input folder, and the album's letter in §12.2
	Artist  string
	Title   string
	Year    int
	Genre   string
	Format  string // "flac", "mp3", "m4a-aac" or "m4a-alac"
	Cover   bool   // a cover.jpg in the input folder
	LRC     bool   // an .lrc for the first track
	RG      bool   // ReplayGain tags on every track
	Purpose string // for FIXTURE.md
	Tracks  []trackSpec
}

type trackSpec struct {
	Disc, No int
	Title    string
	Artist   string // "" is the album's artist
	Freq     int    // Hz
}

var albumSpecs = []albumSpec{
	{Key: "A", Artist: "Aurora Sines", Title: "Alpha: Light?", Year: 2001, Genre: "Ambient", Format: "flac",
		Cover: true, LRC: true, RG: true,
		Purpose: "FLAC, three tracks, cover.jpg, lyrics (.lrc) on track 1, ReplayGain, a featuring artist on track 2, characters that MusicLib replaces in names (`:` and `?`)",
		Tracks: []trackSpec{
			{1, 1, "First Light", "", 220},
			{1, 2, "Second Wave", "Aurora Sines feat. Guest", 247},
			{1, 3, "Third?", "", 262},
		}},
	{Key: "B", Artist: "Bravo Tones", Title: "Beta MP3", Year: 2002, Genre: "Jazz", Format: "mp3", RG: true,
		Purpose: "MP3 (LAME), two tracks, ReplayGain",
		Tracks:  []trackSpec{{1, 1, "One", "", 294}, {1, 2, "Two", "", 330}}},
	{Key: "C", Artist: "Charlie Waves", Title: "Gamma AAC", Year: 2003, Genre: "Rock", Format: "m4a-aac", RG: true,
		Purpose: "M4A with AAC, two tracks, ReplayGain",
		Tracks:  []trackSpec{{1, 1, "One", "", 349}, {1, 2, "Two", "", 392}}},
	{Key: "D", Artist: "Delta Pulse", Title: "Delta ALAC", Year: 2004, Genre: "Classical", Format: "m4a-alac", RG: true,
		Purpose: "M4A with ALAC, two tracks, ReplayGain",
		Tracks:  []trackSpec{{1, 1, "One", "", 440}, {1, 2, "Two", "", 494}}},
	{Key: "E", Artist: "Écho Café", Title: "Epsilon Discs", Year: 2005, Genre: "Pop", Format: "flac",
		Purpose: "FLAC on two discs (`Disc 1/`, `Disc 2/`), a non-ASCII artist name",
		Tracks: []trackSpec{
			{1, 1, "Disc One Track One", "", 523},
			{1, 2, "Disc One Track Two", "", 587},
			{2, 1, "Disc Two Track One", "", 659},
		}},
	{Key: "F", Artist: "Foxtrot Twins", Title: "Phi Same Audio", Year: 2006, Genre: "Electronic", Format: "flac",
		Purpose: "FLAC, two tracks with the same audio (same fingerprint, different tags)",
		Tracks:  []trackSpec{{1, 1, "Same Audio", "", 698}, {1, 2, "Same Audio Again", "", 698}}},
}

// ReplayGain values of the inputs: the track gain differs per track.
func replayGain(t trackSpec) [][2]string {
	return [][2]string{
		{"REPLAYGAIN_TRACK_GAIN", fmt.Sprintf("-%d.50 dB", t.No+5)},
		{"REPLAYGAIN_TRACK_PEAK", "0.125000"},
		{"REPLAYGAIN_ALBUM_GAIN", "-7.01 dB"},
		{"REPLAYGAIN_ALBUM_PEAK", "0.125000"},
	}
}

// The Debian snapshot and the exact packages of the taggers container
// (scripts/spike/compose.yaml): the same snapshot as MusicLib 1.2.0's
// Dockerfile.
const (
	debianSnapshot = "http://snapshot.debian.org/archive/debian/20260926T000000Z"
	lamePackage    = "lame=3.100-6+b3"
	parsleyPackage = "atomicparsley=20240608.083822.1ed9031-1"
)

const sineSeconds = 2

func (a albumSpec) inputDir() string { return filepath.Join(importRoot, a.Key) }

// trackFile is the input file of t, "Disc N/NN Title.ext" or "NN Title.ext".
func (a albumSpec) trackFile(t trackSpec) string {
	ext := map[string]string{"flac": ".flac", "mp3": ".mp3", "m4a-aac": ".m4a", "m4a-alac": ".m4a"}[a.Format]
	name := fmt.Sprintf("%02d %s%s", t.No, strings.TrimSuffix(t.Title, "?"), ext)
	if a.multiDisc() {
		name = filepath.Join(fmt.Sprintf("Disc %d", t.Disc), name)
	}
	return filepath.Join(a.inputDir(), name)
}

func (a albumSpec) multiDisc() bool {
	for _, t := range a.Tracks {
		if t.Disc != 1 {
			return true
		}
	}
	return false
}

func (a albumSpec) totals() (tracks map[int]int, discs int) {
	tracks = map[int]int{}
	for _, t := range a.Tracks {
		tracks[t.Disc]++
		discs = max(discs, t.Disc)
	}
	return tracks, discs
}

// runInputs writes the input albums into /import: FLAC and M4A with the
// pinned ffmpeg (the same binary as MusicLib's image, DESIGN.md D18), and,
// for the MP3, WAV files plus /work/taggers.sh, which the taggers container
// runs to encode them with LAME and to add the M4A ReplayGain atoms.
func runInputs(ctx context.Context) error {
	entries, err := os.ReadDir(importRoot)
	if err != nil {
		return fmt.Errorf("listing %s: %w", importRoot, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("%s is not empty: the scripts start from new volumes", importRoot)
	}
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n# Generated by `spike inputs`: runs in the taggers container.\nset -eu\n")
	fmt.Fprintf(&sh, "printf '%%s\\n' %s > /etc/apt/sources.list\n", shellQuote("deb [check-valid-until=no] "+debianSnapshot+" trixie main"))
	sh.WriteString("rm -f /etc/apt/sources.list.d/debian.sources\napt-get update -qq\n")
	fmt.Fprintf(&sh, "DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends %s %s >/dev/null\n", lamePackage, parsleyPackage)
	sh.WriteString("lame --version | head -n 1\nAtomicParsley --version\n")

	for _, a := range albumSpecs {
		for _, t := range a.Tracks {
			out := a.trackFile(t)
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			switch a.Format {
			case "mp3":
				wav := filepath.Join(workRoot, "wav", a.Key, fmt.Sprintf("%d-%02d.wav", t.Disc, t.No))
				if err := os.MkdirAll(filepath.Dir(wav), 0o755); err != nil {
					return err
				}
				if err := ffmpegGenerate(ctx, sineArgs(t, "pcm_s16le"), wav); err != nil {
					return err
				}
				sh.WriteString(shellLine(lameArgs(a, t, wav, out)) + "\n")
			case "m4a-aac", "m4a-alac":
				codec := map[string][]string{"m4a-aac": {"aac", "-b:a", "96k"}, "m4a-alac": {"alac"}}[a.Format]
				if err := ffmpegGenerate(ctx, append(append(sineArgs(t, codec[0]), codec[1:]...), metadataArgs(a, t)...), out); err != nil {
					return err
				}
				if a.RG {
					// Its progress bar would bury the output.
					sh.WriteString(shellLine(parsleyArgs(t, out)) + " >/dev/null\n")
				}
			default:
				if err := ffmpegGenerate(ctx, append(sineArgs(t, "flac"), metadataArgs(a, t)...), out); err != nil {
					return err
				}
			}
		}
		if a.Cover {
			if err := writeImage(filepath.Join(a.inputDir(), "cover.jpg"), color.RGBA{0x30, 0x60, 0xa0, 0xff}); err != nil {
				return err
			}
		}
		if a.LRC {
			t := a.Tracks[0]
			lrc := strings.TrimSuffix(a.trackFile(t), filepath.Ext(a.trackFile(t))) + ".lrc"
			body := fmt.Sprintf("[ar:%s]\n[ti:%s]\n[00:00.00]The first line\n[00:01.00]The second line\n", a.Artist, t.Title)
			if err := os.WriteFile(lrc, []byte(body), 0o644); err != nil {
				return err
			}
		}
	}
	// The covers the edits of H2 upload: a JPEG and then a PNG.
	for name, c := range map[string]color.RGBA{"cover-edit-1.jpg": {0xc0, 0x40, 0x40, 0xff}, "cover-edit-2.png": {0x40, 0xa0, 0x40, 0xff}} {
		if err := writeImage(filepath.Join(workRoot, name), c); err != nil {
			return err
		}
	}
	if err := writeWorkFile("taggers.sh", []byte(sh.String())); err != nil {
		return err
	}
	fmt.Println("inputs written; /work/taggers.sh encodes the MP3s and tags the M4As")
	return nil
}

// sineArgs is the lavfi input of track t, encoded with codec.
func sineArgs(t trackSpec, codec string) []string {
	return []string{"-f", "lavfi", "-i",
		"sine=frequency=" + strconv.Itoa(t.Freq) + ":sample_rate=44100:duration=" + strconv.Itoa(sineSeconds),
		"-ac", "2", "-c:a", codec}
}

// metadataArgs are the tags of an ffmpeg-written input: Vorbis comments by
// their names for FLAC, ffmpeg's generic keys for M4A (the ipod muxer maps
// them to the iTunes atoms).
func metadataArgs(a albumSpec, t trackSpec) []string {
	tracks, discs := a.totals()
	artist := a.Artist
	if t.Artist != "" {
		artist = t.Artist
	}
	var kv [][2]string
	if a.Format == "flac" {
		kv = [][2]string{{"TITLE", t.Title}, {"ARTIST", artist}, {"ALBUMARTIST", a.Artist}, {"ALBUM", a.Title},
			{"TRACKNUMBER", strconv.Itoa(t.No)}, {"TRACKTOTAL", strconv.Itoa(tracks[t.Disc])},
			{"DISCNUMBER", strconv.Itoa(t.Disc)}, {"DISCTOTAL", strconv.Itoa(discs)},
			{"DATE", strconv.Itoa(a.Year)}, {"GENRE", a.Genre}}
		if a.RG {
			kv = append(kv, replayGain(t)...)
		}
	} else {
		kv = [][2]string{{"title", t.Title}, {"artist", artist}, {"album_artist", a.Artist}, {"album", a.Title},
			{"track", fmt.Sprintf("%d/%d", t.No, tracks[t.Disc])}, {"disc", fmt.Sprintf("%d/%d", t.Disc, discs)},
			{"date", strconv.Itoa(a.Year)}, {"genre", a.Genre}}
	}
	var args []string
	for _, p := range kv {
		args = append(args, "-metadata", p[0]+"="+p[1])
	}
	return args
}

// lameArgs encodes wav into out with LAME and ID3v2 tags; TXXX frames carry
// ReplayGain.
func lameArgs(a albumSpec, t trackSpec, wav, out string) []string {
	tracks, discs := a.totals()
	args := []string{"lame", "--quiet", "-b", "128", "--add-id3v2",
		"--tt", t.Title, "--ta", a.Artist, "--tl", a.Title, "--ty", strconv.Itoa(a.Year),
		"--tn", fmt.Sprintf("%d/%d", t.No, tracks[t.Disc]), "--tg", a.Genre,
		"--tv", "TPE2=" + a.Artist, "--tv", fmt.Sprintf("TPOS=%d/%d", t.Disc, discs)}
	if t.Artist != "" {
		args[slices.Index(args, "--ta")+1] = t.Artist
	}
	if a.RG {
		for _, p := range replayGain(t) {
			args = append(args, "--tv", "TXXX="+p[0]+"="+p[1])
		}
	}
	return append(args, wav, out)
}

// parsleyArgs adds the ReplayGain freeform atoms that iTunes-style taggers
// write in M4A (----:com.apple.iTunes:replaygain_*): ffmpeg's ipod muxer
// drops keys it does not know.
func parsleyArgs(t trackSpec, file string) []string {
	args := []string{"AtomicParsley", file}
	for _, p := range replayGain(t) {
		args = append(args, "--rDNSatom", p[1], "name="+strings.ToLower(p[0]), "domain=com.apple.iTunes")
	}
	return append(args, "--overWrite")
}

// ffmpegGenerate runs the pinned ffmpeg to write out (a file of the spike,
// not an input of Vibrance, so a path is fine), bit-exact so that the same
// inputs give the same bytes.
func ffmpegGenerate(ctx context.Context, args []string, out string) error {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	full := append([]string{"-hide_banner", "-nostdin", "-loglevel", "error"}, args...)
	full = append(full, "-fflags", "+bitexact", "-flags:a", "+bitexact", "-y", out)
	cmd := exec.CommandContext(ctx, ffmpegPath, full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg %s: %w: %s", shellLine(full), err, bytes.TrimSpace(stderr.Bytes()))
	}
	fmt.Printf("%s (%s)\n", out, time.Since(start).Round(time.Millisecond))
	return nil
}

// writeImage writes a 64×64 single-colour image, JPEG or PNG by extension,
// with the standard library (the pinned ffmpeg has no PNG encoder).
func writeImage(p string, c color.RGBA) error {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	var err error
	if filepath.Ext(p) == ".png" {
		err = png.Encode(&b, img)
	} else {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	}
	if err != nil {
		return fmt.Errorf("encoding %s: %w", p, err)
	}
	return os.WriteFile(p, b.Bytes(), 0o644)
}

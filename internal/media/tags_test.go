package media

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }

// show prints Tags with the values its pointers point to.
func show(g Tags) string {
	p := func(v *float64) string {
		if v == nil {
			return "nil"
		}
		return fmt.Sprint(*v)
	}
	return fmt.Sprintf("{Title:%q Artist:%q AlbumArtist:%q Album:%q Genre:%q Track:%d Disc:%d Year:%d Compilation:%v "+
		"TrackGain:%s TrackPeak:%s AlbumGain:%s AlbumPeak:%s}", g.Title, g.Artist, g.AlbumArtist, g.Album, g.Genre,
		g.Track, g.Disc, g.Year, g.Compilation, p(g.TrackGain), p(g.TrackPeak), p(g.AlbumGain), p(g.AlbumPeak))
}

func wantTags(t *testing.T, got, want Tags) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags:\n got %s\nwant %s", show(got), show(want))
	}
}

// The format tags that ffprobe reports for a track of each format, copied
// from docs/spike-report.md (H3, "The four probes"), with the keys Vibrance
// does not read: the real table that MapTags is written from.
var (
	spikeFLAC = map[string]string{
		"ALBUM": "Alpha: Light?", "album_artist": "Aurora Sines Renamed", "ARTIST": "Aurora Sines Renamed",
		"COMPILATION": "1", "DATE": "2001", "disc": "1", "DISCTOTAL": "1", "GENRE": "Ambient",
		"REPLAYGAIN_ALBUM_GAIN": "-7.01 dB", "REPLAYGAIN_ALBUM_PEAK": "0.125000",
		"REPLAYGAIN_TRACK_GAIN": "-6.50 dB", "REPLAYGAIN_TRACK_PEAK": "0.125000",
		"TITLE": "First Light (edited)", "track": "2", "TRACKTOTAL": "3",
	}
	spikeMP3 = map[string]string{
		"title": "One (edited)", "artist": "Bravo Tones Renamed", "album_artist": "Bravo Tones Renamed",
		"album": "Beta MP3", "track": "2/2", "disc": "1/1", "date": "2002", "genre": "Jazz", "compilation": "1",
		"encoder":               "LAME 64bits version 3.100 (http://lame.sf.net)",
		"REPLAYGAIN_TRACK_GAIN": "-6.50 dB", "REPLAYGAIN_TRACK_PEAK": "0.125000",
		"REPLAYGAIN_ALBUM_GAIN": "-7.01 dB", "REPLAYGAIN_ALBUM_PEAK": "0.125000", "TLEN": "2000",
	}
	spikeM4A = map[string]string{
		"major_brand": "M4A ", "minor_version": "512", "compatible_brands": "M4A isomiso2",
		"title": "One (edited)", "artist": "Charlie Waves Renamed", "album_artist": "Charlie Waves Renamed",
		"album": "Gamma AAC", "track": "2/2", "disc": "1/1", "date": "2003", "genre": "Rock", "compilation": "1",
		"replaygain_track_gain": "-6.50 dB", "replaygain_track_peak": "0.125000",
		"replaygain_album_gain": "-7.01 dB", "replaygain_album_peak": "0.125000",
	}
)

// The keys of the three formats differ only in their case, and the numbers
// in whether they carry the total.
func TestMapTagsOfEachFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]string
		want Tags
	}{
		{"FLAC", spikeFLAC, Tags{Title: "First Light (edited)", Artist: "Aurora Sines Renamed", AlbumArtist: "Aurora Sines Renamed",
			Album: "Alpha: Light?", Genre: "Ambient", Track: 2, Disc: 1, Year: 2001, Compilation: true}},
		{"MP3", spikeMP3, Tags{Title: "One (edited)", Artist: "Bravo Tones Renamed", AlbumArtist: "Bravo Tones Renamed",
			Album: "Beta MP3", Genre: "Jazz", Track: 2, Disc: 1, Year: 2002, Compilation: true}},
		{"M4A", spikeM4A, Tags{Title: "One (edited)", Artist: "Charlie Waves Renamed", AlbumArtist: "Charlie Waves Renamed",
			Album: "Gamma AAC", Genre: "Rock", Track: 2, Disc: 1, Year: 2003, Compilation: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			want.TrackGain, want.TrackPeak, want.AlbumGain, want.AlbumPeak = f64(-6.5), f64(0.125), f64(-7.01), f64(0.125)
			wantTags(t, MapTags(tc.raw), want)
		})
	}
}

// No tags at all, and tags Vibrance does not read: every field is absent.
func TestMapTagsAbsent(t *testing.T) {
	wantTags(t, MapTags(nil), Tags{})
	wantTags(t, MapTags(map[string]string{}), Tags{})
	wantTags(t, MapTags(map[string]string{
		"TRACKTOTAL": "3", "DISCTOTAL": "2", "encoder": "x", "TLEN": "2000", "titlesort": "a", "TITLESORT": "a",
		"composer": "c", "comment": "c", "lyrics": "la la", "album artist": "x", "albumartist": "x", "year": "1999",
		"tracknumber": "4", "discnumber": "2", "": "empty key", "title ": "trailing space", "tïtle": "not ASCII",
	}), Tags{})
}

// The keys are matched whatever the case of their ASCII letters, and only
// of those.
func TestMapTagsKeyCase(t *testing.T) {
	want := Tags{Title: "T", Artist: "A", AlbumArtist: "AA", Album: "AL", Genre: "G", Track: 3, Disc: 2, Year: 1999,
		Compilation: true, TrackGain: f64(1), TrackPeak: f64(2), AlbumGain: f64(3), AlbumPeak: f64(4)}
	lower := map[string]string{
		"title": "T", "artist": "A", "album_artist": "AA", "album": "AL", "genre": "G", "track": "3", "disc": "2",
		"date": "1999", "compilation": "1", "replaygain_track_gain": "1 dB", "replaygain_track_peak": "2",
		"replaygain_album_gain": "3 dB", "replaygain_album_peak": "4",
	}
	recase := func(f func(string) string) map[string]string {
		out := map[string]string{}
		for k, v := range lower {
			out[f(k)] = v
		}
		return out
	}
	wantTags(t, MapTags(lower), want)
	wantTags(t, MapTags(recase(strings.ToUpper)), want)
	wantTags(t, MapTags(recase(func(k string) string { return strings.ToUpper(k[:1]) + k[1:] })), want)
	wantTags(t, MapTags(recase(func(k string) string { return k[:len(k)-1] + strings.ToUpper(k[len(k)-1:]) })), want)

	// U+212A KELVIN SIGN and U+017F LONG S fold to "k" and "s" in Unicode:
	// they are not the letters of a key.
	wantTags(t, MapTags(map[string]string{"tracK": "3", "diſc": "2"}), Tags{})

	// The same key in two cases cannot come from MusicLib. The answer does
	// not depend on the order of the map: the key that sorts first wins.
	for range 50 {
		wantTags(t, MapTags(map[string]string{"TITLE": "upper", "title": "lower", "Title": "mixed"}), Tags{Title: "upper"})
	}
}

// The text fields are the values as they are: one string, whatever it
// holds (§4.3: the values of a multi-valued tag are joined by MusicLib).
func TestMapTagsText(t *testing.T) {
	raw := map[string]string{
		"title":        "  Spaced  ",
		"artist":       "A; B; C",
		"album_artist": "Björk feat. 坂本龍一",
		"album":        "",
		"genre":        "Rock; Jazz",
	}
	wantTags(t, MapTags(raw), Tags{Title: "  Spaced  ", Artist: "A; B; C", AlbumArtist: "Björk feat. 坂本龍一", Genre: "Rock; Jazz"})
}

func TestMapTagsNumbers(t *testing.T) {
	for value, want := range map[string]int{
		"1": 1, "7": 7, "12": 12, "999": 999, "2/12": 2, "2/2": 2, "07": 7, "007/012": 7, " 5 ": 5, " 5 / 9 ": 5,
		"3/": 3, "3/x": 3, "3/0": 3, "3/4/5": 3, "0000000000000000000000000000001": 1,
		"": 0, " ": 0, "0": 0, "00": 0, "0/12": 0, "/12": 0, "1000": 0, "99999999999999999999999999999": 0,
		"-1": 0, "+1": 0, "1.0": 0, "1e1": 0, "0x10": 0, "one": 0, "A1": 0, "1A": 0, "1 2": 0, "１": 0, "٣": 0, "1\x00": 0,
	} {
		if got := MapTags(map[string]string{"track": value}).Track; got != want {
			t.Errorf("track %q: %d, want %d", value, got, want)
		}
	}
	// A disc number goes up to 99.
	for value, want := range map[string]int{"1": 1, "1/2": 1, "99": 99, "99/99": 99, "100": 0, "0": 0, "": 0, "2 of 3": 0} {
		if got := MapTags(map[string]string{"disc": value}).Disc; got != want {
			t.Errorf("disc %q: %d, want %d", value, got, want)
		}
	}
}

// The year is the first four characters of the date, which must be digits.
func TestMapTagsYear(t *testing.T) {
	for value, want := range map[string]int{
		"1959": 1959, "2001": 2001, "0001": 1, "9999": 9999, "1959-08-17": 1959, "1959-08": 1959,
		"1959-08-17T10:00:00Z": 1959, " 1959 ": 1959, "19590817": 1959, "1959?": 1959,
		"": 0, "0000": 0, "0000-01-01": 0, "195": 0, "59": 0, "c1959": 0, "-1959": 0, "+1959": 0, "19a9": 0,
		"May 1959": 0, "17/08/1959": 0, "１９５９": 0, "1 959": 0,
	} {
		if got := MapTags(map[string]string{"DATE": value}).Year; got != want {
			t.Errorf("date %q: year %d, want %d", value, got, want)
		}
	}
}

// MusicLib writes the compilation flag as "1" and removes it otherwise.
func TestMapTagsCompilation(t *testing.T) {
	for value, want := range map[string]bool{
		"1": true, " 1 ": true,
		"": false, "0": false, "2": false, "01": false, "true": false, "yes": false, "1.0": false, "11": false, "-1": false,
	} {
		if got := MapTags(map[string]string{"COMPILATION": value}).Compilation; got != want {
			t.Errorf("compilation %q: %v, want %v", value, got, want)
		}
	}
}

// ReplayGain: "-7.12 dB" is the number -7.12, a peak is a number that is
// not negative.
func TestMapTagsReplayGain(t *testing.T) {
	for value, want := range map[string]*float64{
		"-7.12 dB": f64(-7.12), "-6.50 dB": f64(-6.5), "+1.50 dB": f64(1.5), "0.00 dB": f64(0), "-0.00 dB": f64(0),
		"3 dB": f64(3), "-7.12": f64(-7.12), "-7.12dB": f64(-7.12), "-7.12 db": f64(-7.12), "-7.12 DB": f64(-7.12),
		"  -7.12   dB  ": f64(-7.12), "12": f64(12), "-51.00 dB": f64(-51), "64.82 dB": f64(64.82),
		"": nil, " ": nil, "dB": nil, " dB": nil, "- 7.12 dB": nil, "-7,12 dB": nil, "-7.12 dBFS": nil, "-7.12 dB dB": nil,
		"-7.12 LU": nil, "NaN": nil, "nan dB": nil, "Inf": nil, "-Inf dB": nil, "+Infinity": nil, "1e3 dB": nil,
		"0x1p4": nil, "1_000": nil, ".5 dB": nil, "5. dB": nil, "--1 dB": nil, "1.2.3 dB": nil, "７ dB": nil,
		"1" + strings.Repeat("0", 400) + " dB": nil,
	} {
		for _, key := range []string{"REPLAYGAIN_TRACK_GAIN", "replaygain_album_gain"} {
			got := MapTags(map[string]string{key: value})
			if key == "replaygain_album_gain" {
				wantTags(t, got, Tags{AlbumGain: want})
			} else {
				wantTags(t, got, Tags{TrackGain: want})
			}
		}
	}
	for value, want := range map[string]*float64{
		"0.988553": f64(0.988553), "0.125000": f64(0.125), "1.000000": f64(1), "1.230000": f64(1.23), "0": f64(0),
		"0.000000": f64(0), " 0.5 ": f64(0.5), "+0.5": f64(0.5), "-0.0": f64(0), "2": f64(2),
		"": nil, "-0.5": nil, "0.5 dB": nil, "NaN": nil, "Inf": nil, "1e-3": nil, "0,5": nil, "half": nil,
	} {
		for _, key := range []string{"REPLAYGAIN_TRACK_PEAK", "replaygain_album_peak"} {
			got := MapTags(map[string]string{key: value})
			if key == "replaygain_album_peak" {
				wantTags(t, got, Tags{AlbumPeak: want})
			} else {
				wantTags(t, got, Tags{TrackPeak: want})
			}
		}
	}
	// A zero is never negative: it would print as "-0".
	if v := MapTags(map[string]string{"replaygain_track_gain": "-0.00 dB"}).TrackGain; v == nil || math.Signbit(*v) {
		t.Fatalf("the gain of %q is a negative zero", "-0.00 dB")
	}
}

// rawTags reads the map of tags a fuzz input stands for: one "key=value"
// per line.
func rawTags(data string) map[string]string {
	raw := map[string]string{}
	for line := range strings.SplitSeq(data, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			raw[k] = v
		}
	}
	return raw
}

// MapTags is given whatever a file holds: it must never panic, and what it
// returns must always be within the ranges of the index and depend on the
// tags only, not on the order of a map or on the case of the keys.
func FuzzMapTags(f *testing.F) {
	for _, seed := range []map[string]string{spikeFLAC, spikeMP3, spikeM4A} {
		var b strings.Builder
		for k, v := range seed {
			b.WriteString(k + "=" + v + "\n")
		}
		f.Add(b.String())
	}
	f.Add("track=2/12\ndisc=1/2\ndate=1959-08-17\ncompilation=1\nREPLAYGAIN_TRACK_GAIN=-7.12 dB\nreplaygain_track_peak=0.988553")
	f.Add("TITLE=upper\ntitle=lower\nTitle=mixed\ntrack=1000\ndisc=100\ndate=0000")
	f.Add("replaygain_album_gain=NaN dB\nreplaygain_album_peak=-1\ntrack=99999999999999999999\ndate=\xff\xfe59")
	f.Add("tracK=3\nreplaygain_track_gain=1" + strings.Repeat("0", 400) + " dB\n=x\ntrack==\n")

	f.Fuzz(func(t *testing.T, data string) {
		raw := rawTags(data)
		got := MapTags(raw)

		if got.Track < 0 || got.Track > 999 || got.Disc < 0 || got.Disc > 99 || got.Year < 0 || got.Year > 9999 {
			t.Fatalf("out of range: %s", show(got))
		}
		for _, v := range []*float64{got.TrackGain, got.TrackPeak, got.AlbumGain, got.AlbumPeak} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || (*v == 0 && math.Signbit(*v))) {
				t.Fatalf("not a finite number: %s", show(got))
			}
		}
		for _, v := range []*float64{got.TrackPeak, got.AlbumPeak} {
			if v != nil && *v < 0 {
				t.Fatalf("a negative peak: %s", show(got))
			}
		}
		// A text field is a value of the map, untouched.
		values := map[string]bool{"": true}
		for _, v := range raw {
			values[v] = true
		}
		for _, s := range []string{got.Title, got.Artist, got.AlbumArtist, got.Album, got.Genre} {
			if !values[s] {
				t.Fatalf("%q is not a value of the tags: %s", s, show(got))
			}
		}

		// The same tags give the same answer, whatever the order in which
		// the map is walked.
		for range 4 {
			if again := MapTags(raw); !reflect.DeepEqual(again, got) {
				t.Fatalf("two answers for the same tags:\n%s\n%s", show(got), show(again))
			}
		}
		// The case of the keys does not matter, as long as no two keys
		// differ only by it.
		upper := map[string]string{}
		for k, v := range raw {
			upper[asciiUpper(k)] = v
		}
		if len(upper) == len(raw) {
			if recased := MapTags(upper); !reflect.DeepEqual(recased, got) {
				t.Fatalf("the case of the keys changed the answer:\n%s\n%s", show(got), show(recased))
			}
		}
		// Tags that MapTags does not read change nothing.
		raw["encoder"], raw["TRACKTOTAL"], raw["lyrics"] = "x", "9", "la"
		if with := MapTags(raw); !reflect.DeepEqual(with, got) {
			t.Fatalf("a tag that is not read changed the answer:\n%s\n%s", show(got), show(with))
		}
	})
}

func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}

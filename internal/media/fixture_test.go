package media

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

// fixtureTrack is what a track file of the fixture library holds.
type fixtureTrack struct {
	// path is relative to the fixture library.
	path string
	info Info // without Tags
	tags Tags
	// fingerprint is the one docs/spike-report.md records for the track
	// (H2, "Different audio, different fingerprints"). The report follows
	// each track through MusicLib's edits, and the fingerprint is the same
	// in the fixture, which is the library before them.
	fingerprint string
}

func ms(v int64) *int64 { return &v }

// fixtureTracks are all the track files of testdata/library-v1: two-second
// sines, 44.1 kHz stereo, imported by MusicLib 1.1.0 (testdata/FIXTURE.md).
// Albums A to D carry ReplayGain; no album is a compilation.
var fixtureTracks = []fixtureTrack{
	{"Aurora Sines/Alpha_ Light_/01 - First Light.flac",
		Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)},
		Tags{Title: "First Light", Artist: "Aurora Sines", AlbumArtist: "Aurora Sines", Album: "Alpha: Light?", Genre: "Ambient",
			Track: 1, Disc: 1, Year: 2001, TrackGain: f64(-6.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"e2b19f9a76987131c390ccc590eaf6d6819c8a809fc7ffb0ca7251d29054464c"},
	{"Aurora Sines/Alpha_ Light_/02 - Second Wave.flac",
		Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)},
		Tags{Title: "Second Wave", Artist: "Aurora Sines feat. Guest", AlbumArtist: "Aurora Sines", Album: "Alpha: Light?", Genre: "Ambient",
			Track: 2, Disc: 1, Year: 2001, TrackGain: f64(-7.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"5460de58357ae782e98c5116a5df926c7186877922695d19ae3ef78ba4c27e51"},
	{"Aurora Sines/Alpha_ Light_/03 - Third_.flac",
		Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)},
		Tags{Title: "Third?", Artist: "Aurora Sines", AlbumArtist: "Aurora Sines", Album: "Alpha: Light?", Genre: "Ambient",
			Track: 3, Disc: 1, Year: 2001, TrackGain: f64(-8.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"28041741fdd9bb2ffceb16a8c5f958815c1d1e1972dd40ecdf79a4850535c424"},
	{"Bravo Tones/Beta MP3/01 - One.mp3",
		Info{Codec: CodecMP3, SampleRate: 44100, Channels: 2, Bitrate: 128000, DurationMS: ms(2000)},
		Tags{Title: "One", Artist: "Bravo Tones", AlbumArtist: "Bravo Tones", Album: "Beta MP3", Genre: "Jazz",
			Track: 1, Disc: 1, Year: 2002, TrackGain: f64(-6.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"20a49a4b23ab41d7eef89093ac1f9dbff29b8d42a717a77c30dc0f30d89b4053"},
	{"Bravo Tones/Beta MP3/02 - Two.mp3",
		Info{Codec: CodecMP3, SampleRate: 44100, Channels: 2, Bitrate: 128000, DurationMS: ms(2000)},
		Tags{Title: "Two", Artist: "Bravo Tones", AlbumArtist: "Bravo Tones", Album: "Beta MP3", Genre: "Jazz",
			Track: 2, Disc: 1, Year: 2002, TrackGain: f64(-7.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"6b051886d465925cfcfb4ab4f690fc85ce472ca15c7ffcaf3ebaf110b4843d7a"},
	{"Charlie Waves/Gamma AAC/01 - One.m4a",
		Info{Codec: CodecAAC, SampleRate: 44100, Channels: 2, Bitrate: 96392, DurationMS: ms(2000)},
		Tags{Title: "One", Artist: "Charlie Waves", AlbumArtist: "Charlie Waves", Album: "Gamma AAC", Genre: "Rock",
			Track: 1, Disc: 1, Year: 2003, TrackGain: f64(-6.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"504febaca2eeea30c89b87970b47e7a6eb5c5370c8d0841c1781e9aef169cada"},
	{"Charlie Waves/Gamma AAC/02 - Two.m4a",
		Info{Codec: CodecAAC, SampleRate: 44100, Channels: 2, Bitrate: 95930, DurationMS: ms(2000)},
		Tags{Title: "Two", Artist: "Charlie Waves", AlbumArtist: "Charlie Waves", Album: "Gamma AAC", Genre: "Rock",
			Track: 2, Disc: 1, Year: 2003, TrackGain: f64(-7.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"3c1564cfd1889aa04e1b69b7677241e57f8d204507c0d2e4ccbfe5f44e937449"},
	{"Delta Pulse/Delta ALAC/01 - One.m4a",
		Info{Codec: CodecALAC, SampleRate: 44100, Channels: 2, BitDepth: 16, Bitrate: 136208, DurationMS: ms(2000)},
		Tags{Title: "One", Artist: "Delta Pulse", AlbumArtist: "Delta Pulse", Album: "Delta ALAC", Genre: "Classical",
			Track: 1, Disc: 1, Year: 2004, TrackGain: f64(-6.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"acf895ce37b840d3d1fe13a178aa9925cc0bb0f834b5bcf71b50fd9b616dff77"},
	{"Delta Pulse/Delta ALAC/02 - Two.m4a",
		Info{Codec: CodecALAC, SampleRate: 44100, Channels: 2, BitDepth: 16, Bitrate: 138348, DurationMS: ms(2000)},
		Tags{Title: "Two", Artist: "Delta Pulse", AlbumArtist: "Delta Pulse", Album: "Delta ALAC", Genre: "Classical",
			Track: 2, Disc: 1, Year: 2004, TrackGain: f64(-7.5), TrackPeak: f64(0.125), AlbumGain: f64(-7.01), AlbumPeak: f64(0.125)},
		"1683c6399524b716f47ca75d4226c589b50b02d18765cb70162c92f9e834ae6c"},
	{"Écho Café/Epsilon Discs/Disc 1/01 - Disc One Track One.flac",
		Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)},
		Tags{Title: "Disc One Track One", Artist: "Écho Café", AlbumArtist: "Écho Café", Album: "Epsilon Discs", Genre: "Pop",
			Track: 1, Disc: 1, Year: 2005},
		"27d802a92566f8eacee14e07330c75aee20acead2f6b5acf8583f8285862ef3b"},
	{"Écho Café/Epsilon Discs/Disc 1/02 - Disc One Track Two.flac",
		Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)},
		Tags{Title: "Disc One Track Two", Artist: "Écho Café", AlbumArtist: "Écho Café", Album: "Epsilon Discs", Genre: "Pop",
			Track: 2, Disc: 1, Year: 2005},
		"b65264ad65281ab17f0f1b3898380bb10cd8c0a50b1fac4e81c615d572098158"},
	{"Écho Café/Epsilon Discs/Disc 2/01 - Disc Two Track One.flac",
		Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)},
		Tags{Title: "Disc Two Track One", Artist: "Écho Café", AlbumArtist: "Écho Café", Album: "Epsilon Discs", Genre: "Pop",
			Track: 1, Disc: 2, Year: 2005},
		"1016939daf7ed11d08e9e3a462026d17781d990345edb99d7a8d9ed848fb5045"},
	{"Foxtrot Twins/Phi Same Audio/01 - Same Audio.flac",
		Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)},
		Tags{Title: "Same Audio", Artist: "Foxtrot Twins", AlbumArtist: "Foxtrot Twins", Album: "Phi Same Audio", Genre: "Electronic",
			Track: 1, Disc: 1, Year: 2006},
		"aff7f05f07877bc111dda862c7a0217429b323d1aaa8a2276294096309ea7904"},
	{"Foxtrot Twins/Phi Same Audio/02 - Same Audio Again.flac",
		Info{Codec: CodecFLAC, SampleRate: 44100, Channels: 2, BitDepth: 16, DurationMS: ms(2000)},
		Tags{Title: "Same Audio Again", Artist: "Foxtrot Twins", AlbumArtist: "Foxtrot Twins", Album: "Phi Same Audio", Genre: "Electronic",
			Track: 2, Disc: 1, Year: 2006},
		"aff7f05f07877bc111dda862c7a0217429b323d1aaa8a2276294096309ea7904"},
}

// One track of each codec, for the tests that make files from them.
const (
	fixtureFLAC = "Aurora Sines/Alpha_ Light_/01 - First Light.flac" // with an embedded cover
	fixtureMP3  = "Bravo Tones/Beta MP3/01 - One.mp3"
	fixtureAAC  = "Charlie Waves/Gamma AAC/01 - One.m4a"
	fixtureALAC = "Delta Pulse/Delta ALAC/01 - One.m4a"
)

// track is the fixture track at path.
func track(t *testing.T, path string) fixtureTrack {
	t.Helper()
	for _, tr := range fixtureTracks {
		if tr.path == path {
			return tr
		}
	}
	t.Fatalf("%s is not a track of the fixture", path)
	return fixtureTrack{}
}

// showInfo prints an Info without its tags.
func showInfo(i Info) string {
	duration := "nil"
	if i.DurationMS != nil {
		duration = strconv.FormatInt(*i.DurationMS, 10)
	}
	return fmt.Sprintf("{Codec:%s SampleRate:%d Channels:%d BitDepth:%d Bitrate:%d DurationMS:%s}",
		i.Codec, i.SampleRate, i.Channels, i.BitDepth, i.Bitrate, duration)
}

// wantInfo compares everything of an Info but its tags.
func wantInfo(t *testing.T, got, want Info) {
	t.Helper()
	got.Tags, want.Tags = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("info:\n got %s\nwant %s", showInfo(got), showInfo(want))
	}
}

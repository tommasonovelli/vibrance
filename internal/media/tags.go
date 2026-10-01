package media

import (
	"regexp"
	"strconv"
	"strings"
)

// The keys ffprobe gives to the tags Vibrance reads, in lower case. They
// are the same for FLAC, MP3 and M4A, but for their case: FLAC keeps the
// upper case of the Vorbis comments it does not rename (TITLE, ARTIST,
// ALBUM, DATE, GENRE, COMPILATION, REPLAYGAIN_*), MP3 has REPLAYGAIN_* in
// upper case and the rest in lower case, M4A is all lower case
// (docs/spike-report.md, H3 and H9). The total of tracks and of discs is
// inside "track" and "disc" for MP3 and M4A ("2/12") and in keys of its own
// for FLAC; Vibrance does not read it.
const (
	tagTitle       = "title"
	tagArtist      = "artist"
	tagAlbumArtist = "album_artist"
	tagAlbum       = "album"
	tagTrack       = "track"
	tagDisc        = "disc"
	tagDate        = "date"
	tagGenre       = "genre"
	tagCompilation = "compilation"
	tagTrackGain   = "replaygain_track_gain"
	tagTrackPeak   = "replaygain_track_peak"
	tagAlbumGain   = "replaygain_album_gain"
	tagAlbumPeak   = "replaygain_album_peak"
)

// tagKeys are all of them: the only tags ffprobe is asked for (probeArgs),
// and the only ones MapTags reads.
var tagKeys = []string{
	tagTitle, tagArtist, tagAlbumArtist, tagAlbum, tagTrack, tagDisc, tagDate, tagGenre, tagCompilation,
	tagTrackGain, tagTrackPeak, tagAlbumGain, tagAlbumPeak,
}

// The highest track and disc numbers (tracks.no and tracks.disc, DESIGN.md
// §5.2), which are MusicLib's.
const (
	maxTrack = 999
	maxDisc  = 99
)

// Tags are the tags of a track file (§4.3). MusicLib rewrites the fields
// it manages at every render, so an absent value means that there is none.
type Tags struct {
	// The text fields are the values of the tags as they are, "" when
	// absent. A tag with several values is one string (§4.3).
	Title       string
	Artist      string
	AlbumArtist string
	Album       string
	Genre       string
	// Track is the number of the track, from 1 to 999, and Disc that of
	// its disc, from 1 to 99. 0: absent, or not such a number.
	Track int
	Disc  int
	// Year is from 1 to 9999. 0: absent, or the date does not begin with
	// four digits.
	Year int
	// Compilation is true when the tag is "1", which is how MusicLib marks
	// a compilation.
	Compilation bool
	// The ReplayGain of the track and of its album: the gains in dB, the
	// peaks as a linear amplitude. nil: absent, or not a number. They are
	// pointers because 0 is a gain and a peak like any other.
	TrackGain *float64
	TrackPeak *float64
	AlbumGain *float64
	AlbumPeak *float64
}

// MapTags reads the tags of a file out of the format tags that ffprobe
// reports (Info.Tags). The keys are matched without regard to the case of
// the ASCII letters. It does no I/O, and it never fails: a value it cannot
// read is an absent one.
func MapTags(raw map[string]string) Tags {
	get := lookup(raw)
	return Tags{
		Title:       get(tagTitle),
		Artist:      get(tagArtist),
		AlbumArtist: get(tagAlbumArtist),
		Album:       get(tagAlbum),
		Genre:       get(tagGenre),
		Track:       number(get(tagTrack), maxTrack),
		Disc:        number(get(tagDisc), maxDisc),
		Year:        year(get(tagDate)),
		Compilation: strings.TrimSpace(get(tagCompilation)) == "1",
		TrackGain:   gain(get(tagTrackGain)),
		TrackPeak:   peak(get(tagTrackPeak)),
		AlbumGain:   gain(get(tagAlbumGain)),
		AlbumPeak:   peak(get(tagAlbumPeak)),
	}
}

// lookup returns the function that gives the value of a key of tagKeys,
// "" when raw does not have it. Should raw have the same key in two cases,
// the one that sorts first wins, so that the result does not depend on the
// order of a map.
func lookup(raw map[string]string) func(key string) string {
	type entry struct{ key, value string }
	found := make(map[string]entry, len(tagKeys))
	for k, v := range raw {
		lower := asciiLower(k)
		if e, ok := found[lower]; !ok || k < e.key {
			found[lower] = entry{k, v}
		}
	}
	return func(key string) string { return found[key].value }
}

// asciiLower lowers the ASCII letters of s and leaves every other byte as
// it is: a key is never matched through a Unicode case folding.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// number reads the N of "N" and of "N/M", a decimal number from 1 to
// limit. Anything else is 0. M is not read.
func number(s string, limit int) int {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "/")
	s = strings.TrimSpace(s)
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		if n = n*10 + int(s[i]-'0'); n > limit {
			return 0
		}
	}
	return n
}

// year reads the first four characters of a date, which must be digits:
// "1959" and "1959-08-17" are 1959. Anything else, and the year 0000, is 0.
func year(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return 0
	}
	y := 0
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		y = y*10 + int(s[i]-'0')
	}
	return y
}

// decimalSyntax is a plain decimal number: what the ReplayGain tags hold.
// strconv.ParseFloat alone would also take "Inf", "NaN", exponents and
// hexadecimal.
var decimalSyntax = regexp.MustCompile(`^[+-]?[0-9]+(\.[0-9]+)?$`)

// decimal reads a plain decimal number; nil if s is not one.
func decimal(s string) *float64 {
	if !decimalSyntax.MatchString(s) {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil { // out of the range of a float64
		return nil
	}
	if v == 0 {
		v = 0 // never -0
	}
	return &v
}

// gain reads a ReplayGain gain: a decimal number with an optional "dB"
// after it, as in "-7.12 dB".
func gain(s string) *float64 {
	s = strings.TrimSpace(s)
	if n := len(s) - 2; n >= 0 && strings.EqualFold(s[n:], "dB") {
		s = strings.TrimSpace(s[:n])
	}
	return decimal(s)
}

// peak reads a ReplayGain peak: a decimal number that is not negative, as
// in "0.988553".
func peak(s string) *float64 {
	v := decimal(strings.TrimSpace(s))
	if v == nil || *v < 0 {
		return nil
	}
	return v
}

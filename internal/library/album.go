package library

import (
	"cmp"

	"vibrance/internal/names"
)

// The names of an album whose tracks say none (DESIGN.md §5.3). MusicLib
// writes the album and the artist in every track, so they are the names of
// files it did not write.
const (
	unknownArtist = "Unknown Artist"
	unknownAlbum  = "Unknown Album"
)

// TrackTags is what DeriveAlbum reads of one track of the album: its place
// and the tags that say something of the album. A text that is empty, or
// only white space, is a tag the track does not have.
type TrackTags struct {
	// Disc and No are the place of the track in the album: they decide
	// between two titles, or two artists, that as many tracks have.
	Disc int
	No   int
	// Album, AlbumArtist, Artist and Genre are the tags as the file has
	// them.
	Album       string
	AlbumArtist string
	Artist      string
	Genre       string
	// Year is the year of the DATE tag; a value outside 1..9999 is none.
	Year int
	// Compilation is true when the track says it is part of a compilation.
	Compilation bool
}

// AlbumMeta is what the tracks of an album say of it (§5.3).
type AlbumMeta struct {
	// Title and Artist are never empty.
	Title  string
	Artist string
	// Year is 0 when no track has a valid one.
	Year int
	// Genre is "" when no track has one.
	Genre       string
	Compilation bool
}

// DeriveAlbum derives the data of an album from its tracks (§5.3). MusicLib
// writes the same album tags in every track; the rules say what the album
// is when the tracks do not agree.
//
//   - Title: the ALBUM tag most tracks have; of two that as many tracks
//     have, the one of the track that comes first by (disc, no). Without
//     any, "Unknown Album".
//   - Artist: the ALBUMARTIST tag most tracks have; if no track has one,
//     the ARTIST tag most tracks have; if no track has one, "Unknown
//     Artist". Two that as many tracks have are decided as for the title.
//   - Year: the valid year most tracks have; of two, the lower. Without
//     any, 0.
//   - Genre: the genre most tracks have; of two, the first in the order of
//     their bytes. Without any, "".
//   - Compilation: true if any track says so.
//
// Every text is compared, and returned, as names.Normalize makes it. The
// result does not depend on the order of the tracks: where the rules above
// leave two values, the first in the order of their bytes wins. It reads
// only its argument.
func DeriveAlbum(tracks []TrackTags) AlbumMeta {
	meta := AlbumMeta{
		Title: cmp.Or(
			mostCommon(tracks, func(t TrackTags) string { return t.Album }, true),
			unknownAlbum),
		Artist: cmp.Or(
			mostCommon(tracks, func(t TrackTags) string { return t.AlbumArtist }, true),
			mostCommon(tracks, func(t TrackTags) string { return t.Artist }, true),
			unknownArtist),
		Year:  mostCommonYear(tracks),
		Genre: mostCommon(tracks, func(t TrackTags) string { return t.Genre }, false),
	}
	for _, t := range tracks {
		meta.Compilation = meta.Compilation || t.Compilation
	}
	return meta
}

// mostCommon returns the value of a tag that most tracks have, "" when no
// track has the tag. Of two values that as many tracks have, the winner is
// the one of the track that comes first by (disc, no) if byPlace is true,
// and then the first in the order of their bytes.
func mostCommon(tracks []TrackTags, tag func(TrackTags) string, byPlace bool) string {
	type tally struct {
		count    int
		disc, no int // the first place of a track with the value
	}
	tallies := map[string]tally{}
	for _, t := range tracks {
		value := names.Normalize(tag(t))
		if value == "" {
			continue
		}
		seen, ok := tallies[value]
		if !ok || cmp.Or(cmp.Compare(t.Disc, seen.disc), cmp.Compare(t.No, seen.no)) < 0 {
			seen.disc, seen.no = t.Disc, t.No
		}
		seen.count++
		tallies[value] = seen
	}
	// before is a strict order of the values, so the winner does not depend
	// on the order in which the map gives them.
	before := func(a, b string) bool {
		ta, tb := tallies[a], tallies[b]
		order := cmp.Compare(tb.count, ta.count)
		if byPlace {
			order = cmp.Or(order, cmp.Compare(ta.disc, tb.disc), cmp.Compare(ta.no, tb.no))
		}
		return cmp.Or(order, cmp.Compare(a, b)) < 0
	}
	best := ""
	for value := range tallies {
		if best == "" || before(value, best) {
			best = value
		}
	}
	return best
}

// mostCommonYear returns the valid year that most tracks have, the lower
// of two that as many tracks have, and 0 when no track has a valid year.
func mostCommonYear(tracks []TrackTags) int {
	counts := map[int]int{}
	for _, t := range tracks {
		if t.Year >= 1 && t.Year <= 9999 {
			counts[t.Year]++
		}
	}
	best := 0
	for year, count := range counts {
		if best == 0 || count > counts[best] || (count == counts[best] && year < best) {
			best = year
		}
	}
	return best
}

package catalog

import "time"

// What the catalog shows of artists, albums and tracks (DESIGN.md §8.2).
// Nothing here is a path: the files of the library are the business of the
// media endpoints. A nullable value of the API is a pointer, nil when it
// has none.

// ArtistRef names an artist.
type ArtistRef struct {
	ID   string
	Name string
}

// ArtistSummary is an artist of the list, with how many available albums
// it has.
type ArtistSummary struct {
	ArtistRef
	AlbumCount int
}

// ArtistDetail is an artist with its available albums.
type ArtistDetail struct {
	ArtistRef
	Albums []Album
}

// Album is an available album as the lists show it (AlbumSummary).
type Album struct {
	ID          string
	Title       string
	Artist      ArtistRef
	Year        *int
	Genre       *string
	Compilation bool
	// TrackCount and DurationMS count the available tracks.
	TrackCount int
	DurationMS int64
	// CoverHash is the SHA-256 of the cover, "" when the album has none.
	CoverHash string
	// AddedAt is when Vibrance first saw the album.
	AddedAt time.Time
}

// AlbumDetail is an album with its available tracks, ordered by disc and
// number.
type AlbumDetail struct {
	Album
	// DiscCount is how many discs the available tracks are on.
	DiscCount int
	Tracks    []Track
}

// AlbumRef is the album a track belongs to, with the last data known when
// it is not available.
type AlbumRef struct {
	ID        string
	Title     string
	Artist    ArtistRef
	Year      *int
	CoverHash string
}

// Track is a track, available or not.
type Track struct {
	ID         string
	Title      string
	Artist     string
	Album      AlbumRef
	Disc       int
	Number     int
	DurationMS *int64
	Genre      *string
	Format     Format
	HasLyrics  bool
	// ReplayGain is nil when the file has no ReplayGain tag at all.
	ReplayGain *ReplayGain
	Available  bool
	// Favorite says whether the track is a favorite of the user the track
	// was read for.
	Favorite bool
}

// Format is the audio file of a track.
type Format struct {
	Codec      string
	SampleRate int
	Channels   int
	BitDepth   *int
	Bitrate    *int
	Size       int64
}

// ReplayGain is the ReplayGain of a track; each value is nil when the file
// does not have it.
type ReplayGain struct {
	TrackGainDB *float64
	TrackPeak   *float64
	AlbumGainDB *float64
	AlbumPeak   *float64
}

// ArtistPage is a page of the list of the artists. Next is the cursor of
// the page after it, "" on the last page.
type ArtistPage struct {
	Artists []ArtistSummary
	Next    string
}

// AlbumPage is a page of the list of the albums. Next is the cursor of the
// page after it, "" on the last page.
type AlbumPage struct {
	Albums []Album
	Next   string
}

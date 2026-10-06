package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The codes of the refusals of the catalog (DESIGN.md §8.4).
const (
	CodeArtistNotFound = "artist_not_found"
	CodeAlbumNotFound  = "album_not_found"
	CodeTrackNotFound  = "track_not_found"
)

// Service reads the catalog: the artists, albums and tracks the scanner
// indexed (DESIGN.md §8.3), each operation in one read transaction, so what
// it returns is one state of the index. It writes nothing of the index: its
// only writes are the favorites and the playlists of the users.
type Service struct {
	store *store.Store
	now   func() time.Time
}

// New returns the catalog of the index in st; now is the clock, which dates
// the favorites.
func New(st *store.Store, now func() time.Time) *Service {
	return &Service{store: st, now: now}
}

func artistNotFound() *httpx.Error {
	return &httpx.Error{Status: http.StatusNotFound, Code: CodeArtistNotFound, Message: "There is no such artist."}
}

func albumNotFound() *httpx.Error {
	return &httpx.Error{Status: http.StatusNotFound, Code: CodeAlbumNotFound, Message: "There is no such album."}
}

func trackNotFound() *httpx.Error {
	return &httpx.Error{Status: http.StatusNotFound, Code: CodeTrackNotFound, Message: "There is no such track."}
}

// noRows tells whether err says that a row does not exist. Once ctx is over
// a query that was cut short may look like one that found nothing, so it
// is not believed then.
func noRows(ctx context.Context, err error) bool {
	return ctx.Err() == nil && errors.Is(err, sql.ErrNoRows)
}

// GetArtist returns an artist with its available albums, in the order of
// the list by artist: by year, then title (§8.5). An artist that does not
// exist, or has no available album, is 404 artist_not_found: the lists and
// the search leave it out too.
func (s *Service) GetArtist(ctx context.Context, id string) (ArtistDetail, error) {
	var (
		artist store.Artist
		rows   []store.ListAlbumsOfArtistRow
	)
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		if artist, err = q.GetArtist(ctx, id); err != nil {
			return err
		}
		rows, err = q.ListAlbumsOfArtist(ctx, id)
		return err
	})
	switch {
	case noRows(ctx, err):
		return ArtistDetail{}, artistNotFound()
	case err != nil:
		return ArtistDetail{}, fmt.Errorf("catalog: reading an artist: %w", err)
	case len(rows) == 0:
		return ArtistDetail{}, artistNotFound()
	}
	d := ArtistDetail{ArtistRef: ArtistRef{ID: artist.ID, Name: artist.Name}, Albums: make([]Album, 0, len(rows))}
	for _, r := range rows {
		d.Albums = append(d.Albums, albumOf(r.Album, r.ArtistName))
	}
	return d, nil
}

// GetAlbum returns an available album with its available tracks, ordered by
// disc and number, each with whether it is a favorite of userID. An album
// that does not exist or is not available is 404 album_not_found (§8.2).
func (s *Service) GetAlbum(ctx context.Context, userID, id string) (AlbumDetail, error) {
	var (
		album  store.GetAvailableAlbumRow
		tracks []store.ListAvailableTracksOfAlbumRow
	)
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		if album, err = q.GetAvailableAlbum(ctx, id); err != nil {
			return err
		}
		tracks, err = q.ListAvailableTracksOfAlbum(ctx, store.ListAvailableTracksOfAlbumParams{UserID: userID, AlbumID: id})
		return err
	})
	switch {
	case noRows(ctx, err):
		return AlbumDetail{}, albumNotFound()
	case err != nil:
		return AlbumDetail{}, fmt.Errorf("catalog: reading an album: %w", err)
	}
	d := AlbumDetail{Album: albumOf(album.Album, album.ArtistName), Tracks: make([]Track, 0, len(tracks))}
	ref := albumRefOf(d.Album)
	discs := map[int64]bool{}
	for _, t := range tracks {
		d.Tracks = append(d.Tracks, trackOf(t.Track, ref, t.Favorite))
		discs[t.Track.Disc] = true
	}
	d.DiscCount = len(discs)
	return d, nil
}

// GetTrack returns a track, available or not, with whether it is a favorite
// of userID. A track that does not exist is 404 track_not_found; one that
// is not available is answered with the last data known (§8.2).
func (s *Service) GetTrack(ctx context.Context, userID, id string) (Track, error) {
	var row store.GetTrackWithAlbumRow
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		row, err = q.GetTrackWithAlbum(ctx, store.GetTrackWithAlbumParams{UserID: userID, ID: id})
		return err
	})
	switch {
	case noRows(ctx, err):
		return Track{}, trackNotFound()
	case err != nil:
		return Track{}, fmt.Errorf("catalog: reading a track: %w", err)
	}
	return trackInAlbum(trackRow(row)), nil
}

// trackRow is a track with what the API shows of its album and whether it
// is a favorite of the user it was read for, whichever query read it.
type trackRow struct {
	Track            store.Track
	AlbumTitle       string
	AlbumYear        sql.NullInt64
	AlbumCoverSha256 sql.NullString
	AlbumArtistID    string
	AlbumArtistName  string
	Favorite         bool
}

// trackInAlbum is what the API shows of a track that was read with its
// album: the one mapping of a track of the details, of the search and of
// the favorites.
func trackInAlbum(r trackRow) Track {
	ref := AlbumRef{ID: r.Track.AlbumID, Title: r.AlbumTitle, Year: intOf(r.AlbumYear),
		Artist: ArtistRef{ID: r.AlbumArtistID, Name: r.AlbumArtistName}, CoverHash: r.AlbumCoverSha256.String}
	return trackOf(r.Track, ref, r.Favorite)
}

// albumOf is what the API shows of an album row.
func albumOf(a store.Album, artistName string) Album {
	return Album{
		ID:          a.ID,
		Title:       a.Title,
		Artist:      ArtistRef{ID: a.ArtistID, Name: artistName},
		Year:        intOf(a.Year),
		Genre:       stringOf(a.Genre),
		Compilation: a.Compilation != 0,
		TrackCount:  int(a.TrackCount),
		DurationMS:  a.DurationMs,
		CoverHash:   a.CoverSha256.String,
		AddedAt:     time.UnixMilli(a.FirstSeenAt).UTC(),
	}
}

func albumRefOf(a Album) AlbumRef {
	return AlbumRef{ID: a.ID, Title: a.Title, Artist: a.Artist, Year: a.Year, CoverHash: a.CoverHash}
}

// trackOf is what the API shows of a track row, in its album.
func trackOf(t store.Track, album AlbumRef, favorite bool) Track {
	track := Track{
		ID:         t.ID,
		Title:      t.Title,
		Artist:     t.Artist,
		Album:      album,
		Disc:       int(t.Disc),
		Number:     int(t.No),
		DurationMS: int64Of(t.DurationMs),
		Genre:      stringOf(t.Genre),
		Format: Format{Codec: t.Codec, SampleRate: int(t.SampleRate), Channels: int(t.Channels),
			BitDepth: intOf(t.BitDepth), Bitrate: intOf(t.Bitrate), Size: t.FileSize},
		HasLyrics: t.LyricsRel.Valid,
		Available: t.Available != 0,
		Favorite:  favorite,
	}
	if t.RgTrackGain.Valid || t.RgTrackPeak.Valid || t.RgAlbumGain.Valid || t.RgAlbumPeak.Valid {
		track.ReplayGain = &ReplayGain{TrackGainDB: floatOf(t.RgTrackGain), TrackPeak: floatOf(t.RgTrackPeak),
			AlbumGainDB: floatOf(t.RgAlbumGain), AlbumPeak: floatOf(t.RgAlbumPeak)}
	}
	return track
}

func intOf(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

func int64Of(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func stringOf(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func floatOf(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// CatalogSummary is how much music is available
// (docs/proposals/web-client-api.md A3).
type CatalogSummary struct {
	// Artists counts the artists that have an available album, the ones
	// the list of the artists shows.
	Artists int
	Albums  int
	Tracks  int
	// DurationMS adds up the known durations of the available tracks.
	DurationMS int64
}

// GetCatalogSummary counts the available artists, albums and tracks, and
// adds up the known durations of the tracks. What is not available is not
// counted.
func (s *Service) GetCatalogSummary(ctx context.Context) (CatalogSummary, error) {
	var row store.GetCatalogSummaryRow
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		row, err = q.GetCatalogSummary(ctx)
		return err
	})
	if err != nil {
		return CatalogSummary{}, fmt.Errorf("catalog: counting the catalog: %w", err)
	}
	return CatalogSummary{Artists: int(row.Artists), Albums: int(row.Albums), Tracks: int(row.Tracks),
		DurationMS: row.DurationMs}, nil
}

package api

import (
	"context"

	"vibrance/internal/catalog"
)

// The operations of the catalog: artists, albums and tracks (DESIGN.md
// §8.3). What they read and refuse is catalog.Service's; these only
// translate.

// defaultLimit is the size of a page when the client does not say (§8.1).
const defaultLimit = 50

// ListArtists lists the artists that have an available album.
func (s Server) ListArtists(ctx context.Context, req ListArtistsRequestObject) (ListArtistsResponseObject, error) {
	page, err := s.catalog.Load().ListArtists(ctx, limitOf(req.Params.Limit), req.Params.After)
	if err != nil {
		return nil, err
	}
	body := ArtistList{Artists: make([]ArtistSummary, 0, len(page.Artists)), Next: nextOf(page.Next)}
	for _, a := range page.Artists {
		body.Artists = append(body.Artists, ArtistSummary{Id: a.ID, Name: a.Name, AlbumCount: a.AlbumCount})
	}
	return ListArtists200JSONResponse{Body: body}, nil
}

// GetArtist returns an artist with its available albums.
func (s Server) GetArtist(ctx context.Context, req GetArtistRequestObject) (GetArtistResponseObject, error) {
	a, err := s.catalog.Load().GetArtist(ctx, req.Id.String())
	if err != nil {
		return nil, err
	}
	body := ArtistDetail{Id: a.ID, Name: a.Name, Albums: make([]AlbumSummary, 0, len(a.Albums))}
	for _, album := range a.Albums {
		body.Albums = append(body.Albums, albumSummaryOf(album))
	}
	return GetArtist200JSONResponse{Body: body}, nil
}

// ListAlbums lists the available albums in the order asked for.
func (s Server) ListAlbums(ctx context.Context, req ListAlbumsRequestObject) (ListAlbumsResponseObject, error) {
	q := catalog.AlbumQuery{Sort: catalog.SortTitle, Order: catalog.OrderAsc, Limit: limitOf(req.Params.Limit),
		After: req.Params.After}
	if req.Params.Sort != nil {
		q.Sort = string(*req.Params.Sort)
	}
	if req.Params.Order != nil {
		q.Order = string(*req.Params.Order)
	}
	if req.Params.Artist != nil {
		q.ArtistID = req.Params.Artist.String()
	}
	page, err := s.catalog.Load().ListAlbums(ctx, q)
	if err != nil {
		return nil, err
	}
	body := AlbumList{Albums: make([]AlbumSummary, 0, len(page.Albums)), Next: nextOf(page.Next)}
	for _, a := range page.Albums {
		body.Albums = append(body.Albums, albumSummaryOf(a))
	}
	return ListAlbums200JSONResponse{Body: body}, nil
}

// GetAlbum returns an available album with its available tracks.
func (s Server) GetAlbum(ctx context.Context, req GetAlbumRequestObject) (GetAlbumResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.catalog.Load().GetAlbum(ctx, p.UserID, req.Id.String())
	if err != nil {
		return nil, err
	}
	sum := albumSummaryOf(a.Album)
	body := AlbumDetail{Id: sum.Id, Title: sum.Title, Artist: sum.Artist, Year: sum.Year, Genre: sum.Genre,
		Compilation: sum.Compilation, TrackCount: sum.TrackCount, DurationMs: sum.DurationMs, Cover: sum.Cover,
		AddedAt: sum.AddedAt, DiscCount: a.DiscCount, Tracks: make([]Track, 0, len(a.Tracks))}
	for _, t := range a.Tracks {
		body.Tracks = append(body.Tracks, trackOf(t))
	}
	return GetAlbum200JSONResponse{Body: body}, nil
}

// ListTracks lists the available tracks in the order asked for.
func (s Server) ListTracks(ctx context.Context, req ListTracksRequestObject) (ListTracksResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	q := catalog.TrackQuery{UserID: p.UserID, Sort: catalog.SortTitle, Order: catalog.OrderAsc,
		Limit: limitOf(req.Params.Limit), After: req.Params.After}
	if req.Params.Sort != nil {
		q.Sort = string(*req.Params.Sort)
	}
	if req.Params.Order != nil {
		q.Order = string(*req.Params.Order)
	}
	if req.Params.Artist != nil {
		q.ArtistID = req.Params.Artist.String()
	}
	page, err := s.catalog.Load().ListTracks(ctx, q)
	if err != nil {
		return nil, err
	}
	body := TrackList{Tracks: make([]Track, 0, len(page.Tracks)), Next: nextOf(page.Next)}
	for _, t := range page.Tracks {
		body.Tracks = append(body.Tracks, trackOf(t))
	}
	return ListTracks200JSONResponse{Body: body}, nil
}

// GetTrack returns a track, available or not.
func (s Server) GetTrack(ctx context.Context, req GetTrackRequestObject) (GetTrackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.catalog.Load().GetTrack(ctx, p.UserID, req.Id.String())
	if err != nil {
		return nil, err
	}
	return GetTrack200JSONResponse{Body: trackOf(t)}, nil
}

func limitOf(limit *Limit) int {
	if limit == nil {
		return defaultLimit
	}
	return *limit
}

// nextOf is the `next` of a page: null on the last page.
func nextOf(next string) *Cursor {
	if next == "" {
		return nil
	}
	return &next
}

// coverOf is the cover of an album whose cover file has that SHA-256, or
// nil: the URL carries the hash, so that the image can be cached for ever
// and a new cover has a new URL (D12, §9.2).
func coverOf(albumID, hash string) *Cover {
	if hash == "" {
		return nil
	}
	return &Cover{Hash: hash, Url: BasePath + "/albums/" + albumID + "/cover?v=" + hash}
}

func artistRefOf(a catalog.ArtistRef) ArtistRef { return ArtistRef{Id: a.ID, Name: a.Name} }

func albumSummaryOf(a catalog.Album) AlbumSummary {
	return AlbumSummary{Id: a.ID, Title: a.Title, Artist: artistRefOf(a.Artist), Year: a.Year, Genre: a.Genre,
		Compilation: a.Compilation, TrackCount: a.TrackCount, DurationMs: a.DurationMS, Cover: coverOf(a.ID, a.CoverHash),
		AddedAt: timestamp(a.AddedAt)}
}

func trackOf(t catalog.Track) Track {
	track := Track{
		Id:     t.ID,
		Title:  t.Title,
		Artist: t.Artist,
		Album: AlbumRef{Id: t.Album.ID, Title: t.Album.Title, Artist: artistRefOf(t.Album.Artist), Year: t.Album.Year,
			Cover: coverOf(t.Album.ID, t.Album.CoverHash)},
		Disc:       t.Disc,
		Number:     t.Number,
		DurationMs: t.DurationMS,
		Genre:      t.Genre,
		Format: AudioFormat{Codec: AudioFormatCodec(t.Format.Codec), SampleRate: t.Format.SampleRate,
			Channels: t.Format.Channels, BitDepth: t.Format.BitDepth, Bitrate: t.Format.Bitrate, Size: t.Format.Size},
		HasLyrics: t.HasLyrics,
		Available: t.Available,
		Favorite:  t.Favorite,
	}
	if rg := t.ReplayGain; rg != nil {
		track.ReplayGain = &ReplayGain{TrackGainDb: rg.TrackGainDB, TrackPeak: rg.TrackPeak, AlbumGainDb: rg.AlbumGainDB,
			AlbumPeak: rg.AlbumPeak}
	}
	return track
}

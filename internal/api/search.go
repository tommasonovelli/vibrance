package api

import (
	"context"
	"slices"

	"vibrance/internal/search"
)

// defaultSearchLimit is how many results of each kind a search returns
// when the client does not say (DESIGN.md §10.2).
const defaultSearchLimit = 10

// Search finds artists, albums and tracks by their words. The three lists
// are always in the answer; a kind that was not asked for is empty.
func (s Server) Search(ctx context.Context, req SearchRequestObject) (SearchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	kinds := search.Kinds{Artists: true, Albums: true, Tracks: true}
	if types := req.Params.Types; types != nil {
		kinds = search.Kinds{Artists: slices.Contains(*types, SearchParamsTypesArtist),
			Albums: slices.Contains(*types, SearchParamsTypesAlbum), Tracks: slices.Contains(*types, SearchParamsTypesTrack)}
	}
	limit := defaultSearchLimit
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	found, err := s.catalog.Load().Search(ctx, p.UserID, req.Params.Q, kinds, limit)
	if err != nil {
		return nil, err
	}
	body := SearchResult{Artists: make([]ArtistSummary, 0, len(found.Artists)), Albums: make([]AlbumSummary, 0, len(found.Albums)),
		Tracks: make([]Track, 0, len(found.Tracks))}
	for _, a := range found.Artists {
		body.Artists = append(body.Artists, ArtistSummary{Id: a.ID, Name: a.Name, AlbumCount: a.AlbumCount})
	}
	for _, a := range found.Albums {
		body.Albums = append(body.Albums, albumSummaryOf(a))
	}
	for _, t := range found.Tracks {
		body.Tracks = append(body.Tracks, trackOf(t))
	}
	return Search200JSONResponse{Body: body}, nil
}

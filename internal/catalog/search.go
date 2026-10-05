package catalog

import (
	"context"
	"fmt"

	"vibrance/internal/search"
	"vibrance/internal/store"
)

// SearchResults are what a search found of each kind, the best first. A
// kind that was not asked for is empty.
type SearchResults struct {
	Artists []ArtistSummary
	Albums  []Album
	Tracks  []Track
}

// Search finds the artists, albums and tracks that have every word of text
// (DESIGN.md §10.2), at most limit of each of the kinds asked for, with
// whether each track is a favorite of userID. Only what is available is
// found. A text in which the tokenizer of the index finds no word
// (punctuation alone) finds nothing, and is not an error.
//
// The full-text tables and the rows they describe are read in one
// transaction, so a result is never a row the scanner took away meanwhile.
func (s *Service) Search(ctx context.Context, userID, text string, kinds search.Kinds, limit int) (SearchResults, error) {
	query := search.Parse(text)
	var found search.Results
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		found, err = search.Find(ctx, q, userID, query, kinds, limit)
		return err
	})
	if err != nil {
		return SearchResults{}, fmt.Errorf("catalog: searching: %w", err)
	}
	res := SearchResults{Artists: make([]ArtistSummary, 0, len(found.Artists)), Albums: make([]Album, 0, len(found.Albums)),
		Tracks: make([]Track, 0, len(found.Tracks))}
	for _, r := range found.Artists {
		res.Artists = append(res.Artists, ArtistSummary{ArtistRef: ArtistRef{ID: r.Artist.ID, Name: r.Artist.Name},
			AlbumCount: int(r.AlbumCount)})
	}
	for _, r := range found.Albums {
		res.Albums = append(res.Albums, albumOf(r.Album, r.ArtistName))
	}
	for _, r := range found.Tracks {
		res.Tracks = append(res.Tracks, trackInAlbum(trackRow(r)))
	}
	return res, nil
}

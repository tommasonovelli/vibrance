package api

import "context"

// The operations of the favorites (DESIGN.md §8.3). They are those of the
// user of the request and of nobody else: no operation names another user.

// ListFavoriteTracks lists the favorite tracks of the user, the most recent
// first.
func (s Server) ListFavoriteTracks(ctx context.Context, req ListFavoriteTracksRequestObject) (ListFavoriteTracksResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	page, err := s.catalog.Load().ListFavorites(ctx, p.UserID, limitOf(req.Params.Limit), req.Params.After)
	if err != nil {
		return nil, err
	}
	body := FavoriteList{Favorites: make([]Favorite, 0, len(page.Favorites)), Next: nextOf(page.Next)}
	for _, f := range page.Favorites {
		body.Favorites = append(body.Favorites, Favorite{FavoritedAt: timestamp(f.FavoritedAt), Track: trackOf(f.Track)})
	}
	return ListFavoriteTracks200JSONResponse{Body: body}, nil
}

// AddFavoriteTrack makes a track a favorite of the user.
func (s Server) AddFavoriteTrack(ctx context.Context, req AddFavoriteTrackRequestObject) (AddFavoriteTrackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.catalog.Load().AddFavorite(ctx, p.UserID, req.Id.String()); err != nil {
		return nil, err
	}
	return AddFavoriteTrack204Response{}, nil
}

// RemoveFavoriteTrack makes a track no longer a favorite of the user.
func (s Server) RemoveFavoriteTrack(ctx context.Context, req RemoveFavoriteTrackRequestObject) (RemoveFavoriteTrackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.catalog.Load().RemoveFavorite(ctx, p.UserID, req.Id.String()); err != nil {
		return nil, err
	}
	return RemoveFavoriteTrack204Response{}, nil
}

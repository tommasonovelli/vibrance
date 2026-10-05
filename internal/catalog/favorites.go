package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// sortFavorited is the order of the list of the favorites, in its cursors:
// it is not a parameter, and differs from every order of the other lists,
// so that a cursor of one list is refused by the others.
const sortFavorited = "favorited"

// Favorite is a favorite track of a user.
type Favorite struct {
	// FavoritedAt is when the user made the track a favorite.
	FavoritedAt time.Time
	Track       Track
}

// FavoritePage is a page of the list of the favorites. Next is the cursor
// of the page after it, "" on the last page.
type FavoritePage struct {
	Favorites []Favorite
	Next      string
}

// AddFavorite makes a track, available or not, a favorite of userID
// (DESIGN.md §8.3). It is idempotent: a track that is a favorite already
// stays one since the moment it had. A track that does not exist is 404
// track_not_found.
func (s *Service) AddFavorite(ctx context.Context, userID, trackID string) error {
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		if err := knownTrack(ctx, q, trackID); err != nil {
			return err
		}
		return q.AddFavorite(ctx, store.AddFavoriteParams{UserID: userID, TrackID: trackID, CreatedAt: s.now().UnixMilli()})
	})
	return favoriteFailure("adding a favorite", err)
}

// RemoveFavorite makes a track no longer a favorite of userID. It is
// idempotent: a track that is not a favorite changes nothing. A track that
// does not exist is 404 track_not_found.
func (s *Service) RemoveFavorite(ctx context.Context, userID, trackID string) error {
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		if err := knownTrack(ctx, q, trackID); err != nil {
			return err
		}
		return q.RemoveFavorite(ctx, store.RemoveFavoriteParams{UserID: userID, TrackID: trackID})
	})
	return favoriteFailure("removing a favorite", err)
}

// knownTrack refuses, in the transaction q, a track that does not exist.
func knownTrack(ctx context.Context, q *store.Queries, id string) error {
	found, err := q.TrackExists(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return trackNotFound()
	}
	return nil
}

// favoriteFailure is the error of a change of the favorites: a refusal as
// it is, anything else with what was being done.
func favoriteFailure(doing string, err error) error {
	var refusal *httpx.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refusal):
		return refusal
	}
	return fmt.Errorf("catalog: %s: %w", doing, err)
}

// ListFavorites returns a page of the favorites of userID, the most recent
// first: by (created_at, track_id), reversed (§8.5). A track that is no
// longer available stays in the list, with the last data known. after is
// the cursor of the page before, nil for the first page; one that is not a
// cursor of this list, the empty string included, is 400 invalid_cursor.
func (s *Service) ListFavorites(ctx context.Context, userID string, limit int, after *string) (FavoritePage, error) {
	var (
		rows []store.ListFavoritesRow
		err  error
	)
	n := int64(limit) + 1
	if after == nil {
		err = s.store.Read(ctx, func(q *store.Queries) error {
			rows, err = favoriteRows(q.ListFavorites(ctx, store.ListFavoritesParams{UserID: userID, PageSize: n}))
			return err
		})
	} else {
		keys, cerr := httpx.DecodeCursor(*after, sortFavorited, OrderDesc, httpx.CursorInt, httpx.CursorID)
		if cerr != nil {
			return FavoritePage{}, cerr
		}
		err = s.store.Read(ctx, func(q *store.Queries) error {
			rows, err = favoriteRows(q.ListFavoritesAfter(ctx, store.ListFavoritesAfterParams{UserID: userID,
				CreatedAt: keys[0].Int(), AfterID: keys[1].ID(), PageSize: n}))
			return err
		})
	}
	if err != nil {
		return FavoritePage{}, fmt.Errorf("catalog: listing the favorites: %w", err)
	}
	rows, more := cut(rows, limit)
	page := FavoritePage{Favorites: make([]Favorite, 0, len(rows))}
	for _, r := range rows {
		track := trackInAlbum(trackRow{Track: r.Track, AlbumTitle: r.AlbumTitle, AlbumYear: r.AlbumYear,
			AlbumCoverSha256: r.AlbumCoverSha256, AlbumArtistID: r.AlbumArtistID, AlbumArtistName: r.AlbumArtistName,
			Favorite: r.Favorite})
		page.Favorites = append(page.Favorites, Favorite{FavoritedAt: time.UnixMilli(r.FavoritedAt).UTC(), Track: track})
	}
	if more {
		last := rows[len(rows)-1]
		if page.Next, err = httpx.EncodeCursor(sortFavorited, OrderDesc, httpx.IntKey(last.FavoritedAt), httpx.IDKey(last.Track.ID)); err != nil {
			return FavoritePage{}, err
		}
	}
	return page, nil
}

// favoriteRows converts the rows of the list of the favorites, which have
// one shape and a type of their own for each query of sqlc.
func favoriteRows[R ~struct {
	FavoritedAt      int64
	Track            store.Track
	AlbumTitle       string
	AlbumYear        sql.NullInt64
	AlbumCoverSha256 sql.NullString
	AlbumArtistID    string
	AlbumArtistName  string
	Favorite         bool
}](rows []R, err error) ([]store.ListFavoritesRow, error) {
	if err != nil {
		return nil, err
	}
	out := make([]store.ListFavoritesRow, len(rows))
	for i, r := range rows {
		out[i] = store.ListFavoritesRow(r)
	}
	return out, nil
}

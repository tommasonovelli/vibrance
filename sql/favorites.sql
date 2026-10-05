-- name: TrackExists :one
-- TrackExists tells whether there is a track with that id, available or
-- not. The rows of tracks are never deleted (I3), so a track that exists
-- once exists for ever.
SELECT EXISTS (SELECT 1 FROM tracks WHERE tracks.id = sqlc.arg(id)) AS found;

-- name: AddFavorite :exec
-- AddFavorite makes a track a favorite of a user since that moment. A track
-- that is one already keeps the row it has, with its moment (DESIGN.md
-- 8.3: idempotent). The row is read from users, so that an account deleted
-- since its request was authenticated adds nothing instead of failing on
-- the foreign key. The caller checks the track with TrackExists in the same
-- transaction.
INSERT INTO favorites (user_id, track_id, created_at)
SELECT users.id, CAST(sqlc.arg(track_id) AS text), CAST(sqlc.arg(created_at) AS integer)
FROM users
WHERE users.id = sqlc.arg(user_id)
ON CONFLICT (user_id, track_id) DO NOTHING;

-- name: RemoveFavorite :exec
-- RemoveFavorite makes a track no longer a favorite of a user. A track that
-- is not one changes nothing.
DELETE FROM favorites WHERE user_id = sqlc.arg(user_id) AND track_id = sqlc.arg(track_id);

-- name: ListFavorites :many
-- ListFavorites returns the first page of the favorites of a user, the most
-- recent first: by (created_at, track_id), reversed (DESIGN.md 8.5), on the
-- index favorites_user_created_idx. A track that is not available is
-- listed, with what the API shows of its album, like GetTrackWithAlbum.
-- favorite is computed as in every other query that returns a track, not
-- taken for granted.
SELECT favorites.created_at AS favorited_at, sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites AS mine
            WHERE mine.user_id = sqlc.arg(user_id) AND mine.track_id = tracks.id) AS favorite
FROM favorites
JOIN tracks ON tracks.id = favorites.track_id
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE favorites.user_id = sqlc.arg(user_id)
ORDER BY favorites.created_at DESC, favorites.track_id DESC
LIMIT sqlc.arg(page_size);

-- name: ListFavoritesAfter :many
-- ListFavoritesAfter returns the page of ListFavorites after the favorite
-- with that key. The values of the cursor are cast to the type of their
-- column, as in the lists of the catalog.
SELECT favorites.created_at AS favorited_at, sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites AS mine
            WHERE mine.user_id = sqlc.arg(user_id) AND mine.track_id = tracks.id) AS favorite
FROM favorites
JOIN tracks ON tracks.id = favorites.track_id
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE favorites.user_id = sqlc.arg(user_id)
  AND (favorites.created_at, favorites.track_id) < (CAST(sqlc.arg(created_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY favorites.created_at DESC, favorites.track_id DESC
LIMIT sqlc.arg(page_size);

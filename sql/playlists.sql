-- name: GetPlaylistOfUser :one
-- GetPlaylistOfUser returns a playlist of a user with what is computed from
-- its items: item_count counts every item, and duration_ms adds up only the
-- tracks that are available (DESIGN.md, erratum of 2026-10-04). A playlist
-- that does not exist, or is of another user, is sql.ErrNoRows.
--
-- A playlist is read and changed only through this query,
-- GetPlaylistStateOfUser and ListPlaylistsOfUser, which name its owner: the
-- playlist of another user is a row that is not found (DESIGN.md 8.6, I6).
-- The other queries of this file run in the transaction that read the
-- playlist that way.
SELECT sqlc.embed(playlists),
    (SELECT count(*) FROM playlist_items
     WHERE playlist_items.playlist_id = playlists.id) AS item_count,
    CAST((SELECT coalesce(sum(tracks.duration_ms), 0)
          FROM playlist_items
          JOIN tracks ON tracks.id = playlist_items.track_id
          WHERE playlist_items.playlist_id = playlists.id AND tracks.available = 1) AS INTEGER) AS duration_ms
FROM playlists
WHERE playlists.id = sqlc.arg(id) AND playlists.user_id = sqlc.arg(user_id);

-- name: GetPlaylistStateOfUser :one
-- GetPlaylistStateOfUser returns a playlist of a user and how many items it
-- has, without the duration of GetPlaylistOfUser, which reads the track of
-- every item: what a change needs of the playlist before it changes it, and
-- what a page of its items needs, the revision. A playlist that does not
-- exist, or is of another user, is sql.ErrNoRows.
SELECT sqlc.embed(playlists),
    (SELECT count(*) FROM playlist_items
     WHERE playlist_items.playlist_id = playlists.id) AS item_count
FROM playlists
WHERE playlists.id = sqlc.arg(id) AND playlists.user_id = sqlc.arg(user_id);

-- name: ListPlaylistsOfUser :many
-- ListPlaylistsOfUser returns every playlist of a user as GetPlaylistOfUser
-- does, the oldest first. A user has at most 500.
SELECT sqlc.embed(playlists),
    (SELECT count(*) FROM playlist_items
     WHERE playlist_items.playlist_id = playlists.id) AS item_count,
    CAST((SELECT coalesce(sum(tracks.duration_ms), 0)
          FROM playlist_items
          JOIN tracks ON tracks.id = playlist_items.track_id
          WHERE playlist_items.playlist_id = playlists.id AND tracks.available = 1) AS INTEGER) AS duration_ms
FROM playlists
WHERE playlists.user_id = sqlc.arg(user_id)
ORDER BY playlists.created_at, playlists.id;

-- name: ListPlaylistRefsOfUserWithTrack :many
-- ListPlaylistRefsOfUserWithTrack returns the id and the name of every
-- playlist of a user that has at least one item with a track, the oldest
-- first, as ListPlaylistsOfUser (GET /tracks/{id}/playlists,
-- docs/proposals/web-client-api.md B4). It names the owner, like the three
-- queries above: the playlists of another user are never listed (DESIGN.md
-- 8.6, I6). The items of the track, and so their playlists, are read from
-- playlist_items_track_idx, and each playlist by its key: a track is in a
-- few playlists, and the playlists of every user are not read.
SELECT playlists.id, playlists.name
FROM playlists
WHERE playlists.id IN (SELECT playlist_items.playlist_id FROM playlist_items
                       WHERE playlist_items.track_id = sqlc.arg(track_id))
  AND playlists.user_id = sqlc.arg(user_id)
ORDER BY playlists.created_at, playlists.id;

-- name: CountPlaylistsOfUser :one
-- CountPlaylistsOfUser counts the playlists of a user, for the limit of
-- DESIGN.md 5.2.
SELECT count(*) FROM playlists WHERE user_id = ?;

-- name: CreatePlaylist :execrows
-- CreatePlaylist writes an empty playlist of a user, at its first revision.
-- The owner is read from users, so that an account deleted since its request
-- was authenticated creates nothing (no row is written) instead of failing
-- on the foreign key.
INSERT INTO playlists (id, user_id, name, description, revision, created_at, updated_at)
SELECT CAST(sqlc.arg(id) AS text), users.id, CAST(sqlc.arg(name) AS text), CAST(sqlc.arg(description) AS text), 1,
    CAST(sqlc.arg(created_at) AS integer), CAST(sqlc.arg(created_at) AS integer)
FROM users
WHERE users.id = sqlc.arg(user_id);

-- name: RenamePlaylist :exec
-- RenamePlaylist gives a playlist its name and its description, and a new
-- revision (DESIGN.md 8.6). A change of the items uses TouchPlaylist.
UPDATE playlists
SET name = sqlc.arg(name), description = sqlc.arg(description), revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeletePlaylist :exec
-- DeletePlaylist deletes a playlist; its items go with it (ON DELETE
-- CASCADE).
DELETE FROM playlists WHERE id = ?;

-- name: GetPlaylistItemPosition :one
-- GetPlaylistItemPosition returns where an item of a playlist is. An item
-- that the playlist does not have, one of another playlist included, is
-- sql.ErrNoRows.
SELECT position FROM playlist_items WHERE id = sqlc.arg(id) AND playlist_id = sqlc.arg(playlist_id);

-- name: GetTrackAvailability :one
-- GetTrackAvailability tells whether a track is available. A track that
-- does not exist is sql.ErrNoRows.
SELECT available FROM tracks WHERE id = ?;

-- name: ShiftPlaylistItems :exec
-- ShiftPlaylistItems moves the items of a playlist that are at the
-- positions first..last by a number of places, to make room for a block or
-- to close the place an item left. Positions are not unique (T5), so the
-- rows can pass through the same value one after the other.
UPDATE playlist_items
SET position = position + sqlc.arg(places)
WHERE playlist_id = sqlc.arg(playlist_id) AND position >= sqlc.arg(first) AND position <= sqlc.arg(last);

-- name: InsertPlaylistItem :exec
-- InsertPlaylistItem writes a new item of a playlist.
INSERT INTO playlist_items (id, playlist_id, track_id, position, added_at) VALUES (?, ?, ?, ?, ?);

-- name: SetPlaylistItemPosition :exec
-- SetPlaylistItemPosition puts an item at a position.
UPDATE playlist_items SET position = ? WHERE id = ?;

-- name: DeletePlaylistItem :exec
-- DeletePlaylistItem removes an item. RenumberPlaylistItems then closes the
-- place it left.
DELETE FROM playlist_items WHERE id = ?;

-- name: RenumberPlaylistItems :exec
-- RenumberPlaylistItems makes the positions of the items of a playlist
-- dense again, 0..n-1, in the order (position, id) they have (DESIGN.md
-- 8.6, T5). Every change of the items ends with it, in its transaction. It
-- writes only the rows that are out of place, and looks for them only when
-- the positions are not dense already: they are when no two items have the
-- same one and the highest is the number of the items less one. That is
-- read from the index of the positions alone, while looking at every item
-- costs as much as moving them all.
--
-- It is an upsert of the items on themselves, because sqlc does not read
-- UPDATE ... FROM: every row conflicts on its id, so nothing is inserted,
-- and a row takes the place row_number() gives it. SQLite reads the whole
-- SELECT into a temporary table before it writes, as for every INSERT that
-- selects from its own table, so the places are those of the items as they
-- were.
INSERT INTO playlist_items (id, playlist_id, track_id, position, added_at)
SELECT items.id, items.playlist_id, items.track_id,
    row_number() OVER (ORDER BY items.position, items.id) - 1, items.added_at
FROM playlist_items AS items
WHERE items.playlist_id = sqlc.arg(playlist_id)
  AND (SELECT count(*) <> count(DISTINCT placed.position) OR max(placed.position) <> count(*) - 1
       FROM playlist_items AS placed
       WHERE placed.playlist_id = sqlc.arg(playlist_id))
ON CONFLICT (id) DO UPDATE SET position = excluded.position
WHERE playlist_items.position <> excluded.position;

-- name: ListPlaylistItems :many
-- ListPlaylistItems returns a page of the items of a playlist, by
-- (position, id) (DESIGN.md 8.5), on the index playlist_items_position_idx:
-- the items after the one with that key, which need not exist any more. No
-- position is negative, so the first page is the one after (-1, ''). The
-- values of the cursor are cast to the type of their column, as in the
-- lists of the catalog. A track that is not available is listed, with what
-- the API shows of its album, like GetTrackWithAlbum. favorite is computed
-- as in every other query that returns a track.
SELECT playlist_items.id AS item_id, playlist_items.position, playlist_items.added_at, sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites AS mine
            WHERE mine.user_id = sqlc.arg(user_id) AND mine.track_id = tracks.id) AS favorite
FROM playlist_items
JOIN tracks ON tracks.id = playlist_items.track_id
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE playlist_items.playlist_id = sqlc.arg(playlist_id)
  AND (playlist_items.position, playlist_items.id) > (CAST(sqlc.arg(after_position) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY playlist_items.position, playlist_items.id
LIMIT sqlc.arg(page_size);

-- name: NextPlaylistCover :one
-- NextPlaylistCover returns the first item of a playlist after the one with
-- that key (position, id) whose track is available, whose album has a cover
-- and whose album is none of seen1, seen2 and seen3, with that album and its
-- cover: the next cover of the mosaic of the playlist (Playlist.covers,
-- docs/proposals/web-client-api.md A2). A cover is shown once for each
-- album, so the albums found already are passed as seen ('' for none, which
-- is no album id). No position is negative, so the first item is the one
-- after (-1, ''). The items are read in the order of
-- playlist_items_position_idx from the key, and the query stops at the
-- first that is kept: the items it passes have a track that is not
-- available, an album without a cover or an album seen already, so the
-- next call, from the key of this item, does not read them again. The track
-- of an item is found by its id, through the index that also holds
-- available: without statistics SQLite would otherwise read the tracks
-- through an index that begins with available, every available track for
-- each item (NOTES.md N-185).
SELECT playlist_items.position, playlist_items.id, tracks.album_id, CAST(albums.cover_sha256 AS TEXT) AS cover_sha256
FROM playlist_items
JOIN tracks INDEXED BY tracks_duration_idx ON tracks.id = playlist_items.track_id
JOIN albums ON albums.id = tracks.album_id
WHERE playlist_items.playlist_id = sqlc.arg(playlist_id)
  AND (playlist_items.position, playlist_items.id) > (CAST(sqlc.arg(after_position) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
  AND tracks.available = 1
  AND albums.cover_sha256 IS NOT NULL
  AND tracks.album_id NOT IN (CAST(sqlc.arg(seen1) AS TEXT), CAST(sqlc.arg(seen2) AS TEXT), CAST(sqlc.arg(seen3) AS TEXT))
ORDER BY playlist_items.position, playlist_items.id
LIMIT 1;

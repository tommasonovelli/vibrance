-- name: ListMovedReferences :many
-- ListMovedReferences is how the references of the users follow the audio
-- (DESIGN.md, erratum of 2026-10-02): a playlist item or a favorite that
-- points to a track that is no longer available moves to an available track
-- with the same fingerprint. The queries of this file are the only ones
-- with which the scanner writes the tables of the users; none of them
-- deletes or changes a row of tracks (I3).
--
-- It returns, for every unavailable track that a playlist item or a
-- favorite points to, the available tracks with its fingerprint, whatever
-- their album and their fp_version. The first row of an old_id is the track
-- its references move to: one of the same album before one of another
-- album, then the row created last (the highest seq), which is the track
-- that was just moved. A track without such a twin is not listed.
SELECT gone.id AS old_id, here.id AS new_id
FROM tracks AS gone
JOIN tracks AS here ON here.fingerprint = gone.fingerprint AND here.available = 1
WHERE gone.available = 0
  AND gone.id IN (SELECT track_id FROM playlist_items UNION SELECT track_id FROM favorites)
ORDER BY gone.seq, (here.album_id = gone.album_id) DESC, here.seq DESC;

-- name: ListPlaylistIDsByTrack :many
-- ListPlaylistIDsByTrack returns the playlists that have a track among
-- their items.
SELECT DISTINCT playlist_id FROM playlist_items WHERE track_id = ? ORDER BY playlist_id;

-- name: MovePlaylistItems :exec
-- MovePlaylistItems makes every playlist item of a track an item of another
-- track. The id, the position and added_at of the items stay.
UPDATE playlist_items SET track_id = sqlc.arg(new_id) WHERE track_id = sqlc.arg(old_id);

-- name: TouchPlaylist :exec
-- TouchPlaylist records that the items of a playlist changed: its revision
-- goes up by one (DESIGN.md 8.6).
UPDATE playlists SET revision = revision + 1, updated_at = ? WHERE id = ?;

-- name: CopyFavorites :exec
-- CopyFavorites makes every user that has a track among its favorites have
-- another track there, since the same moment. A user that already has the
-- other track keeps the row it has.
INSERT INTO favorites (user_id, track_id, created_at)
SELECT old.user_id, CAST(sqlc.arg(new_id) AS text), old.created_at
FROM favorites AS old
WHERE old.track_id = sqlc.arg(old_id)
ON CONFLICT (user_id, track_id) DO NOTHING;

-- name: DeleteFavoritesOfTrack :exec
-- DeleteFavoritesOfTrack removes a track from the favorites of every user,
-- once CopyFavorites has put the track with the same audio in its place.
DELETE FROM favorites WHERE track_id = ?;

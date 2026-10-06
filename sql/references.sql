-- name: ListAudioTwins :many
-- ListAudioTwins is how the references of the users, and the moment the
-- audio was first seen, follow the audio (DESIGN.md, errata of 2026-10-02
-- and of 2026-10-06, W1): a playlist item or a favorite that points to a
-- track that is no longer available moves to an available track with the
-- same fingerprint, and that track keeps the earlier first_seen_at of the
-- two. The queries of this file are the only ones with which the scanner
-- writes the tables of the users; none of them deletes a row of tracks or
-- changes its id or its availability (I3, I15).
--
-- It returns, for every unavailable track, the available tracks with its
-- fingerprint, whatever their album and their fp_version, with whether a
-- playlist item or a favorite points to the unavailable one and the
-- first_seen_at of both. The first row of an old_id is the track its
-- references move to: one of the same album before one of another album,
-- then the row created last (the highest seq), which is the track that was
-- just moved. A track without such a twin is not listed.
-- The index of the twins is named: without statistics SQLite would look for
-- them among every available track, through an index that begins with
-- available.
SELECT gone.id AS old_id, gone.first_seen_at AS old_first_seen_at,
    CAST(gone.id IN (SELECT track_id FROM playlist_items UNION SELECT track_id FROM favorites) AS INTEGER) AS referenced,
    here.id AS new_id, here.first_seen_at AS new_first_seen_at
FROM tracks AS gone
JOIN tracks AS here INDEXED BY tracks_fingerprint_idx ON here.fingerprint = gone.fingerprint AND here.available = 1
WHERE gone.available = 0
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

-- name: LowerTrackFirstSeen :exec
-- LowerTrackFirstSeen gives a track an earlier first_seen_at: that of a
-- row with the same audio which it replaces. A later one changes nothing.
UPDATE tracks SET first_seen_at = sqlc.arg(first_seen_at)
WHERE id = sqlc.arg(id) AND first_seen_at > sqlc.arg(first_seen_at);

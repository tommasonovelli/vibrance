-- name: CountRows :one
-- CountRows counts the rows of the tables a backup names in its manifest
-- (DESIGN.md 11.4): what a reader of the manifest can compare with what the
-- restored server shows.
SELECT
    (SELECT count(*) FROM users) AS users,
    (SELECT count(*) FROM playlists) AS playlists,
    (SELECT count(*) FROM playlist_items) AS playlist_items,
    (SELECT count(*) FROM favorites) AS favorites,
    (SELECT count(*) FROM artists) AS artists,
    (SELECT count(*) FROM albums) AS albums,
    (SELECT count(*) FROM tracks) AS tracks;

-- name: ListAlbumsWithWrongCounters :many
-- ListAlbumsWithWrongCounters returns the albums whose track_count or
-- duration_ms is not what their available tracks say (DESIGN.md 5.2), with
-- both. The doctor reads it (DESIGN.md 11.4); a sound index has none.
SELECT albums.id, albums.track_count, albums.duration_ms,
    count(tracks.seq) AS available_tracks,
    CAST(coalesce(sum(tracks.duration_ms), 0) AS INTEGER) AS available_duration_ms
FROM albums
LEFT JOIN tracks ON tracks.album_id = albums.id AND tracks.available = 1
GROUP BY albums.seq
HAVING albums.track_count <> count(tracks.seq)
    OR albums.duration_ms <> coalesce(sum(tracks.duration_ms), 0)
ORDER BY albums.seq;

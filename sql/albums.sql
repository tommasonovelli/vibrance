-- name: GetIndexedAlbum :one
-- GetIndexedAlbum returns the row of an album, available or not, with the
-- name of its artist: what the indexer starts from (DESIGN.md 6.3 step 3).
-- An album that was never indexed is sql.ErrNoRows.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.id = ?;

-- name: UpsertAlbum :exec
-- UpsertAlbum writes what the indexer knows of an album and makes it
-- available (DESIGN.md 6.3 step 8). A new album has no tracks yet; a known
-- one keeps its first_seen_at and its counters, which UpdateAlbumCounters
-- sets once the tracks are written. The row is never deleted (I3).
INSERT INTO albums (
    id, artist_id, artist_key, title, title_key, year, year_key, genre, compilation,
    rel_path, album_revision, render_version, receipt_hash,
    cover_rel, cover_sha256, cover_mime, cover_size, cover_mtime_ns,
    track_count, duration_ms, available, first_seen_at, updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?, ?, ?,
    0, 0, 1, ?, ?
)
ON CONFLICT (id) DO UPDATE SET
    artist_id = excluded.artist_id,
    artist_key = excluded.artist_key,
    title = excluded.title,
    title_key = excluded.title_key,
    year = excluded.year,
    year_key = excluded.year_key,
    genre = excluded.genre,
    compilation = excluded.compilation,
    rel_path = excluded.rel_path,
    album_revision = excluded.album_revision,
    render_version = excluded.render_version,
    receipt_hash = excluded.receipt_hash,
    cover_rel = excluded.cover_rel,
    cover_sha256 = excluded.cover_sha256,
    cover_mime = excluded.cover_mime,
    cover_size = excluded.cover_size,
    cover_mtime_ns = excluded.cover_mtime_ns,
    available = 1,
    updated_at = excluded.updated_at;

-- name: UpdateAlbumCounters :exec
-- UpdateAlbumCounters counts the available tracks of an album again, and
-- adds up their known durations (DESIGN.md 5.2).
UPDATE albums SET
    track_count = (
        SELECT count(*) FROM tracks
        WHERE tracks.album_id = albums.id AND tracks.available = 1),
    duration_ms = (
        SELECT coalesce(sum(tracks.duration_ms), 0) FROM tracks
        WHERE tracks.album_id = albums.id AND tracks.available = 1)
WHERE albums.id = ?;

-- name: SetArtistKeyOfAlbums :exec
-- SetArtistKeyOfAlbums gives every album of an artist the sort key of the
-- artist, of which albums.artist_key is a copy (DESIGN.md 5.2), after the
-- name of the artist changed.
UPDATE albums SET artist_key = ? WHERE artist_id = ?;

-- name: ListAlbumIDsByArtist :many
-- ListAlbumIDsByArtist returns the ids of the available albums of an artist.
SELECT id FROM albums WHERE artist_id = ? AND available = 1 ORDER BY seq;

-- name: ListAlbumStates :many
-- ListAlbumStates returns, for every album of the index, available or not,
-- what a scan compares with the library (DESIGN.md 6.1, P3): the folder and
-- the receipt it was indexed from. The rows are in the order they were
-- created.
SELECT id, artist_id, rel_path, receipt_hash, available FROM albums ORDER BY seq;

-- name: SetAlbumUnavailable :exec
-- SetAlbumUnavailable records that the folder of an album is gone (DESIGN.md
-- 6.4). The row stays, with what was last known of the album (I3), and comes
-- back with the same id if its folder does.
UPDATE albums SET available = 0, updated_at = ? WHERE id = ? AND available = 1;

-- name: CountAlbums :many
-- CountAlbums counts the albums that are available (1) and those that are
-- not (0), for the state of the library (DESIGN.md 6.5).
SELECT available, count(*) AS total FROM albums GROUP BY available ORDER BY available;

-- name: ListAlbumTitles :many
-- ListAlbumTitles returns the title of every album: what its title_key is
-- computed from (DESIGN.md 5.5).
SELECT id, title FROM albums ORDER BY seq;

-- name: SetAlbumTitleKey :exec
-- SetAlbumTitleKey gives an album the sort key of its title, computed again
-- after the collation changed (DESIGN.md 5.5, T26).
UPDATE albums SET title_key = ? WHERE id = ?;

-- name: GetAlbumCover :one
-- GetAlbumCover returns what serving the cover of an album needs (DESIGN.md
-- 9.2): the folder of the album and the cover the scanner saw in it, with
-- the size and the time of its file. The cover columns are null for an
-- album without a cover. An album that was never indexed is sql.ErrNoRows.
SELECT rel_path, cover_rel, cover_sha256, cover_mime, cover_size, cover_mtime_ns, available
FROM albums
WHERE id = ?;

-- name: GetAlbumCoverByHash :one
-- GetAlbumCoverByHash returns an available album that has the cover with
-- that SHA-256, the oldest one if several do: where the thumbnails of the
-- cover are made from, ahead of any request (DESIGN.md 9.2). No such album
-- is sql.ErrNoRows.
SELECT rel_path, cover_rel, cover_sha256, cover_mime, cover_size, cover_mtime_ns, available
FROM albums
WHERE cover_sha256 = ? AND available = 1
ORDER BY seq
LIMIT 1;

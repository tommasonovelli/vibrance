-- name: ListTracksByAlbum :many
-- ListTracksByAlbum returns every row of an album, available or not, in the
-- order in which the rows were created: the rows the reconciliation pairs
-- with the files of a new receipt (DESIGN.md 5.4).
SELECT * FROM tracks WHERE album_id = ? ORDER BY seq;

-- name: UpsertTrack :exec
-- UpsertTrack creates a track, or gives a known one, found by its id, what
-- its file says now, and makes it available (DESIGN.md 5.4, 6.3 step 8).
-- The id and the album of a row never change (I3). title_key and
-- artist_key are the sort keys of the title and of the artist (DESIGN.md
-- 5.5). first_seen_at is written only when the row is created: a known row
-- keeps it. album_key, the copy of the title_key of the album, is written
-- for every row of the album by SetAlbumKeyOfTracks.
INSERT INTO tracks (
    id, album_id, fingerprint, fp_version, occurrence, disc, "no", title, artist, genre,
    rel_path, file_size, file_mtime_ns, file_sha256,
    codec, sample_rate, channels, bit_depth, bitrate, duration_ms,
    lyrics_rel, lyrics_sha256,
    rg_track_gain, rg_track_peak, rg_album_gain, rg_album_peak,
    available, updated_at, title_key, artist_key, first_seen_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?,
    ?, ?, ?, ?,
    1, ?, ?, ?, ?
)
ON CONFLICT (id) DO UPDATE SET
    fingerprint = excluded.fingerprint,
    fp_version = excluded.fp_version,
    occurrence = excluded.occurrence,
    disc = excluded.disc,
    "no" = excluded."no",
    title = excluded.title,
    artist = excluded.artist,
    title_key = excluded.title_key,
    artist_key = excluded.artist_key,
    genre = excluded.genre,
    rel_path = excluded.rel_path,
    file_size = excluded.file_size,
    file_mtime_ns = excluded.file_mtime_ns,
    file_sha256 = excluded.file_sha256,
    codec = excluded.codec,
    sample_rate = excluded.sample_rate,
    channels = excluded.channels,
    bit_depth = excluded.bit_depth,
    bitrate = excluded.bitrate,
    duration_ms = excluded.duration_ms,
    lyrics_rel = excluded.lyrics_rel,
    lyrics_sha256 = excluded.lyrics_sha256,
    rg_track_gain = excluded.rg_track_gain,
    rg_track_peak = excluded.rg_track_peak,
    rg_album_gain = excluded.rg_album_gain,
    rg_album_peak = excluded.rg_album_peak,
    available = 1,
    updated_at = excluded.updated_at
WHERE tracks.album_id = excluded.album_id;

-- name: SetTrackUnavailable :exec
-- SetTrackUnavailable records that the file of a track is gone. The row
-- stays, with what was last known of the track (I3): favorites and playlists
-- still show it, and it comes back with the same id if its audio does.
UPDATE tracks SET available = 0, updated_at = ? WHERE id = ?;

-- name: SetTracksOfAlbumUnavailable :exec
-- SetTracksOfAlbumUnavailable records that the files of every track of an
-- album are gone with its folder (DESIGN.md 6.4). The rows stay (I3). The
-- plus keeps SQLite from an index that begins with available, through which
-- it would read every available track (see ListAvailableTracksOfAlbum; an
-- UPDATE that names its index is not read by sqlc).
UPDATE tracks SET available = 0, updated_at = ? WHERE album_id = ? AND +available = 1;

-- name: CountTracks :many
-- CountTracks counts the tracks that are available (1) and those that are
-- not (0), for the state of the library (DESIGN.md 6.5).
SELECT available, count(*) AS total FROM tracks GROUP BY available ORDER BY available;

-- name: ListStaleFingerprints :many
-- ListStaleFingerprints returns a page of the available tracks whose
-- fingerprint was computed by another ffmpeg than the current one, in the
-- order of seq and after a given seq, with the folder of their album
-- (DESIGN.md 6.6). Only such a fingerprint is computed again. The tracks are
-- read in the order of seq, their key, and by no index: one that begins with
-- available would give them in another order, to be sorted for every page.
SELECT tracks.seq, tracks.id, tracks.album_id, albums.rel_path AS album_rel_path, tracks.rel_path,
    tracks.file_size, tracks.file_mtime_ns, tracks.file_sha256,
    tracks.fingerprint, tracks.fp_version, tracks.occurrence
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
WHERE tracks.available = 1 AND tracks.fp_version <> sqlc.arg(fp_version) AND tracks.seq > sqlc.arg(after_seq)
ORDER BY tracks.seq
LIMIT sqlc.arg(page_size);

-- name: ListOccurrences :many
-- ListOccurrences returns the occurrences that the other rows of an album,
-- available or not, have for a fingerprint: those a row that takes that
-- fingerprint cannot have (DESIGN.md 5.4).
SELECT occurrence FROM tracks WHERE album_id = ? AND fingerprint = ? AND id <> ? ORDER BY occurrence;

-- name: SetTrackFingerprint :execrows
-- SetTrackFingerprint gives a row the fingerprint that the current ffmpeg
-- computes for its file (DESIGN.md 6.6): the same row, with the same id
-- (I3). It writes only if the row is still available and still describes
-- the file that was read, with the fingerprint it had then; otherwise it
-- changes nothing, and the number of rows it returns is 0.
UPDATE tracks SET
    fingerprint = sqlc.arg(fingerprint),
    fp_version = sqlc.arg(fp_version),
    occurrence = sqlc.arg(occurrence),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND available = 1
  AND fingerprint = sqlc.arg(old_fingerprint)
  AND fp_version = sqlc.arg(old_fp_version)
  AND file_size = sqlc.arg(file_size)
  AND file_mtime_ns = sqlc.arg(file_mtime_ns);

-- name: SetAlbumKeyOfTracks :exec
-- SetAlbumKeyOfTracks gives every row of an album, available or not, the
-- sort key of the title of the album, of which tracks.album_key is a copy
-- (the list of the tracks by album). Only the rows whose copy differs are
-- written.
UPDATE tracks SET album_key = sqlc.arg(album_key)
WHERE album_id = sqlc.arg(album_id) AND album_key <> sqlc.arg(album_key);

-- name: ListTrackNames :many
-- ListTrackNames returns the title and the artist of every track: what its
-- title_key and artist_key are computed from (DESIGN.md 5.5).
SELECT id, title, artist FROM tracks ORDER BY seq;

-- name: SetTrackSortKeys :exec
-- SetTrackSortKeys gives a track the sort keys of its title and of its
-- artist, computed again after the collation changed (DESIGN.md 5.5, T26).
UPDATE tracks SET title_key = ?, artist_key = ? WHERE id = ?;

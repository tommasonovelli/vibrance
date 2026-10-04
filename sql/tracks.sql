-- name: ListTracksByAlbum :many
-- ListTracksByAlbum returns every row of an album, available or not, in the
-- order in which the rows were created: the rows the reconciliation pairs
-- with the files of a new receipt (DESIGN.md 5.4).
SELECT * FROM tracks WHERE album_id = ? ORDER BY seq;

-- name: UpsertTrack :exec
-- UpsertTrack creates a track, or gives a known one, found by its id, what
-- its file says now, and makes it available (DESIGN.md 5.4, 6.3 step 8).
-- The id and the album of a row never change (I3).
INSERT INTO tracks (
    id, album_id, fingerprint, fp_version, occurrence, disc, "no", title, artist, genre,
    rel_path, file_size, file_mtime_ns, file_sha256,
    codec, sample_rate, channels, bit_depth, bitrate, duration_ms,
    lyrics_rel, lyrics_sha256,
    rg_track_gain, rg_track_peak, rg_album_gain, rg_album_peak,
    available, updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?,
    ?, ?, ?, ?,
    1, ?
)
ON CONFLICT (id) DO UPDATE SET
    fingerprint = excluded.fingerprint,
    fp_version = excluded.fp_version,
    occurrence = excluded.occurrence,
    disc = excluded.disc,
    "no" = excluded."no",
    title = excluded.title,
    artist = excluded.artist,
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
-- album are gone with its folder (DESIGN.md 6.4). The rows stay (I3).
UPDATE tracks SET available = 0, updated_at = ? WHERE album_id = ? AND available = 1;

-- name: CountTracks :many
-- CountTracks counts the tracks that are available (1) and those that are
-- not (0), for the state of the library (DESIGN.md 6.5).
SELECT available, count(*) AS total FROM tracks GROUP BY available ORDER BY available;

-- name: ListStaleFingerprints :many
-- ListStaleFingerprints returns a page of the available tracks whose
-- fingerprint was computed by another ffmpeg than the current one, in the
-- order of seq and after a given seq, with the folder of their album
-- (DESIGN.md 6.6). Only such a fingerprint is computed again.
SELECT tracks.seq, tracks.id, tracks.album_id, albums.rel_path AS album_rel_path, tracks.rel_path,
    tracks.file_size, tracks.file_mtime_ns, tracks.file_sha256,
    tracks.fingerprint, tracks.fp_version, tracks.occurrence
FROM tracks
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

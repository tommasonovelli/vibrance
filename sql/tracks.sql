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

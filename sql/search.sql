-- name: GetListedArtistBySeq :one
-- GetListedArtistBySeq returns the artist with that seq, if it has at least
-- one available album, with how many it has. The full-text search reads
-- what it found with this query and the two below: the rowid of a row of
-- the full-text tables is the seq of what it describes, and they hold only
-- what the lists show (DESIGN.md 10.1, T6), so sql.ErrNoRows here means
-- that the two disagree.
SELECT sqlc.embed(artists),
    (SELECT count(*) FROM albums
     WHERE albums.artist_id = artists.id AND albums.available = 1) AS album_count
FROM artists
WHERE artists.seq = sqlc.arg(seq)
  AND EXISTS (SELECT 1 FROM albums WHERE albums.artist_id = artists.id AND albums.available = 1);

-- name: GetAvailableAlbumBySeq :one
-- GetAvailableAlbumBySeq returns the album with that seq, if it is
-- available, with the name of its artist.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.seq = sqlc.arg(seq) AND albums.available = 1;

-- name: GetAvailableTrackBySeq :one
-- GetAvailableTrackBySeq returns the track with that seq, if it is
-- available, with what the API shows of its album and whether it is a
-- favorite of the user.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq = sqlc.arg(seq) AND tracks.available = 1;

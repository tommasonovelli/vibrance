-- name: ListArtistsByName :many
-- ListArtistsByName returns the first page of the artists that have at
-- least one available album, by (sort_key, id), with how many they have
-- (DESIGN.md 8.5). Like every list of the catalog it is paginated by key,
-- never by OFFSET: the query of each page after the first compares the key
-- of the rows with the key of the cursor as one row value, which SQLite
-- turns into a range of the index of the order (T9). The values of the
-- cursor are cast to the type of their column: a blob is greater than any
-- text in SQLite, and sqlc would bind an id as a blob.
SELECT sqlc.embed(artists),
    (SELECT count(*) FROM albums
     WHERE albums.artist_id = artists.id AND albums.available = 1) AS album_count
FROM artists
WHERE EXISTS (SELECT 1 FROM albums WHERE albums.artist_id = artists.id AND albums.available = 1)
ORDER BY artists.sort_key, artists.id
LIMIT sqlc.arg(page_size);

-- name: ListArtistsByNameAfter :many
-- ListArtistsByNameAfter returns the page of ListArtistsByName after the
-- artist with that key.
SELECT sqlc.embed(artists),
    (SELECT count(*) FROM albums
     WHERE albums.artist_id = artists.id AND albums.available = 1) AS album_count
FROM artists
WHERE EXISTS (SELECT 1 FROM albums WHERE albums.artist_id = artists.id AND albums.available = 1)
  AND (artists.sort_key, artists.id) > (CAST(sqlc.arg(sort_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY artists.sort_key, artists.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByTitleAsc :many
-- ListAlbumsByTitleAsc returns the first page of the available albums by
-- (title_key, id), ascending. In every list of the albums, artist_id, when
-- not null, keeps only the albums of that artist, and a Desc order reverses
-- every key of the Asc one (DESIGN.md 8.5).
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
ORDER BY albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByTitleAscAfter :many
-- ListAlbumsByTitleAscAfter returns the page of ListAlbumsByTitleAsc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
  AND (albums.title_key, albums.id) > (CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByArtistAsc :many
-- ListAlbumsByArtistAsc returns the first page of the available albums by
-- (artist_key, year_key, title_key, id), ascending.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
ORDER BY albums.artist_key, albums.year_key, albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByArtistAscAfter :many
-- ListAlbumsByArtistAscAfter returns the page of ListAlbumsByArtistAsc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
  AND (albums.artist_key, albums.year_key, albums.title_key, albums.id) > (CAST(sqlc.arg(artist_key) AS BLOB), CAST(sqlc.arg(year_key) AS INTEGER), CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.artist_key, albums.year_key, albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByYearAsc :many
-- ListAlbumsByYearAsc returns the first page of the available albums by
-- (year_key, title_key, id), ascending.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
ORDER BY albums.year_key, albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByYearAscAfter :many
-- ListAlbumsByYearAscAfter returns the page of ListAlbumsByYearAsc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
  AND (albums.year_key, albums.title_key, albums.id) > (CAST(sqlc.arg(year_key) AS INTEGER), CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.year_key, albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByAddedAsc :many
-- ListAlbumsByAddedAsc returns the first page of the available albums by
-- (first_seen_at, id), ascending.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
ORDER BY albums.first_seen_at, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByAddedAscAfter :many
-- ListAlbumsByAddedAscAfter returns the page of ListAlbumsByAddedAsc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
  AND (albums.first_seen_at, albums.id) > (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.first_seen_at, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByTitleDesc :many
-- ListAlbumsByTitleDesc returns the first page of the available albums by
-- (title_key, id), reversed.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
ORDER BY albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByTitleDescAfter :many
-- ListAlbumsByTitleDescAfter returns the page of ListAlbumsByTitleDesc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
  AND (albums.title_key, albums.id) < (CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByArtistDesc :many
-- ListAlbumsByArtistDesc returns the first page of the available albums by
-- (artist_key, year_key, title_key, id), reversed.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
ORDER BY albums.artist_key DESC, albums.year_key DESC, albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByArtistDescAfter :many
-- ListAlbumsByArtistDescAfter returns the page of ListAlbumsByArtistDesc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
  AND (albums.artist_key, albums.year_key, albums.title_key, albums.id) < (CAST(sqlc.arg(artist_key) AS BLOB), CAST(sqlc.arg(year_key) AS INTEGER), CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.artist_key DESC, albums.year_key DESC, albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByYearDesc :many
-- ListAlbumsByYearDesc returns the first page of the available albums by
-- (year_key, title_key, id), reversed.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
ORDER BY albums.year_key DESC, albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByYearDescAfter :many
-- ListAlbumsByYearDescAfter returns the page of ListAlbumsByYearDesc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
  AND (albums.year_key, albums.title_key, albums.id) < (CAST(sqlc.arg(year_key) AS INTEGER), CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.year_key DESC, albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByAddedDesc :many
-- ListAlbumsByAddedDesc returns the first page of the available albums by
-- (first_seen_at, id), reversed.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
ORDER BY albums.first_seen_at DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByAddedDescAfter :many
-- ListAlbumsByAddedDescAfter returns the page of ListAlbumsByAddedDesc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (CAST(sqlc.narg(artist_id) AS TEXT) IS NULL OR albums.artist_id = CAST(sqlc.narg(artist_id) AS TEXT))
  AND (albums.first_seen_at, albums.id) < (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.first_seen_at DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsOfArtist :many
-- ListAlbumsOfArtist returns every available album of an artist, by
-- (year_key, title_key, id): the order of the albums of one artist in the
-- list by artist. Without INDEXED BY, SQLite walks every available album in
-- the order of albums_year_idx to avoid sorting the few of one artist.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
ORDER BY albums.year_key, albums.title_key, albums.id;

-- name: GetAvailableAlbum :one
-- GetAvailableAlbum returns an album with the name of its artist, if it is
-- available. An album that is not, or was never indexed, is sql.ErrNoRows.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.id = sqlc.arg(id) AND albums.available = 1;

-- name: ListAvailableTracksOfAlbum :many
-- ListAvailableTracksOfAlbum returns the available tracks of an album by
-- (disc, no), then in the order the rows were created, and whether each is
-- a favorite of the user.
SELECT sqlc.embed(tracks),
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
WHERE tracks.album_id = sqlc.arg(album_id) AND tracks.available = 1
ORDER BY tracks.disc, tracks."no", tracks.seq;

-- name: GetTrackWithAlbum :one
-- GetTrackWithAlbum returns a track, available or not, with what the API
-- shows of its album, which is always there (the rows are never deleted,
-- I3), and whether it is a favorite of the user. No such track is
-- sql.ErrNoRows.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.id = sqlc.arg(id);

-- name: GetTrackFile :one
-- GetTrackFile returns what the index says of the files of a track,
-- available or not: the folder of its album and its own path, both
-- relative to library/ and written only by the scanner (I2), the size, the
-- time and the SHA-256 the scanner saw, its codec, and its lyrics file
-- (DESIGN.md 9.1, 9.3).
SELECT tracks.available, albums.rel_path AS album_rel_path, tracks.rel_path,
    tracks.file_size, tracks.file_mtime_ns, tracks.file_sha256, tracks.codec,
    tracks.lyrics_rel, tracks.lyrics_sha256
FROM tracks
JOIN albums ON albums.id = tracks.album_id
WHERE tracks.id = ?;

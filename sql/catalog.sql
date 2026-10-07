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
-- (title_key, id), ascending. In every list of the albums a Desc order
-- reverses every key of the Asc one (DESIGN.md 8.5). The lists of the albums
-- of one artist are the ListArtistAlbums queries below.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
ORDER BY albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByTitleAscAfter :many
-- ListAlbumsByTitleAscAfter returns the page of ListAlbumsByTitleAsc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
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
ORDER BY albums.artist_key, albums.year_key, albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByArtistAscAfter :many
-- ListAlbumsByArtistAscAfter returns the page of ListAlbumsByArtistAsc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
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
ORDER BY albums.year_key, albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByYearAscAfter :many
-- ListAlbumsByYearAscAfter returns the page of ListAlbumsByYearAsc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
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
ORDER BY albums.first_seen_at, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByAddedAscAfter :many
-- ListAlbumsByAddedAscAfter returns the page of ListAlbumsByAddedAsc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
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
ORDER BY albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByTitleDescAfter :many
-- ListAlbumsByTitleDescAfter returns the page of ListAlbumsByTitleDesc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
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
ORDER BY albums.artist_key DESC, albums.year_key DESC, albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByArtistDescAfter :many
-- ListAlbumsByArtistDescAfter returns the page of ListAlbumsByArtistDesc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
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
ORDER BY albums.year_key DESC, albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByYearDescAfter :many
-- ListAlbumsByYearDescAfter returns the page of ListAlbumsByYearDesc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
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
ORDER BY albums.first_seen_at DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAlbumsByAddedDescAfter :many
-- ListAlbumsByAddedDescAfter returns the page of ListAlbumsByAddedDesc
-- after the album with that key.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums
JOIN artists ON artists.id = albums.artist_id
WHERE albums.available = 1
  AND (albums.first_seen_at, albums.id) < (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY albums.first_seen_at DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- The lists of the albums of one artist (DESIGN.md 8.5: artist=<id>), in
-- the same eight orders. They read the albums of the artist from
-- albums_artist_id_idx and sort them, a few hundred rows at most, where the
-- lists above walk the index of their order: with a filter those would skip
-- the albums of every other artist, up to the whole list for an artist
-- whose albums come last. One query is both the first page (first_page not
-- 0: the key of the cursor is not read) and the pages after a key.

-- name: ListArtistAlbumsByTitleAsc :many
-- ListArtistAlbumsByTitleAsc returns a page of the available albums of an
-- artist by (title_key, id), ascending.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
  AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
       OR (albums.title_key, albums.id) > (CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListArtistAlbumsByTitleDesc :many
-- ListArtistAlbumsByTitleDesc returns a page of the available albums of an
-- artist by (title_key, id), reversed.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
  AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
       OR (albums.title_key, albums.id) < (CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListArtistAlbumsByArtistAsc :many
-- ListArtistAlbumsByArtistAsc returns a page of the available albums of an
-- artist by (artist_key, year_key, title_key, id), ascending.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
  AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
       OR (albums.artist_key, albums.year_key, albums.title_key, albums.id) > (CAST(sqlc.arg(artist_key) AS BLOB), CAST(sqlc.arg(year_key) AS INTEGER), CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY albums.artist_key, albums.year_key, albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListArtistAlbumsByArtistDesc :many
-- ListArtistAlbumsByArtistDesc returns a page of the available albums of an
-- artist by (artist_key, year_key, title_key, id), reversed.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
  AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
       OR (albums.artist_key, albums.year_key, albums.title_key, albums.id) < (CAST(sqlc.arg(artist_key) AS BLOB), CAST(sqlc.arg(year_key) AS INTEGER), CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY albums.artist_key DESC, albums.year_key DESC, albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListArtistAlbumsByYearAsc :many
-- ListArtistAlbumsByYearAsc returns a page of the available albums of an
-- artist by (year_key, title_key, id), ascending.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
  AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
       OR (albums.year_key, albums.title_key, albums.id) > (CAST(sqlc.arg(year_key) AS INTEGER), CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY albums.year_key, albums.title_key, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListArtistAlbumsByYearDesc :many
-- ListArtistAlbumsByYearDesc returns a page of the available albums of an
-- artist by (year_key, title_key, id), reversed.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
  AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
       OR (albums.year_key, albums.title_key, albums.id) < (CAST(sqlc.arg(year_key) AS INTEGER), CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY albums.year_key DESC, albums.title_key DESC, albums.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListArtistAlbumsByAddedAsc :many
-- ListArtistAlbumsByAddedAsc returns a page of the available albums of an
-- artist by (first_seen_at, id), ascending.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
  AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
       OR (albums.first_seen_at, albums.id) > (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY albums.first_seen_at, albums.id
LIMIT sqlc.arg(page_size);

-- name: ListArtistAlbumsByAddedDesc :many
-- ListArtistAlbumsByAddedDesc returns a page of the available albums of an
-- artist by (first_seen_at, id), reversed.
SELECT sqlc.embed(albums), artists.name AS artist_name
FROM albums INDEXED BY albums_artist_id_idx
JOIN artists ON artists.id = albums.artist_id
WHERE albums.artist_id = sqlc.arg(artist_id) AND albums.available = 1
  AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
       OR (albums.first_seen_at, albums.id) < (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT)))
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
-- a favorite of the user. The index is named, as in every query that reads
-- the tracks of one album: the indexes of the list of the tracks begin with
-- available, and without statistics (a first scan) SQLite would read every
-- available track through one of them.
SELECT sqlc.embed(tracks),
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks INDEXED BY tracks_album_disc_no_idx
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
-- available or not: its album, the folder of the album and its own path,
-- both relative to library/ and written only by the scanner (I2), the size,
-- the time and the SHA-256 the scanner saw, its codec, and its lyrics file
-- (DESIGN.md 9.1, 9.3).
SELECT tracks.available, tracks.album_id, albums.rel_path AS album_rel_path, tracks.rel_path,
    tracks.file_size, tracks.file_mtime_ns, tracks.file_sha256, tracks.codec,
    tracks.lyrics_rel, tracks.lyrics_sha256
FROM tracks
JOIN albums ON albums.id = tracks.album_id
WHERE tracks.id = ?;

-- The list of the tracks (GET /tracks, docs/proposals/web-client-api.md
-- A1): the available tracks in four orders, each ending with the id so
-- that it is total, each walking an index of its own (tracks_*_idx), like
-- the lists of the albums above. Every row has what the API shows of its
-- album and whether the track is a favorite of the user.

-- name: ListTracksByTitleAsc :many
-- ListTracksByTitleAsc returns the first page of the available tracks by
-- (title_key, id), ascending.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
ORDER BY tracks.title_key, tracks.id
LIMIT sqlc.arg(page_size);

-- name: ListTracksByTitleAscAfter :many
-- ListTracksByTitleAscAfter returns the page of ListTracksByTitleAsc
-- after the track with that key.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
  AND (tracks.title_key, tracks.id) > (CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY tracks.title_key, tracks.id
LIMIT sqlc.arg(page_size);

-- name: ListTracksByArtistAsc :many
-- ListTracksByArtistAsc returns the first page of the available tracks by
-- (artist_key, album_key, album_id, disc, no, id), ascending.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
ORDER BY tracks.artist_key, tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id
LIMIT sqlc.arg(page_size);

-- name: ListTracksByArtistAscAfter :many
-- ListTracksByArtistAscAfter returns the page of ListTracksByArtistAsc
-- after the track with that key.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
  AND (tracks.artist_key, tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id) > (CAST(sqlc.arg(artist_key) AS BLOB), CAST(sqlc.arg(album_key) AS BLOB), CAST(sqlc.arg(after_album_id) AS TEXT), CAST(sqlc.arg(disc) AS INTEGER), CAST(sqlc.arg(track_no) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY tracks.artist_key, tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id
LIMIT sqlc.arg(page_size);

-- name: ListTracksByAlbumAsc :many
-- ListTracksByAlbumAsc returns the first page of the available tracks by
-- (album_key, album_id, disc, no, id), ascending.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
ORDER BY tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id
LIMIT sqlc.arg(page_size);

-- name: ListTracksByAlbumAscAfter :many
-- ListTracksByAlbumAscAfter returns the page of ListTracksByAlbumAsc
-- after the track with that key.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
  AND (tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id) > (CAST(sqlc.arg(album_key) AS BLOB), CAST(sqlc.arg(after_album_id) AS TEXT), CAST(sqlc.arg(disc) AS INTEGER), CAST(sqlc.arg(track_no) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id
LIMIT sqlc.arg(page_size);

-- name: ListTracksByAddedAsc :many
-- ListTracksByAddedAsc returns the first page of the available tracks by
-- (first_seen_at, id), ascending.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
ORDER BY tracks.first_seen_at, tracks.id
LIMIT sqlc.arg(page_size);

-- name: ListTracksByAddedAscAfter :many
-- ListTracksByAddedAscAfter returns the page of ListTracksByAddedAsc
-- after the track with that key.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
  AND (tracks.first_seen_at, tracks.id) > (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY tracks.first_seen_at, tracks.id
LIMIT sqlc.arg(page_size);

-- name: ListTracksByTitleDesc :many
-- ListTracksByTitleDesc returns the first page of the available tracks by
-- (title_key, id), reversed.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
ORDER BY tracks.title_key DESC, tracks.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListTracksByTitleDescAfter :many
-- ListTracksByTitleDescAfter returns the page of ListTracksByTitleDesc
-- after the track with that key.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
  AND (tracks.title_key, tracks.id) < (CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY tracks.title_key DESC, tracks.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListTracksByArtistDesc :many
-- ListTracksByArtistDesc returns the first page of the available tracks by
-- (artist_key, album_key, album_id, disc, no, id), reversed.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
ORDER BY tracks.artist_key DESC, tracks.album_key DESC, tracks.album_id DESC, tracks.disc DESC, tracks."no" DESC, tracks.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListTracksByArtistDescAfter :many
-- ListTracksByArtistDescAfter returns the page of ListTracksByArtistDesc
-- after the track with that key.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
  AND (tracks.artist_key, tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id) < (CAST(sqlc.arg(artist_key) AS BLOB), CAST(sqlc.arg(album_key) AS BLOB), CAST(sqlc.arg(after_album_id) AS TEXT), CAST(sqlc.arg(disc) AS INTEGER), CAST(sqlc.arg(track_no) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY tracks.artist_key DESC, tracks.album_key DESC, tracks.album_id DESC, tracks.disc DESC, tracks."no" DESC, tracks.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListTracksByAlbumDesc :many
-- ListTracksByAlbumDesc returns the first page of the available tracks by
-- (album_key, album_id, disc, no, id), reversed.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
ORDER BY tracks.album_key DESC, tracks.album_id DESC, tracks.disc DESC, tracks."no" DESC, tracks.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListTracksByAlbumDescAfter :many
-- ListTracksByAlbumDescAfter returns the page of ListTracksByAlbumDesc
-- after the track with that key.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
  AND (tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id) < (CAST(sqlc.arg(album_key) AS BLOB), CAST(sqlc.arg(after_album_id) AS TEXT), CAST(sqlc.arg(disc) AS INTEGER), CAST(sqlc.arg(track_no) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY tracks.album_key DESC, tracks.album_id DESC, tracks.disc DESC, tracks."no" DESC, tracks.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListTracksByAddedDesc :many
-- ListTracksByAddedDesc returns the first page of the available tracks by
-- (first_seen_at, id), reversed.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
ORDER BY tracks.first_seen_at DESC, tracks.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListTracksByAddedDescAfter :many
-- ListTracksByAddedDescAfter returns the page of ListTracksByAddedDesc
-- after the track with that key.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.available = 1
  AND (tracks.first_seen_at, tracks.id) < (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY tracks.first_seen_at DESC, tracks.id DESC
LIMIT sqlc.arg(page_size);

-- The lists of the tracks of the albums of one artist (artist=<id>: the
-- artist of the album, as for the albums), in the same eight orders. Like
-- the lists of the albums of one artist, they read the albums of the
-- artist from albums_artist_id_idx and their tracks from
-- tracks_album_disc_no_idx, and sort those, where the lists above walk the
-- index of their order: with a filter those would skip the tracks of
-- every other artist. An artist may have thousands of tracks: the inner
-- query sorts only their keys and keeps the page, and the outer one reads
-- what the API shows for the rows of the page alone. One query is both the
-- first page (first_page not 0) and the pages after a key. The rows of the
-- page are found by their key, seq, and by no index (NOT INDEXED): one of
-- the order would make SQLite walk every track to look for them.

-- name: ListArtistTracksByTitleAsc :many
-- ListArtistTracksByTitleAsc returns a page of the available tracks of the
-- albums of an artist by (title_key, id), ascending.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
      AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
           OR (t.title_key, t.id) > (CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT)))
    ORDER BY t.title_key, t.id
    LIMIT sqlc.arg(page_size)
)
ORDER BY tracks.title_key, tracks.id;

-- name: ListArtistTracksByArtistAsc :many
-- ListArtistTracksByArtistAsc returns a page of the available tracks of the
-- albums of an artist by (artist_key, album_key, album_id, disc, no, id), ascending.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
      AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
           OR (t.artist_key, t.album_key, t.album_id, t.disc, t."no", t.id) > (CAST(sqlc.arg(artist_key) AS BLOB), CAST(sqlc.arg(album_key) AS BLOB), CAST(sqlc.arg(after_album_id) AS TEXT), CAST(sqlc.arg(disc) AS INTEGER), CAST(sqlc.arg(track_no) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT)))
    ORDER BY t.artist_key, t.album_key, t.album_id, t.disc, t."no", t.id
    LIMIT sqlc.arg(page_size)
)
ORDER BY tracks.artist_key, tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id;

-- name: ListArtistTracksByAlbumAsc :many
-- ListArtistTracksByAlbumAsc returns a page of the available tracks of the
-- albums of an artist by (album_key, album_id, disc, no, id), ascending.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
      AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
           OR (t.album_key, t.album_id, t.disc, t."no", t.id) > (CAST(sqlc.arg(album_key) AS BLOB), CAST(sqlc.arg(after_album_id) AS TEXT), CAST(sqlc.arg(disc) AS INTEGER), CAST(sqlc.arg(track_no) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT)))
    ORDER BY t.album_key, t.album_id, t.disc, t."no", t.id
    LIMIT sqlc.arg(page_size)
)
ORDER BY tracks.album_key, tracks.album_id, tracks.disc, tracks."no", tracks.id;

-- name: ListArtistTracksByAddedAsc :many
-- ListArtistTracksByAddedAsc returns a page of the available tracks of the
-- albums of an artist by (first_seen_at, id), ascending.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
      AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
           OR (t.first_seen_at, t.id) > (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT)))
    ORDER BY t.first_seen_at, t.id
    LIMIT sqlc.arg(page_size)
)
ORDER BY tracks.first_seen_at, tracks.id;

-- name: ListArtistTracksByTitleDesc :many
-- ListArtistTracksByTitleDesc returns a page of the available tracks of the
-- albums of an artist by (title_key, id), reversed.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
      AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
           OR (t.title_key, t.id) < (CAST(sqlc.arg(title_key) AS BLOB), CAST(sqlc.arg(after_id) AS TEXT)))
    ORDER BY t.title_key DESC, t.id DESC
    LIMIT sqlc.arg(page_size)
)
ORDER BY tracks.title_key DESC, tracks.id DESC;

-- name: ListArtistTracksByArtistDesc :many
-- ListArtistTracksByArtistDesc returns a page of the available tracks of the
-- albums of an artist by (artist_key, album_key, album_id, disc, no, id), reversed.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
      AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
           OR (t.artist_key, t.album_key, t.album_id, t.disc, t."no", t.id) < (CAST(sqlc.arg(artist_key) AS BLOB), CAST(sqlc.arg(album_key) AS BLOB), CAST(sqlc.arg(after_album_id) AS TEXT), CAST(sqlc.arg(disc) AS INTEGER), CAST(sqlc.arg(track_no) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT)))
    ORDER BY t.artist_key DESC, t.album_key DESC, t.album_id DESC, t.disc DESC, t."no" DESC, t.id DESC
    LIMIT sqlc.arg(page_size)
)
ORDER BY tracks.artist_key DESC, tracks.album_key DESC, tracks.album_id DESC, tracks.disc DESC, tracks."no" DESC, tracks.id DESC;

-- name: ListArtistTracksByAlbumDesc :many
-- ListArtistTracksByAlbumDesc returns a page of the available tracks of the
-- albums of an artist by (album_key, album_id, disc, no, id), reversed.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
      AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
           OR (t.album_key, t.album_id, t.disc, t."no", t.id) < (CAST(sqlc.arg(album_key) AS BLOB), CAST(sqlc.arg(after_album_id) AS TEXT), CAST(sqlc.arg(disc) AS INTEGER), CAST(sqlc.arg(track_no) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT)))
    ORDER BY t.album_key DESC, t.album_id DESC, t.disc DESC, t."no" DESC, t.id DESC
    LIMIT sqlc.arg(page_size)
)
ORDER BY tracks.album_key DESC, tracks.album_id DESC, tracks.disc DESC, tracks."no" DESC, tracks.id DESC;

-- name: ListArtistTracksByAddedDesc :many
-- ListArtistTracksByAddedDesc returns a page of the available tracks of the
-- albums of an artist by (first_seen_at, id), reversed.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
      AND (CAST(sqlc.arg(first_page) AS INTEGER) <> 0
           OR (t.first_seen_at, t.id) < (CAST(sqlc.arg(first_seen_at) AS INTEGER), CAST(sqlc.arg(after_id) AS TEXT)))
    ORDER BY t.first_seen_at DESC, t.id DESC
    LIMIT sqlc.arg(page_size)
)
ORDER BY tracks.first_seen_at DESC, tracks.id DESC;

-- name: ListRandomTracks :many
-- ListRandomTracks returns up to page_size available tracks chosen at
-- random, none twice, in a random order (GET /tracks/random,
-- docs/proposals/web-client-api.md B1). The inner query reads the key of
-- every available track from tracks_first_seen_idx, the smallest index that
-- begins with available, and keeps page_size of them by a random value;
-- the outer one reads what the API shows for those rows alone, found by
-- their key and by no index, as the lists of one artist do. A key is a
-- row: a track cannot be chosen twice.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM tracks AS t INDEXED BY tracks_first_seen_idx
    WHERE t.available = 1
    ORDER BY random()
    LIMIT sqlc.arg(page_size)
)
ORDER BY random();

-- name: ListRandomTracksOfArtist :many
-- ListRandomTracksOfArtist is ListRandomTracks among the available tracks
-- of the available albums of one artist (the artist of the album, as for
-- GET /tracks?artist=): their albums from albums_artist_id_idx and their
-- tracks from tracks_album_disc_no_idx, as the lists of one artist.
SELECT sqlc.embed(tracks),
    albums.title AS album_title, albums.year AS album_year, albums.cover_sha256 AS album_cover_sha256,
    albums.artist_id AS album_artist_id, artists.name AS album_artist_name,
    EXISTS (SELECT 1 FROM favorites
            WHERE favorites.user_id = sqlc.arg(user_id) AND favorites.track_id = tracks.id) AS favorite
FROM tracks NOT INDEXED
JOIN albums ON albums.id = tracks.album_id
JOIN artists ON artists.id = albums.artist_id
WHERE tracks.seq IN (
    SELECT t.seq
    FROM albums AS a INDEXED BY albums_artist_id_idx
    JOIN tracks AS t INDEXED BY tracks_album_disc_no_idx ON t.album_id = a.id
    WHERE a.artist_id = sqlc.arg(artist_id) AND a.available = 1 AND t.available = 1
    ORDER BY random()
    LIMIT sqlc.arg(page_size)
)
ORDER BY random();

-- name: GetCatalogSummary :one
-- GetCatalogSummary counts what is available to listen to: the artists
-- that have an available album (those GET /artists lists), the available
-- albums, and their available tracks with the sum of their known
-- durations. The tracks are counted from the counters of their album
-- (track_count and duration_ms, DESIGN.md 5.2), which the scanner keeps in
-- the transaction of the album and the doctor checks: a track that is
-- available is always in an available album, so they are the tracks of the
-- albums of the lists, and 20,000 albums are read instead of 200,000
-- tracks.
SELECT count(DISTINCT albums.artist_id) AS artists, count(*) AS albums,
    CAST(coalesce(sum(albums.track_count), 0) AS INTEGER) AS tracks,
    CAST(coalesce(sum(albums.duration_ms), 0) AS INTEGER) AS duration_ms
FROM albums
WHERE albums.available = 1;

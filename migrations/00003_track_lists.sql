-- The list of the tracks, GET /tracks (docs/proposals/web-client-api.md A1,
-- step W1 of DESIGN.md). Forward only (I13): there is no Down section.
--
-- tracks gets the sort keys of its orders, as albums has them (DESIGN.md
-- 5.5): title_key and artist_key are the collation keys of the title and of
-- the artist of the track, and album_key is a copy of albums.title_key, so
-- that a page reads one index and joins nothing to find its rows.
-- first_seen_at is when Vibrance first saw the audio of the track; a track
-- that MusicLib moves to another album is a new row that keeps the moment
-- of the row it replaces (the scanner, at the end of every cycle).
--
-- SQLite adds a NOT NULL column only with a default. The scanner always
-- writes the four, and the defaults are never what a row keeps:
--   - first_seen_at and album_key of the rows that exist are filled here
--     from their album;
--   - title_key and artist_key are made by the collation of Go, which SQL
--     does not have: the version of the collation is forgotten, and the
--     startup computes every sort key again before the server serves
--     (DESIGN.md T26), these included.

-- +goose Up

ALTER TABLE tracks ADD COLUMN title_key blob NOT NULL DEFAULT x'';
ALTER TABLE tracks ADD COLUMN artist_key blob NOT NULL DEFAULT x'';
ALTER TABLE tracks ADD COLUMN album_key blob NOT NULL DEFAULT x'';
ALTER TABLE tracks ADD COLUMN first_seen_at integer NOT NULL DEFAULT 0;

UPDATE tracks SET
    album_key = (SELECT albums.title_key FROM albums WHERE albums.id = tracks.album_id),
    first_seen_at = (SELECT albums.first_seen_at FROM albums WHERE albums.id = tracks.album_id);

DELETE FROM meta WHERE "key" = 'collate_version';

-- The orders of the list of the tracks, each led by available and ending
-- with the id, so that every order is total and a page is a range of its
-- index.
CREATE INDEX tracks_title_idx ON tracks (available, title_key, id);
CREATE INDEX tracks_artist_idx ON tracks (available, artist_key, album_key, album_id, disc, "no", id);
CREATE INDEX tracks_album_idx ON tracks (available, album_key, album_id, disc, "no", id);
CREATE INDEX tracks_first_seen_idx ON tracks (available, first_seen_at, id);

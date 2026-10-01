-- DESIGN.md §10.1: the full-text tables. They have a migration of their own,
-- which is kept out of sqlc (T7): sqlc.yaml lists every migration but this
-- one, and the queries on these tables are written by hand in
-- internal/search.
--
-- The rowid of a row is the seq of the artist, album or track it describes
-- (T6). Only available rows are here.

-- +goose Up

CREATE VIRTUAL TABLE search_artists USING fts5(name,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3');

CREATE VIRTUAL TABLE search_albums USING fts5(title, artist,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3');

CREATE VIRTUAL TABLE search_tracks USING fts5(title, artist, album,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3');

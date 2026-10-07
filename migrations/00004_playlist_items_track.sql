-- The playlists of the user that hold a track, GET /tracks/{id}/playlists
-- (docs/proposals/web-client-api.md B4, step W3 of DESIGN.md). Forward only
-- (I13): there is no Down section.
--
-- The items of a track, and the playlists they are in, read from the index
-- alone: without it every request reads every item of every user. The
-- scanner uses it too, when the references of a track that is gone follow
-- its audio (sql/references.sql), which read every item before.

-- +goose Up

CREATE INDEX playlist_items_track_idx ON playlist_items (track_id, playlist_id);

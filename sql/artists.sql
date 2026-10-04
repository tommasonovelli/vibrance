-- name: GetArtist :one
-- GetArtist returns an artist by its id, which is the UUIDv5 of the identity
-- key of its name (DESIGN.md 5.4). An unknown id is sql.ErrNoRows.
SELECT * FROM artists WHERE id = ?;

-- name: UpsertArtist :exec
-- UpsertArtist creates an artist, or gives a known one the name and the sort
-- key of the last album that was indexed for it: two names with one identity
-- key (another case, other white space) are one artist. The row is never
-- deleted (I3).
INSERT INTO artists (id, name, sort_key) VALUES (?, ?, ?)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, sort_key = excluded.sort_key;

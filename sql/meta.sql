-- name: GetMeta :one
-- GetMeta returns what the server remembers about itself under a key, such
-- as 'ffmpeg_version' or 'collate_version' (DESIGN.md 5.2). A key that was
-- never set is sql.ErrNoRows.
SELECT value FROM meta WHERE "key" = ?;

-- name: SetMeta :exec
-- SetMeta sets the value of a key, new or not.
INSERT INTO meta ("key", value) VALUES (?, ?)
ON CONFLICT ("key") DO UPDATE SET value = excluded.value;

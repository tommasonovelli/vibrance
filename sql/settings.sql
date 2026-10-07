-- name: GetSettings :one
-- GetSettings returns the preferences a user saved. No row is
-- sql.ErrNoRows: the user never saved any, and has the defaults.
SELECT volume_leveling, single_key_shortcuts, theme FROM settings WHERE user_id = ?;

-- name: PutSettings :exec
-- PutSettings writes every preference of a user, creating the row at the
-- first change.
INSERT INTO settings (user_id, volume_leveling, single_key_shortcuts, theme)
VALUES (sqlc.arg(user_id), sqlc.arg(volume_leveling), sqlc.arg(single_key_shortcuts), sqlc.arg(theme))
ON CONFLICT (user_id) DO UPDATE SET
    volume_leveling = excluded.volume_leveling,
    single_key_shortcuts = excluded.single_key_shortcuts,
    theme = excluded.theme;

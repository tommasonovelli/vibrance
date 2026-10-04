-- name: CreateSession :exec
-- CreateSession starts a session. token_hash is the SHA-256 of the token in
-- hexadecimal: the token itself is never stored (I5).
INSERT INTO sessions (id, token_hash, user_id, kind, device_name, created_at, last_used_at, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(device_name),
        sqlc.arg(created_at), sqlc.arg(created_at), sqlc.arg(expires_at));

-- name: GetSessionByTokenHash :one
-- GetSessionByTokenHash returns the session of a token with the role of its
-- user and whether the account is disabled. No row is sql.ErrNoRows. The
-- caller checks the expiry, with its own clock.
SELECT s.id, s.token_hash, s.user_id, s.kind, s.last_used_at, s.expires_at, u.role, u.disabled
FROM sessions AS s
JOIN users AS u ON u.id = s.user_id
WHERE s.token_hash = ?;

-- name: TouchSession :exec
-- TouchSession records that a session was used, and renews it. Neither
-- value ever goes back: of two requests that write at once, the later
-- time stays.
UPDATE sessions
SET last_used_at = max(last_used_at, CAST(sqlc.arg(last_used_at) AS integer)),
    expires_at = max(expires_at, CAST(sqlc.arg(expires_at) AS integer))
WHERE id = sqlc.arg(id);

-- name: ListSessionsOfUser :many
-- ListSessionsOfUser returns the sessions of a user that have not expired
-- at the given time, the newest first.
SELECT * FROM sessions
WHERE user_id = sqlc.arg(user_id) AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id DESC;

-- name: DeleteSession :execrows
-- DeleteSession revokes one session of a user. A session of another user
-- is not touched: the caller answers as if it did not exist (I6).
DELETE FROM sessions WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: DeleteSessionsOfUser :execrows
-- DeleteSessionsOfUser revokes every session of a user.
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteOtherSessionsOfUser :execrows
-- DeleteOtherSessionsOfUser revokes every session of a user but one, the
-- one that asked for a change of password.
DELETE FROM sessions WHERE user_id = sqlc.arg(user_id) AND id <> sqlc.arg(id);

-- name: DeleteExpiredSessions :execrows
-- DeleteExpiredSessions removes the sessions that expired at the given time
-- or before it.
DELETE FROM sessions WHERE expires_at <= ?;

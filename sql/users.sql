-- name: CountUsers :one
-- CountUsers tells whether the server has any account: the first admin is
-- created only when it has none (DESIGN.md 7.4).
SELECT count(*) FROM users;

-- name: CreateUser :exec
-- CreateUser adds an account that is not disabled. password_hash is a PHC
-- argon2id string, never a password (I5).
INSERT INTO users (id, username, password_hash, role, disabled, created_at, password_changed_at)
VALUES (sqlc.arg(id), sqlc.arg(username), sqlc.arg(password_hash), sqlc.arg(role), 0,
        sqlc.arg(created_at), sqlc.arg(created_at));

-- name: GetUser :one
-- GetUser returns an account by its id. No row is sql.ErrNoRows.
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
-- GetUserByUsername returns an account by its name, which is stored in
-- lower case: the caller lowers what the client sent (DESIGN.md 7.1).
SELECT * FROM users WHERE username = ?;

-- name: ListUsers :many
-- ListUsers returns every account, by name.
SELECT * FROM users ORDER BY username;

-- name: SetUserPassword :execrows
-- SetUserPassword replaces the password hash of an account. The caller
-- revokes the sessions the change must end, in the same transaction.
UPDATE users SET password_hash = sqlc.arg(password_hash), password_changed_at = sqlc.arg(password_changed_at)
WHERE id = sqlc.arg(id);

-- name: GetUsers :many
SELECT id, name FROM users ORDER BY id;

-- name: CreateUser :one
INSERT INTO users (name) VALUES ($1) RETURNING id, name;

-- name: ListUsersAfter :many
SELECT id, name FROM users WHERE id > $1 ORDER BY id LIMIT $2;

-- name: GetUser :one
SELECT id, name FROM users WHERE id = $1;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;

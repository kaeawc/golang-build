-- name: GetUsers :many
SELECT id, name FROM users ORDER BY id;

-- name: CreateUser :one
INSERT INTO users (name) VALUES ($1) RETURNING id, name;

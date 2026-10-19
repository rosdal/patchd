-- name: GetOrg :one
SELECT * FROM orgs WHERE id = $1;

-- name: CreateOrg :one
INSERT INTO orgs (name) VALUES ($1) RETURNING *;

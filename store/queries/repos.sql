-- name: GetRepoByName :one
SELECT * FROM repos WHERE full_name = $1;

-- name: ListReposByOrg :many
SELECT * FROM repos WHERE org_id = $1 ORDER BY full_name;

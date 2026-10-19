-- name: CreateReview :one
INSERT INTO reviews (repo_id, pr_number, head_sha, status)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetReview :one
SELECT * FROM reviews WHERE id = $1;

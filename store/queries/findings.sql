-- name: ListFindingsByReview :many
SELECT * FROM findings WHERE review_id = $1 ORDER BY path, line;

-- name: CreateFinding :one
INSERT INTO findings (review_id, rule_id, path, line, severity, evidence)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

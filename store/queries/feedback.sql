-- name: CreateFeedback :one
INSERT INTO feedback (finding_id, vote) VALUES ($1, $2) RETURNING *;

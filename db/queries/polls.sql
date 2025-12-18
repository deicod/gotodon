-- name: CreatePoll :one
INSERT INTO polls (account_id, status_id, expires_at, options, cached_tallies, multiple, votes_count, voters_count, hide_totals)
VALUES ($1, $2, $3, $4, $5, $6, 0, 0, $7)
RETURNING *;

-- name: GetPoll :one
SELECT * FROM polls WHERE id = $1;

-- name: GetPollByStatusId :one
SELECT * FROM polls WHERE status_id = $1;

-- name: Vote :exec
INSERT INTO poll_votes (poll_id, account_id, choice)
VALUES ($1, $2, $3)
ON CONFLICT (poll_id, account_id, choice) DO NOTHING;

-- name: GetVotes :many
SELECT * FROM poll_votes WHERE poll_id = $1 AND account_id = $2;

-- name: GetAllVotes :many
SELECT * FROM poll_votes WHERE poll_id = $1;

-- name: IncrementVoteCount :exec
UPDATE polls SET
    votes_count = votes_count + 1,
    cached_tallies = jsonb_set(
        cached_tallies,
        ARRAY[$2::text],
        to_jsonb(COALESCE((cached_tallies->>$2::text)::int, 0) + 1)
    ),
    updated_at = now()
WHERE id = $1;

-- name: IncrementVotersCount :exec
UPDATE polls SET voters_count = voters_count + 1, updated_at = now()
WHERE id = $1;

-- name: GetPollOptions :many
SELECT 
    ordinality - 1 AS option_index,
    option_text AS title,
    COALESCE((p.cached_tallies->>((ordinality - 1)::text))::bigint, 0) AS votes_count
FROM polls p,
LATERAL unnest(p.options) WITH ORDINALITY AS opt(option_text, ordinality)
WHERE p.id = $1
ORDER BY ordinality;

-- name: GetPollVotesByAccountID :many
SELECT * FROM poll_votes 
WHERE poll_id = $1 AND account_id = $2;


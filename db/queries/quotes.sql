-- Quotes queries
-- name: GetQuote :one
SELECT * FROM quotes WHERE id = $1;

-- name: GetQuoteByStatusID :one
SELECT * FROM quotes WHERE status_id = $1;

-- name: GetQuotesByQuotedStatusID :many
SELECT * FROM quotes 
WHERE quoted_status_id = $1 AND state = 0
ORDER BY id DESC
LIMIT $2 OFFSET $3;

-- name: CreateQuote :one
INSERT INTO quotes (
    account_id, status_id, quoted_status_id, quoted_account_id, 
    state, approval_uri, activity_uri, legacy
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: UpdateQuoteState :one
UPDATE quotes SET state = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteQuote :exec
DELETE FROM quotes WHERE id = $1;

-- name: GetQuoteByActivityURI :one
SELECT * FROM quotes WHERE activity_uri = $1;

-- name: CountQuotesByQuotedStatus :one
SELECT COUNT(*) FROM quotes WHERE quoted_status_id = $1 AND state = 0;

-- name: GetQuotesByStatusIDs :many
SELECT * FROM quotes WHERE status_id = ANY(@status_ids::bigint[]) AND state = 0;


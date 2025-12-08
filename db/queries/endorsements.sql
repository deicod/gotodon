-- name: CreateAccountPin :one
INSERT INTO account_pins (account_id, target_account_id)
VALUES ($1, $2)
RETURNING *;

-- name: DeleteAccountPin :exec
DELETE FROM account_pins
WHERE account_id = $1 AND target_account_id = $2;

-- name: ListAccountPins :many
SELECT ap.*, 
       a.username, a.domain, a.display_name, a.avatar_file_name, a.avatar_remote_url, a.header_file_name, a.header_remote_url, a.note, a.uri, a.url
FROM account_pins ap
JOIN accounts a ON ap.target_account_id = a.id
WHERE ap.account_id = $1
ORDER BY ap.id DESC;

-- name: CountAccountPins :one
SELECT count(*) FROM account_pins WHERE account_id = $1;

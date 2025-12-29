-- name: CreateList :one
INSERT INTO lists (account_id, title, replies_policy, exclusive)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetList :one
SELECT * FROM lists WHERE id = $1 AND account_id = $2;

-- name: ListLists :many
SELECT * FROM lists WHERE account_id = $1 ORDER BY id DESC;

-- name: UpdateList :one
UPDATE lists SET
    title = COALESCE(sqlc.narg('title'), title),
    replies_policy = COALESCE(sqlc.narg('replies_policy'), replies_policy),
    exclusive = COALESCE(sqlc.narg('exclusive'), exclusive),
    updated_at = now()
WHERE id = $1 AND account_id = $2
RETURNING *;

-- name: DeleteList :exec
DELETE FROM lists WHERE id = $1 AND account_id = $2;

-- name: AddAccountToList :exec
INSERT INTO list_accounts (list_id, account_id, follow_id)
VALUES ($1, $2, $3)
ON CONFLICT (account_id, list_id) DO NOTHING;

-- name: RemoveAccountFromList :exec
DELETE FROM list_accounts WHERE list_id = $1 AND account_id = $2;

-- name: ListAccountsInList :many
SELECT a.* FROM accounts a
INNER JOIN list_accounts la ON la.account_id = a.id
WHERE la.list_id = $1
ORDER BY la.id DESC
LIMIT $2 OFFSET $3;

-- name: GetListsContainingAccount :many
SELECT l.* FROM lists l
INNER JOIN list_accounts la ON la.list_id = l.id
WHERE l.account_id = $1 AND la.account_id = $2;

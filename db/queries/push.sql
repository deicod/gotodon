-- name: CreatePushSubscription :one
INSERT INTO web_push_subscriptions (
    endpoint, key_p256dh, key_auth, data, user_id, access_token_id, standard
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: GetPushSubscription :one
SELECT * FROM web_push_subscriptions 
WHERE access_token_id = $1 OR user_id = $2
ORDER BY id DESC LIMIT 1;

-- name: UpdatePushSubscription :one
UPDATE web_push_subscriptions
SET data = $2,
    updated_at = now()
WHERE access_token_id = $1
RETURNING *;

-- name: UpdatePushSubscriptionDetails :one
UPDATE web_push_subscriptions
SET endpoint = $2,
    key_p256dh = $3,
    key_auth = $4,
    data = $5,
    user_id = $6,
    standard = $7,
    updated_at = now()
WHERE access_token_id = $1
RETURNING *;

-- name: GetPushSubscriptionByID :one
SELECT * FROM web_push_subscriptions WHERE id = $1;

-- name: GetPushSubscriptionByEndpoint :one
SELECT * FROM web_push_subscriptions WHERE endpoint = $1;

-- name: DeletePushSubscription :exec
DELETE FROM web_push_subscriptions WHERE access_token_id = $1;

-- name: DeletePushSubscriptionByID :exec
DELETE FROM web_push_subscriptions WHERE id = $1;

-- name: UpdatePushSubscriptionByID :one
UPDATE web_push_subscriptions
SET data = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListPushSubscriptionsByUserID :many
SELECT * FROM web_push_subscriptions WHERE user_id = $1 ORDER BY id;

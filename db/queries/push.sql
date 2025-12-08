-- name: CreatePushSubscription :one
INSERT INTO web_push_subscriptions (
    endpoint, key_p256dh, key_auth, data, user_id, access_token_id
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetPushSubscription :one
SELECT * FROM web_push_subscriptions 
WHERE access_token_id = $1 OR user_id = $2
ORDER BY id DESC LIMIT 1;

-- name: GetPushSubscriptionByEndpoint :one
SELECT * FROM web_push_subscriptions WHERE endpoint = $1;

-- name: DeletePushSubscription :exec
DELETE FROM web_push_subscriptions WHERE access_token_id = $1;

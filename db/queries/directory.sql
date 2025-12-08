-- name: ListDeepDirectory :many
SELECT a.* 
FROM accounts a
LEFT JOIN account_stats s ON a.id = s.account_id
WHERE a.suspended_at IS NULL
  AND a.discoverable = true
  AND (CASE WHEN @local::boolean THEN a.domain IS NULL ELSE TRUE END)
ORDER BY 
    CASE WHEN @order_active::boolean THEN s.last_status_at ELSE NULL END DESC NULLS LAST,
    CASE WHEN @order_new::boolean THEN a.created_at ELSE NULL END DESC
LIMIT $1 OFFSET $2;

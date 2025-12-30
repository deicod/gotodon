-- name: GetFollowSuggestions :many
SELECT a.*
FROM accounts a
LEFT JOIN follows f ON f.target_account_id = a.id AND f.account_id = $1
LEFT JOIN follow_requests fr ON fr.target_account_id = a.id AND fr.account_id = $1
LEFT JOIN follow_recommendation_mutes frm ON frm.account_id = $1 AND frm.target_account_id = a.id
LEFT JOIN blocks b ON b.account_id = $1 AND b.target_account_id = a.id
LEFT JOIN blocks bb ON bb.account_id = a.id AND bb.target_account_id = $1
LEFT JOIN mutes m ON m.account_id = $1 AND m.target_account_id = a.id
LEFT JOIN account_domain_blocks adb ON adb.account_id = $1 AND adb.domain = a.domain
WHERE a.id != $1
  AND f.id IS NULL -- Not already followed
  AND fr.id IS NULL -- Not already requested
  AND frm.id IS NULL -- Not dismissed
  AND b.id IS NULL -- Not blocked
  AND bb.id IS NULL -- Not blocked by
  AND m.id IS NULL -- Not muted
  AND adb.id IS NULL -- Not domain blocked
  AND a.locked = false
  AND a.discoverable = true
  AND a.suspended_at IS NULL
ORDER BY a.created_at DESC -- Suggest new users for now, ideally by popularity
LIMIT $2 OFFSET $3;

-- name: IsFollowRecommendationSuppressed :one
SELECT EXISTS(
    SELECT 1 FROM follow_recommendation_suppressions WHERE account_id = $1
) AS suppressed;

-- name: CreateFollowRecommendationMute :exec
INSERT INTO follow_recommendation_mutes (account_id, target_account_id)
VALUES ($1, $2)
ON CONFLICT (account_id, target_account_id) DO NOTHING;

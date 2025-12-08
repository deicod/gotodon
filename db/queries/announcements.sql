-- name: ListAnnouncements :many
SELECT a.* 
FROM announcements a
LEFT JOIN announcement_mutes am ON a.id = am.announcement_id AND am.account_id = $1
WHERE (am.id IS NULL OR @with_dismissed::boolean)
  AND a.published = true
  AND (a.starts_at IS NULL OR a.starts_at < now())
  AND (a.ends_at IS NULL OR a.ends_at > now())
ORDER BY a.created_at DESC;

-- name: DismissAnnouncement :exec
INSERT INTO announcement_mutes (account_id, announcement_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: AddAnnouncementReaction :one
INSERT INTO announcement_reactions (account_id, announcement_id, name, custom_emoji_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RemoveAnnouncementReaction :exec
DELETE FROM announcement_reactions
WHERE account_id = $1 AND announcement_id = $2 AND name = $3;

-- name: ListAnnouncementReactions :many
SELECT * FROM announcement_reactions 
WHERE announcement_id = $1;

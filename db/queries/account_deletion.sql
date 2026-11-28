-- Queries of the account deletion service (Mastodon's DeleteAccountService).
-- Each delete takes at most sqlc.arg('limit') rows, so a large account is
-- purged in short statements, and each can be run again: a run that stops
-- part way leaves only rows that a later run deletes.

-- name: ClaimAccountDeletion :one
-- The account, if its deletion is still asked for: it is suspended and has
-- its deletion request. Each step of a deletion runs it first, in its
-- transaction. It locks the account and the request until the transaction
-- ends, so an unsuspension, which updates the account and deletes the
-- request, waits for the step, and the next step finds no row.
SELECT a.id FROM accounts a
JOIN account_deletion_requests d ON d.account_id = a.id
WHERE a.id = $1 AND a.suspended_at IS NOT NULL
LIMIT 1
FOR SHARE OF a, d;

-- name: ListUnresolvedReportedStatusIDs :many
-- The statuses that unresolved reports about an account name
-- (DeleteAccountService#reported_status_ids). Deletion keeps them, with
-- their mentions, media and polls, for the moderators handling the reports.
SELECT DISTINCT reported.status_id::bigint AS status_id
FROM reports, unnest(reports.status_ids) AS reported(status_id)
WHERE reports.target_account_id = $1
  AND reports.action_taken_at IS NULL
ORDER BY 1;

-- name: ListAccountStatusesForDeletion :many
-- An account's statuses, discarded ones included, in id order after after_id,
-- leaving out keep_ids.
SELECT * FROM statuses
WHERE account_id = @account_id
  AND id > @after_id
  AND NOT (id = ANY(COALESCE(@keep_ids::bigint[], '{}')))
ORDER BY id
LIMIT sqlc.arg('limit');

-- name: DecrementRepliesOfDeletedStatuses :exec
-- Takes the live replies among the account's statuses ids out of the
-- replies_count of the statuses of other accounts that they answer. A
-- discarded reply was taken out when it was discarded.
WITH replies AS (
    SELECT s.in_reply_to_id AS status_id, count(*)::bigint AS n
    FROM statuses s
    JOIN statuses parent ON parent.id = s.in_reply_to_id
    WHERE s.id = ANY(@ids::bigint[])
      AND s.account_id = @account_id
      AND s.deleted_at IS NULL
      AND parent.account_id <> @account_id
    GROUP BY s.in_reply_to_id
)
UPDATE status_stats
SET replies_count = GREATEST(0, status_stats.replies_count - replies.n), updated_at = now()
FROM replies
WHERE status_stats.status_id = replies.status_id;

-- name: DeleteAccountStatusesByIDs :execrows
-- Deletes the account's statuses ids. Reblogs of them, their stats, pins,
-- favourites, bookmarks, mentions, tags, polls, edits and trends go with
-- them by foreign key; their media attachments are kept, unattached, for
-- the media purge.
DELETE FROM statuses
WHERE account_id = @account_id AND id = ANY(@ids::bigint[]);

-- name: DeleteAccountMentionsBatch :execrows
-- Mentions of the account in other statuses, but in keep_status_ids.
DELETE FROM mentions
WHERE id IN (
    SELECT m.id FROM mentions m
    WHERE m.account_id = @account_id
      AND NOT (m.status_id = ANY(COALESCE(@keep_status_ids::bigint[], '{}')))
    LIMIT sqlc.arg('limit')
);

-- name: ListAccountMediaAttachmentsForDeletion :many
-- The account's media attachments, in id order after after_id, but those of
-- keep_status_ids.
SELECT * FROM media_attachments
WHERE account_id = @account_id::bigint
  AND id > @after_id
  AND (status_id IS NULL OR NOT (status_id = ANY(COALESCE(@keep_status_ids::bigint[], '{}'))))
ORDER BY id
LIMIT sqlc.arg('limit');

-- name: DeleteAccountPollsBatch :execrows
-- The account's polls, but those of keep_status_ids. The polls of deleted
-- statuses are gone with them; this takes the rest.
DELETE FROM polls
WHERE id IN (
    SELECT p.id FROM polls p
    WHERE p.account_id = @account_id::bigint
      AND (p.status_id IS NULL OR NOT (p.status_id = ANY(COALESCE(@keep_status_ids::bigint[], '{}'))))
    LIMIT sqlc.arg('limit')
);

-- name: DeleteAccountNotificationsBatch :execrows
-- The account's notifications and the notifications it caused.
DELETE FROM notifications
WHERE id IN (
    SELECT n.id FROM notifications n
    WHERE n.account_id = @account_id OR n.from_account_id = @account_id
    LIMIT sqlc.arg('limit')
);

-- name: DeleteAccountNotificationRequestsBatch :execrows
-- The account's notification requests and those about it.
DELETE FROM notification_requests
WHERE id IN (
    SELECT r.id FROM notification_requests r
    WHERE r.account_id = @account_id OR r.from_account_id = @account_id
    LIMIT sqlc.arg('limit')
);

-- name: DeleteAccountFavouritesBatch :one
-- Deletes favourites of the account and takes them out of the
-- favourites_count of the statuses (DeleteAccountService#purge_favourites!).
-- It returns how many it deleted.
WITH batch AS (
    SELECT f.id FROM favourites f
    WHERE f.account_id = @account_id
    LIMIT sqlc.arg('limit')
), deleted AS (
    DELETE FROM favourites
    WHERE favourites.id IN (SELECT batch.id FROM batch)
    RETURNING favourites.status_id
), counted AS (
    SELECT deleted.status_id, count(*)::bigint AS n
    FROM deleted
    GROUP BY deleted.status_id
), decremented AS (
    UPDATE status_stats
    SET favourites_count = GREATEST(0, status_stats.favourites_count - counted.n), updated_at = now()
    FROM counted
    WHERE status_stats.status_id = counted.status_id
    RETURNING status_stats.id
)
SELECT count(*)::bigint FROM deleted;

-- name: DeleteAccountBookmarksBatch :execrows
DELETE FROM bookmarks
WHERE id IN (
    SELECT b.id FROM bookmarks b WHERE b.account_id = @account_id LIMIT sqlc.arg('limit')
);

-- name: DeleteAccountFollowsBatch :execrows
-- Follows by the account and of it.
DELETE FROM follows
WHERE id IN (
    SELECT f.id FROM follows f
    WHERE f.account_id = @account_id OR f.target_account_id = @account_id
    LIMIT sqlc.arg('limit')
);

-- name: DeleteAccountFollowRequestsBatch :execrows
-- Follow requests by the account and of it.
DELETE FROM follow_requests
WHERE id IN (
    SELECT r.id FROM follow_requests r
    WHERE r.account_id = @account_id OR r.target_account_id = @account_id
    LIMIT sqlc.arg('limit')
);

-- name: DeleteAccountBlocksBatch :execrows
-- Blocks by the account and of it.
DELETE FROM blocks
WHERE id IN (
    SELECT b.id FROM blocks b
    WHERE b.account_id = @account_id OR b.target_account_id = @account_id
    LIMIT sqlc.arg('limit')
);

-- name: DeleteAccountMutesBatch :execrows
-- Mutes by the account and of it.
DELETE FROM mutes
WHERE id IN (
    SELECT m.id FROM mutes m
    WHERE m.account_id = @account_id OR m.target_account_id = @account_id
    LIMIT sqlc.arg('limit')
);

-- name: DeleteAccountConversationsBatch :execrows
-- The account's own direct conversations.
DELETE FROM account_conversations
WHERE id IN (
    SELECT c.id FROM account_conversations c WHERE c.account_id = @account_id LIMIT sqlc.arg('limit')
);

-- name: PurgeAccountAssociations :exec
-- Deletes what else the account owns and its own server keeps of it
-- (DeleteAccountService::ASSOCIATIONS_ON_SUSPEND): its notes about other
-- accounts, endorsements, aliases, conversation mutes, filters, domain
-- blocks, featured tags, list memberships, migrations, lists, report notes,
-- scheduled statuses, status pins, followed tags, notification permissions
-- and status trends. Its status cleanup policy goes too, so the cleanup
-- worker can't delete the reported statuses that deletion keeps. Notes,
-- endorsements and lists of other accounts about it are theirs and are
-- kept. These are short per account, so one statement takes them all.
WITH account_notes_deleted AS (
    DELETE FROM account_notes WHERE account_notes.account_id = $1 RETURNING 1
), account_pins_deleted AS (
    DELETE FROM account_pins WHERE account_pins.account_id = $1 RETURNING 1
), account_aliases_deleted AS (
    DELETE FROM account_aliases WHERE account_aliases.account_id = $1 RETURNING 1
), conversation_mutes_deleted AS (
    DELETE FROM conversation_mutes WHERE conversation_mutes.account_id = $1 RETURNING 1
), custom_filters_deleted AS (
    DELETE FROM custom_filters WHERE custom_filters.account_id = $1 RETURNING 1
), account_domain_blocks_deleted AS (
    DELETE FROM account_domain_blocks WHERE account_domain_blocks.account_id = $1 RETURNING 1
), featured_tags_deleted AS (
    DELETE FROM featured_tags WHERE featured_tags.account_id = $1 RETURNING 1
), list_accounts_deleted AS (
    DELETE FROM list_accounts WHERE list_accounts.account_id = $1 RETURNING 1
), account_migrations_deleted AS (
    DELETE FROM account_migrations WHERE account_migrations.account_id = $1 RETURNING 1
), lists_deleted AS (
    DELETE FROM lists WHERE lists.account_id = $1 RETURNING 1
), report_notes_deleted AS (
    DELETE FROM report_notes WHERE report_notes.account_id = $1 RETURNING 1
), scheduled_statuses_deleted AS (
    DELETE FROM scheduled_statuses WHERE scheduled_statuses.account_id = $1 RETURNING 1
), status_pins_deleted AS (
    DELETE FROM status_pins WHERE status_pins.account_id = $1 RETURNING 1
), tag_follows_deleted AS (
    DELETE FROM tag_follows WHERE tag_follows.account_id = $1 RETURNING 1
), cleanup_policies_deleted AS (
    DELETE FROM account_statuses_cleanup_policies
    WHERE account_statuses_cleanup_policies.account_id = $1
    RETURNING 1
), notification_permissions_deleted AS (
    DELETE FROM notification_permissions
    WHERE notification_permissions.account_id = $1 OR notification_permissions.from_account_id = $1
    RETURNING 1
)
DELETE FROM status_trends WHERE status_trends.account_id = $1;

-- name: ListLocalFollowsOfAccount :many
-- The follows that local accounts have of an account, in id order after
-- after_id.
SELECT f.* FROM follows f
JOIN accounts a ON a.id = f.account_id
WHERE f.target_account_id = sqlc.arg('account_id')
  AND a.domain IS NULL
  AND f.id > sqlc.arg('after_id')
ORDER BY f.id
LIMIT sqlc.arg('limit');

-- name: ListRemoteInboxes :many
-- Every inbox of a remote account that isn't suspended, a shared inbox in
-- place of the inboxes it serves (Account.inboxes), in order after after.
SELECT DISTINCT COALESCE(NULLIF(a.shared_inbox_url, ''), a.inbox_url)::text AS inbox
FROM accounts a
WHERE a.domain IS NOT NULL
  AND a.suspended_at IS NULL
  AND COALESCE(NULLIF(a.shared_inbox_url, ''), a.inbox_url) <> ''
  AND COALESCE(NULLIF(a.shared_inbox_url, ''), a.inbox_url) > sqlc.arg('after')::text
ORDER BY 1
LIMIT sqlc.arg('limit');

-- name: PurgeAccountProfile :exec
-- Empties the profile of an account whose record deletion keeps
-- (DeleteAccountService#purge_profile!): the account stays, suspended
-- by this server, under its username, URI and keys, with no profile, no
-- images and no counts.
WITH purged AS (
    UPDATE accounts SET
        silenced_at = NULL,
        suspended_at = COALESCE(accounts.suspended_at, now()),
        suspension_origin = 0,
        locked = false,
        memorial = false,
        discoverable = false,
        indexable = false,
        trendable = false,
        display_name = '',
        note = '',
        fields = '[]'::jsonb,
        moved_to_account_id = NULL,
        reviewed_at = NULL,
        requested_review_at = NULL,
        also_known_as = '{}',
        avatar_file_name = '',
        avatar_content_type = '',
        avatar_file_size = NULL,
        avatar_updated_at = NULL,
        avatar_remote_url = '',
        avatar_storage_schema_version = NULL,
        header_file_name = '',
        header_content_type = '',
        header_file_size = NULL,
        header_updated_at = NULL,
        header_remote_url = '',
        header_storage_schema_version = NULL,
        updated_at = now()
    WHERE accounts.id = $1
    RETURNING accounts.id
)
UPDATE account_stats SET
    statuses_count = 0,
    following_count = 0,
    followers_count = 0,
    updated_at = now()
WHERE account_stats.account_id IN (SELECT purged.id FROM purged);

-- name: DisableAccountDeletionUser :exec
-- Disables the user of a local account that is being deleted, deletes its
-- unused invites (User#disable!, invites.where(uses: 0).destroy_all) and
-- ends its sign-ins: its access tokens and grants are revoked, its sessions
-- and Web Push subscriptions deleted.
WITH disabled AS (
    UPDATE users SET disabled = true, updated_at = now()
    WHERE users.account_id = $1
    RETURNING users.id
), invites_deleted AS (
    DELETE FROM invites
    WHERE invites.user_id IN (SELECT disabled.id FROM disabled) AND invites.uses = 0
    RETURNING 1
), tokens_revoked AS (
    UPDATE oauth_access_tokens SET revoked_at = now()
    WHERE oauth_access_tokens.resource_owner_id IN (SELECT disabled.id FROM disabled)
      AND oauth_access_tokens.revoked_at IS NULL
    RETURNING 1
), grants_revoked AS (
    UPDATE oauth_access_grants SET revoked_at = now()
    WHERE oauth_access_grants.resource_owner_id IN (SELECT disabled.id FROM disabled)
      AND oauth_access_grants.revoked_at IS NULL
    RETURNING 1
), sessions_deleted AS (
    DELETE FROM session_activations
    WHERE session_activations.user_id IN (SELECT disabled.id FROM disabled)
    RETURNING 1
)
DELETE FROM web_push_subscriptions
WHERE web_push_subscriptions.user_id IN (SELECT disabled.id FROM disabled);

-- name: DeleteUserByAccountID :exec
DELETE FROM users WHERE account_id = $1;

-- name: DeleteAccount :exec
-- Deletes an account's record and, by foreign key, everything that is left
-- of it: its user, statuses, reports about it and moderation history.
DELETE FROM accounts WHERE id = $1;

-- name: DeleteAccountDeletionRequest :exec
DELETE FROM account_deletion_requests WHERE account_id = $1;

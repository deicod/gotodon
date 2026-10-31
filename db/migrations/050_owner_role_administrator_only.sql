-- Migration 050: Store the Owner Role as Administrator Only
-- Gives the Owner role seeded by 001_initial_schema.sql the permissions
-- Mastodon's config/roles.yml gives it: the administrator bit alone, which
-- implies every other permission.
--
-- Migration 047 kept Owner at -1 (every bit set) because the v1 APIs reported
-- the stored permissions, and clients test individual bits in them. Every API
-- now reports computed permissions, as Mastodon's REST::RoleSerializer does:
-- every flag for a role with the administrator bit. So the Owner reports
-- 8388607 (auth.PermissionAll) whether it stores -1 or 1, and the
-- authorization checks (auth.HasPermission and the SQL permission filters)
-- treat both the same way.
--
-- Only an Owner that still holds the seed value is changed, so an Owner an
-- instance has customized is left alone. The down migration mirrors the guard
-- and only reverts an Owner that still holds 1.
--
-- Owner: -1 -> 1
--     PermissionAdministrator         1 << 0  =       1

-- +goose Up

UPDATE user_roles SET permissions = 1, updated_at = now()
    WHERE name = 'Owner' AND permissions = -1;

-- +goose Down

UPDATE user_roles SET permissions = -1, updated_at = now()
    WHERE name = 'Owner' AND permissions = 1;

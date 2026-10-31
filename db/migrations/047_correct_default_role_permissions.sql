-- Migration 047: Correct Default Role Permissions
-- Aligns the Admin and Moderator roles seeded by 001_initial_schema.sql with
-- Mastodon's config/roles.yml. The original seed gave both the administrator
-- bit, which implies every permission.
--
-- Only rows that still hold the original seed values are changed, so roles an
-- instance has customized are left alone. The down migration mirrors the guard
-- and only reverts rows that still hold the new defaults.
--
-- Owner keeps -1. Mastodon stores 1 (administrator), but the v1 credential
-- account and admin account APIs report the stored value, not the computed
-- permissions Mastodon reports, and clients test individual bits in it. -1 sets
-- every bit, 1 would set none of the ones clients check. Owner moves to 1 once
-- the APIs report computed permissions (#109).
--
-- Values are sums of the Permission* constants in internal/auth/permissions.go.
--
-- Admin: 983103 -> 2031612
--     PermissionViewAuditLog          1 << 2  =       4
--   + PermissionViewDashboard         1 << 3  =       8
--   + PermissionManageReports         1 << 4  =      16
--   + PermissionManageFederation      1 << 5  =      32
--   + PermissionManageSettings        1 << 6  =      64
--   + PermissionManageBlocks          1 << 7  =     128
--   + PermissionManageTaxonomies      1 << 8  =     256
--   + PermissionManageAppeals         1 << 9  =     512
--   + PermissionManageUsers           1 << 10 =    1024
--   + PermissionManageInvites         1 << 11 =    2048
--   + PermissionManageRules           1 << 12 =    4096
--   + PermissionManageAnnouncements   1 << 13 =    8192
--   + PermissionManageCustomEmojis    1 << 14 =   16384
--   + PermissionManageWebhooks        1 << 15 =   32768
--   + PermissionManageRoles           1 << 17 =  131072
--   + PermissionManageUserAccess      1 << 18 =  262144
--   + PermissionDeleteUserData        1 << 19 =  524288
--   + PermissionViewFeeds             1 << 20 = 1048576
--                                             = 2031612
--
-- Moderator: 9423 -> 1049884
--     PermissionViewAuditLog          1 << 2  =       4
--   + PermissionViewDashboard         1 << 3  =       8
--   + PermissionManageReports         1 << 4  =      16
--   + PermissionManageTaxonomies      1 << 8  =     256
--   + PermissionManageUsers           1 << 10 =    1024
--   + PermissionViewFeeds             1 << 20 = 1048576
--                                             = 1049884
--
-- Previous seed values, restored by the down migration:
--   Admin  983103 = Administrator + ViewDevops + ViewAuditLog + ViewDashboard
--                 + ManageReports + ManageFederation + InviteUsers + ManageRoles
--                 + ManageUserAccess + DeleteUserData
--   Moderator 9423 = Administrator + ViewDevops + ViewAuditLog + ViewDashboard
--                  + ManageSettings + ManageBlocks + ManageUsers
--                  + ManageAnnouncements

-- +goose Up

UPDATE user_roles SET permissions = 2031612, updated_at = now()
    WHERE name = 'Admin' AND permissions = 983103;
UPDATE user_roles SET permissions = 1049884, updated_at = now()
    WHERE name = 'Moderator' AND permissions = 9423;

-- +goose Down

UPDATE user_roles SET permissions = 983103, updated_at = now()
    WHERE name = 'Admin' AND permissions = 2031612;
UPDATE user_roles SET permissions = 9423, updated_at = now()
    WHERE name = 'Moderator' AND permissions = 1049884;

package auth

// User role permission flags, mirroring Mastodon's UserRole::FLAGS bitmask.
const (
	PermissionAdministrator            int64 = 1 << 0
	PermissionViewDevops               int64 = 1 << 1
	PermissionViewAuditLog             int64 = 1 << 2
	PermissionViewDashboard            int64 = 1 << 3
	PermissionManageReports            int64 = 1 << 4
	PermissionManageFederation         int64 = 1 << 5
	PermissionManageSettings           int64 = 1 << 6
	PermissionManageBlocks             int64 = 1 << 7
	PermissionManageTaxonomies         int64 = 1 << 8
	PermissionManageAppeals            int64 = 1 << 9
	PermissionManageUsers              int64 = 1 << 10
	PermissionManageInvites            int64 = 1 << 11
	PermissionManageRules              int64 = 1 << 12
	PermissionManageAnnouncements      int64 = 1 << 13
	PermissionManageCustomEmojis       int64 = 1 << 14
	PermissionManageWebhooks           int64 = 1 << 15
	PermissionInviteUsers              int64 = 1 << 16
	PermissionManageRoles              int64 = 1 << 17
	PermissionManageUserAccess         int64 = 1 << 18
	PermissionDeleteUserData           int64 = 1 << 19
	PermissionViewFeeds                int64 = 1 << 20
	PermissionInviteBypassApproval     int64 = 1 << 21
	PermissionManageEmailSubscriptions int64 = 1 << 22
)

// PermissionAll is every permission flag, Mastodon's UserRole::Flags::ALL.
const PermissionAll int64 = (1 << 23) - 1

// PermissionDefault is the permissions of Mastodon's everyone role as it is
// seeded, UserRole::Flags::DEFAULT. Mastodon gives the everyone role to every
// user without a role and adds its permissions to every other role's.
//
// Mastodon stores the everyone role as a row (id -99) whose permissions an
// admin can edit. gotodon has no role management to edit it with, so it keeps
// no row and the everyone role always holds these permissions.
const PermissionDefault int64 = PermissionInviteUsers

// ComputedPermissions returns the effective permissions of a role that stores
// the given bits, as Mastodon's UserRole#computed_permissions does: every flag
// when the administrator bit is set, otherwise the stored bits plus the
// everyone role's, PermissionDefault. For the everyone role itself, which
// stores no bits here, that is PermissionDefault.
func ComputedPermissions(stored int64) int64 {
	if stored&PermissionAdministrator == PermissionAdministrator {
		return PermissionAll
	}
	return stored | PermissionDefault
}

// HasPermission reports whether a role storing the given bits has any of the
// given permissions, as Mastodon's UserRole#can? does, from its computed
// permissions: the administrator bit implies every permission, and every role
// holds PermissionDefault. With no permissions given it reports false.
func HasPermission(stored int64, anyOf ...int64) bool {
	computed := ComputedPermissions(stored)
	for _, permission := range anyOf {
		if permission != 0 && computed&permission == permission {
			return true
		}
	}
	return false
}

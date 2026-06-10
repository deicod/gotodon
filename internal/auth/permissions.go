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

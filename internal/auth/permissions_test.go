package auth

import "testing"

// allPermissions lists every Permission* constant, in bit order.
var allPermissions = []int64{
	PermissionAdministrator,
	PermissionViewDevops,
	PermissionViewAuditLog,
	PermissionViewDashboard,
	PermissionManageReports,
	PermissionManageFederation,
	PermissionManageSettings,
	PermissionManageBlocks,
	PermissionManageTaxonomies,
	PermissionManageAppeals,
	PermissionManageUsers,
	PermissionManageInvites,
	PermissionManageRules,
	PermissionManageAnnouncements,
	PermissionManageCustomEmojis,
	PermissionManageWebhooks,
	PermissionInviteUsers,
	PermissionManageRoles,
	PermissionManageUserAccess,
	PermissionDeleteUserData,
	PermissionViewFeeds,
	PermissionInviteBypassApproval,
	PermissionManageEmailSubscriptions,
}

// Seeded role values after migration 047.
const (
	seededOwner     int64 = -1
	seededAdmin     int64 = 2031612
	seededModerator int64 = 1049884
)

func TestPermissionAllCoversEveryFlag(t *testing.T) {
	var union int64
	for i, permission := range allPermissions {
		if permission != 1<<i {
			t.Fatalf("allPermissions[%d] = %d, want bit %d", i, permission, i)
		}
		union |= permission
	}
	if PermissionAll != union {
		t.Fatalf("PermissionAll = %d, want the OR of all Permission* constants, %d", PermissionAll, union)
	}
	// Mastodon's UserRole::Flags::ALL for its 23 flags.
	if PermissionAll != 8388607 {
		t.Fatalf("PermissionAll = %d, want 8388607", PermissionAll)
	}
}

func TestPermissionDefault(t *testing.T) {
	// Mastodon's UserRole::Flags::DEFAULT, invite_users, which the seeded
	// everyone role holds.
	if PermissionDefault != 65536 {
		t.Fatalf("PermissionDefault = %d, want invite_users, 65536", PermissionDefault)
	}
}

func TestComputedPermissions(t *testing.T) {
	tests := []struct {
		name   string
		stored int64
		want   int64
	}{
		{"no permissions: the everyone role's", 0, PermissionDefault},
		{"administrator bit only", PermissionAdministrator, PermissionAll},
		{"administrator with other bits", PermissionAdministrator | PermissionManageUsers, PermissionAll},
		{"seeded Owner", seededOwner, PermissionAll},
		{"Owner storing every bit", -1, PermissionAll},
		// The values Mastodon reports for its config/roles.yml roles.
		{"seeded Admin gains the everyone role's", seededAdmin, 2097148},
		{"seeded Moderator gains the everyone role's", seededModerator, 1115420},
		{"single permission", PermissionManageReports, PermissionManageReports | PermissionDefault},
		{"stored default bits", PermissionManageReports | PermissionInviteUsers, PermissionManageReports | PermissionInviteUsers},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputedPermissions(tt.stored); got != tt.want {
				t.Fatalf("ComputedPermissions(%d) = %d, want %d", tt.stored, got, tt.want)
			}
		})
	}
}

func TestHasPermission(t *testing.T) {
	tests := []struct {
		name   string
		stored int64
		anyOf  []int64
		want   bool
	}{
		{"nothing requested", PermissionAll, nil, false},
		{"zero requested", PermissionAll, []int64{0}, false},
		{"granted", PermissionManageReports, []int64{PermissionManageReports}, true},
		{"missing", PermissionManageReports, []int64{PermissionManageUsers}, false},
		{"no role permissions", 0, []int64{PermissionManageUsers}, false},
		{"no role permissions, the everyone role's", 0, []int64{PermissionInviteUsers}, true},
		{"every role has the everyone role's", PermissionManageReports, []int64{PermissionInviteUsers}, true},
		{"seeded Moderator has invite_users", seededModerator, []int64{PermissionInviteUsers}, true},
		{"any of: first held", PermissionManageUsers, []int64{PermissionManageUsers, PermissionManageReports}, true},
		{"any of: second held", PermissionManageReports, []int64{PermissionManageUsers, PermissionManageReports}, true},
		{"any of: none held", PermissionViewDashboard, []int64{PermissionManageUsers, PermissionManageReports}, false},
		{"administrator bit implies the rest", PermissionAdministrator, []int64{PermissionManageEmailSubscriptions}, true},
		{"seeded Owner", seededOwner, []int64{PermissionManageFederation}, true},
		{"seeded Admin has manage_federation", seededAdmin, []int64{PermissionManageFederation}, true},
		{"seeded Admin lacks view_devops", seededAdmin, []int64{PermissionViewDevops}, false},
		{"seeded Moderator has manage_reports", seededModerator, []int64{PermissionManageReports}, true},
		{"seeded Moderator lacks manage_blocks", seededModerator, []int64{PermissionManageBlocks}, false},
		{"combined flag needs every bit", PermissionManageUsers, []int64{PermissionManageUsers | PermissionManageReports}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasPermission(tt.stored, tt.anyOf...); got != tt.want {
				t.Fatalf("HasPermission(%d, %v) = %t, want %t", tt.stored, tt.anyOf, got, tt.want)
			}
		})
	}
}

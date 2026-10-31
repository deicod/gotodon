package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/auth"
)

const (
	// rolePermissionsMigrationVersion is the migration that corrects the
	// permissions of the Admin and Moderator roles seeded by
	// 001_initial_schema.sql.
	rolePermissionsMigrationVersion = 47
	// ownerPermissionsMigrationVersion is the migration that stores the
	// seeded Owner role as Mastodon does.
	ownerPermissionsMigrationVersion = 50
)

// permissionFlags lists Mastodon's UserRole::FLAGS names in bit order next to
// the matching internal/auth constant.
var permissionFlags = []struct {
	name string
	bit  int64
}{
	{"administrator", auth.PermissionAdministrator},
	{"view_devops", auth.PermissionViewDevops},
	{"view_audit_log", auth.PermissionViewAuditLog},
	{"view_dashboard", auth.PermissionViewDashboard},
	{"manage_reports", auth.PermissionManageReports},
	{"manage_federation", auth.PermissionManageFederation},
	{"manage_settings", auth.PermissionManageSettings},
	{"manage_blocks", auth.PermissionManageBlocks},
	{"manage_taxonomies", auth.PermissionManageTaxonomies},
	{"manage_appeals", auth.PermissionManageAppeals},
	{"manage_users", auth.PermissionManageUsers},
	{"manage_invites", auth.PermissionManageInvites},
	{"manage_rules", auth.PermissionManageRules},
	{"manage_announcements", auth.PermissionManageAnnouncements},
	{"manage_custom_emojis", auth.PermissionManageCustomEmojis},
	{"manage_webhooks", auth.PermissionManageWebhooks},
	{"invite_users", auth.PermissionInviteUsers},
	{"manage_roles", auth.PermissionManageRoles},
	{"manage_user_access", auth.PermissionManageUserAccess},
	{"delete_user_data", auth.PermissionDeleteUserData},
	{"view_feeds", auth.PermissionViewFeeds},
	{"invite_bypass_approval", auth.PermissionInviteBypassApproval},
	{"manage_email_subscriptions", auth.PermissionManageEmailSubscriptions},
}

// The Owner role's permissions: the value 001_initial_schema.sql seeded it
// with, which migration 047 keeps, and the value migration 050 gives it, the
// administrator bit alone as in Mastodon's config/roles.yml.
const (
	seededOwnerPermissions int64 = -1
	ownerPermissions             = auth.PermissionAdministrator
)

// defaultRoles describes the roles migration 047 corrects: the value
// 001_initial_schema.sql seeded them with, the value 047 gives them, and the
// permissions Mastodon's config/roles.yml grants them.
var defaultRoles = []struct {
	name        string
	seeded      int64
	want        int64
	permissions []string
}{
	{
		name:   "Admin",
		seeded: 983103,
		want:   2031612,
		permissions: []string{
			"view_dashboard",
			"view_audit_log",
			"view_feeds",
			"manage_users",
			"manage_user_access",
			"delete_user_data",
			"manage_reports",
			"manage_taxonomies",
			"manage_federation",
			"manage_settings",
			"manage_blocks",
			"manage_appeals",
			"manage_rules",
			"manage_invites",
			"manage_announcements",
			"manage_custom_emojis",
			"manage_webhooks",
			"manage_roles",
		},
	},
	{
		name:   "Moderator",
		seeded: 9423,
		want:   1049884,
		permissions: []string{
			"view_dashboard",
			"view_audit_log",
			"view_feeds",
			"manage_users",
			"manage_reports",
			"manage_taxonomies",
		},
	},
}

// decodePermissions returns the Mastodon names of the bits set in mask, in bit
// order. Bits without a name are reported as unknown so they cannot hide.
func decodePermissions(mask int64) []string {
	var names []string
	var known int64
	for _, flag := range permissionFlags {
		known |= flag.bit
		if mask&flag.bit != 0 {
			names = append(names, flag.name)
		}
	}
	if unknown := mask &^ known; unknown != 0 {
		names = append(names, fmt.Sprintf("unknown(%#x)", uint64(unknown)))
	}
	return names
}

func TestPermissionFlagsFollowMastodonBitOrder(t *testing.T) {
	seen := make(map[string]struct{}, len(permissionFlags))
	for i, flag := range permissionFlags {
		if _, dup := seen[flag.name]; dup {
			t.Fatalf("duplicate permission flag %q", flag.name)
		}
		seen[flag.name] = struct{}{}

		if want := int64(1) << i; flag.bit != want {
			t.Errorf("%s = %#x, want 1 << %d (%#x)", flag.name, flag.bit, i, want)
		}
	}
}

func TestDefaultRolePermissionValues(t *testing.T) {
	for _, role := range defaultRoles {
		t.Run(role.name, func(t *testing.T) {
			got := decodePermissions(role.want)
			want := slices.Sorted(slices.Values(role.permissions))
			if !slices.Equal(slices.Sorted(slices.Values(got)), want) {
				t.Fatalf("%d decodes to %v, want %v", role.want, got, want)
			}

			if role.want&auth.PermissionAdministrator != 0 {
				t.Fatalf("%d grants administrator", role.want)
			}
		})
	}
}

// roleUpdatePattern matches one guarded UPDATE in the role permissions
// migration: SET permissions = <new> ... WHERE name = '<role>' AND
// permissions = <old>.
var roleUpdatePattern = regexp.MustCompile(`(?is)^UPDATE\s+user_roles\s+SET\s+permissions\s*=\s*(-?\d+)\s*,\s*updated_at\s*=\s*now\(\)\s+WHERE\s+name\s*=\s*'([^']*)'\s+AND\s+permissions\s*=\s*(-?\d+)$`)

type roleUpdate struct {
	set   int64
	where int64
}

func TestDefaultRolePermissionsMigrationSQL(t *testing.T) {
	wantUp := make(map[string]roleUpdate, len(defaultRoles))
	wantDown := make(map[string]roleUpdate, len(defaultRoles))
	for _, role := range defaultRoles {
		wantUp[role.name] = roleUpdate{set: role.want, where: role.seeded}
		wantDown[role.name] = roleUpdate{set: role.seeded, where: role.want}
	}

	assertMigrationRoleUpdates(t, rolePermissionsMigrationVersion, wantUp, wantDown)
}

func TestOwnerRolePermissionsMigrationSQL(t *testing.T) {
	assertMigrationRoleUpdates(t, ownerPermissionsMigrationVersion,
		map[string]roleUpdate{"Owner": {set: ownerPermissions, where: seededOwnerPermissions}},
		map[string]roleUpdate{"Owner": {set: seededOwnerPermissions, where: ownerPermissions}},
	)
}

// assertMigrationRoleUpdates checks that the up and down sections of the
// migration version hold exactly the given guarded role updates and nothing
// else.
func assertMigrationRoleUpdates(t *testing.T, version int64, wantUp, wantDown map[string]roleUpdate) {
	t.Helper()

	matches, err := fs.Glob(Migrations, fmt.Sprintf("%03d_*.sql", version))
	if err != nil {
		t.Fatalf("glob migration: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("found migrations %v for version %d, want exactly one", matches, version)
	}

	content, err := fs.ReadFile(Migrations, matches[0])
	if err != nil {
		t.Fatalf("read %s: %v", matches[0], err)
	}

	up, down, ok := strings.Cut(string(content), "-- +goose Down")
	if !ok {
		t.Fatalf("%s has no -- +goose Down section", matches[0])
	}
	_, up, ok = strings.Cut(up, "-- +goose Up")
	if !ok {
		t.Fatalf("%s has no -- +goose Up section", matches[0])
	}

	if got := parseRoleUpdates(t, up); !maps.Equal(got, wantUp) {
		t.Errorf("%s: up section updates %v, want %v", matches[0], got, wantUp)
	}
	if got := parseRoleUpdates(t, down); !maps.Equal(got, wantDown) {
		t.Errorf("%s: down section updates %v, want %v", matches[0], got, wantDown)
	}
}

// parseRoleUpdates returns the guarded role updates in a migration section,
// keyed by role name. It fails on any statement that is not such an update, so
// nothing else can slip into the migration unchecked.
func parseRoleUpdates(t *testing.T, section string) map[string]roleUpdate {
	t.Helper()

	var lines []string
	for line := range strings.Lines(section) {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}

	updates := make(map[string]roleUpdate)
	for statement := range strings.SplitSeq(strings.Join(lines, ""), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}

		match := roleUpdatePattern.FindStringSubmatch(statement)
		if match == nil {
			t.Fatalf("unexpected statement in role permissions migration: %q", statement)
		}

		set, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			t.Fatalf("parse new permissions in %q: %v", statement, err)
		}
		where, err := strconv.ParseInt(match[3], 10, 64)
		if err != nil {
			t.Fatalf("parse guarded permissions in %q: %v", statement, err)
		}

		name := match[2]
		if _, dup := updates[name]; dup {
			t.Fatalf("role %q updated more than once in one section", name)
		}
		updates[name] = roleUpdate{set: set, where: where}
	}
	return updates
}

func TestDefaultRolePermissionsMigration(t *testing.T) {
	seeded := map[string]int64{"Owner": seededOwnerPermissions}
	corrected := map[string]int64{"Owner": seededOwnerPermissions}
	for _, role := range defaultRoles {
		seeded[role.name] = role.seeded
		corrected[role.name] = role.want
	}

	// Migration 050 changes the Owner later, so this stops at 047.
	// TestOwnerRolePermissionsMigration covers the whole chain.
	t.Run("fresh install up to 047", func(t *testing.T) {
		db, _ := openSchemaTestDB(t)
		migrateUpTo(t, db, rolePermissionsMigrationVersion)

		after := loadRoles(t, db)
		assertRolePermissions(t, after, corrected)
		for _, role := range defaultRoles {
			if after[role.name].permissions&auth.PermissionAdministrator != 0 {
				t.Errorf("%s has the administrator bit: %v", role.name, decodePermissions(after[role.name].permissions))
			}
		}

		before := after
		migrateDownTo(t, db, rolePermissionsMigrationVersion-1)
		after = loadRoles(t, db)

		assertRolePermissions(t, after, seeded)
		assertRolesTouched(t, before, after, "Admin", "Moderator")
	})

	t.Run("roles customized before the upgrade", func(t *testing.T) {
		db, _ := openSchemaTestDB(t)
		ctx := context.Background()
		migrateUpTo(t, db, rolePermissionsMigrationVersion-1)
		assertRolePermissions(t, loadRoles(t, db), seeded)

		// Mastodon 4.0's moderator permissions, as an instance might have set them.
		const customModerator = 1308
		// A role with another name that holds Admin's original value.
		const supportRole = "Support"
		const supportPermissions = 983103

		if _, err := db.ExecContext(ctx, `UPDATE user_roles SET permissions = $1 WHERE name = 'Moderator'`, customModerator); err != nil {
			t.Fatalf("customize Moderator: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO user_roles (name, permissions, position) VALUES ($1, $2, 50)`, supportRole, supportPermissions); err != nil {
			t.Fatalf("insert %s role: %v", supportRole, err)
		}

		before := loadRoles(t, db)
		migrateUpTo(t, db, rolePermissionsMigrationVersion)
		after := loadRoles(t, db)

		assertRolePermissions(t, after, map[string]int64{
			"Owner":     seededOwnerPermissions,
			"Admin":     corrected["Admin"],
			"Moderator": customModerator,
			supportRole: supportPermissions,
		})
		assertRolesTouched(t, before, after, "Admin")

		before = after
		migrateDownTo(t, db, rolePermissionsMigrationVersion-1)
		after = loadRoles(t, db)

		assertRolePermissions(t, after, map[string]int64{
			"Owner":     seededOwnerPermissions,
			"Admin":     seeded["Admin"],
			"Moderator": customModerator,
			supportRole: supportPermissions,
		})
		assertRolesTouched(t, before, after, "Admin")
	})

	t.Run("roles customized after the upgrade", func(t *testing.T) {
		db, _ := openSchemaTestDB(t)
		ctx := context.Background()
		migrateUpTo(t, db, rolePermissionsMigrationVersion)
		assertRolePermissions(t, loadRoles(t, db), corrected)

		customModerator := corrected["Moderator"] | auth.PermissionInviteUsers
		if _, err := db.ExecContext(ctx, `UPDATE user_roles SET permissions = $1 WHERE name = 'Moderator'`, customModerator); err != nil {
			t.Fatalf("customize Moderator: %v", err)
		}

		before := loadRoles(t, db)
		migrateDownTo(t, db, rolePermissionsMigrationVersion-1)
		after := loadRoles(t, db)

		assertRolePermissions(t, after, map[string]int64{
			"Owner":     seededOwnerPermissions,
			"Admin":     seeded["Admin"],
			"Moderator": customModerator,
		})
		assertRolesTouched(t, before, after, "Admin")
	})
}

func TestOwnerRolePermissionsMigration(t *testing.T) {
	before := map[string]int64{"Owner": seededOwnerPermissions}
	after := map[string]int64{"Owner": ownerPermissions}
	for _, role := range defaultRoles {
		before[role.name] = role.want
		after[role.name] = role.want
	}

	// Both values report every permission, so the Owner's API responses and
	// authorization don't change.
	if seeded, migrated := auth.ComputedPermissions(seededOwnerPermissions), auth.ComputedPermissions(ownerPermissions); seeded != auth.PermissionAll || migrated != auth.PermissionAll {
		t.Fatalf("Owner computes to %d before and %d after the migration, want %d both times", seeded, migrated, auth.PermissionAll)
	}

	t.Run("fresh install", func(t *testing.T) {
		db, _ := openSchemaTestDB(t)
		applyMigrations(t, db)

		migrated := loadRoles(t, db)
		assertRolePermissions(t, migrated, after)
		if got := decodePermissions(migrated["Owner"].permissions); !slices.Equal(got, []string{"administrator"}) {
			t.Errorf("Owner permissions = %v, want [administrator]", got)
		}

		migrateDownTo(t, db, ownerPermissionsMigrationVersion-1)
		reverted := loadRoles(t, db)

		assertRolePermissions(t, reverted, before)
		assertRolesTouched(t, migrated, reverted, "Owner")
	})

	t.Run("upgrade", func(t *testing.T) {
		db, _ := openSchemaTestDB(t)
		migrateUpTo(t, db, ownerPermissionsMigrationVersion-1)
		upgradable := loadRoles(t, db)
		assertRolePermissions(t, upgradable, before)

		migrateUpTo(t, db, ownerPermissionsMigrationVersion)
		migrated := loadRoles(t, db)

		assertRolePermissions(t, migrated, after)
		assertRolesTouched(t, upgradable, migrated, "Owner")
	})

	t.Run("Owner customized before the upgrade", func(t *testing.T) {
		db, _ := openSchemaTestDB(t)
		ctx := context.Background()
		migrateUpTo(t, db, ownerPermissionsMigrationVersion-1)

		// Every flag, without the bits above them that -1 also sets.
		const customOwner = auth.PermissionAll
		// A role with another name that holds the Owner's seeded value.
		const rootRole = "Root"

		if _, err := db.ExecContext(ctx, `UPDATE user_roles SET permissions = $1 WHERE name = 'Owner'`, customOwner); err != nil {
			t.Fatalf("customize Owner: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO user_roles (name, permissions, position) VALUES ($1, $2, 500)`, rootRole, seededOwnerPermissions); err != nil {
			t.Fatalf("insert %s role: %v", rootRole, err)
		}
		customized := map[string]int64{
			"Owner":     customOwner,
			"Admin":     before["Admin"],
			"Moderator": before["Moderator"],
			rootRole:    seededOwnerPermissions,
		}

		upgradable := loadRoles(t, db)
		migrateUpTo(t, db, ownerPermissionsMigrationVersion)
		migrated := loadRoles(t, db)

		assertRolePermissions(t, migrated, customized)
		assertRolesTouched(t, upgradable, migrated)

		migrateDownTo(t, db, ownerPermissionsMigrationVersion-1)
		reverted := loadRoles(t, db)

		assertRolePermissions(t, reverted, customized)
		assertRolesTouched(t, migrated, reverted)
	})

	t.Run("Owner customized after the upgrade", func(t *testing.T) {
		db, _ := openSchemaTestDB(t)
		ctx := context.Background()
		migrateUpTo(t, db, ownerPermissionsMigrationVersion)
		assertRolePermissions(t, loadRoles(t, db), after)

		customOwner := ownerPermissions | auth.PermissionViewDevops
		if _, err := db.ExecContext(ctx, `UPDATE user_roles SET permissions = $1 WHERE name = 'Owner'`, customOwner); err != nil {
			t.Fatalf("customize Owner: %v", err)
		}

		migrated := loadRoles(t, db)
		migrateDownTo(t, db, ownerPermissionsMigrationVersion-1)
		reverted := loadRoles(t, db)

		assertRolePermissions(t, reverted, map[string]int64{
			"Owner":     customOwner,
			"Admin":     before["Admin"],
			"Moderator": before["Moderator"],
		})
		assertRolesTouched(t, migrated, reverted)
	})
}

type roleState struct {
	permissions int64
	updatedAt   time.Time
}

func loadRoles(t *testing.T, db *sql.DB) map[string]roleState {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), `SELECT name, permissions, updated_at FROM user_roles`)
	if err != nil {
		t.Fatalf("query user_roles: %v", err)
	}
	defer rows.Close()

	roles := make(map[string]roleState)
	for rows.Next() {
		var name string
		var state roleState
		if err := rows.Scan(&name, &state.permissions, &state.updatedAt); err != nil {
			t.Fatalf("scan user_roles: %v", err)
		}
		if _, dup := roles[name]; dup {
			t.Fatalf("more than one role named %q", name)
		}
		roles[name] = state
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate user_roles: %v", err)
	}
	return roles
}

// assertRolePermissions checks that exactly the roles in want exist and hold
// the given permissions.
func assertRolePermissions(t *testing.T, got map[string]roleState, want map[string]int64) {
	t.Helper()

	if gotNames, wantNames := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)); !slices.Equal(gotNames, wantNames) {
		t.Fatalf("roles = %v, want %v", gotNames, wantNames)
	}
	for name, permissions := range want {
		if got[name].permissions != permissions {
			t.Errorf("%s permissions = %d %v, want %d %v",
				name, got[name].permissions, decodePermissions(got[name].permissions),
				permissions, decodePermissions(permissions))
		}
	}
}

// assertRolesTouched checks that the migration bumped updated_at on exactly the
// named roles and left every other row's timestamp as it was.
func assertRolesTouched(t *testing.T, before, after map[string]roleState, touched ...string) {
	t.Helper()

	for name, prev := range before {
		cur := after[name]
		if slices.Contains(touched, name) {
			if !cur.updatedAt.After(prev.updatedAt) {
				t.Errorf("%s updated_at = %v, want later than %v", name, cur.updatedAt, prev.updatedAt)
			}
		} else if !cur.updatedAt.Equal(prev.updatedAt) {
			t.Errorf("%s updated_at changed from %v to %v, want untouched", name, prev.updatedAt, cur.updatedAt)
		}
	}
}

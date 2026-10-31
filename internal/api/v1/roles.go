package v1

import (
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
)

// everyoneRoleID is the id of Mastodon's everyone role,
// UserRole::EVERYONE_ROLE_ID.
const everyoneRoleID int64 = -99

// SerializeRole returns role as Mastodon's REST::RoleSerializer does. The
// permissions are the computed ones (auth.ComputedPermissions), not the
// stored bits: clients test individual bits in them, and an administrator
// role may store the administrator bit alone.
//
// REST::RoleSerializer gained collection_limit in Mastodon 5.0. gotodon
// targets 4.5 and has no such column.
func SerializeRole(role db.UserRole) AccountRole {
	return AccountRole{
		ID:          strconv.FormatInt(role.ID, 10),
		Name:        role.Name,
		Permissions: strconv.FormatInt(auth.ComputedPermissions(role.Permissions), 10),
		Color:       role.Color,
		Highlighted: role.Highlighted,
	}
}

// EveryoneRole returns Mastodon's everyone role, which User#role gives a user
// without a role: id -99, no name or color, not highlighted, and the
// permissions auth.PermissionDefault. Mastodon stores it as a row; gotodon
// keeps none (see auth.PermissionDefault).
func EveryoneRole() AccountRole {
	return SerializeRole(db.UserRole{ID: everyoneRoleID})
}

// SerializeUserRole returns the role of a user, as Mastodon's User#role gives
// it, from the user_roles columns of a query that LEFT JOINs the user's role:
// the joined role, or the everyone role when id is NULL because the user has
// no role.
func SerializeUserRole(id pgtype.Int8, name, color pgtype.Text, permissions pgtype.Int8, highlighted pgtype.Bool) AccountRole {
	if !id.Valid {
		return EveryoneRole()
	}
	return SerializeRole(db.UserRole{
		ID:          id.Int64,
		Name:        name.String,
		Color:       color.String,
		Permissions: permissions.Int64,
		Highlighted: highlighted.Bool,
	})
}

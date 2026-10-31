package v1

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

// Roles as gotodon seeds them, and their responses. The Owner stores either
// the administrator bit alone, as Mastodon's config/roles.yml has it, or every
// bit. Both report every permission.
var (
	ownerRole           = db.UserRole{ID: 1, Name: "Owner", Position: 1000, Permissions: 1}
	everyBitOwnerRole   = db.UserRole{ID: 1, Name: "Owner", Position: 1000, Permissions: -1}
	adminRole           = db.UserRole{ID: 2, Name: "Admin", Position: 100, Permissions: 2031612}
	moderatorRole       = db.UserRole{ID: 3, Name: "Moderator", Position: 10, Permissions: 1049884}
	highlightedRole     = db.UserRole{ID: 7, Name: "Team", Color: "#ffcc00", Position: 5, Permissions: 0, Highlighted: true}
	reportsOnlyRole     = db.UserRole{ID: 8, Name: "Reports", Position: 4, Permissions: 16}
	everyoneRoleJSON    = `{"id":"-99","name":"","permissions":"65536","color":"","highlighted":false}`
	ownerRoleJSON       = `{"id":"1","name":"Owner","permissions":"8388607","color":"","highlighted":false}`
	adminRoleJSON       = `{"id":"2","name":"Admin","permissions":"2097148","color":"","highlighted":false}`
	moderatorRoleJSON   = `{"id":"3","name":"Moderator","permissions":"1115420","color":"","highlighted":false}`
	highlightedRoleJSON = `{"id":"7","name":"Team","permissions":"65536","color":"#ffcc00","highlighted":true}`
	reportsOnlyRoleJSON = `{"id":"8","name":"Reports","permissions":"65552","color":"","highlighted":false}`
)

// assertJSONEqual checks that got and want encode the same JSON value.
func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()

	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want %s: %v", want, err)
	}
	gotCanonical, _ := json.Marshal(gotValue)
	wantCanonical, _ := json.Marshal(wantValue)
	if string(gotCanonical) != string(wantCanonical) {
		t.Fatalf("JSON = %s, want %s", gotCanonical, wantCanonical)
	}
}

func marshalJSON(t *testing.T, v any) []byte {
	t.Helper()

	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %v: %v", v, err)
	}
	return encoded
}

func TestSerializeRole(t *testing.T) {
	tests := []struct {
		name string
		role db.UserRole
		want string
	}{
		{"Owner storing the administrator bit", ownerRole, ownerRoleJSON},
		{"Owner storing every bit", everyBitOwnerRole, ownerRoleJSON},
		{"Admin", adminRole, adminRoleJSON},
		{"Moderator", moderatorRole, moderatorRoleJSON},
		{"name, color and highlighted", highlightedRole, highlightedRoleJSON},
		{"single permission", reportsOnlyRole, reportsOnlyRoleJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertJSONEqual(t, marshalJSON(t, SerializeRole(tt.role)), tt.want)
		})
	}
}

func TestEveryoneRole(t *testing.T) {
	assertJSONEqual(t, marshalJSON(t, EveryoneRole()), everyoneRoleJSON)
}

func TestSerializeUserRole(t *testing.T) {
	joined := func(role db.UserRole) (pgtype.Int8, pgtype.Text, pgtype.Text, pgtype.Int8, pgtype.Bool) {
		return pgtype.Int8{Int64: role.ID, Valid: true},
			pgtype.Text{String: role.Name, Valid: true},
			pgtype.Text{String: role.Color, Valid: true},
			pgtype.Int8{Int64: role.Permissions, Valid: true},
			pgtype.Bool{Bool: role.Highlighted, Valid: true}
	}

	tests := []struct {
		name string
		got  AccountRole
		want string
	}{
		{"Owner storing the administrator bit", SerializeUserRole(joined(ownerRole)), ownerRoleJSON},
		{"Owner storing every bit", SerializeUserRole(joined(everyBitOwnerRole)), ownerRoleJSON},
		{"Admin", SerializeUserRole(joined(adminRole)), adminRoleJSON},
		{"Moderator", SerializeUserRole(joined(moderatorRole)), moderatorRoleJSON},
		{"name, color and highlighted", SerializeUserRole(joined(highlightedRole)), highlightedRoleJSON},
		{"no role", SerializeUserRole(pgtype.Int8{}, pgtype.Text{}, pgtype.Text{}, pgtype.Int8{}, pgtype.Bool{}), everyoneRoleJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertJSONEqual(t, marshalJSON(t, tt.got), tt.want)
		})
	}
}

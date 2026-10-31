package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// actionTarget is the part of AdminGetAccountActionTargetRow a test
// compares, with the suspension time reduced to whether it is set.
type actionTarget struct {
	domain            pgtype.Text
	uri               string
	suspended         bool
	suspensionOrigin  pgtype.Int4
	hasUser           bool
	userApproved      pgtype.Bool
	rolePosition      pgtype.Int4
	deletionRequested bool
}

func actionTargetOf(row AdminGetAccountActionTargetRow) actionTarget {
	return actionTarget{
		domain:            row.Domain,
		uri:               row.Uri,
		suspended:         row.SuspendedAt.Valid,
		suspensionOrigin:  row.SuspensionOrigin,
		hasUser:           row.UserID.Valid,
		userApproved:      row.UserApproved,
		rolePosition:      row.RolePosition,
		deletionRequested: row.DeletionRequested,
	}
}

func TestAdminGetAccountActionTarget(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()

	insertAccount := func(username string, domain *string, uri string) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx,
			`INSERT INTO accounts (username, domain, uri) VALUES ($1, $2, $3) RETURNING id`,
			username, domain, uri,
		).Scan(&id); err != nil {
			t.Fatalf("insert account %s: %v", username, err)
		}
		return id
	}
	// insertUser adds a user for accountID with the seeded role named role,
	// or without a role when role is "".
	insertUser := func(accountID int64, email string, approved bool, role string) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx,
			`INSERT INTO users (email, account_id, approved, role_id)
			 VALUES ($1, $2, $3, (SELECT id FROM user_roles WHERE name = NULLIF($4::text, '')))
			 RETURNING id`,
			email, accountID, approved, role,
		).Scan(&id); err != nil {
			t.Fatalf("insert user %s: %v", email, err)
		}
		return id
	}
	remoteDomain := "remote.example"

	remote := insertAccount("bob", &remoteDomain, "https://remote.example/users/bob")
	instanceActor := insertAccount("example.com", nil, "https://example.com/actor")
	pending := insertAccount("carol", nil, "https://example.com/users/carol")
	pendingUser := insertUser(pending, "carol@example.com", false, "")
	moderator := insertAccount("dave", nil, "https://example.com/users/dave")
	moderatorUser := insertUser(moderator, "dave@example.com", true, "Moderator")
	owner := insertAccount("olive", nil, "https://example.com/users/olive")
	ownerUser := insertUser(owner, "olive@example.com", true, "Owner")

	suspended := insertAccount("erin", nil, "https://example.com/users/erin")
	suspendedUser := insertUser(suspended, "erin@example.com", true, "Admin")
	if err := q.AdminSuspendAccount(ctx, suspended); err != nil {
		t.Fatalf("suspend erin: %v", err)
	}

	remotelySuspended := insertAccount("frank", &remoteDomain, "https://remote.example/users/frank")
	if _, err := conn.Exec(ctx,
		`UPDATE accounts SET suspended_at = now(), suspension_origin = 1 WHERE id = $1`, remotelySuspended,
	); err != nil {
		t.Fatalf("suspend frank remotely: %v", err)
	}

	// A suspension whose deletion request is gone is permanent in
	// Mastodon's terms (suspended_permanently?).
	permanent := insertAccount("gina", nil, "https://example.com/users/gina")
	insertUser(permanent, "gina@example.com", true, "")
	if err := q.AdminSuspendAccount(ctx, permanent); err != nil {
		t.Fatalf("suspend gina: %v", err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM account_deletion_requests WHERE account_id = $1`, permanent); err != nil {
		t.Fatalf("remove gina's deletion request: %v", err)
	}

	local := pgtype.Text{}
	remoteText := pgtype.Text{String: remoteDomain, Valid: true}
	originLocal := pgtype.Int4{Int32: 0, Valid: true}
	originRemote := pgtype.Int4{Int32: 1, Valid: true}
	approved := pgtype.Bool{Bool: true, Valid: true}
	notApproved := pgtype.Bool{Bool: false, Valid: true}

	tests := []struct {
		name     string
		id       int64
		wantUser int64
		want     actionTarget
	}{
		{
			name: "remote account without a user",
			id:   remote,
			want: actionTarget{domain: remoteText, uri: "https://remote.example/users/bob"},
		},
		{
			name: "local account without a user",
			id:   instanceActor,
			want: actionTarget{domain: local, uri: "https://example.com/actor"},
		},
		{
			name:     "pending user without a role",
			id:       pending,
			wantUser: pendingUser,
			want: actionTarget{
				domain: local, uri: "https://example.com/users/carol",
				hasUser: true, userApproved: notApproved,
			},
		},
		{
			name:     "Moderator",
			id:       moderator,
			wantUser: moderatorUser,
			want: actionTarget{
				domain: local, uri: "https://example.com/users/dave",
				hasUser: true, userApproved: approved, rolePosition: pgtype.Int4{Int32: 10, Valid: true},
			},
		},
		{
			name:     "Owner",
			id:       owner,
			wantUser: ownerUser,
			want: actionTarget{
				domain: local, uri: "https://example.com/users/olive",
				hasUser: true, userApproved: approved, rolePosition: pgtype.Int4{Int32: 1000, Valid: true},
			},
		},
		{
			name:     "suspended locally, with a deletion request",
			id:       suspended,
			wantUser: suspendedUser,
			want: actionTarget{
				domain: local, uri: "https://example.com/users/erin",
				suspended: true, suspensionOrigin: originLocal,
				hasUser: true, userApproved: approved, rolePosition: pgtype.Int4{Int32: 100, Valid: true},
				deletionRequested: true,
			},
		},
		{
			name: "suspended remotely",
			id:   remotelySuspended,
			want: actionTarget{
				domain: remoteText, uri: "https://remote.example/users/frank",
				suspended: true, suspensionOrigin: originRemote,
			},
		},
		{
			name: "suspended without a deletion request",
			id:   permanent,
			want: actionTarget{
				domain: local, uri: "https://example.com/users/gina",
				suspended: true, suspensionOrigin: originLocal,
				hasUser: true, userApproved: approved,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row, err := q.AdminGetAccountActionTarget(ctx, tt.id)
			if err != nil {
				t.Fatalf("AdminGetAccountActionTarget(%d): %v", tt.id, err)
			}
			if row.ID != tt.id {
				t.Fatalf("ID = %d, want %d", row.ID, tt.id)
			}
			if got := actionTargetOf(row); got != tt.want {
				t.Fatalf("target = %+v, want %+v", got, tt.want)
			}
			if tt.wantUser > 0 && row.UserID.Int64 != tt.wantUser {
				t.Fatalf("UserID = %d, want %d", row.UserID.Int64, tt.wantUser)
			}
		})
	}

	t.Run("unsuspended", func(t *testing.T) {
		if err := q.AdminUnsuspendAccount(ctx, suspended); err != nil {
			t.Fatalf("unsuspend erin: %v", err)
		}
		row, err := q.AdminGetAccountActionTarget(ctx, suspended)
		if err != nil {
			t.Fatalf("AdminGetAccountActionTarget(%d): %v", suspended, err)
		}
		want := actionTarget{
			domain: local, uri: "https://example.com/users/erin",
			hasUser: true, userApproved: approved, rolePosition: pgtype.Int4{Int32: 100, Valid: true},
		}
		if got := actionTargetOf(row); got != want {
			t.Fatalf("target = %+v, want %+v", got, want)
		}
	})

	t.Run("unknown account", func(t *testing.T) {
		var unknown int64
		if err := conn.QueryRow(ctx, `SELECT max(id) + 1 FROM accounts`).Scan(&unknown); err != nil {
			t.Fatalf("pick an unknown ID: %v", err)
		}
		_, err := q.AdminGetAccountActionTarget(ctx, unknown)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("AdminGetAccountActionTarget(unknown) = %v, want %v", err, pgx.ErrNoRows)
		}
	})
}

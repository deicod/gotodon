package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestAccountsHandler_RegisterRoutes_Unsensitive(t *testing.T) {
	h := NewAccountsHandler(nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	tests := []struct {
		method      string
		path        string
		wantPattern string
	}{
		{
			method:      http.MethodPost,
			path:        "/api/v1/admin/accounts/123/unsensitive",
			wantPattern: "POST /api/v1/admin/accounts/{id}/unsensitive",
		},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		_, pattern := mux.Handler(req)
		if pattern != tt.wantPattern {
			t.Fatalf("path %s matched pattern %q, want %q", tt.path, pattern, tt.wantPattern)
		}
	}
}

func TestAccountHumanIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		username string
		domain   pgtype.Text
		want     string
	}{
		{
			name:     "local",
			username: "alice",
			domain:   pgtype.Text{Valid: false},
			want:     "alice",
		},
		{
			name:     "remote",
			username: "alice",
			domain:   pgtype.Text{String: "remote.example", Valid: true},
			want:     "alice@remote.example",
		},
		{
			name:     "blank-username",
			username: "  ",
			domain:   pgtype.Text{String: "example.com", Valid: true},
			want:     "",
		},
		{
			name:     "blank-domain",
			username: "bob",
			domain:   pgtype.Text{String: "   ", Valid: true},
			want:     "bob",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := accountHumanIdentifier(tt.username, tt.domain); got != tt.want {
				t.Fatalf("accountHumanIdentifier() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAccountsHandler_AccountIdentifier_NoQueries(t *testing.T) {
	h := NewAccountsHandler(nil)

	got := h.accountIdentifier(context.Background(), 42)
	if got != "42" {
		t.Fatalf("accountIdentifier() = %q, want %q", got, "42")
	}
}

func TestRoleFromRow(t *testing.T) {
	now := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	later := now.Add(2 * time.Hour)

	role := roleFromRow(
		pgtype.Int8{Int64: 7, Valid: true},
		pgtype.Text{String: "Admin", Valid: true},
		pgtype.Text{String: "#ffcc00", Valid: true},
		pgtype.Int4{Int32: 42, Valid: true},
		pgtype.Int8{Int64: 123, Valid: true},
		pgtype.Bool{Bool: true, Valid: true},
		pgtype.Timestamptz{Time: now, Valid: true},
		pgtype.Timestamptz{Time: later, Valid: true},
	)
	if role == nil {
		t.Fatal("roleFromRow() = nil, want role")
	}
	if role.ID != 7 || role.Name != "Admin" || role.Color != "#ffcc00" {
		t.Fatalf("roleFromRow() = %+v, want ID=7 Name=Admin Color=#ffcc00", role)
	}
	if role.Position != 42 || role.Permissions != 123 || !role.Highlighted {
		t.Fatalf("roleFromRow() = %+v, want Position=42 Permissions=123 Highlighted=true", role)
	}
	if role.CreatedAt != now.Format(time.RFC3339) || role.UpdatedAt != later.Format(time.RFC3339) {
		t.Fatalf("roleFromRow() = %+v, want CreatedAt/UpdatedAt RFC3339", role)
	}

	if got := roleFromRow(pgtype.Int8{}, pgtype.Text{}, pgtype.Text{}, pgtype.Int4{}, pgtype.Int8{}, pgtype.Bool{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}); got != nil {
		t.Fatalf("roleFromRow() = %+v, want nil", got)
	}
}

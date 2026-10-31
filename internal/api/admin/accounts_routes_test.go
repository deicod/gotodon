package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestAccountsHandler_RegisterRoutes_Unsensitive(t *testing.T) {
	h := NewAccountsHandler(nil, nil)
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
	h := NewAccountsHandler(nil, nil)

	got := h.accountIdentifier(context.Background(), 42)
	if got != "42" {
		t.Fatalf("accountIdentifier() = %q, want %q", got, "42")
	}
}

package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

func mockEmailsRequest(method, path string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, nil)

	ctx := req.Context()
	if accountID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

type stubEmailsQueries struct {
	user         db.User
	getErr       error
	updateCalled bool
	updateParams db.UpdateUserConfirmationTokenParams
}

func (s *stubEmailsQueries) GetUserByAccountID(_ context.Context, _ int64) (db.User, error) {
	if s.getErr != nil {
		return db.User{}, s.getErr
	}
	return s.user, nil
}

func (s *stubEmailsQueries) UpdateUserConfirmationToken(_ context.Context, arg db.UpdateUserConfirmationTokenParams) error {
	s.updateCalled = true
	s.updateParams = arg
	return nil
}

func TestEmailsHandler_Confirmations(t *testing.T) {
	stub := &stubEmailsQueries{user: db.User{ID: 1}}
	h := NewEmailsHandler(stub)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
		setup     func(*stubEmailsQueries)
	}{
		{
			name:      "authenticated user can request confirmation",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "missing user returns 404",
			accountID: 12345,
			wantCode:  http.StatusNotFound,
			setup: func(s *stubEmailsQueries) {
				s.getErr = pgx.ErrNoRows
			},
		},
		{
			name:      "already confirmed returns 403",
			accountID: 12345,
			wantCode:  http.StatusForbidden,
			setup: func(s *stubEmailsQueries) {
				s.user = db.User{ID: 2, ConfirmedAt: pgtype.Timestamptz{Valid: true}}
			},
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub.getErr = nil
			stub.user = db.User{ID: 1}
			stub.updateCalled = false
			stub.updateParams = db.UpdateUserConfirmationTokenParams{}
			if tt.setup != nil {
				tt.setup(stub)
			}
			req, rec := mockEmailsRequest(http.MethodPost, "/api/v1/emails/confirmations", tt.accountID)

			h.Confirmations(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if tt.wantCode == http.StatusOK {
				if !stub.updateCalled {
					t.Fatal("expected UpdateUserConfirmationToken to be called")
				}
				if !stub.updateParams.ConfirmationToken.Valid || stub.updateParams.ConfirmationToken.String == "" {
					t.Fatal("expected confirmation token to be set")
				}
			}
		})
	}
}

func TestEmailsHandler_CheckConfirmation(t *testing.T) {
	stub := &stubEmailsQueries{user: db.User{ID: 1}}
	h := NewEmailsHandler(stub)

	tests := []struct {
		name          string
		accountID     int64
		wantCode      int
		wantConfirmed bool
		setup         func(*stubEmailsQueries)
	}{
		{
			name:      "authenticated user gets confirmation status",
			accountID: 12345,
			wantCode:  http.StatusOK,
			setup: func(s *stubEmailsQueries) {
				s.user = db.User{ID: 1, ConfirmedAt: pgtype.Timestamptz{Valid: true}}
			},
			wantConfirmed: true,
		},
		{
			name:      "authenticated user not confirmed",
			accountID: 12345,
			wantCode:  http.StatusOK,
			setup: func(s *stubEmailsQueries) {
				s.user = db.User{ID: 1}
			},
			wantConfirmed: false,
		},
		{
			name:      "missing user returns 404",
			accountID: 12345,
			wantCode:  http.StatusNotFound,
			setup: func(s *stubEmailsQueries) {
				s.getErr = pgx.ErrNoRows
			},
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub.getErr = nil
			stub.user = db.User{ID: 1}
			if tt.setup != nil {
				tt.setup(stub)
			}
			req, rec := mockEmailsRequest(http.MethodGet, "/api/v1/emails/check_confirmation", tt.accountID)

			h.CheckConfirmation(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var result map[string]bool
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if _, ok := result["confirmed"]; !ok {
					t.Error("expected 'confirmed' field in response")
				}
				if result["confirmed"] != tt.wantConfirmed {
					t.Fatalf("confirmed = %v, want %v", result["confirmed"], tt.wantConfirmed)
				}
			}
		})
	}
}

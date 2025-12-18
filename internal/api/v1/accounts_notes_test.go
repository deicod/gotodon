package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockAccountsRequest(method, path string, body string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	if accountID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestAccountsHandler_SetNote_MockMode(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil)

	tests := []struct {
		name      string
		targetID  string
		comment   string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can set note",
			targetID:  "12345",
			comment:   "This is my private note",
			accountID: 67890,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			targetID:  "12345",
			comment:   "Test",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "empty comment clears note",
			targetID:  "12345",
			comment:   "",
			accountID: 67890,
			wantCode:  http.StatusOK,
		},
		{
			name:      "invalid target ID returns not found",
			targetID:  "invalid",
			comment:   "Test",
			accountID: 67890,
			wantCode:  http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"comment":"` + tt.comment + `"}`
			req, rec := mockAccountsRequest(http.MethodPost, "/api/v1/accounts/"+tt.targetID+"/note", body, tt.accountID)
			req.SetPathValue("id", tt.targetID)

			h.SetNote(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var rel Relationship
				if err := json.Unmarshal(rec.Body.Bytes(), &rel); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if rel.Note != tt.comment {
					t.Errorf("note = %q, want %q", rel.Note, tt.comment)
				}
			}
		})
	}
}

func TestRelationshipStruct(t *testing.T) {
	rel := Relationship{
		ID:             "12345",
		Following:      true,
		FollowedBy:     true,
		Note:           "My private note about this user",
		Blocking:       false,
		Muting:         false,
		ShowingReblogs: true,
		Languages:      []any{},
	}

	data, err := json.Marshal(rel)
	if err != nil {
		t.Fatalf("Failed to marshal Relationship: %v", err)
	}

	var unmarshaled Relationship
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal Relationship: %v", err)
	}

	if unmarshaled.ID != rel.ID {
		t.Errorf("id = %q, want %q", unmarshaled.ID, rel.ID)
	}
	if unmarshaled.Note != rel.Note {
		t.Errorf("note = %q, want %q", unmarshaled.Note, rel.Note)
	}
	if unmarshaled.Following != rel.Following {
		t.Errorf("following = %v, want %v", unmarshaled.Following, rel.Following)
	}
}

func TestAccountsHandler_SetNote_RequiresAuth(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/123/note", strings.NewReader(`{"comment":"test"}`))
	req.SetPathValue("id", "123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.SetNote(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	var errResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("Failed to unmarshal error response: %v", err)
	}

	if errResp["error"] != "The access token is invalid" {
		t.Errorf("unexpected error message: %v", errResp["error"])
	}
}

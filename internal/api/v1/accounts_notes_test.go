package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
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

func TestAccountsHandler_SetNote_DBUnavailable(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

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
			wantCode:  http.StatusInternalServerError,
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
			wantCode:  http.StatusInternalServerError,
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
	h := NewAccountsHandler(nil, nil, nil, nil)

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

func TestAccountsHandler_RemoveFromFollowers_DBUnavailable(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	tests := []struct {
		name      string
		targetID  string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can remove follower",
			targetID:  "12345",
			accountID: 67890,
			wantCode:  http.StatusInternalServerError,
		},
		{
			name:      "unauthenticated user gets 401",
			targetID:  "12345",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "invalid target ID returns not found",
			targetID:  "invalid",
			accountID: 67890,
			wantCode:  http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockAccountsRequest(http.MethodPost, "/api/v1/accounts/"+tt.targetID+"/remove_from_followers", "", tt.accountID)
			req.SetPathValue("id", tt.targetID)

			h.RemoveFromFollowers(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var rel Relationship
				if err := json.Unmarshal(rec.Body.Bytes(), &rel); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if rel.FollowedBy != false {
					t.Error("expected followed_by to be false")
				}
			}
		})
	}
}

func TestAccountsHandler_Lookup_DBUnavailable(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	tests := []struct {
		name     string
		acct     string
		wantCode int
	}{
		{
			name:     "valid acct returns account",
			acct:     "testuser",
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "empty acct returns not found",
			acct:     "",
			wantCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/v1/accounts/lookup"
			if tt.acct != "" {
				path += "?acct=" + tt.acct
			}
			req, rec := mockAccountsRequest(http.MethodGet, path, "", 0)

			h.Lookup(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var acc Account
				if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if acc.Username != tt.acct {
					t.Errorf("username = %q, want %q", acc.Username, tt.acct)
				}
			}
		})
	}
}

type stubSetNoteQueries struct {
	db.Querier
	getAccountByIDCalls    int
	upsertAccountNoteCalls int
	deleteAccountNoteCalls int
	lastUpsert             db.UpsertAccountNoteParams
	note                   db.AccountNote
}

func (s *stubSetNoteQueries) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	s.getAccountByIDCalls++
	return db.Account{ID: id, Username: "target"}, nil
}

func (s *stubSetNoteQueries) UpsertAccountNote(ctx context.Context, arg db.UpsertAccountNoteParams) (db.AccountNote, error) {
	s.upsertAccountNoteCalls++
	s.lastUpsert = arg
	return db.AccountNote{AccountID: arg.AccountID, TargetAccountID: arg.TargetAccountID, Comment: arg.Comment}, nil
}

func (s *stubSetNoteQueries) DeleteAccountNote(ctx context.Context, arg db.DeleteAccountNoteParams) error {
	s.deleteAccountNoteCalls++
	return nil
}

func (s *stubSetNoteQueries) GetFollow(ctx context.Context, arg db.GetFollowParams) (db.Follow, error) {
	return db.Follow{}, pgx.ErrNoRows
}

func (s *stubSetNoteQueries) GetBlock(ctx context.Context, arg db.GetBlockParams) (db.Block, error) {
	return db.Block{}, pgx.ErrNoRows
}

func (s *stubSetNoteQueries) GetMute(ctx context.Context, arg db.GetMuteParams) (db.Mute, error) {
	return db.Mute{}, pgx.ErrNoRows
}

func (s *stubSetNoteQueries) GetAccountNote(ctx context.Context, arg db.GetAccountNoteParams) (db.AccountNote, error) {
	if s.note.Comment == "" {
		return db.AccountNote{}, errors.New("not found")
	}
	return s.note, nil
}

func TestAccountsHandler_SetNote_CommentLimit_AtLimitSucceeds(t *testing.T) {
	stub := &stubSetNoteQueries{}
	h := NewAccountsHandler(stub, nil, nil, nil)

	comment := strings.Repeat("a", accountNoteCommentMax)
	req, rec := mockAccountsRequest(http.MethodPost, "/api/v1/accounts/42/note", `{"comment":"`+comment+`"}`, 100)
	req.SetPathValue("id", "42")

	h.SetNote(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if stub.upsertAccountNoteCalls != 1 {
		t.Fatalf("upsert calls = %d, want %d", stub.upsertAccountNoteCalls, 1)
	}
	if stub.deleteAccountNoteCalls != 0 {
		t.Fatalf("delete calls = %d, want %d", stub.deleteAccountNoteCalls, 0)
	}
	if got, want := stub.lastUpsert.Comment, comment; got != want {
		t.Fatalf("upsert comment length = %d, want %d", len([]rune(got)), len([]rune(want)))
	}
}

func TestAccountsHandler_SetNote_CommentLimit_OverLimitFails(t *testing.T) {
	stub := &stubSetNoteQueries{}
	h := NewAccountsHandler(stub, nil, nil, nil)

	comment := strings.Repeat("a", accountNoteCommentMax+1)
	req, rec := mockAccountsRequest(http.MethodPost, "/api/v1/accounts/42/note", `{"comment":"`+comment+`"}`, 100)
	req.SetPathValue("id", "42")

	h.SetNote(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if stub.upsertAccountNoteCalls != 0 {
		t.Fatalf("upsert calls = %d, want %d", stub.upsertAccountNoteCalls, 0)
	}
	if stub.deleteAccountNoteCalls != 0 {
		t.Fatalf("delete calls = %d, want %d", stub.deleteAccountNoteCalls, 0)
	}

	var resp mastodonValidationErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got, want := resp.Error, "Validation failed: Comment character limit of 2000 exceeded"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	details, ok := resp.Details["comment"]
	if !ok {
		t.Fatalf("missing comment validation details")
	}
	if len(details) != 1 {
		t.Fatalf("details length = %d, want %d", len(details), 1)
	}
	if got, want := details[0].Error, "ERR_TOO_LONG"; got != want {
		t.Fatalf("details error = %q, want %q", got, want)
	}
	if got, want := details[0].Description, "character limit of 2000 exceeded"; got != want {
		t.Fatalf("details description = %q, want %q", got, want)
	}
}

package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockStatusRequest(method, path string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, nil)
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

func TestStatusesHandler_History_MockMode(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil)

	tests := []struct {
		name       string
		statusID   string
		wantCode   int
		wantLength int
	}{
		{
			name:       "valid status ID returns empty array in mock mode",
			statusID:   "123456789",
			wantCode:   http.StatusOK,
			wantLength: 0, // Mock mode returns empty array
		},
		{
			name:       "empty status ID returns not found",
			statusID:   "",
			wantCode:   http.StatusNotFound,
			wantLength: 0,
		},
		{
			name:       "invalid status ID returns not found",
			statusID:   "invalid",
			wantCode:   http.StatusNotFound,
			wantLength: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockStatusRequest(http.MethodGet, "/api/v1/statuses/"+tt.statusID+"/history", 12345)
			req.SetPathValue("id", tt.statusID)

			h.History(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var history []StatusEdit
				if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if len(history) != tt.wantLength {
					t.Errorf("history length = %d, want %d", len(history), tt.wantLength)
				}
			}
		})
	}
}

func TestStatusesHandler_Source_MockMode(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil)

	tests := []struct {
		name        string
		statusID    string
		accountID   int64
		wantCode    int
		wantText    string
		wantSpoiler string
	}{
		{
			name:        "authenticated user gets source",
			statusID:    "123456789",
			accountID:   12345,
			wantCode:    http.StatusOK,
			wantText:    "Hello, world!",
			wantSpoiler: "",
		},
		{
			name:      "unauthenticated user gets unauthorized",
			statusID:  "123456789",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "empty status ID returns not found",
			statusID:  "",
			accountID: 12345,
			wantCode:  http.StatusNotFound,
		},
		{
			name:      "invalid status ID returns not found",
			statusID:  "invalid",
			accountID: 12345,
			wantCode:  http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockStatusRequest(http.MethodGet, "/api/v1/statuses/"+tt.statusID+"/source", tt.accountID)
			req.SetPathValue("id", tt.statusID)

			h.Source(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var source StatusSource
				if err := json.Unmarshal(rec.Body.Bytes(), &source); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if source.Text != tt.wantText {
					t.Errorf("text = %q, want %q", source.Text, tt.wantText)
				}
				if source.SpoilerText != tt.wantSpoiler {
					t.Errorf("spoiler_text = %q, want %q", source.SpoilerText, tt.wantSpoiler)
				}
				if source.ID != tt.statusID {
					t.Errorf("id = %q, want %q", source.ID, tt.statusID)
				}
			}
		})
	}
}

func TestStatusEditStruct(t *testing.T) {
	now := time.Now()
	edit := StatusEdit{
		Content:     "<p>Test content</p>",
		SpoilerText: "CW: test",
		Sensitive:   true,
		CreatedAt:   now,
		Account: Account{
			ID:       "1",
			Username: "testuser",
			Acct:     "testuser",
		},
		Poll:             nil,
		MediaAttachments: []MediaAttach{},
		Emojis:           []CustomEmoji{},
	}

	data, err := json.Marshal(edit)
	if err != nil {
		t.Fatalf("Failed to marshal StatusEdit: %v", err)
	}

	var unmarshaled StatusEdit
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal StatusEdit: %v", err)
	}

	if unmarshaled.Content != edit.Content {
		t.Errorf("content = %q, want %q", unmarshaled.Content, edit.Content)
	}
	if unmarshaled.SpoilerText != edit.SpoilerText {
		t.Errorf("spoiler_text = %q, want %q", unmarshaled.SpoilerText, edit.SpoilerText)
	}
	if unmarshaled.Sensitive != edit.Sensitive {
		t.Errorf("sensitive = %v, want %v", unmarshaled.Sensitive, edit.Sensitive)
	}
}

func TestStatusSourceStruct(t *testing.T) {
	source := StatusSource{
		ID:          "123456789",
		Text:        "Raw text content with @mention and #hashtag",
		SpoilerText: "Content warning",
	}

	data, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("Failed to marshal StatusSource: %v", err)
	}

	var unmarshaled StatusSource
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal StatusSource: %v", err)
	}

	if unmarshaled.ID != source.ID {
		t.Errorf("id = %q, want %q", unmarshaled.ID, source.ID)
	}
	if unmarshaled.Text != source.Text {
		t.Errorf("text = %q, want %q", unmarshaled.Text, source.Text)
	}
	if unmarshaled.SpoilerText != source.SpoilerText {
		t.Errorf("spoiler_text = %q, want %q", unmarshaled.SpoilerText, source.SpoilerText)
	}
}

func TestFormatContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string returns empty",
			input: "",
			want:  "",
		},
		{
			name:  "simple text wrapped in paragraph",
			input: "Hello world",
			want:  "<p>Hello world</p>",
		},
		{
			name:  "text with newlines",
			input: "Line 1\nLine 2",
			want:  "<p>Line 1\nLine 2</p>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatContent(tt.input)
			if got != tt.want {
				t.Errorf("formatContent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStatusesHandler_History_EmptyID(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil)

	req, rec := mockStatusRequest(http.MethodGet, "/api/v1/statuses//history", 12345)
	req.SetPathValue("id", "")

	h.History(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestStatusesHandler_Source_RequiresAuth(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil)

	// Request without authentication context
	req := httptest.NewRequest(http.MethodGet, "/api/v1/statuses/123/source", nil)
	req.SetPathValue("id", "123")
	rec := httptest.NewRecorder()

	h.Source(rec, req)

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

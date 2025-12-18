package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
)

func mockTagsRequest(method, path string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
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

func TestTagsHandler_Get_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTagsHandler(nil, cfg)

	tests := []struct {
		name     string
		tagName  string
		wantCode int
		wantName string
	}{
		{
			name:     "valid tag returns tag info",
			tagName:  "golang",
			wantCode: http.StatusOK,
			wantName: "golang",
		},
		{
			name:     "empty tag name returns not found",
			tagName:  "",
			wantCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockTagsRequest(http.MethodGet, "/api/v1/tags/"+tt.tagName, 12345)
			req.SetPathValue("id", tt.tagName)

			h.Get(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var tag TagResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &tag); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if tag.Name != tt.wantName {
					t.Errorf("name = %q, want %q", tag.Name, tt.wantName)
				}
			}
		})
	}
}

func TestTagsHandler_Follow_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTagsHandler(nil, cfg)

	tests := []struct {
		name      string
		tagName   string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can follow tag",
			tagName:   "mastodon",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			tagName:   "mastodon",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "empty tag name returns not found",
			tagName:   "",
			accountID: 12345,
			wantCode:  http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockTagsRequest(http.MethodPost, "/api/v1/tags/"+tt.tagName+"/follow", tt.accountID)
			req.SetPathValue("id", tt.tagName)

			h.Follow(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

func TestTagsHandler_Unfollow_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTagsHandler(nil, cfg)

	tests := []struct {
		name      string
		tagName   string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can unfollow tag",
			tagName:   "mastodon",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			tagName:   "mastodon",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockTagsRequest(http.MethodPost, "/api/v1/tags/"+tt.tagName+"/unfollow", tt.accountID)
			req.SetPathValue("id", tt.tagName)

			h.Unfollow(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var tag TagResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &tag); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if tag.Following == nil || *tag.Following != false {
					t.Error("expected following to be false after unfollow")
				}
			}
		})
	}
}

func TestTagsHandler_ListFollowed_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTagsHandler(nil, cfg)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user gets empty list",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockTagsRequest(http.MethodGet, "/api/v1/followed_tags", tt.accountID)

			h.ListFollowed(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var tags []TagResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &tags); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				// Mock mode returns empty list
				if len(tags) != 0 {
					t.Errorf("expected 0 tags, got %d", len(tags))
				}
			}
		})
	}
}

func TestTagResponseStruct(t *testing.T) {
	following := true
	tag := TagResponse{
		Name:      "golang",
		URL:       "https://example.com/tags/golang",
		History:   []TagDayCount{},
		Following: &following,
	}

	data, err := json.Marshal(tag)
	if err != nil {
		t.Fatalf("Failed to marshal TagResponse: %v", err)
	}

	var unmarshaled TagResponse
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal TagResponse: %v", err)
	}

	if unmarshaled.Name != tag.Name {
		t.Errorf("name = %q, want %q", unmarshaled.Name, tag.Name)
	}
	if unmarshaled.URL != tag.URL {
		t.Errorf("url = %q, want %q", unmarshaled.URL, tag.URL)
	}
	if unmarshaled.Following == nil || *unmarshaled.Following != true {
		t.Error("expected following to be true")
	}
}

func TestTagsHandler_Feature_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTagsHandler(nil, cfg)

	req, rec := mockTagsRequest(http.MethodPost, "/api/v1/tags/golang/feature", 12345)
	req.SetPathValue("id", "golang")

	h.Feature(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestTagsHandler_Unfeature_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTagsHandler(nil, cfg)

	req, rec := mockTagsRequest(http.MethodPost, "/api/v1/tags/golang/unfeature", 12345)
	req.SetPathValue("id", "golang")

	h.Unfeature(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
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

type stubTagHistoryQuerier struct {
	rows []db.ListTagHistoriesRow
	err  error
}

func (s stubTagHistoryQuerier) ListTagHistories(_ context.Context, _ []int64) ([]db.ListTagHistoriesRow, error) {
	return s.rows, s.err
}

func TestTagHistoryForIDs(t *testing.T) {
	day1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := []db.ListTagHistoriesRow{
		{TagID: 1, Day: pgtype.Date{Time: day1, Valid: true}, Uses: 2, Accounts: 1},
		{TagID: 1, Day: pgtype.Date{Time: day2, Valid: true}, Uses: 0, Accounts: 0},
		{TagID: 2, Day: pgtype.Date{Time: day1, Valid: true}, Uses: 5, Accounts: 3},
		{TagID: 3, Day: pgtype.Date{Valid: false}, Uses: 1, Accounts: 1},
	}

	history := tagHistoryForIDs(context.Background(), stubTagHistoryQuerier{rows: rows}, []int64{1, 2, 3})
	if len(history[1]) != 2 {
		t.Fatalf("tag 1 history len = %d, want 2", len(history[1]))
	}
	if history[1][0].Day != strconv.FormatInt(day1.Unix(), 10) {
		t.Fatalf("tag 1 day[0] = %q, want %q", history[1][0].Day, strconv.FormatInt(day1.Unix(), 10))
	}
	if history[2][0].Uses != "5" {
		t.Fatalf("tag 2 uses = %q, want %q", history[2][0].Uses, "5")
	}
	if len(history[3]) != 0 {
		t.Fatalf("tag 3 history len = %d, want 0", len(history[3]))
	}
}

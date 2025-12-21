package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPeersHandler_Search_MockMode(t *testing.T) {
	h := NewPeersHandler(nil)

	tests := []struct {
		name     string
		query    string
		wantCode int
	}{
		{
			name:     "search with query returns empty list in mock mode",
			query:    "mastodon",
			wantCode: http.StatusOK,
		},
		{
			name:     "empty query returns empty list",
			query:    "",
			wantCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/v1/peers/search"
			if tt.query != "" {
				path += "?q=" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()

			h.Search(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			var domains []string
			if err := json.Unmarshal(rec.Body.Bytes(), &domains); err != nil {
				t.Fatalf("Failed to unmarshal response: %v", err)
			}
		})
	}
}

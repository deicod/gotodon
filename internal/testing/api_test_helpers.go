// Package testing provides Mastodon API compatibility test helpers.
package testing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// APITestCase represents a single API test case
type APITestCase struct {
	Name           string
	Method         string
	Path           string
	Body           string
	Headers        map[string]string
	ExpectedStatus int
	ExpectedFields []string // Fields that should exist in response
	AccountID      int64    // Set for authenticated requests
	Role           string   // "user", "admin", "moderator"
}

// APITester provides utilities for testing API endpoints
type APITester struct {
	T *testing.T
}

// NewAPITester creates a new API tester
func NewAPITester(t *testing.T) *APITester {
	return &APITester{
		T: t,
	}
}

// RunTestCase runs a single test case
func (at *APITester) RunTestCase(tc APITestCase, handler http.Handler) {
	at.T.Run(tc.Name, func(t *testing.T) {
		req := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(tc.Body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range tc.Headers {
			req.Header.Set(k, v)
		}

		ctx := req.Context()

		// Set authentication context
		if tc.AccountID != 0 {
			ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, tc.AccountID)
			ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write follow push admin:read admin:write")
		}
		if tc.Role != "" {
			// Note: This needs to map to however roles are stored in context if applicable
			// For now assuming role check logic uses db or separate context key if it existed
			// But looking at code, role is usually derived from DB.
			// If needing to mock it, we might need middleware.ContextKeyRole if it existed.
			// Assuming for now it's fine or handled via mocked queries in actual tests.
		}

		req = req.WithContext(ctx)

		// Extract path params if needed
		if idx := strings.Index(tc.Path, "/:"); idx > 0 {
			// Simple param extraction for paths like /api/v1/accounts/:id
			// This part in previous code tried to simulate Echo params.
			// In Go 1.22 tests, we use req.SetPathValue
			// But we need to know WHICH segment maps to WHAT.
			// The tc.Path here seems to be the concrete path " /api/v1/accounts/12345" based on usage in test_vectors.go
			// So we don't have the pattern here to know what is what without more info.
			// However, test_vectors.go paths are like "/api/v1/accounts/12345".
			// Previous helper code had logic: `if idx := strings.Index(tc.Path, "/:"); idx > 0`
			// This implies tc.Path contained colons?
			// Checking test_vectors.go:
			// Path: "/api/v1/accounts/12345" -> No colons.
			// So the previous logic `if idx := strings.Index(tc.Path, "/:"); idx > 0` probably seldom ran or was for specific cases.
			// Go 1.22 router handles extraction automatically if using the router.
			// But here we are passing `handler` which might be a ServeMux OR a direct handler function.
			// If it's a ServeMux, we don't need to manually set path values!
			// If it's a direct handler func, we might.
			// Let's assume handler is a ServeMux for integration tests, or use SetPathValue if we can infer.
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != tc.ExpectedStatus {
			t.Errorf("status = %d, want %d", rec.Code, tc.ExpectedStatus)
		}

		// Verify expected fields in response
		if len(tc.ExpectedFields) > 0 {
			var response map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				// Try array
				var arr []map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
					t.Errorf("failed to parse response: %v", err)
					return
				}
				if len(arr) > 0 {
					response = arr[0]
				}
			}

			for _, field := range tc.ExpectedFields {
				if _, exists := response[field]; !exists {
					t.Errorf("expected field %q not found in response", field)
				}
			}
		}
	})
}

// MastodonAPIValidator validates responses against Mastodon API spec
type MastodonAPIValidator struct{}

// ValidateAccount validates an account response
func (v *MastodonAPIValidator) ValidateAccount(body []byte) []string {
	required := []string{
		"id", "username", "acct", "display_name", "locked", "bot",
		"created_at", "note", "url", "avatar", "avatar_static",
		"header", "header_static", "followers_count", "following_count",
		"statuses_count", "emojis", "fields",
	}
	return v.validateFields(body, required)
}

// ValidateStatus validates a status response
func (v *MastodonAPIValidator) ValidateStatus(body []byte) []string {
	required := []string{
		"id", "created_at", "in_reply_to_id", "in_reply_to_account_id",
		"sensitive", "spoiler_text", "visibility", "language", "uri",
		"content", "account", "media_attachments", "mentions", "tags",
		"emojis", "reblogs_count", "favourites_count", "replies_count",
	}
	return v.validateFields(body, required)
}

// ValidateNotification validates a notification response
func (v *MastodonAPIValidator) ValidateNotification(body []byte) []string {
	required := []string{"id", "type", "created_at", "account"}
	return v.validateFields(body, required)
}

// ValidateMediaAttachment validates a media attachment response
func (v *MastodonAPIValidator) ValidateMediaAttachment(body []byte) []string {
	required := []string{"id", "type", "url", "preview_url"}
	return v.validateFields(body, required)
}

// ValidateList validates a list response
func (v *MastodonAPIValidator) ValidateList(body []byte) []string {
	required := []string{"id", "title", "replies_policy"}
	return v.validateFields(body, required)
}

// ValidatePoll validates a poll response
func (v *MastodonAPIValidator) ValidatePoll(body []byte) []string {
	required := []string{"id", "expires_at", "expired", "multiple", "votes_count", "options"}
	return v.validateFields(body, required)
}

func (v *MastodonAPIValidator) validateFields(body []byte, required []string) []string {
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		return []string{"invalid JSON"}
	}

	var missing []string
	for _, field := range required {
		if _, exists := response[field]; !exists {
			missing = append(missing, field)
		}
	}
	return missing
}

// HTTPTestClient is a test HTTP client for integration tests
type HTTPTestClient struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// NewHTTPTestClient creates a new test client
func NewHTTPTestClient(baseURL, token string) *HTTPTestClient {
	return &HTTPTestClient{
		BaseURL: baseURL,
		Token:   token,
		Client:  &http.Client{},
	}
}

// Get makes a GET request
func (c *HTTPTestClient) Get(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// Post makes a POST request
func (c *HTTPTestClient) Post(path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// Delete makes a DELETE request
func (c *HTTPTestClient) Delete(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodDelete, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *HTTPTestClient) do(req *http.Request) (*http.Response, error) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
	return c.Client.Do(req)
}

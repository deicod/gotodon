// Package testing provides Mastodon API compatibility test helpers.
package testing

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
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
	Echo *echo.Echo
	T    *testing.T
}

// NewAPITester creates a new API tester
func NewAPITester(t *testing.T) *APITester {
	return &APITester{
		Echo: echo.New(),
		T:    t,
	}
}

// RunTestCase runs a single test case
func (at *APITester) RunTestCase(tc APITestCase, handler echo.HandlerFunc) {
	at.T.Run(tc.Name, func(t *testing.T) {
		req := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(tc.Body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		for k, v := range tc.Headers {
			req.Header.Set(k, v)
		}

		rec := httptest.NewRecorder()
		c := at.Echo.NewContext(req, rec)

		// Set authentication context
		if tc.AccountID != 0 {
			c.Set("account_id", tc.AccountID)
			c.Set("scopes", "read write follow push admin:read admin:write")
		}
		if tc.Role != "" {
			c.Set("role", tc.Role)
		}

		// Extract path params if needed
		if idx := strings.Index(tc.Path, "/:"); idx > 0 {
			// Simple param extraction for paths like /api/v1/accounts/:id
			parts := strings.Split(tc.Path, "/")
			for i, part := range parts {
				if strings.HasPrefix(part, ":") {
					paramName := part[1:]
					c.SetParamNames(paramName)
					if i+1 < len(parts) {
						c.SetParamValues(parts[i+1])
					}
				}
			}
		}

		err := handler(c)
		if err != nil {
			he, ok := err.(*echo.HTTPError)
			if ok {
				if he.Code != tc.ExpectedStatus {
					t.Errorf("status = %d, want %d", he.Code, tc.ExpectedStatus)
				}
				return
			}
			t.Fatalf("handler error: %v", err)
		}

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

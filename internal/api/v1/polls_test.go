package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockPollRequest(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func mockPollFormRequest(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestPollsHandler_Get_DBUnavailable(t *testing.T) {
	h := NewPollsHandler(nil)

	req, rec := mockPollRequest(http.MethodGet, "/api/v1/polls/123", "")
	req.SetPathValue("id", "123")

	h.Get(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestPollsHandler_Vote_DBUnavailable(t *testing.T) {
	h := NewPollsHandler(nil)

	req, rec := mockPollRequest(http.MethodPost, "/api/v1/polls/123/votes", `{"choices":[0]}`)
	req.SetPathValue("id", "123")

	h.Vote(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestPollsHandler_Vote_Form_DBUnavailable(t *testing.T) {
	h := NewPollsHandler(nil)

	req, rec := mockPollFormRequest(http.MethodPost, "/api/v1/polls/123/votes", "choices[]=1")
	req.SetPathValue("id", "123")

	h.Vote(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestPollsHandler_VoteValidation(t *testing.T) {
	h := NewPollsHandler(nil)

	req, rec := mockPollRequest(http.MethodPost, "/api/v1/polls/123/votes", `{"choices":[]}`)
	req.SetPathValue("id", "123")

	h.Vote(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	req, rec = mockPollRequest(http.MethodPost, "/api/v1/polls/bad/votes", `{"choices":[0]}`)
	req.SetPathValue("id", "bad")

	h.Vote(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

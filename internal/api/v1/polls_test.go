package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestPollsHandler_CreateAndGet(t *testing.T) {
	h := NewPollsHandler(nil)

	// Create a poll
	poll := h.CreatePoll(12345, []string{"Option A", "Option B", "Option C"}, 86400, false, false)

	// Get it
	req, rec := mockPollRequest(http.MethodGet, "/api/v1/polls/"+strconv.FormatInt(poll.ID, 10), "")
	req.SetPathValue("id", strconv.FormatInt(poll.ID, 10))

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp PollResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(resp.Options) != 3 {
		t.Errorf("Options length = %d, want 3", len(resp.Options))
	}

	if resp.Options[0].Title != "Option A" {
		t.Errorf("Options[0].Title = %q, want %q", resp.Options[0].Title, "Option A")
	}
}

func TestPollsHandler_Vote(t *testing.T) {
	h := NewPollsHandler(nil)

	// Create a poll
	poll := h.CreatePoll(12345, []string{"Yes", "No"}, 86400, false, false)

	// Vote
	req, rec := mockPollRequest(http.MethodPost, "/api/v1/polls/"+strconv.FormatInt(poll.ID, 10)+"/votes", `{"choices":[0]}`)
	req.SetPathValue("id", strconv.FormatInt(poll.ID, 10))

	h.Vote(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp PollResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	if !resp.Voted {
		t.Error("Voted should be true")
	}

	if len(resp.OwnVotes) != 1 || resp.OwnVotes[0] != 0 {
		t.Errorf("OwnVotes = %v, want [0]", resp.OwnVotes)
	}
}

func TestPollsHandler_VoteValidation(t *testing.T) {
	h := NewPollsHandler(nil)

	// Single choice poll
	poll := h.CreatePoll(12345, []string{"A", "B"}, 86400, false, false)
	pollID := strconv.FormatInt(poll.ID, 10)

	// Try multiple choices on single-choice poll
	req, rec := mockPollRequest(http.MethodPost, "/api/v1/polls/"+pollID+"/votes", `{"choices":[0,1]}`)
	req.SetPathValue("id", pollID)

	h.Vote(rec, req)

	if rec.Code < 400 {
		t.Error("Should reject multiple choices on single-choice poll")
	}
}

func TestPollsHandler_ExpiredPoll(t *testing.T) {
	h := NewPollsHandler(nil)

	// Create an expired poll
	pollID := h.idGen.Generate()
	poll := &PollData{
		ID:        pollID,
		AccountID: 12345,
		ExpiresAt: time.Now().Add(-time.Hour), // Already expired
		Options:   []string{"A", "B"},
		Votes:     []int{0, 0},
	}
	h.memPolls[pollID] = poll

	// Try to vote
	req, rec := mockPollRequest(http.MethodPost, "/api/v1/polls/"+strconv.FormatInt(pollID, 10)+"/votes", `{"choices":[0]}`)
	req.SetPathValue("id", strconv.FormatInt(pollID, 10))

	h.Vote(rec, req)

	if rec.Code < 400 {
		t.Error("Should reject vote on expired poll")
	}
}

func TestPollsHandler_MultipleChoice(t *testing.T) {
	h := NewPollsHandler(nil)

	// Create multiple choice poll
	poll := h.CreatePoll(12345, []string{"A", "B", "C"}, 86400, true, false)
	pollID := strconv.FormatInt(poll.ID, 10)

	// Vote for multiple options
	req, rec := mockPollRequest(http.MethodPost, "/api/v1/polls/"+pollID+"/votes", `{"choices":[0,2]}`)
	req.SetPathValue("id", pollID)

	h.Vote(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp PollResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.OwnVotes) != 2 {
		t.Errorf("OwnVotes length = %d, want 2", len(resp.OwnVotes))
	}
}

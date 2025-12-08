package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func mockPollContext(e *echo.Echo, method, path string, body string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("account_id", int64(12345))
	c.Set("scopes", "read write")
	return c, rec
}

func TestPollsHandler_CreateAndGet(t *testing.T) {
	e := echo.New()
	h := NewPollsHandler()

	// Create a poll
	poll := h.CreatePoll(12345, []string{"Option A", "Option B", "Option C"}, 86400, false, false)

	// Get it
	c, rec := mockPollContext(e, http.MethodGet, "/api/v1/polls/"+strconv.FormatInt(poll.ID, 10), "")
	c.SetParamNames("id")
	c.SetParamValues(strconv.FormatInt(poll.ID, 10))

	err := h.Get(c)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

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
	e := echo.New()
	h := NewPollsHandler()

	// Create a poll
	poll := h.CreatePoll(12345, []string{"Yes", "No"}, 86400, false, false)

	// Vote
	c, rec := mockPollContext(e, http.MethodPost, "/api/v1/polls/"+strconv.FormatInt(poll.ID, 10)+"/votes", `{"choices":[0]}`)
	c.SetParamNames("id")
	c.SetParamValues(strconv.FormatInt(poll.ID, 10))

	err := h.Vote(c)
	if err != nil {
		t.Fatalf("Vote() error = %v", err)
	}

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
	e := echo.New()
	h := NewPollsHandler()

	// Single choice poll
	poll := h.CreatePoll(12345, []string{"A", "B"}, 86400, false, false)
	pollID := strconv.FormatInt(poll.ID, 10)

	// Try multiple choices on single-choice poll
	c, rec := mockPollContext(e, http.MethodPost, "/api/v1/polls/"+pollID+"/votes", `{"choices":[0,1]}`)
	c.SetParamNames("id")
	c.SetParamValues(pollID)

	err := h.Vote(c)
	if err == nil && rec.Code < 400 {
		t.Error("Should reject multiple choices on single-choice poll")
	}
}

func TestPollsHandler_ExpiredPoll(t *testing.T) {
	e := echo.New()
	h := NewPollsHandler()

	// Create an expired poll
	pollID := h.idGen.Generate()
	poll := &PollData{
		ID:        pollID,
		AccountID: 12345,
		ExpiresAt: time.Now().Add(-time.Hour), // Already expired
		Options:   []string{"A", "B"},
		Votes:     []int{0, 0},
	}
	h.polls[pollID] = poll

	// Try to vote
	c, rec := mockPollContext(e, http.MethodPost, "/api/v1/polls/"+strconv.FormatInt(pollID, 10)+"/votes", `{"choices":[0]}`)
	c.SetParamNames("id")
	c.SetParamValues(strconv.FormatInt(pollID, 10))

	err := h.Vote(c)
	if err == nil && rec.Code < 400 {
		t.Error("Should reject vote on expired poll")
	}
}

func TestPollsHandler_MultipleChoice(t *testing.T) {
	e := echo.New()
	h := NewPollsHandler()

	// Create multiple choice poll
	poll := h.CreatePoll(12345, []string{"A", "B", "C"}, 86400, true, false)
	pollID := strconv.FormatInt(poll.ID, 10)

	// Vote for multiple options
	c, rec := mockPollContext(e, http.MethodPost, "/api/v1/polls/"+pollID+"/votes", `{"choices":[0,2]}`)
	c.SetParamNames("id")
	c.SetParamValues(pollID)

	err := h.Vote(c)
	if err != nil {
		t.Fatalf("Vote() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp PollResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.OwnVotes) != 2 {
		t.Errorf("OwnVotes length = %d, want 2", len(resp.OwnVotes))
	}
}

// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// PollsHandler handles poll endpoints
type PollsHandler struct {
	queries *db.Queries
	idGen   *snowflake.Generator
	// In-memory storage fallback
	polls     map[int64]*PollData
	pollVotes map[int64]map[int64][]int // poll_id -> account_id -> choices
}

// PollData stores poll information
type PollData struct {
	ID          int64
	AccountID   int64
	ExpiresAt   time.Time
	Options     []string
	Votes       []int
	Multiple    bool
	HideTotals  bool
	VotesCount  int
	VotersCount int
}

// NewPollsHandler creates a new polls handler
func NewPollsHandler(queries *db.Queries) *PollsHandler {
	return &PollsHandler{
		queries:   queries,
		idGen:     snowflake.NewGenerator(),
		polls:     make(map[int64]*PollData),
		pollVotes: make(map[int64]map[int64][]int),
	}
}

// PollResponse is the API response for a poll
type PollResponse struct {
	ID          string       `json:"id"`
	ExpiresAt   *time.Time   `json:"expires_at"`
	Expired     bool         `json:"expired"`
	Multiple    bool         `json:"multiple"`
	VotesCount  int          `json:"votes_count"`
	VotersCount *int         `json:"voters_count,omitempty"`
	Voted       bool         `json:"voted"`
	OwnVotes    []int        `json:"own_votes"`
	Options     []PollOption `json:"options"`
	Emojis      []any        `json:"emojis"`
}

// PollOption is a poll option in the response
type PollOption struct {
	Title      string `json:"title"`
	VotesCount *int   `json:"votes_count"`
}

// Get handles GET /api/v1/polls/:id
func (h *PollsHandler) Get(c echo.Context) error {
	pollID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	poll, exists := h.polls[pollID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	// Check if current user voted
	var ownVotes []int
	voted := false
	accountID, ok := middleware.GetAccountID(c)
	if ok {
		if votes, exists := h.pollVotes[pollID]; exists {
			if v, exists := votes[accountID]; exists {
				ownVotes = v
				voted = true
			}
		}
	}

	return c.JSON(http.StatusOK, h.buildPollResponse(poll, voted, ownVotes))
}

// Vote handles POST /api/v1/polls/:id/votes
func (h *PollsHandler) Vote(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	pollID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	poll, exists := h.polls[pollID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	// Check if expired
	if time.Now().After(poll.ExpiresAt) {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Poll has ended",
		})
	}

	// Check if already voted
	if h.pollVotes[pollID] == nil {
		h.pollVotes[pollID] = make(map[int64][]int)
	}
	if _, voted := h.pollVotes[pollID][accountID]; voted {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: You have already voted on this poll",
		})
	}

	var req struct {
		Choices []int `json:"choices" form:"choices[]"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if len(req.Choices) == 0 {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Choices can't be blank",
		})
	}

	// Validate choices
	if !poll.Multiple && len(req.Choices) > 1 {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: You can only select one option",
		})
	}

	for _, choice := range req.Choices {
		if choice < 0 || choice >= len(poll.Options) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Invalid choice",
			})
		}
	}

	// Record votes
	h.pollVotes[pollID][accountID] = req.Choices
	poll.VotersCount++
	for _, choice := range req.Choices {
		poll.Votes[choice]++
		poll.VotesCount++
	}

	return c.JSON(http.StatusOK, h.buildPollResponse(poll, true, req.Choices))
}

// CreatePoll creates a poll for a status (internal use)
func (h *PollsHandler) CreatePoll(accountID int64, options []string, expiresIn int, multiple, hideTotals bool) *PollData {
	pollID := h.idGen.Generate()
	poll := &PollData{
		ID:         pollID,
		AccountID:  accountID,
		ExpiresAt:  time.Now().Add(time.Duration(expiresIn) * time.Second),
		Options:    options,
		Votes:      make([]int, len(options)),
		Multiple:   multiple,
		HideTotals: hideTotals,
	}
	h.polls[pollID] = poll
	return poll
}

func (h *PollsHandler) buildPollResponse(poll *PollData, voted bool, ownVotes []int) PollResponse {
	options := make([]PollOption, len(poll.Options))
	for i, opt := range poll.Options {
		votes := poll.Votes[i]
		options[i] = PollOption{
			Title:      opt,
			VotesCount: &votes,
		}
	}

	expired := time.Now().After(poll.ExpiresAt)

	resp := PollResponse{
		ID:          strconv.FormatInt(poll.ID, 10),
		ExpiresAt:   &poll.ExpiresAt,
		Expired:     expired,
		Multiple:    poll.Multiple,
		VotesCount:  poll.VotesCount,
		VotersCount: &poll.VotersCount,
		Voted:       voted,
		OwnVotes:    ownVotes,
		Options:     options,
		Emojis:      []any{},
	}

	if ownVotes == nil {
		resp.OwnVotes = []int{}
	}

	return resp
}

// RegisterRoutes registers poll routes
func (h *PollsHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/polls/:id", h.Get)
	g.POST("/polls/:id/votes", h.Vote)
}

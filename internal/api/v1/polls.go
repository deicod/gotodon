// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// PollsHandler handles poll endpoints
type PollsHandler struct {
	queries *db.Queries
}

// NewPollsHandler creates a new polls handler
func NewPollsHandler(queries *db.Queries) *PollsHandler {
	return &PollsHandler{
		queries: queries,
	}
}

func (h *PollsHandler) useDB() bool {
	return h.queries != nil
}

// PollResponse is the API response for a poll
type PollResponse struct {
	ID          string        `json:"id"`
	ExpiresAt   *time.Time    `json:"expires_at"`
	Expired     bool          `json:"expired"`
	Multiple    bool          `json:"multiple"`
	VotesCount  int           `json:"votes_count"`
	VotersCount *int          `json:"voters_count,omitempty"`
	Voted       bool          `json:"voted"`
	OwnVotes    []int         `json:"own_votes"`
	Options     []PollOption  `json:"options"`
	Emojis      []CustomEmoji `json:"emojis"`
}

// PollOption is a poll option in the response
type PollOption struct {
	Title      string `json:"title"`
	VotesCount *int   `json:"votes_count"`
}

func (h *PollsHandler) dbPollToAPI(ctx context.Context, dbPoll db.Poll, accountID int64) PollResponse {
	// Parse cached tallies (bigint array)
	tallies := make([]int, len(dbPoll.Options))
	for i, tally := range dbPoll.CachedTallies {
		if i >= len(tallies) {
			break
		}
		tallies[i] = int(tally)
	}

	options := make([]PollOption, len(dbPoll.Options))
	for i, opt := range dbPoll.Options {
		votes := 0
		if i < len(tallies) {
			votes = tallies[i]
		}
		options[i] = PollOption{
			Title:      opt,
			VotesCount: &votes,
		}
	}

	var expiresAt *time.Time
	expired := false
	if dbPoll.ExpiresAt.Valid {
		expiresAt = &dbPoll.ExpiresAt.Time
		expired = time.Now().After(dbPoll.ExpiresAt.Time)
	}

	// Check if user voted
	var ownVotes []int
	voted := false
	if h.useDB() && accountID > 0 {
		votes, err := h.queries.GetVotes(ctx, db.GetVotesParams{
			PollID:    dbPoll.ID,
			AccountID: pgtype.Int8{Int64: accountID, Valid: true},
		})
		if err == nil && len(votes) > 0 {
			voted = true
			ownVotes = make([]int, len(votes))
			for i, v := range votes {
				ownVotes[i] = int(v.Choice)
			}
		}
	}
	if ownVotes == nil {
		ownVotes = []int{}
	}

	votersCount := int(dbPoll.VotersCount.Int64)

	return PollResponse{
		ID:          strconv.FormatInt(dbPoll.ID, 10),
		ExpiresAt:   expiresAt,
		Expired:     expired,
		Multiple:    dbPoll.Multiple,
		VotesCount:  int(dbPoll.VotesCount),
		VotersCount: &votersCount,
		Voted:       voted,
		OwnVotes:    ownVotes,
		Options:     options,
		Emojis:      []CustomEmoji{},
	}
}

// Get handles GET /api/v1/polls/:id
func (h *PollsHandler) Get(w http.ResponseWriter, r *http.Request) {
	pollID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	accountID, _ := middleware.GetAccountID(r.Context())
	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Polls unavailable")
		return
	}

	dbPoll, err := h.queries.GetPoll(ctx, pollID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, h.dbPollToAPI(ctx, dbPoll, accountID))
}

// Vote handles POST /api/v1/polls/:id/votes
func (h *PollsHandler) Vote(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	pollID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		Choices []int `json:"choices" form:"choices[]"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if len(req.Choices) == 0 {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Choices can't be blank")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Polls unavailable")
		return
	}

	dbPoll, err := h.queries.GetPoll(ctx, pollID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	// Check expiration
	if dbPoll.ExpiresAt.Valid && time.Now().After(dbPoll.ExpiresAt.Time) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Poll has ended")
		return
	}

	// Check if already voted
	existingVotes, err := h.queries.GetVotes(ctx, db.GetVotesParams{
		PollID:    pollID,
		AccountID: pgtype.Int8{Int64: accountID, Valid: true},
	})
	if err == nil && len(existingVotes) > 0 {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: You have already voted on this poll")
		return
	}

	// Validate choices
	if !dbPoll.Multiple && len(req.Choices) > 1 {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: You can only select one option")
		return
	}
	for _, choice := range req.Choices {
		if choice < 0 || choice >= len(dbPoll.Options) {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Invalid choice")
			return
		}
	}

	// Record votes
	for _, choice := range req.Choices {
		_ = h.queries.Vote(ctx, db.VoteParams{
			PollID:    pollID,
			AccountID: pgtype.Int8{Int64: accountID, Valid: true},
			Choice:    int32(choice),
		})
		_ = h.queries.IncrementVoteCount(ctx, db.IncrementVoteCountParams{
			ID:      pollID,
			Column2: int32(choice),
		})
	}
	_ = h.queries.IncrementVotersCount(ctx, pollID)

	// Refresh and return
	dbPoll, _ = h.queries.GetPoll(ctx, pollID)
	web.JSON(w, http.StatusOK, h.dbPollToAPI(ctx, dbPoll, accountID))
}

// RegisterRoutes registers poll routes
func (h *PollsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/polls/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/polls/{id}/votes", h.Vote)
}

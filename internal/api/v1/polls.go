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
	"github.com/deicod/gotodon/pkg/snowflake"
)

// PollsHandler handles poll endpoints
type PollsHandler struct {
	queries *db.Queries
	idGen   *snowflake.Generator
	// In-memory storage fallback
	memPolls     map[int64]*PollData
	memPollVotes map[int64]map[int64][]int // poll_id -> account_id -> choices
}

// PollData stores poll information (in-memory fallback)
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
		queries:      queries,
		idGen:        snowflake.NewGenerator(),
		memPolls:     make(map[int64]*PollData),
		memPollVotes: make(map[int64]map[int64][]int),
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

	if h.useDB() {
		dbPoll, err := h.queries.GetPoll(ctx, pollID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, h.dbPollToAPI(ctx, dbPoll, accountID))
		return
	}

	// In-memory fallback
	poll, exists := h.memPolls[pollID]
	if !exists {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var ownVotes []int
	voted := false
	if votes, exists := h.memPollVotes[pollID]; exists {
		if v, exists := votes[accountID]; exists {
			ownVotes = v
			voted = true
		}
	}

	web.JSON(w, http.StatusOK, h.buildMemPollResponse(poll, voted, ownVotes))
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

	if h.useDB() {
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
		return
	}

	// In-memory fallback
	poll, exists := h.memPolls[pollID]
	if !exists {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	// Check if expired
	if time.Now().After(poll.ExpiresAt) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Poll has ended")
		return
	}

	// Check if already voted
	if h.memPollVotes[pollID] == nil {
		h.memPollVotes[pollID] = make(map[int64][]int)
	}
	if _, voted := h.memPollVotes[pollID][accountID]; voted {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: You have already voted on this poll")
		return
	}

	// Validate choices
	if !poll.Multiple && len(req.Choices) > 1 {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: You can only select one option")
		return
	}
	for _, choice := range req.Choices {
		if choice < 0 || choice >= len(poll.Options) {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Invalid choice")
			return
		}
	}

	// Record votes
	h.memPollVotes[pollID][accountID] = req.Choices
	poll.VotersCount++
	for _, choice := range req.Choices {
		poll.Votes[choice]++
		poll.VotesCount++
	}

	web.JSON(w, http.StatusOK, h.buildMemPollResponse(poll, true, req.Choices))
}

// CreatePoll creates a poll for a status (internal use)
func (h *PollsHandler) CreatePoll(accountID int64, options []string, expiresIn int, multiple, hideTotals bool) *PollData {
	ctx := context.Background()

	if h.useDB() {
		// Create initial tallies
		tallies := make([]int64, len(options))
		memTallies := make([]int, len(options))

		expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)

		dbPoll, err := h.queries.CreatePoll(ctx, db.CreatePollParams{
			AccountID:     pgtype.Int8{Int64: accountID, Valid: true},
			ExpiresAt:     pgtype.Timestamptz{Time: expiresAt, Valid: true},
			Options:       options,
			CachedTallies: tallies,
			Multiple:      multiple,
			HideTotals:    hideTotals,
		})
		if err == nil {
			// Return memory representation for compatibility
			return &PollData{
				ID:          dbPoll.ID,
				AccountID:   accountID,
				ExpiresAt:   expiresAt,
				Options:     options,
				Votes:       memTallies,
				Multiple:    multiple,
				HideTotals:  hideTotals,
				VotesCount:  0,
				VotersCount: 0,
			}
		}
	}

	// In-memory fallback
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
	h.memPolls[pollID] = poll
	return poll
}

func (h *PollsHandler) buildMemPollResponse(poll *PollData, voted bool, ownVotes []int) PollResponse {
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
		Emojis:      []CustomEmoji{},
	}

	if ownVotes == nil {
		resp.OwnVotes = []int{}
	}

	return resp
}

// RegisterRoutes registers poll routes
func (h *PollsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/polls/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/polls/{id}/votes", h.Vote)
}

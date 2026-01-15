// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/snowflake"
)

const (
	filterActionWarn = 0
	filterActionHide = 1
)

var validFilterContexts = map[string]bool{
	"home":          true,
	"notifications": true,
	"public":        true,
	"thread":        true,
	"account":       true,
}

// FiltersHandler handles v1 filter endpoints.
type FiltersHandler struct {
	queries   *db.Queries
	pool      *pgxpool.Pool
	idGen     *snowflake.Generator
	publisher streaming.EventPublisher

	// In-memory storage for fallback when queries is nil.
	memFilters map[int64]map[int64]*FilterV1
}

// NewFiltersHandler creates a new v1 filters handler.
func NewFiltersHandler(queries *db.Queries, pool *pgxpool.Pool) *FiltersHandler {
	return &FiltersHandler{
		queries:    queries,
		pool:       pool,
		idGen:      snowflake.NewGenerator(),
		memFilters: make(map[int64]map[int64]*FilterV1),
	}
}

// SetPublisher sets the streaming publisher for filter change events.
func (h *FiltersHandler) SetPublisher(publisher streaming.EventPublisher) {
	h.publisher = publisher
}

func (h *FiltersHandler) useDB() bool {
	return h.queries != nil
}

// FilterV1 represents a v1 filter response.
type FilterV1 struct {
	ID           string     `json:"id"`
	Phrase       string     `json:"phrase"`
	Context      []string   `json:"context"`
	WholeWord    bool       `json:"whole_word"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Irreversible bool       `json:"irreversible"`
}

type filterRequest struct {
	Phrase       *string        `json:"phrase" form:"phrase"`
	Context      []string       `json:"context" form:"context[]"`
	ExpiresIn    *flexibleInt64 `json:"expires_in" form:"expires_in"`
	Irreversible *bool          `json:"irreversible" form:"irreversible"`
	WholeWord    *bool          `json:"whole_word" form:"whole_word"`
}

type flexibleInt64 struct {
	Value int64
	Valid bool
}

func (i *flexibleInt64) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		i.Valid = false
		return nil
	}

	var num json.Number
	if err := json.Unmarshal(trimmed, &num); err == nil {
		value, err := num.Int64()
		if err != nil {
			return err
		}
		i.Value = value
		i.Valid = true
		return nil
	}

	var str string
	if err := json.Unmarshal(trimmed, &str); err != nil {
		return err
	}
	str = strings.TrimSpace(str)
	if str == "" {
		i.Valid = false
		return nil
	}
	value, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return err
	}
	i.Value = value
	i.Valid = true
	return nil
}

func filterV1FromDB(filter db.CustomFilter, keyword db.CustomFilterKeyword) FilterV1 {
	return FilterV1{
		ID:           strconv.FormatInt(keyword.ID, 10),
		Phrase:       keyword.Keyword,
		Context:      filter.Context,
		WholeWord:    keyword.WholeWord,
		ExpiresAt:    expiresAtPtr(filter.ExpiresAt),
		Irreversible: filter.Action == filterActionHide,
	}
}

func filterV1FromKeywordRow(row db.ListFilterKeywordsForAccountRow) FilterV1 {
	return FilterV1{
		ID:           strconv.FormatInt(row.ID, 10),
		Phrase:       row.Keyword,
		Context:      row.Context,
		WholeWord:    row.WholeWord,
		ExpiresAt:    expiresAtPtr(row.ExpiresAt),
		Irreversible: row.Action == filterActionHide,
	}
}

func expiresAtPtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	ts := value.Time
	return &ts
}

func normalizeFilterContexts(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func validateFilterContexts(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !validFilterContexts[value] {
			return false
		}
	}
	return true
}

func computeExpiresAt(expiresIn *flexibleInt64) pgtype.Timestamptz {
	if expiresIn == nil || !expiresIn.Valid || expiresIn.Value <= 0 {
		return pgtype.Timestamptz{Valid: false}
	}
	exp := time.Now().Add(time.Duration(expiresIn.Value) * time.Second)
	return pgtype.Timestamptz{Time: exp, Valid: true}
}

func sameExpiresAt(a, b pgtype.Timestamptz) bool {
	if a.Valid != b.Valid {
		return false
	}
	if !a.Valid {
		return true
	}
	return a.Time.Equal(b.Time)
}

// List handles GET /api/v1/filters
func (h *FiltersHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.useDB() {
		rows, err := h.queries.ListFilterKeywordsForAccount(r.Context(), accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to list filters")
			return
		}

		result := make([]FilterV1, len(rows))
		for i, row := range rows {
			result[i] = filterV1FromKeywordRow(row)
		}
		web.JSON(w, http.StatusOK, result)
		return
	}

	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		web.JSON(w, http.StatusOK, []FilterV1{})
		return
	}

	ids := make([]int64, 0, len(accountFilters))
	for id := range accountFilters {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b int64) int {
		if a > b {
			return -1
		}
		if a < b {
			return 1
		}
		return 0
	})

	result := make([]FilterV1, 0, len(ids))
	for _, id := range ids {
		result = append(result, *accountFilters[id])
	}
	web.JSON(w, http.StatusOK, result)
}

// Get handles GET /api/v1/filters/:id
func (h *FiltersHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		ctx := r.Context()
		keyword, err := h.queries.GetFilterKeyword(ctx, db.GetFilterKeywordParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		filter, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        keyword.CustomFilterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		web.JSON(w, http.StatusOK, filterV1FromDB(filter, keyword))
		return
	}

	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, accountFilters[filterID])
}

// Create handles POST /api/v1/filters
func (h *FiltersHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	var req filterRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	phrase := ""
	if req.Phrase != nil {
		phrase = strings.TrimSpace(*req.Phrase)
	}
	if phrase == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Phrase is required")
		return
	}

	contexts := normalizeFilterContexts(req.Context)
	if !validateFilterContexts(contexts) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Context is invalid")
		return
	}

	wholeWord := true
	if req.WholeWord != nil {
		wholeWord = *req.WholeWord
	}

	action := int32(filterActionWarn)
	if req.Irreversible != nil && *req.Irreversible {
		action = filterActionHide
	}

	expiresAt := computeExpiresAt(req.ExpiresIn)

	if h.useDB() {
		ctx := r.Context()
		if h.pool == nil {
			dbFilter, err := h.queries.CreateFilter(ctx, db.CreateFilterParams{
				AccountID: accountID,
				Phrase:    phrase,
				Context:   contexts,
				Action:    action,
				ExpiresAt: expiresAt,
			})
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to create filter")
				return
			}
			dbKeyword, err := h.queries.CreateFilterKeyword(ctx, db.CreateFilterKeywordParams{
				CustomFilterID: dbFilter.ID,
				Keyword:        phrase,
				WholeWord:      wholeWord,
			})
			if err != nil {
				_ = h.queries.DeleteFilter(ctx, db.DeleteFilterParams{
					ID:        dbFilter.ID,
					AccountID: accountID,
				})
				web.JSONError(w, http.StatusInternalServerError, "Failed to create filter")
				return
			}
			web.JSON(w, http.StatusOK, filterV1FromDB(dbFilter, dbKeyword))
			h.publishFiltersChanged(ctx, accountID)
			return
		}

		tx, err := h.pool.Begin(ctx)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create filter")
			return
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()

		qtx := h.queries.WithTx(tx)
		dbFilter, err := qtx.CreateFilter(ctx, db.CreateFilterParams{
			AccountID: accountID,
			Phrase:    phrase,
			Context:   contexts,
			Action:    action,
			ExpiresAt: expiresAt,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create filter")
			return
		}

		dbKeyword, err := qtx.CreateFilterKeyword(ctx, db.CreateFilterKeywordParams{
			CustomFilterID: dbFilter.ID,
			Keyword:        phrase,
			WholeWord:      wholeWord,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create filter")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create filter")
			return
		}

		web.JSON(w, http.StatusOK, filterV1FromDB(dbFilter, dbKeyword))
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	filterID := h.idGen.Generate()
	filter := &FilterV1{
		ID:           strconv.FormatInt(filterID, 10),
		Phrase:       phrase,
		Context:      contexts,
		WholeWord:    wholeWord,
		ExpiresAt:    expiresAtPtr(expiresAt),
		Irreversible: action == filterActionHide,
	}
	if h.memFilters[accountID] == nil {
		h.memFilters[accountID] = make(map[int64]*FilterV1)
	}
	h.memFilters[accountID][filterID] = filter
	web.JSON(w, http.StatusOK, filter)
	h.publishFiltersChanged(r.Context(), accountID)
}

// Update handles PUT /api/v1/filters/:id
func (h *FiltersHandler) Update(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req filterRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var phrase string
	if req.Phrase != nil {
		phrase = strings.TrimSpace(*req.Phrase)
		if phrase == "" {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Phrase is required")
			return
		}
	}

	var contexts []string
	if req.Context != nil {
		contexts = normalizeFilterContexts(req.Context)
		if !validateFilterContexts(contexts) {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Context is invalid")
			return
		}
	}

	if h.useDB() {
		ctx := r.Context()
		keyword, err := h.queries.GetFilterKeyword(ctx, db.GetFilterKeywordParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		filter, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        keyword.CustomFilterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		action := filter.Action
		if req.Irreversible != nil {
			if *req.Irreversible {
				action = filterActionHide
			} else {
				action = filterActionWarn
			}
		}

		expiresAt := filter.ExpiresAt
		if req.ExpiresIn != nil {
			expiresAt = computeExpiresAt(req.ExpiresIn)
		}

		updatedPhrase := filter.Phrase
		if req.Phrase != nil {
			updatedPhrase = phrase
		}

		updatedContext := filter.Context
		if req.Context != nil {
			updatedContext = contexts
		}

		customFilterChanged := false
		if updatedPhrase != filter.Phrase {
			customFilterChanged = true
		}
		if req.Context != nil && !equalStringSlices(updatedContext, filter.Context) {
			customFilterChanged = true
		}
		if req.Irreversible != nil && action != filter.Action {
			customFilterChanged = true
		}
		if req.ExpiresIn != nil && !sameExpiresAt(expiresAt, filter.ExpiresAt) {
			customFilterChanged = true
		}

		if customFilterChanged {
			keywords, err := h.queries.ListFilterKeywords(ctx, filter.ID)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to update filter")
				return
			}
			if len(keywords) > 1 {
				web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Filter has multiple keywords")
				return
			}
		}

		keywordUpdate := req.Phrase != nil || req.WholeWord != nil
		filterUpdate := req.Phrase != nil || req.Context != nil || req.Irreversible != nil || req.ExpiresIn != nil

		if !keywordUpdate && !filterUpdate {
			web.JSON(w, http.StatusOK, filterV1FromDB(filter, keyword))
			return
		}

		if h.pool == nil {
			updatedKeyword := keyword
			if keywordUpdate {
				params := db.UpdateFilterKeywordParams{
					ID:        keyword.ID,
					Keyword:   pgtype.Text{String: phrase, Valid: req.Phrase != nil},
					WholeWord: pgtype.Bool{Bool: req.WholeWord != nil && *req.WholeWord, Valid: req.WholeWord != nil},
				}
				updatedKeyword, err = h.queries.UpdateFilterKeyword(ctx, params)
				if err != nil {
					web.JSONError(w, http.StatusInternalServerError, "Failed to update filter")
					return
				}
			}

			updatedFilter := filter
			if filterUpdate {
				params := db.UpdateFilterParams{
					ID:        filter.ID,
					AccountID: accountID,
					Context:   updatedContext,
					ExpiresAt: expiresAt,
				}
				if req.Phrase != nil {
					params.Phrase = pgtype.Text{String: updatedPhrase, Valid: true}
				}
				if req.Irreversible != nil {
					params.Action = pgtype.Int4{Int32: action, Valid: true}
				}
				if req.ExpiresIn != nil {
					params.ExpiresAt = expiresAt
				}
				updatedFilter, err = h.queries.UpdateFilter(ctx, params)
				if err != nil {
					web.JSONError(w, http.StatusInternalServerError, "Failed to update filter")
					return
				}
			}

			web.JSON(w, http.StatusOK, filterV1FromDB(updatedFilter, updatedKeyword))
			h.publishFiltersChanged(ctx, accountID)
			return
		}

		tx, err := h.pool.Begin(ctx)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update filter")
			return
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()

		qtx := h.queries.WithTx(tx)
		updatedKeyword := keyword
		if keywordUpdate {
			params := db.UpdateFilterKeywordParams{
				ID:        keyword.ID,
				Keyword:   pgtype.Text{String: phrase, Valid: req.Phrase != nil},
				WholeWord: pgtype.Bool{Bool: req.WholeWord != nil && *req.WholeWord, Valid: req.WholeWord != nil},
			}
			updatedKeyword, err = qtx.UpdateFilterKeyword(ctx, params)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to update filter")
				return
			}
		}

		updatedFilter := filter
		if filterUpdate {
			params := db.UpdateFilterParams{
				ID:        filter.ID,
				AccountID: accountID,
				Context:   updatedContext,
				ExpiresAt: expiresAt,
			}
			if req.Phrase != nil {
				params.Phrase = pgtype.Text{String: updatedPhrase, Valid: true}
			}
			if req.Irreversible != nil {
				params.Action = pgtype.Int4{Int32: action, Valid: true}
			}
			if req.ExpiresIn != nil {
				params.ExpiresAt = expiresAt
			}
			updatedFilter, err = qtx.UpdateFilter(ctx, params)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to update filter")
				return
			}
		}

		if err := tx.Commit(ctx); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update filter")
			return
		}

		web.JSON(w, http.StatusOK, filterV1FromDB(updatedFilter, updatedKeyword))
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	filter := accountFilters[filterID]
	if req.Phrase != nil {
		filter.Phrase = phrase
	}
	if req.Context != nil {
		filter.Context = contexts
	}
	if req.WholeWord != nil {
		filter.WholeWord = *req.WholeWord
	}
	if req.Irreversible != nil {
		filter.Irreversible = *req.Irreversible
	}
	if req.ExpiresIn != nil {
		expiresAt := computeExpiresAt(req.ExpiresIn)
		filter.ExpiresAt = expiresAtPtr(expiresAt)
	}

	web.JSON(w, http.StatusOK, filter)
	h.publishFiltersChanged(r.Context(), accountID)
}

// Delete handles DELETE /api/v1/filters/:id
func (h *FiltersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		ctx := r.Context()
		keyword, err := h.queries.GetFilterKeyword(ctx, db.GetFilterKeywordParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		if err := h.queries.DeleteFilterKeyword(ctx, keyword.ID); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to delete filter")
			return
		}
		web.JSON(w, http.StatusOK, map[string]any{})
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	delete(accountFilters, filterID)
	web.JSON(w, http.StatusOK, map[string]any{})
	h.publishFiltersChanged(r.Context(), accountID)
}

// RegisterRoutes registers filter routes.
func (h *FiltersHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/filters", h.List)
	mux.HandleFunc("POST /api/v1/filters", h.Create)
	mux.HandleFunc("GET /api/v1/filters/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/filters/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/filters/{id}", h.Delete)
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (h *FiltersHandler) publishFiltersChanged(ctx context.Context, accountID int64) {
	if h == nil || h.publisher == nil || accountID <= 0 {
		return
	}
	_ = h.publisher.PublishEventToRedis(ctx, streaming.EventFiltersChanged, "", []string{streaming.UserStreamKey(accountID)})
}

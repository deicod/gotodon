// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// Filter action constants
const (
	FilterActionWarn = 0
	FilterActionHide = 1
)

var filterActionToString = map[int32]string{
	FilterActionWarn: "warn",
	FilterActionHide: "hide",
}

var stringToFilterAction = map[string]int32{
	"warn": FilterActionWarn,
	"hide": FilterActionHide,
}

// FiltersHandler handles filter endpoints
type FiltersHandler struct {
	queries   *db.Queries
	idGen     *snowflake.Generator
	publisher streaming.EventPublisher
	// In-memory storage for fallback when queries is nil
	memFilters map[int64]map[int64]*FilterV2 // account_id -> filter_id -> filter
}

// NewFiltersHandler creates a new filters handler
func NewFiltersHandler(queries *db.Queries) *FiltersHandler {
	return &FiltersHandler{
		queries:    queries,
		idGen:      snowflake.NewGenerator(),
		memFilters: make(map[int64]map[int64]*FilterV2),
	}
}

// SetPublisher sets the streaming publisher for filter change events.
func (h *FiltersHandler) SetPublisher(publisher streaming.EventPublisher) {
	h.publisher = publisher
}

func (h *FiltersHandler) useDB() bool {
	return h.queries != nil
}

// FilterV2 represents a v2 filter
type FilterV2 struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Context      []string        `json:"context"`
	ExpiresAt    *time.Time      `json:"expires_at"`
	FilterAction string          `json:"filter_action"`
	Keywords     []FilterKeyword `json:"keywords"`
	Statuses     []FilterStatus  `json:"statuses"`
}

// FilterKeyword represents a filter keyword
type FilterKeyword struct {
	ID        string `json:"id"`
	Keyword   string `json:"keyword"`
	WholeWord bool   `json:"whole_word"`
}

// FilterStatus represents a filtered status
type FilterStatus struct {
	ID       string `json:"id"`
	StatusID string `json:"status_id"`
}

type KeywordUpdateRequest struct {
	Keyword   *string `json:"keyword" form:"keyword"`
	WholeWord *bool   `json:"whole_word" form:"whole_word"`
}

type CreateFilterStatusRequest struct {
	StatusID string `json:"status_id" form:"status_id"`
}

// CreateFilterRequest is the request for POST /api/v2/filters
type CreateFilterRequest struct {
	Title              string        `json:"title" form:"title"`
	Context            []string      `json:"context" form:"context[]"`
	FilterAction       string        `json:"filter_action" form:"filter_action"`
	ExpiresIn          *int          `json:"expires_in" form:"expires_in"`
	KeywordsAttributes []KeywordAttr `json:"keywords_attributes" form:"keywords_attributes[]"`
}

// KeywordAttr is a keyword attribute in filter creation
type KeywordAttr struct {
	Keyword   string `json:"keyword" form:"keyword"`
	WholeWord *bool  `json:"whole_word" form:"whole_word"`
}

// UpdateFilterRequest is the request for PUT /api/v2/filters/:id
type UpdateFilterRequest struct {
	Title              *string             `json:"title" form:"title"`
	Context            []string            `json:"context" form:"context[]"`
	FilterAction       *string             `json:"filter_action" form:"filter_action"`
	ExpiresIn          *int                `json:"expires_in" form:"expires_in"`
	KeywordsAttributes []KeywordUpdateAttr `json:"keywords_attributes" form:"keywords_attributes[]"`
}

// KeywordUpdateAttr is a keyword attribute for updates
type KeywordUpdateAttr struct {
	ID        *string `json:"id" form:"id"`
	Keyword   *string `json:"keyword" form:"keyword"`
	WholeWord *bool   `json:"whole_word" form:"whole_word"`
	Destroy   *bool   `json:"_destroy" form:"_destroy"`
}

// Valid filter contexts
var validContexts = map[string]bool{
	"home":          true,
	"notifications": true,
	"public":        true,
	"thread":        true,
	"account":       true,
}

// Valid filter actions
var validActions = map[string]bool{
	"warn": true,
	"hide": true,
}

func (h *FiltersHandler) dbFilterToAPI(dbFilter db.CustomFilter) FilterV2 {
	action := filterActionToString[dbFilter.Action]
	if action == "" {
		action = "warn"
	}
	var expiresAt *time.Time
	if dbFilter.ExpiresAt.Valid {
		expiresAt = &dbFilter.ExpiresAt.Time
	}
	return FilterV2{
		ID:           strconv.FormatInt(dbFilter.ID, 10),
		Title:        dbFilter.Phrase,
		Context:      dbFilter.Context,
		ExpiresAt:    expiresAt,
		FilterAction: action,
		Keywords:     []FilterKeyword{},
		Statuses:     []FilterStatus{},
	}
}

func (h *FiltersHandler) loadFilterKeywords(ctx context.Context, filterID int64) []FilterKeyword {
	if !h.useDB() {
		return []FilterKeyword{}
	}
	dbKeywords, err := h.queries.ListFilterKeywords(ctx, filterID)
	if err != nil {
		return []FilterKeyword{}
	}
	keywords := make([]FilterKeyword, len(dbKeywords))
	for i, kw := range dbKeywords {
		keywords[i] = FilterKeyword{
			ID:        strconv.FormatInt(kw.ID, 10),
			Keyword:   kw.Keyword,
			WholeWord: kw.WholeWord,
		}
	}
	return keywords
}

func (h *FiltersHandler) loadFilterStatuses(ctx context.Context, filterID int64) []FilterStatus {
	if !h.useDB() {
		return []FilterStatus{}
	}
	dbStatuses, err := h.queries.ListFilterStatuses(ctx, filterID)
	if err != nil {
		return []FilterStatus{}
	}
	statuses := make([]FilterStatus, len(dbStatuses))
	for i, s := range dbStatuses {
		statuses[i] = FilterStatus{
			ID:       strconv.FormatInt(s.ID, 10),
			StatusID: strconv.FormatInt(s.StatusID, 10),
		}
	}
	return statuses
}

func (h *FiltersHandler) findMemKeyword(accountID int64, keywordID string) (*FilterV2, int) {
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		return nil, -1
	}
	for _, filter := range accountFilters {
		for i := range filter.Keywords {
			if filter.Keywords[i].ID == keywordID {
				return filter, i
			}
		}
	}
	return nil, -1
}

func (h *FiltersHandler) findMemStatus(accountID int64, statusID string) (*FilterV2, int) {
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		return nil, -1
	}
	for _, filter := range accountFilters {
		for i := range filter.Statuses {
			if filter.Statuses[i].ID == statusID {
				return filter, i
			}
		}
	}
	return nil, -1
}

// List handles GET /api/v2/filters
func (h *FiltersHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		dbFilters, err := h.queries.ListFilters(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		result := make([]FilterV2, 0, len(dbFilters))
		for _, dbFilter := range dbFilters {
			// Skip expired filters
			if dbFilter.ExpiresAt.Valid && dbFilter.ExpiresAt.Time.Before(time.Now()) {
				continue
			}
			filter := h.dbFilterToAPI(dbFilter)
			filter.Keywords = h.loadFilterKeywords(ctx, dbFilter.ID)
			filter.Statuses = h.loadFilterStatuses(ctx, dbFilter.ID)
			result = append(result, filter)
		}
		web.JSON(w, http.StatusOK, result)
		return
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		web.JSON(w, http.StatusOK, []FilterV2{})
		return
	}
	result := make([]FilterV2, 0, len(accountFilters))
	for _, filter := range accountFilters {
		if filter.ExpiresAt != nil && filter.ExpiresAt.Before(time.Now()) {
			continue
		}
		result = append(result, *filter)
	}
	web.JSON(w, http.StatusOK, result)
}

// Get handles GET /api/v2/filters/:id
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

	ctx := context.Background()

	if h.useDB() {
		dbFilter, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		filter := h.dbFilterToAPI(dbFilter)
		filter.Keywords = h.loadFilterKeywords(ctx, filterID)
		filter.Statuses = h.loadFilterStatuses(ctx, filterID)
		web.JSON(w, http.StatusOK, filter)
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, accountFilters[filterID])
}

// Create handles POST /api/v2/filters
func (h *FiltersHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	var req CreateFilterRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate
	if req.Title == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Title can't be blank")
		return
	}

	if len(req.Context) == 0 {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Context can't be blank")
		return
	}

	for _, ctx := range req.Context {
		if !validContexts[ctx] {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Context is invalid")
			return
		}
	}

	actionStr := "warn"
	actionInt := int32(FilterActionWarn)
	if req.FilterAction != "" {
		if !validActions[req.FilterAction] {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Filter action is invalid")
			return
		}
		actionStr = req.FilterAction
		actionInt = stringToFilterAction[req.FilterAction]
	}

	// Calculate expiration
	var expiresAt *time.Time
	var expiresAtPG pgtype.Timestamptz
	if req.ExpiresIn != nil && *req.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = &exp
		expiresAtPG = pgtype.Timestamptz{Time: exp, Valid: true}
	}

	ctx := context.Background()

	if h.useDB() {
		dbFilter, err := h.queries.CreateFilter(ctx, db.CreateFilterParams{
			AccountID: accountID,
			Phrase:    req.Title,
			Context:   req.Context,
			Action:    actionInt,
			ExpiresAt: expiresAtPG,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create filter")
			return
		}

		// Create keywords
		keywords := make([]FilterKeyword, 0, len(req.KeywordsAttributes))
		for _, kw := range req.KeywordsAttributes {
			wholeWord := true
			if kw.WholeWord != nil {
				wholeWord = *kw.WholeWord
			}
			dbKw, err := h.queries.CreateFilterKeyword(ctx, db.CreateFilterKeywordParams{
				CustomFilterID: dbFilter.ID,
				Keyword:        kw.Keyword,
				WholeWord:      wholeWord,
			})
			if err == nil {
				keywords = append(keywords, FilterKeyword{
					ID:        strconv.FormatInt(dbKw.ID, 10),
					Keyword:   dbKw.Keyword,
					WholeWord: dbKw.WholeWord,
				})
			}
		}

		filter := h.dbFilterToAPI(dbFilter)
		filter.Keywords = keywords
		filter.Statuses = []FilterStatus{}
		web.JSON(w, http.StatusOK, filter)
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	// In-memory fallback
	filterID := h.idGen.Generate()
	keywords := make([]FilterKeyword, 0, len(req.KeywordsAttributes))
	for _, kw := range req.KeywordsAttributes {
		wholeWord := true
		if kw.WholeWord != nil {
			wholeWord = *kw.WholeWord
		}
		keywords = append(keywords, FilterKeyword{
			ID:        strconv.FormatInt(h.idGen.Generate(), 10),
			Keyword:   kw.Keyword,
			WholeWord: wholeWord,
		})
	}

	filter := &FilterV2{
		ID:           strconv.FormatInt(filterID, 10),
		Title:        req.Title,
		Context:      req.Context,
		ExpiresAt:    expiresAt,
		FilterAction: actionStr,
		Keywords:     keywords,
		Statuses:     []FilterStatus{},
	}

	if h.memFilters[accountID] == nil {
		h.memFilters[accountID] = make(map[int64]*FilterV2)
	}
	h.memFilters[accountID][filterID] = filter

	web.JSON(w, http.StatusOK, filter)
	h.publishFiltersChanged(r.Context(), accountID)
}

// Update handles PUT /api/v2/filters/:id
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

	var req UpdateFilterRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		// Verify exists
		existing, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Build update params
		params := db.UpdateFilterParams{
			ID:        filterID,
			AccountID: accountID,
			Context:   existing.Context,
			ExpiresAt: existing.ExpiresAt,
		}

		if req.Title != nil {
			params.Phrase = pgtype.Text{String: *req.Title, Valid: true}
		}

		if len(req.Context) > 0 {
			for _, ctx := range req.Context {
				if !validContexts[ctx] {
					web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Context is invalid")
					return
				}
			}
			params.Context = req.Context
		}

		if req.FilterAction != nil {
			if !validActions[*req.FilterAction] {
				web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Filter action is invalid")
				return
			}
			params.Action = pgtype.Int4{Int32: stringToFilterAction[*req.FilterAction], Valid: true}
		}

		if req.ExpiresIn != nil {
			if *req.ExpiresIn > 0 {
				exp := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
				params.ExpiresAt = pgtype.Timestamptz{Time: exp, Valid: true}
			} else {
				params.ExpiresAt = pgtype.Timestamptz{Valid: false}
			}
		}

		dbFilter, err := h.queries.UpdateFilter(ctx, params)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update filter")
			return
		}

		// Handle keyword updates
		for _, kw := range req.KeywordsAttributes {
			if kw.ID != nil {
				kwID, err := strconv.ParseInt(*kw.ID, 10, 64)
				if err != nil {
					web.JSONError(w, http.StatusNotFound, "Record not found")
					return
				}
				dbKw, err := h.queries.GetFilterKeyword(ctx, db.GetFilterKeywordParams{
					ID:        kwID,
					AccountID: accountID,
				})
				if err != nil || dbKw.CustomFilterID != filterID {
					web.JSONError(w, http.StatusNotFound, "Record not found")
					return
				}
				if kw.Destroy != nil && *kw.Destroy {
					_ = h.queries.DeleteFilterKeyword(ctx, kwID)
				} else {
					updateParams := db.UpdateFilterKeywordParams{ID: kwID}
					if kw.Keyword != nil {
						updateParams.Keyword = pgtype.Text{String: *kw.Keyword, Valid: true}
					}
					if kw.WholeWord != nil {
						updateParams.WholeWord = pgtype.Bool{Bool: *kw.WholeWord, Valid: true}
					}
					_, _ = h.queries.UpdateFilterKeyword(ctx, updateParams)
				}
			} else if kw.Keyword != nil {
				wholeWord := true
				if kw.WholeWord != nil {
					wholeWord = *kw.WholeWord
				}
				_, _ = h.queries.CreateFilterKeyword(ctx, db.CreateFilterKeywordParams{
					CustomFilterID: filterID,
					Keyword:        *kw.Keyword,
					WholeWord:      wholeWord,
				})
			}
		}

		filter := h.dbFilterToAPI(dbFilter)
		filter.Keywords = h.loadFilterKeywords(ctx, filterID)
		filter.Statuses = h.loadFilterStatuses(ctx, filterID)
		web.JSON(w, http.StatusOK, filter)
		return
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	filter := accountFilters[filterID]
	if req.Title != nil {
		filter.Title = *req.Title
	}
	if len(req.Context) > 0 {
		for _, ctx := range req.Context {
			if !validContexts[ctx] {
				web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Context is invalid")
				return
			}
		}
		filter.Context = req.Context
	}
	if req.FilterAction != nil {
		if !validActions[*req.FilterAction] {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Filter action is invalid")
			return
		}
		filter.FilterAction = *req.FilterAction
	}
	if req.ExpiresIn != nil {
		if *req.ExpiresIn > 0 {
			exp := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
			filter.ExpiresAt = &exp
		} else {
			filter.ExpiresAt = nil
		}
	}

	// Handle keyword updates (in-memory)
	if len(req.KeywordsAttributes) > 0 {
		keywordMap := make(map[string]*FilterKeyword)
		for i := range filter.Keywords {
			keywordMap[filter.Keywords[i].ID] = &filter.Keywords[i]
		}
		for _, kw := range req.KeywordsAttributes {
			if kw.ID != nil {
				if existing, ok := keywordMap[*kw.ID]; ok {
					if kw.Destroy != nil && *kw.Destroy {
						delete(keywordMap, *kw.ID)
					} else {
						if kw.Keyword != nil {
							existing.Keyword = *kw.Keyword
						}
						if kw.WholeWord != nil {
							existing.WholeWord = *kw.WholeWord
						}
					}
				}
			} else if kw.Keyword != nil {
				wholeWord := true
				if kw.WholeWord != nil {
					wholeWord = *kw.WholeWord
				}
				newKw := FilterKeyword{
					ID:        strconv.FormatInt(h.idGen.Generate(), 10),
					Keyword:   *kw.Keyword,
					WholeWord: wholeWord,
				}
				keywordMap[newKw.ID] = &newKw
			}
		}
		filter.Keywords = make([]FilterKeyword, 0, len(keywordMap))
		for _, kw := range keywordMap {
			filter.Keywords = append(filter.Keywords, *kw)
		}
	}

	web.JSON(w, http.StatusOK, filter)
	h.publishFiltersChanged(r.Context(), accountID)
}

// Delete handles DELETE /api/v2/filters/:id
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

	ctx := context.Background()

	if h.useDB() {
		if err := h.queries.DeleteFilter(ctx, db.DeleteFilterParams{
			ID:        filterID,
			AccountID: accountID,
		}); err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, map[string]any{})
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	delete(accountFilters, filterID)
	web.JSON(w, http.StatusOK, map[string]any{})
	h.publishFiltersChanged(r.Context(), accountID)
}

// ListKeywords handles GET /api/v2/filters/:filter_id/keywords
func (h *FiltersHandler) ListKeywords(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, err := strconv.ParseInt(web.Param(r, "filter_id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		_, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		dbKeywords, err := h.queries.ListFilterKeywords(ctx, filterID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		keywords := make([]FilterKeyword, len(dbKeywords))
		for i, kw := range dbKeywords {
			keywords[i] = FilterKeyword{
				ID:        strconv.FormatInt(kw.ID, 10),
				Keyword:   kw.Keyword,
				WholeWord: kw.WholeWord,
			}
		}
		web.JSON(w, http.StatusOK, keywords)
		return
	}

	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, accountFilters[filterID].Keywords)
}

// GetKeyword handles GET /api/v2/filters/:filter_id/keywords/:id
func (h *FiltersHandler) GetKeyword(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, _ := strconv.ParseInt(web.Param(r, "filter_id"), 10, 64)
	keywordIDStr := web.Param(r, "id")

	ctx := context.Background()

	if h.useDB() {
		// Verify filter ownership
		_, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		keywords := h.loadFilterKeywords(ctx, filterID)
		for _, kw := range keywords {
			if kw.ID == keywordIDStr {
				web.JSON(w, http.StatusOK, kw)
				return
			}
		}
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	filter := accountFilters[filterID]
	if filter == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	for _, kw := range filter.Keywords {
		if kw.ID == keywordIDStr {
			web.JSON(w, http.StatusOK, kw)
			return
		}
	}
	web.JSONError(w, http.StatusNotFound, "Record not found")
}

// GetKeywordByID handles GET /api/v2/filters/keywords/:id
func (h *FiltersHandler) GetKeywordByID(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	keywordIDStr := web.Param(r, "id")
	keywordID, err := strconv.ParseInt(keywordIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		dbKw, err := h.queries.GetFilterKeyword(ctx, db.GetFilterKeywordParams{
			ID:        keywordID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, FilterKeyword{
			ID:        strconv.FormatInt(dbKw.ID, 10),
			Keyword:   dbKw.Keyword,
			WholeWord: dbKw.WholeWord,
		})
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	filter, idx := h.findMemKeyword(accountID, keywordIDStr)
	if filter == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, filter.Keywords[idx])
	h.publishFiltersChanged(r.Context(), accountID)
}

// UpdateKeywordByID handles PUT /api/v2/filters/keywords/:id
func (h *FiltersHandler) UpdateKeywordByID(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	keywordIDStr := web.Param(r, "id")
	keywordID, err := strconv.ParseInt(keywordIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req KeywordUpdateRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	if req.Keyword == nil && req.WholeWord == nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Keyword can't be blank")
		return
	}
	if req.Keyword != nil && *req.Keyword == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Keyword can't be blank")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		_, err := h.queries.GetFilterKeyword(ctx, db.GetFilterKeywordParams{
			ID:        keywordID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		updateParams := db.UpdateFilterKeywordParams{ID: keywordID}
		if req.Keyword != nil {
			updateParams.Keyword = pgtype.Text{String: *req.Keyword, Valid: true}
		}
		if req.WholeWord != nil {
			updateParams.WholeWord = pgtype.Bool{Bool: *req.WholeWord, Valid: true}
		}
		dbKw, err := h.queries.UpdateFilterKeyword(ctx, updateParams)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update keyword")
			return
		}

		web.JSON(w, http.StatusOK, FilterKeyword{
			ID:        strconv.FormatInt(dbKw.ID, 10),
			Keyword:   dbKw.Keyword,
			WholeWord: dbKw.WholeWord,
		})
		return
	}

	filter, idx := h.findMemKeyword(accountID, keywordIDStr)
	if filter == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if req.Keyword != nil {
		filter.Keywords[idx].Keyword = *req.Keyword
	}
	if req.WholeWord != nil {
		filter.Keywords[idx].WholeWord = *req.WholeWord
	}

	web.JSON(w, http.StatusOK, filter.Keywords[idx])
}

// DeleteKeywordByID handles DELETE /api/v2/filters/keywords/:id
func (h *FiltersHandler) DeleteKeywordByID(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	keywordIDStr := web.Param(r, "id")
	keywordID, err := strconv.ParseInt(keywordIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		_, err := h.queries.GetFilterKeyword(ctx, db.GetFilterKeywordParams{
			ID:        keywordID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		if err := h.queries.DeleteFilterKeyword(ctx, keywordID); err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, map[string]any{})
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	filter, idx := h.findMemKeyword(accountID, keywordIDStr)
	if filter == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	filter.Keywords = append(filter.Keywords[:idx], filter.Keywords[idx+1:]...)
	web.JSON(w, http.StatusOK, map[string]any{})
	h.publishFiltersChanged(r.Context(), accountID)
}

// CreateKeyword handles POST /api/v2/filters/:filter_id/keywords
func (h *FiltersHandler) CreateKeyword(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, _ := strconv.ParseInt(web.Param(r, "filter_id"), 10, 64)

	var req struct {
		Keyword   string `json:"keyword" form:"keyword"`
		WholeWord *bool  `json:"whole_word" form:"whole_word"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	if req.Keyword == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Keyword can't be blank")
		return
	}

	wholeWord := true
	if req.WholeWord != nil {
		wholeWord = *req.WholeWord
	}

	ctx := context.Background()

	if h.useDB() {
		// Verify filter ownership
		_, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		dbKw, err := h.queries.CreateFilterKeyword(ctx, db.CreateFilterKeywordParams{
			CustomFilterID: filterID,
			Keyword:        req.Keyword,
			WholeWord:      wholeWord,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create keyword")
			return
		}
		web.JSON(w, http.StatusOK, FilterKeyword{
			ID:        strconv.FormatInt(dbKw.ID, 10),
			Keyword:   dbKw.Keyword,
			WholeWord: dbKw.WholeWord,
		})
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	filter := accountFilters[filterID]
	if filter == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	kw := FilterKeyword{
		ID:        strconv.FormatInt(h.idGen.Generate(), 10),
		Keyword:   req.Keyword,
		WholeWord: wholeWord,
	}
	filter.Keywords = append(filter.Keywords, kw)
	web.JSON(w, http.StatusOK, kw)
	h.publishFiltersChanged(r.Context(), accountID)
}

// DeleteKeyword handles DELETE /api/v2/filters/:filter_id/keywords/:id
func (h *FiltersHandler) DeleteKeyword(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, err := strconv.ParseInt(web.Param(r, "filter_id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	keywordIDStr := web.Param(r, "id")
	keywordID, err := strconv.ParseInt(keywordIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		dbKw, err := h.queries.GetFilterKeyword(ctx, db.GetFilterKeywordParams{
			ID:        keywordID,
			AccountID: accountID,
		})
		if err != nil || dbKw.CustomFilterID != filterID {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		if err := h.queries.DeleteFilterKeyword(ctx, keywordID); err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, map[string]any{})
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	filter := accountFilters[filterID]
	if filter == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	// Find and remove
	for i, kw := range filter.Keywords {
		if kw.ID == keywordIDStr {
			filter.Keywords = append(filter.Keywords[:i], filter.Keywords[i+1:]...)
			web.JSON(w, http.StatusOK, map[string]any{})
			h.publishFiltersChanged(r.Context(), accountID)
			return
		}
	}
	web.JSONError(w, http.StatusNotFound, "Record not found")
}

// ListStatuses handles GET /api/v2/filters/:filter_id/statuses
func (h *FiltersHandler) ListStatuses(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, err := strconv.ParseInt(web.Param(r, "filter_id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		_, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		dbStatuses, err := h.queries.ListFilterStatuses(ctx, filterID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		statuses := make([]FilterStatus, len(dbStatuses))
		for i, s := range dbStatuses {
			statuses[i] = FilterStatus{
				ID:       strconv.FormatInt(s.ID, 10),
				StatusID: strconv.FormatInt(s.StatusID, 10),
			}
		}
		web.JSON(w, http.StatusOK, statuses)
		return
	}

	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, accountFilters[filterID].Statuses)
}

// CreateStatus handles POST /api/v2/filters/:filter_id/statuses
func (h *FiltersHandler) CreateStatus(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	filterID, err := strconv.ParseInt(web.Param(r, "filter_id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req CreateFilterStatusRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	if req.StatusID == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Status ID can't be blank")
		return
	}
	statusID, err := strconv.ParseInt(req.StatusID, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Status ID can't be blank")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		_, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		dbStatus, err := h.queries.CreateFilterStatus(ctx, db.CreateFilterStatusParams{
			CustomFilterID: filterID,
			StatusID:       statusID,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create status")
			return
		}
		web.JSON(w, http.StatusOK, FilterStatus{
			ID:       strconv.FormatInt(dbStatus.ID, 10),
			StatusID: strconv.FormatInt(dbStatus.StatusID, 10),
		})
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	filter := accountFilters[filterID]
	status := FilterStatus{
		ID:       strconv.FormatInt(h.idGen.Generate(), 10),
		StatusID: req.StatusID,
	}
	filter.Statuses = append(filter.Statuses, status)
	web.JSON(w, http.StatusOK, status)
	h.publishFiltersChanged(r.Context(), accountID)
}

// GetStatus handles GET /api/v2/filters/statuses/:id
func (h *FiltersHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	statusIDStr := web.Param(r, "id")
	statusID, err := strconv.ParseInt(statusIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		dbStatus, err := h.queries.GetFilterStatus(ctx, db.GetFilterStatusParams{
			ID:        statusID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, FilterStatus{
			ID:       strconv.FormatInt(dbStatus.ID, 10),
			StatusID: strconv.FormatInt(dbStatus.StatusID, 10),
		})
		return
	}

	filter, idx := h.findMemStatus(accountID, statusIDStr)
	if filter == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, filter.Statuses[idx])
}

// DeleteStatus handles DELETE /api/v2/filters/statuses/:id
func (h *FiltersHandler) DeleteStatus(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	statusIDStr := web.Param(r, "id")
	statusID, err := strconv.ParseInt(statusIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := context.Background()

	if h.useDB() {
		_, err := h.queries.GetFilterStatus(ctx, db.GetFilterStatusParams{
			ID:        statusID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		if err := h.queries.DeleteFilterStatus(ctx, statusID); err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, map[string]any{})
		h.publishFiltersChanged(ctx, accountID)
		return
	}

	filter, idx := h.findMemStatus(accountID, statusIDStr)
	if filter == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	filter.Statuses = append(filter.Statuses[:idx], filter.Statuses[idx+1:]...)
	web.JSON(w, http.StatusOK, map[string]any{})
	h.publishFiltersChanged(r.Context(), accountID)
}

// RegisterRoutes registers filter routes
func (h *FiltersHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/filters", h.List)
	mux.HandleFunc("POST /api/v2/filters", h.Create)
	mux.HandleFunc("GET /api/v2/filters/", h.dispatchGet)
	mux.HandleFunc("POST /api/v2/filters/", h.dispatchPost)
	mux.HandleFunc("PUT /api/v2/filters/", h.dispatchPut)
	mux.HandleFunc("DELETE /api/v2/filters/", h.dispatchDelete)
}

func (h *FiltersHandler) publishFiltersChanged(ctx context.Context, accountID int64) {
	if h == nil || h.publisher == nil || accountID <= 0 {
		return
	}
	_ = h.publisher.PublishEventToRedis(ctx, streaming.EventFiltersChanged, "", []string{streaming.UserStreamKey(accountID)})
}

func (h *FiltersHandler) dispatchGet(w http.ResponseWriter, r *http.Request) {
	segments := filterSubpathSegments(r.URL.Path)
	if segments == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if len(segments) == 0 {
		h.List(w, r)
		return
	}

	switch segments[0] {
	case "keywords":
		if len(segments) == 2 {
			r.SetPathValue("id", segments[1])
			h.GetKeywordByID(w, r)
			return
		}
	case "statuses":
		if len(segments) == 2 {
			r.SetPathValue("id", segments[1])
			h.GetStatus(w, r)
			return
		}
	default:
		if len(segments) == 1 {
			r.SetPathValue("id", segments[0])
			h.Get(w, r)
			return
		}
		if len(segments) == 2 {
			if segments[1] == "keywords" {
				r.SetPathValue("filter_id", segments[0])
				h.ListKeywords(w, r)
				return
			}
			if segments[1] == "statuses" {
				r.SetPathValue("filter_id", segments[0])
				h.ListStatuses(w, r)
				return
			}
		}
		if len(segments) == 3 && segments[1] == "keywords" {
			r.SetPathValue("filter_id", segments[0])
			r.SetPathValue("id", segments[2])
			h.GetKeyword(w, r)
			return
		}
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

func (h *FiltersHandler) dispatchPost(w http.ResponseWriter, r *http.Request) {
	segments := filterSubpathSegments(r.URL.Path)
	if segments == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if len(segments) == 0 {
		h.Create(w, r)
		return
	}
	if len(segments) == 2 && segments[1] == "keywords" {
		r.SetPathValue("filter_id", segments[0])
		h.CreateKeyword(w, r)
		return
	}
	if len(segments) == 2 && segments[1] == "statuses" {
		r.SetPathValue("filter_id", segments[0])
		h.CreateStatus(w, r)
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

func (h *FiltersHandler) dispatchPut(w http.ResponseWriter, r *http.Request) {
	segments := filterSubpathSegments(r.URL.Path)
	if segments == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if len(segments) == 1 {
		r.SetPathValue("id", segments[0])
		h.Update(w, r)
		return
	}
	if len(segments) == 2 && segments[0] == "keywords" {
		r.SetPathValue("id", segments[1])
		h.UpdateKeywordByID(w, r)
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

func (h *FiltersHandler) dispatchDelete(w http.ResponseWriter, r *http.Request) {
	segments := filterSubpathSegments(r.URL.Path)
	if segments == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if len(segments) == 1 {
		r.SetPathValue("id", segments[0])
		h.Delete(w, r)
		return
	}
	if len(segments) == 2 && segments[0] == "keywords" {
		r.SetPathValue("id", segments[1])
		h.DeleteKeywordByID(w, r)
		return
	}
	if len(segments) == 2 && segments[0] == "statuses" {
		r.SetPathValue("id", segments[1])
		h.DeleteStatus(w, r)
		return
	}
	if len(segments) == 3 && segments[1] == "keywords" {
		r.SetPathValue("filter_id", segments[0])
		r.SetPathValue("id", segments[2])
		h.DeleteKeyword(w, r)
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

func filterSubpathSegments(fullPath string) []string {
	const prefix = "/api/v2/filters/"
	if fullPath == "/api/v2/filters" {
		return []string{}
	}
	if !strings.HasPrefix(fullPath, prefix) {
		return nil
	}
	rest := strings.TrimPrefix(fullPath, prefix)
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return []string{}
	}
	return strings.Split(rest, "/")
}

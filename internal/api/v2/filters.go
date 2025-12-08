// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
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
	queries *db.Queries
	idGen   *snowflake.Generator
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

func (h *FiltersHandler) dbFilterToAPI(dbFilter db.Filter) FilterV2 {
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
		Title:        dbFilter.Title,
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

// List handles GET /api/v2/filters
func (h *FiltersHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		dbFilters, err := h.queries.ListFilters(ctx, accountID)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": "Database error",
			})
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
		return c.JSON(http.StatusOK, result)
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		return c.JSON(http.StatusOK, []FilterV2{})
	}
	result := make([]FilterV2, 0, len(accountFilters))
	for _, filter := range accountFilters {
		if filter.ExpiresAt != nil && filter.ExpiresAt.Before(time.Now()) {
			continue
		}
		result = append(result, *filter)
	}
	return c.JSON(http.StatusOK, result)
}

// Get handles GET /api/v2/filters/:id
func (h *FiltersHandler) Get(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	filterID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		dbFilter, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		filter := h.dbFilterToAPI(dbFilter)
		filter.Keywords = h.loadFilterKeywords(ctx, filterID)
		filter.Statuses = h.loadFilterStatuses(ctx, filterID)
		return c.JSON(http.StatusOK, filter)
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}
	return c.JSON(http.StatusOK, accountFilters[filterID])
}

// Create handles POST /api/v2/filters
func (h *FiltersHandler) Create(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	var req CreateFilterRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Validate
	if req.Title == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Title can't be blank",
		})
	}

	if len(req.Context) == 0 {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Context can't be blank",
		})
	}

	for _, ctx := range req.Context {
		if !validContexts[ctx] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Context is invalid",
			})
		}
	}

	actionStr := "warn"
	actionInt := int32(FilterActionWarn)
	if req.FilterAction != "" {
		if !validActions[req.FilterAction] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Filter action is invalid",
			})
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
			Title:     req.Title,
			Context:   req.Context,
			Action:    actionInt,
			ExpiresAt: expiresAtPG,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": "Failed to create filter",
			})
		}

		// Create keywords
		keywords := make([]FilterKeyword, 0, len(req.KeywordsAttributes))
		for _, kw := range req.KeywordsAttributes {
			wholeWord := true
			if kw.WholeWord != nil {
				wholeWord = *kw.WholeWord
			}
			dbKw, err := h.queries.CreateFilterKeyword(ctx, db.CreateFilterKeywordParams{
				FilterID:  dbFilter.ID,
				Keyword:   kw.Keyword,
				WholeWord: wholeWord,
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
		return c.JSON(http.StatusOK, filter)
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

	return c.JSON(http.StatusOK, filter)
}

// Update handles PUT /api/v2/filters/:id
func (h *FiltersHandler) Update(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	filterID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	var req UpdateFilterRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		// Verify exists
		existing, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}

		// Build update params
		params := db.UpdateFilterParams{
			ID:        filterID,
			AccountID: accountID,
			Context:   existing.Context,
			ExpiresAt: existing.ExpiresAt,
		}

		if req.Title != nil {
			params.Title = pgtype.Text{String: *req.Title, Valid: true}
		}

		if len(req.Context) > 0 {
			for _, ctx := range req.Context {
				if !validContexts[ctx] {
					return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
						"error": "Validation failed: Context is invalid",
					})
				}
			}
			params.Context = req.Context
		}

		if req.FilterAction != nil {
			if !validActions[*req.FilterAction] {
				return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
					"error": "Validation failed: Filter action is invalid",
				})
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
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": "Failed to update filter",
			})
		}

		// Handle keyword updates
		for _, kw := range req.KeywordsAttributes {
			if kw.ID != nil {
				kwID, _ := strconv.ParseInt(*kw.ID, 10, 64)
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
					FilterID:  filterID,
					Keyword:   *kw.Keyword,
					WholeWord: wholeWord,
				})
			}
		}

		filter := h.dbFilterToAPI(dbFilter)
		filter.Keywords = h.loadFilterKeywords(ctx, filterID)
		filter.Statuses = h.loadFilterStatuses(ctx, filterID)
		return c.JSON(http.StatusOK, filter)
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	filter := accountFilters[filterID]
	if req.Title != nil {
		filter.Title = *req.Title
	}
	if len(req.Context) > 0 {
		for _, ctx := range req.Context {
			if !validContexts[ctx] {
				return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
					"error": "Validation failed: Context is invalid",
				})
			}
		}
		filter.Context = req.Context
	}
	if req.FilterAction != nil {
		if !validActions[*req.FilterAction] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Filter action is invalid",
			})
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

	return c.JSON(http.StatusOK, filter)
}

// Delete handles DELETE /api/v2/filters/:id
func (h *FiltersHandler) Delete(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	filterID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		if err := h.queries.DeleteFilter(ctx, db.DeleteFilterParams{
			ID:        filterID,
			AccountID: accountID,
		}); err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		return c.JSON(http.StatusOK, map[string]any{})
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil || accountFilters[filterID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}
	delete(accountFilters, filterID)
	return c.JSON(http.StatusOK, map[string]any{})
}

// GetKeyword handles GET /api/v2/filters/:filter_id/keywords/:id
func (h *FiltersHandler) GetKeyword(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	filterID, _ := strconv.ParseInt(c.Param("filter_id"), 10, 64)
	keywordIDStr := c.Param("id")

	ctx := context.Background()

	if h.useDB() {
		// Verify filter ownership
		_, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
		}

		keywords := h.loadFilterKeywords(ctx, filterID)
		for _, kw := range keywords {
			if kw.ID == keywordIDStr {
				return c.JSON(http.StatusOK, kw)
			}
		}
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}
	filter := accountFilters[filterID]
	if filter == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}
	for _, kw := range filter.Keywords {
		if kw.ID == keywordIDStr {
			return c.JSON(http.StatusOK, kw)
		}
	}
	return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
}

// CreateKeyword handles POST /api/v2/filters/:filter_id/keywords
func (h *FiltersHandler) CreateKeyword(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	filterID, _ := strconv.ParseInt(c.Param("filter_id"), 10, 64)

	var req struct {
		Keyword   string `json:"keyword" form:"keyword"`
		WholeWord *bool  `json:"whole_word" form:"whole_word"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}

	if req.Keyword == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Keyword can't be blank",
		})
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
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
		}

		dbKw, err := h.queries.CreateFilterKeyword(ctx, db.CreateFilterKeywordParams{
			FilterID:  filterID,
			Keyword:   req.Keyword,
			WholeWord: wholeWord,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{"error": "Failed to create keyword"})
		}
		return c.JSON(http.StatusOK, FilterKeyword{
			ID:        strconv.FormatInt(dbKw.ID, 10),
			Keyword:   dbKw.Keyword,
			WholeWord: dbKw.WholeWord,
		})
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}
	filter := accountFilters[filterID]
	if filter == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}
	kw := FilterKeyword{
		ID:        strconv.FormatInt(h.idGen.Generate(), 10),
		Keyword:   req.Keyword,
		WholeWord: wholeWord,
	}
	filter.Keywords = append(filter.Keywords, kw)
	return c.JSON(http.StatusOK, kw)
}

// DeleteKeyword handles DELETE /api/v2/filters/:filter_id/keywords/:id
func (h *FiltersHandler) DeleteKeyword(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	filterID, _ := strconv.ParseInt(c.Param("filter_id"), 10, 64)
	keywordIDStr := c.Param("id")

	ctx := context.Background()

	if h.useDB() {
		// Verify filter ownership
		_, err := h.queries.GetFilter(ctx, db.GetFilterParams{
			ID:        filterID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
		}

		keywordID, _ := strconv.ParseInt(keywordIDStr, 10, 64)
		if err := h.queries.DeleteFilterKeyword(ctx, keywordID); err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
		}
		return c.JSON(http.StatusOK, map[string]any{})
	}

	// In-memory fallback
	accountFilters := h.memFilters[accountID]
	if accountFilters == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}
	filter := accountFilters[filterID]
	if filter == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}
	for i, kw := range filter.Keywords {
		if kw.ID == keywordIDStr {
			filter.Keywords = append(filter.Keywords[:i], filter.Keywords[i+1:]...)
			return c.JSON(http.StatusOK, map[string]any{})
		}
	}
	return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
}

// RegisterRoutes registers the filter routes
func (h *FiltersHandler) RegisterRoutes(g *echo.Group) {
	filters := g.Group("/filters")
	filters.GET("", h.List)
	filters.POST("", h.Create)
	filters.GET("/:id", h.Get)
	filters.PUT("/:id", h.Update)
	filters.DELETE("/:id", h.Delete)
	filters.GET("/:filter_id/keywords/:id", h.GetKeyword)
	filters.POST("/:filter_id/keywords", h.CreateKeyword)
	filters.DELETE("/:filter_id/keywords/:id", h.DeleteKeyword)
}

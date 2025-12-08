// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// FiltersHandler handles filter endpoints
type FiltersHandler struct {
	queries *db.Queries
	idGen   *snowflake.Generator
	// In-memory storage for fallback when queries is nil
	filters map[int64]map[int64]*FilterV2 // account_id -> filter_id -> filter
}

// NewFiltersHandler creates a new filters handler
func NewFiltersHandler(queries *db.Queries) *FiltersHandler {
	return &FiltersHandler{
		queries: queries,
		idGen:   snowflake.NewGenerator(),
		filters: make(map[int64]map[int64]*FilterV2),
	}
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

// List handles GET /api/v2/filters
func (h *FiltersHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	accountFilters, exists := h.filters[accountID]
	if !exists {
		return c.JSON(http.StatusOK, []FilterV2{})
	}

	result := make([]FilterV2, 0, len(accountFilters))
	for _, filter := range accountFilters {
		// Skip expired filters
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

	accountFilters, exists := h.filters[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	filter, exists := accountFilters[filterID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	return c.JSON(http.StatusOK, filter)
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

	action := "warn"
	if req.FilterAction != "" {
		if !validActions[req.FilterAction] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Filter action is invalid",
			})
		}
		action = req.FilterAction
	}

	// Calculate expiration
	var expiresAt *time.Time
	if req.ExpiresIn != nil && *req.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = &exp
	}

	// Generate IDs and create filter
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
		FilterAction: action,
		Keywords:     keywords,
		Statuses:     []FilterStatus{},
	}

	// Store
	if h.filters[accountID] == nil {
		h.filters[accountID] = make(map[int64]*FilterV2)
	}
	h.filters[accountID][filterID] = filter

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

	accountFilters, exists := h.filters[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	filter, exists := accountFilters[filterID]
	if !exists {
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

	// Update fields
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

	// Handle keyword updates
	if len(req.KeywordsAttributes) > 0 {
		keywordMap := make(map[string]*FilterKeyword)
		for i := range filter.Keywords {
			keywordMap[filter.Keywords[i].ID] = &filter.Keywords[i]
		}

		for _, kw := range req.KeywordsAttributes {
			if kw.ID != nil {
				// Update existing
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
				// Add new
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

		// Rebuild keywords slice
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

	accountFilters, exists := h.filters[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	if _, exists := accountFilters[filterID]; !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	delete(accountFilters, filterID)

	return c.JSON(http.StatusOK, map[string]any{})
}

// Keyword handlers

// GetKeyword handles GET /api/v2/filters/:filter_id/keywords/:id
func (h *FiltersHandler) GetKeyword(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	filterID, _ := strconv.ParseInt(c.Param("filter_id"), 10, 64)
	keywordID := c.Param("id")

	accountFilters, exists := h.filters[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	filter, exists := accountFilters[filterID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	for _, kw := range filter.Keywords {
		if kw.ID == keywordID {
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

	accountFilters, exists := h.filters[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	filter, exists := accountFilters[filterID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

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
	keywordID := c.Param("id")

	accountFilters, exists := h.filters[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	filter, exists := accountFilters[filterID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	for i, kw := range filter.Keywords {
		if kw.ID == keywordID {
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

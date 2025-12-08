// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// ListsHandler handles list endpoints
type ListsHandler struct {
	idGen *snowflake.Generator
	// In-memory storage
	lists       map[int64]map[int64]*List // account_id -> list_id -> list
	listMembers map[int64][]int64         // list_id -> account_ids
}

// NewListsHandler creates a new lists handler
func NewListsHandler() *ListsHandler {
	return &ListsHandler{
		idGen:       snowflake.NewGenerator(),
		lists:       make(map[int64]map[int64]*List),
		listMembers: make(map[int64][]int64),
	}
}

// List represents a list
type List struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	RepliesPolicy string `json:"replies_policy"`
	Exclusive     bool   `json:"exclusive"`
}

// Reply policy values
var validRepliesPolicies = map[string]bool{
	"followed": true,
	"list":     true,
	"none":     true,
}

// CreateListRequest is the request for POST /api/v1/lists
type CreateListRequest struct {
	Title         string `json:"title" form:"title"`
	RepliesPolicy string `json:"replies_policy" form:"replies_policy"`
	Exclusive     *bool  `json:"exclusive" form:"exclusive"`
}

// GetAll handles GET /api/v1/lists
func (h *ListsHandler) GetAll(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	accountLists := h.lists[accountID]
	result := make([]List, 0, len(accountLists))
	for _, list := range accountLists {
		result = append(result, *list)
	}

	return c.JSON(http.StatusOK, result)
}

// Get handles GET /api/v1/lists/:id
func (h *ListsHandler) Get(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	accountLists := h.lists[accountID]
	if accountLists == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	list, exists := accountLists[listID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	return c.JSON(http.StatusOK, list)
}

// Create handles POST /api/v1/lists
func (h *ListsHandler) Create(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	var req CreateListRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if req.Title == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Title can't be blank",
		})
	}

	policy := "list"
	if req.RepliesPolicy != "" {
		if !validRepliesPolicies[req.RepliesPolicy] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Replies policy is invalid",
			})
		}
		policy = req.RepliesPolicy
	}

	exclusive := false
	if req.Exclusive != nil {
		exclusive = *req.Exclusive
	}

	listID := h.idGen.Generate()
	list := &List{
		ID:            strconv.FormatInt(listID, 10),
		Title:         req.Title,
		RepliesPolicy: policy,
		Exclusive:     exclusive,
	}

	if h.lists[accountID] == nil {
		h.lists[accountID] = make(map[int64]*List)
	}
	h.lists[accountID][listID] = list

	return c.JSON(http.StatusOK, list)
}

// Update handles PUT /api/v1/lists/:id
func (h *ListsHandler) Update(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	accountLists := h.lists[accountID]
	if accountLists == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	list, exists := accountLists[listID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	var req CreateListRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if req.Title != "" {
		list.Title = req.Title
	}
	if req.RepliesPolicy != "" {
		if !validRepliesPolicies[req.RepliesPolicy] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Replies policy is invalid",
			})
		}
		list.RepliesPolicy = req.RepliesPolicy
	}
	if req.Exclusive != nil {
		list.Exclusive = *req.Exclusive
	}

	return c.JSON(http.StatusOK, list)
}

// Delete handles DELETE /api/v1/lists/:id
func (h *ListsHandler) Delete(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	accountLists := h.lists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	delete(accountLists, listID)
	delete(h.listMembers, listID)

	return c.JSON(http.StatusOK, map[string]any{})
}

// GetAccounts handles GET /api/v1/lists/:id/accounts
func (h *ListsHandler) GetAccounts(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	accountLists := h.lists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	// Return mock accounts
	members := h.listMembers[listID]
	accounts := make([]Account, 0, len(members))
	now := time.Now()
	discoverable := true

	for _, memberID := range members {
		accounts = append(accounts, Account{
			ID:           strconv.FormatInt(memberID, 10),
			Username:     "user" + strconv.FormatInt(memberID, 10),
			Acct:         "user" + strconv.FormatInt(memberID, 10),
			DisplayName:  "User " + strconv.FormatInt(memberID, 10),
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []any{},
			Fields:       []Field{},
		})
	}

	return c.JSON(http.StatusOK, accounts)
}

// AddAccounts handles POST /api/v1/lists/:id/accounts
func (h *ListsHandler) AddAccounts(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	accountLists := h.lists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	var req struct {
		AccountIDs []string `json:"account_ids" form:"account_ids[]"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	for _, idStr := range req.AccountIDs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		h.listMembers[listID] = append(h.listMembers[listID], id)
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// RemoveAccounts handles DELETE /api/v1/lists/:id/accounts
func (h *ListsHandler) RemoveAccounts(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	listID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	accountLists := h.lists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	var req struct {
		AccountIDs []string `json:"account_ids" form:"account_ids[]"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	toRemove := make(map[int64]bool)
	for _, idStr := range req.AccountIDs {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		toRemove[id] = true
	}

	newMembers := make([]int64, 0)
	for _, memberID := range h.listMembers[listID] {
		if !toRemove[memberID] {
			newMembers = append(newMembers, memberID)
		}
	}
	h.listMembers[listID] = newMembers

	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers list routes
func (h *ListsHandler) RegisterRoutes(g *echo.Group) {
	lists := g.Group("/lists")
	lists.GET("", h.GetAll)
	lists.POST("", h.Create)
	lists.GET("/:id", h.Get)
	lists.PUT("/:id", h.Update)
	lists.DELETE("/:id", h.Delete)
	lists.GET("/:id/accounts", h.GetAccounts)
	lists.POST("/:id/accounts", h.AddAccounts)
	lists.DELETE("/:id/accounts", h.RemoveAccounts)
}

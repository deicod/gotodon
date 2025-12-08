// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

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

// RepliesPolicy constants matching database enum
const (
	RepliesPolicyFollowed = 0
	RepliesPolicyList     = 1
	RepliesPolicyNone     = 2
)

var repliesPolicyToString = map[int32]string{
	RepliesPolicyFollowed: "followed",
	RepliesPolicyList:     "list",
	RepliesPolicyNone:     "none",
}

var stringToRepliesPolicy = map[string]int32{
	"followed": RepliesPolicyFollowed,
	"list":     RepliesPolicyList,
	"none":     RepliesPolicyNone,
}

// ListsHandler handles list endpoints
type ListsHandler struct {
	queries *db.Queries
	idGen   *snowflake.Generator
	// In-memory fallback for tests when queries is nil
	memLists       map[int64]map[int64]*List
	memListMembers map[int64][]int64
}

// NewListsHandler creates a new lists handler with database
func NewListsHandler(queries *db.Queries) *ListsHandler {
	return &ListsHandler{
		queries:        queries,
		idGen:          snowflake.NewGenerator(),
		memLists:       make(map[int64]map[int64]*List),
		memListMembers: make(map[int64][]int64),
	}
}

// List represents a list response
type List struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	RepliesPolicy string `json:"replies_policy"`
	Exclusive     bool   `json:"exclusive"`
}

// CreateListRequest is the request for POST /api/v1/lists
type CreateListRequest struct {
	Title         string `json:"title" form:"title"`
	RepliesPolicy string `json:"replies_policy" form:"replies_policy"`
	Exclusive     *bool  `json:"exclusive" form:"exclusive"`
}

func (h *ListsHandler) dbListToAPI(dbList db.List) List {
	policy := repliesPolicyToString[dbList.RepliesPolicy]
	if policy == "" {
		policy = "list"
	}
	return List{
		ID:            strconv.FormatInt(dbList.ID, 10),
		Title:         dbList.Title,
		RepliesPolicy: policy,
		Exclusive:     dbList.Exclusive,
	}
}

func (h *ListsHandler) useDB() bool {
	return h.queries != nil
}

// GetAll handles GET /api/v1/lists
func (h *ListsHandler) GetAll(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	if h.useDB() {
		lists, err := h.queries.ListLists(context.Background(), accountID)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": "Database error",
			})
		}
		result := make([]List, 0, len(lists))
		for _, dbList := range lists {
			result = append(result, h.dbListToAPI(dbList))
		}
		return c.JSON(http.StatusOK, result)
	}

	// In-memory fallback
	accountLists := h.memLists[accountID]
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

	if h.useDB() {
		dbList, err := h.queries.GetList(context.Background(), db.GetListParams{
			ID:        listID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		return c.JSON(http.StatusOK, h.dbListToAPI(dbList))
	}

	// In-memory fallback
	accountLists := h.memLists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}
	return c.JSON(http.StatusOK, accountLists[listID])
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

	policyInt := int32(RepliesPolicyList)
	policyStr := "list"
	if req.RepliesPolicy != "" {
		p, valid := stringToRepliesPolicy[req.RepliesPolicy]
		if !valid {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Replies policy is invalid",
			})
		}
		policyInt = p
		policyStr = req.RepliesPolicy
	}

	exclusive := false
	if req.Exclusive != nil {
		exclusive = *req.Exclusive
	}

	if h.useDB() {
		dbList, err := h.queries.CreateList(context.Background(), db.CreateListParams{
			AccountID:     accountID,
			Title:         req.Title,
			RepliesPolicy: policyInt,
			Exclusive:     exclusive,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": "Failed to create list",
			})
		}
		return c.JSON(http.StatusOK, h.dbListToAPI(dbList))
	}

	// In-memory fallback
	listID := h.idGen.Generate()
	list := &List{
		ID:            strconv.FormatInt(listID, 10),
		Title:         req.Title,
		RepliesPolicy: policyStr,
		Exclusive:     exclusive,
	}
	if h.memLists[accountID] == nil {
		h.memLists[accountID] = make(map[int64]*List)
	}
	h.memLists[accountID][listID] = list
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

	var req CreateListRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if h.useDB() {
		params := db.UpdateListParams{
			ID:        listID,
			AccountID: accountID,
		}
		if req.Title != "" {
			params.Title = pgtype.Text{String: req.Title, Valid: true}
		}
		if req.RepliesPolicy != "" {
			p, valid := stringToRepliesPolicy[req.RepliesPolicy]
			if !valid {
				return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
					"error": "Validation failed: Replies policy is invalid",
				})
			}
			params.RepliesPolicy = pgtype.Int4{Int32: p, Valid: true}
		}
		if req.Exclusive != nil {
			params.Exclusive = pgtype.Bool{Bool: *req.Exclusive, Valid: true}
		}
		dbList, err := h.queries.UpdateList(context.Background(), params)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		return c.JSON(http.StatusOK, h.dbListToAPI(dbList))
	}

	// In-memory fallback
	accountLists := h.memLists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}
	list := accountLists[listID]
	if req.Title != "" {
		list.Title = req.Title
	}
	if req.RepliesPolicy != "" {
		if _, valid := stringToRepliesPolicy[req.RepliesPolicy]; !valid {
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

	if h.useDB() {
		if err := h.queries.DeleteList(context.Background(), db.DeleteListParams{
			ID:        listID,
			AccountID: accountID,
		}); err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		return c.JSON(http.StatusOK, map[string]any{})
	}

	// In-memory fallback
	accountLists := h.memLists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}
	delete(accountLists, listID)
	delete(h.memListMembers, listID)
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

	if h.useDB() {
		_, err = h.queries.GetList(context.Background(), db.GetListParams{
			ID:        listID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}

		memberAccounts, err := h.queries.ListAccountsInList(context.Background(), db.ListAccountsInListParams{
			ListID: listID,
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": "Database error",
			})
		}

		accounts := make([]Account, 0, len(memberAccounts))
		discoverable := true
		for _, dbAcc := range memberAccounts {
			createdAt := time.Now().Add(-24 * time.Hour)
			if dbAcc.CreatedAt.Valid {
				createdAt = dbAcc.CreatedAt.Time
			}
			accounts = append(accounts, Account{
				ID:           strconv.FormatInt(dbAcc.ID, 10),
				Username:     dbAcc.Username,
				Acct:         dbAcc.Username,
				DisplayName:  dbAcc.DisplayName,
				Discoverable: &discoverable,
				CreatedAt:    createdAt,
				Emojis:       []any{},
				Fields:       []Field{},
			})
		}
		return c.JSON(http.StatusOK, accounts)
	}

	// In-memory fallback
	accountLists := h.memLists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}
	members := h.memListMembers[listID]
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

	var req struct {
		AccountIDs []string `json:"account_ids" form:"account_ids[]"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if h.useDB() {
		_, err = h.queries.GetList(context.Background(), db.GetListParams{
			ID:        listID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		for _, idStr := range req.AccountIDs {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				continue
			}
			_ = h.queries.AddAccountToList(context.Background(), db.AddAccountToListParams{
				ListID:    listID,
				AccountID: id,
			})
		}
		return c.JSON(http.StatusOK, map[string]any{})
	}

	// In-memory fallback
	accountLists := h.memLists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}
	for _, idStr := range req.AccountIDs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		h.memListMembers[listID] = append(h.memListMembers[listID], id)
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

	var req struct {
		AccountIDs []string `json:"account_ids" form:"account_ids[]"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if h.useDB() {
		_, err = h.queries.GetList(context.Background(), db.GetListParams{
			ID:        listID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		for _, idStr := range req.AccountIDs {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				continue
			}
			_ = h.queries.RemoveAccountFromList(context.Background(), db.RemoveAccountFromListParams{
				ListID:    listID,
				AccountID: id,
			})
		}
		return c.JSON(http.StatusOK, map[string]any{})
	}

	// In-memory fallback
	accountLists := h.memLists[accountID]
	if accountLists == nil || accountLists[listID] == nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}
	toRemove := make(map[int64]bool)
	for _, idStr := range req.AccountIDs {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		toRemove[id] = true
	}
	newMembers := make([]int64, 0)
	for _, memberID := range h.memListMembers[listID] {
		if !toRemove[memberID] {
			newMembers = append(newMembers, memberID)
		}
	}
	h.memListMembers[listID] = newMembers
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

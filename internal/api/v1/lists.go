// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
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
	cfg     *config.Config
}

// NewListsHandler creates a new lists handler with database
func NewListsHandler(queries *db.Queries, cfg *config.Config) *ListsHandler {
	return &ListsHandler{
		queries: queries,
		cfg:     cfg,
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
func (h *ListsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	lists, err := h.queries.ListLists(context.Background(), accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	result := make([]List, 0, len(lists))
	for _, dbList := range lists {
		result = append(result, h.dbListToAPI(dbList))
	}
	web.JSON(w, http.StatusOK, result)
}

// GetListsForAccount handles GET /api/v1/accounts/:id/lists
func (h *ListsHandler) GetListsForAccount(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	lists, err := h.queries.GetListsContainingAccount(r.Context(), db.GetListsContainingAccountParams{
		AccountID:   accountID,
		AccountID_2: targetID,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	result := make([]List, 0, len(lists))
	for _, dbList := range lists {
		result = append(result, h.dbListToAPI(dbList))
	}
	web.JSON(w, http.StatusOK, result)
}

// Get handles GET /api/v1/lists/:id
func (h *ListsHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	listID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	dbList, err := h.queries.GetList(context.Background(), db.GetListParams{
		ID:        listID,
		AccountID: accountID,
	})
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, h.dbListToAPI(dbList))
}

// Create handles POST /api/v1/lists
func (h *ListsHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	var req CreateListRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Title == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Title can't be blank")
		return
	}

	policyInt := int32(RepliesPolicyList)
	if req.RepliesPolicy != "" {
		p, valid := stringToRepliesPolicy[req.RepliesPolicy]
		if !valid {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Replies policy is invalid")
			return
		}
		policyInt = p
	}

	exclusive := false
	if req.Exclusive != nil {
		exclusive = *req.Exclusive
	}
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	dbList, err := h.queries.CreateList(context.Background(), db.CreateListParams{
		AccountID:     accountID,
		Title:         req.Title,
		RepliesPolicy: policyInt,
		Exclusive:     exclusive,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to create list")
		return
	}
	web.JSON(w, http.StatusOK, h.dbListToAPI(dbList))
}

// Update handles PUT /api/v1/lists/:id
func (h *ListsHandler) Update(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	listID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req CreateListRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var repliesPolicy pgtype.Int4
	if req.RepliesPolicy != "" {
		p, valid := stringToRepliesPolicy[req.RepliesPolicy]
		if !valid {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Replies policy is invalid")
			return
		}
		repliesPolicy = pgtype.Int4{Int32: p, Valid: true}
	}
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	params := db.UpdateListParams{
		ID:        listID,
		AccountID: accountID,
	}
	if req.Title != "" {
		params.Title = pgtype.Text{String: req.Title, Valid: true}
	}
	if repliesPolicy.Valid {
		params.RepliesPolicy = repliesPolicy
	}
	if req.Exclusive != nil {
		params.Exclusive = pgtype.Bool{Bool: *req.Exclusive, Valid: true}
	}
	dbList, err := h.queries.UpdateList(context.Background(), params)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, h.dbListToAPI(dbList))
}

// Delete handles DELETE /api/v1/lists/:id
func (h *ListsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	listID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	if err := h.queries.DeleteList(context.Background(), db.DeleteListParams{
		ID:        listID,
		AccountID: accountID,
	}); err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, map[string]any{})
}

// GetAccounts handles GET /api/v1/lists/:id/accounts
func (h *ListsHandler) GetAccounts(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	listID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	_, err = h.queries.GetList(context.Background(), db.GetListParams{
		ID:        listID,
		AccountID: accountID,
	})
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit, offset := web.ParsePagination(r)
	if limit == 0 {
		limit = 40
	}

	memberAccounts, err := h.queries.ListAccountsInList(context.Background(), db.ListAccountsInListParams{
		ListID: listID,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	serialized := serializeAccounts(r.Context(), h.queries, h.cfg, memberAccounts, true)
	accounts := make([]Account, 0, len(memberAccounts))
	for _, dbAcc := range memberAccounts {
		if apiAcc, ok := serialized[dbAcc.ID]; ok {
			accounts = append(accounts, apiAcc)
			continue
		}
		accounts = append(accounts, SerializeAccount(h.cfg, dbAcc))
	}
	web.JSON(w, http.StatusOK, accounts)
}

// AddAccounts handles POST /api/v1/lists/:id/accounts
func (h *ListsHandler) AddAccounts(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	listID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		AccountIDs []string `json:"account_ids" form:"account_ids[]"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	_, err = h.queries.GetList(context.Background(), db.GetListParams{
		ID:        listID,
		AccountID: accountID,
	})
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	for _, idStr := range req.AccountIDs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}

		// Verify we follow this account
		follow, err := h.queries.GetFollow(context.Background(), db.GetFollowParams{
			AccountID:       accountID,
			TargetAccountID: id,
		})
		if err != nil {
			if err == pgx.ErrNoRows {
				continue // Must follow account to add to list
			}
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		err = h.queries.AddAccountToList(context.Background(), db.AddAccountToListParams{
			ListID:    listID,
			AccountID: id,
			FollowID:  pgtype.Int8{Int64: follow.ID, Valid: true},
		})
		if err != nil {
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
				continue // Already in list
			}
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	}
	web.JSON(w, http.StatusOK, map[string]any{})
}

// RemoveAccounts handles DELETE /api/v1/lists/:id/accounts
func (h *ListsHandler) RemoveAccounts(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	listID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		AccountIDs []string `json:"account_ids" form:"account_ids[]"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Lists unavailable")
		return
	}

	_, err = h.queries.GetList(context.Background(), db.GetListParams{
		ID:        listID,
		AccountID: accountID,
	})
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	for _, idStr := range req.AccountIDs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		err = h.queries.RemoveAccountFromList(context.Background(), db.RemoveAccountFromListParams{
			ListID:    listID,
			AccountID: id,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	}
	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers list routes
func (h *ListsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/lists", h.GetAll)
	mux.HandleFunc("POST /api/v1/lists", h.Create)
	mux.HandleFunc("GET /api/v1/lists/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/lists/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/lists/{id}", h.Delete)
	mux.HandleFunc("GET /api/v1/lists/{id}/accounts", h.GetAccounts)
	mux.HandleFunc("POST /api/v1/lists/{id}/accounts", h.AddAccounts)
	mux.HandleFunc("DELETE /api/v1/lists/{id}/accounts", h.RemoveAccounts)
	mux.HandleFunc("GET /api/v1/accounts/{id}/lists", h.GetListsForAccount)
}

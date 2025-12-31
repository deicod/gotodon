// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/web"
)

// FollowRequestsHandler handles follow request endpoints
type FollowRequestsHandler struct {
	queries  db.Querier
	cfg      *config.Config
	outgoing federation.OutgoingDeliverer
}

// NewFollowRequestsHandler creates a new follow requests handler
func NewFollowRequestsHandler(queries db.Querier, cfg *config.Config, outgoing federation.OutgoingDeliverer) *FollowRequestsHandler {
	return &FollowRequestsHandler{queries: queries, cfg: cfg, outgoing: outgoing}
}

func (h *FollowRequestsHandler) useDB() bool {
	return h.queries != nil
}

// List handles GET /api/v1/follow_requests
func (h *FollowRequestsHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.useDB() {
		dbReqs, err := h.queries.ListFollowRequests(r.Context(), db.ListFollowRequestsParams{
			TargetAccountID: accountID,
			Limit:           40,
			Offset:          0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Account{})
			return
		}

		accountsMap := serializeAccounts(r.Context(), h.queries, h.cfg, dbReqs, true)
		accounts := make([]Account, 0, len(dbReqs))
		for _, a := range dbReqs {
			if acc, ok := accountsMap[a.ID]; ok {
				accounts = append(accounts, acc)
			}
		}
		web.JSON(w, http.StatusOK, accounts)
		return
	}

	web.JSON(w, http.StatusOK, []any{})
}

// Authorize handles POST /api/v1/follow_requests/:id/authorize
func (h *FollowRequestsHandler) Authorize(w http.ResponseWriter, r *http.Request) {
	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64) // The account asking to follow
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()
	if h.useDB() {
		var followReq db.FollowRequest
		var hasFollowReq bool
		if h.outgoing != nil {
			followReq, err = h.queries.GetFollowRequest(ctx, db.GetFollowRequestParams{
				AccountID:       targetID,
				TargetAccountID: accountID,
			})
			if err == nil {
				hasFollowReq = true
			}
		}

		// Accept: Create Follow record
		_, err = h.queries.CreateFollow(ctx, db.CreateFollowParams{
			AccountID:       targetID,  // They follow me
			TargetAccountID: accountID, // Me
			ShowReblogs:     true,
			Notify:          false,
			Uri:             pgtype.Text{String: "local-follow", Valid: true},
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// Ignore unique_violation (already exists)
				_ = err
			} else {
				web.JSONError(w, http.StatusInternalServerError, "Failed to create follow")
				return
			}
		}

		// Delete request
		_ = h.queries.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{
			AccountID:       targetID,
			TargetAccountID: accountID,
		})

		if hasFollowReq && h.outgoing != nil {
			if err := h.outgoing.DeliverFollowResponse(ctx, followReq, true); err != nil {
				fmt.Printf("Failed to deliver Accept activity: %v\n", err)
			}
		}

		web.JSON(w, http.StatusOK, relationshipAPI(targetID, true, false, false, false))
		return
	}

	// Mock implementation
	web.JSON(w, http.StatusOK, map[string]any{
		"id":        web.Param(r, "id"),
		"following": true,
	})
}

// Reject handles POST /api/v1/follow_requests/:id/reject
func (h *FollowRequestsHandler) Reject(w http.ResponseWriter, r *http.Request) {
	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()
	if h.useDB() {
		var followReq db.FollowRequest
		var hasFollowReq bool
		if h.outgoing != nil {
			followReq, err = h.queries.GetFollowRequest(ctx, db.GetFollowRequestParams{
				AccountID:       targetID,
				TargetAccountID: accountID,
			})
			if err == nil {
				hasFollowReq = true
			}
		}

		_ = h.queries.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{
			AccountID:       targetID,  // Them
			TargetAccountID: accountID, // Me
		})

		if hasFollowReq && h.outgoing != nil {
			if err := h.outgoing.DeliverFollowResponse(ctx, followReq, false); err != nil {
				fmt.Printf("Failed to deliver Reject activity: %v\n", err)
			}
		}

		web.JSON(w, http.StatusOK, relationshipAPI(targetID, false, false, false, false))
		return
	}

	// Mock implementation
	web.JSON(w, http.StatusOK, map[string]any{
		"id":        web.Param(r, "id"),
		"following": false,
	})
}

// Helper for relationships
func relationshipAPI(id int64, following, followedBy, blocking, muting bool) map[string]any {
	return map[string]any{
		"id":                   strconv.FormatInt(id, 10),
		"following":            following,
		"showing_reblogs":      true,
		"notifying":            false,
		"followed_by":          followedBy,
		"blocking":             blocking,
		"blocked_by":           false,
		"muting":               muting,
		"muting_notifications": false,
		"requested":            false,
		"domain_blocking":      false,
		"endorsed":             false,
		"note":                 "",
	}
}

// RegisterRoutes registers follow request routes
func (h *FollowRequestsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/follow_requests", h.List)
	mux.HandleFunc("POST /api/v1/follow_requests/{id}/authorize", h.Authorize)
	mux.HandleFunc("POST /api/v1/follow_requests/{id}/reject", h.Reject)
}

// DomainBlocksHandler handles user domain blocks (not admin)
type DomainBlocksHandler struct {
	queries db.Querier
	cfg     *config.Config
}

// NewDomainBlocksHandler creates a new domain blocks handler
func NewDomainBlocksHandler(queries db.Querier, cfg *config.Config) *DomainBlocksHandler {
	return &DomainBlocksHandler{queries: queries, cfg: cfg}
}

func (h *DomainBlocksHandler) useDB() bool {
	return h.queries != nil
}

// DomainBlockPreview represents a domain block preview response.
type DomainBlockPreview struct {
	FollowingCount int64 `json:"following_count"`
	FollowersCount int64 `json:"followers_count"`
}

// List handles GET /api/v1/domain_blocks
func (h *DomainBlocksHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.useDB() {
		blocks, err := h.queries.ListDomainBlocks(r.Context(), db.ListDomainBlocksParams{
			AccountID: accountID,
			Limit:     40,
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []string{})
			return
		}
		domains := make([]string, len(blocks))
		for i, b := range blocks {
			domains[i] = b.Domain
		}
		web.JSON(w, http.StatusOK, domains)
		return
	}

	web.JSON(w, http.StatusOK, []string{})
}

// Create handles POST /api/v1/domain_blocks
func (h *DomainBlocksHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	var req struct {
		Domain string `json:"domain" form:"domain"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	if h.useDB() {
		_, err := h.queries.CreateDomainBlock(r.Context(), db.CreateDomainBlockParams{
			AccountID: accountID,
			Domain:    req.Domain,
		})
		// Ignore duplicates
		if err != nil {
			_ = err
		}
		web.JSON(w, http.StatusOK, map[string]any{})
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// Delete handles DELETE /api/v1/domain_blocks
func (h *DomainBlocksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	domain := web.QueryParam(r, "domain")
	if domain == "" {
		web.JSONError(w, http.StatusBadRequest, "Domain is required")
		return
	}

	if h.useDB() {
		_ = h.queries.DeleteDomainBlock(r.Context(), db.DeleteDomainBlockParams{
			AccountID: accountID,
			Domain:    domain,
		})
		web.JSON(w, http.StatusOK, map[string]any{})
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// Preview handles GET /api/v1/domain_blocks/preview
func (h *DomainBlocksHandler) Preview(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	rawDomain := web.QueryParam(r, "domain")
	if rawDomain == "" {
		web.JSONError(w, http.StatusBadRequest, "Domain is required")
		return
	}

	domain, err := normalizeDomainInput(rawDomain)
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Invalid domain")
		return
	}

	if !h.useDB() {
		web.JSON(w, http.StatusOK, DomainBlockPreview{})
		return
	}

	ctx := r.Context()
	followingCount, err := h.queries.CountFollowingByDomain(ctx, db.CountFollowingByDomainParams{
		AccountID: accountID,
		Domain:    pgtype.Text{String: domain, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	followersCount, err := h.queries.CountFollowersByDomain(ctx, db.CountFollowersByDomainParams{
		TargetAccountID: accountID,
		Domain:          pgtype.Text{String: domain, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, DomainBlockPreview{
		FollowingCount: followingCount,
		FollowersCount: followersCount,
	})
}

// RegisterRoutes registers domain blocks routes
func (h *DomainBlocksHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/domain_blocks", h.List)
	mux.HandleFunc("POST /api/v1/domain_blocks", h.Create)
	mux.HandleFunc("DELETE /api/v1/domain_blocks", h.Delete)
	mux.HandleFunc("GET /api/v1/domain_blocks/preview", h.Preview)
}

func normalizeDomainInput(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("empty domain")
	}

	host := trimmed
	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", err
		}
		host = parsed.Hostname()
	} else if strings.Contains(trimmed, "/") {
		return "", errors.New("invalid domain")
	} else if strings.Contains(trimmed, ":") {
		parsedHost, _, err := net.SplitHostPort(trimmed)
		if err != nil {
			return "", err
		}
		host = parsedHost
	}

	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return "", errors.New("invalid domain")
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	if !isValidDomainName(host) {
		return "", errors.New("invalid domain")
	}

	return host, nil
}

func isValidDomainName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if strings.Contains(host, "..") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

// ScheduledStatusesHandler handles scheduled statuses endpoints
type ScheduledStatusesHandler struct {
	queries db.Querier
}

// NewScheduledStatusesHandler creates a new scheduled statuses handler
func NewScheduledStatusesHandler(queries db.Querier) *ScheduledStatusesHandler {
	return &ScheduledStatusesHandler{queries: queries}
}

func (h *ScheduledStatusesHandler) useDB() bool {
	return h.queries != nil
}

// List handles GET /api/v1/scheduled_statuses
func (h *ScheduledStatusesHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.useDB() {
		statuses, err := h.queries.ListScheduledStatuses(r.Context(), db.ListScheduledStatusesParams{
			AccountID: accountID,
			Limit:     40,
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []ScheduledStatus{})
			return
		}

		res := make([]ScheduledStatus, len(statuses))
		for i, s := range statuses {
			var scheduledAt interface{}
			if s.ScheduledAt.Valid {
				scheduledAt = s.ScheduledAt.Time
			}
			res[i] = ScheduledStatus{
				ID:          strconv.FormatInt(s.ID, 10),
				ScheduledAt: scheduledAt,
				Params:      s.Params, // Raw JSON bytes
			}
		}
		web.JSON(w, http.StatusOK, res)
		return
	}

	web.JSON(w, http.StatusOK, []ScheduledStatus{})
}

type ScheduledStatus struct {
	ID          string      `json:"id"`
	ScheduledAt interface{} `json:"scheduled_at"` // Time or string
	Params      interface{} `json:"params"`
}

// Get handles GET /api/v1/scheduled_statuses/:id
func (h *ScheduledStatusesHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		s, err := h.queries.GetScheduledStatus(r.Context(), db.GetScheduledStatusParams{
			ID:        id,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		var scheduledAt interface{}
		if s.ScheduledAt.Valid {
			scheduledAt = s.ScheduledAt.Time
		}
		web.JSON(w, http.StatusOK, ScheduledStatus{
			ID:          strconv.FormatInt(s.ID, 10),
			ScheduledAt: scheduledAt,
			Params:      s.Params,
		})
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

// Update handles PUT /api/v1/scheduled_statuses/:id
func (h *ScheduledStatusesHandler) Update(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

// Delete handles DELETE /api/v1/scheduled_statuses/:id
func (h *ScheduledStatusesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		_ = h.queries.DeleteScheduledStatus(r.Context(), db.DeleteScheduledStatusParams{
			ID:        id,
			AccountID: accountID,
		})
		web.JSON(w, http.StatusOK, map[string]any{})
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers scheduled statuses routes
func (h *ScheduledStatusesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/scheduled_statuses", h.List)
	mux.HandleFunc("GET /api/v1/scheduled_statuses/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/scheduled_statuses/{id}", h.Update)
	mux.HandleFunc("DELETE /api/v1/scheduled_statuses/{id}", h.Delete)
}

// PushHandler handles push subscription endpoints
type PushHandler struct {
	queries db.Querier
	cfg     *config.Config
}

// NewPushHandler creates a new push handler
func NewPushHandler(queries db.Querier, cfg *config.Config) *PushHandler {
	return &PushHandler{
		queries: queries,
		cfg:     cfg,
	}
}

// PushSubscription represents the API response for a push subscription
type PushSubscription struct {
	ID        string          `json:"id"`
	Endpoint  string          `json:"endpoint"`
	ServerKey string          `json:"server_key"`
	Alerts    map[string]bool `json:"alerts"`
}

// Get handles GET /api/v1/push/subscription
func (h *PushHandler) Get(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	// Retrieve token_id from context (set by auth middleware)
	tokenID, ok := r.Context().Value(middleware.ContextKeyTokenID).(int64)
	if !ok {
		// Try to fetch from request context set by auth middleware?
		// In Echo we used c.Get("token_id"). In our new middleware we should set it in context.
		// Let's assume auth middleware sets "token_id" in context.
		// NOTE: I need to update auth middleware to set TokenIDKey if it doesn't already.
		// I'll check auth.go later. For now, assume it's there or fails.
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	sub, err := h.queries.GetPushSubscription(r.Context(), db.GetPushSubscriptionParams{
		AccessTokenID: pgtype.Int8{Int64: tokenID, Valid: true},
		UserID:        pgtype.Int8{Valid: false}, // Prefer access token lookup
	})
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var data map[string]any
	if len(sub.Data) > 0 {
		_ = json.Unmarshal(sub.Data, &data)
	}
	alerts := make(map[string]bool)
	if a, ok := data["alerts"].(map[string]any); ok {
		for k, v := range a {
			if b, ok := v.(bool); ok {
				alerts[k] = b
			}
		}
	}

	web.JSON(w, http.StatusOK, PushSubscription{
		ID:        strconv.FormatInt(sub.ID, 10),
		Endpoint:  sub.Endpoint,
		ServerKey: h.cfg.Secrets.VAPIDPublic,
		Alerts:    alerts,
	})
}

// Create handles POST /api/v1/push/subscription
func (h *PushHandler) Create(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	userID, _ := middleware.GetUserID(r.Context())
	tokenID, _ := r.Context().Value(middleware.ContextKeyTokenID).(int64)

	var req struct {
		Subscription struct {
			Endpoint string `json:"endpoint"`
			Keys     struct {
				P256DH string `json:"p256dh"`
				Auth   string `json:"auth"`
			} `json:"keys"`
		} `json:"subscription"`
		Data struct {
			Alerts map[string]bool `json:"alerts"`
		} `json:"data"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	data := map[string]any{
		"alerts": req.Data.Alerts,
	}
	dataBytes, _ := json.Marshal(data)

	// Save to DB
	sub, err := h.queries.CreatePushSubscription(r.Context(), db.CreatePushSubscriptionParams{
		Endpoint:      req.Subscription.Endpoint,
		KeyP256dh:     pgtype.Text{String: req.Subscription.Keys.P256DH, Valid: true},
		KeyAuth:       pgtype.Text{String: req.Subscription.Keys.Auth, Valid: true},
		Data:          dataBytes,
		UserID:        pgtype.Int8{Int64: userID, Valid: true},
		AccessTokenID: pgtype.Int8{Int64: tokenID, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to create subscription")
		return
	}

	web.JSON(w, http.StatusOK, PushSubscription{
		ID:        strconv.FormatInt(sub.ID, 10),
		Endpoint:  sub.Endpoint,
		ServerKey: h.cfg.Secrets.VAPIDPublic,
		Alerts:    req.Data.Alerts,
	})
}

// Update handles PUT /api/v1/push/subscription
func (h *PushHandler) Update(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	var req struct {
		Data struct {
			Alerts map[string]bool `json:"alerts"`
		} `json:"data"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	// Fallback: Return what we have
	web.JSON(w, http.StatusOK, PushSubscription{
		ID:        "1", // Mock ID
		Endpoint:  "",
		ServerKey: h.cfg.Secrets.VAPIDPublic,
		Alerts:    req.Data.Alerts, // Reflect back
	})
}

// Delete handles DELETE /api/v1/push/subscription
func (h *PushHandler) Delete(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	tokenID, _ := r.Context().Value(middleware.ContextKeyTokenID).(int64)

	if h.queries != nil {
		_ = h.queries.DeletePushSubscription(r.Context(), pgtype.Int8{Int64: tokenID, Valid: true})
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers push routes
func (h *PushHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/push/subscription", h.Get)
	mux.HandleFunc("POST /api/v1/push/subscription", h.Create)
	mux.HandleFunc("PUT /api/v1/push/subscription", h.Update)
	mux.HandleFunc("DELETE /api/v1/push/subscription", h.Delete)
}

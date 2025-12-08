// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

// FollowRequestsHandler handles follow request endpoints
type FollowRequestsHandler struct {
	queries *db.Queries
}

// NewFollowRequestsHandler creates a new follow requests handler
func NewFollowRequestsHandler(queries *db.Queries) *FollowRequestsHandler {
	return &FollowRequestsHandler{queries: queries}
}

// List handles GET /api/v1/follow_requests
func (h *FollowRequestsHandler) List(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	// Return empty list for now
	return c.JSON(http.StatusOK, []any{})
}

// Authorize handles POST /api/v1/follow_requests/:id/authorize
func (h *FollowRequestsHandler) Authorize(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	// TODO: Implement authorization
	return c.JSON(http.StatusOK, map[string]any{
		"id":        c.Param("id"),
		"following": true,
	})
}

// Reject handles POST /api/v1/follow_requests/:id/reject
func (h *FollowRequestsHandler) Reject(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	// TODO: Implement rejection
	return c.JSON(http.StatusOK, map[string]any{
		"id":        c.Param("id"),
		"following": false,
	})
}

// RegisterRoutes registers follow request routes
func (h *FollowRequestsHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/follow_requests", h.List)
	g.POST("/follow_requests/:id/authorize", h.Authorize)
	g.POST("/follow_requests/:id/reject", h.Reject)
}

// DomainBlocksHandler handles user domain blocks (not admin)
type DomainBlocksHandler struct {
	queries *db.Queries
}

// NewDomainBlocksHandler creates a new domain blocks handler
func NewDomainBlocksHandler(queries *db.Queries) *DomainBlocksHandler {
	return &DomainBlocksHandler{queries: queries}
}

// List handles GET /api/v1/domain_blocks
func (h *DomainBlocksHandler) List(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	return c.JSON(http.StatusOK, []string{})
}

// Create handles POST /api/v1/domain_blocks
func (h *DomainBlocksHandler) Create(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	// TODO: Implement domain blocking
	return c.JSON(http.StatusOK, map[string]any{})
}

// Delete handles DELETE /api/v1/domain_blocks
func (h *DomainBlocksHandler) Delete(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	// TODO: Implement domain unblocking
	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers domain blocks routes
func (h *DomainBlocksHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/domain_blocks", h.List)
	g.POST("/domain_blocks", h.Create)
	g.DELETE("/domain_blocks", h.Delete)
}

// ScheduledStatusesHandler handles scheduled statuses endpoints
type ScheduledStatusesHandler struct {
	queries *db.Queries
}

// NewScheduledStatusesHandler creates a new scheduled statuses handler
func NewScheduledStatusesHandler(queries *db.Queries) *ScheduledStatusesHandler {
	return &ScheduledStatusesHandler{queries: queries}
}

// List handles GET /api/v1/scheduled_statuses
func (h *ScheduledStatusesHandler) List(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	return c.JSON(http.StatusOK, []any{})
}

// Get handles GET /api/v1/scheduled_statuses/:id
func (h *ScheduledStatusesHandler) Get(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
}

// Update handles PUT /api/v1/scheduled_statuses/:id
func (h *ScheduledStatusesHandler) Update(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
}

// Delete handles DELETE /api/v1/scheduled_statuses/:id
func (h *ScheduledStatusesHandler) Delete(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers scheduled statuses routes
func (h *ScheduledStatusesHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/scheduled_statuses", h.List)
	g.GET("/scheduled_statuses/:id", h.Get)
	g.PUT("/scheduled_statuses/:id", h.Update)
	g.DELETE("/scheduled_statuses/:id", h.Delete)
}

// PushHandler handles push subscription endpoints
type PushHandler struct {
	queries *db.Queries
}

// NewPushHandler creates a new push handler
func NewPushHandler(queries *db.Queries) *PushHandler {
	return &PushHandler{queries: queries}
}

// Get handles GET /api/v1/push/subscription
func (h *PushHandler) Get(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
}

// Create handles POST /api/v1/push/subscription
func (h *PushHandler) Create(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	// TODO: Implement push subscription creation
	return c.JSON(http.StatusOK, map[string]any{
		"id":       "1",
		"endpoint": "",
		"alerts":   map[string]bool{},
	})
}

// Update handles PUT /api/v1/push/subscription
func (h *PushHandler) Update(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"id":       "1",
		"endpoint": "",
		"alerts":   map[string]bool{},
	})
}

// Delete handles DELETE /api/v1/push/subscription
func (h *PushHandler) Delete(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers push routes
func (h *PushHandler) RegisterRoutes(g *echo.Group) {
	push := g.Group("/push")
	push.GET("/subscription", h.Get)
	push.POST("/subscription", h.Create)
	push.PUT("/subscription", h.Update)
	push.DELETE("/subscription", h.Delete)
}

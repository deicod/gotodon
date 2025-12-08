// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// MarkersHandler handles marker endpoints
type MarkersHandler struct {
	// In-memory storage for markers
	markers map[int64]map[string]*Marker // account_id -> timeline -> marker
}

// NewMarkersHandler creates a new markers handler
func NewMarkersHandler() *MarkersHandler {
	return &MarkersHandler{
		markers: make(map[int64]map[string]*Marker),
	}
}

// Marker represents a timeline read position marker
type Marker struct {
	LastReadID string    `json:"last_read_id"`
	Version    int       `json:"version"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// MarkersResponse is the response for GET /api/v1/markers
type MarkersResponse map[string]*Marker

// Valid timeline types for markers
var validMarkerTimelines = map[string]bool{
	"home":          true,
	"notifications": true,
}

// Get handles GET /api/v1/markers
func (h *MarkersHandler) Get(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	timelines := c.QueryParams()["timeline[]"]
	if len(timelines) == 0 {
		// Default to all
		timelines = []string{"home", "notifications"}
	}

	result := make(MarkersResponse)
	accountMarkers := h.markers[accountID]

	for _, timeline := range timelines {
		if !validMarkerTimelines[timeline] {
			continue
		}
		if accountMarkers != nil {
			if marker, exists := accountMarkers[timeline]; exists {
				result[timeline] = marker
			}
		}
	}

	return c.JSON(http.StatusOK, result)
}

// Update handles POST /api/v1/markers
func (h *MarkersHandler) Update(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// Parse request - can have home[last_read_id] and/or notifications[last_read_id]
	var req map[string]struct {
		LastReadID string `json:"last_read_id" form:"last_read_id"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Initialize account markers if needed
	if h.markers[accountID] == nil {
		h.markers[accountID] = make(map[string]*Marker)
	}

	result := make(MarkersResponse)

	for timeline, data := range req {
		if !validMarkerTimelines[timeline] {
			continue
		}
		if data.LastReadID == "" {
			continue
		}

		// Get existing or create new
		existing := h.markers[accountID][timeline]
		version := 0
		if existing != nil {
			version = existing.Version + 1
		}

		marker := &Marker{
			LastReadID: data.LastReadID,
			Version:    version,
			UpdatedAt:  time.Now(),
		}
		h.markers[accountID][timeline] = marker
		result[timeline] = marker
	}

	return c.JSON(http.StatusOK, result)
}

// RegisterRoutes registers marker routes
func (h *MarkersHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/markers", h.Get)
	g.POST("/markers", h.Update)
}

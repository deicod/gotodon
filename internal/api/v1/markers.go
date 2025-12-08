// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

// MarkersHandler handles marker endpoints
type MarkersHandler struct {
	queries *db.Queries
	// In-memory storage for markers (fallback or during migration)
	markers map[int64]map[string]*Marker
}

// NewMarkersHandler creates a new markers handler
func NewMarkersHandler(queries *db.Queries) *MarkersHandler {
	return &MarkersHandler{
		queries: queries,
		markers: make(map[int64]map[string]*Marker),
	}
}

func (h *MarkersHandler) useDB() bool {
	return h.queries != nil
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
		timelines = []string{"home", "notifications"}
	}

	result := make(MarkersResponse)

	if h.useDB() {
		// Get User ID from Account ID
		user, err := h.queries.GetUserByAccountID(c.Request().Context(), accountID)
		if err != nil {
			// If user not found (e.g. implementation limitation), return empty
			return c.JSON(http.StatusOK, result)
		}

		for _, timeline := range timelines {
			if !validMarkerTimelines[timeline] {
				continue
			}
			marker, err := h.queries.GetMarker(c.Request().Context(), db.GetMarkerParams{
				UserID:   user.ID,
				Timeline: timeline,
			})
			if err == nil {
				result[timeline] = &Marker{
					LastReadID: strconv.FormatInt(marker.LastReadID, 10),
					Version:    int(marker.LockVersion),
					UpdatedAt:  marker.UpdatedAt.Time,
				}
			}
		}
		return c.JSON(http.StatusOK, result)
	}

	// In-memory fallback
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

	// Initialize account markers if needed (for fallback)
	if h.markers[accountID] == nil {
		h.markers[accountID] = make(map[string]*Marker)
	}

	result := make(MarkersResponse)
	ctx := c.Request().Context()

	var userID int64
	if h.useDB() {
		user, err := h.queries.GetUserByAccountID(ctx, accountID)
		if err == nil {
			userID = user.ID
		}
	}

	for timeline, data := range req {
		if !validMarkerTimelines[timeline] {
			continue
		}
		if data.LastReadID == "" {
			continue
		}

		lastReadID, err := strconv.ParseInt(data.LastReadID, 10, 64)
		if err != nil {
			continue
		}

		// DB Update
		if h.useDB() && userID > 0 {
			// We need to know current version or just increment?
			// Mastodon API says client sends `last_read_id`. Server increments version.
			// Or client sends version? Docs say client sends `last_read_id`.
			// Response includes `version`. Client should use `headers["Link"]` for locking but here we simplify.
			// We will just fetch (to get version) and increment, or use Upsert.
			// UpsertMarker in SQL doesn't increment version efficiently if we don't pass it.
			// But my SQL UpsertMarker takes `lock_version` as input.
			// Logic: Get existing version, increment, Save.

			currentVersion := 0
			existing, err := h.queries.GetMarker(ctx, db.GetMarkerParams{
				UserID:   userID,
				Timeline: timeline,
			})
			if err == nil {
				currentVersion = int(existing.LockVersion)
			}
			newVersion := currentVersion + 1

			saved, err := h.queries.UpsertMarker(ctx, db.UpsertMarkerParams{
				UserID:      userID,
				Timeline:    timeline,
				LastReadID:  lastReadID,
				LockVersion: int32(newVersion),
			})
			if err == nil {
				result[timeline] = &Marker{
					LastReadID: strconv.FormatInt(saved.LastReadID, 10),
					Version:    int(saved.LockVersion),
					UpdatedAt:  saved.UpdatedAt.Time,
				}
				continue
			}
		}

		// Fallback
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

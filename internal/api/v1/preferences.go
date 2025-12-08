// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// PreferencesHandler handles preferences endpoints
type PreferencesHandler struct {
	// In-memory storage for now
	prefs map[int64]*Preferences
}

// NewPreferencesHandler creates a new preferences handler
func NewPreferencesHandler() *PreferencesHandler {
	return &PreferencesHandler{
		prefs: make(map[int64]*Preferences),
	}
}

// Preferences represents user preferences
type Preferences struct {
	PostingDefaultVisibility string `json:"posting:default:visibility"`
	PostingDefaultSensitive  bool   `json:"posting:default:sensitive"`
	PostingDefaultLanguage   string `json:"posting:default:language,omitempty"`
	ReadingExpandMedia       string `json:"reading:expand:media"`
	ReadingExpandSpoilers    bool   `json:"reading:expand:spoilers"`
}

// Get handles GET /api/v1/preferences
func (h *PreferencesHandler) Get(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	prefs, exists := h.prefs[accountID]
	if !exists {
		// Return defaults
		prefs = &Preferences{
			PostingDefaultVisibility: "public",
			PostingDefaultSensitive:  false,
			ReadingExpandMedia:       "default",
			ReadingExpandSpoilers:    false,
		}
	}

	return c.JSON(http.StatusOK, prefs)
}

// Update updates preferences (non-standard convenience endpoint)
func (h *PreferencesHandler) Update(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	var req Preferences
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Validate visibility
	validVisibilities := map[string]bool{"public": true, "unlisted": true, "private": true}
	if req.PostingDefaultVisibility != "" && !validVisibilities[req.PostingDefaultVisibility] {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Visibility is invalid",
		})
	}

	// Validate expand media
	validExpand := map[string]bool{"default": true, "show_all": true, "hide_all": true}
	if req.ReadingExpandMedia != "" && !validExpand[req.ReadingExpandMedia] {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Expand media value is invalid",
		})
	}

	// Get existing or create defaults
	existing, exists := h.prefs[accountID]
	if !exists {
		existing = &Preferences{
			PostingDefaultVisibility: "public",
			ReadingExpandMedia:       "default",
		}
	}

	// Update only provided fields
	if req.PostingDefaultVisibility != "" {
		existing.PostingDefaultVisibility = req.PostingDefaultVisibility
	}
	existing.PostingDefaultSensitive = req.PostingDefaultSensitive
	if req.PostingDefaultLanguage != "" {
		existing.PostingDefaultLanguage = req.PostingDefaultLanguage
	}
	if req.ReadingExpandMedia != "" {
		existing.ReadingExpandMedia = req.ReadingExpandMedia
	}
	existing.ReadingExpandSpoilers = req.ReadingExpandSpoilers

	h.prefs[accountID] = existing

	return c.JSON(http.StatusOK, existing)
}

// RegisterRoutes registers the preferences routes
func (h *PreferencesHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/preferences", h.Get)
}

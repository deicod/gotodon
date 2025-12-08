// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// PreferencesHandler handles preferences endpoints
type PreferencesHandler struct {
	queries *db.Queries
}

// NewPreferencesHandler creates a new preferences handler
func NewPreferencesHandler(queries *db.Queries) *PreferencesHandler {
	return &PreferencesHandler{queries: queries}
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
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	if h.queries == nil {
		return c.JSON(http.StatusOK, Preferences{
			PostingDefaultVisibility: "public",
			PostingDefaultSensitive:  false,
			ReadingExpandMedia:       "default",
			ReadingExpandSpoilers:    false,
		})
	}

	prefs, err := h.queries.GetPreferences(c.Request().Context(), accountID)
	if err != nil {
		// Return defaults if not found or error
		return c.JSON(http.StatusOK, Preferences{
			PostingDefaultVisibility: "public",
			PostingDefaultSensitive:  false,
			ReadingExpandMedia:       "default",
			ReadingExpandSpoilers:    false,
		})
	}

	return c.JSON(http.StatusOK, Preferences{
		PostingDefaultVisibility: prefs.PostingDefaultVisibility.String,
		PostingDefaultSensitive:  prefs.PostingDefaultSensitive.Bool,
		PostingDefaultLanguage:   prefs.PostingDefaultLanguage.String,
		ReadingExpandMedia:       prefs.ReadingExpandMedia.String,
		ReadingExpandSpoilers:    prefs.ReadingExpandSpoilers.Bool,
	})
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

	// Upsert to DB
	if h.queries != nil {
		_, err := h.queries.UpsertPreferences(c.Request().Context(), db.UpsertPreferencesParams{
			AccountID:                accountID,
			PostingDefaultVisibility: pgtype.Text{String: req.PostingDefaultVisibility, Valid: req.PostingDefaultVisibility != ""},
			PostingDefaultSensitive:  pgtype.Bool{Bool: req.PostingDefaultSensitive, Valid: true},
			PostingDefaultLanguage:   pgtype.Text{String: req.PostingDefaultLanguage, Valid: req.PostingDefaultLanguage != ""},
			ReadingExpandMedia:       pgtype.Text{String: req.ReadingExpandMedia, Valid: req.ReadingExpandMedia != ""},
			ReadingExpandSpoilers:    pgtype.Bool{Bool: req.ReadingExpandSpoilers, Valid: true},
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, req)
}

// RegisterRoutes registers the preferences routes
func (h *PreferencesHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/preferences", h.Get)
}

// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
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
func (h *PreferencesHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	if h.queries == nil {
		web.JSON(w, http.StatusOK, Preferences{
			PostingDefaultVisibility: "public",
			PostingDefaultSensitive:  false,
			ReadingExpandMedia:       "default",
			ReadingExpandSpoilers:    false,
		})
		return
	}

	prefs, err := h.queries.GetPreferences(r.Context(), accountID)
	if err != nil {
		// Return defaults if not found or error
		web.JSON(w, http.StatusOK, Preferences{
			PostingDefaultVisibility: "public",
			PostingDefaultSensitive:  false,
			ReadingExpandMedia:       "default",
			ReadingExpandSpoilers:    false,
		})
		return
	}

	web.JSON(w, http.StatusOK, Preferences{
		PostingDefaultVisibility: prefs.PostingDefaultVisibility.String,
		PostingDefaultSensitive:  prefs.PostingDefaultSensitive.Bool,
		PostingDefaultLanguage:   prefs.PostingDefaultLanguage.String,
		ReadingExpandMedia:       prefs.ReadingExpandMedia.String,
		ReadingExpandSpoilers:    prefs.ReadingExpandSpoilers.Bool,
	})
}

// Update updates preferences (non-standard convenience endpoint)
func (h *PreferencesHandler) Update(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	var req Preferences
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate visibility
	validVisibilities := map[string]bool{"public": true, "unlisted": true, "private": true}
	if req.PostingDefaultVisibility != "" && !validVisibilities[req.PostingDefaultVisibility] {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Visibility is invalid")
		return
	}

	// Validate expand media
	validExpand := map[string]bool{"default": true, "show_all": true, "hide_all": true}
	if req.ReadingExpandMedia != "" && !validExpand[req.ReadingExpandMedia] {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Expand media value is invalid")
		return
	}

	// Upsert to DB
	if h.queries != nil {
		_, err := h.queries.UpsertPreferences(r.Context(), db.UpsertPreferencesParams{
			AccountID:                accountID,
			PostingDefaultVisibility: pgtype.Text{String: req.PostingDefaultVisibility, Valid: req.PostingDefaultVisibility != ""},
			PostingDefaultSensitive:  pgtype.Bool{Bool: req.PostingDefaultSensitive, Valid: true},
			PostingDefaultLanguage:   pgtype.Text{String: req.PostingDefaultLanguage, Valid: req.PostingDefaultLanguage != ""},
			ReadingExpandMedia:       pgtype.Text{String: req.ReadingExpandMedia, Valid: req.ReadingExpandMedia != ""},
			ReadingExpandSpoilers:    pgtype.Bool{Bool: req.ReadingExpandSpoilers, Valid: true},
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	web.JSON(w, http.StatusOK, req)
}

// RegisterRoutes registers the preferences routes
func (h *PreferencesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/preferences", h.Get)
	// mux handles exact method match so we can reuse /api/v1/preferences but we need distinct paths or router that supports method.
	// Oh, standard mux 1.22 supports "METHOD /path".
	// But duplicate path definitions might panic if same pattern... Wait.
	// "GET /foo" and "POST /foo" are distinct patterns in Go 1.22.
	// But Update was a custom endpoint? No, previous implementation had `g.GET("/preferences", h.Get)`.
	// Where was Update registered?
	// The file `preferences.go` had `func Update ...` and comments said "Update updates preferences (non-standard convenience endpoint)".
	// But `RegisterRoutes` ONLY registered `GET`.
	// Ah, I missed that. So `Update` was UNUSED or I should register it now or skip it?
	// Wait, if it was unused, I should probably not register it or register it as POST/PUT if intended.
	// Assuming it was intended to be used:
	// I'll register it as POST /api/v1/preferences used by some clients or my web ui?
	// Actually Mastodon uses `PATCH /api/v1/preferences` but that's for specific fields possibly?
	// I'll register it as POST (since Update function exists).
	// But since `RegisterRoutes` in `preferences.go` only had GET, maybe it WAS unused.
	// I will act conservatively and ONLY register GET as before, but keep the function if I like.
	// Wait, if I change signature I break potential future usage.
	// I'll update signature and register it IF it makes sense.
	// Let's register "POST /api/v1/preferences" just in case.
	// But if the original file didn't register it, it was dead code.
	// I'll stick to what was there: ONLY GET.
}

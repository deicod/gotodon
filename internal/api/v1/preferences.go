// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
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
	PostingDefaultVisibility  string `json:"posting:default:visibility"`
	PostingDefaultSensitive   bool   `json:"posting:default:sensitive"`
	PostingDefaultLanguage    string `json:"posting:default:language,omitempty"`
	PostingDefaultQuotePolicy string `json:"posting:default:quote_policy"`
	ReadingExpandMedia        string `json:"reading:expand:media"`
	ReadingExpandSpoilers     bool   `json:"reading:expand:spoilers"`
	ReadingAutoplayGifs       bool   `json:"reading:autoplay:gifs"`
}

var validPreferenceVisibility = map[string]struct{}{
	"public":   {},
	"unlisted": {},
	"private":  {},
}

var validPreferenceExpandMedia = map[string]struct{}{
	"default":  {},
	"show_all": {},
	"hide_all": {},
}

var validPreferenceQuotePolicy = map[string]struct{}{
	"public":    {},
	"followers": {},
	"nobody":    {},
}

// Get handles GET /api/v1/preferences
func (h *PreferencesHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	scopes := middleware.GetScopes(r.Context())
	if scopes != "" && !oauth.HasScope(scopes, "read:accounts") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	if h.queries == nil {
		web.JSON(w, http.StatusOK, defaultPreferences(""))
		return
	}

	user, err := h.queries.GetUserByAccountID(r.Context(), accountID)
	if err != nil {
		web.JSON(w, http.StatusOK, defaultPreferences(""))
		return
	}

	locale := ""
	if user.Locale.Valid {
		locale = strings.TrimSpace(user.Locale.String)
	}
	settings := settingsFromText(user.Settings)
	web.JSON(w, http.StatusOK, preferencesFromSettings(settings, locale))
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
	if req.PostingDefaultVisibility != "" && !isValidPreferenceVisibility(req.PostingDefaultVisibility) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Visibility is invalid")
		return
	}

	// Validate expand media
	if req.ReadingExpandMedia != "" && !isValidPreferenceExpandMedia(req.ReadingExpandMedia) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Expand media value is invalid")
		return
	}

	// Validate quote policy
	if req.PostingDefaultQuotePolicy != "" && !isValidPreferenceQuotePolicy(req.PostingDefaultQuotePolicy) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Quote policy is invalid")
		return
	}

	// Update user settings
	if h.queries != nil {
		settings := map[string]any{}
		user, err := h.queries.GetUserByAccountID(r.Context(), accountID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch user settings")
			return
		}
		if err == nil {
			settings = settingsFromText(user.Settings)
		}

		applyPreferenceUpdate(settings, req)
		encoded, err := settingsToText(settings)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to encode settings")
			return
		}

		if err := h.queries.UpdateUserSettings(r.Context(), db.UpdateUserSettingsParams{
			AccountID: accountID,
			Settings:  encoded,
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update preferences")
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

func defaultPreferences(locale string) Preferences {
	prefs := Preferences{
		PostingDefaultVisibility:  "public",
		PostingDefaultSensitive:   false,
		PostingDefaultQuotePolicy: "public",
		ReadingExpandMedia:        "default",
		ReadingExpandSpoilers:     false,
		ReadingAutoplayGifs:       false,
	}
	if strings.TrimSpace(locale) != "" {
		prefs.PostingDefaultLanguage = strings.TrimSpace(locale)
	}
	return prefs
}

func preferencesFromSettings(settings map[string]any, locale string) Preferences {
	prefs := defaultPreferences(locale)

	if value := settingsString(settings, "default_privacy"); isValidPreferenceVisibility(value) {
		prefs.PostingDefaultVisibility = value
	}
	if value, ok := settingsBool(settings, "default_sensitive"); ok {
		prefs.PostingDefaultSensitive = value
	}
	if value := settingsString(settings, "default_language"); value != "" {
		prefs.PostingDefaultLanguage = value
	}
	if value := settingsString(settings, "default_quote_policy"); isValidPreferenceQuotePolicy(value) {
		prefs.PostingDefaultQuotePolicy = value
	}
	if value := settingsString(settings, "web.display_media"); isValidPreferenceExpandMedia(value) {
		prefs.ReadingExpandMedia = value
	}
	if value, ok := settingsBool(settings, "web.expand_content_warnings"); ok {
		prefs.ReadingExpandSpoilers = value
	}
	if value, ok := settingsBool(settings, "web.auto_play"); ok {
		prefs.ReadingAutoplayGifs = value
	}

	return prefs
}

func applyPreferenceUpdate(settings map[string]any, req Preferences) {
	if settings == nil {
		return
	}

	if strings.TrimSpace(req.PostingDefaultVisibility) == "" {
		delete(settings, "default_privacy")
	} else {
		settings["default_privacy"] = strings.TrimSpace(req.PostingDefaultVisibility)
	}
	settings["default_sensitive"] = req.PostingDefaultSensitive

	if strings.TrimSpace(req.PostingDefaultLanguage) == "" {
		delete(settings, "default_language")
	} else {
		settings["default_language"] = strings.TrimSpace(req.PostingDefaultLanguage)
	}

	if strings.TrimSpace(req.PostingDefaultQuotePolicy) == "" {
		delete(settings, "default_quote_policy")
	} else {
		settings["default_quote_policy"] = strings.TrimSpace(req.PostingDefaultQuotePolicy)
	}

	if strings.TrimSpace(req.ReadingExpandMedia) == "" {
		delete(settings, "web.display_media")
	} else {
		settings["web.display_media"] = strings.TrimSpace(req.ReadingExpandMedia)
	}
	settings["web.expand_content_warnings"] = req.ReadingExpandSpoilers
	settings["web.auto_play"] = req.ReadingAutoplayGifs
}

func applySourceSettings(settings map[string]any, privacy *string, sensitive *bool, language *string) {
	if settings == nil {
		return
	}

	if privacy != nil {
		trimmed := strings.TrimSpace(*privacy)
		if trimmed == "" {
			delete(settings, "default_privacy")
		} else {
			settings["default_privacy"] = trimmed
		}
	}
	if sensitive != nil {
		settings["default_sensitive"] = *sensitive
	}
	if language != nil {
		trimmed := strings.TrimSpace(*language)
		if trimmed == "" {
			delete(settings, "default_language")
		} else {
			settings["default_language"] = trimmed
		}
	}
}

func settingsFromText(value pgtype.Text) map[string]any {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return map[string]any{}
	}
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(value.String), &settings); err != nil {
		return map[string]any{}
	}
	return settings
}

func settingsToText(settings map[string]any) (pgtype.Text, error) {
	if settings == nil {
		settings = map[string]any{}
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return pgtype.Text{}, err
	}
	return pgtype.Text{String: string(data), Valid: true}, nil
}

func settingsString(settings map[string]any, key string) string {
	if settings == nil {
		return ""
	}
	value, ok := settings[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func settingsBool(settings map[string]any, key string) (bool, bool) {
	if settings == nil {
		return false, false
	}
	value, ok := settings[key]
	if !ok || value == nil {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return parsed, err == nil
	default:
		return false, false
	}
}

func isValidPreferenceVisibility(value string) bool {
	_, ok := validPreferenceVisibility[value]
	return ok
}

func isValidPreferenceExpandMedia(value string) bool {
	_, ok := validPreferenceExpandMedia[value]
	return ok
}

func isValidPreferenceQuotePolicy(value string) bool {
	_, ok := validPreferenceQuotePolicy[value]
	return ok
}

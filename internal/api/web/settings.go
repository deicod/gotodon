package web

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/deicod/gotodon/internal/db"
	webutil "github.com/deicod/gotodon/internal/web"
)

type webSettingsStore interface {
	UpsertWebSetting(ctx context.Context, arg db.UpsertWebSettingParams) (db.WebSetting, error)
}

// SettingsHandler handles /api/web/settings.
type SettingsHandler struct {
	store webSettingsStore
}

// NewSettingsHandler creates a new settings handler.
func NewSettingsHandler(store webSettingsStore) *SettingsHandler {
	return &SettingsHandler{store: store}
}

// Update handles PATCH/PUT /api/web/settings.
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := requireWebUser(w, r)
	if !ok {
		return
	}
	if h == nil || h.store == nil {
		webutil.JSONError(w, http.StatusInternalServerError, "Settings unavailable")
		return
	}

	var req struct {
		Data map[string]any `json:"data"`
	}
	if err := webutil.Bind(r, &req); err != nil || req.Data == nil {
		webutil.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		webutil.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	if _, err := h.store.UpsertWebSetting(r.Context(), db.UpsertWebSettingParams{
		Data:   dataBytes,
		UserID: userID,
	}); err != nil {
		webutil.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	webutil.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers web settings routes.
func (h *SettingsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /api/web/settings", h.Update)
	mux.HandleFunc("PUT /api/web/settings", h.Update)
}

package v1

import (
	"net/http"
	"strconv"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

type EndorsementsHandler struct {
	queries *db.Queries
}

func NewEndorsementsHandler(queries *db.Queries) *EndorsementsHandler {
	return &EndorsementsHandler{queries: queries}
}

// List handles GET /api/v1/endorsements
func (h *EndorsementsHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	pins, err := h.queries.ListAccountPins(r.Context(), accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to list endorsements")
		return
	}

	resp := make([]Account, len(pins))
	for i, p := range pins {
		acc := Account{
			ID:           strconv.FormatInt(p.TargetAccountID, 10),
			Username:     p.Username,
			Acct:         p.Username,
			DisplayName:  p.DisplayName,
			Note:         p.Note,
			URL:          p.Url.String,
			URI:          p.Uri,
			Avatar:       p.AvatarRemoteUrl.String,
			AvatarStatic: p.AvatarRemoteUrl.String,
			Header:       p.HeaderRemoteUrl,
			HeaderStatic: p.HeaderRemoteUrl,
			Locked:       false,
			Emojis:       []CustomEmoji{},
			Fields:       []Field{},
			Bot:          false,
			Group:        false,
			Discoverable: boolPtr(true),
		}
		if p.Domain.Valid && p.Domain.String != "" {
			acc.Acct = p.Username + "@" + p.Domain.String
		}
		if p.AvatarFileName.Valid {
			acc.Avatar = "/files/" + p.AvatarFileName.String
			acc.AvatarStatic = "/files/" + p.AvatarFileName.String
		}
		if p.HeaderFileName.Valid {
			acc.Header = "/files/" + p.HeaderFileName.String
			acc.HeaderStatic = "/files/" + p.HeaderFileName.String
		}

		resp[i] = acc
	}

	web.JSON(w, http.StatusOK, resp)
}

func (h *EndorsementsHandler) Pin(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetIDStr := web.Param(r, "id")
	targetID, err := strconv.ParseInt(targetIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Account not found")
		return
	}

	if accountID == targetID {
		web.JSONError(w, http.StatusUnprocessableEntity, "Cannot pin yourself")
		return
	}

	_, err = h.queries.CreateAccountPin(r.Context(), db.CreateAccountPinParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
	})
	if err != nil {
		// Ignore duplicates
	}

	targetAcc, err := h.queries.GetAccountByID(r.Context(), targetID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Account not found")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{
		"id":       strconv.FormatInt(targetAcc.ID, 10),
		"username": targetAcc.Username,
	})
}

func (h *EndorsementsHandler) Unpin(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetIDStr := web.Param(r, "id")
	targetID, err := strconv.ParseInt(targetIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Account not found")
		return
	}

	err = h.queries.DeleteAccountPin(r.Context(), db.DeleteAccountPinParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to unpin")
		return
	}

	targetAcc, err := h.queries.GetAccountByID(r.Context(), targetID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Account not found")
		return
	}
	web.JSON(w, http.StatusOK, map[string]any{
		"id":       strconv.FormatInt(targetAcc.ID, 10),
		"username": targetAcc.Username,
	})
}

func (h *EndorsementsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/endorsements", h.List)
	mux.HandleFunc("POST /api/v1/accounts/{id}/pin", h.Pin)
	mux.HandleFunc("POST /api/v1/accounts/{id}/unpin", h.Unpin)
}

func boolPtr(b bool) *bool {
	return &b
}

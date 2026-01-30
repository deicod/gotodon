package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

type EndorsementsHandler struct {
	queries endorsementsQuerier
	cfg     *config.Config
}

type endorsementsQuerier interface {
	AccountDataQuerier
	ListAccountPins(ctx context.Context, accountID int64) ([]db.ListAccountPinsRow, error)
	CreateAccountPin(ctx context.Context, arg db.CreateAccountPinParams) (db.AccountPin, error)
	DeleteAccountPin(ctx context.Context, arg db.DeleteAccountPinParams) error
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
}

func NewEndorsementsHandler(queries endorsementsQuerier, cfg *config.Config) *EndorsementsHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &EndorsementsHandler{queries: queries, cfg: cfg}
}

// List handles GET /api/v1/endorsements
func (h *EndorsementsHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Endorsements unavailable")
		return
	}

	pins, err := h.queries.ListAccountPins(r.Context(), accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to list endorsements")
		return
	}

	targetIDs := make([]int64, 0, len(pins))
	seen := make(map[int64]struct{}, len(pins))
	for _, pin := range pins {
		if _, ok := seen[pin.TargetAccountID]; ok {
			continue
		}
		seen[pin.TargetAccountID] = struct{}{}
		targetIDs = append(targetIDs, pin.TargetAccountID)
	}

	serialized := map[int64]Account{}
	if len(targetIDs) > 0 {
		accounts, err := h.queries.GetAccountsByIDs(r.Context(), targetIDs)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to list endorsements")
			return
		}
		serialized = serializeAccounts(r.Context(), h.queries, h.cfg, accounts, true)
	}

	resp := make([]Account, 0, len(pins))
	for _, pin := range pins {
		if apiAcc, ok := serialized[pin.TargetAccountID]; ok {
			resp = append(resp, apiAcc)
			continue
		}
		resp = append(resp, accountFromPinRow(pin))
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

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Endorsements unavailable")
		return
	}

	_, err = h.queries.CreateAccountPin(r.Context(), db.CreateAccountPinParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
	})
	if err != nil {
		// Ignore duplicates
		_ = err
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

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Endorsements unavailable")
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
	mux.HandleFunc("POST /api/v1/accounts/{id}/endorse", h.Pin)
	mux.HandleFunc("POST /api/v1/accounts/{id}/unpin", h.Unpin)
	mux.HandleFunc("POST /api/v1/accounts/{id}/unendorse", h.Unpin)
}

func boolPtr(b bool) *bool {
	return &b
}

func accountFromPinRow(pin db.ListAccountPinsRow) Account {
	acc := Account{
		ID:           strconv.FormatInt(pin.TargetAccountID, 10),
		Username:     pin.Username,
		Acct:         pin.Username,
		DisplayName:  pin.DisplayName,
		Note:         pin.Note,
		URL:          pin.Url.String,
		URI:          pin.Uri,
		Avatar:       pin.AvatarRemoteUrl.String,
		AvatarStatic: pin.AvatarRemoteUrl.String,
		Header:       pin.HeaderRemoteUrl,
		HeaderStatic: pin.HeaderRemoteUrl,
		Locked:       false,
		Emojis:       []CustomEmoji{},
		Fields:       []Field{},
		Bot:          false,
		Group:        false,
		Discoverable: boolPtr(true),
	}
	if pin.Domain.Valid && pin.Domain.String != "" {
		acc.Acct = pin.Username + "@" + pin.Domain.String
	}
	if pin.AvatarFileName.Valid {
		acc.Avatar = "/files/" + pin.AvatarFileName.String
		acc.AvatarStatic = "/files/" + pin.AvatarFileName.String
	}
	if pin.HeaderFileName.Valid {
		acc.Header = "/files/" + pin.HeaderFileName.String
		acc.HeaderStatic = "/files/" + pin.HeaderFileName.String
	}
	return acc
}

package activitypub

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const quoteStateAccepted = 1

type quoteAuthorization struct {
	activitypub.Object
	InteractingObject any `json:"interactingObject,omitempty"`
	InteractionTarget any `json:"interactionTarget,omitempty"`
}

// GetQuoteAuthorization handles GET /users/:username/quote_authorizations/:id
func (h *FederationHandler) GetQuoteAuthorization(w http.ResponseWriter, r *http.Request) {
	if !h.useDB() {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	account, err := h.loadAccountFromRequest(r.Context(), r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	quoteID, err := parseQuoteID(r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}
	quote, err := h.store.GetQuote(r.Context(), quoteID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}
	if quote.State != quoteStateAccepted {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}
	if quote.QuotedAccountID.Valid && quote.QuotedAccountID.Int64 != account.ID {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	quotedStatusID, ok := validInt64(quote.QuotedStatusID)
	if !ok {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}
	quotedStatus, err := h.store.GetStatusByID(r.Context(), quotedStatusID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}
	if !statusDistributable(quotedStatus) {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	quoteStatus, err := h.store.GetStatusByID(r.Context(), quote.StatusID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	quotedAccount, err := h.store.GetAccountByID(r.Context(), quotedStatus.AccountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Quote not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Failed to load quote")
		return
	}

	actorURI := actorURIForAccount(h.domain, quotedAccount)
	if actorURI == "" {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	approvalID := quoteApprovalID(h.domain, account.Username, quote)
	if approvalID == "" {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	interactionTarget := statusURI(h.domain, quotedAccount, quotedStatus)
	if interactionTarget == "" {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	interactingObject := ""
	if author, err := h.store.GetAccountByID(r.Context(), quoteStatus.AccountID); err == nil {
		interactingObject = statusURI(h.domain, author, quoteStatus)
	}
	if interactingObject == "" {
		web.JSONError(w, http.StatusNotFound, "Quote not found")
		return
	}

	resp := quoteAuthorization{
		Object: activitypub.Object{
			Context:      activitypub.QuoteAuthorizationContext(),
			ID:           approvalID,
			Type:         "QuoteAuthorization",
			AttributedTo: actorURI,
		},
		InteractingObject: interactingObject,
		InteractionTarget: interactionTarget,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, resp)
}

func quoteApprovalID(domain, username string, quote db.Quote) string {
	if quote.ApprovalUri.Valid && strings.TrimSpace(quote.ApprovalUri.String) != "" {
		return quote.ApprovalUri.String
	}
	if domain == "" || strings.TrimSpace(username) == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/users/%s/quote_authorizations/%d", domain, username, quote.ID)
}

func validInt64(value pgtype.Int8) (int64, bool) {
	if !value.Valid || value.Int64 <= 0 {
		return 0, false
	}
	return value.Int64, true
}

func parseQuoteID(r *http.Request) (int64, error) {
	if raw := web.Param(r, "id"); raw != "" {
		return strconv.ParseInt(raw, 10, 64)
	}
	if raw := web.Param(r, "quoteID"); raw != "" {
		return strconv.ParseInt(raw, 10, 64)
	}
	return 0, fmt.Errorf("missing id")
}

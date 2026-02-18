package web

import (
	"net/http"

	"github.com/deicod/gotodon/internal/api/middleware"
	webutil "github.com/deicod/gotodon/internal/web"
)

const csrfErrorMessage = "Can't verify CSRF token authenticity."

func requireWebUser(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		webutil.JSONError(w, http.StatusUnprocessableEntity, csrfErrorMessage)
		return 0, 0, false
	}
	accountID, _ := middleware.GetAccountID(r.Context())
	return userID, accountID, true
}

func tokenIDFromContext(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value, ok := r.Context().Value(middleware.ContextKeyTokenID).(int64)
	if !ok {
		webutil.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return 0, false
	}
	return value, true
}

package admin

import (
	"net/http"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// requireAdmin checks if the user has admin or moderator role
func requireAdmin(w http.ResponseWriter, r *http.Request, queries *db.Queries) error {
	ctx := r.Context()

	if queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Admin data unavailable")
		return http.ErrServerClosed
	}

	userID, ok := middleware.GetUserID(ctx)
	if !ok {
		// Try account ID lookup
		accountID, ok := middleware.GetAccountID(ctx)
		if !ok {
			web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
			return http.ErrNoCookie
		}
		user, err := queries.GetUserByAccountID(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusUnauthorized, "User not found")
			return err
		}
		userID = user.ID
	}

	role, err := queries.GetUserRole(ctx, userID)
	if err != nil {
		// Possibly no role assigned
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return err
	}

	if role.Name != "admin" && role.Name != "moderator" {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return http.ErrHandlerTimeout // Generic error to indicate failure
	}

	return nil
}

// RequireAdmin exposes admin authorization for other API packages.
func RequireAdmin(w http.ResponseWriter, r *http.Request, queries *db.Queries) error {
	return requireAdmin(w, r, queries)
}

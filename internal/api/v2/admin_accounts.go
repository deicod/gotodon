// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	adminapi "github.com/deicod/gotodon/internal/api/admin"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// adminAccountsStore is the part of *db.Queries the v2 admin accounts
// handler uses. Tests use stubs.
type adminAccountsStore = adminapi.AccountListStore

// adminAccountsLinkParams are the query parameters that the list's Link
// headers keep: Api::V2::Admin::AccountsController::PAGINATION_PARAMS, plus
// the role_ids[] form of role_ids.
var adminAccountsLinkParams = []string{
	"limit", "origin", "status", "permissions", "username", "by_domain",
	"display_name", "email", "ip", "invited_by", "role_ids[]", "role_ids",
}

// AdminAccountsHandler handles v2 admin account endpoints.
type AdminAccountsHandler struct {
	store adminAccountsStore
	roles adminapi.RoleLookup
	cfg   *config.Config
}

// NewAdminAccountsHandler creates a new admin accounts handler.
func NewAdminAccountsHandler(queries *db.Queries, cfg *config.Config) *AdminAccountsHandler {
	h := &AdminAccountsHandler{roles: adminapi.NewRoleLookup(queries), cfg: cfg}
	// A nil *db.Queries leaves store nil rather than a non-nil interface
	// holding a nil pointer, so List reports the data as unavailable.
	if queries != nil {
		h.store = queries
	}
	return h
}

// List handles GET /api/v2/admin/accounts. Like
// Api::V2::Admin::AccountsController#translated_filter_params, it passes
// the AccountFilter params through unchanged, with permissions=staff
// selecting the roles that can manage reports instead of role_ids. Unlike
// v1, it has no default origin or status. The listing itself is v1's,
// which the v2 controller inherits.
func (h *AdminAccountsHandler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	origin := strings.TrimSpace(query.Get("origin"))
	status := strings.TrimSpace(query.Get("status"))

	if origin != "" && origin != "local" && origin != "remote" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Unknown origin: "+origin)
		return
	}
	if status != "" && !isValidAdminAccountStatus(status) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Unknown status: "+status)
		return
	}

	invitedRaw := query.Get("invited_by")
	invitedBy, invitedErr := strconv.ParseInt(invitedRaw, 10, 64)
	staff := strings.TrimSpace(query.Get("permissions")) == "staff"
	var roleIDs []int64
	if !staff {
		var present, invalid bool
		roleIDs, present, invalid = parseRoleIDs(query)
		if present && invalid {
			web.JSON(w, http.StatusOK, []adminapi.AdminAccount{})
			return
		}
	}
	if invitedRaw != "" && (invitedErr != nil || invitedBy <= 0) {
		web.JSON(w, http.StatusOK, []adminapi.AdminAccount{})
		return
	}

	adminapi.NewAccountList(h.store, h.cfg, "/api/v2/admin/accounts", adminAccountsLinkParams).Serve(w, r, adminapi.AccountFilter{
		Origin:      origin,
		Status:      status,
		ByDomain:    query.Get("by_domain"),
		Username:    query.Get("username"),
		DisplayName: query.Get("display_name"),
		Email:       query.Get("email"),
		IP:          query.Get("ip"),
		InvitedBy:   invitedBy,
		Staff:       staff,
		RoleIDs:     roleIDs,
	})
}

func parseRoleIDs(query url.Values) ([]int64, bool, bool) {
	values := query["role_ids[]"]
	if len(values) == 0 {
		values = query["role_ids"]
	}
	if len(values) == 0 {
		return nil, false, false
	}
	ids := make([]int64, 0, len(values))
	invalid := false
	for _, value := range values {
		if value == "" {
			invalid = true
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			invalid = true
			continue
		}
		ids = append(ids, parsed)
	}
	if len(ids) == 0 && invalid {
		return nil, true, true
	}
	return ids, true, false
}

func isValidAdminAccountStatus(status string) bool {
	switch status {
	case "active", "pending", "disabled", "suspended", "silenced", "sensitized":
		return true
	default:
		return false
	}
}

// RegisterRoutes registers admin account routes. Listing accounts needs
// manage_users, as in Mastodon's AccountPolicy#index?, and
// admin:read:accounts, as in Api::V2::Admin::AccountsController, which
// inherits the v1 controller's scopes.
func (h *AdminAccountsHandler) RegisterRoutes(mux adminapi.RouteRegistrar) {
	read := adminapi.WithAccess(h.roles, oauth.ScopeAdminReadAccounts, auth.PermissionManageUsers)

	mux.HandleFunc("GET /api/v2/admin/accounts", read(h.List))
}

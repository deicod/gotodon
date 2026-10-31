// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	adminapi "github.com/deicod/gotodon/internal/api/admin"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

const (
	adminAccountsDefaultLimit = 100
	adminAccountsMaxLimit     = 200
)

// adminAccountsStore is the part of *db.Queries the v2 admin accounts
// handler uses. Tests use stubs.
type adminAccountsStore interface {
	AdminListAccountsV2(ctx context.Context, arg db.AdminListAccountsV2Params) ([]db.AdminListAccountsV2Row, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	AdminListUserIPs(ctx context.Context, userIds []int64) ([]db.AdminListUserIPsRow, error)
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

// AdminIP represents an IP entry in admin account responses.
type AdminIP struct {
	IP     string    `json:"ip"`
	UsedAt time.Time `json:"used_at"`
}

// AdminAccountV2 represents an admin account response entry.
type AdminAccountV2 struct {
	ID                     string     `json:"id"`
	Username               string     `json:"username"`
	Domain                 *string    `json:"domain"`
	CreatedAt              time.Time  `json:"created_at"`
	Email                  *string    `json:"email"`
	IP                     *string    `json:"ip"`
	Confirmed              *bool      `json:"confirmed"`
	Suspended              bool       `json:"suspended"`
	Silenced               bool       `json:"silenced"`
	Sensitized             bool       `json:"sensitized"`
	Disabled               *bool      `json:"disabled"`
	Approved               *bool      `json:"approved"`
	Locale                 *string    `json:"locale"`
	InviteRequest          *string    `json:"invite_request"`
	CreatedByApplicationID *string    `json:"created_by_application_id,omitempty"`
	InvitedByAccountID     *string    `json:"invited_by_account_id,omitempty"`
	Account                v1.Account `json:"account"`
	// Role is the user's role, or the everyone role for a user without
	// one, as REST::Admin::AccountSerializer#role gives it. It is null for
	// an account without a user.
	Role *v1.AccountRole `json:"role"`
	Ips  []AdminIP       `json:"ips"`
}

type adminAccountStats struct {
	followersCount pgtype.Int8
	followingCount pgtype.Int8
	statusesCount  pgtype.Int8
	lastStatusAt   pgtype.Timestamptz
}

// List handles GET /api/v2/admin/accounts.
func (h *AdminAccountsHandler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	origin := strings.TrimSpace(query.Get("origin"))
	status := strings.TrimSpace(query.Get("status"))
	permissions := strings.TrimSpace(query.Get("permissions"))
	username := strings.TrimSpace(query.Get("username"))
	byDomain := strings.TrimSpace(query.Get("by_domain"))
	displayName := strings.TrimSpace(query.Get("display_name"))
	email := strings.TrimSpace(query.Get("email"))
	ipFilter := strings.TrimSpace(query.Get("ip"))

	if origin != "" && origin != "local" && origin != "remote" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Unknown origin: "+origin)
		return
	}
	if status != "" && !isValidAdminAccountStatus(status) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Unknown status: "+status)
		return
	}

	if origin == "remote" && byDomain != "" {
		origin = ""
	}
	if username != "" {
		username = strings.TrimPrefix(username, "@")
	}

	limit := parseAdminAccountsLimit(query)
	maxRaw := query.Get("max_id")
	maxID, maxValid := parseOptionalInt64(maxRaw)
	sinceRaw := query.Get("since_id")
	sinceID, sinceValid := parseOptionalInt64(sinceRaw)
	minRaw := query.Get("min_id")
	minID, minValid := parseOptionalInt64(minRaw)
	invitedRaw := query.Get("invited_by")
	invitedBy, invitedValid := parseOptionalInt64(invitedRaw)
	roleIDs, roleIDsPresent, roleIDsInvalid := parseRoleIDs(query)

	if permissions != "staff" {
		permissions = ""
	}
	if permissions == "staff" {
		roleIDs = nil
		roleIDsPresent = false
		roleIDsInvalid = false
	}

	invalidFilter := !maxValid || !sinceValid || !minValid || (!invitedValid && invitedRaw != "")
	if maxRaw != "" && maxValid && maxID <= 0 {
		invalidFilter = true
	}
	if invitedRaw != "" && invitedValid && invitedBy <= 0 {
		invalidFilter = true
	}
	if roleIDsPresent && roleIDsInvalid {
		invalidFilter = true
	}
	if ipFilter != "" && !validIPFilter(ipFilter) {
		invalidFilter = true
	}

	if invalidFilter {
		web.JSON(w, http.StatusOK, []AdminAccountV2{})
		return
	}
	if h.store == nil {
		web.JSONError(w, http.StatusInternalServerError, "Admin data unavailable")
		return
	}

	params := db.AdminListAccountsV2Params{
		Limit:       limit,
		Origin:      origin,
		Status:      status,
		ByDomain:    byDomain,
		Username:    username,
		DisplayName: displayName,
		Email:       email,
		Ip:          ipFilter,
		InvitedBy:   invitedBy,
		Permissions: permissions,
		RoleIds:     roleIDs,
		MaxID:       maxID,
		SinceID:     sinceID,
		MinID:       minID,
	}

	rows, err := h.store.AdminListAccountsV2(r.Context(), params)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	if len(rows) == 0 {
		web.JSON(w, http.StatusOK, []AdminAccountV2{})
		return
	}

	accountIDs := make([]int64, 0, len(rows))
	statsByAccount := make(map[int64]adminAccountStats, len(rows))
	userIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		accountIDs = append(accountIDs, row.ID)
		if row.UserID.Valid {
			userIDs = append(userIDs, row.UserID.Int64)
		}
		statsByAccount[row.ID] = adminAccountStats{
			followersCount: row.FollowersCount,
			followingCount: row.FollowingCount,
			statusesCount:  row.StatusesCount,
			lastStatusAt:   row.LastStatusAt,
		}
	}

	accounts, err := h.store.GetAccountsByIDs(r.Context(), accountIDs)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	accountByID := make(map[int64]db.Account, len(accounts))
	for _, acc := range accounts {
		accountByID[acc.ID] = acc
	}

	ipsByUserID := map[int64][]AdminIP{}
	if len(userIDs) > 0 {
		ipRows, err := h.store.AdminListUserIPs(r.Context(), userIDs)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		for _, ipRow := range ipRows {
			if ipRow.Ip == nil || !ipRow.UsedAt.Valid {
				continue
			}
			ip := ipRow.Ip.String()
			ipsByUserID[ipRow.UserID] = append(ipsByUserID[ipRow.UserID], AdminIP{
				IP:     ip,
				UsedAt: ipRow.UsedAt.Time,
			})
		}
		for userID, ips := range ipsByUserID {
			sort.Slice(ips, func(i, j int) bool {
				return ips[i].UsedAt.After(ips[j].UsedAt)
			})
			ipsByUserID[userID] = ips
		}
	}

	results := make([]AdminAccountV2, 0, len(rows))
	for _, row := range rows {
		acc, ok := accountByID[row.ID]
		if !ok {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		apiAccount := v1.SerializeAccount(h.cfg, acc)
		if stats, ok := statsByAccount[row.ID]; ok {
			if stats.followersCount.Valid {
				apiAccount.FollowersCount = stats.followersCount.Int64
			}
			if stats.followingCount.Valid {
				apiAccount.FollowingCount = stats.followingCount.Int64
			}
			if stats.statusesCount.Valid {
				apiAccount.StatusesCount = stats.statusesCount.Int64
			}
			if stats.lastStatusAt.Valid {
				lastStatus := stats.lastStatusAt.Time.Format("2006-01-02")
				apiAccount.LastStatusAt = &lastStatus
			}
		}

		userExists := row.UserID.Valid

		result := AdminAccountV2{
			ID:         strconv.FormatInt(row.ID, 10),
			Username:   row.Username,
			CreatedAt:  row.CreatedAt.Time,
			Suspended:  row.SuspendedAt.Valid,
			Silenced:   row.SilencedAt.Valid,
			Sensitized: row.SensitizedAt.Valid,
			Account:    apiAccount,
		}

		if row.Domain.Valid {
			domain := row.Domain.String
			result.Domain = &domain
		}
		if row.Email.Valid {
			emailValue := row.Email.String
			result.Email = &emailValue
		}
		if row.Locale.Valid {
			localeValue := row.Locale.String
			result.Locale = &localeValue
		}
		if row.InviteRequest.Valid {
			inviteValue := row.InviteRequest.String
			result.InviteRequest = &inviteValue
		}
		if row.CreatedByApplicationID.Valid {
			createdByApp := strconv.FormatInt(row.CreatedByApplicationID.Int64, 10)
			result.CreatedByApplicationID = &createdByApp
		}
		if row.InvitedByAccountID.Valid {
			invitedBy := strconv.FormatInt(row.InvitedByAccountID.Int64, 10)
			result.InvitedByAccountID = &invitedBy
		}

		if userExists {
			confirmed := row.ConfirmedAt.Valid
			result.Confirmed = boolPointer(confirmed)
			if row.Approved.Valid {
				result.Approved = boolPointer(row.Approved.Bool)
			} else {
				result.Approved = boolPointer(false)
			}
			if row.Disabled.Valid {
				result.Disabled = boolPointer(row.Disabled.Bool)
			} else {
				result.Disabled = boolPointer(false)
			}
		}

		if userExists {
			role := v1.SerializeUserRole(row.RoleID, row.RoleName, row.RoleColor, row.RolePermissions, row.RoleHighlighted)
			result.Role = &role
		}

		if userExists {
			ips := ipsByUserID[row.UserID.Int64]
			if ips == nil {
				ips = []AdminIP{}
			}
			result.Ips = ips
			if len(ips) > 0 {
				ip := ips[0].IP
				result.IP = &ip
			}
		}

		results = append(results, result)
	}

	h.setPaginationHeaders(w, r, rows, limit)
	web.JSON(w, http.StatusOK, results)
}

func (h *AdminAccountsHandler) setPaginationHeaders(w http.ResponseWriter, r *http.Request, rows []db.AdminListAccountsV2Row, limit int32) {
	if len(rows) == 0 {
		return
	}
	params := paginationParams(r)
	links := make([]string, 0, 2)
	if limit > 0 && len(rows) == int(limit) {
		nextParams := cloneValues(params)
		nextParams.Set("max_id", strconv.FormatInt(rows[len(rows)-1].ID, 10))
		links = append(links, "<"+h.adminAccountsURL(r, nextParams)+">; rel=\"next\"")
	}
	prevParams := cloneValues(params)
	prevParams.Set("min_id", strconv.FormatInt(rows[0].ID, 10))
	links = append(links, "<"+h.adminAccountsURL(r, prevParams)+">; rel=\"prev\"")
	if len(links) > 0 {
		w.Header().Set("Link", strings.Join(links, ", "))
	}
}

func paginationParams(r *http.Request) url.Values {
	allowed := map[string]struct{}{
		"limit":        {},
		"origin":       {},
		"status":       {},
		"permissions":  {},
		"username":     {},
		"by_domain":    {},
		"display_name": {},
		"email":        {},
		"ip":           {},
		"invited_by":   {},
		"role_ids[]":   {},
		"role_ids":     {},
	}
	values := url.Values{}
	for key, vals := range r.URL.Query() {
		if _, ok := allowed[key]; !ok {
			continue
		}
		for _, v := range vals {
			values.Add(key, v)
		}
	}
	return values
}

func cloneValues(src url.Values) url.Values {
	dst := url.Values{}
	for key, vals := range src {
		copied := append([]string(nil), vals...)
		dst[key] = copied
	}
	return dst
}

func (h *AdminAccountsHandler) adminAccountsURL(r *http.Request, params url.Values) string {
	scheme, host := h.baseURL(r)
	u := url.URL{
		Scheme:   scheme,
		Host:     host,
		Path:     "/api/v2/admin/accounts",
		RawQuery: params.Encode(),
	}
	return u.String()
}

func (h *AdminAccountsHandler) baseURL(r *http.Request) (string, string) {
	if h.cfg != nil && h.cfg.Instance.LocalDomain != "" {
		return "https", h.cfg.Instance.LocalDomain
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme, host
}

func parseAdminAccountsLimit(query url.Values) int32 {
	limitStr := query.Get("limit")
	if limitStr == "" {
		return adminAccountsDefaultLimit
	}
	parsed, err := strconv.Atoi(limitStr)
	if err != nil {
		parsed = 0
	}
	if parsed < 0 {
		parsed = -parsed
	}
	if parsed > adminAccountsMaxLimit {
		parsed = adminAccountsMaxLimit
	}
	return int32(parsed)
}

func parseOptionalInt64(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	val, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return val, true
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

func validIPFilter(value string) bool {
	if strings.Contains(value, "/") {
		_, _, err := net.ParseCIDR(value)
		return err == nil
	}
	return net.ParseIP(value) != nil
}

func isValidAdminAccountStatus(status string) bool {
	switch status {
	case "active", "pending", "disabled", "suspended", "silenced", "sensitized":
		return true
	default:
		return false
	}
}

func boolPointer(value bool) *bool {
	v := value
	return &v
}

// RegisterRoutes registers admin account routes. Listing accounts needs
// manage_users, as in Mastodon's AccountPolicy#index?, and
// admin:read:accounts, as in Api::V2::Admin::AccountsController, which
// inherits the v1 controller's scopes.
func (h *AdminAccountsHandler) RegisterRoutes(mux adminapi.RouteRegistrar) {
	read := adminapi.WithAccess(h.roles, oauth.ScopeAdminReadAccounts, auth.PermissionManageUsers)

	mux.HandleFunc("GET /api/v2/admin/accounts", read(h.List))
}

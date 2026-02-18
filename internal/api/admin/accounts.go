// Package admin provides Mastodon-compatible Admin API handlers.
package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// AccountsHandler handles admin account endpoints
type AccountsHandler struct {
	queries *db.Queries
}

// NewAccountsHandler creates a new admin accounts handler
func NewAccountsHandler(queries *db.Queries) *AccountsHandler {
	return &AccountsHandler{
		queries: queries,
	}
}

// AdminAccount represents an account in the admin view
type AdminAccount struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Domain         *string   `json:"domain"`
	CreatedAt      time.Time `json:"created_at"`
	Email          string    `json:"email,omitempty"`
	IP             *string   `json:"ip,omitempty"`
	Role           *Role     `json:"role,omitempty"`
	Confirmed      bool      `json:"confirmed"`
	Approved       bool      `json:"approved"`
	Disabled       bool      `json:"disabled"`
	Silenced       bool      `json:"silenced"`
	Suspended      bool      `json:"suspended"`
	Sensitized     bool      `json:"sensitized"`
	InviteRequest  *string   `json:"invite_request,omitempty"`
	CreatedByAppID *string   `json:"created_by_application_id,omitempty"`
	InvitedByID    *string   `json:"invited_by_account_id,omitempty"`
}

// Role represents an admin role
type Role struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Position    int    `json:"position"`
	Permissions int64  `json:"permissions"`
	Highlighted bool   `json:"highlighted"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// AccountAction represents an action taken on an account.
type AccountAction struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	TargetID  string    `json:"target_account_id"`
	Action    string    `json:"action"`
	Text      string    `json:"text,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	SendEmail bool      `json:"send_email_notification"`
}

const (
	adminActionTargetAccount = "Account"
	adminActionTargetUser    = "User"
)

func accountHumanIdentifier(username string, domain pgtype.Text) string {
	if strings.TrimSpace(username) == "" {
		return ""
	}
	if domain.Valid && strings.TrimSpace(domain.String) != "" {
		return username + "@" + domain.String
	}
	return username
}

func roleFromRow(roleID pgtype.Int8, roleName pgtype.Text, roleColor pgtype.Text, rolePosition pgtype.Int4, rolePermissions pgtype.Int8, roleHighlighted pgtype.Bool, roleCreatedAt pgtype.Timestamptz, roleUpdatedAt pgtype.Timestamptz) *Role {
	if !roleID.Valid {
		return nil
	}

	role := Role{
		ID:          int(roleID.Int64),
		Name:        roleName.String,
		Color:       roleColor.String,
		Position:    int(rolePosition.Int32),
		Permissions: rolePermissions.Int64,
		Highlighted: roleHighlighted.Bool,
	}
	if roleCreatedAt.Valid {
		role.CreatedAt = roleCreatedAt.Time.Format(time.RFC3339)
	}
	if roleUpdatedAt.Valid {
		role.UpdatedAt = roleUpdatedAt.Time.Format(time.RFC3339)
	}
	return &role
}

func (h *AccountsHandler) accountIdentifier(ctx context.Context, accountID int64) string {
	if h.queries == nil {
		return strconv.FormatInt(accountID, 10)
	}
	account, err := h.queries.GetAccountByIDAllowSuspended(ctx, accountID)
	if err != nil {
		return strconv.FormatInt(accountID, 10)
	}
	identifier := accountHumanIdentifier(account.Username, account.Domain)
	if identifier == "" {
		return strconv.FormatInt(accountID, 10)
	}
	return identifier
}

func (h *AccountsHandler) logAdminAction(ctx context.Context, adminID int64, action string, targetType string, targetID int64, humanIdentifier string, routeParam string, permalink string) {
	if h.queries == nil || adminID <= 0 || action == "" || targetType == "" || targetID <= 0 {
		return
	}
	_ = h.queries.CreateAdminActionLog(ctx, db.CreateAdminActionLogParams{
		AccountID:  adminID,
		Action:     action,
		TargetType: pgtype.Text{String: targetType, Valid: true},
		TargetID:   pgtype.Int8{Int64: targetID, Valid: true},
		HumanIdentifier: pgtype.Text{
			String: humanIdentifier,
			Valid:  strings.TrimSpace(humanIdentifier) != "",
		},
		RouteParam: pgtype.Text{
			String: routeParam,
			Valid:  strings.TrimSpace(routeParam) != "",
		},
		Permalink: pgtype.Text{
			String: permalink,
			Valid:  strings.TrimSpace(permalink) != "",
		},
	})
}

func (h *AccountsHandler) logAccountAction(ctx context.Context, adminID, targetAccountID int64, action string) {
	h.logAdminAction(ctx, adminID, action, adminActionTargetAccount, targetAccountID, h.accountIdentifier(ctx, targetAccountID), "", "")
}

func (h *AccountsHandler) logUserAction(ctx context.Context, adminID, targetAccountID int64, action string) {
	if h.queries == nil {
		return
	}
	user, err := h.queries.GetUserByAccountID(ctx, targetAccountID)
	if err != nil {
		return
	}
	routeParam := strconv.FormatInt(targetAccountID, 10)
	h.logAdminAction(ctx, adminID, action, adminActionTargetUser, user.ID, h.accountIdentifier(ctx, targetAccountID), routeParam, "")
}

// GetAll handles GET /api/v1/admin/accounts
func (h *AccountsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	// Parse query parameters
	limit := 100
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	offset := 0
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	params := db.AdminListAccountsParams{
		Limit:           int32(limit),
		Offset:          int32(offset),
		LocalOnly:       web.QueryParam(r, "local") == "true",
		StatusActive:    web.QueryParam(r, "status") == "active",
		StatusPending:   web.QueryParam(r, "status") == "pending",
		StatusDisabled:  web.QueryParam(r, "status") == "disabled",
		StatusSilenced:  web.QueryParam(r, "status") == "silenced",
		StatusSuspended: web.QueryParam(r, "status") == "suspended",
		UsernameCurr:    web.QueryParam(r, "username"),
		Email:           web.QueryParam(r, "email"),
		Ip:              web.QueryParam(r, "ip"),
	}

	rows, err := h.queries.AdminListAccounts(r.Context(), params)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	accounts := make([]AdminAccount, 0, len(rows))
	for _, row := range rows {
		acc := AdminAccount{
			ID:         strconv.FormatInt(row.ID, 10),
			Username:   row.Username,
			CreatedAt:  row.CreatedAt.Time,
			Email:      row.Email,
			Confirmed:  row.ConfirmedAt.Valid,
			Approved:   row.Approved,
			Disabled:   row.Disabled,
			Silenced:   row.SilencedAt.Valid,
			Suspended:  row.SuspendedAt.Valid,
			Sensitized: row.SensitizedAt.Valid,
			Role: roleFromRow(
				row.RoleID,
				row.RoleName,
				row.RoleColor,
				row.RolePosition,
				row.RolePermissions,
				row.RoleHighlighted,
				row.RoleCreatedAt,
				row.RoleUpdatedAt,
			),
		}
		if row.Domain.Valid {
			d := row.Domain.String
			acc.Domain = &d
		}

		accounts = append(accounts, acc)
	}

	web.JSON(w, http.StatusOK, accounts)
}

// Get handles GET /api/v1/admin/accounts/:id
func (h *AccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	row, err := h.queries.AdminGetAccount(r.Context(), accountID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	acc := AdminAccount{
		ID:         strconv.FormatInt(row.ID, 10),
		Username:   row.Username,
		CreatedAt:  row.CreatedAt.Time,
		Email:      row.Email,
		Confirmed:  row.ConfirmedAt.Valid,
		Approved:   row.Approved,
		Disabled:   row.Disabled,
		Silenced:   row.SilencedAt.Valid,
		Suspended:  row.SuspendedAt.Valid,
		Sensitized: row.SensitizedAt.Valid,
		Role: roleFromRow(
			row.RoleID,
			row.RoleName,
			row.RoleColor,
			row.RolePosition,
			row.RolePermissions,
			row.RoleHighlighted,
			row.RoleCreatedAt,
			row.RoleUpdatedAt,
		),
	}
	if row.Domain.Valid {
		d := row.Domain.String
		acc.Domain = &d
	}
	if row.SignUpIp != nil {
		ip := row.SignUpIp.String()
		acc.IP = &ip
	}

	web.JSON(w, http.StatusOK, acc)
}

// TakeAction handles POST /api/v1/admin/accounts/:id/action
func (h *AccountsHandler) TakeAction(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		Type                  string `json:"type" form:"type"`
		Text                  string `json:"text" form:"text"`
		ReportID              string `json:"report_id" form:"report_id"`
		WarningPresetID       string `json:"warning_preset_id" form:"warning_preset_id"`
		SendEmailNotification bool   `json:"send_email_notification" form:"send_email_notification"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	ctx := r.Context()
	adminID, _ := middleware.GetAccountID(ctx)
	var logFn func()

	switch req.Type {
	case "disable":
		err = h.queries.AdminDisableAccount(ctx, accountID)
		logFn = func() { h.logUserAction(ctx, adminID, accountID, "disable") }
	case "sensitive":
		err = h.queries.AdminSensitizeAccount(ctx, accountID)
		logFn = func() { h.logAccountAction(ctx, adminID, accountID, "sensitive") }
	case "silence":
		err = h.queries.AdminSilenceAccount(ctx, accountID)
		logFn = func() { h.logAccountAction(ctx, adminID, accountID, "silence") }
	case "suspend":
		err = h.queries.AdminSuspendAccount(ctx, accountID)
		logFn = func() { h.logAccountAction(ctx, adminID, accountID, "suspend") }
	case "none":
		// Do nothing, just log action?
	default:
		web.JSONError(w, http.StatusUnprocessableEntity, "Invalid action type")
		return
	}

	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if logFn != nil {
		logFn()
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// Approve handles POST /api/v1/admin/accounts/:id/approve
func (h *AccountsHandler) Approve(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Invalid ID")
		return
	}

	if err := h.queries.AdminApproveAccount(r.Context(), accountID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logUserAction(r.Context(), adminID, accountID, "approve")
	}

	// Fetch updated
	h.Get(w, r)
}

// Reject handles POST /api/v1/admin/accounts/:id/reject
func (h *AccountsHandler) Reject(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Invalid ID")
		return
	}

	if err := h.queries.AdminRejectAccount(r.Context(), accountID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logUserAction(r.Context(), adminID, accountID, "reject")
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// Enable handles POST /api/v1/admin/accounts/:id/enable
func (h *AccountsHandler) Enable(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Invalid ID")
		return
	}

	if err := h.queries.AdminEnableAccount(r.Context(), accountID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logUserAction(r.Context(), adminID, accountID, "enable")
	}

	h.Get(w, r)
}

// Unsilence handles POST /api/v1/admin/accounts/:id/unsilence
func (h *AccountsHandler) Unsilence(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Invalid ID")
		return
	}

	if err := h.queries.AdminUnsilenceAccount(r.Context(), accountID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logAccountAction(r.Context(), adminID, accountID, "unsilence")
	}

	h.Get(w, r)
}

// Unsuspend handles POST /api/v1/admin/accounts/:id/unsuspend
func (h *AccountsHandler) Unsuspend(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Invalid ID")
		return
	}

	if err := h.queries.AdminUnsuspendAccount(r.Context(), accountID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logAccountAction(r.Context(), adminID, accountID, "unsuspend")
	}

	h.Get(w, r)
}

// Unsensitize handles POST /api/v1/admin/accounts/:id/unsensitize
func (h *AccountsHandler) Unsensitize(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Invalid ID")
		return
	}

	if err := h.queries.AdminUnsensitizeAccount(r.Context(), accountID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logAccountAction(r.Context(), adminID, accountID, "unsensitive")
	}

	h.Get(w, r)
}

// Delete handles DELETE /api/v1/admin/accounts/:id
func (h *AccountsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if _, err := h.queries.AdminGetAccount(r.Context(), accountID); err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if err := h.queries.AdminDeleteAccount(r.Context(), accountID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers admin account routes
func (h *AccountsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/accounts", h.GetAll)
	mux.HandleFunc("GET /api/v1/admin/accounts/{id}", h.Get)
	mux.HandleFunc("DELETE /api/v1/admin/accounts/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/action", h.TakeAction)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/approve", h.Approve)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/reject", h.Reject)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/enable", h.Enable)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsilence", h.Unsilence)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsuspend", h.Unsuspend)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsensitive", h.Unsensitize)
}

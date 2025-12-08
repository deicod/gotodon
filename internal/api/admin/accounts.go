// Package admin provides Mastodon-compatible Admin API handlers.
package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

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

// AccountAction represents an action taken on an account
// TODO: Store this in a proper admin_actions or audit log table
type AccountAction struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	TargetID  string    `json:"target_account_id"`
	Action    string    `json:"action"`
	Text      string    `json:"text,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	SendEmail bool      `json:"send_email_notification"`
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
		}
		if row.Domain.Valid {
			d := row.Domain.String
			acc.Domain = &d
		}

		// Map Role (Stub)
		// acc.Role = ...

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
	}
	if row.Domain.Valid {
		d := row.Domain.String
		acc.Domain = &d
	}
	if row.SignUpIp != nil {
		ip := row.SignUpIp.String()
		acc.IP = &ip
	}

	// Roles logic left as TODO or stub
	acc.Role = &Role{
		ID: 3, Name: "User", Position: 0, Permissions: 0,
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

	switch req.Type {
	case "disable":
		err = h.queries.AdminDisableAccount(ctx, accountID)
	case "sensitive":
		err = h.queries.AdminSensitizeAccount(ctx, accountID)
	case "silence":
		err = h.queries.AdminSilenceAccount(ctx, accountID)
	case "suspend":
		err = h.queries.AdminSuspendAccount(ctx, accountID)
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

	// Audit Log
	fmt.Printf("Admin Action: %s on account %d details: %s\n", req.Type, accountID, req.Text)

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

	h.Get(w, r)
}

// RegisterRoutes registers admin account routes
func (h *AccountsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/accounts", h.GetAll)
	mux.HandleFunc("GET /api/v1/admin/accounts/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/action", h.TakeAction)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/approve", h.Approve)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/reject", h.Reject)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/enable", h.Enable)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsilence", h.Unsilence)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsuspend", h.Unsuspend)
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsensitize", h.Unsensitize)
}

// Package admin provides Mastodon-compatible Admin API handlers.
package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/oauth"
)

// accountsStore is the part of *db.Queries the admin accounts handler uses.
// Tests use stubs.
type accountsStore interface {
	AdminListAccounts(ctx context.Context, arg db.AdminListAccountsParams) ([]db.AdminListAccountsRow, error)
	AdminGetAccount(ctx context.Context, id int64) (db.AdminGetAccountRow, error)
	AdminGetAccountActionTarget(ctx context.Context, id int64) (db.AdminGetAccountActionTargetRow, error)
	AdminApproveAccount(ctx context.Context, accountID int64) error
	AdminRejectAccount(ctx context.Context, accountID int64) error
	AdminDeleteAccount(ctx context.Context, id int64) error
	AdminDisableAccount(ctx context.Context, accountID int64) error
	AdminEnableAccount(ctx context.Context, accountID int64) error
	AdminSilenceAccount(ctx context.Context, id int64) error
	AdminUnsilenceAccount(ctx context.Context, id int64) error
	AdminSuspendAccount(ctx context.Context, id int64) error
	AdminUnsuspendAccount(ctx context.Context, id int64) error
	AdminSensitizeAccount(ctx context.Context, id int64) error
	AdminUnsensitizeAccount(ctx context.Context, id int64) error
	GetAccountByIDAllowSuspended(ctx context.Context, id int64) (db.Account, error)
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	CreateAdminActionLog(ctx context.Context, arg db.CreateAdminActionLogParams) error
}

// AccountsHandler handles admin account endpoints
type AccountsHandler struct {
	store accountsStore
	roles RoleLookup
	// instanceActorURI identifies the instance actor, which can't be
	// suspended. It is "" without a local domain, and then matches no
	// account.
	instanceActorURI string
}

// NewAccountsHandler creates a new admin accounts handler for the server at
// localDomain, whose instance actor it recognizes.
func NewAccountsHandler(queries *db.Queries, localDomain string) *AccountsHandler {
	h := &AccountsHandler{
		roles:            NewRoleLookup(queries),
		instanceActorURI: activitypub.InstanceActorURI(localDomain),
	}
	// A nil *db.Queries leaves store nil rather than a non-nil interface
	// holding a nil pointer. The routes are unreachable then: without a
	// role lookup, WithAccess answers 500.
	if queries != nil {
		h.store = queries
	}
	return h
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
	if h.store == nil {
		return strconv.FormatInt(accountID, 10)
	}
	account, err := h.store.GetAccountByIDAllowSuspended(ctx, accountID)
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
	if h.store == nil || adminID <= 0 || action == "" || targetType == "" || targetID <= 0 {
		return
	}
	_ = h.store.CreateAdminActionLog(ctx, db.CreateAdminActionLogParams{
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
	if h.store == nil {
		return
	}
	user, err := h.store.GetUserByAccountID(ctx, targetAccountID)
	if err != nil {
		return
	}
	routeParam := strconv.FormatInt(targetAccountID, 10)
	h.logAdminAction(ctx, adminID, action, adminActionTargetUser, user.ID, h.accountIdentifier(ctx, targetAccountID), routeParam, "")
}

// GetAll handles GET /api/v1/admin/accounts
func (h *AccountsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
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

	rows, err := h.store.AdminListAccounts(r.Context(), params)
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
	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	row, err := h.store.AdminGetAccount(r.Context(), accountID)
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

// TakeAction handles POST /api/v1/admin/accounts/:id/action. Like
// Admin::AccountAction, it refuses with 403 an action that Mastodon's
// policies refuse for the target; see accountActionAllowed.
func (h *AccountsHandler) TakeAction(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok {
		return
	}
	accountID := target.ID

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
	var apply func(context.Context, int64) error
	var logFn func()

	switch req.Type {
	case "disable":
		apply = h.store.AdminDisableAccount
		logFn = func() { h.logUserAction(ctx, adminID, accountID, "disable") }
	case "sensitive":
		apply = h.store.AdminSensitizeAccount
		logFn = func() { h.logAccountAction(ctx, adminID, accountID, "sensitive") }
	case "silence":
		apply = h.store.AdminSilenceAccount
		logFn = func() { h.logAccountAction(ctx, adminID, accountID, "silence") }
	case "suspend":
		apply = h.store.AdminSuspendAccount
		logFn = func() { h.logAccountAction(ctx, adminID, accountID, "suspend") }
	case "none":
		// Changes no account state.
	default:
		web.JSONError(w, http.StatusUnprocessableEntity, "Invalid action type")
		return
	}

	acting, ok := ActingRole(ctx)
	if !ok {
		slog.ErrorContext(ctx, "Admin account action without an acting role", "account_id", accountID)
		web.JSONError(w, http.StatusInternalServerError, "Admin data unavailable")
		return
	}
	if !h.accountActionAllowed(acting, req.Type, target) {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	if apply != nil {
		if err := apply(ctx, accountID); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	}

	if logFn != nil {
		logFn()
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// accountActionAllowed reports whether acting may take an action of
// actionType against target, as Admin::AccountAction authorizes it:
//
//   - every type, none included, needs acting to override the role of the
//     target's user (AccountPolicy#warn?, which process_strike! checks);
//   - disable needs a user to disable. Mastodon authorizes
//     UserPolicy#disable? on a nil user there and fails with a 500;
//     gotodon refuses instead;
//   - suspend can't target the instance actor (AccountPolicy#suspend?).
//
// The type-specific policies, UserPolicy#disable? and AccountPolicy's
// sensitive?, silence? and suspend?, compare the same roles as warn?.
func (h *AccountsHandler) accountActionAllowed(acting db.UserRole, actionType string, target db.AdminGetAccountActionTargetRow) bool {
	if !overridesRole(acting, userRolePosition(target)) {
		return false
	}
	switch actionType {
	case "disable":
		return target.UserID.Valid
	case "suspend":
		return !h.isInstanceActor(target)
	}
	return true
}

// userRolePosition is the position of Mastodon's Account#user_role for
// target: nil for an account without a user, and the everyone role's
// position for a user without a role.
func userRolePosition(target db.AdminGetAccountActionTargetRow) *int32 {
	if !target.UserID.Valid {
		return nil
	}
	position := everyonePosition
	if target.RolePosition.Valid {
		position = target.RolePosition.Int32
	}
	return &position
}

// isInstanceActor reports whether target is this server's instance actor:
// the local account with the instance actor's URI. Mastodon recognizes it
// by its fixed ID, -99; gotodon creates it with an ordinary one.
func (h *AccountsHandler) isInstanceActor(target db.AdminGetAccountActionTargetRow) bool {
	return h.instanceActorURI != "" && !target.Domain.Valid && target.Uri == h.instanceActorURI
}

// actionTarget looks up the account a write route names, as Mastodon's
// set_account does before authorizing: an ID that isn't a number or names
// no account gets 404 Record not found, and a failed lookup 500.
func (h *AccountsHandler) actionTarget(w http.ResponseWriter, r *http.Request) (db.AdminGetAccountActionTargetRow, bool) {
	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return db.AdminGetAccountActionTargetRow{}, false
	}
	target, err := h.store.AdminGetAccountActionTarget(r.Context(), accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return db.AdminGetAccountActionTargetRow{}, false
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "Admin account lookup failed", "account_id", accountID, "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return db.AdminGetAccountActionTargetRow{}, false
	}
	return target, true
}

// Approve handles POST /api/v1/admin/accounts/:id/approve. It needs a local
// account with a user who isn't approved yet (UserPolicy#approve?).
func (h *AccountsHandler) Approve(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok || !requireLocalUser(w, target) {
		return
	}
	if target.UserApproved.Bool {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	if err := h.store.AdminApproveAccount(r.Context(), target.ID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logUserAction(r.Context(), adminID, target.ID, "approve")
	}

	// Fetch updated
	h.Get(w, r)
}

// Reject handles POST /api/v1/admin/accounts/:id/reject. It needs a local
// account with a user who isn't approved yet (UserPolicy#reject?).
func (h *AccountsHandler) Reject(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok || !requireLocalUser(w, target) {
		return
	}
	if target.UserApproved.Bool {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	if err := h.store.AdminRejectAccount(r.Context(), target.ID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logUserAction(r.Context(), adminID, target.ID, "reject")
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// Enable handles POST /api/v1/admin/accounts/:id/enable. It needs a local
// account with a user.
func (h *AccountsHandler) Enable(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok || !requireLocalUser(w, target) {
		return
	}

	if err := h.store.AdminEnableAccount(r.Context(), target.ID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logUserAction(r.Context(), adminID, target.ID, "enable")
	}

	h.Get(w, r)
}

// requireLocalUser is Api::V1::Admin::AccountsController's
// require_local_account!: enable, approve and reject need a local account
// with a user. Otherwise it answers as Mastodon's forbidden does, with the
// generic 403 body Forbidden rather than a policy's This action is not
// allowed.
func requireLocalUser(w http.ResponseWriter, target db.AdminGetAccountActionTargetRow) bool {
	if target.Domain.Valid || !target.UserID.Valid {
		web.JSONError(w, http.StatusForbidden, "Forbidden")
		return false
	}
	return true
}

// Unsilence handles POST /api/v1/admin/accounts/:id/unsilence
func (h *AccountsHandler) Unsilence(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok {
		return
	}

	if err := h.store.AdminUnsilenceAccount(r.Context(), target.ID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logAccountAction(r.Context(), adminID, target.ID, "unsilence")
	}

	h.Get(w, r)
}

// Unsuspend handles POST /api/v1/admin/accounts/:id/unsuspend. It only lifts
// a suspension made on this server (AccountPolicy#unsuspend?), so an account
// that its own server suspended, or that isn't suspended, gets 403.
func (h *AccountsHandler) Unsuspend(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok {
		return
	}
	if !suspendedLocally(target) {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	if err := h.store.AdminUnsuspendAccount(r.Context(), target.ID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logAccountAction(r.Context(), adminID, target.ID, "unsuspend")
	}

	h.Get(w, r)
}

// suspensionOriginLocal is Mastodon's suspension_origin enum value :local.
const suspensionOriginLocal = 0

// suspendedLocally is Account#suspension_origin_local?. It reads only the
// origin, which suspending sets and unsuspending clears.
func suspendedLocally(target db.AdminGetAccountActionTargetRow) bool {
	return target.SuspensionOrigin.Valid && target.SuspensionOrigin.Int32 == suspensionOriginLocal
}

// Unsensitize handles POST /api/v1/admin/accounts/:id/unsensitive
func (h *AccountsHandler) Unsensitize(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok {
		return
	}

	if err := h.store.AdminUnsensitizeAccount(r.Context(), target.ID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logAccountAction(r.Context(), adminID, target.ID, "unsensitive")
	}

	h.Get(w, r)
}

// Delete handles DELETE /api/v1/admin/accounts/:id. Like Mastodon's
// AccountPolicy#destroy?, it only deletes an account that is suspended
// temporarily: suspended, with the deletion request that suspending creates.
func (h *AccountsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok {
		return
	}
	if !target.SuspendedAt.Valid || !target.DeletionRequested {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	if err := h.store.AdminDeleteAccount(r.Context(), target.ID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers admin account routes. Permissions follow
// Mastodon's AccountPolicy and UserPolicy. Account actions need manage_users:
// Api::V1::Admin::AccountActionsController authorizes show? first, which
// covers the permission of every per-type check of Admin::AccountAction.
// The handlers check the policies' conditions on the target account. Scopes
// follow Api::V1::Admin::AccountsController and AccountActionsController:
// listing and showing need admin:read:accounts, everything else, account
// actions included, admin:write:accounts.
func (h *AccountsHandler) RegisterRoutes(mux RouteRegistrar) {
	read := WithAccess(h.roles, oauth.ScopeAdminReadAccounts, auth.PermissionManageUsers)
	write := WithAccess(h.roles, oauth.ScopeAdminWriteAccounts, auth.PermissionManageUsers)
	deleteUserData := WithAccess(h.roles, oauth.ScopeAdminWriteAccounts, auth.PermissionDeleteUserData)

	mux.HandleFunc("GET /api/v1/admin/accounts", read(h.GetAll))
	mux.HandleFunc("GET /api/v1/admin/accounts/{id}", read(h.Get))
	mux.HandleFunc("DELETE /api/v1/admin/accounts/{id}", deleteUserData(h.Delete))
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/action", write(h.TakeAction))
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/approve", write(h.Approve))
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/reject", write(h.Reject))
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/enable", write(h.Enable))
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsilence", write(h.Unsilence))
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsuspend", write(h.Unsuspend))
	mux.HandleFunc("POST /api/v1/admin/accounts/{id}/unsensitive", write(h.Unsensitize))
}

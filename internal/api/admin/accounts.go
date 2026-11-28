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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/oauth"
)

// accountsStore is the part of *db.Queries the admin accounts handler uses.
// Tests use stubs.
type accountsStore interface {
	AccountListStore
	AdminGetAccount(ctx context.Context, id int64) (db.AdminGetAccountRow, error)
	AdminGetAccountActionTarget(ctx context.Context, id int64) (db.AdminGetAccountActionTargetRow, error)
	AdminApproveAccount(ctx context.Context, accountID int64) error
	AdminRejectAccount(ctx context.Context, accountID int64) error
	AdminDisableAccount(ctx context.Context, accountID int64) error
	AdminEnableAccount(ctx context.Context, accountID int64) error
	AdminSilenceAccount(ctx context.Context, id int64) error
	AdminUnsilenceAccount(ctx context.Context, id int64) error
	AdminSuspendAccount(ctx context.Context, id int64) error
	AdminUnsuspendAccount(ctx context.Context, id int64) (int64, error)
	AdminSensitizeAccount(ctx context.Context, id int64) error
	AdminUnsensitizeAccount(ctx context.Context, id int64) error
	GetAccountByIDAllowSuspended(ctx context.Context, id int64) (db.Account, error)
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	CreateAdminActionLog(ctx context.Context, arg db.CreateAdminActionLogParams) error
	// What an account action reads and writes after its transaction.
	CreateNotification(ctx context.Context, arg db.CreateNotificationParams) (db.Notification, error)
	AdminListCitedStatuses(ctx context.Context, ids []int64) ([]db.AdminListCitedStatusesRow, error)
}

// AccountsHandler handles admin account endpoints
type AccountsHandler struct {
	store accountsStore
	// actions runs the database work of an account action in one
	// transaction. It is nil without a connection pool, and then account
	// actions answer 500.
	actions accountActionTransactor
	roles   RoleLookup
	cfg     *config.Config
	// instanceActorURI identifies the instance actor, which can't be
	// suspended. It is "" without a local domain, and then matches no
	// account.
	instanceActorURI string

	// mail sends the warning e-mail of account actions, and streamer and
	// pusher deliver their moderation_warning notifications. Each may be
	// nil, which skips that delivery.
	mail     mailer.Deliverer
	streamer NotificationStreamer
	pusher   NotificationPusher

	// deletions runs account deletions in the background. It is nil until
	// set, and then deleting an account answers 500.
	deletions AccountDeletionScheduler
}

// AccountDeletionScheduler starts the deletion of an account's data in the
// background. *workers.AccountDeletionEnqueuer and
// *services.InProcessAccountDeletion satisfy it.
type AccountDeletionScheduler interface {
	ScheduleAccountDeletion(ctx context.Context, accountID int64, opts services.AccountDeletionOptions) error
}

// v1AccountsLinkParams are the query parameters that the v1 list's Link
// headers keep: Api::V1::Admin::AccountsController::PAGINATION_PARAMS.
var v1AccountsLinkParams = []string{
	"limit", "local", "remote", "by_domain", "active", "pending", "disabled",
	"sensitized", "silenced", "suspended", "username", "display_name",
	"email", "ip", "staff",
}

// NewAccountsHandler creates a new admin accounts handler. pool runs the
// transactions of account actions. cfg names the server's local domain,
// whose instance actor the handler recognizes, and may be nil.
func NewAccountsHandler(queries *db.Queries, pool *pgxpool.Pool, cfg *config.Config) *AccountsHandler {
	localDomain := cfg.FederationDomain()
	h := &AccountsHandler{
		roles:            NewRoleLookup(queries),
		cfg:              cfg,
		instanceActorURI: activitypub.InstanceActorURI(localDomain),
	}
	// A nil *db.Queries leaves store nil rather than a non-nil interface
	// holding a nil pointer. The routes are unreachable then: without a
	// role lookup, WithAccess answers 500.
	if queries != nil {
		h.store = queries
		if pool != nil {
			h.actions = poolTransactor{pool: pool, queries: queries}
		}
	}
	return h
}

// SetMailDeliverer enables the warning e-mail of account actions
// (UserMailer.warning).
func (h *AccountsHandler) SetMailDeliverer(deliverer mailer.Deliverer) {
	h.mail = deliverer
}

// SetAccountDeletionScheduler sets what runs account deletions.
func (h *AccountsHandler) SetAccountDeletionScheduler(scheduler AccountDeletionScheduler) {
	h.deletions = scheduler
}

// SetNotificationPublishers enables streaming and Web Push for the
// moderation_warning notifications of account actions. Either may be nil.
func (h *AccountsHandler) SetNotificationPublishers(streamer NotificationStreamer, pusher NotificationPusher) {
	h.streamer = streamer
	h.pusher = pusher
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

func (h *AccountsHandler) accountIdentifier(ctx context.Context, accountID int64) string {
	if h.store == nil {
		return strconv.FormatInt(accountID, 10)
	}
	return accountIdentifierFrom(ctx, h.store, accountID)
}

// accountLookup finds an account whether or not it is suspended.
type accountLookup interface {
	GetAccountByIDAllowSuspended(ctx context.Context, id int64) (db.Account, error)
}

// accountIdentifierFrom is Account#to_log_human_identifier, the acct of
// accountID, or the ID when the account can't be found.
func accountIdentifierFrom(ctx context.Context, accounts accountLookup, accountID int64) string {
	account, err := accounts.GetAccountByIDAllowSuspended(ctx, accountID)
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
	h.logUserActionFor(ctx, adminID, targetAccountID, user.ID, action)
}

// logUserActionFor logs an action on the user userID of the account
// targetAccountID, for when the user row is already gone.
func (h *AccountsHandler) logUserActionFor(ctx context.Context, adminID, targetAccountID, userID int64, action string) {
	routeParam := strconv.FormatInt(targetAccountID, 10)
	h.logAdminAction(ctx, adminID, action, adminActionTargetUser, userID, h.accountIdentifier(ctx, targetAccountID), routeParam, "")
}

// GetAll handles GET /api/v1/admin/accounts. Like
// Api::V1::Admin::AccountsController#translated_filter_params, it lists
// local, active accounts unless the request asks for others with presence
// flags, which any non-blank value sets: remote selects remote accounts, and
// active, pending, disabled, silenced and suspended select a status, the
// last of them that is present winning in that order. staff selects users
// whose role can manage reports. by_domain, username, display_name, email
// and ip filter as in v2. local only names the default, and sensitized,
// though Mastodon accepts it, selects nothing in v1.
func (h *AccountsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	present := func(name string) bool { return strings.TrimSpace(query.Get(name)) != "" }

	filter := AccountFilter{
		Origin:      "local",
		Status:      "active",
		ByDomain:    query.Get("by_domain"),
		Username:    query.Get("username"),
		DisplayName: query.Get("display_name"),
		Email:       query.Get("email"),
		IP:          query.Get("ip"),
		Staff:       present("staff"),
	}
	if present("remote") {
		filter.Origin = "remote"
	}
	for _, status := range []string{"active", "pending", "disabled", "silenced", "suspended"} {
		if present(status) {
			filter.Status = status
		}
	}

	// A nil store converts to a nil AccountListStore.
	NewAccountList(h.store, h.cfg, "/api/v1/admin/accounts", v1AccountsLinkParams).Serve(w, r, filter)
}

// Get handles GET /api/v1/admin/accounts/:id. Like Mastodon's Account.find,
// it shows any account, with or without a user: an ID that isn't a number
// or names no account gets 404 Record not found, and a failed lookup 500.
func (h *AccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	row, err := h.store.AdminGetAccount(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "Admin account lookup failed", "account_id", accountID, "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	// AdminGetAccount selects the columns of AdminListAccounts.
	accounts, err := SerializeAccounts(ctx, h.store, h.cfg, []db.AdminListAccountsRow{db.AdminListAccountsRow(row)})
	if err != nil {
		slog.ErrorContext(ctx, "Admin account serialization failed", "account_id", accountID, "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, accounts[0])
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

	// Rejecting deleted the user row, so the entry names the user that the
	// target lookup found.
	if adminID, ok := middleware.GetAccountID(r.Context()); ok {
		h.logUserActionFor(r.Context(), adminID, target.ID, target.UserID.Int64, "reject")
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
// that its own server suspended, or that isn't suspended, gets 403. So does
// an account whose deletion has finished, which stays suspended without its
// deletion request: Mastodon would make it usable again, empty; gotodon
// keeps a deleted account deleted. A deletion that finishes while the
// request runs wins too: the unsuspension then changes nothing and answers
// 403.
func (h *AccountsHandler) Unsuspend(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok {
		return
	}
	if !suspendedLocally(target) || !target.DeletionRequested {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	unsuspended, err := h.store.AdminUnsuspendAccount(r.Context(), target.ID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	if unsuspended == 0 {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
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
// As Api::V1::Admin::AccountsController#destroy queues
// Admin::AccountDeletionWorker, it starts the deletion in the background,
// keeping the username and the e-mail address taken; the account is gone
// for good once its deletion request is.
func (h *AccountsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok {
		return
	}
	if !target.SuspendedAt.Valid || !target.DeletionRequested {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}
	if h.deletions == nil {
		web.JSONError(w, http.StatusInternalServerError, "Account deletion unavailable")
		return
	}

	if err := h.deletions.ScheduleAccountDeletion(r.Context(), target.ID, services.AdminAccountDeletionOptions()); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to schedule account deletion")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers admin account routes. Permissions follow
// Mastodon's AccountPolicy and UserPolicy. Account actions need manage_users:
// Api::V1::Admin::AccountActionsController authorizes show? first, which
// covers the permission of every per-type check of Admin::AccountAction.
// Resolving reports also needs manage_reports (ReportPolicy#update?), which
// TakeAction checks only when there are reports to resolve. The handlers
// check the policies' conditions on the target account. Scopes
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

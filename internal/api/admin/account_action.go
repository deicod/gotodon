package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/moderation"
	"github.com/deicod/gotodon/internal/web"
)

// accountActionStore is the part of *db.Queries that an account action uses
// inside its transaction. Tests use stubs.
type accountActionStore interface {
	accountLookup
	AdminGetReport(ctx context.Context, id int64) (db.Report, error)
	AdminGetAccountWarningPreset(ctx context.Context, id int64) (db.AccountWarningPreset, error)
	AdminListUnresolvedReportsByTarget(ctx context.Context, targetAccountID int64) ([]db.Report, error)
	AdminDisableAccount(ctx context.Context, accountID int64) error
	AdminSensitizeAccount(ctx context.Context, id int64) error
	AdminSilenceAccount(ctx context.Context, id int64) error
	AdminSuspendAccount(ctx context.Context, id int64) error
	CreateAccountWarning(ctx context.Context, arg db.CreateAccountWarningParams) (db.AccountWarning, error)
	AdminResolveReport(ctx context.Context, arg db.AdminResolveReportParams) error
	CreateAdminActionLog(ctx context.Context, arg db.CreateAdminActionLogParams) error
}

// accountActionTransactor runs fn in one database transaction, which it
// commits when fn returns nil and rolls back otherwise.
type accountActionTransactor interface {
	InTx(ctx context.Context, fn func(accountActionStore) error) error
}

// poolTransactor runs transactions on a connection pool.
type poolTransactor struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func (t poolTransactor) InTx(ctx context.Context, fn func(accountActionStore) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return err
	}
	// Rolling back a committed transaction does nothing.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(t.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NotificationStreamer publishes a notification to its recipient's
// notification stream. *v1.NotificationsHandler satisfies it.
type NotificationStreamer interface {
	StreamNotification(ctx context.Context, notification db.Notification) error
}

// NotificationPusher fans a notification out to its recipient's Web Push
// subscriptions. *push.Publisher satisfies it.
type NotificationPusher interface {
	PublishNotification(ctx context.Context, notification db.Notification)
}

// accountActionTypes are Admin::AccountAction::TYPES.
var accountActionTypes = map[string]bool{
	"none":      true,
	"disable":   true,
	"sensitive": true,
	"silence":   true,
	"suspend":   true,
}

// actionParam is a request parameter as Rails' params.permit passes a scalar
// on: a JSON string as is, a JSON number or boolean in its JSON text, or a
// form value. Set is false when the parameter is absent or isn't a scalar,
// which permit drops; Null is true for a JSON null.
type actionParam struct {
	Value string
	Set   bool
	Null  bool
}

func (p *actionParam) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	switch {
	case bytes.Equal(data, []byte("null")):
		*p = actionParam{Set: true, Null: true}
	case len(data) > 0 && data[0] == '"':
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*p = actionParam{Value: value, Set: true}
	case len(data) > 0 && (data[0] == '{' || data[0] == '['):
		*p = actionParam{}
	default:
		*p = actionParam{Value: string(data), Set: true}
	}
	return nil
}

func (p *actionParam) UnmarshalText(text []byte) error {
	*p = actionParam{Value: string(text), Set: true}
	return nil
}

// present is Object#present? of the value: given and not blank.
func (p actionParam) present() bool {
	return strings.TrimSpace(p.Value) != ""
}

// railsFalseValues are ActiveModel::Type::Boolean::FALSE_VALUES in their
// text forms.
var railsFalseValues = map[string]bool{
	"0": true, "f": true, "F": true, "false": true, "FALSE": true, "off": true, "OFF": true,
}

// enabled is the parameter as Admin::BaseAction's boolean setters cast it
// with ActiveModel::Type::Boolean, defaulting to true when it is absent: a
// null or empty value casts to nil, which reads as false, a false value to
// false, and anything else to true.
func (p actionParam) enabled() bool {
	if !p.Set {
		return true
	}
	if p.Null || p.Value == "" {
		return false
	}
	return !railsFalseValues[p.Value]
}

// accountActionRequest is the body of POST /api/v1/admin/accounts/:id/action,
// as Api::V1::Admin::AccountActionsController#resource_params permits it.
// include_statuses isn't permitted, so Admin::BaseAction's default, true,
// always applies.
type accountActionRequest struct {
	Type                  actionParam `json:"type" form:"type"`
	Text                  actionParam `json:"text" form:"text"`
	ReportID              actionParam `json:"report_id" form:"report_id"`
	WarningPresetID       actionParam `json:"warning_preset_id" form:"warning_preset_id"`
	SendEmailNotification actionParam `json:"send_email_notification" form:"send_email_notification"`
}

// Errors that end an account action's transaction with a refusal.
var (
	// errActionRecordNotFound is ActiveRecord::RecordNotFound from
	// Report.find or AccountWarningPreset.find.
	errActionRecordNotFound = errors.New("admin: account action record not found")
	// errActionReportsNotPermitted is a refused ReportPolicy#update?.
	errActionReportsNotPermitted = errors.New("admin: account action may not resolve reports")
)

// accountAction is one Admin::AccountAction.
type accountAction struct {
	actionType string
	target     db.AdminGetAccountActionTargetRow
	acting     db.UserRole
	actingID   int64
	req        accountActionRequest
}

// TakeAction handles POST /api/v1/admin/accounts/:id/action as
// Admin::AccountAction#save! does. In one transaction it:
//
//  1. applies the type: disable, sensitive, silence or suspend (none
//     changes nothing), logging it;
//  2. creates a strike for the target (Admin::BaseAction#process_strike!)
//     with the action, the warning preset's text and text joined by a blank
//     line, the report and, with a report, its statuses;
//  3. logs the strike as create, for type none with custom text only;
//  4. resolves reports (process_reports!), logging each: for type none the
//     given report only, for every other type every unresolved report about
//     the target, whether or not a report is given.
//
// After the transaction it sends a local target's user the warning e-mail
// and a moderation_warning notification, unless send_email_notification is
// false (process_notification!).
//
// It refuses with 403 an action that Mastodon's policies refuse for the
// target (see accountActionAllowed), and one that would resolve reports when
// the acting role can't manage them (ReportPolicy#update?). A report_id or
// warning_preset_id that names nothing gets 404. A refusal or failure
// changes nothing.
func (h *AccountsHandler) TakeAction(w http.ResponseWriter, r *http.Request) {
	target, ok := h.actionTarget(w, r)
	if !ok {
		return
	}

	var req accountActionRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Admin::AccountAction validates the type before it authorizes.
	actionType := req.Type.Value
	if !accountActionTypes[actionType] {
		web.JSONError(w, http.StatusUnprocessableEntity, "Invalid action type")
		return
	}

	ctx := r.Context()
	acting, hasRole := ActingRole(ctx)
	actingID, hasAccount := middleware.GetAccountID(ctx)
	if !hasRole || !hasAccount || h.actions == nil {
		slog.ErrorContext(ctx, "Admin account action without an acting role, account or transaction",
			"account_id", target.ID, "role", hasRole, "acting_account", hasAccount, "transactions", h.actions != nil)
		web.JSONError(w, http.StatusInternalServerError, "Admin data unavailable")
		return
	}
	if !h.accountActionAllowed(acting, actionType, target) {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	action := accountAction{actionType: actionType, target: target, acting: acting, actingID: actingID, req: req}
	var warning db.AccountWarning
	err := h.actions.InTx(ctx, func(store accountActionStore) error {
		var err error
		warning, err = h.processAccountAction(ctx, store, action)
		return err
	})
	switch {
	case errors.Is(err, errActionRecordNotFound):
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	case errors.Is(err, errActionReportsNotPermitted):
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	case err != nil:
		slog.ErrorContext(ctx, "Admin account action failed", "account_id", target.ID, "type", actionType, "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if req.SendEmailNotification.enabled() {
		h.deliverWarning(ctx, action, warning)
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// processAccountAction does the database work of action in store, the
// action's transaction, and returns the strike it created. It finds the
// report and warning preset and the reports to resolve, and checks that the
// acting role may resolve them, before it changes anything.
func (h *AccountsHandler) processAccountAction(ctx context.Context, store accountActionStore, action accountAction) (db.AccountWarning, error) {
	report, hasReport, err := findActionRecord(ctx, action.req.ReportID, store.AdminGetReport)
	if err != nil {
		return db.AccountWarning{}, err
	}
	preset, hasPreset, err := findActionRecord(ctx, action.req.WarningPresetID, store.AdminGetAccountWarningPreset)
	if err != nil {
		return db.AccountWarning{}, err
	}

	// Admin::AccountAction#reports.
	var reports []db.Report
	if action.actionType == "none" {
		if hasReport {
			reports = []db.Report{report}
		}
	} else {
		reports, err = store.AdminListUnresolvedReportsByTarget(ctx, action.target.ID)
		if err != nil {
			return db.AccountWarning{}, fmt.Errorf("listing unresolved reports: %w", err)
		}
	}
	if len(reports) > 0 && !auth.HasPermission(action.acting.Permissions, auth.PermissionManageReports) {
		return db.AccountWarning{}, errActionReportsNotPermitted
	}

	if err := h.applyActionType(ctx, store, action); err != nil {
		return db.AccountWarning{}, err
	}

	// process_strike!. AccountWarning stores status IDs as strings.
	var statusIDs []string
	if hasReport {
		statusIDs = make([]string, 0, len(report.StatusIds))
		for _, id := range report.StatusIds {
			statusIDs = append(statusIDs, strconv.FormatInt(id, 10))
		}
	}
	actionValue, _ := moderation.StrikeActionValue(action.actionType)
	warning, err := store.CreateAccountWarning(ctx, db.CreateAccountWarningParams{
		AccountID:       pgtype.Int8{Int64: action.actingID, Valid: true},
		TargetAccountID: pgtype.Int8{Int64: action.target.ID, Valid: true},
		ReportID:        pgtype.Int8{Int64: report.ID, Valid: hasReport},
		Action:          actionValue,
		Text:            warningText(preset, hasPreset, action.req.Text),
		StatusIds:       statusIDs,
	})
	if err != nil {
		return db.AccountWarning{}, fmt.Errorf("creating strike: %w", err)
	}
	// Only a warning with custom text is worth a log entry; the other
	// types' entries are logged with their type.
	if action.actionType == "none" && strings.TrimSpace(warning.Text) != "" {
		identifier := accountIdentifierFrom(ctx, store, action.target.ID)
		routeParam := strconv.FormatInt(action.target.ID, 10)
		if err := createActionLog(ctx, store, action.actingID, "create", "AccountWarning", warning.ID, identifier, routeParam); err != nil {
			return db.AccountWarning{}, err
		}
	}

	// process_reports!: Report#resolve! by the acting account.
	for _, resolved := range reports {
		if err := createActionLog(ctx, store, action.actingID, "resolve", "Report", resolved.ID, strconv.FormatInt(resolved.ID, 10), ""); err != nil {
			return db.AccountWarning{}, err
		}
		if err := store.AdminResolveReport(ctx, db.AdminResolveReportParams{
			ID:                     resolved.ID,
			ActionTakenByAccountID: pgtype.Int8{Int64: action.actingID, Valid: true},
		}); err != nil {
			return db.AccountWarning{}, fmt.Errorf("resolving report %d: %w", resolved.ID, err)
		}
	}

	return warning, nil
}

// applyActionType is Admin::AccountAction#process_action!: it logs and
// applies the action's type. none changes nothing. disable logs the user,
// which accountActionAllowed made sure exists, the other types the account.
func (h *AccountsHandler) applyActionType(ctx context.Context, store accountActionStore, action accountAction) error {
	var apply func(context.Context, int64) error
	targetType, targetID := adminActionTargetAccount, action.target.ID
	routeParam := ""
	switch action.actionType {
	case "disable":
		apply = store.AdminDisableAccount
		targetType, targetID = adminActionTargetUser, action.target.UserID.Int64
		routeParam = strconv.FormatInt(action.target.ID, 10)
	case "sensitive":
		apply = store.AdminSensitizeAccount
	case "silence":
		apply = store.AdminSilenceAccount
	case "suspend":
		apply = store.AdminSuspendAccount
	default:
		return nil
	}

	identifier := accountIdentifierFrom(ctx, store, action.target.ID)
	if err := createActionLog(ctx, store, action.actingID, action.actionType, targetType, targetID, identifier, routeParam); err != nil {
		return err
	}
	if err := apply(ctx, action.target.ID); err != nil {
		return fmt.Errorf("applying %s: %w", action.actionType, err)
	}
	return nil
}

// findActionRecord finds the record that id names with find, as
// Admin::AccountAction finds its report and warning preset: a blank id names
// none, and one that names nothing, a malformed one included, is
// errActionRecordNotFound.
func findActionRecord[T any](ctx context.Context, id actionParam, find func(context.Context, int64) (T, error)) (T, bool, error) {
	var zero T
	if !id.present() {
		return zero, false, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(id.Value), 10, 64)
	if err != nil {
		return zero, false, errActionRecordNotFound
	}
	record, err := find(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, false, errActionRecordNotFound
	}
	if err != nil {
		return zero, false, err
	}
	return record, true, nil
}

// warningText is Admin::AccountAction#text_for_warning: the preset's text
// and text, joined by a blank line. Mastodon joins every part given, so an
// empty text after a preset leaves a trailing blank line; gotodon leaves out
// empty parts.
func warningText(preset db.AccountWarningPreset, hasPreset bool, text actionParam) string {
	parts := make([]string, 0, 2)
	if hasPreset && preset.Text != "" {
		parts = append(parts, preset.Text)
	}
	if text.Value != "" {
		parts = append(parts, text.Value)
	}
	return strings.Join(parts, "\n\n")
}

// createActionLog creates an Admin::ActionLog entry in store, as
// Auditable#log_action does inside an action's transaction.
func createActionLog(ctx context.Context, store accountActionStore, actingID int64, action, targetType string, targetID int64, humanIdentifier, routeParam string) error {
	err := store.CreateAdminActionLog(ctx, db.CreateAdminActionLogParams{
		AccountID:       actingID,
		Action:          action,
		TargetType:      pgtype.Text{String: targetType, Valid: true},
		TargetID:        pgtype.Int8{Int64: targetID, Valid: true},
		HumanIdentifier: pgtype.Text{String: humanIdentifier, Valid: strings.TrimSpace(humanIdentifier) != ""},
		RouteParam:      pgtype.Text{String: routeParam, Valid: strings.TrimSpace(routeParam) != ""},
	})
	if err != nil {
		return fmt.Errorf("logging %s of %s %d: %w", action, targetType, targetID, err)
	}
	return nil
}

// deliverWarning sends the warning e-mail and moderation_warning
// notification of warning, as Admin::BaseAction#process_notification! does
// for a local target (warnable?). A target without a user gets neither:
// Mastodon's mailer has no one to write to, and NotifyService drops a
// notification for an account without a user. NotifyService also drops one
// for a suspended recipient (Account#unavailable?), so a suspended target
// gets only the e-mail. Delivery is best effort: the action has been
// committed, so failures are logged.
func (h *AccountsHandler) deliverWarning(ctx context.Context, action accountAction, warning db.AccountWarning) {
	target := action.target
	if target.Domain.Valid || !target.UserID.Valid {
		return
	}

	h.sendWarningMail(ctx, target, warning, action.actionType)

	if action.actionType == "suspend" || target.SuspendedAt.Valid {
		return
	}
	h.notifyWarning(ctx, target, warning)
}

// sendWarningMail sends UserMailer.warning to the target's user.
func (h *AccountsHandler) sendWarningMail(ctx context.Context, target db.AdminGetAccountActionTargetRow, warning db.AccountWarning, actionType string) {
	if h.mail == nil {
		return
	}
	user, err := h.store.GetUserByAccountID(ctx, target.ID)
	if err != nil {
		slog.ErrorContext(ctx, "Warning e-mail recipient lookup failed", "account_id", target.ID, "error", err)
		return
	}
	account, err := h.store.GetAccountByIDAllowSuspended(ctx, target.ID)
	if err != nil {
		slog.ErrorContext(ctx, "Warning e-mail account lookup failed", "account_id", target.ID, "error", err)
		return
	}
	statuses, err := h.citedStatusLinks(ctx, warning)
	if err != nil {
		slog.ErrorContext(ctx, "Warning e-mail status lookup failed", "warning_id", warning.ID, "error", err)
		return
	}

	// User#account's local_username_and_domain.
	acct := "@" + account.Username
	if domain := h.cfg.FederationDomain(); domain != "" {
		acct += "@" + domain
	}
	mail := mailer.Mail{
		Kind:   mailer.KindWarning,
		To:     user.Email,
		Locale: user.Locale.String,
		Params: map[string]string{
			"Action":   actionType,
			"Acct":     acct,
			"Text":     warning.Text,
			"Statuses": strings.Join(statuses, "\n"),
		},
	}
	if err := h.mail.Deliver(ctx, mail); err != nil {
		slog.ErrorContext(ctx, "Failed to enqueue warning e-mail", "account_id", target.ID, "warning_id", warning.ID, "error", err)
	}
}

// citedStatusLinks returns a "=> URL" line for each status warning cites:
// a status's URL, or, for a local one, which has none, its web page.
func (h *AccountsHandler) citedStatusLinks(ctx context.Context, warning db.AccountWarning) ([]string, error) {
	ids := make([]int64, 0, len(warning.StatusIds))
	for _, value := range warning.StatusIds {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := h.store.AdminListCitedStatuses(ctx, ids)
	if err != nil {
		return nil, err
	}

	webDomain := ""
	if h.cfg != nil {
		webDomain = h.cfg.Instance.PublicWebDomain()
	}
	links := make([]string, 0, len(rows))
	for _, row := range rows {
		link := strings.TrimSpace(row.Url.String)
		if link == "" && !row.Domain.Valid && webDomain != "" {
			link = "https://" + webDomain + "/@" + row.Username + "/" + strconv.FormatInt(row.ID, 10)
		}
		if link == "" {
			link = strings.TrimSpace(row.Uri.String)
		}
		if link != "" {
			links = append(links, "=> "+link)
		}
	}
	return links, nil
}

// notifyWarning creates the target's moderation_warning notification, as
// LocalNotificationWorker does, from the target itself, and streams and
// pushes it.
func (h *AccountsHandler) notifyWarning(ctx context.Context, target db.AdminGetAccountActionTargetRow, warning db.AccountWarning) {
	notification, err := h.store.CreateNotification(ctx, db.CreateNotificationParams{
		AccountID:     target.ID,
		FromAccountID: target.ID,
		ActivityType:  moderation.ActivityTypeWarning,
		ActivityID:    warning.ID,
		Type:          pgtype.Text{String: moderation.NotificationTypeWarning, Valid: true},
	})
	if err != nil {
		slog.ErrorContext(ctx, "Failed to create moderation warning notification", "account_id", target.ID, "warning_id", warning.ID, "error", err)
		return
	}
	if h.streamer != nil {
		if err := h.streamer.StreamNotification(ctx, notification); err != nil {
			slog.ErrorContext(ctx, "Failed to stream moderation warning notification", "notification_id", notification.ID, "error", err)
		}
	}
	if h.pusher != nil {
		h.pusher.PublishNotification(ctx, notification)
	}
}

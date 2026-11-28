package moderation

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
)

// ReportNotificationStore is the data NotifyNewReport reads.
type ReportNotificationStore interface {
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	ListUsersWithPermission(ctx context.Context, permission int64) ([]db.User, error)
}

// NotifyNewReport mails every user who can manage reports about report, a
// report just filed, whether a local account filed it or a remote server's
// Flag did. It does nothing without a deliverer. Failures are logged, not
// returned: the report stands whether or not the mails go out.
func NotifyNewReport(ctx context.Context, store ReportNotificationStore, deliverer mailer.Deliverer, report db.Report) {
	if store == nil || deliverer == nil {
		return
	}
	staff, err := store.ListUsersWithPermission(ctx, auth.PermissionManageReports)
	if err != nil {
		slog.Error("Failed to load staff for report mail", "error", err)
		return
	}
	if len(staff) == 0 {
		return
	}
	reporter := accountHandle(ctx, store, report.AccountID)
	target := accountHandle(ctx, store, report.TargetAccountID)
	for _, member := range staff {
		locale := ""
		if member.Locale.Valid {
			locale = member.Locale.String
		}
		mail := mailer.Mail{
			Kind:   mailer.KindAdminNewReport,
			To:     member.Email,
			Locale: locale,
			Params: map[string]string{
				"ReportID": strconv.FormatInt(report.ID, 10),
				"Reporter": reporter,
				"Target":   target,
			},
		}
		if err := deliverer.Deliver(ctx, mail); err != nil {
			slog.Error("Failed to enqueue report mail", "error", err)
		}
	}
}

// accountHandle is the @username or @username@domain of the account, or
// #id when it cannot be loaded.
func accountHandle(ctx context.Context, store ReportNotificationStore, accountID int64) string {
	account, err := store.GetAccountByID(ctx, accountID)
	if err != nil {
		return "#" + strconv.FormatInt(accountID, 10)
	}
	handle := "@" + account.Username
	if account.Domain.Valid && account.Domain.String != "" {
		handle += "@" + account.Domain.String
	}
	return handle
}

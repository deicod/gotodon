package v1

import (
	"context"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

// Notification and activity types of a strike's moderation_warning
// notification, as Admin::BaseAction#process_notification! creates it.
const (
	NotificationTypeModerationWarning = "moderation_warning"
	ActivityTypeAccountWarning        = "AccountWarning"
)

// accountWarningActions is Mastodon's AccountWarning action enum, which
// account_warnings.action stores.
var accountWarningActions = map[string]int32{
	"none":                       0,
	"disable":                    1000,
	"mark_statuses_as_sensitive": 1250,
	"delete_statuses":            1500,
	"sensitive":                  2000,
	"silence":                    3000,
	"suspend":                    4000,
}

// AccountWarningActionValue returns the stored value of the strike action
// name, and false for a name that isn't one.
func AccountWarningActionValue(name string) (int32, bool) {
	value, ok := accountWarningActions[name]
	return value, ok
}

// AccountWarningActionName returns the name of the stored strike action
// value, and "" for a value that isn't one.
func AccountWarningActionName(value int32) string {
	for name, v := range accountWarningActions {
		if v == value {
			return name
		}
	}
	return ""
}

// AccountWarning is Mastodon's AccountWarning entity
// (REST::AccountWarningSerializer), which moderation_warning notifications
// carry. StatusIDs is null for a strike without cited statuses, and Appeal
// for one that hasn't been appealed.
type AccountWarning struct {
	ID            string    `json:"id"`
	Action        string    `json:"action"`
	Text          string    `json:"text"`
	StatusIDs     []string  `json:"status_ids"`
	TargetAccount Account   `json:"target_account"`
	Appeal        *Appeal   `json:"appeal"`
	CreatedAt     time.Time `json:"created_at"`
}

// Appeal is Mastodon's Appeal entity (REST::AppealSerializer).
type Appeal struct {
	Text string `json:"text"`
	// State is approved, rejected or pending (Appeal#state).
	State string `json:"state"`
}

// AccountWarningQuerier is what LoadAccountWarnings reads. *db.Queries
// satisfies it.
type AccountWarningQuerier interface {
	AccountDataQuerier
	GetAccountWarningsByIDs(ctx context.Context, ids []int64) ([]db.GetAccountWarningsByIDsRow, error)
}

// LoadAccountWarnings renders the strikes that ids name, by ID, with their
// target accounts. A strike that's gone, or whose target account is, is left
// out, as Mastodon leaves out a notification's missing activity.
func LoadAccountWarnings(ctx context.Context, q AccountWarningQuerier, cfg *config.Config, ids []int64) (map[int64]AccountWarning, error) {
	warnings := map[int64]AccountWarning{}
	if len(ids) == 0 {
		return warnings, nil
	}

	rows, err := q.GetAccountWarningsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	targetIDs := make([]int64, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for _, row := range rows {
		if row.TargetAccountID.Valid && !seen[row.TargetAccountID.Int64] {
			seen[row.TargetAccountID.Int64] = true
			targetIDs = append(targetIDs, row.TargetAccountID.Int64)
		}
	}
	targets := map[int64]Account{}
	if len(targetIDs) > 0 {
		dbAccounts, err := q.GetAccountsByIDs(ctx, targetIDs)
		if err != nil {
			return nil, err
		}
		targets = serializeAccounts(ctx, q, cfg, dbAccounts, true)
	}

	for _, row := range rows {
		target, ok := targets[row.TargetAccountID.Int64]
		if !row.TargetAccountID.Valid || !ok {
			continue
		}
		warning := AccountWarning{
			ID:            strconv.FormatInt(row.ID, 10),
			Action:        AccountWarningActionName(row.Action),
			Text:          row.Text,
			StatusIDs:     row.StatusIds,
			TargetAccount: target,
			CreatedAt:     row.CreatedAt.Time,
		}
		if row.AppealID.Valid {
			warning.Appeal = &Appeal{Text: row.AppealText.String, State: appealState(row)}
		}
		warnings[row.ID] = warning
	}
	return warnings, nil
}

// appealState is Appeal#state.
func appealState(row db.GetAccountWarningsByIDsRow) string {
	switch {
	case row.AppealApprovedAt.Valid:
		return "approved"
	case row.AppealRejectedAt.Valid:
		return "rejected"
	default:
		return "pending"
	}
}

// IsModerationWarning reports whether notification is a strike's
// moderation_warning notification, whose activity is the strike.
func IsModerationWarning(notification db.Notification) bool {
	return notification.Type.Valid && notification.Type.String == NotificationTypeModerationWarning &&
		notification.ActivityType == ActivityTypeAccountWarning && notification.ActivityID > 0
}

// ModerationWarningIDs returns the strikes that the moderation_warning
// notifications among notifications name, each once.
func ModerationWarningIDs(notifications []db.Notification) []int64 {
	var ids []int64
	seen := map[int64]bool{}
	for _, notification := range notifications {
		if IsModerationWarning(notification) && !seen[notification.ActivityID] {
			seen[notification.ActivityID] = true
			ids = append(ids, notification.ActivityID)
		}
	}
	return ids
}

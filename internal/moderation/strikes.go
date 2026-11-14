// Package moderation holds what the moderation features share: the actions
// of strikes (Mastodon's AccountWarning) and the notification that tells an
// account about one.
package moderation

import "github.com/deicod/gotodon/internal/db"

// The notification and activity types of a strike's notification, as
// Admin::BaseAction#process_notification! creates it.
const (
	NotificationTypeWarning = "moderation_warning"
	ActivityTypeWarning     = "AccountWarning"
)

// strikeActions is Mastodon's AccountWarning action enum, which
// account_warnings.action stores.
var strikeActions = map[string]int32{
	"none":                       0,
	"disable":                    1000,
	"mark_statuses_as_sensitive": 1250,
	"delete_statuses":            1500,
	"sensitive":                  2000,
	"silence":                    3000,
	"suspend":                    4000,
}

// StrikeActionValue returns the stored value of the strike action name, and
// false for a name that isn't one.
func StrikeActionValue(name string) (int32, bool) {
	value, ok := strikeActions[name]
	return value, ok
}

// StrikeActionName returns the name of the stored strike action value, and
// "" for a value that isn't one.
func StrikeActionName(value int32) string {
	for name, v := range strikeActions {
		if v == value {
			return name
		}
	}
	return ""
}

// IsWarningNotification reports whether notification is a strike's
// moderation_warning notification, whose activity is the strike.
func IsWarningNotification(notification db.Notification) bool {
	return notification.Type.Valid && notification.Type.String == NotificationTypeWarning &&
		notification.ActivityType == ActivityTypeWarning && notification.ActivityID > 0
}

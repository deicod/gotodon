package moderation

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

func TestStrikeActions(t *testing.T) {
	for name, value := range map[string]int32{
		"none": 0, "disable": 1000, "mark_statuses_as_sensitive": 1250, "delete_statuses": 1500,
		"sensitive": 2000, "silence": 3000, "suspend": 4000,
	} {
		if got, ok := StrikeActionValue(name); !ok || got != value {
			t.Errorf("StrikeActionValue(%q) = %d, %v, want %d", name, got, ok, value)
		}
		if got := StrikeActionName(value); got != name {
			t.Errorf("StrikeActionName(%d) = %q, want %q", value, got, name)
		}
	}
	if _, ok := StrikeActionValue("ban"); ok {
		t.Error("StrikeActionValue(ban) is an action")
	}
	if got := StrikeActionName(42); got != "" {
		t.Errorf("StrikeActionName(42) = %q, want none", got)
	}
}

func TestIsWarningNotification(t *testing.T) {
	warning := pgtype.Text{String: "moderation_warning", Valid: true}
	tests := []struct {
		name         string
		notification db.Notification
		want         bool
	}{
		{"strike notification", db.Notification{Type: warning, ActivityType: "AccountWarning", ActivityID: 5}, true},
		{"another activity", db.Notification{Type: warning, ActivityType: "Status", ActivityID: 5}, false},
		{"another type", db.Notification{Type: pgtype.Text{String: "mention", Valid: true}, ActivityType: "AccountWarning", ActivityID: 5}, false},
		{"no type", db.Notification{ActivityType: "AccountWarning", ActivityID: 5}, false},
		{"no strike", db.Notification{Type: warning, ActivityType: "AccountWarning"}, false},
	}
	for _, tt := range tests {
		if got := IsWarningNotification(tt.notification); got != tt.want {
			t.Errorf("%s: IsWarningNotification() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

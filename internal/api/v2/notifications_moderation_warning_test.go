package v2

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/db"
)

// TestNotificationsHandler_AttachModerationWarnings checks that every
// moderation_warning group carries its strike, and no other group does.
func TestNotificationsHandler_AttachModerationWarnings(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)
	now := time.Now()
	at := func(minutes int) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: now.Add(-time.Duration(minutes) * time.Minute), Valid: true}
	}
	warning := func(id, strike int64, minutes int) db.Notification {
		return db.Notification{
			ID: id, AccountID: 7, FromAccountID: 7, ActivityType: "AccountWarning", ActivityID: strike,
			Type: pgtype.Text{String: "moderation_warning", Valid: true}, CreatedAt: at(minutes),
		}
	}
	notifications := []db.Notification{
		warning(12, 50, 0),
		{ID: 11, FromAccountID: 2, ActivityType: "Status", ActivityID: 42, Type: pgtype.Text{String: "mention", Valid: true}, CreatedAt: at(1)},
		warning(10, 51, 2),
		// The strike of this one is gone.
		warning(9, 52, 3),
	}
	warnings := map[int64]v1.AccountWarning{
		50: {ID: "50", Action: "silence"},
		51: {ID: "51", Action: "none"},
	}

	groups, _, _ := h.groupNotifications(notifications)
	h.attachModerationWarnings(groups, notifications, warnings)

	want := map[string]string{
		"moderation_warning:AccountWarning:50": "50",
		"mention:Status:42":                    "",
		"moderation_warning:AccountWarning:51": "51",
		"moderation_warning:AccountWarning:52": "",
	}
	if len(groups) != len(want) {
		t.Fatalf("groups = %+v, want %d", groups, len(want))
	}
	for _, group := range groups {
		wantID, ok := want[group.GroupKey]
		if !ok {
			t.Fatalf("unexpected group %q", group.GroupKey)
		}
		gotID := ""
		if group.ModerationWarning != nil {
			gotID = group.ModerationWarning.ID
		}
		if gotID != wantID {
			t.Errorf("group %q carries strike %q, want %q", group.GroupKey, gotID, wantID)
		}

		data, err := json.Marshal(group)
		if err != nil {
			t.Fatalf("encoding: %v", err)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(data, &keys); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if _, ok := keys["moderation_warning"]; ok != (wantID != "") {
			t.Errorf("group %q renders moderation_warning = %v: %s", group.GroupKey, ok, data)
		}
	}
}

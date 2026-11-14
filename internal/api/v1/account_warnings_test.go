package v1

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
)

// accountWarningStub serves strikes and their target accounts.
type accountWarningStub struct {
	accountSerializeStub
	warnings    []db.GetAccountWarningsByIDsRow
	accounts    []db.Account
	warningsErr error
	accountsErr error
	// asked records the strike IDs looked up.
	asked []int64
}

func (s *accountWarningStub) GetAccountWarningsByIDs(_ context.Context, ids []int64) ([]db.GetAccountWarningsByIDsRow, error) {
	s.asked = append(s.asked, ids...)
	if s.warningsErr != nil {
		return nil, s.warningsErr
	}
	var rows []db.GetAccountWarningsByIDsRow
	for _, row := range s.warnings {
		if slices.Contains(ids, row.ID) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (s *accountWarningStub) GetAccountsByIDs(_ context.Context, ids []int64) ([]db.Account, error) {
	if s.accountsErr != nil {
		return nil, s.accountsErr
	}
	var accounts []db.Account
	for _, account := range s.accounts {
		if slices.Contains(ids, account.ID) {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func TestLoadAccountWarnings(t *testing.T) {
	created := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	at := pgtype.Timestamptz{Time: created, Valid: true}
	target := pgtype.Int8{Int64: 7, Valid: true}
	stub := &accountWarningStub{
		accounts: []db.Account{{ID: 7, Username: "alice"}},
		warnings: []db.GetAccountWarningsByIDsRow{
			// Without a report or an appeal.
			{ID: 1, TargetAccountID: target, Action: 3000, Text: "Spam.", CreatedAt: at},
			// With cited statuses and a pending appeal.
			{ID: 2, TargetAccountID: target, Action: 0, StatusIds: []string{"10", "11"}, CreatedAt: at,
				AppealID: pgtype.Int8{Int64: 1, Valid: true}, AppealText: pgtype.Text{String: "Sorry", Valid: true}},
			// From a report without statuses, appeal approved.
			{ID: 3, TargetAccountID: target, Action: 4000, StatusIds: []string{}, CreatedAt: at,
				AppealID: pgtype.Int8{Int64: 2, Valid: true}, AppealText: pgtype.Text{String: "Mistake", Valid: true}, AppealApprovedAt: at},
			// Appeal rejected.
			{ID: 4, TargetAccountID: target, Action: 1000, CreatedAt: at,
				AppealID: pgtype.Int8{Int64: 3, Valid: true}, AppealRejectedAt: at},
			// The target account is gone.
			{ID: 5, TargetAccountID: pgtype.Int8{Int64: 8, Valid: true}, CreatedAt: at},
			// No target account at all.
			{ID: 6, CreatedAt: at},
		},
	}

	warnings, err := LoadAccountWarnings(context.Background(), stub, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, []int64{1, 2, 3, 4, 5, 6, 9})
	if err != nil {
		t.Fatalf("LoadAccountWarnings() error = %v", err)
	}
	if len(warnings) != 4 {
		t.Fatalf("warnings = %v, want 1 to 4", warnings)
	}

	encoded := map[int64]string{}
	for id, warning := range warnings {
		if warning.TargetAccount.ID != "7" || warning.TargetAccount.Username != "alice" {
			t.Errorf("warning %d targets %+v, want alice", id, warning.TargetAccount)
		}
		if !warning.CreatedAt.Equal(created) {
			t.Errorf("warning %d created %v, want %v", id, warning.CreatedAt, created)
		}
		data, err := json.Marshal(struct {
			ID        string   `json:"id"`
			Action    string   `json:"action"`
			Text      string   `json:"text"`
			StatusIDs []string `json:"status_ids"`
			Appeal    *Appeal  `json:"appeal"`
		}{warning.ID, warning.Action, warning.Text, warning.StatusIDs, warning.Appeal})
		if err != nil {
			t.Fatalf("encoding warning %d: %v", id, err)
		}
		encoded[id] = string(data)
	}
	want := map[int64]string{
		1: `{"id":"1","action":"silence","text":"Spam.","status_ids":null,"appeal":null}`,
		2: `{"id":"2","action":"none","text":"","status_ids":["10","11"],"appeal":{"text":"Sorry","state":"pending"}}`,
		3: `{"id":"3","action":"suspend","text":"","status_ids":[],"appeal":{"text":"Mistake","state":"approved"}}`,
		4: `{"id":"4","action":"disable","text":"","status_ids":null,"appeal":{"text":"","state":"rejected"}}`,
	}
	for id, json := range want {
		if encoded[id] != json {
			t.Errorf("warning %d = %s, want %s", id, encoded[id], json)
		}
	}

	// The entity has Mastodon's keys.
	data, err := json.Marshal(warnings[1])
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, key := range []string{"id", "action", "text", "status_ids", "target_account", "appeal", "created_at"} {
		if _, ok := keys[key]; !ok {
			t.Errorf("entity lacks %s: %s", key, data)
		}
	}
}

func TestLoadAccountWarningsEmptyAndErrors(t *testing.T) {
	ctx := context.Background()
	stub := &accountWarningStub{}
	warnings, err := LoadAccountWarnings(ctx, stub, nil, nil)
	if err != nil || len(warnings) != 0 || len(stub.asked) != 0 {
		t.Fatalf("LoadAccountWarnings(nil) = %v, %v after asking %v, want nothing without a query", warnings, err, stub.asked)
	}

	failing := errors.New("database unavailable")
	row := db.GetAccountWarningsByIDsRow{ID: 1, TargetAccountID: pgtype.Int8{Int64: 7, Valid: true}}
	for name, stub := range map[string]*accountWarningStub{
		"strikes":  {warningsErr: failing},
		"accounts": {warnings: []db.GetAccountWarningsByIDsRow{row}, accountsErr: failing},
	} {
		if _, err := LoadAccountWarnings(ctx, stub, nil, []int64{1}); !errors.Is(err, failing) {
			t.Errorf("%s lookup failing: error = %v, want %v", name, err, failing)
		}
	}
}

func TestModerationWarningIDs(t *testing.T) {
	warning := func(id, activity int64) db.Notification {
		return db.Notification{ID: id, ActivityType: "AccountWarning", ActivityID: activity, Type: pgtype.Text{String: "moderation_warning", Valid: true}}
	}
	notifications := []db.Notification{
		warning(1, 50),
		{ID: 2, ActivityType: "Status", ActivityID: 51, Type: pgtype.Text{String: "mention", Valid: true}},
		warning(3, 52),
		warning(4, 50),
		// A moderation_warning of another activity, and an AccountWarning
		// of another type, aren't strikes' notifications.
		{ID: 5, ActivityType: "Status", ActivityID: 53, Type: pgtype.Text{String: "moderation_warning", Valid: true}},
		{ID: 6, ActivityType: "AccountWarning", ActivityID: 54, Type: pgtype.Text{String: "mention", Valid: true}},
		warning(7, 0),
	}
	if got := ModerationWarningIDs(notifications); !slices.Equal(got, []int64{50, 52}) {
		t.Fatalf("ModerationWarningIDs() = %v, want [50 52]", got)
	}
}

func TestAttachModerationWarning(t *testing.T) {
	warnings := map[int64]AccountWarning{50: {ID: "50", Action: "silence"}}
	moderation := db.Notification{ID: 1, ActivityType: "AccountWarning", ActivityID: 50, Type: pgtype.Text{String: "moderation_warning", Valid: true}}

	notification := Notification{ID: "1"}
	attachModerationWarning(&notification, moderation, warnings)
	if notification.ModerationWarning == nil || notification.ModerationWarning.ID != "50" {
		t.Fatalf("moderation_warning = %+v, want strike 50", notification.ModerationWarning)
	}

	missing := moderation
	missing.ActivityID = 51
	notification = Notification{ID: "1"}
	attachModerationWarning(&notification, missing, warnings)
	if notification.ModerationWarning != nil {
		t.Fatalf("moderation_warning = %+v for a missing strike, want none", notification.ModerationWarning)
	}

	other := db.Notification{ID: 2, ActivityType: "Status", ActivityID: 50, Type: pgtype.Text{String: "mention", Valid: true}}
	notification = Notification{ID: "2"}
	attachModerationWarning(&notification, other, warnings)
	if notification.ModerationWarning != nil {
		t.Fatalf("moderation_warning = %+v on a mention, want none", notification.ModerationWarning)
	}

	data, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if _, ok := keys["moderation_warning"]; ok {
		t.Fatalf("mention renders moderation_warning: %s", data)
	}
}

// recordingPublisher is a streaming.Publisher that records notifications.
type recordingPublisher struct {
	streaming.Publisher
	accounts      []int64
	notifications []any
	err           error
}

func (p *recordingPublisher) PublishNotificationToRedis(_ context.Context, accountID int64, notification any) error {
	p.accounts = append(p.accounts, accountID)
	p.notifications = append(p.notifications, notification)
	return p.err
}

func TestStreamNotification(t *testing.T) {
	ctx := context.Background()
	notification := db.Notification{
		ID: 9, AccountID: 7, FromAccountID: 7, ActivityType: "AccountWarning", ActivityID: 50,
		Type:      pgtype.Text{String: "moderation_warning", Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Valid: true},
	}

	if err := (*NotificationsHandler)(nil).StreamNotification(ctx, notification); err != nil {
		t.Fatalf("nil handler: %v", err)
	}
	if err := NewNotificationsHandler(nil, nil, nil, nil).StreamNotification(ctx, notification); err != nil {
		t.Fatalf("handler without a publisher: %v", err)
	}

	publisher := &recordingPublisher{}
	if err := NewNotificationsHandler(nil, publisher, nil, nil).StreamNotification(ctx, notification); err != nil {
		t.Fatalf("StreamNotification() error = %v", err)
	}
	if !slices.Equal(publisher.accounts, []int64{7}) || len(publisher.notifications) != 1 {
		t.Fatalf("published to %v: %v, want account 7", publisher.accounts, publisher.notifications)
	}
	streamed, ok := publisher.notifications[0].(Notification)
	if !ok || streamed.ID != "9" || streamed.Type != "moderation_warning" || streamed.Account.ID != "7" {
		t.Fatalf("streamed %#v, want notification 9 from account 7", publisher.notifications[0])
	}

	publisher.err = errors.New("redis unavailable")
	if err := NewNotificationsHandler(nil, publisher, nil, nil).StreamNotification(ctx, notification); !errors.Is(err, publisher.err) {
		t.Fatalf("error = %v, want %v", err, publisher.err)
	}
}

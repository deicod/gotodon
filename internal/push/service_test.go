package push

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type fakeStore struct {
	subscriptions map[int64]db.WebPushSubscription
	notifications map[int64]db.Notification
	accounts      map[int64]db.Account
	statuses      map[int64]db.Status
	tokens        map[int64]db.OauthAccessToken
	usersByAcct   map[int64]db.User
	usersByID     map[int64]db.User
	follows       map[[2]int64]bool

	deletedSubscriptions []int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		subscriptions: map[int64]db.WebPushSubscription{},
		notifications: map[int64]db.Notification{},
		accounts:      map[int64]db.Account{},
		statuses:      map[int64]db.Status{},
		tokens:        map[int64]db.OauthAccessToken{},
		usersByAcct:   map[int64]db.User{},
		usersByID:     map[int64]db.User{},
		follows:       map[[2]int64]bool{},
	}
}

func (f *fakeStore) GetPushSubscriptionByID(_ context.Context, id int64) (db.WebPushSubscription, error) {
	sub, ok := f.subscriptions[id]
	if !ok {
		return db.WebPushSubscription{}, pgx.ErrNoRows
	}
	return sub, nil
}

func (f *fakeStore) ListPushSubscriptionsByUserID(_ context.Context, userID pgtype.Int8) ([]db.WebPushSubscription, error) {
	var subs []db.WebPushSubscription
	for _, sub := range f.subscriptions {
		if sub.UserID.Valid && userID.Valid && sub.UserID.Int64 == userID.Int64 {
			subs = append(subs, sub)
		}
	}
	return subs, nil
}

func (f *fakeStore) DeletePushSubscriptionByID(_ context.Context, id int64) error {
	delete(f.subscriptions, id)
	f.deletedSubscriptions = append(f.deletedSubscriptions, id)
	return nil
}

func (f *fakeStore) GetNotificationByID(_ context.Context, id int64) (db.Notification, error) {
	notification, ok := f.notifications[id]
	if !ok {
		return db.Notification{}, pgx.ErrNoRows
	}
	return notification, nil
}

func (f *fakeStore) GetAccountByID(_ context.Context, id int64) (db.Account, error) {
	account, ok := f.accounts[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (f *fakeStore) GetStatusByID(_ context.Context, id int64) (db.Status, error) {
	status, ok := f.statuses[id]
	if !ok {
		return db.Status{}, pgx.ErrNoRows
	}
	return status, nil
}

func (f *fakeStore) GetOAuthAccessTokenByID(_ context.Context, id int64) (db.OauthAccessToken, error) {
	token, ok := f.tokens[id]
	if !ok {
		return db.OauthAccessToken{}, pgx.ErrNoRows
	}
	return token, nil
}

func (f *fakeStore) GetUserByAccountID(_ context.Context, accountID int64) (db.User, error) {
	user, ok := f.usersByAcct[accountID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (f *fakeStore) GetUserByID(_ context.Context, id int64) (db.User, error) {
	user, ok := f.usersByID[id]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (f *fakeStore) GetFollow(_ context.Context, arg db.GetFollowParams) (db.Follow, error) {
	if f.follows[[2]int64{arg.AccountID, arg.TargetAccountID}] {
		return db.Follow{}, nil
	}
	return db.Follow{}, pgx.ErrNoRows
}

type fakeSender struct {
	status   int
	err      error
	payloads [][]byte
	subs     []Subscription
}

func (f *fakeSender) Send(_ context.Context, sub Subscription, payload []byte) (int, error) {
	f.subs = append(f.subs, sub)
	f.payloads = append(f.payloads, payload)
	return f.status, f.err
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Instance.LocalDomain = "example.com"
	return cfg
}

// fixture builds a store with one user (account 1), one mention notification
// from account 2, and one subscription with the mention alert enabled.
func fixture() *fakeStore {
	store := newFakeStore()
	store.accounts[1] = db.Account{ID: 1, Username: "bob"}
	store.accounts[2] = db.Account{
		ID:          2,
		Username:    "alice",
		DisplayName: "Alice",
		Note:        "<p>Bio</p>",
		AvatarFileName: pgtype.Text{
			String: "accounts/avatars/000/000/002/original/a.png", Valid: true,
		},
	}
	store.usersByAcct[1] = db.User{ID: 10, AccountID: 1, Locale: pgtype.Text{String: "de", Valid: true}}
	store.usersByID[10] = store.usersByAcct[1]
	store.statuses[100] = db.Status{ID: 100, Text: "<p>hello <span>@bob</span></p>", AccountID: 2}
	store.tokens[55] = db.OauthAccessToken{ID: 55, Token: "secret-token"}
	store.notifications[500] = db.Notification{
		ID:            500,
		AccountID:     1,
		FromAccountID: 2,
		ActivityType:  "Status",
		ActivityID:    100,
		Type:          pgtype.Text{String: "mention", Valid: true},
		UpdatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	store.subscriptions[7] = db.WebPushSubscription{
		ID:            7,
		Endpoint:      "https://push.example.org/send/abc",
		KeyP256dh:     pgtype.Text{String: "key", Valid: true},
		KeyAuth:       pgtype.Text{String: "auth", Valid: true},
		Data:          []byte(`{"alerts":{"mention":true,"favourite":false}}`),
		AccessTokenID: pgtype.Int8{Int64: 55, Valid: true},
		UserID:        pgtype.Int8{Int64: 10, Valid: true},
	}
	return store
}

func decodePayload(t *testing.T, raw []byte) notificationPayload {
	t.Helper()
	var payload notificationPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	return payload
}

func TestDeliverSubscriptionPayloadShape(t *testing.T) {
	store := fixture()
	sender := &fakeSender{status: 201}
	service := NewService(store, sender, testConfig(), nil)

	if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
		t.Fatalf("DeliverSubscription: %v", err)
	}
	if len(sender.payloads) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sender.payloads))
	}
	if sender.subs[0].Endpoint != "https://push.example.org/send/abc" {
		t.Errorf("endpoint = %q", sender.subs[0].Endpoint)
	}

	payload := decodePayload(t, sender.payloads[0])
	if payload.AccessToken != "secret-token" {
		t.Errorf("access_token = %q", payload.AccessToken)
	}
	if payload.PreferredLocale != "de" {
		t.Errorf("preferred_locale = %q", payload.PreferredLocale)
	}
	if payload.NotificationID != 500 {
		t.Errorf("notification_id = %d", payload.NotificationID)
	}
	if payload.NotificationType != "mention" {
		t.Errorf("notification_type = %q", payload.NotificationType)
	}
	if payload.Title != "You were mentioned by Alice" {
		t.Errorf("title = %q", payload.Title)
	}
	if payload.Body != "hello @bob" {
		t.Errorf("body = %q", payload.Body)
	}
	if payload.Icon != "https://example.com/files/accounts/avatars/000/000/002/original/a.png" {
		t.Errorf("icon = %q", payload.Icon)
	}
}

func TestDeliverSubscriptionUsesSpoilerText(t *testing.T) {
	store := fixture()
	status := store.statuses[100]
	status.SpoilerText = "CW: something"
	store.statuses[100] = status

	sender := &fakeSender{status: 200}
	service := NewService(store, sender, testConfig(), nil)
	if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
		t.Fatalf("DeliverSubscription: %v", err)
	}
	if payload := decodePayload(t, sender.payloads[0]); payload.Body != "CW: something" {
		t.Errorf("body = %q", payload.Body)
	}
}

func TestDeliverSubscriptionSkipsDisabledAlert(t *testing.T) {
	store := fixture()
	notification := store.notifications[500]
	notification.Type = pgtype.Text{String: "favourite", Valid: true}
	store.notifications[500] = notification

	sender := &fakeSender{status: 200}
	service := NewService(store, sender, testConfig(), nil)
	if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
		t.Fatalf("DeliverSubscription: %v", err)
	}
	if len(sender.payloads) != 0 {
		t.Errorf("expected no sends for disabled alert, got %d", len(sender.payloads))
	}
}

func TestDeliverSubscriptionPolicyFiltering(t *testing.T) {
	cases := []struct {
		policy  string
		follows bool // recipient (1) follows sender (2)
		want    bool
	}{
		{"all", false, true},
		{"", false, true},
		{"none", true, false},
		{"followed", true, true},
		{"followed", false, false},
		{"follower", false, false},
	}
	for _, tc := range cases {
		store := fixture()
		sub := store.subscriptions[7]
		sub.Data = []byte(`{"alerts":{"mention":true},"policy":"` + tc.policy + `"}`)
		if tc.policy == "" {
			sub.Data = []byte(`{"alerts":{"mention":true}}`)
		}
		store.subscriptions[7] = sub
		if tc.follows {
			store.follows[[2]int64{1, 2}] = true
		}

		sender := &fakeSender{status: 200}
		service := NewService(store, sender, testConfig(), nil)
		if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
			t.Fatalf("policy %q: %v", tc.policy, err)
		}
		sent := len(sender.payloads) == 1
		if sent != tc.want {
			t.Errorf("policy %q follows=%v: sent=%v want=%v", tc.policy, tc.follows, sent, tc.want)
		}
	}
}

func TestDeliverSubscriptionDeletesDeadSubscription(t *testing.T) {
	for _, status := range []int{400, 404, 410} {
		store := fixture()
		sender := &fakeSender{status: status}
		service := NewService(store, sender, testConfig(), nil)
		if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
		if len(store.deletedSubscriptions) != 1 || store.deletedSubscriptions[0] != 7 {
			t.Errorf("status %d: expected subscription 7 deleted, got %v", status, store.deletedSubscriptions)
		}
	}
}

func TestDeliverSubscriptionRetriesOnTransientErrors(t *testing.T) {
	for _, status := range []int{408, 429, 500, 503} {
		store := fixture()
		sender := &fakeSender{status: status}
		service := NewService(store, sender, testConfig(), nil)
		err := service.DeliverSubscription(context.Background(), 7, 500)
		if err == nil {
			t.Errorf("status %d: expected error for retry", status)
		}
		if len(store.deletedSubscriptions) != 0 {
			t.Errorf("status %d: subscription must not be deleted", status)
		}
	}
}

func TestDeliverSubscriptionSkipsMissingRows(t *testing.T) {
	store := fixture()
	sender := &fakeSender{status: 200}
	service := NewService(store, sender, testConfig(), nil)

	if err := service.DeliverSubscription(context.Background(), 999, 500); err != nil {
		t.Errorf("missing subscription: %v", err)
	}
	if err := service.DeliverSubscription(context.Background(), 7, 999); err != nil {
		t.Errorf("missing notification: %v", err)
	}
	if len(sender.payloads) != 0 {
		t.Errorf("expected no sends, got %d", len(sender.payloads))
	}
}

func TestDeliverSubscriptionSkipsDeletedStatus(t *testing.T) {
	store := fixture()
	delete(store.statuses, 100)

	sender := &fakeSender{status: 200}
	service := NewService(store, sender, testConfig(), nil)
	if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
		t.Fatalf("DeliverSubscription: %v", err)
	}
	if len(sender.payloads) != 0 {
		t.Errorf("expected no send for deleted target status, got %d", len(sender.payloads))
	}
}

func TestDeliverSubscriptionSkipsOversizedPayload(t *testing.T) {
	store := fixture()
	sender := &fakeSender{err: ErrPayloadTooLarge}
	service := NewService(store, sender, testConfig(), nil)

	if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
		t.Errorf("oversized payload must not be retried, got %v", err)
	}
	if len(store.deletedSubscriptions) != 0 {
		t.Errorf("oversized payload must not delete the subscription")
	}
}

func TestDeliverSubscriptionSkipsStaleNotification(t *testing.T) {
	store := fixture()
	notification := store.notifications[500]
	notification.UpdatedAt = pgtype.Timestamptz{Time: time.Now().Add(-72 * time.Hour), Valid: true}
	store.notifications[500] = notification

	sender := &fakeSender{status: 200}
	service := NewService(store, sender, testConfig(), nil)
	if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
		t.Fatalf("DeliverSubscription: %v", err)
	}
	if len(sender.payloads) != 0 {
		t.Errorf("expected stale notification to be skipped")
	}
}

func TestDeliverSubscriptionDropsKeylessSubscription(t *testing.T) {
	store := fixture()
	sub := store.subscriptions[7]
	sub.KeyAuth = pgtype.Text{}
	store.subscriptions[7] = sub

	sender := &fakeSender{status: 200}
	service := NewService(store, sender, testConfig(), nil)
	if err := service.DeliverSubscription(context.Background(), 7, 500); err != nil {
		t.Fatalf("DeliverSubscription: %v", err)
	}
	if len(store.deletedSubscriptions) != 1 {
		t.Errorf("expected keyless subscription to be deleted")
	}
	if len(sender.payloads) != 0 {
		t.Errorf("expected no send for keyless subscription")
	}
}

func TestNotificationTitles(t *testing.T) {
	cases := map[string]string{
		"mention":        "You were mentioned by Alice",
		"follow":         "Alice is now following you",
		"follow_request": "Pending follower: Alice",
		"favourite":      "Alice favorited your post",
		"reblog":         "Alice boosted your post",
		"quote":          "Alice quoted your post",
		"poll":           "A poll by Alice has ended",
		"status":         "Alice just posted",
		"update":         "Alice edited a post",
		"admin.sign_up":  "Alice signed up",
		"admin.report":   "Alice submitted a report",
	}
	for notificationType, want := range cases {
		if got := notificationTitle(notificationType, "Alice"); got != want {
			t.Errorf("title(%s) = %q, want %q", notificationType, got, want)
		}
	}
}

func TestTruncateBody(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := truncateBody(long, 140)
	if len([]rune(got)) != 140 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncateBody length = %d, suffix ok = %v", len([]rune(got)), strings.HasSuffix(got, "..."))
	}
	if truncateBody("short", 140) != "short" {
		t.Error("short strings must be untouched")
	}
}

func TestStripTags(t *testing.T) {
	got := stripTags(`<p>Hello <a href="https://example.com">&amp; world</a></p>`)
	if got != "Hello & world" {
		t.Errorf("stripTags = %q", got)
	}
}

package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

// Store is the subset of db.Querier the push subsystem needs.
type Store interface {
	GetPushSubscriptionByID(ctx context.Context, id int64) (db.WebPushSubscription, error)
	ListPushSubscriptionsByUserID(ctx context.Context, userID pgtype.Int8) ([]db.WebPushSubscription, error)
	DeletePushSubscriptionByID(ctx context.Context, id int64) error
	GetNotificationByID(ctx context.Context, id int64) (db.Notification, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	GetOAuthAccessTokenByID(ctx context.Context, id int64) (db.OauthAccessToken, error)
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	GetUserByID(ctx context.Context, id int64) (db.User, error)
	GetFollow(ctx context.Context, arg db.GetFollowParams) (db.Follow, error)
}

// Deliverer hands a per-subscription delivery to the worker queue (or an
// in-process fallback).
type Deliverer interface {
	DeliverPush(ctx context.Context, subscriptionID, notificationID int64) error
}

// notificationPayload mirrors Mastodon's Web::NotificationSerializer.
type notificationPayload struct {
	AccessToken      string `json:"access_token"`
	PreferredLocale  string `json:"preferred_locale"`
	NotificationID   int64  `json:"notification_id"`
	NotificationType string `json:"notification_type"`
	Icon             string `json:"icon"`
	Title            string `json:"title"`
	Body             string `json:"body"`
}

// Service builds Mastodon-shaped push payloads and delivers them.
type Service struct {
	store  Store
	sender Sender
	cfg    *config.Config
	logger *slog.Logger
}

// NewService creates the push delivery service.
func NewService(store Store, sender Sender, cfg *config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, sender: sender, cfg: cfg, logger: logger}
}

// DeliverSubscription sends one notification to one subscription. It mirrors
// Mastodon's Web::PushNotificationWorker: stale notifications and deleted
// rows are skipped, alert/policy preferences are re-checked, and a 4xx
// response (other than 408/429) deletes the dead subscription.
func (s *Service) DeliverSubscription(ctx context.Context, subscriptionID, notificationID int64) error {
	sub, err := s.store.GetPushSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("loading push subscription %d: %w", subscriptionID, err)
	}
	notification, err := s.store.GetNotificationByID(ctx, notificationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("loading notification %d: %w", notificationID, err)
	}

	if notification.UpdatedAt.Valid && time.Since(notification.UpdatedAt.Time) > pushTTL {
		return nil
	}

	// Subscriptions stored before key validation may be unusable; drop them
	// like Mastodon does.
	if strings.TrimSpace(sub.KeyP256dh.String) == "" || strings.TrimSpace(sub.KeyAuth.String) == "" {
		return s.store.DeletePushSubscriptionByID(ctx, sub.ID)
	}

	pushable, err := s.pushable(ctx, sub, notification)
	if err != nil {
		return err
	}
	if !pushable {
		return nil
	}

	payload, err := s.buildPayload(ctx, sub, notification)
	if err != nil {
		// The sender account or target status can be deleted between
		// notification creation and delivery; that is permanent, not worth
		// retrying.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	status, err := s.sender.Send(ctx, Subscription{
		Endpoint: sub.Endpoint,
		P256dh:   sub.KeyP256dh.String,
		Auth:     sub.KeyAuth.String,
	}, payload)
	if err != nil {
		// An oversized payload cannot shrink on retry; drop this delivery
		// and keep the subscription.
		if errors.Is(err, ErrPayloadTooLarge) {
			s.logger.Warn("Push payload too large, skipping delivery", "subscription_id", sub.ID, "notification_id", notification.ID)
			return nil
		}
		return err
	}

	switch {
	case status >= 200 && status < 300:
		return nil
	case status >= 400 && status < 500 && status != 408 && status != 429:
		// Invalid or expired subscription (404, 410, ...): remove it.
		if err := s.store.DeletePushSubscriptionByID(ctx, sub.ID); err != nil {
			return fmt.Errorf("deleting dead subscription %d: %w", sub.ID, err)
		}
		s.logger.Info("Removed dead push subscription", "subscription_id", sub.ID, "status", status)
		return nil
	default:
		return fmt.Errorf("push endpoint returned unexpected status %d", status)
	}
}

func (s *Service) buildPayload(ctx context.Context, sub db.WebPushSubscription, notification db.Notification) ([]byte, error) {
	fromAccount, err := s.store.GetAccountByID(ctx, notification.FromAccountID)
	if err != nil {
		return nil, fmt.Errorf("loading sender account: %w", err)
	}

	accessToken := ""
	if sub.AccessTokenID.Valid {
		if token, err := s.store.GetOAuthAccessTokenByID(ctx, sub.AccessTokenID.Int64); err == nil {
			accessToken = token.Token
		}
	}

	locale := "en"
	if sub.UserID.Valid {
		if user, err := s.store.GetUserByID(ctx, sub.UserID.Int64); err == nil {
			if l := strings.TrimSpace(user.Locale.String); l != "" {
				locale = l
			}
		}
	}

	// Body: target status spoiler text or text, falling back to the sender's
	// bio, stripped of HTML and truncated to 140 characters. A deleted target
	// status aborts the delivery (Mastodon skips when the activity is gone).
	bodySource := fromAccount.Note
	if notification.ActivityType == "Status" {
		status, err := s.store.GetStatusByID(ctx, notification.ActivityID)
		if err != nil {
			return nil, fmt.Errorf("loading target status: %w", err)
		}
		if spoiler := strings.TrimSpace(status.SpoilerText); spoiler != "" {
			bodySource = spoiler
		} else {
			bodySource = status.Text
		}
	}

	notificationType := notification.Type.String

	return json.Marshal(notificationPayload{
		AccessToken:      accessToken,
		PreferredLocale:  locale,
		NotificationID:   notification.ID,
		NotificationType: notificationType,
		Icon:             s.avatarURL(fromAccount),
		Title:            notificationTitle(notificationType, displayName(fromAccount)),
		Body:             truncateBody(stripTags(bodySource), 140),
	})
}

// pushable mirrors Web::PushSubscription#pushable?: the subscription's alert
// preference for the notification type must be enabled and its policy must
// allow the sender.
func (s *Service) pushable(ctx context.Context, sub db.WebPushSubscription, notification db.Notification) (bool, error) {
	data := subscriptionData(sub.Data)
	if !data.Alerts[notification.Type.String] {
		return false, nil
	}

	switch data.Policy {
	case "", "all":
		return true, nil
	case "none":
		return false, nil
	case "followed":
		return s.follows(ctx, notification.AccountID, notification.FromAccountID)
	case "follower":
		return s.follows(ctx, notification.FromAccountID, notification.AccountID)
	default:
		return false, nil
	}
}

func (s *Service) follows(ctx context.Context, accountID, targetAccountID int64) (bool, error) {
	_, err := s.store.GetFollow(ctx, db.GetFollowParams{
		AccountID:       accountID,
		TargetAccountID: targetAccountID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Service) avatarURL(account db.Account) string {
	domain := ""
	if s.cfg != nil {
		domain = s.cfg.Instance.PublicWebDomain()
	}
	value := strings.TrimSpace(account.AvatarRemoteUrl.String)
	if value == "" {
		value = strings.TrimSpace(account.AvatarFileName.String)
	}
	if value == "" {
		value = "/avatars/original/missing.png"
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	if !strings.HasPrefix(value, "/") {
		value = "/files/" + value
	}
	if domain == "" {
		return value
	}
	return "https://" + domain + value
}

type pushSubscriptionData struct {
	Alerts map[string]bool `json:"alerts"`
	Policy string          `json:"policy"`
}

func subscriptionData(raw []byte) pushSubscriptionData {
	data := pushSubscriptionData{Alerts: map[string]bool{}}
	if len(raw) == 0 {
		return data
	}
	_ = json.Unmarshal(raw, &data)
	if data.Alerts == nil {
		data.Alerts = map[string]bool{}
	}
	return data
}

func displayName(account db.Account) string {
	if name := strings.TrimSpace(account.DisplayName); name != "" {
		return name
	}
	return account.Username
}

// notificationTitle returns the English notification_mailer subject for the
// type, matching Mastodon's Web::NotificationSerializer#title.
func notificationTitle(notificationType, name string) string {
	switch notificationType {
	case "mention":
		return fmt.Sprintf("You were mentioned by %s", name)
	case "follow":
		return fmt.Sprintf("%s is now following you", name)
	case "follow_request":
		return fmt.Sprintf("Pending follower: %s", name)
	case "favourite":
		return fmt.Sprintf("%s favorited your post", name)
	case "reblog":
		return fmt.Sprintf("%s boosted your post", name)
	case "quote":
		return fmt.Sprintf("%s quoted your post", name)
	case "poll":
		return fmt.Sprintf("A poll by %s has ended", name)
	case "status":
		return fmt.Sprintf("%s just posted", name)
	case "update":
		return fmt.Sprintf("%s edited a post", name)
	case "admin.sign_up":
		return fmt.Sprintf("%s signed up", name)
	case "admin.report":
		return fmt.Sprintf("%s submitted a report", name)
	default:
		return name
	}
}

var tagPattern = regexp.MustCompile(`<[^>]*>`)

func stripTags(value string) string {
	value = strings.ReplaceAll(value, "</p>", "\n\n")
	value = strings.ReplaceAll(value, "<br>", "\n")
	value = strings.ReplaceAll(value, "<br/>", "\n")
	value = strings.ReplaceAll(value, "<br />", "\n")
	value = tagPattern.ReplaceAllString(value, "")
	return strings.TrimSpace(html.UnescapeString(value))
}

// truncateBody shortens to at most length characters, ending with "..." like
// Rails' truncate helper.
func truncateBody(value string, length int) string {
	runes := []rune(value)
	if len(runes) <= length {
		return value
	}
	return strings.TrimRight(string(runes[:length-3]), " ") + "..."
}

package streaming

import "context"

// Publisher defines the interface for publishing events
type Publisher interface {
	PublishStatusToRedis(ctx context.Context, status any, streams []string) error
	PublishDeleteToRedis(ctx context.Context, statusID string, streams []string) error
	PublishNotificationToRedis(ctx context.Context, accountID int64, notification any) error
}

// EventPublisher publishes arbitrary streaming events.
type EventPublisher interface {
	PublishEventToRedis(ctx context.Context, eventType string, payload any, streams []string) error
}

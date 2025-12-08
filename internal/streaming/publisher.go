package streaming

import "context"

// Publisher defines the interface for publishing events
type Publisher interface {
	PublishStatusToRedis(ctx context.Context, status any, streams []string) error
	PublishDeleteToRedis(ctx context.Context, statusID string, streams []string) error
	PublishNotificationToRedis(ctx context.Context, accountID int64, notification any) error
}

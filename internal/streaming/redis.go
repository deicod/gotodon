// Package streaming provides Redis pub/sub integration for streaming.
package streaming

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RedisHub wraps Hub with Redis pub/sub for distributed streaming
type RedisHub struct {
	*Hub
	client *redis.Client
	pubsub *redis.PubSub
	prefix string
	ctx    context.Context
	cancel context.CancelFunc
}

// NewRedisHub creates a new Redis-backed streaming hub
func NewRedisHub(client *redis.Client, prefix string) *RedisHub {
	ctx, cancel := context.WithCancel(context.Background())
	return &RedisHub{
		Hub:    NewHub(),
		client: client,
		prefix: prefix,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start starts the Redis hub with pattern subscription
func (h *RedisHub) Start(ctx context.Context) error {
	// Subscribe to all streaming channels with pattern
	pattern := h.prefix + ":stream:*"
	h.pubsub = h.client.PSubscribe(ctx, pattern)

	// Wait for confirmation
	_, err := h.pubsub.Receive(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to Redis: %w", err)
	}

	// Start the base hub
	go h.Run(ctx)

	// Start Redis message handler
	go h.handleRedisMessages(ctx)

	return nil
}

// Stop stops the Redis hub
func (h *RedisHub) Stop() error {
	h.cancel()
	if h.pubsub != nil {
		return h.pubsub.Close()
	}
	return nil
}

// handleRedisMessages handles incoming Redis messages
func (h *RedisHub) handleRedisMessages(ctx context.Context) {
	ch := h.pubsub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			h.handleRedisMessage(msg)
		}
	}
}

// handleRedisMessage processes a single Redis message
func (h *RedisHub) handleRedisMessage(msg *redis.Message) {
	var event Event
	if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
		return
	}

	// Extract stream name from channel
	// Channel format: prefix:stream:streamname
	stream := extractStreamFromChannel(msg.Channel, h.prefix)

	// Broadcast to local subscribers
	h.Broadcast([]string{stream}, &event)
}

// PublishToRedis publishes an event to Redis for distribution
func (h *RedisHub) PublishToRedis(ctx context.Context, stream string, event *Event) error {
	channel := fmt.Sprintf("%s:stream:%s", h.prefix, stream)
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return h.client.Publish(ctx, channel, payload).Err()
}

// PublishEventToRedis publishes an event to Redis for distribution.
func (h *RedisHub) PublishEventToRedis(ctx context.Context, eventType string, payload any, streams []string) error {
	payloadStr, err := encodePayload(payload)
	if err != nil {
		return err
	}
	event := &Event{
		Event:   eventType,
		Payload: payloadStr,
	}

	var lastErr error
	for _, stream := range streams {
		if err := h.PublishToRedis(ctx, stream, event); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// PublishStatusToRedis publishes a status to Redis
func (h *RedisHub) PublishStatusToRedis(ctx context.Context, status any, streams []string) error {
	return h.PublishEventToRedis(ctx, EventUpdate, status, streams)
}

// PublishDeleteToRedis publishes a deletion to Redis
func (h *RedisHub) PublishDeleteToRedis(ctx context.Context, statusID string, streams []string) error {
	return h.PublishEventToRedis(ctx, EventDelete, statusID, streams)
}

// PublishNotificationToRedis publishes a notification via Redis
func (h *RedisHub) PublishNotificationToRedis(ctx context.Context, accountID int64, notification any) error {
	return h.PublishEventToRedis(ctx, EventNotification, notification, []string{UserNotificationStreamKey(accountID)})
}

func extractStreamFromChannel(channel, prefix string) string {
	// Channel format: prefix:stream:streamname
	// We want to extract streamname
	prefixLen := len(prefix) + len(":stream:")
	if len(channel) > prefixLen {
		return channel[prefixLen:]
	}
	return ""
}

func encodePayload(payload any) (string, error) {
	switch value := payload.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	default:
		data, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

// RedisTimeline tracks timeline state in Redis
type RedisTimeline struct {
	client *redis.Client
	prefix string
}

// NewRedisTimeline creates a new Redis timeline tracker
func NewRedisTimeline(client *redis.Client, prefix string) *RedisTimeline {
	return &RedisTimeline{
		client: client,
		prefix: prefix,
	}
}

// AddToTimeline adds a status ID to a timeline
func (t *RedisTimeline) AddToTimeline(ctx context.Context, timeline string, statusID string, score float64) error {
	key := fmt.Sprintf("%s:timeline:%s", t.prefix, timeline)
	return t.client.ZAdd(ctx, key, redis.Z{Score: score, Member: statusID}).Err()
}

// RemoveFromTimeline removes a status from a timeline
func (t *RedisTimeline) RemoveFromTimeline(ctx context.Context, timeline string, statusID string) error {
	key := fmt.Sprintf("%s:timeline:%s", t.prefix, timeline)
	return t.client.ZRem(ctx, key, statusID).Err()
}

// GetTimeline gets status IDs from a timeline
func (t *RedisTimeline) GetTimeline(ctx context.Context, timeline string, limit int64) ([]string, error) {
	key := fmt.Sprintf("%s:timeline:%s", t.prefix, timeline)
	return t.client.ZRevRange(ctx, key, 0, limit-1).Result()
}

// TrimTimeline trims a timeline to a maximum size
func (t *RedisTimeline) TrimTimeline(ctx context.Context, timeline string, maxSize int64) error {
	key := fmt.Sprintf("%s:timeline:%s", t.prefix, timeline)
	return t.client.ZRemRangeByRank(ctx, key, 0, -maxSize-1).Err()
}

// HomeTimelineKey returns the key for a user's home timeline
func HomeTimelineKey(accountID int64) string {
	return fmt.Sprintf("home:%d", accountID)
}

// PublicTimelineKey returns the key for the public timeline
func PublicTimelineKey() string {
	return "public"
}

// ListTimelineKey returns the key for a list timeline
func ListTimelineKey(listID int64) string {
	return fmt.Sprintf("list:%d", listID)
}

package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultRateLimitKeyPrefix namespaces RedisRateLimitStore keys.
const DefaultRateLimitKeyPrefix = "gotodon:rate_limit:"

// A window is a counter that expires when the window ends. The first hit
// creates it and sets its expiry; the expiry is measured on Redis's clock,
// so instances whose clocks drift apart share windows. A counter that lost
// its expiry is given a fresh one rather than living forever.
var redisRateLimitHit = redis.NewScript(`
local key, period = KEYS[1], tonumber(ARGV[1])
local count = redis.call('INCR', key)
local ttl = redis.call('PTTL', key)
if count == 1 or ttl < 0 then
  redis.call('PEXPIRE', key, period)
  ttl = period
end
return {count, ttl}
`)

// RedisRateLimitStore is a RateLimitStore kept in Redis, shared by all
// server instances using the same Redis database.
type RedisRateLimitStore struct {
	client redis.Scripter
	prefix string
}

// NewRedisRateLimitStore creates a Redis-backed store whose keys start with
// prefix; an empty prefix selects DefaultRateLimitKeyPrefix.
func NewRedisRateLimitStore(client redis.Scripter, prefix string) *RedisRateLimitStore {
	if prefix == "" {
		prefix = DefaultRateLimitKeyPrefix
	}
	return &RedisRateLimitStore{client: client, prefix: prefix}
}

// Hit implements RateLimitStore.
func (s *RedisRateLimitStore) Hit(ctx context.Context, key string, period time.Duration) (int64, time.Duration, error) {
	result, err := redisRateLimitHit.Run(ctx, s.client, []string{s.prefix + key}, max(period.Milliseconds(), 1)).Int64Slice()
	if err != nil {
		return 0, 0, fmt.Errorf("count rate limited attempt: %w", err)
	}
	if len(result) != 2 {
		return 0, 0, fmt.Errorf("count rate limited attempt: unexpected reply %v", result)
	}
	return result[0], time.Duration(result[1]) * time.Millisecond, nil
}

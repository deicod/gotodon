package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultInboxReplayKeyPrefix namespaces RedisInboxReplayStore keys.
const DefaultInboxReplayKeyPrefix = "gotodon:inbox_replay:"

// Replay entries hold "claim:<token>" while a delivery is processed and
// "done" afterwards; TTLs only ever grow so retention is never cut short.
var (
	redisReplayClaim = redis.NewScript(`
local state = redis.call('GET', KEYS[1])
if not state then
  redis.call('SET', KEYS[1], 'claim:' .. ARGV[1], 'PX', ARGV[2])
  return 0
end
if state == 'done' then
  if redis.call('PTTL', KEYS[1]) < tonumber(ARGV[3]) then
    redis.call('PEXPIRE', KEYS[1], ARGV[3])
  end
  return 2
end
return 1
`)

	redisReplayRenew = redis.NewScript(`
if redis.call('GET', KEYS[1]) == 'claim:' .. ARGV[1] then
  if redis.call('PTTL', KEYS[1]) < tonumber(ARGV[2]) then
    redis.call('PEXPIRE', KEYS[1], ARGV[2])
  end
  return 1
end
return 0
`)

	redisReplayComplete = redis.NewScript(`
local retain = tonumber(ARGV[1])
local ttl = redis.call('PTTL', KEYS[1])
if ttl > retain then
  retain = ttl
end
redis.call('SET', KEYS[1], 'done', 'PX', retain)
return 1
`)

	redisReplayRelease = redis.NewScript(`
if redis.call('GET', KEYS[1]) == 'claim:' .. ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)
)

// RedisInboxReplayStore is an InboxReplayStore kept in Redis, shared by all
// server instances using the same Redis database.
type RedisInboxReplayStore struct {
	client redis.Scripter
	prefix string
}

// NewRedisInboxReplayStore creates a Redis-backed store whose keys start with
// prefix; an empty prefix selects DefaultInboxReplayKeyPrefix.
func NewRedisInboxReplayStore(client redis.Scripter, prefix string) *RedisInboxReplayStore {
	if prefix == "" {
		prefix = DefaultInboxReplayKeyPrefix
	}
	return &RedisInboxReplayStore{client: client, prefix: prefix}
}

// Claim implements InboxReplayStore.
func (s *RedisInboxReplayStore) Claim(ctx context.Context, key, token string, lease, retain time.Duration) (ReplayState, error) {
	result, err := redisReplayClaim.Run(ctx, s.client, []string{s.prefix + key}, token, redisMillis(lease), redisMillis(retain)).Int()
	if err != nil {
		return 0, fmt.Errorf("claim inbox delivery: %w", err)
	}
	switch ReplayState(result) {
	case ReplayClaimed, ReplayInFlight, ReplayProcessed:
		return ReplayState(result), nil
	}
	return 0, fmt.Errorf("claim inbox delivery: unexpected state %d", result)
}

// Renew implements InboxReplayStore.
func (s *RedisInboxReplayStore) Renew(ctx context.Context, key, token string, lease time.Duration) (bool, error) {
	held, err := redisReplayRenew.Run(ctx, s.client, []string{s.prefix + key}, token, redisMillis(lease)).Int()
	if err != nil {
		return false, fmt.Errorf("renew inbox delivery claim: %w", err)
	}
	return held == 1, nil
}

// Complete implements InboxReplayStore.
func (s *RedisInboxReplayStore) Complete(ctx context.Context, key string, retain time.Duration) error {
	if err := redisReplayComplete.Run(ctx, s.client, []string{s.prefix + key}, redisMillis(retain)).Err(); err != nil {
		return fmt.Errorf("complete inbox delivery: %w", err)
	}
	return nil
}

// Release implements InboxReplayStore.
func (s *RedisInboxReplayStore) Release(ctx context.Context, key, token string) error {
	if err := redisReplayRelease.Run(ctx, s.client, []string{s.prefix + key}, token).Err(); err != nil {
		return fmt.Errorf("release inbox delivery: %w", err)
	}
	return nil
}

// redisMillis converts d to a positive millisecond TTL; Redis rejects zero
// and negative expirations.
func redisMillis(d time.Duration) int64 {
	if ms := d.Milliseconds(); ms > 0 {
		return ms
	}
	return 1
}

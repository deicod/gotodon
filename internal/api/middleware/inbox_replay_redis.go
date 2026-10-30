package middleware

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultInboxReplayKeyPrefix namespaces RedisInboxReplayStore keys.
const DefaultInboxReplayKeyPrefix = "gotodon:inbox_replay:"

// Replay entries are hashes. "token" and "lease" hold the claim token and the
// end of its lease while a delivery is claimed, "done" marks a processed
// delivery and "retain" holds the latest retention deadline. Times are Unix
// milliseconds from the callers' clocks, stored exactly as passed in. A key
// expires once both its lease and its retention deadline have passed, and
// expirations are only ever extended.
var (
	redisReplayClaim = redis.NewScript(`
local key = KEYS[1]
local token, lease, retain, now = ARGV[1], ARGV[2], ARGV[3], tonumber(ARGV[4])
local f = redis.call('HMGET', key, 'token', 'lease', 'done', 'retain')
if not f[4] or tonumber(retain) > tonumber(f[4]) then
  redis.call('HSET', key, 'retain', retain)
end
local state = 0
if f[3] then
  state = 2
elseif f[1] and tonumber(f[2] or '0') > now then
  state = 1
else
  redis.call('HSET', key, 'token', token, 'lease', lease)
end
local g = redis.call('HMGET', key, 'lease', 'retain')
local ttl = math.max(tonumber(g[1] or '0'), tonumber(g[2] or '0')) - now
local current = redis.call('PTTL', key)
if current < 0 or ttl > current then
  redis.call('PEXPIRE', key, math.max(ttl, 1))
end
return state
`)

	redisReplayRenew = redis.NewScript(`
local key, lease, now = KEYS[1], ARGV[2], tonumber(ARGV[3])
local f = redis.call('HMGET', key, 'token', 'lease', 'done')
if f[3] or f[1] ~= ARGV[1] or tonumber(f[2] or '0') <= now then
  return 0
end
if tonumber(lease) > tonumber(f[2]) then
  redis.call('HSET', key, 'lease', lease)
  local ttl = tonumber(lease) - now
  if ttl > redis.call('PTTL', key) then
    redis.call('PEXPIRE', key, ttl)
  end
end
return 1
`)

	redisReplayComplete = redis.NewScript(`
local key, retain, now = KEYS[1], ARGV[1], tonumber(ARGV[2])
local stored = redis.call('HGET', key, 'retain')
if not stored or tonumber(retain) > tonumber(stored) then
  redis.call('HSET', key, 'retain', retain)
  stored = retain
end
redis.call('HSET', key, 'done', '1')
redis.call('HDEL', key, 'token', 'lease')
local ttl = tonumber(stored) - now
local current = redis.call('PTTL', key)
if current < 0 or ttl > current then
  redis.call('PEXPIRE', key, math.max(ttl, 1))
end
return 1
`)

	redisReplayRelease = redis.NewScript(`
local key, now = KEYS[1], tonumber(ARGV[2])
local f = redis.call('HMGET', key, 'token', 'done', 'retain')
if f[2] or f[1] ~= ARGV[1] then
  return 0
end
local ttl = tonumber(f[3] or '0') - now
if ttl <= 0 then
  redis.call('DEL', key)
else
  redis.call('HDEL', key, 'token', 'lease')
  redis.call('PEXPIRE', key, ttl)
end
return 1
`)
)

// RedisInboxReplayStore is an InboxReplayStore kept in Redis, shared by all
// server instances using the same Redis database.
type RedisInboxReplayStore struct {
	client redis.Scripter
	prefix string
	now    func() time.Time
}

// NewRedisInboxReplayStore creates a Redis-backed store whose keys start with
// prefix; an empty prefix selects DefaultInboxReplayKeyPrefix.
func NewRedisInboxReplayStore(client redis.Scripter, prefix string) *RedisInboxReplayStore {
	if prefix == "" {
		prefix = DefaultInboxReplayKeyPrefix
	}
	return &RedisInboxReplayStore{client: client, prefix: prefix, now: time.Now}
}

// Claim implements InboxReplayStore.
func (s *RedisInboxReplayStore) Claim(ctx context.Context, key, token string, lease time.Duration, retainUntil time.Time) (ReplayState, error) {
	now := s.now()
	result, err := redisReplayClaim.Run(ctx, s.client, []string{s.prefix + key},
		token, unixMillis(now.Add(lease)), unixMillis(retainUntil), unixMillis(now)).Int()
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
	now := s.now()
	held, err := redisReplayRenew.Run(ctx, s.client, []string{s.prefix + key},
		token, unixMillis(now.Add(lease)), unixMillis(now)).Int()
	if err != nil {
		return false, fmt.Errorf("renew inbox delivery claim: %w", err)
	}
	return held == 1, nil
}

// Complete implements InboxReplayStore.
func (s *RedisInboxReplayStore) Complete(ctx context.Context, key string, retainUntil time.Time) error {
	err := redisReplayComplete.Run(ctx, s.client, []string{s.prefix + key},
		unixMillis(retainUntil), unixMillis(s.now())).Err()
	if err != nil {
		return fmt.Errorf("complete inbox delivery: %w", err)
	}
	return nil
}

// Release implements InboxReplayStore.
func (s *RedisInboxReplayStore) Release(ctx context.Context, key, token string) error {
	err := redisReplayRelease.Run(ctx, s.client, []string{s.prefix + key}, token, unixMillis(s.now())).Err()
	if err != nil {
		return fmt.Errorf("release inbox delivery: %w", err)
	}
	return nil
}

// unixMillis formats t as a decimal Unix millisecond timestamp, the exact
// string the scripts store.
func unixMillis(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}

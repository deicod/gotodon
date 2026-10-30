package middleware

import (
	"context"
	"crypto/rand"
	"net"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// newTestRedisReplayStore returns a store on the Redis at REDIS_HOST and
// REDIS_PORT under a prefix unique to the test, and the client behind it.
func newTestRedisReplayStore(t *testing.T) (*RedisInboxReplayStore, *redis.Client, string) {
	t.Helper()
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		t.Skip("REDIS_HOST not set; skipping Redis replay store test")
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}

	client := redis.NewClient(&redis.Options{
		Addr:     net.JoinHostPort(host, port),
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis ping: %v", err)
	}

	prefix := "gotodon:test:inbox_replay:" + rand.Text() + ":"
	t.Cleanup(func() {
		keys, err := client.Keys(ctx, prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			client.Del(ctx, keys...)
		}
		_ = client.Close()
	})
	return NewRedisInboxReplayStore(client, prefix), client, prefix
}

func TestRedisInboxReplayStore(t *testing.T) {
	store, _, _ := newTestRedisReplayStore(t)
	testInboxReplayStore(t, store)
}

func TestRedisInboxReplayStore_Expiry(t *testing.T) {
	store, client, prefix := newTestRedisReplayStore(t)
	ctx := context.Background()

	ttl := func(key string) time.Duration {
		t.Helper()
		d, err := client.PTTL(ctx, prefix+key).Result()
		if err != nil {
			t.Fatalf("PTTL(%s) error = %v", key, err)
		}
		return d
	}
	claim := func(key, token string, lease, retain time.Duration, want ReplayState) {
		t.Helper()
		got, err := store.Claim(ctx, key, token, lease, retain)
		if err != nil {
			t.Fatalf("Claim(%s) error = %v", key, err)
		}
		if got != want {
			t.Fatalf("Claim(%s, %s) = %v, want %v", key, token, got, want)
		}
	}

	t.Run("claims carry the lease", func(t *testing.T) {
		claim("lease", "t1", 2*time.Minute, time.Hour, ReplayClaimed)
		if d := ttl("lease"); d <= time.Minute || d > 2*time.Minute {
			t.Fatalf("claim TTL = %v, want about 2m", d)
		}
	})

	t.Run("abandoned claim expires after its lease", func(t *testing.T) {
		claim("abandoned", "t1", 50*time.Millisecond, time.Hour, ReplayClaimed)
		time.Sleep(150 * time.Millisecond)
		claim("abandoned", "t2", time.Minute, time.Hour, ReplayClaimed)

		// The abandoned claim's token no longer releases the key.
		if err := store.Release(ctx, "abandoned", "t1"); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		claim("abandoned", "t3", time.Minute, time.Hour, ReplayInFlight)
	})

	t.Run("retention only grows", func(t *testing.T) {
		claim("retain", "t1", time.Minute, time.Hour, ReplayClaimed)
		if err := store.Complete(ctx, "retain", time.Hour); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if d := ttl("retain"); d <= 59*time.Minute || d > time.Hour {
			t.Fatalf("completed TTL = %v, want about 1h", d)
		}

		claim("retain", "t2", time.Minute, 2*time.Hour, ReplayProcessed)
		if d := ttl("retain"); d <= 119*time.Minute || d > 2*time.Hour {
			t.Fatalf("TTL after replay = %v, want about 2h", d)
		}

		claim("retain", "t3", time.Minute, time.Minute, ReplayProcessed)
		if err := store.Complete(ctx, "retain", time.Minute); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if d := ttl("retain"); d <= 119*time.Minute {
			t.Fatalf("TTL after shorter retention = %v, want about 2h", d)
		}
	})

	t.Run("non-positive durations still expire", func(t *testing.T) {
		claim("zero", "t1", 0, 0, ReplayClaimed)
		time.Sleep(50 * time.Millisecond)
		if n, err := client.Exists(ctx, prefix+"zero").Result(); err != nil || n != 0 {
			t.Fatalf("Exists = %d, %v; want the key expired", n, err)
		}
	})
}

func TestRedisMillis(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want int64
	}{
		{in: 2 * time.Second, want: 2000},
		{in: 1500 * time.Microsecond, want: 1},
		{in: 0, want: 1},
		{in: -time.Minute, want: 1},
	}
	for _, tt := range tests {
		if got := redisMillis(tt.in); got != tt.want {
			t.Errorf("redisMillis(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

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
	// leaseLeft is how long the claim on key has left; the key itself lives
	// until its retention deadline as well.
	leaseLeft := func(key string) time.Duration {
		t.Helper()
		ms, err := client.HGet(ctx, prefix+key, "lease").Int64()
		if err != nil {
			t.Fatalf("HGET %s lease error = %v", key, err)
		}
		return time.Until(time.UnixMilli(ms))
	}
	claim := func(key, token string, lease, retain time.Duration, want ReplayState) {
		t.Helper()
		got, err := store.Claim(ctx, key, token, lease, time.Now().Add(retain))
		if err != nil {
			t.Fatalf("Claim(%s) error = %v", key, err)
		}
		if got != want {
			t.Fatalf("Claim(%s, %s) = %v, want %v", key, token, got, want)
		}
	}

	t.Run("claims carry the lease", func(t *testing.T) {
		claim("lease", "t1", 2*time.Minute, time.Hour, ReplayClaimed)
		if d := leaseLeft("lease"); d <= time.Minute || d > 2*time.Minute {
			t.Fatalf("lease left = %v, want about 2m", d)
		}
		if d := ttl("lease"); d <= 59*time.Minute || d > time.Hour {
			t.Fatalf("claim TTL = %v, want about the 1h retention", d)
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

	t.Run("renewal extends the lease", func(t *testing.T) {
		claim("renew", "t1", 100*time.Millisecond, time.Hour, ReplayClaimed)
		held, err := store.Renew(ctx, "renew", "t1", 2*time.Minute)
		if err != nil || !held {
			t.Fatalf("Renew() = %v, %v, want held", held, err)
		}
		if d := leaseLeft("renew"); d <= time.Minute || d > 2*time.Minute {
			t.Fatalf("renewed lease left = %v, want about 2m", d)
		}
		time.Sleep(200 * time.Millisecond)
		claim("renew", "t2", time.Minute, time.Hour, ReplayInFlight)

		if held, err := store.Renew(ctx, "renew", "t2", 2*time.Minute); err != nil || held {
			t.Fatalf("Renew(other token) = %v, %v, want false", held, err)
		}
	})

	t.Run("in-flight duplicates extend retention", func(t *testing.T) {
		claim("inflight", "t1", time.Minute, 10*time.Minute, ReplayClaimed)
		claim("inflight", "t2", time.Minute, 70*time.Minute, ReplayInFlight)
		// The duplicate's deadline does not extend the claim itself.
		if d := leaseLeft("inflight"); d > time.Minute {
			t.Fatalf("lease left = %v, want at most the 1m lease", d)
		}
		if err := store.Complete(ctx, "inflight", time.Now().Add(10*time.Minute)); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if d := ttl("inflight"); d <= 69*time.Minute || d > 70*time.Minute {
			t.Fatalf("completed TTL = %v, want about 70m", d)
		}
		claim("inflight", "t3", time.Minute, time.Minute, ReplayProcessed)
	})

	t.Run("released claims keep their deadlines", func(t *testing.T) {
		claim("released", "t1", time.Minute, 10*time.Minute, ReplayClaimed)
		claim("released", "t2", time.Minute, 70*time.Minute, ReplayInFlight)
		if err := store.Release(ctx, "released", "t1"); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		if d := ttl("released"); d <= 69*time.Minute || d > 70*time.Minute {
			t.Fatalf("released TTL = %v, want about 70m", d)
		}
		claim("released", "t3", time.Minute, 10*time.Minute, ReplayClaimed)
		if err := store.Complete(ctx, "released", time.Now().Add(10*time.Minute)); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if d := ttl("released"); d <= 69*time.Minute || d > 70*time.Minute {
			t.Fatalf("completed TTL = %v, want about 70m", d)
		}
		claim("released", "t4", time.Minute, time.Minute, ReplayProcessed)
	})

	t.Run("lapsed claims keep their deadlines", func(t *testing.T) {
		claim("lapsed", "t1", 50*time.Millisecond, 70*time.Minute, ReplayClaimed)
		time.Sleep(100 * time.Millisecond)
		claim("lapsed", "t2", time.Minute, 10*time.Minute, ReplayClaimed)
		if err := store.Complete(ctx, "lapsed", time.Now().Add(10*time.Minute)); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if d := ttl("lapsed"); d <= 69*time.Minute || d > 70*time.Minute {
			t.Fatalf("completed TTL = %v, want about 70m", d)
		}
	})

	t.Run("released claims past their deadlines are dropped", func(t *testing.T) {
		claim("dropped", "t1", time.Minute, -time.Second, ReplayClaimed)
		if err := store.Release(ctx, "dropped", "t1"); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		if n, err := client.Exists(ctx, prefix+"dropped").Result(); err != nil || n != 0 {
			t.Fatalf("Exists = %d, %v; want the key dropped", n, err)
		}
	})

	t.Run("retention only grows", func(t *testing.T) {
		claim("retain", "t1", time.Minute, time.Hour, ReplayClaimed)
		if err := store.Complete(ctx, "retain", time.Now().Add(time.Hour)); err != nil {
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
		if err := store.Complete(ctx, "retain", time.Now().Add(time.Minute)); err != nil {
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

func TestUnixMillis(t *testing.T) {
	at := time.Date(2026, 10, 30, 19, 30, 0, 123456789, time.UTC)
	if got, want := unixMillis(at), "1793388600123"; got != want {
		t.Fatalf("unixMillis() = %q, want %q", got, want)
	}
}

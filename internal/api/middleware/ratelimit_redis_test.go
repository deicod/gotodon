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

// newTestRedisRateLimitStore returns a store on the Redis at REDIS_HOST and
// REDIS_PORT under a prefix unique to the test, and the client behind it.
func newTestRedisRateLimitStore(t *testing.T) (*RedisRateLimitStore, *redis.Client, string) {
	t.Helper()
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		t.Skip("REDIS_HOST not set; skipping Redis rate limit store test")
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

	prefix := "gotodon:test:rate_limit:" + rand.Text() + ":"
	t.Cleanup(func() {
		keys, err := client.Keys(ctx, prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			client.Del(ctx, keys...)
		}
		_ = client.Close()
	})
	return NewRedisRateLimitStore(client, prefix), client, prefix
}

func TestRedisRateLimitStore(t *testing.T) {
	store, _, _ := newTestRedisRateLimitStore(t)
	testRateLimitStore(t, store)
}

func TestRedisRateLimitStoreWindowEnds(t *testing.T) {
	store, client, prefix := newTestRedisRateLimitStore(t)
	ctx := context.Background()
	const period = 200 * time.Millisecond

	if count, _, err := store.Hit(ctx, "k", period); err != nil || count != 1 {
		t.Fatalf("first Hit = %d, %v, want 1", count, err)
	}
	if count, ttl, err := store.Hit(ctx, "k", period); err != nil || count != 2 || ttl > period {
		t.Fatalf("second Hit = %d, %v, %v, want 2 within the first window", count, ttl, err)
	}
	// The counter expires with its window.
	if ttl, err := client.PTTL(ctx, prefix+"k").Result(); err != nil || ttl <= 0 || ttl > period {
		t.Fatalf("PTTL = %v, %v, want the window's remainder", ttl, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		exists, err := client.Exists(ctx, prefix+"k").Result()
		if err != nil {
			t.Fatalf("EXISTS error = %v", err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("counter outlived its window")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if count, _, err := store.Hit(ctx, "k", period); err != nil || count != 1 {
		t.Fatalf("Hit after the window = %d, %v, want a new window", count, err)
	}
}

func TestRedisRateLimitStoreRepairsCounterWithoutExpiry(t *testing.T) {
	store, client, prefix := newTestRedisRateLimitStore(t)
	ctx := context.Background()

	if err := client.Set(ctx, prefix+"k", 7, 0).Err(); err != nil {
		t.Fatalf("SET error = %v", err)
	}
	count, ttl, err := store.Hit(ctx, "k", time.Minute)
	if err != nil || count != 8 || ttl != time.Minute {
		t.Fatalf("Hit = %d, %v, %v, want 8 with a fresh window", count, ttl, err)
	}
	if pttl, err := client.PTTL(ctx, prefix+"k").Result(); err != nil || pttl <= 0 {
		t.Fatalf("PTTL = %v, %v, want an expiry", pttl, err)
	}
}

func TestRedisRateLimitStoreSharedAcrossInstances(t *testing.T) {
	store, client, prefix := newTestRedisRateLimitStore(t)
	other := NewRedisRateLimitStore(client, prefix)
	ctx := context.Background()

	if _, _, err := store.Hit(ctx, "k", time.Minute); err != nil {
		t.Fatalf("Hit error = %v", err)
	}
	if count, _, err := other.Hit(ctx, "k", time.Minute); err != nil || count != 2 {
		t.Fatalf("Hit through another instance = %d, %v, want 2", count, err)
	}
}

package cache

import (
	"context"
	"github.com/redis/go-redis/v9"
	"os"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/infrastructure/security"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func reviewRedis(t *testing.T) *redis.Client {
	t.Helper()
	endpoint := os.Getenv("TEST_REDIS_URL")
	if endpoint == "" {
		t.Skip("TEST_REDIS_URL required")
	}
	options, err := redis.ParseURL(endpoint)
	if err != nil {
		t.Fatal("invalid integration Redis configuration")
	}
	options.PoolSize = 4
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = client.Ping(ctx).Err(); err != nil {
		t.Fatal("integration Redis unavailable")
	}
	return client
}

func TestLimiterConcurrentQuotaAndWindowExpiry(t *testing.T) {
	client := reviewRedis(t)
	key := "review:" + security.NewID()
	redisKey := "qingye:limit:" + key
	t.Cleanup(func() { _ = client.Del(context.Background(), redisKey).Err() })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	limiter := Limiter{Client: client}
	var accepted, rejected, unavailable atomic.Int32
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			err := limiter.Allow(ctx, key, 7, 30*time.Second)
			if err == nil {
				accepted.Add(1)
				return
			}
			if known, ok := fault.As(err); ok && known.Kind == fault.RateLimited {
				rejected.Add(1)
				return
			}
			unavailable.Add(1)
		})
	}
	workers.Wait()
	if accepted.Load() != 7 || rejected.Load() != 25 || unavailable.Load() != 0 {
		t.Fatalf("quota not atomic: accepted=%d rejected=%d unavailable=%d", accepted.Load(), rejected.Load(), unavailable.Load())
	}
	ttl, err := client.PTTL(ctx, redisKey).Result()
	if err != nil || ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("counter lost expiry: %v %v", ttl, err)
	}
	// Advance only this test's key to its window end, without waiting for the full interval.
	if err = client.PExpire(ctx, redisKey, time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if err = limiter.Allow(ctx, key, 7, 30*time.Second); err == nil {
		t.Fatal("exhausted quota reopened before expiry")
	}
	ttl, err = client.PTTL(ctx, redisKey).Result()
	if err != nil || ttl < 0 || ttl > time.Second {
		t.Fatalf("rejected request extended window: %v %v", ttl, err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err = limiter.Allow(ctx, key, 7, 30*time.Second); err != nil {
		t.Fatalf("expired window did not recover: %v", err)
	}
}

func TestLimiterUnavailableFailsClosed(t *testing.T) {
	client := redis.NewClient(&redis.Options{})
	_ = client.Close()
	for _, candidate := range []*redis.Client{nil, client} {
		err := (Limiter{Client: candidate}).Allow(context.Background(), "review-closed", 10, time.Minute)
		known, ok := fault.As(err)
		if !ok || known.Kind != fault.Unavailable || known.Code != "RATE_LIMIT_UNAVAILABLE" {
			t.Fatalf("Redis failure bypassed limiter: %v", err)
		}
	}
}

func TestChatNotifierDeduplicatesRecipients(t *testing.T) {
	client := reviewRedis(t)
	id := "review-" + security.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sub := client.Subscribe(ctx, ChatChannel(id))
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	messages := sub.Channel()
	(ChatNotifier{Client: client}).Notify(ctx, id, id, "")
	select {
	case message := <-messages:
		if message.Payload != "changed" {
			t.Fatal("notification should not expose message contents")
		}
	case <-ctx.Done():
		t.Fatal("notification not delivered")
	}
	select {
	case <-messages:
		t.Fatal("duplicate recipient received duplicate notification")
	case <-time.After(100 * time.Millisecond):
	}
}

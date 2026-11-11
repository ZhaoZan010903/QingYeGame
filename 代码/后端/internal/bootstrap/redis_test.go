package bootstrap

import (
	"context"
	"github.com/redis/go-redis/v9"
	"os"
	"qingye/backend/internal/config"
	"qingye/backend/internal/infrastructure/security"
	"testing"
	"time"
)

func TestChatSubscriptionAfterCommandPoolFills(t *testing.T) {
	endpoint := os.Getenv("TEST_REDIS_URL")
	if endpoint == "" {
		t.Skip("TEST_REDIS_URL required")
	}
	options, err := redisOptions(config.Config{RedisURL: endpoint, RedisPoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	options.MinIdleConns = 2
	client := redis.NewClient(options)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	for client.PoolStats().IdleConns < 2 {
		select {
		case <-ctx.Done():
			t.Fatal("command pool did not fill")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// 先达到普通池容量，再申请独立订阅连接；旧MaxActiveConns设置会在此失败。
	sub := client.Subscribe(ctx, "qingye:test:pool:"+security.NewID())
	defer sub.Close()
	if _, err = sub.Receive(ctx); err != nil {
		t.Fatalf("chat subscription rejected after command pool filled: %v", err)
	}
	if err = client.Ping(ctx).Err(); err != nil {
		t.Fatalf("subscription consumed ordinary command capacity: %v", err)
	}
}

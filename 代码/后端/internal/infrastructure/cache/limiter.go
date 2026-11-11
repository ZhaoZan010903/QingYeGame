package cache

import (
	"context"
	"github.com/redis/go-redis/v9"
	"qingye/backend/internal/domain/fault"
	"time"
)

// Script.Run优先使用脚本散列；计数与首次过期设置在Redis内原子执行。
var limitScript = redis.NewScript(`local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]) end; return n`)

type Limiter struct{ Client *redis.Client }

func (l Limiter) Allow(ctx context.Context, key string, max int, period time.Duration) error {
	if l.Client == nil {
		return fault.New(fault.Unavailable, "RATE_LIMIT_UNAVAILABLE", "请求限流服务暂不可用")
	}
	n, err := limitScript.Run(ctx, l.Client, []string{"qingye:limit:" + key}, int(period.Seconds())).Int()
	if err != nil {
		return fault.Wrap(fault.Unavailable, "RATE_LIMIT_UNAVAILABLE", "请求限流服务暂不可用", err)
	}
	if n > max {
		return fault.New(fault.RateLimited, "RATE_LIMITED", "操作频繁，请稍后重试")
	}
	return nil
}

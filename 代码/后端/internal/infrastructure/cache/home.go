package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

type HomeDocument struct{ Client *redis.Client }

var homeLoads singleflight.Group

// Redis只缓存运营文案和布局。版本由数据库事务生成，旧请求不能回填新版本。
func (c HomeDocument) Load(ctx context.Context, revision string, load func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	if c.Client == nil {
		return load(ctx)
	}
	key := "qingye:home:presentation:" + revision
	lookup := func(ctx context.Context) json.RawMessage {
		budget, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		raw, err := c.Client.Get(budget, key).Bytes()
		if err == nil && len(raw) <= 128<<10 && len(raw) > 0 && raw[0] == '{' && json.Valid(raw) {
			return raw
		}
		return nil
	}
	if raw := lookup(ctx); raw != nil {
		return raw, nil
	}
	// 同进程并发未命中合并读取；独立超时避免首位访问者断开导致所有等待者失败。
	pending := homeLoads.DoChan(fmt.Sprintf("%p:%s", c.Client, key), func() (any, error) {
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if raw := lookup(work); raw != nil {
			return raw, nil
		}
		raw, err := load(work)
		if err != nil {
			return nil, err
		}
		if len(raw) > 0 && len(raw) <= 128<<10 && json.Valid(raw) {
			write, stop := context.WithTimeout(work, 150*time.Millisecond)
			// 抖动过期防止配置集中保存后同时回源；缓存失败不影响数据库结果。
			_ = c.Client.Set(write, key, []byte(raw), time.Duration(270+rand.IntN(61))*time.Second).Err()
			stop()
		}
		return raw, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-pending:
		if result.Err != nil {
			return nil, result.Err
		}
		raw, _ := result.Val.(json.RawMessage)
		return append(json.RawMessage(nil), raw...), nil
	}
}

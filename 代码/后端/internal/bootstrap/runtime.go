// Package bootstrap assembles resources and owns their lifetime.
package bootstrap

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	chatapp "qingye/backend/internal/application/chat"
	recommendationapp "qingye/backend/internal/application/recommendation"
	"qingye/backend/internal/config"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/infrastructure/security"
	"qingye/backend/internal/maintenance"
	"qingye/backend/internal/transport/httpapi"
	"time"
)

type Runtime struct {
	HTTP            *httpapi.App
	Maintenance     *maintenance.Service
	Chat            *chatapp.Service
	Recommendations *recommendationapp.Service
	db              *pgxpool.Pool
	redis           *redis.Client
}

func New(ctx context.Context, c config.Config) (*Runtime, error) {
	c = c.Defaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	poolConfig.MaxConns = int32(c.DBMaxConns)
	poolConfig.MinConns = 0
	poolConfig.ConnConfig.ConnectTimeout = 5 * time.Second
	db, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("database initialization failed")
	}
	if err = db.Ping(ctx); err != nil {
		db.Close()
		return nil, errors.New("database unavailable at startup")
	}
	ro, err := redisOptions(c)
	if err != nil {
		db.Close()
		return nil, err
	}
	client := redis.NewClient(ro)
	if err = client.Ping(ctx).Err(); err != nil {
		db.Close()
		_ = client.Close()
		return nil, errors.New("Redis unavailable at startup")
	}
	r := &Runtime{HTTP: &httpapi.App{DB: db, Redis: client, Config: c}, Maintenance: &maintenance.Service{DB: db, MediaDir: c.MediaDir}, db: db, redis: client}
	r.Chat = chatapp.New(&postgres.ChatStore{DB: db}, cache.Limiter{Client: client}, cache.ChatNotifier{Client: client}, c.Surface, security.NewID)
	r.Recommendations = recommendationapp.New(&postgres.RecommendationStore{DB: db}, security.NewID, func(ctx context.Context, batch string) {
		_ = client.Set(ctx, "qingye:recommendations:batch", batch, time.Hour).Err()
	})
	return r, nil
}

func (r *Runtime) Close() { r.db.Close(); _ = r.redis.Close() }

func redisOptions(c config.Config) (*redis.Options, error) {
	ro, err := redis.ParseURL(c.RedisURL)
	if err != nil {
		return nil, errors.New("invalid Redis configuration")
	}
	ro.DialTimeout = time.Second
	ro.ReadTimeout = time.Second
	ro.WriteTimeout = time.Second
	ro.PoolSize = c.RedisPoolSize
	ro.PoolTimeout = time.Second
	ro.ContextTimeoutEnabled = true
	// PoolSize限制普通命令并发。PubSub另开连接，不能让已填满的普通池阻止聊天订阅。
	// 聊天连接数量由单用户租约和会话期限约束。
	ro.MaxActiveConns = 0
	return ro, nil
}

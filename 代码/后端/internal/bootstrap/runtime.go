// Package bootstrap assembles resources and owns their lifetime.
package bootstrap

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"path/filepath"
	chatapp "qingye/backend/internal/application/chat"
	mediaapp "qingye/backend/internal/application/medialifecycle"
	purgeapp "qingye/backend/internal/application/mediapurge"
	recommendationapp "qingye/backend/internal/application/recommendation"
	registrationapp "qingye/backend/internal/application/registration"
	securityapp "qingye/backend/internal/application/security"
	"qingye/backend/internal/config"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/mediaobject"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/infrastructure/responsecrypto"
	"qingye/backend/internal/infrastructure/security"
	"qingye/backend/internal/infrastructure/sms"
	"qingye/backend/internal/maintenance"
	"qingye/backend/internal/transport/httpapi"
	"time"
)

type Runtime struct {
	HTTP            *httpapi.App
	Maintenance     *maintenance.Service
	Chat            *chatapp.Service
	Recommendations *recommendationapp.Service
	Media           *mediaapp.Worker
	Purge           *purgeapp.Worker
	mediaDB         *pgxpool.Pool
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
	if c.ResponseCrypto.Enabled {
		r.HTTP.ResponseKeys = &responsecrypto.RedisStore{Client: client, Namespace: "qingye:r24:response:" + c.Surface + ":"}
	}
	if c.Surface == "public" && c.MediaWorkersEnabled {
		r.Purge = &purgeapp.Worker{Repository: &postgres.MediaPermanentStore{DB: db, NewID: security.NewID}, Local: &mediafiles.Store{Root: c.MediaDir}}
	}
	if c.MediaSafety.Enabled {
		store := &cache.MediaReadStore{Client: client, Policy: c.MediaSafety.Policy}
		r.HTTP.ReadSafety = &securityapp.ReadService{Store: store, Audit: postgres.MediaReadAudit{DB: db}, NewID: security.NewID}
		r.HTTP.HumanSafety = &securityapp.HumanService{Store: store, Verifier: security.NewTurnstile(c.MediaSafety.Secret), NewID: security.NewID}
	}
	if (c.Registration.Enabled || c.Registration.LoginEnabled) && c.Surface == "public" {
		provider := sms.NewAliyun(c.Registration)
		r.HTTP.Registration = &registrationapp.Service{Store: postgres.RegistrationStore{DB: db}, Provider: provider, LoginProvider: provider.WithScheme(c.Registration.LoginScheme), Human: security.NewTurnstile(c.MediaSafety.Secret), Limits: cache.RegistrationLimiter{Client: client, DailyLimit: c.Registration.DailyLimit}, NewID: security.NewID, NewToken: security.RandomToken, Hash: security.TokenHash, MAC: security.RegistrationMAC(c.SessionSecret), PasswordHash: security.NewPasswordVerifier(2).Hash, AllowedPhones: c.Registration.AllowedPhones, PublicAccess: c.Environment == "production" && c.Registration.AccessMode == "public"}
	}
	r.Chat = chatapp.New(&postgres.ChatStore{DB: db}, cache.Limiter{Client: client}, cache.ChatNotifier{Client: client}, c.Surface, security.NewID)
	r.Recommendations = recommendationapp.New(&postgres.RecommendationStore{DB: db}, security.NewID, func(ctx context.Context, batch string) {
		_ = client.Set(ctx, "qingye:recommendations:batch", batch, time.Hour).Err()
	})
	if c.MediaR20.Enabled {
		mc := c.MediaR20
		temp := filepath.Join(c.MediaDir, "r20-tmp")
		stage, err := mediaobject.NewS3(mediaobject.Config{Environment: mc.Environment, Endpoint: mc.StageEndpoint, Bucket: mc.StageBucket, AccessKeyID: mc.StageAccessKeyID, AccessKeySecret: mc.StageAccessKeySecret, TempDir: temp, MinFreeBytes: mc.MinFreeBytes, CapacityPath: mc.StageCapacityPath})
		if err != nil {
			r.Close()
			return nil, err
		}
		published, err := mediaobject.NewOSS(mediaobject.Config{Environment: mc.Environment, Endpoint: mc.OSSEndpoint, Bucket: mc.OSSBucket, AccessKeyID: mc.OSSAccessKeyID, AccessKeySecret: mc.OSSAccessKeySecret, TempDir: temp, MinFreeBytes: mc.MinFreeBytes})
		if err != nil {
			r.Close()
			return nil, err
		}
		legacyConfig := mediaobject.Config{Environment: mc.Environment, Endpoint: mc.OSSEndpoint, Bucket: mc.OSSBucket, AccessKeyID: mc.OSSAccessKeyID, AccessKeySecret: mc.OSSAccessKeySecret, TempDir: temp, LegacyObjectPrefix: mc.LegacyObjectPrefix}
		files := &mediaobject.Files{ProtectedReads: c.MediaSafety.Enabled, Local: &mediafiles.Store{Root: c.MediaDir, MinFreeBytes: mc.MinFreeBytes, ThumbnailCommit: (&postgres.MediaPermanentStore{DB: db}).CommitThumbnail}, Staging: stage, Published: published, Repository: &postgres.MediaLifecycleStore{DB: db, NewID: security.NewID}, Environment: mc.Environment, TempDir: temp, NewID: security.NewID}
		if mc.LegacyReadsEnabled {
			reader, err := mediaobject.NewLegacyReader(legacyConfig)
			if err != nil {
				r.Close()
				return nil, err
			}
			files.Legacy = &mediaobject.LegacyFiles{Repository: &postgres.MediaLifecycleStore{DB: db}, Store: reader, TempDir: temp}
		}
		r.HTTP.MediaFiles = files
		if c.Surface == "public" && c.MediaWorkersEnabled {
			workerConfig := poolConfig.Copy()
			workerConfig.MaxConns = 2
			r.mediaDB, err = pgxpool.NewWithConfig(ctx, workerConfig)
			if err != nil {
				r.Close()
				return nil, errors.New("media worker database initialization failed")
			}
			r.Media = &mediaapp.Worker{Repository: &postgres.MediaLifecycleStore{DB: r.mediaDB, NewID: security.NewID}, Staging: stage, Published: published}
			r.Purge.Repository = &postgres.MediaPermanentStore{DB: r.mediaDB, NewID: security.NewID}
			if mc.LegacyDeleteEnabled {
				legacy, err := mediaobject.NewLegacyOSS(legacyConfig)
				if err != nil {
					r.Close()
					return nil, err
				}
				r.Purge.Legacy = legacy
			}
		}
	}
	return r, nil
}

func (r *Runtime) Close() {
	if r.mediaDB != nil {
		r.mediaDB.Close()
	}
	r.db.Close()
	_ = r.redis.Close()
}

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

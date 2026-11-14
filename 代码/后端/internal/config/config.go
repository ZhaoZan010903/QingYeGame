// Package config validates deployment settings before any network listener starts.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ResponseCrypto                                                      ResponseCryptoConfig
	MediaDeliveryEnabled                                                bool
	Registration                                                        RegistrationConfig
	MediaSafety                                                         MediaSafetyConfig
	MediaR20                                                            MediaR20Config
	Addr, DatabaseURL, RedisURL, MediaDir, PublicBaseURL, SessionSecret string
	ProductCredentialKey                                                string
	Surface, Environment                                                string
	AllowedOrigins                                                      []string
	SecureCookies                                                       bool
	MediaWorkersEnabled                                                 bool
	MaxUploadBytes                                                      int64
	ChatWTAddr, ChatWTURL, ChatWTCert, ChatWTKey                        string
	ChatWTPinCertificate                                                bool
	DBMaxConns                                                          int
	RedisPoolSize                                                       int
	MaxConcurrentRequests                                               int
	RequestTimeout                                                      time.Duration
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func Load() (Config, error) {
	surface := env("APP_SURFACE", "public")
	addr, origin := "127.0.0.1:5180", "http://127.0.0.1:5179"
	if surface == "admin" {
		addr, origin = "127.0.0.1:5182", "http://127.0.0.1:5181"
	}
	c := Config{Surface: surface, Environment: env("APP_ENV", "test"), Addr: env("QINGYE_ADDR", env("HTTP_ADDR", addr)), DatabaseURL: os.Getenv("DATABASE_URL"), RedisURL: env("REDIS_URL", "redis://127.0.0.1:6381/0"), MediaDir: env("MEDIA_DIR", "./data/media"), PublicBaseURL: os.Getenv("PUBLIC_BASE_URL"), SessionSecret: os.Getenv("SESSION_SECRET"), AllowedOrigins: strings.Split(env("ALLOWED_ORIGINS", origin), ",")}
	c.ProductCredentialKey = os.Getenv("PRODUCT_CREDENTIAL_KEY")
	var responseErr error
	c.ResponseCrypto, responseErr = loadResponseCrypto()
	if responseErr != nil {
		return c, responseErr
	}
	c.MediaDeliveryEnabled, responseErr = strconv.ParseBool(env("MEDIA_DELIVERY_ENABLED", "false"))
	if responseErr != nil {
		return c, errors.New("MEDIA_DELIVERY_ENABLED must be boolean")
	}
	var registrationErr error
	c.Registration, registrationErr = loadRegistration()
	if registrationErr != nil {
		return c, registrationErr
	}
	var safetyErr error
	c.MediaSafety, safetyErr = loadMediaSafety()
	if safetyErr != nil {
		return c, safetyErr
	}
	var mediaErr error
	c.MediaR20, mediaErr = loadMediaR20()
	if mediaErr != nil {
		return c, mediaErr
	}
	c.ChatWTAddr, c.ChatWTURL = os.Getenv("CHAT_WT_ADDR"), os.Getenv("CHAT_WT_URL")
	c.ChatWTCert, c.ChatWTKey = os.Getenv("CHAT_WT_CERT"), os.Getenv("CHAT_WT_KEY")
	for _, item := range []struct {
		key, value string
		target     *bool
	}{{"SECURE_COOKIES", env("SECURE_COOKIES", env("COOKIE_SECURE", "false")), &c.SecureCookies}, {"CHAT_WT_PIN_CERTIFICATE", env("CHAT_WT_PIN_CERTIFICATE", "false"), &c.ChatWTPinCertificate}, {"MEDIA_WORKERS_ENABLED", env("MEDIA_WORKERS_ENABLED", "true"), &c.MediaWorkersEnabled}} {
		value, err := strconv.ParseBool(item.value)
		if err != nil {
			return c, fmt.Errorf("%s must be a boolean", item.key)
		}
		*item.target = value
	}
	for _, item := range []struct {
		key, fallback string
		target        *int
	}{{"DB_MAX_CONNS", "5", &c.DBMaxConns}, {"REDIS_POOL_SIZE", "5", &c.RedisPoolSize}, {"MAX_CONCURRENT_REQUESTS", "128", &c.MaxConcurrentRequests}} {
		v, err := strconv.Atoi(env(item.key, item.fallback))
		if err != nil || v < 1 {
			return c, fmt.Errorf("%s must be a positive integer", item.key)
		}
		*item.target = v
	}
	upload, err := strconv.ParseInt(env("MAX_UPLOAD_BYTES", env("UPLOAD_MAX_BYTES", "10485760")), 10, 64)
	if err != nil || upload < 1 {
		return c, errors.New("MAX_UPLOAD_BYTES must be a positive integer")
	}
	c.MaxUploadBytes = upload
	c.RequestTimeout, err = time.ParseDuration(env("REQUEST_TIMEOUT", "15s"))
	if err != nil || c.RequestTimeout <= 0 {
		return c, errors.New("REQUEST_TIMEOUT must be a positive duration")
	}
	c = c.Defaults()
	return c, c.Validate()
}

func (c Config) Defaults() Config {
	if c.Environment == "" {
		c.Environment = "test"
	}
	if c.Registration.AccessMode == "" {
		c.Registration.AccessMode = "allowlist"
	}
	if c.Surface == "" {
		c.Surface = "public"
	}
	if c.DBMaxConns == 0 {
		c.DBMaxConns = 5
	}
	if c.RedisPoolSize == 0 {
		c.RedisPoolSize = 5
	}
	if c.MaxConcurrentRequests == 0 {
		c.MaxConcurrentRequests = 128
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = 15 * time.Second
	}
	if c.MaxUploadBytes == 0 {
		c.MaxUploadBytes = 10 << 20
	}
	origins := make([]string, len(c.AllowedOrigins))
	for i, origin := range c.AllowedOrigins {
		origins[i] = strings.TrimRight(strings.TrimSpace(origin), "/")
	}
	c.AllowedOrigins = origins
	return c
}

func (c Config) Validate() error {
	if c.MediaDeliveryEnabled && (!c.ResponseCrypto.Enabled || !c.MediaSafety.Enabled) {
		return errors.New("MEDIA_DELIVERY_ENABLED requires response encryption and media protection")
	}
	if c.Environment != "test" && c.Environment != "acceptance" && c.Environment != "production" {
		return errors.New("APP_ENV must be test, acceptance or production")
	}
	if err := c.Registration.validate(c.MediaSafety, c.Environment); err != nil {
		return err
	}
	if err := c.MediaSafety.validate(); err != nil {
		return err
	}
	if err := c.MediaR20.validate(); err != nil {
		return err
	}
	if c.MediaR20.LegacyReadsEnabled && !c.MediaSafety.Enabled {
		return errors.New("MEDIA_LEGACY_OSS_READ_ENABLED requires MEDIA_SAFETY_ENABLED")
	}
	if c.MediaR20.LegacyObjectPrefix != "" && c.Environment != "production" {
		return errors.New("MEDIA_LEGACY_OBJECT_PREFIX requires APP_ENV=production")
	}
	if c.ProductCredentialKey != "" {
		key, err := base64.StdEncoding.DecodeString(c.ProductCredentialKey)
		if err != nil || len(key) != 32 {
			return errors.New("PRODUCT_CREDENTIAL_KEY must be base64 of 32 bytes")
		}
	}
	if c.Surface != "public" && c.Surface != "admin" {
		return errors.New("APP_SURFACE must be public or admin")
	}
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if len(c.SessionSecret) < 32 {
		return errors.New("SESSION_SECRET must contain at least 32 characters")
	}
	if c.DBMaxConns < 1 || c.DBMaxConns > 50 {
		return errors.New("DB_MAX_CONNS must be between 1 and 50")
	}
	if c.RedisPoolSize < 1 || c.RedisPoolSize > 100 {
		return errors.New("REDIS_POOL_SIZE must be between 1 and 100")
	}
	if c.MaxConcurrentRequests < 1 || c.MaxConcurrentRequests > 2048 {
		return errors.New("MAX_CONCURRENT_REQUESTS must be between 1 and 2048")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > 60*time.Second {
		return errors.New("REQUEST_TIMEOUT must be between 1s and 60s")
	}
	if c.MaxUploadBytes < 1 || c.MaxUploadBytes > 64<<20 {
		return errors.New("MAX_UPLOAD_BYTES must be between 1 and 67108864")
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.Contains(u.Host, "*") {
			return errors.New("ALLOWED_ORIGINS must contain exact HTTP(S) origins")
		}
		if c.Environment == "production" && u.Scheme != "https" {
			return errors.New("production ALLOWED_ORIGINS must use HTTPS")
		}
	}
	if c.Environment == "production" {
		if c.MediaR20.Enabled && c.MediaR20.Environment != "production" {
			return errors.New("production requires MEDIA_R20_ENVIRONMENT=production when media storage is enabled")
		}
		if !c.SecureCookies || len(c.AllowedOrigins) == 0 {
			return errors.New("production requires SECURE_COOKIES and exact HTTPS origins")
		}
		if !c.MediaSafety.Enabled {
			return errors.New("production requires MEDIA_SAFETY_ENABLED")
		}
		if c.ChatWTPinCertificate {
			return errors.New("production requires trusted TLS certificates without CHAT_WT_PIN_CERTIFICATE")
		}
	}
	return nil
}

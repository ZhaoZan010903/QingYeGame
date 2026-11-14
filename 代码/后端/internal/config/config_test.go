package config

import (
	domain "qingye/backend/internal/domain/security"
	"strings"
	"testing"
)

func TestInvalidSecuritySettingsFailStartup(t *testing.T) {
	for _, tt := range []struct{ key, value string }{{"APP_SURFACE", "administrator"}, {"SECURE_COOKIES", "tru"}, {"MAX_CONCURRENT_REQUESTS", "0"}, {"DB_MAX_CONNS", "5000"}, {"REDIS_POOL_SIZE", "-1"}, {"REQUEST_TIMEOUT", "0s"}, {"MAX_UPLOAD_BYTES", "-1"}, {"ALLOWED_ORIGINS", "https://*.example.com"}, {"ALLOWED_ORIGINS", "https://example.com/path"}} {
		t.Run(tt.key, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://example")
			t.Setenv("SESSION_SECRET", strings.Repeat("x", 32))
			t.Setenv(tt.key, tt.value)
			if _, err := Load(); err == nil {
				t.Fatalf("invalid %s accepted", tt.key)
			}
		})
	}
}

func TestProductionSecuritySettingsFailClosed(t *testing.T) {
	base := Config{Environment: "production", DatabaseURL: "postgres://example", SessionSecret: strings.Repeat("x", 32), SecureCookies: true, AllowedOrigins: []string{"https://qingye.test:5179"}, MediaSafety: MediaSafetyConfig{Enabled: true, SiteKey: "0x" + strings.Repeat("a", 24), Secret: "0x" + strings.Repeat("b", 32), Hostnames: []string{"qingye.test"}, Policy: domain.DefaultReadPolicy()}}.Defaults()
	base.Registration.AccessMode = "public"
	if err := base.Validate(); err != nil {
		t.Fatal("safe production with SMS disabled rejected", err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"unknown-environment", func(c *Config) { c.Environment = "prod" }},
		{"insecure-cookie", func(c *Config) { c.SecureCookies = false }},
		{"http-origin", func(c *Config) { c.AllowedOrigins = []string{"http://qingye.test"} }},
		{"mixed-origins", func(c *Config) { c.AllowedOrigins = []string{"https://qingye.test", "http://127.0.0.1:5179"} }},
		{"empty-origins", func(c *Config) { c.AllowedOrigins = nil }},
		{"disabled-media-protection", func(c *Config) { c.MediaSafety.Enabled = false }},
		{"certificate-pinning", func(c *Config) { c.ChatWTPinCertificate = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			tt.mutate(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe production configuration accepted")
			}
		})
	}
	for _, environment := range []string{"", "test", "acceptance"} {
		c := Config{Environment: environment, DatabaseURL: "postgres://example", SessionSecret: strings.Repeat("x", 32), AllowedOrigins: []string{"http://127.0.0.1:5179"}}.Defaults()
		if err := c.Validate(); err != nil {
			t.Fatalf("isolated HTTP environment %q rejected: %v", environment, err)
		}
	}
}

func TestEnvironmentAndMediaWorkerParsing(t *testing.T) {
	for _, key := range []string{"APP_ENV", "APP_SURFACE", "SMS_ACCESS_MODE", "SMS_REGISTER_ENABLED", "SMS_LOGIN_ENABLED", "MEDIA_R20_ENABLED", "MEDIA_LEGACY_OSS_READ_ENABLED", "MEDIA_LEGACY_OSS_DELETE_ENABLED", "MEDIA_LEGACY_OBJECT_PREFIX", "MEDIA_SAFETY_ENABLED", "MEDIA_WORKERS_ENABLED", "SECURE_COOKIES", "COOKIE_SECURE", "CHAT_WT_PIN_CERTIFICATE"} {
		t.Setenv(key, "")
	}
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("SESSION_SECRET", strings.Repeat("x", 32))
	c, err := Load()
	if err != nil || c.Environment != "test" || !c.MediaWorkersEnabled || c.Registration.AccessMode != "allowlist" {
		t.Fatalf("default config: environment=%s workers=%v mode=%s error=%v", c.Environment, c.MediaWorkersEnabled, c.Registration.AccessMode, err)
	}
	t.Setenv("APP_ENV", "acceptance")
	t.Setenv("MEDIA_WORKERS_ENABLED", "false")
	c, err = Load()
	if err != nil || c.Environment != "acceptance" || c.MediaWorkersEnabled {
		t.Fatal("explicit worker pause rejected", err)
	}
	t.Setenv("MEDIA_WORKERS_ENABLED", "invalid")
	if _, err = Load(); err == nil {
		t.Fatal("invalid worker switch accepted")
	}
	t.Setenv("MEDIA_WORKERS_ENABLED", "true")
	t.Setenv("APP_ENV", "prod")
	if _, err = Load(); err == nil {
		t.Fatal("unknown environment accepted")
	}
}

func TestLegacyOSSReadsRequireMediaProtection(t *testing.T) {
	c := Config{Environment: "test", DatabaseURL: "postgres://example", SessionSecret: strings.Repeat("x", 32), AllowedOrigins: []string{"http://127.0.0.1:5179"}, MediaR20: MediaR20Config{Enabled: true, LegacyReadsEnabled: true, Environment: "test", StageEndpoint: "http://127.0.0.1:8333", StageBucket: "qingye-test", StageAccessKeyID: "key", StageAccessKeySecret: "secret", OSSEndpoint: "https://oss-cn-beijing.aliyuncs.com", OSSBucket: "qingye-images", OSSAccessKeyID: "key", OSSAccessKeySecret: "secret"}}.Defaults()
	if c.Validate() == nil {
		t.Fatal("legacy OSS reads accepted without media download protection")
	}
	c.MediaSafety = MediaSafetyConfig{Enabled: true, SiteKey: "0x" + strings.Repeat("a", 24), Secret: "0x" + strings.Repeat("b", 32), Hostnames: []string{"qingye.test"}, Policy: domain.DefaultReadPolicy()}
	if err := c.Validate(); err != nil {
		t.Fatal("protected legacy reads rejected", err)
	}
	c.Environment, c.SecureCookies, c.AllowedOrigins = "production", true, []string{"https://qingye.test"}
	if c.Validate() == nil {
		t.Fatal("production accepted a test storage namespace")
	}
	c.MediaR20.Environment = "production"
	c.MediaR20.LegacyObjectPrefix = "qingye-test/media/"
	if err := c.Validate(); err != nil {
		t.Fatal("production storage namespace rejected", err)
	}
	c.Environment = "test"
	if c.Validate() == nil {
		t.Fatal("test application accepted production historical takeover")
	}
}

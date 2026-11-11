package config

import (
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

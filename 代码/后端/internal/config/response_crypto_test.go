package config

import (
	domain "qingye/backend/internal/domain/security"
	"strings"
	"testing"
)

func TestResponseCryptoSwitchAndMediaDependencies(t *testing.T) {
	t.Setenv("RESPONSE_CRYPTO_ENABLED", "invalid")
	if _, err := loadResponseCrypto(); err == nil {
		t.Fatal("invalid switch accepted")
	}
	t.Setenv("RESPONSE_CRYPTO_ENABLED", "true")
	if c, err := loadResponseCrypto(); err != nil || !c.Enabled {
		t.Fatal("enabled switch rejected", err)
	}
	c := Config{DatabaseURL: "postgres://example", SessionSecret: strings.Repeat("x", 32), AllowedOrigins: []string{"http://qingye.test"}, MediaDeliveryEnabled: true}.Defaults()
	if c.Validate() == nil {
		t.Fatal("delivery enabled without response and media protection")
	}
	c.ResponseCrypto.Enabled = true
	if c.Validate() == nil {
		t.Fatal("delivery enabled without media protection")
	}
	c.MediaSafety = MediaSafetyConfig{Enabled: true, SiteKey: "0x" + strings.Repeat("a", 24), Secret: "0x" + strings.Repeat("b", 32), Hostnames: []string{"qingye.test"}, Policy: domain.DefaultReadPolicy()}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

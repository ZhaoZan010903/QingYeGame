package config

import (
	"strings"
	"testing"
)

func TestRegistrationConfigFailClosed(t *testing.T) {
	c := RegistrationConfig{Enabled: true, AccessKeyID: "test-access-id", AccessKeySecret: strings.Repeat("x", 32), SignName: "test-sign", Template: "100001", Scheme: "test-register", DailyLimit: 20, AllowedPhones: []string{"13800000000"}}
	safety := MediaSafetyConfig{Enabled: true, Hostnames: []string{"qingye.test"}}
	if e := c.validate(safety, "test"); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*RegistrationConfig){func(c *RegistrationConfig) { c.AccessKeyID = "" }, func(c *RegistrationConfig) { c.SignName = "" }, func(c *RegistrationConfig) { c.Template = "100003" }, func(c *RegistrationConfig) { c.Scheme = strings.Repeat("s", 21) }, func(c *RegistrationConfig) { c.DailyLimit = 0 }, func(c *RegistrationConfig) { c.AllowedPhones = nil }} {
		next := c
		mutate(&next)
		if next.validate(safety, "test") == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	if c.validate(MediaSafetyConfig{}, "test") == nil {
		t.Fatal("human verification optional")
	}
	t.Setenv("SMS_REGISTER_ENABLED", "false")
	t.Setenv("MEDIA_OSS_ACCESS_KEY_ID", "not-sms")
	t.Setenv("MEDIA_OSS_ACCESS_KEY_SECRET", "not-sms")
	t.Setenv("SMS_ACCESS_KEY_ID", "")
	t.Setenv("SMS_ACCESS_KEY_SECRET", "")
	got, e := loadRegistration()
	if e != nil || got.AccessKeyID != "" || got.AccessKeySecret != "" {
		t.Fatal("implicit OSS credential fallback")
	}
}
func TestR21SMSLoginConfigSeparateGates(t *testing.T) {
	c := RegistrationConfig{LoginEnabled: true, LoginScheme: "qy-login", AccessKeyID: "test-access-id", AccessKeySecret: strings.Repeat("x", 32), SignName: "test", Template: "100001", Scheme: "qy-register", DailyLimit: 20, AllowedPhones: []string{"13800000000"}}
	safety := MediaSafetyConfig{Enabled: true, Hostnames: []string{"qingye.test"}}
	if err := c.validate(safety, "test"); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"", c.Scheme, strings.Repeat("s", 21)} {
		next := c
		next.LoginScheme = scheme
		if next.validate(safety, "test") == nil {
			t.Fatal("unsafe scenario accepted")
		}
	}
	t.Setenv("SMS_LOGIN_ENABLED", "true")
	t.Setenv("SMS_REGISTER_ENABLED", "false")
	t.Setenv("SMS_LOGIN_SCHEME", "qy-login")
	got, err := loadRegistration()
	if err != nil || !got.LoginEnabled || got.Enabled || got.LoginScheme != "qy-login" {
		t.Fatal("independent gate", err)
	}
}

func TestSMSAccessRequiresExplicitProductionMode(t *testing.T) {
	base := RegistrationConfig{Enabled: true, LoginEnabled: true, AccessMode: "allowlist", AccessKeyID: "test-access-id", AccessKeySecret: strings.Repeat("x", 32), SignName: "test-sign", Template: "100001", Scheme: "qy-register", LoginScheme: "qy-login", DailyLimit: 20}
	safety := MediaSafetyConfig{Enabled: true, Hostnames: []string{"qingye.test"}}
	for _, tt := range []struct {
		name, environment, mode string
		phones                  []string
		disabled, valid         bool
	}{
		{"test-default-denies-empty", "test", "", nil, false, false},
		{"test-allowlist", "test", "allowlist", []string{"13800000000"}, false, true},
		{"acceptance-allowlist", "acceptance", "allowlist", []string{"13800000000"}, false, true},
		{"test-public-denied", "test", "public", nil, false, false},
		{"acceptance-public-denied", "acceptance", "public", nil, false, false},
		{"disabled-test-public-denied", "test", "public", nil, true, false},
		{"production-default-denies-empty", "production", "", nil, false, false},
		{"production-allowlist", "production", "allowlist", []string{"13800000000"}, false, true},
		{"production-public", "production", "public", nil, false, true},
		{"production-public-can-be-disabled", "production", "public", nil, true, true},
		{"production-public-ambiguous-list", "production", "public", []string{"13800000000"}, false, false},
		{"invalid-mode", "production", "open", nil, false, false},
		{"invalid-phone", "test", "allowlist", []string{"123"}, false, false},
		{"too-many-phones", "test", "allowlist", make([]string, 11), false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			c.AccessMode, c.AllowedPhones = tt.mode, tt.phones
			c.Enabled, c.LoginEnabled = !tt.disabled, !tt.disabled
			if err := c.validate(safety, tt.environment); (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
	base.AccessMode = "public"
	for _, mutate := range []func(*RegistrationConfig){
		func(c *RegistrationConfig) { c.AccessKeySecret = "" },
		func(c *RegistrationConfig) { c.DailyLimit = 0 },
		func(c *RegistrationConfig) { c.DailyLimit = 1001 },
		func(c *RegistrationConfig) { c.LoginScheme = c.Scheme },
	} {
		c := base
		mutate(&c)
		if c.validate(safety, "production") == nil {
			t.Fatal("public mode bypassed provider or shared budget configuration")
		}
	}
	if base.validate(MediaSafetyConfig{}, "production") == nil {
		t.Fatal("public mode bypassed human verification")
	}
}

func TestSMSAccessEnvironmentDefaultsRemainClosed(t *testing.T) {
	for _, key := range []string{"SMS_ACCESS_MODE", "SMS_REGISTER_ENABLED", "SMS_LOGIN_ENABLED", "SMS_ALLOWED_PHONES"} {
		t.Setenv(key, "")
	}
	c, err := loadRegistration()
	if err != nil || c.Enabled || c.LoginEnabled || c.AccessMode != "allowlist" || len(c.AllowedPhones) != 0 {
		t.Fatalf("unsafe defaults: enabled=%v login=%v access=%q, %v", c.Enabled, c.LoginEnabled, c.AccessMode, err)
	}
	t.Setenv("SMS_ACCESS_MODE", "public")
	c, err = loadRegistration()
	if err != nil || c.AccessMode != "public" || c.Enabled || c.LoginEnabled {
		t.Fatal("access mode changed independent SMS enable flags", err)
	}
}

package config

import (
	"strings"
	"testing"
)

func TestRegistrationConfigFailClosed(t *testing.T) {
	c := RegistrationConfig{Enabled: true, AccessKeyID: "test-access-id", AccessKeySecret: strings.Repeat("x", 32), SignName: "test-sign", Template: "100001", Scheme: "test-register", DailyLimit: 20, AllowedPhones: []string{"13800000000"}}
	safety := MediaSafetyConfig{Enabled: true, Hostnames: []string{"qingye.test"}}
	if e := c.validate(safety); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*RegistrationConfig){func(c *RegistrationConfig) { c.AccessKeyID = "" }, func(c *RegistrationConfig) { c.SignName = "" }, func(c *RegistrationConfig) { c.Template = "100003" }, func(c *RegistrationConfig) { c.Scheme = strings.Repeat("s", 21) }, func(c *RegistrationConfig) { c.DailyLimit = 0 }, func(c *RegistrationConfig) { c.AllowedPhones = nil }} {
		next := c
		mutate(&next)
		if next.validate(safety) == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	if c.validate(MediaSafetyConfig{}) == nil {
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
	if err := c.validate(safety); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"", c.Scheme, strings.Repeat("s", 21)} {
		next := c
		next.LoginScheme = scheme
		if next.validate(safety) == nil {
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

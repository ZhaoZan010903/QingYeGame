package config

import (
	"errors"
	"os"
	domain "qingye/backend/internal/domain/registration"
	"strconv"
	"strings"
	"unicode/utf8"
)

type RegistrationConfig struct {
	Enabled                                                  bool
	AccessKeyID, AccessKeySecret, SignName, Template, Scheme string
	AllowedPhones                                            []string
	DailyLimit                                               int
}

func loadRegistration() (RegistrationConfig, error) {
	c := RegistrationConfig{AccessKeyID: os.Getenv("SMS_ACCESS_KEY_ID"), AccessKeySecret: os.Getenv("SMS_ACCESS_KEY_SECRET"), SignName: os.Getenv("SMS_SIGN_NAME"), Template: env("SMS_REGISTER_TEMPLATE", "100001"), Scheme: env("SMS_REGISTER_SCHEME", "qingye-test-register"), DailyLimit: 20}
	var err error
	c.Enabled, err = strconv.ParseBool(env("SMS_REGISTER_ENABLED", "false"))
	if err != nil {
		return c, errors.New("SMS_REGISTER_ENABLED must be boolean")
	}
	c.DailyLimit, err = strconv.Atoi(env("SMS_DAILY_LIMIT", "20"))
	if err != nil {
		return c, errors.New("SMS_DAILY_LIMIT must be integer")
	}
	for _, p := range strings.Split(os.Getenv("SMS_ALLOWED_PHONES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.AllowedPhones = append(c.AllowedPhones, p)
		}
	}
	return c, nil
}
func (c RegistrationConfig) validate(s MediaSafetyConfig) error {
	if !c.Enabled {
		return nil
	}
	if len(c.AccessKeyID) < 8 || len(c.AccessKeySecret) < 16 || strings.TrimSpace(c.SignName) == "" || c.Template != "100001" || utf8.RuneCountInString(c.Scheme) < 1 || utf8.RuneCountInString(c.Scheme) > 20 {
		return errors.New("registration SMS credentials, sign, scheme and template 100001 required")
	}
	if !s.Enabled || len(s.Hostnames) == 0 {
		return errors.New("registration requires configured Turnstile verification")
	}
	if c.DailyLimit < 1 || c.DailyLimit > 1000 {
		return errors.New("SMS_DAILY_LIMIT must be 1..1000")
	}
	if len(c.AllowedPhones) == 0 || len(c.AllowedPhones) > 10 {
		return errors.New("test registration requires 1..10 explicit SMS_ALLOWED_PHONES")
	}
	for _, p := range c.AllowedPhones {
		if !domain.ValidPhone(p) {
			return errors.New("invalid SMS_ALLOWED_PHONES")
		}
	}
	return nil
}

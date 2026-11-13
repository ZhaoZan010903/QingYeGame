package config

import (
	"errors"
	"net/url"
	"os"
	domain "qingye/backend/internal/domain/security"
	"strconv"
	"strings"
)

type MediaSafetyConfig struct {
	Enabled         bool
	SiteKey, Secret string
	Hostnames       []string
	Policy          domain.ReadPolicy
}

func loadMediaSafety() (MediaSafetyConfig, error) {
	c := MediaSafetyConfig{SiteKey: os.Getenv("TURNSTILE_SITE_KEY"), Secret: os.Getenv("TURNSTILE_SECRET"), Policy: domain.DefaultReadPolicy()}
	var err error
	c.Enabled, err = strconv.ParseBool(env("MEDIA_SAFETY_ENABLED", "false"))
	if err != nil {
		return c, errors.New("MEDIA_SAFETY_ENABLED must be boolean")
	}
	for _, h := range strings.Split(os.Getenv("TURNSTILE_HOSTNAMES"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			c.Hostnames = append(c.Hostnames, h)
		}
	}
	for _, v := range []struct {
		name string
		dst  *int
	}{{"MEDIA_READ_BURST", &c.Policy.Burst}, {"MEDIA_READ_HARD_BURST", &c.Policy.HardBurst}, {"MEDIA_READ_IP_BURST", &c.Policy.IPBurst}, {"MEDIA_READ_MAX_SUBJECTS", &c.Policy.MaxSubjects}} {
		if s := os.Getenv(v.name); s != "" {
			*v.dst, err = strconv.Atoi(s)
			if err != nil {
				return c, errors.New("invalid media protection limit")
			}
		}
	}
	return c, c.validate()
}
func (c MediaSafetyConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.Secret) < 20 || len(c.Secret) > 256 || len(c.SiteKey) < 10 || len(c.SiteKey) > 128 || len(c.Hostnames) == 0 {
		return errors.New("Turnstile credentials and hostnames required")
	}
	if !strings.HasPrefix(c.SiteKey, "0x") || !strings.HasPrefix(c.Secret, "0x") {
		return errors.New("real deployment cannot use Turnstile test keys")
	}
	for _, h := range c.Hostnames {
		u, e := url.Parse("https://" + h)
		if e != nil || u.Hostname() != h || u.Port() != "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(h, "*") {
			return errors.New("exact Turnstile hostname required")
		}
	}
	p := c.Policy
	if p.Burst < 10 || p.Burst > 2000 || p.HardBurst < p.Burst || p.HardBurst > 10000 || p.IPBurst < p.HardBurst || p.IPBurst > 50000 || p.MaxSubjects < 100 || p.MaxSubjects > 50000 {
		return errors.New("media request limits out of bounds")
	}
	if p.PerSecond <= 0 || p.HardPerSecond <= 0 || p.IPPerSecond <= 0 || p.ByteBurst < 64<<20 || p.BytesPerSecond <= 0 {
		return errors.New("invalid media budget")
	}
	return nil
}

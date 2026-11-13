package config

import (
	domain "qingye/backend/internal/domain/security"
	"strings"
	"testing"
)

func TestMediaSafetyRejectsUnsafeDeployment(t *testing.T) {
	for _, name := range []string{"valid", "no-secret", "test-key", "wildcard", "port", "path", "zero-rate", "huge-state", "low-hard-budget"} {
		t.Run(name, func(t *testing.T) {
			c := MediaSafetyConfig{Enabled: true, SiteKey: "0x" + strings.Repeat("a", 24), Secret: "0x" + strings.Repeat("b", 32), Hostnames: []string{"152.136.252.204"}, Policy: domain.DefaultReadPolicy()}
			switch name {
			case "no-secret":
				c.Secret = ""
			case "test-key":
				c.SiteKey = "1x00000000000000000000AA"
			case "wildcard":
				c.Hostnames = []string{"*.example.com"}
			case "port":
				c.Hostnames = []string{"localhost:5179"}
			case "path":
				c.Hostnames = []string{"example.com/path"}
			case "zero-rate":
				c.Policy.PerSecond = 0
			case "huge-state":
				c.Policy.MaxSubjects = 10000000
			case "low-hard-budget":
				c.Policy.HardBurst = 1
			}
			if (c.validate() == nil) != (name == "valid") {
				t.Fatal("unsafe configuration accepted or valid configuration rejected")
			}
		})
	}
}

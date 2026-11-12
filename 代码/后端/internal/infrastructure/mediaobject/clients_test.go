package mediaobject

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStorageConfigurationBoundaries(t *testing.T) {
	base := Config{Environment: "acceptance", Bucket: "qingye-acceptance", AccessKeyID: "test-key", AccessKeySecret: "test-secret", TempDir: t.TempDir()}
	for _, endpoint := range []string{"http://8.8.8.8:8333", "http://localhost.evil:8333", "http://127.0.0.1:8333/path", "http://user:pass@127.0.0.1:8333", "http://127.0.0.1:8333?x=1"} {
		c := base
		c.Endpoint = endpoint
		if _, e := NewS3(c); e == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	base.Endpoint = "http://127.0.0.1:8333"
	if _, e := NewS3(base); e != nil {
		t.Fatal(e)
	}
	base.Endpoint = "https://oss-cn-beijing-internal.aliyuncs.com"
	s, e := NewOSS(base)
	if e != nil {
		t.Fatal(e)
	}
	// The production internal endpoint must never be exposed to a browser.
	u, e := s.SignedURL(context.Background(), testKey, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(u, "-internal.") || !strings.HasPrefix(u, "https://qingye-acceptance.oss-cn-beijing.aliyuncs.com/") {
		t.Fatal("invalid browser host")
	}
	base.Endpoint = "https://attacker.invalid"
	if _, e = NewOSS(base); e == nil {
		t.Fatal("arbitrary OSS host accepted")
	}
}

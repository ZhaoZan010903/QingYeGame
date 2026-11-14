package config

import "testing"

func TestR20StorageIsExplicitAndEnvironmentBound(t *testing.T) {
	c := MediaR20Config{}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.Enabled = true
	if c.validate() == nil {
		t.Fatal("enabled without credentials")
	}
	c = MediaR20Config{Enabled: true, Environment: "acceptance", StageEndpoint: "http://127.0.0.1:8333", StageBucket: "qingye-acceptance", StageAccessKeyID: "key", StageAccessKeySecret: "secret", OSSEndpoint: "https://oss-cn-beijing.aliyuncs.com", OSSBucket: "qingye-images", OSSAccessKeyID: "key", OSSAccessKeySecret: "secret"}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.OSSEndpoint = "https://oss-cn-beijing-internal.aliyuncs.com"
	if c.validate() == nil {
		t.Fatal("Tencent environment accepted private Aliyun endpoint")
	}
	c.Environment = "production"
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.StageEndpoint = "http://203.0.113.10:8333"
	if c.validate() == nil {
		t.Fatal("public unencrypted staging accepted")
	}
}

func TestLegacyOSSReadsRequireExplicitStorageEnable(t *testing.T) {
	if err := (MediaR20Config{LegacyReadsEnabled: true}).validate(); err == nil {
		t.Fatal("legacy reads enabled without R20 storage")
	}
	for _, key := range []string{"MEDIA_R20_ENABLED", "MEDIA_LEGACY_OSS_READ_ENABLED", "MEDIA_LEGACY_OSS_DELETE_ENABLED", "MEDIA_LEGACY_OBJECT_PREFIX"} {
		t.Setenv(key, "")
	}
	c, err := loadMediaR20()
	if err != nil || c.Enabled || c.LegacyReadsEnabled {
		t.Fatal("legacy reads enabled by default", err)
	}
	t.Setenv("MEDIA_LEGACY_OSS_READ_ENABLED", "true")
	if _, err = loadMediaR20(); err == nil {
		t.Fatal("legacy read switch bypassed storage enable")
	}
	t.Setenv("MEDIA_LEGACY_OSS_READ_ENABLED", "invalid")
	if _, err = loadMediaR20(); err == nil {
		t.Fatal("invalid legacy read switch accepted")
	}
}

func TestLegacyTakeoverScopeAndDeleteSwitch(t *testing.T) {
	for _, key := range []string{"MEDIA_R20_ENABLED", "MEDIA_LEGACY_OSS_READ_ENABLED", "MEDIA_LEGACY_OSS_DELETE_ENABLED", "MEDIA_LEGACY_OBJECT_PREFIX"} {
		t.Setenv(key, "")
	}
	c, err := loadMediaR20()
	if err != nil || !c.LegacyDeleteEnabled || c.LegacyObjectPrefix != "" {
		t.Fatal("legacy delete default changed", err)
	}
	t.Setenv("MEDIA_LEGACY_OSS_DELETE_ENABLED", "false")
	c, err = loadMediaR20()
	if err != nil || c.LegacyDeleteEnabled {
		t.Fatal("test deletion pause ignored", err)
	}
	t.Setenv("MEDIA_LEGACY_OSS_DELETE_ENABLED", "invalid")
	if _, err = loadMediaR20(); err == nil {
		t.Fatal("invalid deletion switch accepted")
	}
	base := MediaR20Config{Enabled: true, Environment: "production", LegacyObjectPrefix: "qingye-test/media/", StageEndpoint: "http://127.0.0.1:8333", StageBucket: "qingye-production", StageAccessKeyID: "key", StageAccessKeySecret: "secret", OSSEndpoint: "https://oss-cn-beijing.aliyuncs.com", OSSBucket: "qingye-images", OSSAccessKeyID: "key", OSSAccessKeySecret: "secret"}
	if err = base.validate(); err != nil {
		t.Fatal("production takeover rejected", err)
	}
	for _, scope := range []struct{ environment, prefix string }{{"test", "qingye-test/media/"}, {"acceptance", "qingye-test/media/"}, {"production", "qingye-test/"}, {"production", "qingye-production/media/"}} {
		c := base
		c.Environment, c.LegacyObjectPrefix = scope.environment, scope.prefix
		if c.validate() == nil {
			t.Fatal("invalid takeover scope accepted", scope)
		}
	}
}

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

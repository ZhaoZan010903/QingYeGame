package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
)

// MediaR20Config only enrolls new product uploads; legacy migration is separate.
type MediaR20Config struct {
	Enabled                                                            bool
	LegacyReadsEnabled                                                 bool
	LegacyDeleteEnabled                                                bool
	LegacyObjectPrefix                                                 string
	Environment                                                        string
	MinFreeBytes                                                       int64
	StageCapacityPath                                                  string
	StageEndpoint, StageBucket, StageAccessKeyID, StageAccessKeySecret string
	OSSEndpoint, OSSBucket, OSSAccessKeyID, OSSAccessKeySecret         string
}

func loadMediaR20() (MediaR20Config, error) {
	c := MediaR20Config{Environment: env("MEDIA_R20_ENVIRONMENT", "test"), StageEndpoint: env("MEDIA_STAGE_ENDPOINT", "http://127.0.0.1:8333"), StageBucket: os.Getenv("MEDIA_STAGE_BUCKET"), StageAccessKeyID: os.Getenv("MEDIA_STAGE_ACCESS_KEY_ID"), StageAccessKeySecret: os.Getenv("MEDIA_STAGE_ACCESS_KEY_SECRET"), OSSEndpoint: env("MEDIA_OSS_ENDPOINT", "https://oss-cn-beijing.aliyuncs.com"), OSSBucket: os.Getenv("MEDIA_OSS_BUCKET"), OSSAccessKeyID: os.Getenv("MEDIA_OSS_ACCESS_KEY_ID"), OSSAccessKeySecret: os.Getenv("MEDIA_OSS_ACCESS_KEY_SECRET")}
	var err error
	c.MinFreeBytes, err = strconv.ParseInt(env("MEDIA_R20_MIN_FREE_BYTES", "2147483648"), 10, 64)
	if err != nil || c.MinFreeBytes < 512<<20 || c.MinFreeBytes > 1<<40 {
		return c, errors.New("MEDIA_R20_MIN_FREE_BYTES must be between 512MiB and 1TiB")
	}
	c.StageCapacityPath = env("MEDIA_STAGE_CAPACITY_PATH", "/var/lib/qingye-storage")
	c.Enabled, err = strconv.ParseBool(env("MEDIA_R20_ENABLED", "false"))
	if err != nil {
		return c, errors.New("MEDIA_R20_ENABLED must be boolean")
	}
	c.LegacyReadsEnabled, err = strconv.ParseBool(env("MEDIA_LEGACY_OSS_READ_ENABLED", "false"))
	if err != nil {
		return c, errors.New("MEDIA_LEGACY_OSS_READ_ENABLED must be boolean")
	}
	c.LegacyDeleteEnabled, err = strconv.ParseBool(env("MEDIA_LEGACY_OSS_DELETE_ENABLED", "true"))
	if err != nil {
		return c, errors.New("MEDIA_LEGACY_OSS_DELETE_ENABLED must be boolean")
	}
	c.LegacyObjectPrefix = os.Getenv("MEDIA_LEGACY_OBJECT_PREFIX")
	return c, c.validate()
}
func (c MediaR20Config) validate() error {
	if c.LegacyObjectPrefix != "" && (!c.Enabled || c.Environment != "production" || c.LegacyObjectPrefix != "qingye-test/media/") {
		return errors.New("historical prefix takeover requires production R20 and exact historical prefix")
	}
	if c.LegacyReadsEnabled && !c.Enabled {
		return errors.New("MEDIA_LEGACY_OSS_READ_ENABLED requires MEDIA_R20_ENABLED")
	}
	if !c.Enabled {
		return nil
	}
	if c.Environment != "test" && c.Environment != "acceptance" && c.Environment != "production" {
		return errors.New("invalid R20 media environment")
	}
	if c.StageAccessKeyID == "" || c.StageAccessKeySecret == "" || c.OSSAccessKeyID == "" || c.OSSAccessKeySecret == "" {
		return errors.New("R20 media credentials required")
	}
	bucket := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	if !bucket.MatchString(c.StageBucket) || !bucket.MatchString(c.OSSBucket) {
		return errors.New("invalid R20 media bucket")
	}
	if c.OSSEndpoint != "https://oss-cn-beijing.aliyuncs.com" && c.OSSEndpoint != "https://oss-cn-beijing-internal.aliyuncs.com" {
		return errors.New("R20 OSS requires Beijing HTTPS endpoint")
	}
	if c.Environment != "production" && c.OSSEndpoint == "https://oss-cn-beijing-internal.aliyuncs.com" {
		return errors.New("cross-cloud testing requires OSS public endpoint")
	}
	u, err := url.Parse(c.StageEndpoint)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || !net.ParseIP(u.Hostname()).IsLoopback())) {
		return errors.New("staging endpoint must be private loopback or HTTPS")
	}
	return nil
}

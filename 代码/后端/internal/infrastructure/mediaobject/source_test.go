package mediaobject

import (
	"context"
	"testing"

	"qingye/backend/internal/application/medialifecycle"
)

func TestObjectReadClassifiesPersistentSourceNotPrivateTemporaryFile(t *testing.T) {
	data := []byte("isolated-object-source")
	for _, tc := range []struct {
		name, store             string
		localAdapter, wantLocal bool
	}{
		{"local-stage", "staging", true, true},
		{"remote-stage", "staging", false, false},
		{"oss-spool", "oss", false, false},
		{"oss-cannot-inherit-stage-flag", "oss", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			backend := &memoryBackend{data: data}
			store := &Store{backend: backend, environment: "acceptance", temp: temp, serverLocal: tc.localAdapter}
			files := &Files{ProtectedReads: true, Staging: store, Published: store, TempDir: temp, Repository: protectedObjectRepo{medialifecycle.Object{MediaID: "media0001", Store: tc.store, Key: testKey, Size: int64(len(data)), SHA256: digest(data), MIME: "image/png"}}}
			file, err := files.Open(context.Background(), testKey, "media0001", false)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Reader.Close()
			if file.ServerLocal != tc.wantLocal {
				t.Fatal("download spool confused with persistent source", file.ServerLocal)
			}
		})
	}
}

func TestStorageConstructorsClassifyLoopbackStagingOnly(t *testing.T) {
	base := Config{Environment: "acceptance", Bucket: "qingye-acceptance", AccessKeyID: "isolated-key", AccessKeySecret: "isolated-secret", TempDir: t.TempDir()}
	for _, tc := range []struct {
		endpoint string
		local    bool
	}{
		{"http://127.0.0.1:8333", true},
		{"https://localhost:8333", true},
		{"https://staging.example.invalid", false},
	} {
		c := base
		c.Endpoint = tc.endpoint
		s, err := NewS3(c)
		if err != nil {
			t.Fatal(err)
		}
		if s.ServerLocalSource() != tc.local {
			t.Fatal("wrong persistent staging classification", tc.endpoint)
		}
	}
	for _, endpoint := range []string{"https://oss-cn-beijing.aliyuncs.com", "https://oss-cn-beijing-internal.aliyuncs.com"} {
		c := base
		c.Endpoint = endpoint
		s, err := NewOSS(c)
		if err != nil {
			t.Fatal(err)
		}
		if s.ServerLocalSource() {
			t.Fatal("OSS source classified as local", endpoint)
		}
	}
}

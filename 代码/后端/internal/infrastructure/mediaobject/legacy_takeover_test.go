package mediaobject

import (
	"bytes"
	"context"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"net/http"
	"net/http/httptest"
	"qingye/backend/internal/application/mediapurge"
	"testing"
)

func TestLegacyTakeoverRequiresProductionAndKeepsR20Scope(t *testing.T) {
	for _, environment := range []string{"test", "acceptance", "production"} {
		prefix, only, err := legacyScope(Config{Environment: environment, LegacyObjectPrefix: "qingye-test/media/"})
		if environment == "production" {
			if err != nil || prefix != "qingye-test/media/" || !only {
				t.Fatal("production takeover unavailable", err)
			}
			if ValidateKey(environment, "r20/test/objects/product/id/original") == nil {
				t.Fatal("takeover changed R20 namespace")
			}
		} else if err == nil {
			t.Fatal("nonproduction takeover accepted")
		}
	}
	if _, ok := any(&LegacyReader{}).(mediapurge.Legacy); ok {
		t.Fatal("read adapter exposes delete")
	}
}

func TestProductionTakeoverReadsAndDeletesOnlyExactLegacyOriginal(t *testing.T) {
	data, object := legacyReadFixture(t)
	object.ObjectKey = "qingye-test/media/" + object.StorageKey
	requests, deleted := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/production-bucket/"+object.ObjectKey {
			t.Error("takeover escaped exact original")
		}
		if r.Method == "DELETE" {
			deleted++
			w.WriteHeader(204)
			return
		}
		if r.Method != "HEAD" && r.Method != "GET" {
			t.Error("unexpected write")
		}
		w.Header().Set("Content-Type", object.MIME)
		w.Header().Set("X-Oss-Meta-Source-Sha256", object.SHA256)
		w.Header().Set("X-Oss-Meta-Media-Id", object.MediaID)
		w.Header().Set("X-Oss-Hash-Crc64ecma", object.CRC64)
		w.Header().Set("ETag", `"takeover-fixture"`)
		http.ServeContent(w, r, "fixture", object.Modified, bytes.NewReader(data))
	}))
	defer server.Close()
	client := oss.NewClient(oss.LoadDefaultConfig().WithRegion("cn-beijing").WithEndpoint(server.URL).WithUsePathStyle(true).WithCredentialsProvider(credentials.NewAnonymousCredentialsProvider()).WithRetryMaxAttempts(1))
	backend := &ossBackend{client: client, bucket: object.Bucket}
	reader := &LegacyReader{backend: backend, prefix: "qingye-test/media/", legacyOnly: true}
	files := LegacyFiles{Repository: legacyRepoStub{o: object, found: true}, Store: reader, TempDir: t.TempDir()}
	f, found, err := files.Open(context.Background(), object.StorageKey, object.MediaID)
	if err != nil || !found {
		t.Fatal("existing original unreadable", err)
	}
	f.Reader.Close()
	deleter := &LegacyDeleter{backend: backend, prefix: "qingye-test/media/", legacyOnly: true}
	o := mediapurge.Object{MediaID: object.MediaID, StorageKey: object.StorageKey, ObjectKey: object.ObjectKey, Bucket: object.Bucket, SHA256: object.SHA256, CRC64: object.CRC64, Size: object.Size}
	if err = deleter.DeleteReplica(context.Background(), o); err != nil || deleted != 1 {
		t.Fatal("owned original not physically deleted", err)
	}
	before := requests
	for _, key := range []string{"images/11223344-1122-3344-5566-112233445566.jpg", "legacy/wrong"} {
		bad := object
		bad.StorageKey, bad.ObjectKey = key, "qingye-test/media/"+key
		if _, err = reader.OpenReplica(context.Background(), bad); err == nil {
			t.Fatal("unowned image accepted")
		}
		badDelete := o
		badDelete.StorageKey, badDelete.ObjectKey = key, bad.ObjectKey
		if err = deleter.DeleteReplica(context.Background(), badDelete); err == nil {
			t.Fatal("unowned delete accepted")
		}
	}
	wrong := o
	wrong.ObjectKey = "qingye-production/media/" + wrong.StorageKey
	if err = deleter.DeleteReplica(context.Background(), wrong); err == nil || requests != before {
		t.Fatal("invalid scope contacted OSS")
	}
}

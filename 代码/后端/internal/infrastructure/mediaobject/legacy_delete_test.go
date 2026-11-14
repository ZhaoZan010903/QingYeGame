package mediaobject

import (
	"bytes"
	"context"
	"errors"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"net/http"
	"net/http/httptest"
	"qingye/backend/internal/application/mediapurge"
	"strings"
	"testing"
	"time"
)

func TestLegacyDeleteChecksIdentityAndExactPrefix(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "wrong-sha", "wrong-id", "wrong-size", "wrong-crc", "wrong-prefix", "denied"} {
		t.Run(mode, func(t *testing.T) {
			data := bytes.Repeat([]byte("a"), 100)
			removed, heads := 0, 0
			object := mediapurge.Object{MediaID: "test-id", StorageKey: "legacy/" + strings.Repeat("a", 64), Bucket: "test-bucket", SHA256: strings.Repeat("b", 64), CRC64: "42", Size: 100}
			object.ObjectKey = "qingye-acceptance/media/" + object.StorageKey
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/test-bucket/"+object.ObjectKey {
					t.Error("wrong object request")
				}
				if mode == "missing" {
					http.Error(w, "<Error><Code>NoSuchKey</Code></Error>", 404)
					return
				}
				if r.Method == "DELETE" {
					removed++
					if mode == "denied" {
						http.Error(w, "<Error><Code>AccessDenied</Code></Error>", 403)
					} else {
						w.WriteHeader(204)
					}
					return
				}
				if r.Method != "HEAD" {
					t.Error("unexpected content transfer")
				}
				heads++
				w.Header().Set("X-Oss-Meta-Source-Sha256", object.SHA256)
				w.Header().Set("X-Oss-Meta-Media-Id", object.MediaID)
				w.Header().Set("X-Oss-Hash-Crc64ecma", object.CRC64)
				switch mode {
				case "wrong-sha":
					w.Header().Set("X-Oss-Meta-Source-Sha256", "bad")
				case "wrong-id":
					w.Header().Set("X-Oss-Meta-Media-Id", "different")
				case "wrong-crc":
					w.Header().Set("X-Oss-Hash-Crc64ecma", "0")
				}
				http.ServeContent(w, r, "fixture", time.Unix(1700000000, 0), bytes.NewReader(data))
			}))
			defer server.Close()
			client := oss.NewClient(oss.LoadDefaultConfig().WithRegion("cn-beijing").WithEndpoint(server.URL).WithUsePathStyle(true).WithCredentialsProvider(credentials.NewAnonymousCredentialsProvider()).WithRetryMaxAttempts(1))
			deleter := &LegacyDeleter{backend: &ossBackend{client: client, bucket: object.Bucket}, prefix: "qingye-acceptance/media/"}
			if mode == "wrong-size" {
				object.Size++
			}
			if mode == "wrong-prefix" {
				object.ObjectKey = "qingye-test/media/" + object.StorageKey
			}
			err := deleter.DeleteReplica(context.Background(), object)
			if mode == "valid" || mode == "missing" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe or failed delete reported success")
			}
			if mode == "valid" || mode == "denied" {
				if removed != 1 || heads != 1 {
					t.Fatal("exact delete missing", removed, heads)
				}
			} else if removed != 0 {
				t.Fatal("mismatched object deleted")
			}
			if mode == "denied" && !errors.Is(err, ErrStorage) {
				t.Fatal("storage error was not sanitized")
			}
		})
	}
}

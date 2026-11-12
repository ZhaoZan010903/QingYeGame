//go:build oss_read

package mediafilesoss

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

func sdkFixture(t *testing.T, alter func(http.Header), body []byte) (*sdkReader, Entry, []byte) {
	t.Helper()
	data := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 1024)...)
	if body != nil {
		data = body
	}
	entry := Entry{MediaID: "fixture", Bucket: "test-bucket", ObjectKey: "qingye-test/media/legacy/" + strings.Repeat("a", 64), Size: int64(len(data)), MIME: "image/png", SHA256: strings.Repeat("b", 64), CRC64: "42"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", entry.MIME)
		w.Header().Set("X-Oss-Meta-Source-Sha256", entry.SHA256)
		w.Header().Set("X-Oss-Meta-Media-Id", entry.MediaID)
		w.Header().Set("X-Oss-Hash-Crc64ecma", entry.CRC64)
		w.Header().Set("ETag", `"test-etag"`)
		if alter != nil {
			alter(w.Header())
		}
		http.ServeContent(w, r, "fixture", time.Unix(1700000000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	client := oss.NewClient(oss.LoadDefaultConfig().WithRegion("cn-beijing").WithEndpoint(server.URL).WithUsePathStyle(true).WithCredentialsProvider(credentials.NewAnonymousCredentialsProvider()).WithRetryMaxAttempts(1))
	return &sdkReader{client: client}, entry, data
}
func TestSDKSeekableReadUsesRange(t *testing.T) {
	r, entry, want := sdkFixture(t, nil, nil)
	f, err := r.Open(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Reader.Close()
	if f.MIME != "image/png" {
		t.Fatal("image type lost")
	}
	if n, err := f.Reader.Seek(-12, io.SeekEnd); err != nil || n != int64(len(want)-12) {
		t.Fatal("seek-end failed", n, err)
	}
	got, err := io.ReadAll(f.Reader)
	if err != nil || !bytes.Equal(got, want[len(want)-12:]) {
		t.Fatal("range read mismatch", err)
	}
	if _, err = f.Reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(f.Reader)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("full read after seek mismatch", err)
	}
}
func TestSDKRejectsChangedMetadata(t *testing.T) {
	for _, header := range []string{"Content-Type", "X-Oss-Meta-Source-Sha256", "X-Oss-Meta-Media-Id", "X-Oss-Hash-Crc64ecma"} {
		t.Run(header, func(t *testing.T) {
			r, e, _ := sdkFixture(t, func(h http.Header) { h.Set(header, "changed") }, nil)
			if _, err := r.Open(context.Background(), e); !errors.Is(err, errObject) {
				t.Fatal("changed object accepted")
			}
		})
	}
}
func TestSDKRejectsUnexpectedSizeAndNonImage(t *testing.T) {
	r, e, _ := sdkFixture(t, nil, nil)
	e.Size++
	if _, err := r.Open(context.Background(), e); !errors.Is(err, errObject) {
		t.Fatal("size mismatch accepted")
	}
	r, e, _ = sdkFixture(t, nil, []byte("<html>not an image</html>"))
	if _, err := r.Open(context.Background(), e); !errors.Is(err, errObject) {
		t.Fatal("metadata cannot make HTML an image")
	}
}
func TestSDKCancellationClosesRead(t *testing.T) {
	r, e, _ := sdkFixture(t, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	f, err := r.Open(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Reader.Close()
	cancel()
	// A seek outside the buffered read forces the SDK to use the canceled context.
	f.Reader.Seek(900, io.SeekStart)
	_, err = f.Reader.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("canceled remote read succeeded")
	}
}
func TestNewRejectsUntrustedEndpointWithoutNetwork(t *testing.T) {
	for _, endpoint := range []string{"http://oss-cn-beijing.aliyuncs.com", "https://127.0.0.1", "https://oss-cn-beijing.aliyuncs.com@evil.invalid", "https://oss-cn-beijing.aliyuncs.com/path"} {
		_, err := New(Config{Bucket: "test-bucket", Region: "cn-beijing", Endpoint: endpoint}, &localStub{}, strings.NewReader("{}\n"))
		if err == nil {
			t.Fatal("unexpected endpoint accepted")
		}
	}
}

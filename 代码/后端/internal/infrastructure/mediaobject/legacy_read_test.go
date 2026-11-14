package mediaobject

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	mediaapp "qingye/backend/internal/application/media"
	"qingye/backend/internal/domain/media"
)

// Adding the historical reader must not remove the R20 upload capabilities.
var _ mediaapp.CapacityChecker = (*Files)(nil)
var _ mediaapp.ProductCapacityChecker = (*Files)(nil)
var _ mediaapp.ChunkFiles = (*Files)(nil)

type legacyRepoStub struct {
	o     media.LegacyReplica
	found bool
	err   error
}

func (r legacyRepoStub) ReadLegacyReplica(context.Context, string, string) (media.LegacyReplica, bool, error) {
	return r.o, r.found, r.err
}

type legacyReaderStub struct {
	data  []byte
	calls int
}

func (s *legacyReaderStub) OpenReplica(context.Context, media.LegacyReplica) (io.ReadCloser, error) {
	s.calls++
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func legacyReadFixture(t *testing.T) ([]byte, media.LegacyReplica) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		t.Fatal(err)
	}
	data := body.Bytes()
	o := media.LegacyReplica{MediaID: "legacy-read-fixture", StorageKey: "legacy/" + strings.Repeat("a", 64), Bucket: "production-bucket", MIME: "image/png", Size: int64(len(data)), SHA256: digest(data), CRC64: "42", Modified: time.Unix(1700000000, 0)}
	o.ObjectKey = "qingye-production/media/" + o.StorageKey
	return data, o
}

func TestLegacyPrivateReadOriginalAndPreviewWithoutLocalOriginal(t *testing.T) {
	for _, preview := range []bool{false, true} {
		data, o := legacyReadFixture(t)
		reader := &legacyReaderStub{data: data}
		temp := t.TempDir()
		files := &Files{Legacy: &LegacyFiles{Repository: legacyRepoStub{o: o, found: true}, Store: reader, TempDir: temp}}
		f, err := files.Open(context.Background(), o.StorageKey, o.MediaID, preview)
		if err != nil {
			t.Fatal(err)
		}
		if f.Reader == nil || f.RedirectURL != "" || f.MIME != o.MIME || !f.Modified.Equal(o.Modified) {
			t.Fatal("legacy read bypassed private response")
		}
		if _, err = f.Reader.Seek(8, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(f.Reader)
		if err != nil || !bytes.Equal(got, data[8:]) || reader.calls != 1 {
			t.Fatal("seekable response differs from verified bytes", err)
		}
		if err = f.Reader.Close(); err != nil {
			t.Fatal(err)
		}
		_ = f.Reader.Close()
		entries, err := os.ReadDir(temp)
		if err != nil || len(entries) != 0 {
			t.Fatal("private download retained after close", err)
		}
	}
}

func TestLegacyPrivateReadRejectsInvalidMappingsAndContent(t *testing.T) {
	for _, mode := range []string{"repository-error", "media-id", "storage-key", "digest", "size", "crc", "mime", "content-hash", "content-mime", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			data, o := legacyReadFixture(t)
			id, key := o.MediaID, o.StorageKey
			repo := legacyRepoStub{o: o, found: true}
			reader := &legacyReaderStub{data: data}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "repository-error":
				repo.err = ErrIntegrity
			case "media-id":
				repo.o.MediaID = "another-id"
			case "storage-key":
				repo.o.StorageKey = "legacy/" + strings.Repeat("b", 64)
			case "digest":
				repo.o.SHA256 = "bad"
			case "size":
				repo.o.Size = MaxObjectBytes + 1
			case "crc":
				repo.o.CRC64 = "+42"
			case "mime":
				repo.o.MIME = "image/svg+xml"
			case "content-hash":
				reader.data = append([]byte(nil), data...)
				reader.data[len(data)-1] ^= 1
			case "content-mime":
				reader.data = bytes.Repeat([]byte("a"), len(data))
				repo.o.SHA256 = digest(reader.data)
			case "cancelled":
				cancel()
			}
			temp := t.TempDir()
			files := &Files{Legacy: &LegacyFiles{Repository: repo, Store: reader, TempDir: temp}}
			if f, err := files.Open(ctx, key, id, false); err == nil || f != nil {
				t.Fatal("unverified content was returned")
			}
			if mode != "content-hash" && mode != "content-mime" && reader.calls != 0 {
				t.Fatal("invalid admission contacted object storage")
			}
			entries, err := os.ReadDir(temp)
			if err != nil || len(entries) != 0 {
				t.Fatal("rejected copy retained", err)
			}
		})
	}
}

func TestLegacyOSSReaderPinsEnvironmentAndVerifiedMetadata(t *testing.T) {
	for _, mode := range []string{"valid", "test-prefix", "bucket", "sha", "media-id", "size", "crc", "mime", "missing", "body"} {
		t.Run(mode, func(t *testing.T) {
			data, o := legacyReadFixture(t)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "HEAD" && r.Method != "GET" {
					t.Error("read path performed a storage mutation")
				}
				if r.URL.Path != "/production-bucket/qingye-production/media/"+o.StorageKey {
					t.Error("read escaped exact production object")
				}
				if mode == "missing" {
					http.Error(w, "<Error><Code>NoSuchKey</Code></Error>", 404)
					return
				}
				w.Header().Set("Content-Type", o.MIME)
				w.Header().Set("X-Oss-Meta-Source-Sha256", o.SHA256)
				w.Header().Set("X-Oss-Meta-Media-Id", o.MediaID)
				w.Header().Set("X-Oss-Hash-Crc64ecma", o.CRC64)
				w.Header().Set("ETag", `"isolated-legacy-object"`)
				body := data
				switch mode {
				case "sha":
					w.Header().Set("X-Oss-Meta-Source-Sha256", strings.Repeat("b", 64))
				case "media-id":
					w.Header().Set("X-Oss-Meta-Media-Id", "another-id")
				case "crc":
					w.Header().Set("X-Oss-Hash-Crc64ecma", "43")
				case "mime":
					w.Header().Set("Content-Type", "text/html")
				case "size":
					body = append(append([]byte(nil), data...), 0)
				case "body":
					body = bytes.Repeat([]byte("x"), len(data))
				}
				http.ServeContent(w, r, "fixture", o.Modified, bytes.NewReader(body))
			}))
			defer server.Close()
			client := oss.NewClient(oss.LoadDefaultConfig().WithRegion("cn-beijing").WithEndpoint(server.URL).WithUsePathStyle(true).WithCredentialsProvider(credentials.NewAnonymousCredentialsProvider()).WithRetryMaxAttempts(1))
			store := &LegacyReader{backend: &ossBackend{client: client, bucket: "production-bucket"}, prefix: "qingye-production/media/"}
			if mode == "test-prefix" {
				o.ObjectKey = "qingye-test/media/" + o.StorageKey
			}
			if mode == "bucket" {
				o.Bucket = "test-bucket"
			}
			files := LegacyFiles{Repository: legacyRepoStub{o: o, found: true}, Store: store, TempDir: t.TempDir()}
			f, found, err := files.Open(context.Background(), o.StorageKey, o.MediaID)
			if mode == "valid" {
				if err != nil || !found {
					t.Fatal(err)
				}
				f.Reader.Close()
			} else if err == nil || f != nil {
				t.Fatal("mismatched production object accepted")
			}
			if (mode == "test-prefix" || mode == "bucket") && requests != 0 {
				t.Fatal("cross-environment object contacted")
			}
			if mode == "missing" && errors.Is(err, io.EOF) {
				t.Fatal("storage error leaked")
			}
		})
	}
}

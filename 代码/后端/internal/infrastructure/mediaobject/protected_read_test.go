package mediaobject

import (
	"context"
	"io"
	"os"
	"qingye/backend/internal/application/medialifecycle"
	"testing"
)

type protectedObjectRepo struct{ object medialifecycle.Object }

func (r protectedObjectRepo) ReadObject(context.Context, string, string) (medialifecycle.Object, error) {
	return r.object, nil
}
func (protectedObjectRepo) StageCandidate(context.Context, string, string, string, string, string, string, int64) error {
	return nil
}

func TestProtectedOSSReadsNeverExposeSignedLink(t *testing.T) {
	data := []byte("small-isolated-image-body")
	temp := t.TempDir()
	b := &memoryBackend{data: data}
	store := &Store{backend: b, environment: "acceptance", temp: temp}
	f := Files{ProtectedReads: true, Published: store, TempDir: temp, Repository: protectedObjectRepo{medialifecycle.Object{Store: "oss", Key: testKey, Size: int64(len(data)), SHA256: digest(data), MIME: "image/png"}}}
	image, err := f.Open(context.Background(), testKey, "media0001", false)
	if err != nil {
		t.Fatal(err)
	}
	if image.RedirectURL != "" || image.Reader == nil {
		t.Fatal("unprotected OSS redirect")
	}
	raw, e := io.ReadAll(image.Reader)
	if e != nil || string(raw) != string(data) {
		t.Fatal("protected body mismatch", e)
	}
	if e = image.Reader.Close(); e != nil {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(temp)
	if len(entries) != 0 {
		t.Fatal("temporary download retained")
	}
	b.data = []byte("corrupt")
	if _, e = f.Open(context.Background(), testKey, "media0001", false); e == nil {
		t.Fatal("unverified remote bytes returned")
	}
	entries, _ = os.ReadDir(temp)
	if len(entries) != 0 {
		t.Fatal("corrupt temporary copy retained")
	}
}

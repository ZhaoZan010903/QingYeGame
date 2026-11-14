package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	mediaapp "qingye/backend/internal/application/media"
	"qingye/backend/internal/domain/media"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/responsecrypto"
)

// A remote source can still expose a temporary seekable file. Its presence must
// not cause the HTTP layer to infer persistent-local provenance.
type remoteSourceTestFiles struct{ mediaapp.Files }

func (f remoteSourceTestFiles) Open(ctx context.Context, key, id string, thumb bool) (*media.File, error) {
	file, err := f.Files.Open(ctx, key, id, thumb)
	if err == nil {
		file.ServerLocal = false
	}
	return file, err
}

func TestR24MediaLocalSourceExceptionKeepsGrantsErrorsAndRemoteEncryption(t *testing.T) {
	f := newChatFixture(t)
	id := f.image(t, f.member, "product", "")
	store := enableReadProtection(t, f.a, "test:r24-source:"+newID()+":")
	f.a.Config.ResponseCrypto.Enabled = true
	f.a.ResponseKeys = &responseMemoryKeys{keys: map[string]responsecrypto.Key{}}
	h := f.a.Routes()
	init := httptest.NewRequest("GET", responseInitPath, nil)
	init.Header.Set("X-QY-Init", "1")
	init.AddCookie(f.cookies[f.member])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, init)
	statusIs(t, w, 200)
	var ready struct {
		Data responsecrypto.Key `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ready); err != nil || ready.Data.Material == "" {
		t.Fatal("response context unavailable", err)
	}
	key := ready.Data
	plan := func() (mediaDelivery, *httptest.ResponseRecorder) {
		body := `{"items":[{"id":"` + id + `"}]}`
		r := responseRequestFor(key, f.cookies[f.member], "POST", "/api/v1/media/access-plan")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://qingye.test")
		r.Body = io.NopCloser(strings.NewReader(body))
		r.ContentLength = int64(len(body))
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		statusIs(t, out, 200)
		return decodeDelivery(t, decryptResponse(t, f.a, key, r, out)), out
	}
	local, planned := plan()
	if local.Mode != "go" {
		t.Fatal("private local image acquired OSS URL")
	}
	read := responseRequestFor(key, f.cookies[f.member], "GET", local.URL)
	for _, cookie := range planned.Result().Cookies() {
		read.AddCookie(cookie)
	}
	got := httptest.NewRecorder()
	h.ServeHTTP(got, read)
	statusIs(t, got, 200)
	if got.Header().Get("X-QY-Response-Mode") != "local-image" || !strings.HasPrefix(got.Header().Get("Content-Type"), "image/") || got.Header().Get("X-QY-Nonce") != "" {
		t.Fatal("persistent local image was encrypted or leaked envelope metadata")
	}
	plain := append([]byte(nil), got.Body.Bytes()...)
	_, metrics, err := store.Records(context.Background())
	if err != nil || metrics["bytes_sent"] != strconv.Itoa(len(plain)) {
		t.Fatal("local response counted a GCM tag or lost wire bytes", err, metrics)
	}
	replay := httptest.NewRecorder()
	h.ServeHTTP(replay, read.Clone(context.Background()))
	statusIs(t, replay, 403)
	if replay.Header().Get("Content-Type") != responsecrypto.ContentType || replay.Header().Get("X-QY-Response-Mode") == "local-image" {
		t.Fatal("grant rejection bypassed response encryption")
	}
	_ = decryptResponse(t, f.a, key, read, replay)
	f.a.MediaFiles = remoteSourceTestFiles{Files: &mediafiles.Store{Root: f.a.Config.MediaDir}}
	remote, planned := plan()
	read = responseRequestFor(key, f.cookies[f.member], "GET", remote.URL+"&plain=1")
	read.Header.Set("X-QY-Response-Mode", "local-image")
	for _, cookie := range planned.Result().Cookies() {
		read.AddCookie(cookie)
	}
	got = httptest.NewRecorder()
	h.ServeHTTP(got, read)
	statusIs(t, got, 200)
	if got.Header().Get("Content-Type") != responsecrypto.ContentType || got.Header().Get("X-QY-Response-Mode") == "local-image" || !bytes.Equal(decryptResponse(t, f.a, key, read, got), plain) {
		t.Fatal("client flag or temporary file disabled remote encryption")
	}
	_, metrics, err = store.Records(context.Background())
	if err != nil || metrics["bytes_sent"] != strconv.Itoa(len(plain)*2+16) {
		t.Fatal("source-aware wire accounting incorrect", err, metrics)
	}
}

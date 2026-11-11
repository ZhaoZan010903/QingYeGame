package httpapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"golang.org/x/crypto/bcrypt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemberMutationOrigin(t *testing.T) {
	a := &App{Config: Config{AllowedOrigins: []string{"https://qingye.example"}}}
	cases := []struct {
		name, origin, referer, fetch string
		cookie                       bool
		status                       int
	}{
		{"same origin", "https://qingye.example", "", "same-origin", true, 204},
		{"referer fallback", "", "https://qingye.example/me", "", true, 204},
		{"missing origin", "", "", "", true, 403},
		{"suffix attack", "https://qingye.example.evil.test", "", "", true, 403},
		{"cross site", "https://qingye.example", "", "cross-site", true, 403},
		{"null origin", "null", "", "", true, 403},
		{"trusted CLI without session", "", "", "", false, 204},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/me", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Referer", tc.referer)
			r.Header.Set("Sec-Fetch-Site", tc.fetch)
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: strings.Repeat("a", 64)})
			}
			w := httptest.NewRecorder()
			a.memberMutation(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d", w.Code, tc.status)
			}
		})
	}
}

func TestDummyPasswordHashIsValidCostTwelve(t *testing.T) {
	cost, err := bcrypt.Cost([]byte(dummyPasswordHash))
	if err != nil || cost != 12 {
		t.Fatalf("dummy password cost must be 12: %d %v", cost, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte("QingYe dummy login comparison: never an account")); err != nil {
		t.Fatal("dummy password hash is not a valid precomputed comparison")
	}
}

func TestRequestIPTrustsOnlyLocalSingleAddressProxy(t *testing.T) {
	cases := []struct{ remote, forwarded, want string }{{"127.0.0.1:123", "203.0.113.8", "203.0.113.8"}, {"[::1]:123", "2001:db8::8", "2001:db8::8"}, {"203.0.113.7:123", "203.0.113.8", "203.0.113.7"}, {"127.0.0.1:123", "203.0.113.8, 203.0.113.9", "127.0.0.1"}, {"127.0.0.1:123", "not-an-ip", "127.0.0.1"}}
	for _, tc := range cases {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		if actual := requestIP(r); actual != tc.want {
			t.Fatalf("remote %s with forwarded %s: got %s", tc.remote, tc.forwarded, actual)
		}
	}
}
func TestMemberVersionConflict(t *testing.T) {
	for _, v := range []int{0, 1, 3} {
		w := httptest.NewRecorder()
		if checkMemberVersion(w, 2, v) || w.Code != 409 {
			t.Fatal("stale version accepted")
		}
	}
	w := httptest.NewRecorder()
	if !checkMemberVersion(w, 2, 2) {
		t.Fatal("current version rejected")
	}
}
func TestUploadTypeAndPathValidation(t *testing.T) {
	if !validUploadName("photo.JPEG", "image/jpeg") || !validUploadName("photo.webp", "image/webp") {
		t.Fatal("supported image rejected")
	}
	for _, v := range []struct{ name, mime string }{{"photo.svg", "image/jpeg"}, {"photo.jpg.exe", "image/jpeg"}, {"../photo.png", "image/png"}, {"photo.png", "image/jpeg"}} {
		if validUploadName(v.name, v.mime) {
			t.Fatalf("unsafe name accepted: %s", v.name)
		}
	}
	root := t.TempDir()
	for _, key := range []string{"../outside", "/absolute", "C:/absolute", "a\\b", "..", ""} {
		if _, err := mediaPath(root, key); err == nil {
			t.Fatalf("unsafe key accepted: %s", key)
		}
	}
	local, err := mediaPath(root, "legacy/abc123")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(local, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readMediaPath(root, "legacy/abc123"); err != nil {
		t.Fatal(err)
	}
}
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.NRGBA{R: 120, A: 180})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestImageSanitization(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	var original bytes.Buffer
	if err := jpeg.Encode(&original, img, nil); err != nil {
		t.Fatal(err)
	}
	private := []byte("Exif\x00\x00PRIVATE-GPS-METADATA")
	segment := []byte{0xff, 0xe1, 0, byte(len(private) + 2)}
	segment = append(segment, private...)
	source := append([]byte{0xff, 0xd8}, segment...)
	source = append(source, original.Bytes()[2:]...)
	var out bytes.Buffer
	mime, err := reencodeImage(bytes.NewReader(source), &out, "image/jpeg")
	if err != nil || mime != "image/jpeg" {
		t.Fatalf("processing: %s %v", mime, err)
	}
	if bytes.Contains(out.Bytes(), private) {
		t.Fatal("private EXIF was retained")
	}
	if _, _, err = image.Decode(bytes.NewReader(out.Bytes())); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, err = reencodeImage(bytes.NewReader(testPNG(t)), &out, "image/jpeg"); err == nil {
		t.Fatal("forged MIME accepted")
	}
	out.Reset()
	if _, err = reencodeImage(bytes.NewReader([]byte("<svg onload='x'>")), &out, "image/png"); err == nil {
		t.Fatal("non-raster input accepted")
	}
}
func TestImageDecompressionAndOutputBounds(t *testing.T) {
	source := testPNG(t)
	binary.BigEndian.PutUint32(source[16:20], 16000)
	binary.BigEndian.PutUint32(source[20:24], 16000)
	binary.BigEndian.PutUint32(source[29:33], crc32.ChecksumIEEE(source[12:29]))
	var out bytes.Buffer
	if _, err := reencodeImage(bytes.NewReader(source), &out, "image/png"); err == nil || !strings.Contains(err.Error(), "像素") {
		t.Fatalf("oversized pixel count not rejected before allocation: %v", err)
	}
	if _, err := reencodeImage(bytes.NewReader(testPNG(t)), &limitedImageWriter{w: &out, remaining: 5}, "image/png"); err == nil {
		t.Fatal("output byte limit ignored")
	}
}
func TestPaginationRejectsInvalidInput(t *testing.T) {
	for _, query := range []string{"limit=0", "limit=51", "cursor=-1", "cursor=abc", "limit=wat"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/?"+query, nil)
		if _, _, ok := memberPagination(w, r); ok || w.Code != 422 {
			t.Fatalf("accepted %s", query)
		}
	}
}

func TestThumbnailPreservesOriginalAndBoundsSize(t *testing.T) {
	root := t.TempDir()
	original := image.NewRGBA(image.Rect(0, 0, 1440, 800))
	var raw bytes.Buffer
	if err := png.Encode(&raw, original); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "original.png")
	if err := os.WriteFile(source, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	a := &App{Config: Config{MediaDir: root}}
	path, ok := a.thumbnailPath(context.Background(), source, "test")
	if !ok {
		t.Fatal("thumbnail was not generated")
	}
	thumb, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || format != "jpeg" || cfg.Width != 720 || cfg.Height != 400 {
		t.Fatalf("unexpected thumbnail dimensions: %v %s %v", cfg, format, err)
	}
	untouched, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(untouched, raw.Bytes()) {
		t.Fatal("thumbnail processing changed the original")
	}
	cached, ok := a.thumbnailPath(context.Background(), source, "test")
	if !ok || cached != path {
		t.Fatal("thumbnail disk cache missed")
	}
	for _, size := range [][2]int{{1, 1}, {200, 16000}, {16000, 200}, {720, 720}} {
		w, h := thumbnailDimensions(size[0], size[1])
		if w < 1 || h < 1 || w > 720 || h > 720 {
			t.Fatalf("invalid thumbnail size %d×%d", w, h)
		}
	}
}

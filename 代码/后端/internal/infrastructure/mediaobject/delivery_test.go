package mediaobject

import (
	"context"
	"net/url"
	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/media"
	"strings"
	"testing"
	"time"
)

func TestPublishedDeliverySignsOneMinuteWithoutReadingOrDisablingProtection(t *testing.T) {
	u := media.Upload{ID: "media0001", Key: testKey, Size: 12, MIME: "image/png", Purpose: "product", Public: true, PublishedProduct: true}
	o := medialifecycle.Object{MediaID: u.ID, Store: "oss", Key: u.Key, Size: u.Size, MIME: u.MIME, SHA256: strings.Repeat("a", 64)}
	b := &memoryBackend{}
	f := &Files{ProtectedReads: true, Repository: protectedObjectRepo{o}, Published: &Store{backend: b, environment: "acceptance"}}
	if target, found, err := f.SignPublished(context.Background(), u, "original", time.Minute); err != nil || !found || target.URL == "" || len(target.CacheKey) != 64 || b.calls != 1 || !f.ProtectedReads {
		t.Fatal("safe signer failed", found, err)
	}
	for _, mode := range []string{"private", "contact", "unpublished", "size", "id", "mime", "key", "digest", "ttl"} {
		t.Run(mode, func(t *testing.T) {
			v, obj, ttl := u, o, time.Minute
			switch mode {
			case "private":
				v.Public = false
			case "contact":
				v.Purpose = "contact"
			case "unpublished":
				v.PublishedProduct = false
			case "size":
				obj.Size++
			case "id":
				obj.MediaID = "other"
			case "mime":
				obj.MIME = "text/html"
			case "key":
				obj.Key += "-other"
			case "digest":
				obj.SHA256 = "invalid"
			case "ttl":
				ttl = time.Minute + time.Second
			}
			f.Repository = protectedObjectRepo{obj}
			before := b.calls
			target, found, err := f.SignPublished(context.Background(), v, "original", ttl)
			if target.URL != "" || (err == nil && found) || b.calls != before {
				t.Fatal("unsafe object signed", mode, err)
			}
		})
	}
}

func TestLegacyDeliverySignerUsesOnlyExactProductionMappingAndPublicEndpoint(t *testing.T) {
	_, o := legacyReadFixture(t)
	o.ObjectKey = "qingye-test/media/" + o.StorageKey
	c := Config{Environment: "production", Endpoint: "https://oss-cn-beijing-internal.aliyuncs.com", Bucket: o.Bucket, AccessKeyID: "isolated-key", AccessKeySecret: "isolated-secret", TempDir: t.TempDir(), LegacyObjectPrefix: "qingye-test/media/"}
	r, err := NewLegacyReader(c)
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.SignReplica(context.Background(), o, "original", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(target)
	if err != nil || u.Host != o.Bucket+".oss-cn-beijing.aliyuncs.com" || u.Query().Get("x-oss-expires") != "60" {
		t.Fatal("wrong signer endpoint/expiry")
	}
	target, err = r.SignReplica(context.Background(), o, "thumb", time.Minute)
	thumb, parseErr := url.Parse(target)
	if err != nil || parseErr != nil || thumb.Query().Get("x-oss-process") != "image/resize,m_lfit,w_720,h_720,limit_1/format,webp" ||
		thumb.Query().Get("x-oss-expires") != "60" || thumb.Path != u.Path || thumb.Query().Get("x-oss-signature") == u.Query().Get("x-oss-signature") {
		t.Fatal("thumbnail recipe was not included in the signed legacy URL")
	}
	for _, mode := range []string{"bucket", "key", "scope", "digest", "ttl"} {
		v, ttl := o, time.Minute
		switch mode {
		case "bucket":
			v.Bucket = "wrong-bucket"
		case "key":
			v.ObjectKey += "other"
		case "scope":
			v.StorageKey = "images/" + strings.Repeat("a", 64)
			v.ObjectKey = "qingye-test/media/" + v.StorageKey
		case "digest":
			v.SHA256 = "broken"
		case "ttl":
			ttl = 6 * time.Second
		}
		if _, err := r.SignReplica(context.Background(), v, "original", ttl); err == nil {
			t.Fatal("invalid legacy mapping signed", mode)
		}
	}
	legacyPlan, err := r.SignReplica(context.Background(), o, "thumb", 5*time.Second)
	legacyURL, parseErr := url.Parse(legacyPlan)
	if err != nil || parseErr != nil || legacyURL.Query().Get("x-oss-expires") != "5" {
		t.Fatal("previous frontend plan must retain its five-second signature")
	}
}

func TestManagedDeliveryCacheIdentityAndSignedVariants(t *testing.T) {
	ctx := context.Background()
	conf := Config{Environment: "acceptance", Endpoint: "https://oss-cn-beijing-internal.aliyuncs.com", Bucket: "images-test", AccessKeyID: "isolated-key", AccessKeySecret: "isolated-secret", TempDir: t.TempDir()}
	store, err := NewOSS(conf)
	if err != nil {
		t.Fatal(err)
	}
	u := media.Upload{ID: "media0001", Key: testKey, Size: 240, MIME: "image/png", Purpose: "product", Public: true, PublishedProduct: true}
	o := medialifecycle.Object{MediaID: u.ID, Store: "oss", Key: u.Key, Size: u.Size, MIME: u.MIME, SHA256: strings.Repeat("a", 64)}
	f := &Files{Repository: protectedObjectRepo{o}, Published: store}
	sign := func(variant string) media.SignedImage {
		t.Helper()
		result, found, err := f.SignPublished(ctx, u, variant, time.Minute)
		if err != nil || !found || len(result.CacheKey) != 64 {
			t.Fatal("missing signed representation", found, err)
		}
		return result
	}
	original, thumb := sign("original"), sign("thumb")
	a, _ := url.Parse(original.URL)
	b, _ := url.Parse(thumb.URL)
	if original.CacheKey == thumb.CacheKey || thumb.CacheKey != sign("thumb").CacheKey || thumb.MIME != "image/webp" || original.MIME != u.MIME ||
		a.Query().Get("x-oss-expires") != "60" || b.Query().Get("x-oss-expires") != "60" ||
		a.Query().Has("x-oss-process") || b.Query().Get("x-oss-process") != "image/resize,m_lfit,w_720,h_720,limit_1/format,webp" {
		t.Fatal("incorrect signed representation")
	}
	o.SHA256 = strings.Repeat("b", 64)
	f.Repository = protectedObjectRepo{o}
	if thumb.CacheKey == sign("thumb").CacheKey {
		t.Fatal("old cache identity reused after content change")
	}
	for _, mime := range []string{"image/gif", "image/jpeg"} {
		o.MIME, u.MIME = mime, mime
		if mime == "image/jpeg" {
			o.Size, u.Size = 21<<20, 21<<20
		}
		f.Repository = protectedObjectRepo{o}
		output := sign("thumb")
		link, _ := url.Parse(output.URL)
		if output.MIME != mime || (mime == "image/gif" && link.Query().Get("x-oss-process") != "image/resize,m_lfit,w_720,h_720,limit_1") ||
			(mime == "image/jpeg" && link.Query().Has("x-oss-process")) {
			t.Fatal("incompatible OSS image processing", mime)
		}
	}
}

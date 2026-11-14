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

func TestPublishedDeliverySignsFiveSecondsWithoutReadingOrDisablingProtection(t *testing.T) {
	u := media.Upload{ID: "media0001", Key: testKey, Size: 12, MIME: "image/png", Purpose: "product", Public: true, PublishedProduct: true}
	o := medialifecycle.Object{MediaID: u.ID, Store: "oss", Key: u.Key, Size: u.Size, MIME: u.MIME, SHA256: strings.Repeat("a", 64)}
	b := &memoryBackend{}
	f := &Files{ProtectedReads: true, Repository: protectedObjectRepo{o}, Published: &Store{backend: b, environment: "acceptance"}}
	if target, found, err := f.SignPublished(context.Background(), u, 5*time.Second); err != nil || !found || target == "" || b.calls != 1 || !f.ProtectedReads {
		t.Fatal("safe signer failed", found, err)
	}
	for _, mode := range []string{"private", "contact", "unpublished", "size", "id", "mime", "key", "digest", "ttl"} {
		t.Run(mode, func(t *testing.T) {
			v, obj, ttl := u, o, 5*time.Second
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
				ttl = time.Minute
			}
			f.Repository = protectedObjectRepo{obj}
			before := b.calls
			target, found, err := f.SignPublished(context.Background(), v, ttl)
			if target != "" || (err == nil && found) || b.calls != before {
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
	target, err := r.SignReplica(context.Background(), o, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(target)
	if err != nil || u.Host != o.Bucket+".oss-cn-beijing.aliyuncs.com" || u.Query().Get("x-oss-expires") != "5" {
		t.Fatal("wrong signer endpoint/expiry")
	}
	for _, mode := range []string{"bucket", "key", "scope", "digest", "ttl"} {
		v, ttl := o, 5*time.Second
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
		if _, err := r.SignReplica(context.Background(), v, ttl); err == nil {
			t.Fatal("invalid legacy mapping signed", mode)
		}
	}
}

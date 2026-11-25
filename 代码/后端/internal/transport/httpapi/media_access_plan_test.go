package httpapi

import (
	"net/http/httptest"
	"qingye/backend/internal/domain/media"
	securitydomain "qingye/backend/internal/domain/security"
	"strings"
	"testing"
)

func TestMediaPlanRejectsUnboundedMalformedAndDuplicateRequests(t *testing.T) {
	for _, body := range []string{`{"items":[]}`, `{"items":[{"id":"one","variant":"raw"}]}`, `{"items":[{"id":"one"},{"id":"one","variant":"original"}]}`, `{"items":[{"id":"one","product_id":"p"}]}`, `{"items":[{"id":"one","product_version":2}]}`, `{"items":[{"id":"one","url":"https://other.invalid"}]}`, `{"items":[{"id":"one"}]} {}`, `{"items":[{"id":"` + strings.Repeat("a", 4100) + `"}]}`, `{"items":[` + strings.Repeat(`{"id":"one"},`, 12) + `{"id":"last"}]}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/media/access-plan", strings.NewReader(body))
		if _, ok := readMediaPlan(w, r); ok || w.Code < 400 {
			t.Fatal("unsafe request accepted", body[:min(80, len(body))])
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/media/access-plan", strings.NewReader(`{"items":[{"id":"one","product_id":"p","product_version":2},{"id":"one","variant":"thumb"}]}`))
	if items, ok := readMediaPlan(w, r); !ok || len(items) != 2 || items[0].Variant != "original" {
		t.Fatal("valid bounded batch rejected")
	}
}

func TestMediaRoutesAllPublishedProductImagesToOSS(t *testing.T) {
	a := &App{Config: Config{MediaDeliveryEnabled: true}}
	u := media.Upload{Purpose: "product", Public: true, PublishedProduct: true}
	if !a.directProductImage(u) {
		t.Fatal("published image not routed direct")
	}
	for _, purpose := range []string{"chat", "contact", "avatar"} {
		u.Purpose = purpose
		if a.directProductImage(u) {
			t.Fatal("nonproduct image routed direct", purpose)
		}
	}
	u.Purpose, u.PublishedProduct = "product", false
	if a.directProductImage(u) {
		t.Fatal("unpublished product image routed direct")
	}
}

func TestMediaGrantDescriptorBindsIdentityAndRequestedRepresentation(t *testing.T) {
	a := &App{Config: Config{SessionSecret: strings.Repeat("s", 32)}}
	w := securitydomain.ReadSubject{Binding: "visitor", Key: "account", IPKey: "ip", Surface: "public"}
	item := mediaPlanItem{ID: "id", Variant: "original", ProductID: "p", ProductVersion: 1}
	u := media.Upload{Key: "key", Size: 20, MIME: "image/png"}
	base := a.mediaPlanDescriptor(w, item, u)
	for _, kind := range []string{"binding", "ip", "id", "variant", "version", "key", "mime", "size"} {
		v, it, image := w, item, u
		switch kind {
		case "binding":
			v.Binding = "another"
		case "ip":
			v.IPKey = "another"
		case "id":
			it.ID = "another"
		case "variant":
			it.Variant = "thumb"
		case "version":
			it.ProductVersion++
		case "key":
			image.Key = "another"
		case "mime":
			image.MIME = "image/jpeg"
		case "size":
			image.Size++
		}
		if a.mediaPlanDescriptor(v, it, image) == base {
			t.Fatal("grant context not bound", kind)
		}
	}
}

package httpapi

import (
	"fmt"
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

func TestMediaRouteStableIndependentOfBatchAndOptionalVersionHints(t *testing.T) {
	a := &App{Config: Config{SessionSecret: strings.Repeat("s", 32), MediaDeliveryEnabled: true}}
	w := securitydomain.ReadSubject{Binding: "visitor", Key: "account", IPKey: "ip", Surface: "public"}
	u := media.Upload{ID: "id", Key: "legacy/key", Purpose: "product", Public: true, PublishedProduct: true}
	oss := 0
	for i := range 1000 {
		item := mediaPlanItem{ID: fmt.Sprintf("id-%d", i), Variant: "original"}
		initial := a.preferOSS(w, item, u)
		item.Variant = "thumb"
		item.ProductID = "p"
		item.ProductVersion = 9
		if a.preferOSS(w, item, u) != initial {
			t.Fatal("client hint rerolled route")
		}
		if initial {
			oss++
		}
	}
	if oss < 740 || oss > 860 {
		t.Fatal("routing departed target", oss)
	}
	u.SingleProductImage = true
	if !a.preferOSS(w, mediaPlanItem{ID: "single"}, u) {
		t.Fatal("actual single image did not prefer OSS")
	}
	u.PublishedProduct = false
	if a.preferOSS(w, mediaPlanItem{ID: "single"}, u) {
		t.Fatal("private/contact image routed direct")
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

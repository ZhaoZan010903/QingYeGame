package httpapi

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestLegacyImageURLsPreserveTransformParameters(t *testing.T) {
	input := "//example.test/a.jpg?x-oss-process=image/resize,w_800,h_600,https://example.test/b.jpg,//example.test/c.jpg"
	got := legacyImageURLs(input)
	if len(got) != 3 || got[0] != "//example.test/a.jpg?x-oss-process=image/resize,w_800,h_600" {
		t.Fatalf("incorrect URL boundaries: %v", got)
	}
	source, key, err := legacyMediaLocation(got[0])
	if err != nil || source != "https:"+got[0] || !regexpLegacyKey(key) {
		t.Fatalf("unexpected image mapping: %s %s %v", source, key, err)
	}
	if _, _, err = legacyMediaLocation("file:///etc/passwd"); err == nil {
		t.Fatal("file URL accepted")
	}
	for _, key := range []string{"../secret", "legacy/../../secret", "legacy/" + strings.Repeat("z", 64)} {
		if regexpLegacyKey(key) {
			t.Fatalf("unsafe key %s accepted", key)
		}
	}
}
func TestCatalogPaginationAndMoneyBoundary(t *testing.T) {
	for _, raw := range []string{"limit=0", "limit=51", "cursor=%%%", "cursor=LTE"} {
		if _, _, err := decodePage(mustQuery(raw)); err == nil {
			t.Fatalf("invalid page accepted: %s", raw)
		}
	}
	next := nextPage(0, 20, 21).(string)
	limit, offset, err := decodePage(url.Values{"cursor": []string{next}})
	if err != nil || limit != 20 || offset != 20 {
		t.Fatalf("cursor did not round trip")
	}
	for _, raw := range []string{"-1", "1.234", "1e3", "Infinity", "10000000000", "1;DROP TABLE products"} {
		if moneyPattern.MatchString(raw) {
			t.Fatalf("invalid money %q accepted", raw)
		}
	}
	if _, err = productOrder("newest;DROP TABLE products"); err == nil {
		t.Fatal("untrusted order accepted")
	}
}
func mustQuery(raw string) url.Values {
	q, _ := url.ParseQuery(raw)
	if q.Get("cursor") == "" && strings.Contains(raw, "cursor=") {
		q.Set("cursor", strings.TrimPrefix(raw, "cursor="))
	}
	return q
}
func TestRecommendationConfigurationBounds(t *testing.T) {
	c := defaultRecommendations()
	raw, _ := json.Marshal(c)
	if _, err := parseRecommendations(raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"window_days", "view_weight", "favorite_weight", "consultation_weight", "new_bonus", "new_hours"} {
		var data map[string]any
		_ = json.Unmarshal(raw, &data)
		data[field] = -1
		b, _ := json.Marshal(data)
		if _, err := parseRecommendations(b); err == nil {
			t.Fatalf("negative %s accepted", field)
		}
	}
}
func TestPublicProductDoesNotLeakInternalReview(t *testing.T) {
	p := map[string]any{"id": "one", "review_note": "private review", "legacy_payload": map[string]any{"source_url": "private"}, "owner_id": "owner", "legacy_id": "source-id", "approved_version": 1}
	publicProductFields(p)
	if len(p) != 1 || p["id"] != "one" {
		t.Fatal("internal product metadata leaked")
	}
}

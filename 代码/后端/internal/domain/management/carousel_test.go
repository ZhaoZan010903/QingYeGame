package management

import (
	"encoding/json"
	"testing"
)

func TestCarouselLimitsAndVisualStyleValidation(t *testing.T) {
	base := map[string]any{"maxSlides": 30, "slides": []any{}, "editorial": map[string]any{"title": "精选", "intervalMs": 7500}, "sections": []any{}, "shortcuts": []any{}, "publishing": map[string]any{"title": "发布", "action": "发布账号"}}
	slide := map[string]any{"productId": "p", "title": "独立标题", "imagePositionX": 50, "imagePositionY": 50, "imageFit": "cover", "layout": "overlay", "textAlign": "left", "verticalAlign": "center", "placement": "free", "textPositionX": 100, "textPositionY": 0, "textWidth": 45, "titleSize": 48, "descriptionSize": 16, "fontFamily": "serif", "titleColor": "#FFFFFF"}
	base["slides"] = []any{slide}
	validate := func() error { raw, _ := json.Marshal(base); return ValidatePresentation(raw) }
	if err := validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		key   string
		value any
	}{{"textPositionX", 101}, {"textPositionY", -1}, {"titleSize", 73}, {"descriptionSize", 11}, {"textWidth", 24}, {"fontFamily", "url(evil)"}, {"titleColor", "red;position:fixed"}, {"imagePositionX", 2.5}, {"layout", "image-right"}} {
		old := slide[bad.key]
		slide[bad.key] = bad.value
		if validate() == nil {
			t.Errorf("accepted invalid %s", bad.key)
		}
		slide[bad.key] = old
	}
	for _, limit := range []int{0, 31} {
		base["maxSlides"] = limit
		if validate() == nil {
			t.Errorf("accepted limit %d", limit)
		}
	}
	base["maxSlides"] = 1
	copy := map[string]any{}
	for k, v := range slide {
		copy[k] = v
	}
	copy["productId"] = "p2"
	base["slides"] = []any{slide, copy}
	if validate() == nil {
		t.Fatal("saved more slides than configured cap")
	}
}

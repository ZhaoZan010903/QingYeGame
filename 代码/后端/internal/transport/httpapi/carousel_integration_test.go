package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestR16CarouselCacheRevisionVisibilityAndLimit(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	token := recommendationUser(t, a, "carousel-admin", "admin")
	admin := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config.Surface = "admin"
	admin.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	h := memberHTTP{t: t, handler: a.Routes(), ip: "carousel-public", origin: "http://qingye.test"}
	ah := memberHTTP{t: t, handler: admin.Routes(), ip: "carousel-admin", origin: "http://qingye-admin.test"}
	if _, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type) VALUES('carousel-sale','出售','sale');INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,status,version,approved_version) SELECT 'carousel-'||i,'QYCH'||i,'carousel-sale','测试轮播'||i,'sale','account','published',1,1 FROM generate_series(1,15) i`); err != nil {
		t.Fatal(err)
	}
	slides := []map[string]any{}
	for i := 1; i <= 15; i++ {
		slides = append(slides, map[string]any{"productId": fmt.Sprintf("carousel-%d", i), "title": fmt.Sprintf("运营标题%d", i), "imagePositionX": 50, "imagePositionY": 50, "imageFit": "contain", "layout": "overlay", "textAlign": "left", "verticalAlign": "center", "placement": "free", "titleSize": 44, "textWidth": 55})
	}
	cfg := map[string]any{"maxSlides": 20, "slides": slides, "sections": []any{}, "shortcuts": []any{}, "editorial": map[string]any{"title": "甄选", "intervalMs": 7500}, "publishing": map[string]any{"title": "发布", "action": "发布账号"}}
	save := func() {
		ah.call("PUT", "/api/v1/admin/settings/home_presentation", map[string]any{"value": cfg}, token, 200)
	}
	save()
	revision := func() string {
		var r string
		if err := a.DB.QueryRow(ctx, "SELECT cache_revision FROM site_settings WHERE key='home_presentation'").Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	old := revision()
	cachedKeys := []string{old}
	defer func() {
		for _, r := range cachedKeys {
			a.Redis.Del(ctx, "qingye:home:presentation:"+r)
		}
	}()
	home := h.call("GET", "/api/v1/home", nil, nil, 200)
	var out struct {
		Data struct {
			Featured []any `json:"featured"`
		} `json:"data"`
	}
	json.Unmarshal(home.Body.Bytes(), &out)
	if len(out.Data.Featured) != 15 {
		t.Fatalf("carousel still capped at 12: %d", len(out.Data.Featured))
	}
	if !strings.Contains(a.Redis.Get(ctx, "qingye:home:presentation:"+old).Val(), "运营标题1") {
		t.Fatal("configuration absent from Redis")
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO product_dictionary_options(dictionary_key,value,label) VALUES('server','carousel-region','轮播区服中文名'); UPDATE products SET attributes='{"server":"carousel-region"}' WHERE id='carousel-1'`); err != nil {
		t.Fatal(err)
	}
	home = h.call("GET", "/api/v1/home", nil, nil, 200)
	if !strings.Contains(home.Body.String(), "轮播区服中文名") || !strings.Contains(home.Body.String(), "carousel-region") {
		t.Fatal("cached presentation must not freeze dictionary labels or replace raw codes")
	}
	slides[0]["title"] = "新保存的运营标题"
	save()
	current := revision()
	cachedKeys = append(cachedKeys, current)
	if current == old {
		t.Fatal("save failed to replace revision")
	}
	home = h.call("GET", "/api/v1/home", nil, nil, 200)
	if !strings.Contains(home.Body.String(), "新保存的运营标题") {
		t.Fatal("stale cache after save")
	}
	if _, err := a.DB.Exec(ctx, "UPDATE products SET status='offline' WHERE id='carousel-1'"); err != nil {
		t.Fatal(err)
	}
	home = h.call("GET", "/api/v1/home", nil, nil, 200)
	json.Unmarshal(home.Body.Bytes(), &out)
	if len(out.Data.Featured) != 14 {
		t.Fatal("offline product retained by cached config")
	}
	cfg["slides"] = []any{}
	save()
	cachedKeys = append(cachedKeys, revision())
	home = h.call("GET", "/api/v1/home", nil, nil, 200)
	json.Unmarshal(home.Body.Bytes(), &out)
	if len(out.Data.Featured) != 0 {
		t.Fatal("cleared slides restored from stale cache")
	}
}

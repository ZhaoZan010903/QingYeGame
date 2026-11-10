package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAdminContactPreservationAndCategoryContracts(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	admin := *a
	admin.Config = a.Config
	admin.Config.Surface = "admin"
	admin.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	h := memberHTTP{t: t, handler: admin.Routes(), ip: "config-" + newID(), origin: "http://qingye-admin.test"}
	first := recommendationUser(t, a, "config-admin-a", "admin")
	second := recommendationUser(t, a, "config-admin-b", "admin")
	mediaID := newID()
	_, err := a.DB.Exec(ctx, "INSERT INTO media_files(id,owner_id,status) VALUES($1,'config-admin-a','ready')", mediaID)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"name": "客服配置测试", "purpose": []string{"general"}, "channel": "wechat", "contact_value": "test-contact", "qr_media_id": mediaID, "hours": "09:00-18:00", "enabled": true}
	contact := memberData(t, h.call("POST", "/api/v1/admin/contacts", input, first, 200))
	id := contact["id"].(string)
	w := h.call("GET", "/api/v1/admin/contacts", nil, second, 200)
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0]["qr_media_id"] != mediaID || list.Data[0]["qr_url"] != "/api/v1/media/"+mediaID+"/file" {
		t.Fatalf("saved QR preview not derivable after reload: %s", w.Body.String())
	}
	// Retaining this contact's existing ready QR is allowed to another administrator.
	input["hours"] = "10:00-19:00"
	h.call("PUT", "/api/v1/admin/contacts/"+id, input, second, 200)
	// The exemption does not let that administrator bind the other owner's media elsewhere.
	h.call("POST", "/api/v1/admin/contacts", input, second, 422)
	otherMedia := newID()
	_, err = a.DB.Exec(ctx, "INSERT INTO media_files(id,owner_id,status) VALUES($1,'config-admin-a','ready')", otherMedia)
	if err != nil {
		t.Fatal(err)
	}
	input["qr_media_id"] = otherMedia
	h.call("PUT", "/api/v1/admin/contacts/"+id, input, second, 422)
	ownMedia := newID()
	_, err = a.DB.Exec(ctx, "INSERT INTO media_files(id,owner_id,status) VALUES($1,'config-admin-b','ready')", ownMedia)
	if err != nil {
		t.Fatal(err)
	}
	input["qr_media_id"] = ownMedia
	h.call("PUT", "/api/v1/admin/contacts/"+id, input, second, 200)
	input["qr_media_id"] = nil
	h.call("PUT", "/api/v1/admin/contacts/"+id, input, first, 200)
	w = h.call("GET", "/api/v1/admin/contacts", nil, first, 200)
	if strings.Contains(w.Body.String(), "/api/v1/media/") {
		t.Fatal("removed QR still has a display URL")
	}

	category := map[string]any{"code": "single-choice", "name": "单选测试", "trade_type": "sale", "price_units": []string{"account"}, "filter_config": []map[string]any{{"key": "quality", "label": "账号品质", "multiple": false, "options": []map[string]string{{"value": "one", "label": "一"}, {"value": "two", "label": "二"}}}}, "enabled": true}
	h.call("POST", "/api/v1/admin/categories", category, first, 200)
	delete(category, "code")
	category["trade_type"] = "rent"
	category["price_units"] = []string{"day"}
	response := h.call("PUT", "/api/v1/admin/categories/single-choice", category, first, 422)
	if !strings.Contains(response.Body.String(), "IMMUTABLE_TRADE_TYPE") {
		t.Fatal("trade type change was not explicitly rejected")
	}
	delete(category, "trade_type")
	category["price_units"] = []string{"account"}
	h.call("PUT", "/api/v1/admin/categories/single-choice", category, first, 200)
	product := ProductInput{CategoryID: "single-choice", Title: "单选属性", TradeType: "sale", PriceUnit: "account", Description: "测试说明", Attributes: map[string]any{"quality": []any{"one", "two"}}}
	if err = a.validateProductInput(ctx, product, false); err == nil {
		t.Fatal("single choice accepted multiple values")
	}
	product.Attributes["quality"] = []any{"one", "one"}
	if err = a.validateProductInput(ctx, product, false); err == nil {
		t.Fatal("single choice accepted duplicate values")
	}
	product.Attributes["quality"] = "one"
	if err = a.validateProductInput(ctx, product, false); err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `UPDATE categories SET filter_config=jsonb_set(filter_config,'{0,multiple}','true') WHERE id='single-choice'`)
	if err != nil {
		t.Fatal(err)
	}
	product.Attributes["quality"] = []any{"one", "two"}
	if err = a.validateProductInput(ctx, product, false); err != nil {
		t.Fatal(err)
	}
	product.Attributes["quality"] = []any{"one", "one"}
	if err = a.validateProductInput(ctx, product, false); err == nil {
		t.Fatal("multiple choice accepted duplicate options")
	}
}

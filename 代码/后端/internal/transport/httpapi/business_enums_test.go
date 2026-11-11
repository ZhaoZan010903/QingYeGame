package httpapi

import "testing"

func TestBusinessEnumsRejectConcatenatedValues(t *testing.T) {
	f := newChatFixture(t)
	category := map[string]any{"code": "enum-check", "name": "枚举验证", "trade_type": "rent", "price_units": []string{"day"}, "enabled": true}
	f.call(t, f.first, "POST", "/api/v1/admin/categories", category, 200)
	for _, invalid := range []string{"hour|day", "day|week", "month|unknown"} {
		t.Run("category-unit-"+invalid, func(t *testing.T) {
			category["price_units"] = []string{invalid}
			f.call(t, f.first, "PUT", "/api/v1/admin/categories/enum-check", category, 422)
		})
	}
	category["price_units"] = []string{"day", "month"}
	f.call(t, f.first, "PUT", "/api/v1/admin/categories/enum-check", category, 200)
	contact := map[string]any{"name": "合法客服", "channel": "qq", "contact_value": "123456", "purpose": []string{"general", "rent"}, "enabled": true}
	t.Run("contact-channel", func(t *testing.T) {
		contact["channel"] = "wechat|qq"
		f.call(t, f.first, "POST", "/api/v1/admin/contacts", contact, 422)
	})
	contact["channel"] = "qq"
	t.Run("contact-purpose", func(t *testing.T) {
		contact["purpose"] = []string{"sale|rent"}
		f.call(t, f.first, "POST", "/api/v1/admin/contacts", contact, 422)
	})
	contact["purpose"] = []string{"general", "rent"}
	f.call(t, f.first, "POST", "/api/v1/admin/contacts", contact, 200)
	t.Run("member-status", func(t *testing.T) {
		f.call(t, f.member, "GET", "/api/v1/me/products?status=draft%7Cpending", nil, 422)
	})
	for _, status := range []string{"", "draft", "pending", "published", "rejected", "offline"} {
		f.call(t, f.member, "GET", "/api/v1/me/products?status="+status, nil, 200)
	}
}

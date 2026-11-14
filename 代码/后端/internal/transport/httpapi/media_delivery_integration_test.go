package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	mediaapp "qingye/backend/internal/application/media"
	"qingye/backend/internal/domain/media"
	"qingye/backend/internal/infrastructure/mediafiles"
)

type deliveryTestFiles struct {
	mediaapp.Files
	signed int
}

func (f *deliveryTestFiles) SignPublished(_ context.Context, u media.Upload, ttl time.Duration) (string, bool, error) {
	if !u.Public || !u.PublishedProduct || u.Purpose != "product" || ttl != 5*time.Second {
		panic("unapproved direct image")
	}
	f.signed++
	return "https://isolated-bucket.oss-cn-beijing.aliyuncs.com/fixture?temporary=5", true, nil
}
func decodeDelivery(t *testing.T, body []byte) mediaDelivery {
	t.Helper()
	var out struct {
		Data struct {
			Items []mediaDelivery `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Data.Items) != 1 {
		t.Fatal("invalid delivery response", err)
	}
	return out.Data.Items[0]
}

func TestR24MediaPlanPublishedOSSAndPrivateGoStaySeparate(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	public := f.image(t, f.member, "product", "")
	private := f.image(t, f.member, "product", "")
	if _, err := f.a.DB.Exec(ctx, "INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,0)", f.product, public); err != nil {
		t.Fatal(err)
	}
	store := enableReadProtection(t, f.a, "test:r24-delivery:"+newID()+":")
	f.a.Config.MediaDeliveryEnabled = true
	files := &deliveryTestFiles{Files: &mediafiles.Store{Root: f.a.Config.MediaDir}}
	f.a.MediaFiles = files
	handler := f.a.Routes()
	status := surfaceRequest(t, handler, "GET", "/api/v1/security/media-access", "", nil)
	statusIs(t, status, 200)
	cookies := status.Result().Cookies()
	body := map[string]any{"items": []map[string]any{{"id": public}}}
	response := surfaceRequest(t, handler, "POST", "/api/v1/media/access-plan", "http://qingye.test", body, cookies...)
	statusIs(t, response, 200)
	out := decodeDelivery(t, response.Body.Bytes())
	if out.Mode != "oss" || out.ID != public || out.Variant != "original" || out.ExpiresAt.Before(time.Now()) || out.ExpiresAt.After(time.Now().Add(5*time.Second)) || files.signed != 1 {
		t.Fatal("published single image not short signed", out.Mode)
	}
	_, metrics, err := store.Records(ctx)
	if err != nil || metrics["oss_plans_issued"] != "1" || metrics["bytes_sent"] != "" {
		t.Fatal("issuance misreported as actual download", err, metrics)
	}
	body = map[string]any{"items": []map[string]any{{"id": private}}}
	statusIs(t, surfaceRequest(t, handler, "POST", "/api/v1/media/access-plan", "http://qingye.test", body, cookies...), 404)
	memberCookies := []*http.Cookie{f.cookies[f.member]}
	memberStatus := surfaceRequest(t, handler, "GET", "/api/v1/security/media-access", "", nil, memberCookies...)
	statusIs(t, memberStatus, 200)
	memberCookies = append(memberCookies, memberStatus.Result().Cookies()...)
	response = surfaceRequest(t, handler, "POST", "/api/v1/media/access-plan", "http://qingye.test", body, memberCookies...)
	statusIs(t, response, 200)
	out = decodeDelivery(t, response.Body.Bytes())
	if out.Mode != "go" || !strings.Contains(out.URL, "grant=") || files.signed != 1 {
		t.Fatal("private image signed directly")
	}
	statusIs(t, surfaceRequest(t, handler, "GET", out.URL, "", nil, cookies...), 404)
	statusIs(t, surfaceRequest(t, handler, "GET", out.URL, "", nil, memberCookies...), 200)
	statusIs(t, surfaceRequest(t, handler, "GET", out.URL, "", nil, memberCookies...), 403)
	_, metrics, err = store.Records(ctx)
	if err != nil || metrics["go_plans_issued"] != "1" || metrics["go_transfers_started"] != "1" {
		t.Fatal("Go grant accounting lost", err, metrics)
	}
}

func TestR24MediaGoGrantRechecksVisibilityAndProductVersion(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	id := f.image(t, f.member, "product", "")
	if _, err := f.a.DB.Exec(ctx, "INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,0)", f.product, id); err != nil {
		t.Fatal(err)
	}
	enableReadProtection(t, f.a, "test:r24-recheck:"+newID()+":")
	h := f.a.Routes()
	status := surfaceRequest(t, h, "GET", "/api/v1/security/media-access", "", nil)
	statusIs(t, status, 200)
	cookies := status.Result().Cookies()
	var version int64
	if err := f.a.DB.QueryRow(ctx, "SELECT version FROM products WHERE id=$1", f.product).Scan(&version); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"items": []map[string]any{{"id": id, "product_id": f.product, "product_version": version}}}
	r := surfaceRequest(t, h, "POST", "/api/v1/media/access-plan", "http://qingye.test", body, cookies...)
	statusIs(t, r, 200)
	out := decodeDelivery(t, r.Body.Bytes())
	if out.Mode != "go" {
		t.Fatal("disabled direct mode issued OSS")
	}
	// A changed representation cannot consume a valid grant for the original.
	statusIs(t, surfaceRequest(t, h, "GET", strings.Replace(out.URL, "variant=original", "variant=thumb", 1), "", nil, cookies...), 403)
	if _, err := f.a.DB.Exec(ctx, "UPDATE products SET status='offline' WHERE id=$1", f.product); err != nil {
		t.Fatal(err)
	}
	statusIs(t, surfaceRequest(t, h, "GET", out.URL, "", nil, cookies...), 404)
	// An administrator still has private permission but must provide the seen version.
	enableReadProtection(t, f.admin, "test:r24-version:"+newID()+":")
	admin := f.admin.Routes()
	adminCookies := []*http.Cookie{f.cookies[f.first]}
	s := surfaceRequest(t, admin, "GET", "/api/v1/security/media-access", "", nil, adminCookies...)
	statusIs(t, s, 200)
	adminCookies = append(adminCookies, s.Result().Cookies()...)
	body = map[string]any{"items": []map[string]any{{"id": id, "product_id": f.product, "product_version": version + 1}}}
	statusIs(t, surfaceRequest(t, admin, "POST", "/api/v1/media/access-plan", "http://qingye-admin.test", body, adminCookies...), 409)
}

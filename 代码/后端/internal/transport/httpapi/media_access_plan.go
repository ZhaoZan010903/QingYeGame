package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	securityapp "qingye/backend/internal/application/security"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/media"
	securitydomain "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/cache"
)

type mediaPlanItem struct {
	ID             string `json:"id"`
	Variant        string `json:"variant,omitempty"`
	ProductID      string `json:"product_id,omitempty"`
	ProductVersion int64  `json:"product_version,omitempty"`
}
type mediaDelivery struct {
	ID        string    `json:"id"`
	Variant   string    `json:"variant"`
	Mode      string    `json:"mode"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	MIME      string    `json:"mime_type"`
	CacheKey  string    `json:"cache_key,omitempty"`
}

func readMediaPlan(w http.ResponseWriter, r *http.Request) ([]mediaPlanItem, bool) {
	var input struct {
		Items []mediaPlanItem `json:"items"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	err := d.Decode(&input)
	if err == nil {
		var extra any
		if d.Decode(&extra) != io.EOF {
			err = errors.New("extra body")
		}
	}
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		fail(w, 413, "MEDIA_PLAN_TOO_LARGE", "图片访问申请过大")
		return nil, false
	}
	if err != nil || len(input.Items) < 1 || len(input.Items) > 12 {
		fail(w, 422, "INVALID_MEDIA_PLAN", "每次只能申请1至12张图片")
		return nil, false
	}
	seen := map[string]bool{}
	for i := range input.Items {
		item := &input.Items[i]
		if item.Variant == "" {
			item.Variant = "original"
		}
		key := item.ID + "\x00" + item.Variant
		if len(item.ID) == 0 || len(item.ID) > 100 || (item.Variant != "original" && item.Variant != "thumb") || seen[key] ||
			(item.ProductID == "") != (item.ProductVersion == 0) || item.ProductVersion < 0 || len(item.ProductID) > 100 {
			fail(w, 422, "INVALID_MEDIA_PLAN", "图片编号、规格或商品版本无效")
			return nil, false
		}
		seen[key] = true
	}
	return input.Items, true
}

func (a *App) mediaPlanDescriptor(who securitydomain.ReadSubject, item mediaPlanItem, u media.Upload) string {
	b, _ := json.Marshal([]any{"media-delivery-v1", who.Key, who.Binding, who.IPKey, item.ID, item.Variant, item.ProductID, item.ProductVersion, u.Key, u.MIME, u.Size})
	return a.mediaMAC(string(b))
}

func (a *App) directProductImage(u media.Upload) bool {
	return a.Config.MediaDeliveryEnabled && u.Public && u.PublishedProduct && u.Purpose == "product"
}

func (a *App) mediaAccessPlan(w http.ResponseWriter, r *http.Request) {
	items, ok := readMediaPlan(w, r)
	if !ok {
		return
	}
	var who securitydomain.ReadSubject
	var deliveryStore securityapp.DeliveryStore
	if a.Config.MediaSafety.Enabled {
		if !a.safetyReady(w) {
			return
		}
		var err error
		who, err = a.mediaSubject(w, r)
		if err != nil {
			a.mediaSafetyError(w, err)
			return
		}
		deliveryStore, ok = a.ReadSafety.Store.(securityapp.DeliveryStore)
		if !ok {
			a.mediaSafetyError(w, securitydomain.ErrUnavailable)
			return
		}
		if err = (cache.Limiter{Client: a.Redis}).Allow(r.Context(), "media-plan:"+who.IPKey, 600, time.Minute); err != nil {
			writeAppError(w, err)
			return
		}
	}
	service := a.mediaService()
	uploads := make([]media.Upload, len(items))
	for i, item := range items {
		u, err := service.Authorize(r.Context(), item.ID, func(context.Context) (media.Actor, error) {
			actor, e := a.currentUser(r)
			if e != nil {
				return media.Actor{}, e
			}
			return a.mediaActor(actor), nil
		})
		if err == nil {
			err = service.CheckProductVersion(r.Context(), item.ID, item.ProductID, item.ProductVersion)
		}
		if err != nil {
			writeAppError(w, err)
			return
		}
		uploads[i] = u
	}
	results := make([]mediaDelivery, 0, len(items))
	// Already-open clients using v1 reject OSS plans longer than ten seconds.
	ossTTL := media.LegacyPublishedImageURLTTL
	if versions := r.Header.Values("X-QY-Media-Plan-Version"); len(versions) == 1 && versions[0] == "2" {
		ossTTL = media.PublishedImageURLTTL
	}
	for i, item := range items {
		out, decision, err := a.issueMediaPlan(r.Context(), who, deliveryStore, item, uploads[i], ossTTL)
		if err != nil {
			if errors.Is(err, securitydomain.ErrUnavailable) {
				a.mediaSafetyError(w, err)
			} else {
				writeAppError(w, err)
			}
			return
		}
		if decision.State != "allowed" {
			writeReadDecision(w, decision)
			return
		}
		results = append(results, out)
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, 200, map[string]any{"data": map[string]any{"server_time": time.Now().UTC(), "items": results}})
}

func (a *App) issueMediaPlan(ctx context.Context, who securitydomain.ReadSubject, store securityapp.DeliveryStore, item mediaPlanItem, u media.Upload, ossTTL time.Duration) (mediaDelivery, securitydomain.ReadDecision, error) {
	out := mediaDelivery{ID: item.ID, Variant: item.Variant, Mode: "go", MIME: u.MIME}
	allowed := securitydomain.AllowedRead()
	lease := newID()
	completed := false
	if store != nil {
		d, err := a.ReadSafety.Store.Begin(ctx, who, lease, u.Size)
		if err != nil || d.State != "allowed" {
			return out, d, err
		}
		// This is an authorization admission only; CompletePlan releases it before
		// returning any URL. A later Go read acquires the actual transfer lease.
		defer func() {
			if completed {
				return
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			_ = a.ReadSafety.Store.Finish(cleanup, who, lease, 0)
		}()
	}
	// OSS v4 dates and Redis TTLs use whole seconds; never advertise a later
	// deadline than the signed capability actually has.
	started := time.Now().UTC().Truncate(time.Second)
	if a.directProductImage(u) {
		if store == nil {
			return out, allowed, securitydomain.ErrUnavailable
		}
		image, found, err := a.mediaService().SignPublished(ctx, u, item.Variant, ossTTL)
		if err != nil || !found {
			return out, allowed, fault.New(fault.Unavailable, "MEDIA_UNAVAILABLE", "图片暂不可用，请稍后重试")
		}
		out.Mode = "oss"
		out.URL, out.MIME, out.CacheKey = image.URL, image.MIME, image.CacheKey
		out.ExpiresAt = started.Add(ossTTL)
	}
	token := randomToken()
	if out.Mode == "go" {
		query := url.Values{"variant": {item.Variant}}
		if store != nil {
			query.Set("grant", token)
		}
		if item.ProductID != "" {
			query.Set("product_id", item.ProductID)
			query.Set("product_version", strconv.FormatInt(item.ProductVersion, 10))
		}
		out.URL = "/api/v1/media/" + url.PathEscape(item.ID) + "/file?" + query.Encode()
		out.ExpiresAt = started.Add(30 * time.Second)
	}
	if store != nil {
		if err := store.CompletePlan(ctx, who, lease, token, a.mediaPlanDescriptor(who, item, u), out.Mode, u.Size); err != nil {
			return out, allowed, err
		}
		completed = true
	}
	return out, allowed, nil
}

func mediaItemFromRequest(r *http.Request) (mediaPlanItem, error) {
	item := mediaPlanItem{ID: r.PathValue("id"), Variant: "original", ProductID: r.URL.Query().Get("product_id")}
	if r.URL.Query().Get("variant") == "thumb" || strings.HasSuffix(r.URL.Path, "/preview") {
		item.Variant = "thumb"
	}
	if raw := r.URL.Query().Get("product_version"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 1 {
			return item, fault.New(fault.Invalid, "INVALID_MEDIA_VERSION", "商品版本无效")
		}
		item.ProductVersion = v
	}
	if (item.ProductID == "") != (item.ProductVersion == 0) {
		return item, fault.New(fault.Invalid, "INVALID_MEDIA_VERSION", "商品版本无效")
	}
	return item, nil
}

package legacyfeed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/domain/legacycatalog"
)

const (
	oldSite         = "https://www.qingyeyouxi.com"
	catalogURL      = oldSite + "/api/admin/md/public/moshi12005/products-checklist1"
	pageSize        = 50
	maxRecords      = 5000
	maxPageBytes    = 16 << 20
	maxCatalogBytes = 64 << 20
)

// SaaS reads the known public storefront feed. The POST is the storefront's
// list query; it carries no account credentials and never performs SaaS writes.
type SaaS struct {
	client *http.Client
}

func NewSaaS() *SaaS {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &SaaS{client: &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (s *SaaS) page(ctx context.Context, index int, remaining *int64) ([]legacysync.Record, int, error) {
	if *remaining <= 0 {
		return nil, 0, errors.New("旧站目录超过单批读取上限")
	}
	target := fmt.Sprintf("%s?index=%d&size=%d&sortName=create_time&sortType=-1", catalogURL, index, pageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(`{"and":{}}`))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", oldSite+"/")
	req.Header.Set("Origin", oldSite)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("旧站商品第 %d 页连接失败", index)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("旧站商品第 %d 页返回 HTTP %d", index, resp.StatusCode)
	}
	limit := min(*remaining, maxPageBytes)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	*remaining -= int64(len(raw))
	if err != nil || int64(len(raw)) > limit {
		return nil, 0, fmt.Errorf("旧站商品第 %d 页读取不完整或超限", index)
	}
	var result struct {
		Status *int                `json:"status"`
		Count  *int                `json:"count"`
		List   []legacysync.Record `json:"list"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || result.Status == nil || *result.Status != 0 ||
		result.Count == nil || *result.Count < 1 || *result.Count > maxRecords || result.List == nil {
		return nil, 0, fmt.Errorf("旧站商品第 %d 页返回了无效目录", index)
	}
	return result.List, *result.Count, nil
}

func (s *SaaS) Catalog(ctx context.Context) (legacysync.Inventory, error) {
	remaining := int64(maxCatalogBytes)
	first, total, err := s.page(ctx, 1, &remaining)
	if err != nil {
		return legacysync.Inventory{}, err
	}
	seen := make(map[string]bool, total)
	records := make([]legacysync.Record, 0, total)
	var newest time.Time
	pages := (total + pageSize - 1) / pageSize
	for page := 1; page <= pages; page++ {
		list, count := first, total
		if page > 1 {
			list, count, err = s.page(ctx, page, &remaining)
			if err != nil {
				return legacysync.Inventory{}, err
			}
		}
		if count != total || len(list) != min(pageSize, total-len(records)) {
			return legacysync.Inventory{}, errors.New("旧站商品分页在读取时发生变化，未使用此次目录")
		}
		for _, item := range list {
			if !legacycatalog.ValidSourceID(item.ID) || seen[item.ID] {
				return legacysync.Inventory{}, errors.New("旧站目录存在重复或无效商品编号")
			}
			seen[item.ID] = true
			records = append(records, item)
			if item.Updated.After(newest) {
				newest = item.Updated
			}
		}
	}
	if pages > 1 {
		again, count, err := s.page(ctx, 1, &remaining)
		if err != nil {
			return legacysync.Inventory{}, err
		}
		if count != total || len(again) != len(first) {
			return legacysync.Inventory{}, errors.New("旧站目录总数在读取时发生变化")
		}
		for i := range again {
			if again[i].ID != first[i].ID {
				return legacysync.Inventory{}, errors.New("旧站目录首屏在读取时发生变化")
			}
		}
	}
	return legacysync.Inventory{Records: records, Latest: legacysync.TimePointer(newest)}, nil
}

func (s *SaaS) Image(ctx context.Context, source string, out io.Writer) (int64, error) {
	imageURL, err := legacycatalog.CanonicalImageURL(source)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return 0, legacysync.ErrImage
	}
	req.Header.Set("Referer", oldSite+"/")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, legacysync.ErrImage
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > legacysync.MaxImageBytes {
		return 0, legacysync.ErrImage
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, legacysync.MaxImageBytes+1))
	if err != nil || n < 1 || n > legacysync.MaxImageBytes || (resp.ContentLength >= 0 && n != resp.ContentLength) {
		return 0, legacysync.ErrImage
	}
	return n, nil
}

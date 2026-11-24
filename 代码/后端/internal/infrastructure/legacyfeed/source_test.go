package legacyfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/domain/legacycatalog"
)

type testTransport func(*http.Request) (*http.Response, error)

func (transport testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestCatalogRejectsPaginationChanges(t *testing.T) {
	for _, scenario := range []string{"完整目录", "缺一条", "编号重复", "首屏变化", "首屏状态变化"} {
		t.Run(scenario, func(t *testing.T) {
			hits := 0
			source := SaaS{client: &http.Client{Transport: testTransport(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost || request.URL.Host != "www.qingyeyouxi.com" ||
					request.Header.Get("Referer") != oldSite+"/" || request.Header.Get("Cookie") != "" {
					t.Fatalf("unexpected source request: %s %s", request.Method, request.URL)
				}
				hits++
				page, _ := strconv.Atoi(request.URL.Query().Get("index"))
				count := pageSize + 1
				start := (page - 1) * pageSize
				list := []legacysync.Record{}
				for i := start; i < min(start+pageSize, count); i++ {
					list = append(list, legacysync.Record{LegacyRecord: legacycatalog.LegacyRecord{
						ID: fmt.Sprintf("legacy-%d", i), Price: json.Number("90"),
						Updated: time.Date(2026, 11, 23, 0, 0, i, 0, time.UTC),
					}})
				}
				if scenario == "缺一条" && page == 2 {
					list = list[:0]
				}
				if scenario == "编号重复" && page == 2 {
					list[0].ID = "legacy-1"
				}
				if scenario == "首屏变化" && hits == 3 {
					list[0].ID = "legacy-replaced"
				}
				if scenario == "首屏状态变化" && hits == 3 {
					list[0].Status = "已售出"
				}
				data, err := json.Marshal(map[string]any{"status": 0, "count": count, "list": list})
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}}
			var progress [][2]int
			inventory, err := source.Catalog(context.Background(), func(completed, total int) error {
				progress = append(progress, [2]int{completed, total})
				return nil
			})
			if scenario != "完整目录" {
				if err == nil || len(inventory.Records) != 0 {
					t.Fatalf("incomplete catalog was returned: %d records, %v", len(inventory.Records), err)
				}
				return
			}
			if err != nil || hits != 3 || len(inventory.Records) != pageSize+1 || inventory.Latest == nil || inventory.Latest.Second() != pageSize {
				t.Fatalf("complete catalog failed: %d requests, %d records, latest %v, %v", hits, len(inventory.Records), inventory.Latest, err)
			}
			if !slices.Equal(progress, [][2]int{{1, 2}, {2, 2}}) {
				t.Fatalf("unexpected page progress: %v", progress)
			}
		})
	}
}

func TestOldImageURLAndReferer(t *testing.T) {
	for _, invalid := range []string{
		"https://cdn.xuansiwei.com.invalid/p.png", "https://cdn.xuansiwei.com:8443/p.png",
		"https://user@cdn.xuansiwei.com/p.png", "file:///a.png", "https://cdn.xuansiwei.com/#x",
	} {
		if _, err := legacycatalog.CanonicalImageURL(invalid); err == nil {
			t.Errorf("accepted %s", invalid)
		}
	}
	source := SaaS{client: &http.Client{Transport: testTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://cdn.xuansiwei.com/p.png" ||
			request.Header.Get("Referer") != oldSite+"/" {
			t.Fatalf("unexpected image request: %s %s", request.Method, request.URL)
		}
		return &http.Response{StatusCode: 200, ContentLength: 1, Body: io.NopCloser(strings.NewReader("x"))}, nil
	})}}
	var result bytes.Buffer
	if n, err := source.Image(context.Background(), "//cdn.xuansiwei.com/p.png", &result); err != nil || n != 1 || result.String() != "x" {
		t.Fatalf("image request failed: %d %q %v", n, result.String(), err)
	}
}

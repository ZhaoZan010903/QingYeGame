package legacysync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	maxRunBytes = 256 << 20
	runTimeout  = 20 * time.Minute
)

var chinaTime = time.FixedZone("CST", 8*60*60)

// ScheduledNoon identifies the calendar occurrence that started the job.
func ScheduledNoon(now time.Time) time.Time {
	local := now.In(chinaTime)
	noon := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, chinaTime)
	if noon.After(local) {
		return noon.AddDate(0, 0, -1)
	}
	return noon
}

type Service struct {
	Source      Source
	Store       Store
	Objects     ObjectStore
	Images      ImagePreparer
	Environment string
	NewID       func() string
}

// A nil schedule produces a manual preview even when automatic publishing is armed.
func (s *Service) Run(ctx context.Context, planned *time.Time) (Batch, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	batch, began, err := s.Store.BeginBatch(ctx, s.NewID(), planned)
	if err != nil || !began {
		return batch, err
	}
	return s.complete(ctx, batch, "")
}

func (s *Service) complete(ctx context.Context, batch Batch, actor string) (Batch, error) {
	failed := func(reason string) (Batch, error) {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		batch.Status, batch.Reason = "failed", reason
		if saveErr := s.Store.FailBatch(finishCtx, batch.ID, reason); saveErr != nil {
			return batch, fmt.Errorf("%s；失败记录暂不可写入", reason)
		}
		return batch, errors.New(reason)
	}
	source, err := s.Source.Catalog(ctx)
	if err != nil || len(source.Records) == 0 {
		if err == nil {
			err = errors.New("旧站目录为空")
		}
		return failed(err.Error())
	}
	current, categories, codes, err := s.Store.LoadCatalog(ctx)
	if err != nil {
		return failed("读取新站商品或分类失败")
	}
	batch.SourceCount = len(source.Records)
	batch.SourceUpdated = source.Latest
	seen := make(map[string]bool, len(source.Records))
	sourceCodes := map[string]int{}
	for _, record := range source.Records {
		sourceCodes[strings.ToUpper(strings.TrimSpace(record.Code))]++
	}
	items := []Item{}
	budget := int64(maxRunBytes)
	for _, record := range source.Records {
		seen[record.ID] = true
		var old *Current
		if product, exists := current[record.ID]; exists {
			old = &product
		}
		code := strings.ToUpper(strings.TrimSpace(record.Code))
		collision := old == nil && (sourceCodes[code] != 1 || codes[code] != "")
		item, urls := planRecord(record, old, categories, collision, batch.StartedAt)
		if item.After != nil && len(urls) > 0 {
			images, err := s.resolveImages(ctx, record.ID, urls, old, &budget)
			if err != nil {
				if ctx.Err() != nil {
					return failed("本批同步超时，未发布商品")
				}
				item.hold("旧站图片读取或校验失败，已保留现有图集")
			} else {
				item.After.Images = images
			}
		}
		if item.After != nil && item.Before != nil && sameProduct(*item.After, *item.Before) {
			if len(item.Notices) == 0 {
				continue
			}
			item.Action, item.After = "hold", nil
		}
		items = append(items, item)
	}
	published := 0
	for id, product := range current {
		if product.Deleted || product.Status != "published" {
			continue
		}
		published++
		if !seen[id] {
			items = append(items, Item{LegacyID: id, Code: product.Code, Action: "missing",
				Before: &product.Product, Notices: []string{"本次没有读到该商品，保留当前状态，需核对旧站"}})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LegacyID < items[j].LegacyID })
	batch.Summary = summarize(items)
	batch.Status = "ready"
	if reason := reviewReason(batch.Summary, published); reason != "" {
		batch.Status, batch.Reason = "review", reason
	}
	if err = s.Store.SavePreview(ctx, batch, items); err != nil {
		return failed("保存批次差异失败")
	}
	if (batch.PlannedAt != nil || actor != "") && batch.Mode == Automatic && batch.Status == "ready" {
		if err = s.Store.ApplyAutomaticBatch(ctx, batch.ID, actor); err != nil {
			return batch, err
		}
		batch.Status = "applied"
	}
	return batch, nil
}

func (s *Service) resolveImages(ctx context.Context, legacyID string, urls []string, old *Current, budget *int64) ([]string, error) {
	result := make([]string, 0, len(urls))
	for _, source := range urls {
		id := ""
		if old != nil {
			id = old.ImagesByURL[source]
		}
		if id == "" {
			var err error
			id, err = s.stageImage(ctx, legacyID, source, budget)
			if err != nil {
				return nil, err
			}
		}
		if !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	return result, nil
}

func (s *Service) stageImage(ctx context.Context, legacyID, source string, budget *int64) (string, error) {
	known, found, err := s.Store.FindAsset(ctx, legacyID, source, "")
	if err != nil {
		return "", err
	}
	if found && known.Ready {
		return known.ID, nil
	}
	if *budget <= 0 {
		return "", ErrImage
	}
	image, read, err := s.Images.Prepare(ctx, min(*budget, MaxImageBytes), func(ctx context.Context, out io.Writer) (int64, error) {
		return s.Source.Image(ctx, source, out)
	})
	*budget -= read
	if err != nil {
		return "", err
	}
	defer image.Body.Close()
	known, found, err = s.Store.FindAsset(ctx, legacyID, source, image.RawSHA256)
	if err != nil {
		return "", err
	}
	if found && known.Ready {
		return known.ID, nil
	}
	if found && (known.Size != image.Size || known.SHA256 != image.SHA256 || known.MIME != image.MIME) {
		return "", ErrImage
	}
	if !found {
		id := s.NewID()
		known = Asset{ID: id, Key: "r20/" + s.Environment + "/objects/" + id + "/" + s.NewID() + "/original",
			MIME: image.MIME, Size: image.Size, SHA256: image.SHA256}
		if err = s.Store.ReserveAsset(ctx, legacyID, source, image.RawSHA256, known); err != nil {
			return "", err
		}
	}
	if err = s.Objects.Put(ctx, known.Key, image.Body, image.Size, image.MIME, image.SHA256); err != nil {
		return "", err
	}
	if err = s.Objects.Verify(ctx, known.Key, image.Size, image.SHA256); err != nil {
		return "", err
	}
	if err = s.Store.ReadyAsset(ctx, known.ID); err != nil {
		return "", err
	}
	return known.ID, nil
}

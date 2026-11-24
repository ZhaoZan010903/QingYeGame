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

	"qingye/backend/internal/domain/fault"
)

const (
	maxRunBytes   = 256 << 20
	runTimeout    = 20 * time.Minute
	commitTimeout = 75 * time.Second
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

func (s *Service) RunScheduled(ctx context.Context, planned time.Time) (Batch, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	batch, began, err := s.Store.BeginScheduledBatch(ctx, s.NewID(), planned)
	if err != nil || !began {
		return batch, err
	}
	return s.complete(ctx, batch, "")
}

func (s *Service) complete(ctx context.Context, batch Batch, actor string) (Batch, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.maintainLease(ctx, batch.ID, cancel)

	var items []Item
	missing := 0
	finish := func(status, reason string) (Batch, error) {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		batch.Status, batch.Reason = status, reason
		batch.Summary = summarize(items)
		batch.Summary.Missing = missing
		saved, err := s.Store.CloseBatch(finishCtx, batch, items)
		if err != nil {
			return batch, fmt.Errorf("暂无法确认本批结果，请查阅批次记录: %w", err)
		}
		if saved.Status == "applied" || saved.Status == "rolled_back" {
			return saved, nil
		}
		return saved, errors.New(saved.Reason)
	}
	reporter := progressReporter{store: s.Store, id: batch.ID}
	read := func(stage string) (Inventory, map[string]string, error) {
		if err := reporter.update(ctx, stage, 0, 0); err != nil {
			return Inventory{}, nil, err
		}
		source, err := s.Source.Catalog(ctx, func(completed, total int) error {
			return reporter.update(ctx, stage, completed, total)
		})
		if err != nil {
			return Inventory{}, nil, err
		}
		states, err := inventoryStates(source)
		return source, states, err
	}
	source, states, err := read(ReadSource)
	if err != nil {
		return finish("failed", stopReason(ctx, err))
	}
	current, categories, codes, err := s.Store.LoadCatalog(ctx)
	if err != nil {
		return finish("failed", "读取新站商品或分类失败")
	}
	visible, unavailable := 0, []string{}
	for id, product := range current {
		if !product.Legacy || product.Deleted || product.Status != "published" ||
			!product.Approved || !categories[product.Category].Enabled {
			continue
		}
		visible++
		status, found := states[id]
		if !found || status != "" && status != "published" {
			unavailable = append(unavailable, id)
			if !found {
				missing++
			}
		}
	}
	if len(unavailable) > 0 {
		// Fresh complete membership is required even when additions offset the removals.
		source = Inventory{}
		verified, latestStates, err := read(VerifySource)
		if err != nil {
			return finish("failed", stopReason(ctx, err))
		}
		if !sameInventory(states, latestStates) {
			return finish("blocked", "两次完整读取的商品集合或上架状态不同，请稍后重新同步")
		}
		source = verified
	}
	batch.SourceCount = len(source.Records)
	batch.SourceUpdated = source.Latest
	if len(unavailable) >= max(12, (visible+9)/10) ||
		len(unavailable) >= 2 && len(source.Records)*2 < visible {
		for _, id := range unavailable {
			product := current[id]
			items = append(items, Item{LegacyID: id, Code: product.Code, Action: "hold", Blocking: true,
				Notices: []string{"旧站目录未显示在售，已触发整批下架保护"}})
		}
		return finish("blocked", fmt.Sprintf("本次有 %d 件在售商品拟下架，触发批量下架保护，请核对旧站目录范围", len(unavailable)))
	}
	total := len(source.Records) + missing
	if err = reporter.update(ctx, Compare, 0, total); err != nil {
		return finish("failed", stopReason(ctx, err))
	}
	sourceCodes := map[string]int{}
	for _, record := range source.Records {
		sourceCodes[strings.ToUpper(strings.TrimSpace(record.Code))]++
	}
	galleries := map[string][]string{}
	for i, record := range source.Records {
		var old *Current
		if product, exists := current[record.ID]; exists {
			old = &product
		}
		code := strings.ToUpper(strings.TrimSpace(record.Code))
		collision := sourceCodes[code] != 1 || codes[code] != "" && (old == nil || codes[code] != old.ID)
		item, urls := planRecord(record, old, categories, collision, batch.StartedAt)
		if item.Action != "" {
			items = append(items, item)
		}
		if len(urls) > 0 {
			galleries[record.ID] = urls
		}
		if err = reporter.update(ctx, Compare, i+1, total); err != nil {
			return finish("failed", stopReason(ctx, err))
		}
	}
	compared := len(source.Records)
	for _, id := range unavailable {
		if _, ok := states[id]; ok {
			continue
		}
		product := current[id]
		items = append(items, planMissing(&product))
		compared++
		if err = reporter.update(ctx, Compare, compared, total); err != nil {
			return finish("failed", stopReason(ctx, err))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LegacyID < items[j].LegacyID })
	if slices.ContainsFunc(items, func(i Item) bool { return i.Blocking }) {
		return finish("blocked", "有商品资料无法通过校验，请处理提示中的商品后重新同步")
	}
	if err = reporter.update(ctx, PrepareMedia, 0, len(galleries)); err != nil {
		return finish("failed", stopReason(ctx, err))
	}
	budget := int64(maxRunBytes)
	prepared := 0
	for i := range items {
		item := &items[i]
		urls := galleries[item.LegacyID]
		if len(urls) == 0 {
			continue
		}
		var old *Current
		if value, ok := current[item.LegacyID]; ok {
			old = &value
		}
		images, err := s.resolveImages(ctx, item.LegacyID, urls, old, &budget)
		switch {
		case ctx.Err() != nil:
			return finish("failed", stopReason(ctx, ctx.Err()))
		case err == nil:
			item.After.Images = images
		case old != nil && old.hasReadyGallery():
			item.Notices = append(item.Notices, "旧站图集下载或校验失败，已保留新站的完整图集")
		case errors.Is(err, ErrMediaBudget):
			item.block(ErrMediaBudget.Error())
			return finish("blocked", "新图下载量超出本批上限，可重新同步并复用已校验图片")
		default:
			item.block("图集下载或校验失败，商品无法上架")
			return finish("blocked", "有商品缺少可用图集，请核对图片后重新同步")
		}
		prepared++
		if err = reporter.update(ctx, PrepareMedia, prepared, len(galleries)); err != nil {
			return finish("failed", stopReason(ctx, err))
		}
	}
	differences := items[:0]
	for _, item := range items {
		if item.After != nil && item.Before != nil && sameProduct(*item.After, *item.Before) {
			if len(item.Notices) == 0 {
				continue
			}
			item.Action, item.After = "hold", nil
		}
		differences = append(differences, item)
	}
	items = differences
	if err = reporter.update(ctx, Commit, 0, 0); err != nil {
		return finish("failed", stopReason(ctx, err))
	}
	batch.Summary = summarize(items)
	batch.Summary.Missing = missing
	// The final database transaction must exit before a silent worker's lease can expire.
	commitCtx, endCommit := context.WithTimeout(ctx, commitTimeout)
	saved, err := s.Store.CommitBatch(commitCtx, batch, items, actor)
	commitDeadline := commitCtx.Err()
	endCommit()
	if err == nil {
		return saved, nil
	}
	if conflict, ok := fault.As(err); ok && (conflict.Kind == fault.Conflict || conflict.Kind == fault.Forbidden) {
		return finish("blocked", conflict.Message)
	}
	if errors.Is(err, ErrLeaseLost) {
		return finish("failed", ErrLeaseLost.Error())
	}
	if commitDeadline == context.DeadlineExceeded {
		return finish("failed", "数据库提交超时，请重新同步")
	}
	return finish("failed", stopReason(ctx, errors.New("批次提交失败，请重新同步")))
}

func inventoryStates(source Inventory) (map[string]string, error) {
	if len(source.Records) == 0 {
		return nil, errors.New("旧站目录为空")
	}
	states := make(map[string]string, len(source.Records))
	for _, record := range source.Records {
		if _, seen := states[record.ID]; seen {
			return nil, errors.New("旧站目录有重复商品")
		}
		states[record.ID] = sourceStatus(record)
	}
	return states, nil
}

func sameInventory(first, second map[string]string) bool {
	if len(first) != len(second) {
		return false
	}
	for id, status := range first {
		if next, ok := second[id]; !ok || next != status {
			return false
		}
	}
	return true
}

func stopReason(ctx context.Context, err error) string {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return "同步超时，请稍后重新同步"
	case context.Canceled:
		return "同步中断，请重新发起"
	}
	return err.Error()
}

func (s *Service) maintainLease(ctx context.Context, id string, cancel context.CancelFunc) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			touch, stop := context.WithTimeout(ctx, 5*time.Second)
			err := s.Store.Heartbeat(touch, id)
			stop()
			if errors.Is(err, ErrLeaseLost) {
				cancel()
				return
			}
		}
	}
}

type progressReporter struct {
	store Store
	id    string
	last  Progress
	at    time.Time
}

func (r *progressReporter) update(ctx context.Context, stage string, completed, total int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value := Progress{Stage: stage, Completed: completed, Total: total}
	if value == r.last || stage == r.last.Stage && completed != total && completed != 1 &&
		r.last.Total == total && time.Since(r.at) < time.Second {
		return nil
	}
	write, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.store.ReportProgress(write, r.id, value); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			return err
		}
		return errors.New("暂无法保存同步进度，请稍后重试")
	}
	r.last, r.at = value, time.Now()
	return nil
}

func (s *Service) resolveImages(ctx context.Context, legacyID string, urls []string, old *Current, budget *int64) ([]string, error) {
	result := make([]string, 0, len(urls))
	seen := make(map[string]bool, len(urls))
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
		if !seen[id] {
			seen[id] = true
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
		return "", ErrMediaBudget
	}
	image, read, err := s.Images.Prepare(ctx, min(*budget, MaxImageBytes), func(ctx context.Context, out io.Writer) (int64, error) {
		return s.Source.Image(ctx, source, out)
	})
	*budget -= read
	if err != nil {
		if *budget <= 0 {
			return "", ErrMediaBudget
		}
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

package legacysync

import (
	"context"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"qingye/backend/internal/domain/fault"
)

var manualBatchID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ManualRunner owns the work beyond the request that initiated it.
type ManualRunner struct {
	ctx     context.Context
	service *Service
	gate    sync.Mutex
	jobs    sync.WaitGroup
	closed  bool
	active  string
}

func NewManualRunner(ctx context.Context, service *Service) *ManualRunner {
	return &ManualRunner{ctx: ctx, service: service}
}

func (r *ManualRunner) Start(actor, id string, version int64) (Batch, error) {
	if !manualBatchID.MatchString(id) || version < 1 {
		return Batch{}, fault.New(fault.Invalid, "INVALID_REQUEST", "同步请求标识无效，请刷新后重试")
	}
	r.gate.Lock()
	defer r.gate.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return Batch{}, fault.New(fault.Unavailable, "LEGACY_SYNC_UNAVAILABLE", "同步服务正在关闭，请稍后重试")
	}
	if r.active != "" && r.active != id {
		return Batch{}, ErrBusy
	}
	// Committing the reservation does not depend on the browser staying connected.
	reserve, closeReserve := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
	defer closeReserve()
	batch, started, err := r.service.Store.BeginManualBatch(reserve, id, actor, version)
	if err != nil || !started {
		return batch, err
	}
	r.active = batch.ID
	r.jobs.Add(1)
	go func() {
		defer r.jobs.Done()
		defer func() {
			r.gate.Lock()
			r.active = ""
			r.gate.Unlock()
		}()
		task, cancel := context.WithTimeout(r.ctx, runTimeout)
		defer cancel()
		if _, err := r.service.complete(task, batch, actor); err != nil {
			slog.Warn("legacy catalog sync stopped", "batch_id", batch.ID, "error", err)
		}
	}()
	return batch, nil
}

func (r *ManualRunner) ActiveBatchID() string {
	r.gate.Lock()
	defer r.gate.Unlock()
	return r.active
}

func (r *ManualRunner) Wait() {
	r.gate.Lock()
	r.closed = true
	r.gate.Unlock()
	r.jobs.Wait()
}

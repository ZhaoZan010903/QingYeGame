package mediaobject

import (
	"context"
	"qingye/backend/internal/domain/fault"
	"sync"
	"time"
)

// Normal browser thumbnail bursts wait briefly; both disk readers and waiters stay bounded.
type readSlots struct {
	active, waiting chan struct{}
	timeout         time.Duration
}

func newReadSlots(active, waiting int, timeout time.Duration) *readSlots {
	return &readSlots{make(chan struct{}, active), make(chan struct{}, waiting), timeout}
}
func (s *readSlots) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	busy := func() (func(), error) {
		return nil, fault.New(fault.RateLimited, "MEDIA_BUSY", "图片读取繁忙，请稍后重试")
	}
	select {
	case s.waiting <- struct{}{}:
	default:
		return busy()
	}
	defer func() { <-s.waiting }()
	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case s.active <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-s.active }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return busy()
	}
}

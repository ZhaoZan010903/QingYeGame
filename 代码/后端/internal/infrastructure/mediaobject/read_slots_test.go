package mediaobject

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrivateGalleryBurstQueuesWithoutRejectingNormalReaders(t *testing.T) {
	gate := newReadSlots(4, 64, time.Second)
	var current, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := gate.acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			n := current.Add(1)
			for p := peak.Load(); n > p; p = peak.Load() {
				if peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			current.Add(-1)
		}()
	}
	wg.Wait()
	if peak.Load() > 4 || len(gate.active) != 0 || len(gate.waiting) != 0 {
		t.Fatalf("unbounded or leaked slots: peak=%d", peak.Load())
	}
}

func TestPrivateGalleryWaitCanExpireAndCancelWithoutLeakingSlots(t *testing.T) {
	gate := newReadSlots(1, 2, 10*time.Millisecond)
	release, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gate.acquire(context.Background()); err == nil {
		t.Fatal("saturated reader did not time out")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = gate.acquire(ctx); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	release()
	release()
	next, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next()
	if len(gate.active) != 0 || len(gate.waiting) != 0 {
		t.Fatal("slots leaked")
	}
}

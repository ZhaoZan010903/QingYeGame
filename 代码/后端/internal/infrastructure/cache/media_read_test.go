package cache

import (
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	app "qingye/backend/internal/application/security"
	domain "qingye/backend/internal/domain/security"
	ids "qingye/backend/internal/infrastructure/security"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mediaReadFixture(t *testing.T) (*MediaReadStore, domain.ReadSubject) {
	t.Helper()
	c := reviewRedis(t)
	s := &MediaReadStore{Client: c, Policy: domain.DefaultReadPolicy(), Namespace: "test:r201:" + ids.NewID() + ":"}
	t.Cleanup(func() {
		ctx := context.Background()
		var cursor uint64
		for {
			keys, next, e := c.Scan(ctx, cursor, s.prefix()+"*", 100).Result()
			if e != nil {
				return
			}
			if len(keys) > 0 {
				_ = c.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	return s, domain.ReadSubject{Key: ids.NewID(), Binding: ids.NewID(), IPKey: ids.NewID(), Surface: "public", Kind: "anonymous"}
}
func readOnce(t *testing.T, s *MediaReadStore, w domain.ReadSubject, size int64) domain.ReadDecision {
	t.Helper()
	ctx := context.Background()
	lease := ids.NewID()
	d, e := s.Begin(ctx, w, lease, size)
	if e != nil {
		t.Fatal(e)
	}
	if d.State == "allowed" {
		if e = s.Finish(ctx, w, lease, size); e != nil {
			t.Fatal(e)
		}
	}
	return d
}
func TestMediaReadSoftChallengeAndHardBudget(t *testing.T) {
	s, w := mediaReadFixture(t)
	s.Policy.Burst = 2
	s.Policy.PerSecond = .0001
	s.Policy.HardBurst = 5
	s.Policy.HardPerSecond = .0001
	for range 2 {
		if d := readOnce(t, s, w, 1024); d.State != "allowed" {
			t.Fatal(d)
		}
	}
	if d := readOnce(t, s, w, 1024); d.State != "challenge" {
		t.Fatal(d)
	}
	ctx := context.Background()
	c, e := s.CreateChallenge(ctx, w, ids.NewID(), domain.MediaReadAction, "qingye.test")
	if e != nil {
		t.Fatal(e)
	}
	c, e = s.ClaimChallenge(ctx, w, c.ID, "token", ids.NewID())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteChallenge(ctx, w, c, true, false); e != nil {
		t.Fatal(e)
	}
	for range 3 {
		if d := readOnce(t, s, w, 1024); d.State != "allowed" {
			t.Fatal(d)
		}
	}
	if d := readOnce(t, s, w, 1024); d.State != "limited" {
		t.Fatalf("verification erased hard budget: %+v", d)
	}
}
func TestMediaReadRangeBudgetAndIdempotentFinish(t *testing.T) {
	s, w := mediaReadFixture(t)
	s.Policy.BytesPerSecond = 1
	ctx := context.Background()
	lease := ids.NewID()
	d, e := s.Begin(ctx, w, lease, 64<<20)
	if e != nil || d.State != "allowed" {
		t.Fatal(d, e)
	}
	for range 2 {
		if e = s.Finish(ctx, w, lease, 128); e != nil {
			t.Fatal(e)
		}
	}
	if n := s.Client.HGet(ctx, s.prefix()+"metrics", "bytes_sent").Val(); n != "128" {
		t.Fatal("duplicate accounting", n)
	}
	if d = readOnce(t, s, w, 64<<20); d.State != "allowed" {
		t.Fatal(d)
	}
	if d = readOnce(t, s, w, 1024); d.State != "limited" {
		t.Fatal("partial downloads bypassed full reservation", d)
	}
}

func TestMediaReadExpandedThumbnailUsesRealSizeBudget(t *testing.T) {
	s, w := mediaReadFixture(t)
	s.Policy.BytesPerSecond = 1
	ctx := context.Background()
	lease := ids.NewID()
	d, e := s.Begin(ctx, w, lease, 100)
	if e != nil || d.State != "allowed" {
		t.Fatal(d, e)
	}
	d, e = s.Expand(ctx, w, 600)
	if e != nil || d.State != "allowed" {
		t.Fatal(d, e)
	}
	if n := s.Client.HGet(ctx, s.prefix()+"metrics", "reserved_bytes").Val(); n != "700" {
		t.Fatal("derived response undercounted", n)
	}
	s.Client.HSet(ctx, s.readKey(w.Key), "bytes", 0)
	d, e = s.Expand(ctx, w, 10000)
	if e != nil || d.State != "limited" {
		t.Fatal("byte shortage ignored", d, e)
	}
	if e = s.Finish(ctx, w, lease, 0); e != nil {
		t.Fatal(e)
	}
}
func TestMediaReadConcurrentLeasesAndRecentDenial(t *testing.T) {
	s, w := mediaReadFixture(t)
	ctx := context.Background()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	leases := make(chan string, 32)
	for range 32 {
		wg.Go(func() {
			id := ids.NewID()
			d, e := s.Begin(ctx, w, id, 1024)
			if e != nil {
				t.Error(e)
				return
			}
			if d.State == "allowed" {
				accepted.Add(1)
				leases <- id
			}
		})
	}
	wg.Wait()
	close(leases)
	if accepted.Load() != 6 {
		t.Fatalf("concurrency not atomic: %d", accepted.Load())
	}
	for id := range leases {
		_ = s.Finish(ctx, w, id, 10)
	}
	if d := readOnce(t, s, w, 1); d.State != "allowed" {
		t.Fatal(d)
	}
	if d, e := s.Status(ctx, w); e != nil || d.State != "limited" {
		t.Fatal("concurrent success erased failed-image recovery signal", d, e)
	}
	// Simulate an expired orphan lease using only this test's private namespace.
	for _, k := range s.keys(w)[4:7] {
		s.Client.ZAdd(ctx, k, redis.Z{Member: "orphan", Score: float64(time.Now().Add(-time.Second).Unix())})
	}
	if d := readOnce(t, s, w, 1); d.State != "allowed" {
		t.Fatal(d)
	}
}
func TestMediaReadSharedIPAndBoundedSubjects(t *testing.T) {
	s, w := mediaReadFixture(t)
	s.Policy.MaxSubjects = 3
	for i := range 3 {
		v := w
		v.Key = fmt.Sprint(i)
		if d := readOnce(t, s, v, 1024); d.State != "allowed" {
			t.Fatal("shared IP false positive", d)
		}
	}
	if d := readOnce(t, s, w, 1024); d.Code != "MEDIA_CAPACITY" {
		t.Fatal(d)
	}
	if n := s.Client.ZCard(context.Background(), s.prefix()+"subjects").Val(); n != 3 {
		t.Fatal("subject cap exceeded", n)
	}
}
func TestMediaReadManualCASAndBinding(t *testing.T) {
	s, w := mediaReadFixture(t)
	ctx := context.Background()
	readOnce(t, s, w, 1024)
	records, _, e := s.Records(ctx)
	if e != nil || len(records) != 1 {
		t.Fatal(e)
	}
	version := records[0].Version
	if e = s.Control(ctx, w.Key, version, 600); e != nil {
		t.Fatal(e)
	}
	if d := readOnce(t, s, w, 1024); d.State != "restricted" {
		t.Fatal(d)
	}
	if e = s.Control(ctx, w.Key, version, 0); e == nil {
		t.Fatal("stale operator overwrote restriction")
	}
	c, e := s.CreateChallenge(ctx, w, ids.NewID(), domain.MediaReadAction, "qingye.test")
	if e != nil {
		t.Fatal(e)
	}
	other := w
	other.Binding = "another-browser"
	if _, e = s.ClaimChallenge(ctx, other, c.ID, "token", ids.NewID()); !errors.Is(e, domain.ErrChallenge) {
		t.Fatal("cross-session accepted", e)
	}
	c, e = s.ClaimChallenge(ctx, w, c.ID, "token", ids.NewID())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteChallenge(ctx, w, c, true, false); e != nil {
		t.Fatal(e)
	}
	if d := readOnce(t, s, w, 1024); d.State != "restricted" {
		t.Fatal("challenge removed operator restriction", d)
	}
	records, _, _ = s.Records(ctx)
	if e = s.Control(ctx, w.Key, records[0].Version, 0); e != nil {
		t.Fatal(e)
	}
	if d := readOnce(t, s, w, 1024); d.State != "allowed" {
		t.Fatal(d)
	}
}

type acceptingVerifier struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (v *acceptingVerifier) Verify(context.Context, string, string, string, string, string) (bool, error) {
	v.calls.Add(1)
	if v.started != nil {
		close(v.started)
		<-v.release
	}
	return true, nil
}
func TestMediaReadConcurrentProviderSuccessGrantsOnce(t *testing.T) {
	s, w := mediaReadFixture(t)
	ctx := context.Background()
	readOnce(t, s, w, 1)
	v := &acceptingVerifier{started: make(chan struct{}), release: make(chan struct{})}
	h := app.HumanService{Store: s, Verifier: v, NewID: ids.NewID}
	c, e := h.Create(ctx, w, "qingye.test")
	if e != nil {
		t.Fatal(e)
	}
	result := make(chan error, 1)
	go func() { result <- h.Verify(ctx, w, c.ID, "token", "") }()
	<-v.started
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if e := h.Verify(ctx, w, c.ID, "token", ""); !errors.Is(e, domain.ErrBusy) {
				t.Error("duplicate provider claim", e)
			}
		})
	}
	wg.Wait()
	close(v.release)
	if e = <-result; e != nil {
		t.Fatal(e)
	}
	if e = h.Verify(ctx, w, c.ID, "token", ""); !errors.Is(e, domain.ErrChallenge) {
		t.Fatal(e)
	}
	if v.calls.Load() != 1 || s.Client.HGet(ctx, s.prefix()+"metrics", "verified").Val() != "1" {
		t.Fatal("duplicate privilege grant")
	}
}
func TestMediaReadChallengeRetryOwnershipAndExpiry(t *testing.T) {
	s, w := mediaReadFixture(t)
	ctx := context.Background()
	readOnce(t, s, w, 1)
	c, _ := s.CreateChallenge(ctx, w, ids.NewID(), domain.MediaReadAction, "qingye.test")
	claimed, e := s.ClaimChallenge(ctx, w, c.ID, "hash", ids.NewID())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteChallenge(ctx, w, claimed, false, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimChallenge(ctx, w, c.ID, "different-token", ids.NewID()); !errors.Is(e, domain.ErrChallenge) {
		t.Fatal(e)
	}
	newer, e := s.ClaimChallenge(ctx, w, c.ID, "hash", ids.NewID())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteChallenge(ctx, w, claimed, true, false); !errors.Is(e, domain.ErrChallenge) {
		t.Fatal("old worker granted after losing ownership", e)
	}
	if e = s.CompleteChallenge(ctx, w, newer, false, true); e != nil {
		t.Fatal(e)
	}
	fresh, e := s.CreateChallenge(ctx, w, ids.NewID(), domain.MediaReadAction, "qingye.test")
	if e != nil || fresh.ID == c.ID {
		t.Fatal("manual retry recycled token-bound challenge", e)
	}
	s.Client.HSet(ctx, s.prefix()+"c:"+fresh.ID, "expires", time.Now().Add(-time.Second).Unix())
	if _, e = s.ClaimChallenge(ctx, w, fresh.ID, "hash", ids.NewID()); !errors.Is(e, domain.ErrChallenge) {
		t.Fatal("expired accepted", e)
	}
}

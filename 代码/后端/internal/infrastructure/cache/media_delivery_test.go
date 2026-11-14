package cache

import (
	"context"
	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/security"
	ids "qingye/backend/internal/infrastructure/security"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newBudgetedPlan(t *testing.T, s *MediaReadStore, w domain.ReadSubject, mode string) (string, string) {
	t.Helper()
	ctx := context.Background()
	lease := ids.NewID()
	token := ids.RandomToken()
	descriptor := strings.Repeat("a", 64)
	d, err := s.Begin(ctx, w, lease, 1024)
	if err != nil || d.State != "allowed" {
		t.Fatal(d, err)
	}
	if err = s.CompletePlan(ctx, w, lease, token, descriptor, mode, 1024); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(ctx, w, lease, 0); err != nil {
		t.Fatal(err)
	}
	return token, descriptor
}

func TestMediaDeliveryGrantConsumesOnceWithoutDoubleCharging(t *testing.T) {
	s, w := mediaReadFixture(t)
	ctx := context.Background()
	token, descriptor := newBudgetedPlan(t, s, w, "go")
	before, err := s.Client.HGetAll(ctx, s.readKey(w.Key)).Result()
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			lease := ids.NewID()
			d, e := s.BeginGrant(ctx, w, token, descriptor, lease)
			if e == nil && d.State == "allowed" {
				accepted.Add(1)
				if e = s.Finish(ctx, w, lease, 1024); e != nil {
					t.Error(e)
				}
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("grant replayed", accepted.Load())
	}
	after, err := s.Client.HGetAll(ctx, s.readKey(w.Key)).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"requests", "hard", "soft", "bytes"} {
		if before[key] != after[key] {
			t.Fatal("Go grant charged again", key, before[key], after[key])
		}
	}
	if after["bytes_sent"] != "1024" {
		t.Fatal("transfer accounting lost", after["bytes_sent"])
	}
	for _, key := range s.keys(w)[4:7] {
		if n := s.Client.ZCard(ctx, key).Val(); n != 0 {
			t.Fatal("lease leaked", n)
		}
	}
}

func TestMediaDeliveryGrantBindsIdentitySpecAndExpires(t *testing.T) {
	s, w := mediaReadFixture(t)
	ctx := context.Background()
	token, descriptor := newBudgetedPlan(t, s, w, "go")
	for _, kind := range []string{"binding", "ip", "surface", "descriptor"} {
		other, value := w, descriptor
		switch kind {
		case "binding":
			other.Binding = "another"
		case "ip":
			other.IPKey = "another"
		case "surface":
			other.Surface = "admin"
		case "descriptor":
			value = strings.Repeat("b", 64)
		}
		if _, err := s.BeginGrant(ctx, other, token, value, ids.NewID()); err == nil {
			t.Fatal("unbound grant", kind)
		}
	}
	if err := s.Client.PExpire(ctx, s.prefix()+"grant:"+token, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	_, err := s.BeginGrant(ctx, w, token, descriptor, ids.NewID())
	f, ok := fault.As(err)
	if !ok || f.Code != "MEDIA_GRANT_EXPIRED" {
		t.Fatal("expired grant accepted", err)
	}
}

func TestMediaDeliveryOSSPlansDoNotHoldTransferLeasesOrClaimDownloadBytes(t *testing.T) {
	s, w := mediaReadFixture(t)
	ctx := context.Background()
	for range 12 {
		newBudgetedPlan(t, s, w, "oss")
	}
	for _, key := range s.keys(w)[4:7] {
		if n := s.Client.ZCard(ctx, key).Val(); n != 0 {
			t.Fatal("OSS held transfer lease", n)
		}
	}
	stats, err := s.Client.HGetAll(ctx, s.prefix()+"metrics").Result()
	if err != nil {
		t.Fatal(err)
	}
	if stats["oss_plans_issued"] != "12" || stats["oss_planned_bytes"] != "12288" || stats["bytes_sent"] != "" {
		t.Fatal("OSS issuance counted as download", stats)
	}
	if n := s.Client.ZCard(ctx, s.prefix()+"grants:global").Val(); n != 0 {
		t.Fatal("OSS created unused Go grants")
	}
}

func TestMediaDeliveryGrantRechecksRestrictionAndTransferConcurrency(t *testing.T) {
	s, w := mediaReadFixture(t)
	ctx := context.Background()
	token, descriptor := newBudgetedPlan(t, s, w, "go")
	if err := s.Client.HSet(ctx, s.readKey(w.Key), "manual_until", time.Now().Add(time.Minute).Unix()).Err(); err != nil {
		t.Fatal(err)
	}
	if d, err := s.BeginGrant(ctx, w, token, descriptor, ids.NewID()); err != nil || d.State != "restricted" {
		t.Fatal("manual ban bypassed", d, err)
	}
	if err := s.Client.HSet(ctx, s.readKey(w.Key), "manual_until", 0).Err(); err != nil {
		t.Fatal(err)
	}
	leases := []string{}
	for range 6 {
		lease := ids.NewID()
		d, err := s.Begin(ctx, w, lease, 10)
		if err != nil || d.State != "allowed" {
			t.Fatal(d, err)
		}
		leases = append(leases, lease)
	}
	if d, err := s.BeginGrant(ctx, w, token, descriptor, ids.NewID()); err != nil || d.Code != "MEDIA_CONCURRENCY" {
		t.Fatal("concurrency bypassed", d, err)
	}
	for _, lease := range leases {
		if err := s.Finish(ctx, w, lease, 0); err != nil {
			t.Fatal(err)
		}
	}
	lease := ids.NewID()
	if d, err := s.BeginGrant(ctx, w, token, descriptor, lease); err != nil || d.State != "allowed" {
		t.Fatal("denied attempt consumed grant", d, err)
	}
	if err := s.Finish(ctx, w, lease, 10); err != nil {
		t.Fatal(err)
	}
}

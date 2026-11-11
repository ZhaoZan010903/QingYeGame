package cache

import (
	"context"
	"encoding/json"
	"errors"
	"qingye/backend/internal/infrastructure/security"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHomeCacheCoalescesMissesAndVersions(t *testing.T) {
	client := reviewRedis(t)
	doc := HomeDocument{Client: client}
	revision := security.NewID()
	ctx := context.Background()
	t.Cleanup(func() {
		client.Del(ctx, "qingye:home:presentation:"+revision, "qingye:home:presentation:"+revision+"-new")
	})
	var calls atomic.Int32
	load := func(context.Context) (json.RawMessage, error) {
		calls.Add(1)
		time.Sleep(30 * time.Millisecond)
		return json.RawMessage(`{"slides":[]}`), nil
	}
	var group sync.WaitGroup
	for range 24 {
		group.Go(func() {
			raw, err := doc.Load(ctx, revision, load)
			if err != nil || string(raw) != `{"slides":[]}` {
				t.Errorf("invalid cache result: %s %v", raw, err)
			}
		})
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent miss loaded %d times", calls.Load())
	}
	if ttl := client.TTL(ctx, "qingye:home:presentation:"+revision).Val(); ttl < 260*time.Second || ttl > 330*time.Second {
		t.Fatalf("unbounded TTL: %s", ttl)
	}
	_, _ = doc.Load(ctx, revision, load)
	if calls.Load() != 1 {
		t.Fatal("cache hit read database")
	}
	_, _ = doc.Load(ctx, revision+"-new", load)
	if calls.Load() != 2 {
		t.Fatal("new revision used old content")
	}
}

func TestHomeCacheFallsBackAndNeverCachesDatabaseErrors(t *testing.T) {
	client := reviewRedis(t)
	doc := HomeDocument{Client: client}
	revision := security.NewID()
	ctx := context.Background()
	key := "qingye:home:presentation:" + revision
	t.Cleanup(func() { client.Del(ctx, key) })
	sentinel := errors.New("database failure")
	_, err := doc.Load(ctx, revision, func(context.Context) (json.RawMessage, error) { return nil, sentinel })
	if !errors.Is(err, sentinel) || client.Exists(ctx, key).Val() != 0 {
		t.Fatal("database error swallowed/cached")
	}
	client.Set(ctx, key, "broken JSON", time.Minute)
	got, err := doc.Load(ctx, revision, func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{"slides":[]}`), nil })
	if err != nil || string(got) != `{"slides":[]}` {
		t.Fatal("corrupt cache did not fall back")
	}
	closed := reviewRedis(t)
	closed.Close()
	got, err = (HomeDocument{Client: closed}).Load(ctx, revision, func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{"slides":[]}`), nil })
	if err != nil || string(got) != `{"slides":[]}` {
		t.Fatal("Redis outage blocked homepage content")
	}
}

func TestHomeCacheCanceledWaiterDoesNotCancelOtherReaders(t *testing.T) {
	client := reviewRedis(t)
	doc := HomeDocument{Client: client}
	revision := security.NewID()
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	release := make(chan struct{})
	load := func(context.Context) (json.RawMessage, error) {
		close(entered)
		<-release
		return json.RawMessage(`{"slides":[]}`), nil
	}
	done := make(chan error, 1)
	go func() { _, err := doc.Load(ctx, revision, load); done <- err }()
	<-entered
	cancel()
	if !errors.Is(<-done, context.Canceled) {
		t.Fatal("canceled reader remained blocked")
	}
	close(release)
	got, err := doc.Load(context.Background(), revision, func(context.Context) (json.RawMessage, error) { return nil, errors.New("must use shared load") })
	if err != nil || string(got) != `{"slides":[]}` {
		t.Fatal("first reader poisoned shared load", err)
	}
	client.Del(context.Background(), "qingye:home:presentation:"+revision)
}

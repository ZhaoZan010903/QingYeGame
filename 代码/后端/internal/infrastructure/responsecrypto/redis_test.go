package responsecrypto

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResponseCryptoRedisConcurrentKeyAndRetention(t *testing.T) {
	endpoint := os.Getenv("TEST_REDIS_URL")
	if endpoint == "" {
		t.Skip("TEST_REDIS_URL required")
	}
	opts, err := redis.ParseURL(endpoint)
	if err != nil {
		t.Fatal("invalid integration Redis configuration")
	}
	opts.PoolSize = 4
	client := redis.NewClient(opts)
	defer client.Close()
	ctx := context.Background()
	store := &RedisStore{Client: client, Namespace: "test:r24:keys:" + RandomID() + ":"}
	t.Cleanup(func() {
		var cursor uint64
		for {
			keys, next, e := client.Scan(ctx, cursor, store.prefix()+"*", 100).Result()
			if e != nil {
				return
			}
			if len(keys) > 0 {
				_ = client.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	identity := strings.Repeat("a", 64)
	keys := make(chan Key, 32)
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			k, e := store.Current(ctx, identity)
			if e != nil {
				errs <- e
			} else {
				keys <- k
			}
		})
	}
	wg.Wait()
	close(keys)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first Key
	for k := range keys {
		if first.ID == "" {
			first = k
		}
		if k != first {
			t.Fatal("concurrent bootstrap created different keys")
		}
	}
	issuedPart, _, _ := strings.Cut(first.ID, "-")
	issued, _ := strconv.ParseInt(issuedPart, 10, 64)
	if first.RotateAt-issued != 3600 || first.RetainUntil-first.RotateAt != 120 {
		t.Fatal("full hour rotation/retention changed")
	}
	if _, err = store.Lookup(ctx, strings.Repeat("b", 64), first.ID); !errors.Is(err, ErrKey) {
		t.Fatal("cross context lookup accepted")
	}
	now, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	previous := first
	previous.ID = strconv.FormatInt(now.Unix()-3630, 10) + "-" + RandomID()
	previous.RotateAt = now.Unix() - 30
	previous.RetainUntil = now.Unix() + 90
	raw, _ := json.Marshal(previous)
	oldKey := store.prefix() + identity + ":keys:" + previous.ID
	if err = client.Set(ctx, oldKey, raw, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if got, e := store.Lookup(ctx, identity, previous.ID); e != nil || got.ID != previous.ID {
		t.Fatal("retained previous key unavailable", e)
	}
	previous.RetainUntil = now.Unix() - 1
	raw, _ = json.Marshal(previous)
	if err = client.Set(ctx, oldKey, raw, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Lookup(ctx, identity, previous.ID); !errors.Is(err, ErrKey) {
		t.Fatal("expired key accepted despite persisted record")
	}
	if err = client.Set(ctx, store.prefix()+"alloc:"+strconv.FormatInt(now.Unix()/3600, 10), 10000, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Current(ctx, strings.Repeat("c", 64)); !errors.Is(err, ErrCapacity) {
		t.Fatal("unbounded context allocation accepted")
	}
	if _, err = store.Current(ctx, identity); err != nil {
		t.Fatal("capacity must not evict established sessions", err)
	}
}

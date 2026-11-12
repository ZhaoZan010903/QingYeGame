//go:build media_integration

package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/domain/media"
	"qingye/backend/internal/domain/member"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/mediaobject"
	"qingye/backend/internal/infrastructure/postgres"
)

// Track only exact keys created by this isolated test; never list/delete a bucket prefix.
type trackedMediaStore struct {
	medialifecycle.ObjectStore
	mu   sync.Mutex
	live map[string]bool
}

func (s *trackedMediaStore) Put(ctx context.Context, key string, r io.Reader, n int64, mime, sha string) error {
	s.mu.Lock()
	s.live[key] = true
	s.mu.Unlock()
	return s.ObjectStore.Put(ctx, key, r, n, mime, sha)
}
func (s *trackedMediaStore) Delete(ctx context.Context, key string) error {
	err := s.ObjectStore.Delete(ctx, key)
	if err == nil {
		s.mu.Lock()
		delete(s.live, key)
		s.mu.Unlock()
	}
	return err
}
func (s *trackedMediaStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for key := range s.live {
		out = append(out, key)
	}
	return out
}

func TestR20RealStorageUploadPublishReturnAndDelete(t *testing.T) {
	if os.Getenv("MEDIA_R20_ENVIRONMENT") != "acceptance" {
		t.Fatal("real storage test requires explicit acceptance environment")
	}
	n, err := strconv.Atoi(os.Getenv("R20_TEST_IMAGES"))
	if err != nil || (n != 3 && n != 300) {
		t.Fatal("R20_TEST_IMAGES must be 3 or 300")
	}
	f := newChatFixture(t)
	a := f.a
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	config := func(prefix string) mediaobject.Config {
		return mediaobject.Config{Environment: "acceptance", Endpoint: os.Getenv(prefix + "_ENDPOINT"), Bucket: os.Getenv(prefix + "_BUCKET"), AccessKeyID: os.Getenv(prefix + "_ACCESS_KEY_ID"), AccessKeySecret: os.Getenv(prefix + "_ACCESS_KEY_SECRET"), TempDir: t.TempDir()}
	}
	stageRaw, err := mediaobject.NewS3(config("MEDIA_STAGE"))
	if err != nil {
		t.Fatal("invalid isolated staging configuration")
	}
	ossRaw, err := mediaobject.NewOSS(config("MEDIA_OSS"))
	if err != nil {
		t.Fatal("invalid isolated OSS configuration")
	}
	stage := &trackedMediaStore{ObjectStore: stageRaw, live: map[string]bool{}}
	oss := &trackedMediaStore{ObjectStore: ossRaw, live: map[string]bool{}}
	defer func() {
		clean, c := context.WithTimeout(context.Background(), 20*time.Minute)
		defer c()
		for _, store := range []*trackedMediaStore{stage, oss} {
			for _, key := range store.keys() {
				if err := store.Delete(clean, key); err != nil {
					t.Error("isolated exact-object cleanup failed")
				}
			}
		}
	}()
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	files := &mediaobject.Files{Local: &mediafiles.Store{Root: a.Config.MediaDir}, Staging: stage, Published: oss, Repository: repo, Environment: "acceptance", TempDir: t.TempDir(), NewID: newID}
	a.Config.MediaR20.Enabled = true
	a.MediaFiles = files
	f.admin.Config.MediaR20.Enabled = true
	f.admin.MediaFiles = files
	if _, err = a.DB.Exec(ctx, `INSERT INTO product_dictionary_options(dictionary_key,value,label) VALUES('server','storage-test','存储验收区')`); err != nil {
		t.Fatal(err)
	}
	actor := member.Actor{ID: f.member, Role: "member"}
	admin := management.Actor{ID: f.first, Role: "admin"}
	draft, err := a.memberService().Create(ctx, actor, catalog.ProductInput{CategoryID: "chat-test", Title: "R20存储隔离验收", Description: "合成小图批量上传测试", TradeType: "sale", PriceUnit: "account", RequestID: newID(), Attributes: map[string]any{"server": "storage-test", "gender": "female", "level": "", "kind": "", "custom_tags": []string{"验收"}}})
	if err != nil {
		t.Fatal(err)
	}
	product := draft["data"].(map[string]any)["id"].(string)
	version := func() int {
		var v int
		if err := a.DB.QueryRow(ctx, `SELECT version FROM products WHERE id=$1`, product).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	data := r20SmallPNG(t)
	ids := make([]string, n)
	api := a.mediaService()
	uploadActor := media.Actor{ID: f.member, Role: "member", Surface: "public"}
	started := time.Now()
	for batch := 0; batch < n; batch += 99 {
		end := min(batch+99, n)
		jobs := make(chan int, end-batch)
		failures := make(chan error, end-batch)
		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for index := range jobs {
					result, e := api.Create(ctx, uploadActor, media.CreateInput{Filename: fmt.Sprintf("test-%03d.png", index), MIME: "image/png", Size: int64(len(data)), Purpose: "product", Resumable: true})
					if e != nil {
						failures <- e
						continue
					}
					ticket := result["data"].(map[string]any)
					id := ticket["media_id"].(string)
					ids[index] = id
					token := strings.SplitN(ticket["upload_url"].(string), "token=", 2)[1]
					e = api.ReceiveRange(ctx, id, token, fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data)), int64(len(data)), bytes.NewReader(data))
					if e == nil {
						_, e = api.Complete(ctx, uploadActor, id)
					}
					if e != nil {
						failures <- e
					}
				}
			}()
		}
		for i := batch; i < end; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(failures)
		for e := range failures {
			t.Fatal("isolated upload failed", e)
		}
		// The association API deliberately uses smaller transactions than browser selection batches.
		for first := batch; first < end; first += 20 {
			last := min(first+20, end)
			if _, err = a.memberService().AppendImages(ctx, actor, product, member.AppendImagesInput{MediaIDs: ids[first:last], ExpectedVersion: version(), RequestID: newID()}); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("uploaded batch %d-%d of %d (1–99 per selection)", batch+1, end, n)
	}
	// Reorder after transfer and verify the persisted selection order, not completion order.
	first := ids[0]
	if _, err = a.memberService().ChangeImage(ctx, actor, product, ids[n-1], member.ChangeImageInput{ExpectedVersion: version(), BeforeMediaID: &first}, false); err != nil {
		t.Fatal(err)
	}
	rows, err := a.DB.Query(ctx, `SELECT media_id FROM product_images WHERE product_id=$1 ORDER BY sort_order,media_id`, product)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		order = append(order, id)
	}
	rows.Close()
	expected := append([]string{ids[n-1]}, ids[:n-1]...)
	if fmt.Sprint(order) != fmt.Sprint(expected) {
		t.Fatal("persisted gallery order differs")
	}
	if _, err = a.memberService().Transition(ctx, actor, product, member.TransitionInput{ExpectedVersion: version()}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.admin.managementService().TransitionProduct(ctx, admin, product, "review", management.TransitionInput{ExpectedVersion: version(), Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	status := func(want string) {
		var got string
		if err := a.DB.QueryRow(ctx, `SELECT status FROM products WHERE id=$1`, product).Scan(&got); err != nil || got != want {
			t.Fatalf("product status %s want %s", got, want)
		}
	}
	status("publishing")
	worker := medialifecycle.Worker{Repository: repo, Staging: stage, Published: oss}
	drain := func() {
		for i := 0; i < n*15+50; i++ {
			worked, e := worker.RunOnce(ctx)
			if e != nil {
				t.Fatal("isolated lifecycle step failed", e)
			}
			if !worked {
				return
			}
		}
		t.Fatal("lifecycle did not drain")
	}
	drain()
	status("published")
	if len(stage.keys()) != 0 || len(oss.keys()) != n {
		t.Fatalf("unexpected copies stage=%d oss=%d", len(stage.keys()), len(oss.keys()))
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for _, variant := range []string{"file", "preview"} {
		w := surfaceRequest(t, f.pub, "GET", "/api/v1/media/"+ids[0]+"/"+variant, "", nil)
		statusIs(t, w, 302)
		url := w.Header().Get("Location")
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if e != nil {
			t.Fatal("invalid signed image URL")
		}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal("signed image fetch failed")
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if e != nil || response.StatusCode != 200 || len(raw) == 0 {
			t.Fatal("signed image response invalid")
		}
	}
	if _, err = f.admin.managementService().TransitionProduct(ctx, admin, product, "status", management.TransitionInput{ExpectedVersion: version(), Action: "offline", Reason: "isolated retention test"}); err != nil {
		t.Fatal(err)
	}
	statusIs(t, surfaceRequest(t, f.pub, "GET", "/api/v1/media/"+ids[0]+"/file", "", nil), 404)
	var grace bool
	if err = a.DB.QueryRow(ctx, `SELECT bool_and(retain_until>now()+interval '4 days 23 hours') FROM media_objects WHERE product_id=$1`, product).Scan(&grace); err != nil || !grace {
		t.Fatal("OSS grace missing", err)
	}
	if worked, e := worker.RunOnce(ctx); e != nil || worked {
		t.Fatal("early OSS return")
	}
	if _, err = a.DB.Exec(ctx, `UPDATE media_objects SET retain_until=now()-interval '1 second' WHERE product_id=$1`, product); err != nil {
		t.Fatal(err)
	}
	drain()
	if len(stage.keys()) != n || len(oss.keys()) != 0 {
		t.Fatal("return did not leave exactly staging copies")
	}
	if err = a.DB.QueryRow(ctx, `SELECT bool_and(retain_until>now()+interval '4 days 23 hours') FROM media_objects WHERE product_id=$1`, product).Scan(&grace); err != nil || !grace {
		t.Fatal("ECS grace not measured from completed move", err)
	}
	if _, err = a.DB.Exec(ctx, `UPDATE media_objects SET retain_until=now()-interval '1 second' WHERE product_id=$1`, product); err != nil {
		t.Fatal(err)
	}
	drain()
	if len(stage.keys()) != 0 || len(oss.keys()) != 0 {
		t.Fatal("expired images retained")
	}
	t.Logf("real S3/OSS lifecycle passed: %d images, fixture=%d bytes, elapsed=%s", n, len(data), time.Since(started))
}

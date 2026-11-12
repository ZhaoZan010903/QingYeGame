package media

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/media"
)

type securityWindowCounter struct {
	counts map[string]int
	limits map[string]int
}

func (l *securityWindowCounter) Allow(_ context.Context, key string, max int, period time.Duration) error {
	if period != 10*time.Minute {
		return fmt.Errorf("unexpected rate window: %v", period)
	}
	l.counts[key]++
	l.limits[key] = max
	if l.counts[key] > max {
		return fault.New(fault.RateLimited, "RATE_LIMITED", "rate window exhausted")
	}
	return nil
}

func TestR20SecurityChunkLimitCoversConfiguredSizesWithoutOverflow(t *testing.T) {
	for _, tc := range []struct {
		size int64
		want int
	}{{1, 1200}, {ChunkSize, 1200}, {ChunkSize + 1, 2400}, {10 * ChunkSize, 12000}, {64 * ChunkSize, 76800}} {
		if got := productChunkLimit(tc.size); got != tc.want {
			t.Fatalf("size=%d limit=%d want=%d", tc.size, got, tc.want)
		}
	}
	if got := productChunkLimit(math.MaxInt64); got <= 0 {
		t.Fatal("overflowed chunk allowance", got)
	}
}

func TestR20SecurityTwoThreeHundredFileWindowsAllowChunkRetries(t *testing.T) {
	s, repo, _ := resumeFixture(t)
	s.max = 10 * ChunkSize
	repo.upload.Size = s.max
	limiter := &securityWindowCounter{counts: map[string]int{}, limits: map[string]int{}}
	s.limiter = limiter
	ctx := context.Background()
	token := strings.Repeat("a", 64)
	// Fake chunk storage exercises only validation/accounting; no large payload is created or sent.
	for file := 0; file < 600; file++ {
		repo.upload.Offset, repo.upload.State = 0, "pending"
		for part := int64(0); part < 10; part++ {
			header := fmt.Sprintf("bytes %d-%d/%d", part*ChunkSize, (part+1)*ChunkSize-1, s.max)
			for attempt := 0; attempt < 2; attempt++ {
				if err := s.ReceiveRange(ctx, "id", token, header, ChunkSize, strings.NewReader("")); err != nil {
					t.Fatalf("normal file=%d chunk=%d attempt=%d: %v", file, part, attempt, err)
				}
			}
		}
	}
	key := "upload-chunk:public:member"
	if limiter.counts[key] != 12000 || limiter.limits[key] != 12000 {
		t.Fatal("accounting mismatch", limiter.counts, limiter.limits)
	}
	err := s.ReceiveRange(ctx, "id", token, fmt.Sprintf("bytes 0-%d/%d", ChunkSize-1, s.max), ChunkSize, strings.NewReader(""))
	code(t, err, "RATE_LIMITED")
}

type securityCreateRepo struct {
	*repoStub
	created int
}

func (r *securityCreateRepo) Create(context.Context, domain.Actor, domain.CreateInput, string, string, string) error {
	r.created++
	return nil
}

func TestR20SecurityTwoThreeHundredCreateWindowsRemainBounded(t *testing.T) {
	s, original, _ := fixture(t)
	repo := &securityCreateRepo{repoStub: original}
	s.repo = repo
	limiter := &securityWindowCounter{counts: map[string]int{}, limits: map[string]int{}}
	s.limiter = limiter
	input := domain.CreateInput{Filename: "synthetic.png", MIME: "image/png", Size: 10, Purpose: "product", Resumable: true}
	for i := 0; i < 600; i++ {
		if _, err := s.Create(context.Background(), actor(), input); err != nil {
			t.Fatalf("legitimate create %d: %v", i, err)
		}
	}
	_, err := s.Create(context.Background(), actor(), input)
	code(t, err, "RATE_LIMITED")
	if repo.created != 600 {
		t.Fatal("denied create reached repository", repo.created)
	}
}

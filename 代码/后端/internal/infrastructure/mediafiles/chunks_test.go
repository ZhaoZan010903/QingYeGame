package mediafiles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkCommitReplayAndRollbackTail(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	ctx := context.Background()
	first, err := s.ReceiveChunk(ctx, "uploads/test.raw", 0, 4, strings.NewReader("ABCD"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Discard()
	if err = first.Install(0); err != nil {
		t.Fatal(err)
	}
	if err = first.Install(4); err != nil {
		t.Fatal("matching retry", err)
	}
	changed, err := s.ReceiveChunk(ctx, "uploads/test.raw", 0, 4, strings.NewReader("AAAA"))
	if err != nil {
		t.Fatal(err)
	}
	defer changed.Discard()
	if err = changed.Install(4); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	// Simulate a file write that outlived a rolled-back DB transaction.
	if err = os.WriteFile(filepath.Join(s.Root, "uploads", "test.raw"), []byte("ABCDorphan"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := s.ReceiveChunk(ctx, "uploads/test.raw", 4, 2, strings.NewReader("EF"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Discard()
	if err = second.Install(4); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Root, "uploads", "test.raw"))
	if err != nil || string(raw) != "ABCDEF" {
		t.Fatalf("wrong resumed bytes %q %v", raw, err)
	}
}
func TestChunkRejectsPathsAndPartialBodies(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	for _, key := range []string{"../a", "uploads/../escape.raw", "uploads/sub/id.raw", "legacy/id.raw", "uploads/id.raw/"} {
		if _, err := s.ReceiveChunk(context.Background(), key, 0, 2, strings.NewReader("ok")); err == nil {
			t.Fatalf("accepted path %s", key)
		}
	}
	for _, body := range []string{"x", "toolong"} {
		if _, err := s.ReceiveChunk(context.Background(), "uploads/id.raw", 0, 2, strings.NewReader(body)); err == nil {
			t.Fatal("accepted partial body")
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.Root, "uploads"))
	if len(entries) != 0 {
		t.Fatal("incomplete temp files leaked")
	}
}

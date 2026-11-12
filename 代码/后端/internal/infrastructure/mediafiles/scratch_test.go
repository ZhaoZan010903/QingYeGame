package mediafiles

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScratchReaperPreservesRawAndBusinessImages(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-6 * 24 * time.Hour)
	files := []string{"uploads/.chunk-0123456789abcdef0123456789abcdef", "uploads/.incoming-1234", "images/.processed-456", "derived/.thumb-789", "uploads/media-id.raw", "images/media-id.png", "derived/saved.jpg"}
	for _, key := range files {
		p := filepath.Join(root, filepath.FromSlash(key))
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, make([]byte, 1024), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Chtimes(p, old, old); e != nil {
			t.Fatal(e)
		}
	}
	recent := filepath.Join(root, "uploads/.incoming-999")
	if e := os.WriteFile(recent, make([]byte, 1024), 0600); e != nil {
		t.Fatal(e)
	}
	count, err := (&Store{Root: root}).CleanupScratch(now)
	if err != nil || count != 4 {
		t.Fatal(count, err)
	}
	for _, key := range append(files[4:], "uploads/.incoming-999") {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(key))); err != nil {
			t.Fatal("removed business/recent file", key)
		}
	}
}

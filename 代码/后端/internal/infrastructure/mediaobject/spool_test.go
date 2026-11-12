package mediaobject

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSpoolCleanupOnlyExpiredGeneratedNames(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-6 * 24 * time.Hour)
	for _, name := range []string{"object-123", "private-preview-123", "process-123", "object-recent", "legacy.jpg", "uploads"} {
		p := filepath.Join(root, name)
		if name == "process-123" {
			if e := os.Mkdir(p, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(p, "image.png"), make([]byte, 2048), 0600); e != nil {
				t.Fatal(e)
			}
		} else {
			if e := os.WriteFile(p, make([]byte, 1024), 0600); e != nil {
				t.Fatal(e)
			}
		}
		if e := os.Chtimes(p, old, old); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(root, "object-999"), make([]byte, 1024), 0600); e != nil {
		t.Fatal(e)
	}
	n, e := CleanupSpool(root, now)
	if e != nil || n != 3 {
		t.Fatalf("removed=%d error=%v", n, e)
	}
	for _, name := range []string{"object-recent", "legacy.jpg", "uploads", "object-999"} {
		if _, e := os.Stat(filepath.Join(root, name)); e != nil {
			t.Fatal("unrelated/recent scratch removed", name)
		}
	}
	n, e = CleanupSpool(root, now)
	if e != nil || n != 0 {
		t.Fatal("cleanup not idempotent", n, e)
	}
}

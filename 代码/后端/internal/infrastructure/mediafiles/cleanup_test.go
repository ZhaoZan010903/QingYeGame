package mediafiles

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestManagedCleanupCannotRemoveLegacyOrEscapingPaths(t *testing.T) {
	root := t.TempDir()
	s := Store{Root: root}
	for _, key := range []string{"legacy/original", "../outside", "images/another.jpg"} {
		if err := s.RemoveManaged("item", key); err == nil {
			t.Fatalf("unmanaged path accepted: %s", key)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "images", "item.jpg")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveManaged("item", "images/item.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("managed file not removed")
	}
	if err := s.RemoveManaged("item", "images/item.jpg"); err != nil {
		t.Fatal("retry not idempotent")
	}
}
func TestManagedCleanupRejectsSymlinkEvenWithinRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "images", "protected.jpg")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "images", "link.jpg")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows symlink permission unavailable; covered on Linux")
		}
		t.Fatal(err)
	}
	if err := (&Store{Root: root}).RemoveManaged("link", "images/link.jpg"); err == nil {
		t.Fatal("symlink cleanup accepted")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("shared target removed")
	}
}

func TestManagedCleanupProtectsLegacyAndOutsideSymlinkTargets(t *testing.T) {
	for _, kind := range []string{"legacy", "outside", "derived"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			for _, dir := range []string{"images", "legacy", "derived"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(root, "legacy", "original.jpg")
			if kind == "outside" {
				target = filepath.Join(outside, "original.jpg")
			}
			if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "images", "item.jpg")
			if kind == "derived" {
				if err := os.WriteFile(link, []byte("processed"), 0600); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(link)
				if err != nil {
					t.Fatal(err)
				}
				key := fmt.Sprintf("%s:%s:%d:%d:v1", "item", link, info.Size(), info.ModTime().UnixNano())
				link = filepath.Join(root, "derived", hash(key)+".jpg")
			}
			if err := os.Symlink(target, link); err != nil {
				if runtime.GOOS == "windows" {
					t.Skip("Windows symlink unavailable; Linux CI must cover")
				}
				t.Fatal(err)
			}
			if err := (&Store{Root: root}).RemoveManaged("item", "images/item.jpg"); err == nil {
				t.Fatal("symlink accepted")
			}
			raw, err := os.ReadFile(target)
			if err != nil || string(raw) != "original" {
				t.Fatal("protected original changed")
			}
		})
	}
}

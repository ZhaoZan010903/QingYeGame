package mediafiles

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentLocalOriginalAndPreviewCarryTrustedSource(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "images", "fixture.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	store := &Store{Root: root}
	for _, preview := range []bool{false, true} {
		read, err := store.Open(context.Background(), "images/fixture.png", "fixture", preview)
		if err != nil {
			t.Fatal(err)
		}
		if !read.ServerLocal || read.Reader == nil || read.RedirectURL != "" {
			t.Fatal("persistent local image lost source classification")
		}
		if err = read.Reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

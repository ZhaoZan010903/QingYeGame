package mediafiles

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func permanentTestWrite(t *testing.T, root, key, contents string) string {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func permanentTestImage(t *testing.T, root, key string) string {
	t.Helper()
	name := permanentTestWrite(t, root, key, "")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 83, G: 137, B: 191, A: 255})
	encodeErr := png.Encode(file, img)
	closeErr := file.Close()
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return name
}

func permanentTestPreserved(t *testing.T, name, contents string) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil || string(raw) != contents {
		t.Fatalf("unrelated file was changed: %s: %v", name, err)
	}
}

func permanentTestAbsent(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact cleanup target still exists or could not be checked: %s: %v", name, err)
	}
}

func permanentTestSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows symlink permission unavailable; Linux validation must cover this case")
		}
		t.Fatal(err)
	}
}

func TestPermanentCleanupMatchesThumbnailAndPreservesOtherFiles(t *testing.T) {
	for _, key := range []string{
		"legacy/" + strings.Repeat("a", 64),
		"images/item.png",
		"images/item-0123456789abcdef.webp",
	} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			store := Store{Root: root}
			original := permanentTestImage(t, root, key)
			source, err := ReadPath(root, key)
			if err != nil {
				t.Fatal(err)
			}
			thumbnail, ok := store.Thumbnail(context.Background(), source, "item")
			if !ok {
				t.Fatal("real thumbnail generation failed")
			}
			derived, err := store.PlanPermanent(context.Background(), "item", key)
			if err != nil || derived == "" {
				t.Fatalf("could not plan exact derived key: %q %v", derived, err)
			}
			planned, err := Path(root, derived)
			if err != nil || !sameManagedPath(planned, thumbnail) {
				t.Fatalf("plan does not identify the thumbnail generated from this original: %q %q %v", planned, thumbnail, err)
			}
			preserved := []string{
				permanentTestWrite(t, root, "images/item.jpg", "same id different image"),
				permanentTestWrite(t, root, "images/item-ffffffffffffffff.png", "another version"),
				permanentTestWrite(t, root, "images/other.png", "another product"),
				permanentTestWrite(t, root, "derived/"+strings.Repeat("f", 64)+".jpg", "another thumbnail"),
				permanentTestWrite(t, root, "legacy/"+strings.Repeat("b", 64), "another imported original"),
			}
			contents := []string{"same id different image", "another version", "another product", "another thumbnail", "another imported original"}
			if err = store.RemovePermanent(context.Background(), "item", key, derived); err != nil {
				t.Fatal(err)
			}
			permanentTestAbsent(t, original)
			permanentTestAbsent(t, thumbnail)
			for i, name := range preserved {
				permanentTestPreserved(t, name, contents[i])
			}
			if err = store.RemovePermanent(context.Background(), "item", key, derived); err != nil {
				t.Fatalf("successful deletion retry is not idempotent: %v", err)
			}
			for i, name := range preserved {
				permanentTestPreserved(t, name, contents[i])
			}
		})
	}
}

func TestPermanentCleanupRetriesPersistedPlanAfterEitherFileIsGone(t *testing.T) {
	for _, missing := range []string{"original", "derived"} {
		t.Run(missing, func(t *testing.T) {
			root := t.TempDir()
			store := Store{Root: root}
			key := "images/item.png"
			original := permanentTestWrite(t, root, key, "original")
			derived, err := store.PlanPermanent(context.Background(), "item", key)
			if err != nil {
				t.Fatal(err)
			}
			thumbnail := permanentTestWrite(t, root, derived, "derived")
			first := original
			if missing == "derived" {
				first = thumbnail
			}
			if err = os.Remove(first); err != nil {
				t.Fatal(err)
			}
			if err = store.RemovePermanent(context.Background(), "item", key, derived); err != nil {
				t.Fatalf("persisted exact plan did not recover a partial deletion: %v", err)
			}
			permanentTestAbsent(t, original)
			permanentTestAbsent(t, thumbnail)
			if err = store.RemovePermanent(context.Background(), "item", key, derived); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPermanentCleanupMissingOriginalCannotBePlannedAsSuccess(t *testing.T) {
	for _, key := range []string{"images/item.jpg", "legacy/" + strings.Repeat("a", 64)} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			unrelated := permanentTestWrite(t, root, "derived/"+strings.Repeat("f", 64)+".jpg", "retain unknown identity")
			derived, err := (&Store{Root: root}).PlanPermanent(context.Background(), "item", key)
			if err == nil || derived != "" {
				t.Fatalf("missing original was reported as a valid cleanup plan: %q %v", derived, err)
			}
			permanentTestPreserved(t, unrelated, "retain unknown identity")
		})
	}
}

func TestPermanentCleanupRejectsTraversalAndUnrelatedKeysBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	original := permanentTestWrite(t, root, "images/item.jpg", "keep original")
	protected := permanentTestWrite(t, root, "images/other.jpg", "keep other")
	derivedKey := "derived/" + strings.Repeat("c", 64) + ".jpg"
	derived := permanentTestWrite(t, root, derivedKey, "keep derived")
	for _, entry := range []struct{ id, key, derived string }{
		{"../item", "images/item.jpg", derivedKey},
		{"item", "../images/item.jpg", derivedKey},
		{"item", "images/../images/item.jpg", derivedKey},
		{"item", "images\\item.jpg", derivedKey},
		{"item", "/images/item.jpg", derivedKey},
		{"item", "images/other.jpg", derivedKey},
		{"item", "legacy/not-a-digest", derivedKey},
		{"item", "uploads/other.raw", derivedKey},
		{"item", "images/item.jpg", "../outside.jpg"},
		{"item", "images/item.jpg", "images/other.jpg"},
		{"item", "images/item.jpg", "derived/not-a-digest.jpg"},
	} {
		if err := store.RemovePermanent(context.Background(), entry.id, entry.key, entry.derived); err == nil {
			t.Fatalf("unsafe removal accepted: %#v", entry)
		}
		if !permanentKey(entry.id, entry.key) {
			if _, err := store.PlanPermanent(context.Background(), entry.id, entry.key); err == nil {
				t.Fatalf("unsafe planning accepted: %#v", entry)
			}
		}
		permanentTestPreserved(t, original, "keep original")
		permanentTestPreserved(t, protected, "keep other")
		permanentTestPreserved(t, derived, "keep derived")
	}
}

func TestPermanentCleanupRejectsOriginalAndDerivedSymlinks(t *testing.T) {
	for _, kind := range []string{"original-inside", "original-outside", "derived-inside", "derived-outside", "original-parent"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			store := Store{Root: root}
			key := "images/item.jpg"
			protectedRoot := root
			if strings.HasSuffix(kind, "outside") {
				protectedRoot = t.TempDir()
			}
			target := permanentTestWrite(t, protectedRoot, "protected/item.jpg", "never remove target")
			link := filepath.Join(root, "images", "item.jpg")
			var derived string
			if strings.HasPrefix(kind, "derived") {
				permanentTestWrite(t, root, key, "retain source on rejected derivative")
				var err error
				derived, err = store.PlanPermanent(context.Background(), "item", key)
				if err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(root, filepath.FromSlash(derived))
			}
			if kind == "original-parent" {
				permanentTestSymlink(t, filepath.Dir(target), filepath.Join(root, "images"))
			} else {
				permanentTestSymlink(t, target, link)
			}
			if strings.HasPrefix(kind, "original") {
				if _, err := store.PlanPermanent(context.Background(), "item", key); err == nil {
					t.Fatal("symbolic original or parent was accepted during planning")
				}
			}
			if err := store.RemovePermanent(context.Background(), "item", key, derived); err == nil {
				t.Fatal("symbolic original, derivative or parent was accepted during removal")
			}
			permanentTestPreserved(t, target, "never remove target")
			if strings.HasPrefix(kind, "derived") {
				permanentTestPreserved(t, filepath.Join(root, filepath.FromSlash(key)), "retain source on rejected derivative")
			}
		})
	}
}

func TestPermanentCleanupRawUploadAndCancelledContext(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	raw := permanentTestWrite(t, root, "uploads/item.raw", "raw upload")
	other := permanentTestWrite(t, root, "uploads/other.raw", "other upload")
	derived, err := store.PlanPermanent(context.Background(), "item", "uploads/item.raw")
	if err != nil || derived != "" {
		t.Fatalf("raw upload must not invent a thumbnail key: %q %v", derived, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = store.PlanPermanent(ctx, "item", "uploads/item.raw"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled plan did not stop: %v", err)
	}
	if err = store.RemovePermanent(ctx, "item", "uploads/item.raw", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled deletion did not stop: %v", err)
	}
	permanentTestPreserved(t, raw, "raw upload")
	if err = store.RemovePermanent(context.Background(), "item", "uploads/item.raw", ""); err != nil {
		t.Fatal(err)
	}
	permanentTestAbsent(t, raw)
	permanentTestPreserved(t, other, "other upload")
	if err = store.RemovePermanent(context.Background(), "item", "uploads/item.raw", ""); err != nil {
		t.Fatal("raw upload retry was not idempotent", err)
	}
}

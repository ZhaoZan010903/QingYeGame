package mediafiles

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/media"
)

func requireStorageFault(t *testing.T, err error, code string) {
	t.Helper()
	f, ok := fault.As(err)
	if !ok || f.Kind != fault.Unavailable || f.Code != code {
		t.Fatalf("expected unavailable/%s, got %v", code, err)
	}
}

func TestDiskCapacityBoundariesAndOverflow(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name               string
		available          uint64
		reserve, requested int64
		allowed            bool
	}{
		{"exact headroom", 200, 100, 100, true},
		{"one byte short", 199, 100, 100, false},
		{"reserve already breached", 99, 100, 0, false},
		{"unsigned sum beyond int64", math.MaxUint64 - 1, math.MaxInt64, math.MaxInt64, true},
		{"large request cannot overflow to success", math.MaxInt64, math.MaxInt64, math.MaxInt64, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAvailable(root, tc.reserve, tc.requested, func(string) (uint64, error) { return tc.available, nil })
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireStorageFault(t, err, "STORAGE_CAPACITY_LOW")
			}
		})
	}
}

func TestDiskCapacityDisabledDoesNotInspectFilesystem(t *testing.T) {
	for _, reserve := range []int64{0, -1} {
		if err := checkAvailable("", reserve, -1, func(string) (uint64, error) {
			t.Fatal("disabled guard probed disk")
			return 0, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiskCapacityUsesExistingParentWithoutCreatingDirectories(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "not-created", "images")
	err := checkAvailable(target, 10, 10, func(path string) (uint64, error) {
		if path != root {
			t.Fatalf("probed %q instead of existing parent %q", path, root)
		}
		return 20, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Fatalf("capacity probe created directories: %v", err)
	}
}

func TestDiskCapacityProbeFailuresAreClosedAndRedacted(t *testing.T) {
	root := t.TempDir()
	secret := "private-storage-path"
	err := checkAvailable(root, 1, 1, func(string) (uint64, error) { return 0, errors.New(secret) })
	requireStorageFault(t, err, "STORAGE_UNAVAILABLE")
	if strings.Contains(err.Error(), secret) {
		t.Fatal("private probe error escaped into public message")
	}
	requireStorageFault(t, CheckAvailable(root, 1, -1), "STORAGE_UNAVAILABLE")
	requireStorageFault(t, CheckAvailable("", 1, 1), "STORAGE_UNAVAILABLE")
	file := filepath.Join(root, "not-a-directory")
	if err = os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	requireStorageFault(t, CheckAvailable(file, 1, 1), "STORAGE_UNAVAILABLE")
}

func TestDiskCapacityRejectsSymlinkAncestors(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows symlink permission unavailable; Linux tests cover this")
		}
		t.Fatal(err)
	}
	for _, target := range []string{link, filepath.Join(link, "new", "images")} {
		err := checkAvailable(target, 1, 1, func(string) (uint64, error) {
			t.Fatal("symlink path reached disk probe")
			return 2, nil
		})
		requireStorageFault(t, err, "STORAGE_UNAVAILABLE")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside path changed: %v", err)
	}
}

func TestDiskCapacityUsesNativeFilesystemAvailableBytes(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("native capacity guard supports Windows and Linux")
	}
	root := t.TempDir()
	if err := CheckAvailable(root, 1, 1); err != nil {
		t.Fatal(err)
	}
	requireStorageFault(t, CheckAvailable(root, math.MaxInt64, 1), "STORAGE_CAPACITY_LOW")
}

type unreadBody struct{ t *testing.T }

func (r unreadBody) Read([]byte) (int, error) {
	r.t.Fatal("rejected upload body was read")
	return 0, io.EOF
}

func TestLowDiskRejectsWholeAndChunkBeforeReceiving(t *testing.T) {
	for _, kind := range []string{"whole", "chunk"} {
		t.Run(kind, func(t *testing.T) {
			s := &Store{Root: t.TempDir(), MinFreeBytes: math.MaxInt64}
			var err error
			if kind == "whole" {
				_, err = s.Receive(context.Background(), "uploads/item.raw", 4, unreadBody{t})
			} else {
				_, err = s.ReceiveChunk(context.Background(), "uploads/item.raw", 0, 4, unreadBody{t})
			}
			requireStorageFault(t, err, "STORAGE_CAPACITY_LOW")
			entries, err := os.ReadDir(s.Root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected upload left files/directories: %v", err)
			}
		})
	}
}

func TestLowDiskChunkCommitProtectsNewFilesAndRollbackTail(t *testing.T) {
	for _, existing := range []bool{false, true} {
		s := &Store{Root: t.TempDir()}
		chunk, err := s.ReceiveChunk(context.Background(), "uploads/item.raw", 0, 4, strings.NewReader("ABCD"))
		if err != nil {
			t.Fatal(err)
		}
		defer chunk.Discard()
		path := filepath.Join(s.Root, "uploads", "item.raw")
		if existing {
			if err = os.WriteFile(path, []byte("uncommitted tail"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		chunk.(*incomingChunk).minFreeBytes = math.MaxInt64
		requireStorageFault(t, chunk.Install(0), "STORAGE_CAPACITY_LOW")
		raw, err := os.ReadFile(path)
		if existing && (err != nil || string(raw) != "uncommitted tail") {
			t.Fatalf("rejected append altered rollback tail: %q %v", raw, err)
		}
		if !existing && !os.IsNotExist(err) {
			t.Fatalf("rejected append created destination: %v", err)
		}
	}
}

func TestLowDiskAllowsCommittedRetryAndCleanup(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	chunk, err := s.ReceiveChunk(context.Background(), "uploads/item.raw", 0, 4, strings.NewReader("ABCD"))
	if err != nil {
		t.Fatal(err)
	}
	defer chunk.Discard()
	if err = chunk.Install(0); err != nil {
		t.Fatal(err)
	}
	chunk.(*incomingChunk).minFreeBytes = math.MaxInt64
	s.MinFreeBytes = math.MaxInt64
	if err = chunk.Install(4); err != nil {
		t.Fatalf("low disk blocked committed retry: %v", err)
	}
	s.RemoveRaw("uploads/item.raw")
	if _, err = os.Stat(filepath.Join(s.Root, "uploads", "item.raw")); !os.IsNotExist(err) {
		t.Fatalf("low disk blocked raw cleanup: %v", err)
	}
}

func TestLowDiskBlocksSanitizingButAllowsImageReadAndDelete(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	key := "images/item.png"
	if err := os.WriteFile(filepath.Join(s.Root, filepath.FromSlash(key)), imageBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	s.MinFreeBytes = math.MaxInt64
	output := filepath.Join(s.Root, "processing")
	_, err := s.SanitizeTo(context.Background(), media.Upload{ID: "item", Key: key, MIME: "image/png"}, output)
	requireStorageFault(t, err, "STORAGE_CAPACITY_LOW")
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("rejected sanitizer created output: %v", err)
	}
	f, err := s.Open(context.Background(), key, "item", false)
	if err != nil {
		t.Fatalf("low disk blocked image read: %v", err)
	}
	raw, err := io.ReadAll(f.Reader)
	f.Reader.Close()
	if err != nil || !bytes.Equal(raw, imageBytes.Bytes()) {
		t.Fatalf("image read changed: %v", err)
	}
	if err = s.RemoveManaged("item", key); err != nil {
		t.Fatalf("low disk blocked managed cleanup: %v", err)
	}
	if _, err = os.Stat(filepath.Join(s.Root, filepath.FromSlash(key))); !os.IsNotExist(err) {
		t.Fatalf("managed cleanup did not remove image: %v", err)
	}
}

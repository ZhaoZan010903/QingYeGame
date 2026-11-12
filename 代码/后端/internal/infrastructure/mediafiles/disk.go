package mediafiles

import (
	"errors"
	"os"
	"path/filepath"

	"qingye/backend/internal/domain/fault"
)

// CheckAvailable preserves host disk headroom, separately from account quotas.
// This is an admission check, not a cross-process reservation; callers must also bound concurrency.
// A non-positive reserve preserves the non-R20 behavior without probing the filesystem.
func CheckAvailable(path string, reserve, requiredBytes int64) error {
	return checkAvailable(path, reserve, requiredBytes, availableBytes)
}

func checkAvailable(path string, reserve, requiredBytes int64, probe func(string) (uint64, error)) error {
	if reserve <= 0 {
		return nil
	}
	if requiredBytes < 0 {
		return fault.New(fault.Unavailable, "STORAGE_UNAVAILABLE", "图片存储暂不可用，请稍后重试")
	}
	dir, err := existingDirectory(path)
	if err != nil {
		return fault.Wrap(fault.Unavailable, "STORAGE_UNAVAILABLE", "图片存储暂不可用，请稍后重试", err)
	}
	available, err := probe(dir)
	if err != nil {
		return fault.Wrap(fault.Unavailable, "STORAGE_UNAVAILABLE", "图片存储暂不可用，请稍后重试", err)
	}
	// Subtraction avoids overflow when both configured reserve and requested size are large.
	if available < uint64(reserve) || available-uint64(reserve) < uint64(requiredBytes) {
		return fault.New(fault.Unavailable, "STORAGE_CAPACITY_LOW", "图片存储暂不可用，请稍后重试")
	}
	return nil
}

// Probe the nearest existing directory without creating the target or following symlink ancestors.
func existingDirectory(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty capacity path")
	}
	current, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	nearest := ""
	for {
		info, err := os.Lstat(current)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		} else {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", errors.New("capacity path must contain only directories")
			}
			if nearest == "" {
				nearest = current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	if nearest == "" {
		return "", errors.New("capacity directory unavailable")
	}
	return nearest, nil
}

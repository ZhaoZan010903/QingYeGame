//go:build linux

package mediafiles

import (
	"errors"
	"math"

	"golang.org/x/sys/unix"
)

func availableBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	if stat.Bsize <= 0 {
		return 0, errors.New("invalid filesystem block size")
	}
	// Bavail excludes blocks reserved for the system, even when the process runs as root.
	blockSize := uint64(stat.Bsize)
	if stat.Bavail > math.MaxUint64/blockSize {
		return math.MaxUint64, nil
	}
	return stat.Bavail * blockSize, nil
}

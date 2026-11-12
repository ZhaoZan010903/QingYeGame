//go:build !linux && !windows

package mediafiles

import "errors"

func availableBytes(string) (uint64, error) {
	return 0, errors.New("disk capacity probe unsupported on this platform")
}

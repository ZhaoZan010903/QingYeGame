package mediaobject

import (
	"errors"
	"io"
	"os"
	"regexp"
	"time"
)

var spoolName = regexp.MustCompile(`^(object-|private-preview-|process-)[0-9]+$`)

// CleanupSpool only handles our private scratch area, never business objects.
// Five days is much longer than any upload/transfer lease, including restarts.
func CleanupSpool(path string, now time.Time) (int, error) {
	root, err := os.OpenRoot(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, ErrStorage
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return 0, ErrStorage
	}
	defer directory.Close()
	count := 0
	for count < 200 {
		entries, readErr := directory.ReadDir(100)
		for _, entry := range entries {
			if !spoolName.MatchString(entry.Name()) {
				continue
			}
			info, e := root.Lstat(entry.Name())
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil {
				return count, ErrStorage
			}
			if info.Mode()&os.ModeSymlink != 0 || info.ModTime().After(now.Add(-5*24*time.Hour)) {
				continue
			}
			if e = root.RemoveAll(entry.Name()); e != nil {
				return count, ErrStorage
			}
			count++
			if count >= 200 {
				break
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return count, ErrStorage
		}
	}
	return count, nil
}

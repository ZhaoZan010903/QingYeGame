package mediafiles

import (
	"errors"
	"io"
	"os"
	"regexp"
	"time"
)

var scratchName = regexp.MustCompile(`^\.(incoming-[0-9]+|chunk-[a-f0-9]{32}|processed-[0-9]+|thumb-[0-9]+)$`)

// Interrupted request scratch files have no business identity. Raw/image keys are excluded.
func (s *Store) CleanupScratch(now time.Time) (int, error) {
	root, err := os.OpenRoot(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	count := 0
	for _, name := range []string{"uploads", "images", "derived"} {
		directory, e := root.Open(name)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return count, e
		}
		for count < 200 {
			entries, readErr := directory.ReadDir(100)
			for _, entry := range entries {
				if !scratchName.MatchString(entry.Name()) {
					continue
				}
				key := name + "/" + entry.Name()
				info, e := root.Lstat(key)
				if errors.Is(e, os.ErrNotExist) {
					continue
				}
				if e != nil {
					directory.Close()
					return count, e
				}
				if !info.Mode().IsRegular() || info.ModTime().After(now.Add(-5*24*time.Hour)) {
					continue
				}
				if e = root.Remove(key); e != nil && !errors.Is(e, os.ErrNotExist) {
					directory.Close()
					return count, e
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
				directory.Close()
				return count, readErr
			}
		}
		directory.Close()
		if count >= 200 {
			break
		}
	}
	return count, nil
}

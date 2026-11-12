package mediafiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Only server-generated upload/image paths are removable; legacy/import trees are excluded.
var managedMediaID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

func sameManagedPath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (s *Store) RemoveManaged(id, key string) error {
	if !managedMediaID.MatchString(id) {
		return errors.New("invalid managed media id")
	}
	allowed := map[string]bool{"uploads/" + id + ".raw": true, "images/" + id + ".jpg": true, "images/" + id + ".png": true}
	if regexp.MustCompile(`^images/` + regexp.QuoteMeta(id) + `-[a-f0-9]{16}\.(png|jpg)$`).MatchString(key) {
		allowed[key] = true
	}
	if !allowed[key] {
		return errors.New("unmanaged storage key cannot be removed")
	}
	realRoot, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return err
	}
	for candidate := range allowed {
		path, err := ReadPath(s.Root, candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		expected, e := Path(realRoot, candidate)
		if e != nil {
			return e
		}
		if !sameManagedPath(expected, path) {
			return errors.New("managed media symlinks cannot be removed")
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		derivedKey := fmt.Sprintf("%s:%s:%d:%d:v1", id, path, info.Size(), info.ModTime().UnixNano())
		derived, err := ReadPath(s.Root, "derived/"+hash(derivedKey)+".jpg")
		if err == nil {
			expectedDerived, e := Path(realRoot, "derived/"+hash(derivedKey)+".jpg")
			if e != nil {
				return e
			}
			if !sameManagedPath(expectedDerived, derived) {
				return errors.New("derived media symlinks cannot be removed")
			}
			if err = os.Remove(derived); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

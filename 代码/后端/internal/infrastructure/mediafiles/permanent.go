package mediafiles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var legacyPermanentKey = regexp.MustCompile(`^legacy/[0-9a-f]{64}$`)
var derivedPermanentKey = regexp.MustCompile(`^derived/[0-9a-f]{64}\.jpg$`)

func permanentKey(id, key string) bool {
	if !managedMediaID.MatchString(id) {
		return false
	}
	return legacyPermanentKey.MatchString(key) || key == "uploads/"+id+".raw" ||
		regexp.MustCompile(`^images/`+regexp.QuoteMeta(id)+`(?:-[a-f0-9]{16})?\.(png|jpg|jpeg|webp)$`).MatchString(key)
}
func (s *Store) exactPermanentPath(key string) (string, error) {
	path, err := ReadPath(s.Root, key)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return "", err
	}
	expected, err := Path(root, key)
	if err != nil {
		return "", err
	}
	if !sameManagedPath(path, expected) {
		return "", errors.New("permanent cleanup refuses symlinks")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("permanent cleanup requires regular files")
	}
	return path, nil
}

// Persist the derived key before deleting either file. A crash between DELETEs
// can then retry the exact same paths even after the original is gone.
func (s *Store) PlanPermanent(ctx context.Context, id, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !permanentKey(id, key) {
		return "", errors.New("invalid permanent local key")
	}
	if strings.HasPrefix(key, "uploads/") {
		return "", nil
	}
	source, err := s.exactPermanentPath(key)
	if err != nil {
		return "", errors.New("original identity unavailable for derivative cleanup")
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	return "derived/" + hash(fmt.Sprintf("%s:%s:%d:%d:v1", id, source, info.Size(), info.ModTime().UnixNano())) + ".jpg", nil
}
func (s *Store) RemovePermanent(ctx context.Context, id, key, derived string) error {
	if !permanentKey(id, key) || derived != "" && !derivedPermanentKey.MatchString(derived) {
		return errors.New("invalid permanent local key")
	}
	keys := []string{key}
	if derived != "" {
		keys = []string{derived, key}
	}
	for _, candidate := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := s.exactPermanentPath(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err = os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

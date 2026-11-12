// Package medialifecycle moves only explicitly enrolled R20 product objects.
package medialifecycle

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

type ObjectStore interface {
	Put(context.Context, string, io.Reader, int64, string, string) error
	Open(context.Context, string) (io.ReadCloser, error)
	Verify(context.Context, string, int64, string) error
	Delete(context.Context, string) error
	SignedURL(context.Context, string, time.Duration) (string, error)
}

type Object struct {
	MediaID, Store, Key, MIME, SHA256 string
	Size                              int64
}
type Transfer func(context.Context, Object, string, string) error
type Remove func(context.Context, string, string) error
type Repository interface {
	Step(context.Context, Transfer, Remove) (bool, error)
}
type Worker struct {
	Repository         Repository
	Staging, Published ObjectStore
}

func (w *Worker) store(name string) (ObjectStore, error) {
	switch name {
	case "staging":
		if w.Staging != nil {
			return w.Staging, nil
		}
	case "oss":
		if w.Published != nil {
			return w.Published, nil
		}
	}
	return nil, fmt.Errorf("unavailable media store %q", name)
}
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return w.Repository.Step(ctx, func(ctx context.Context, o Object, destination, key string) error {
		source, err := w.store(o.Store)
		if err != nil {
			return err
		}
		target, err := w.store(destination)
		if err != nil {
			return err
		}
		r, err := source.Open(ctx, o.Key)
		if err != nil {
			return err
		}
		defer r.Close()
		if err = target.Put(ctx, key, r, o.Size, o.MIME, o.SHA256); err != nil {
			return err
		}
		// Metadata supplied by the uploader is not evidence of durable content.
		return target.Verify(ctx, key, o.Size, o.SHA256)
	}, func(ctx context.Context, store, key string) error {
		s, err := w.store(store)
		if err != nil {
			return err
		}
		return s.Delete(ctx, key)
	})
}

// NextKey never reuses a source generation: delayed deletes cannot hit a relist.
func NextKey(current, generation string) (string, error) {
	parts := strings.Split(current, "/")
	if len(parts) != 6 || parts[0] != "r20" || parts[2] != "objects" || parts[1] == "" || parts[3] == "" || parts[4] == "" || (parts[5] != "original" && parts[5] != "preview") || generation == "" {
		return "", fmt.Errorf("invalid managed object key")
	}
	for _, p := range append(parts, generation) {
		if p == "." || p == ".." || strings.ContainsAny(p, "\\\x00?#%") || strings.Contains(p, "/") {
			return "", fmt.Errorf("invalid managed object key")
		}
	}
	parts[4] = generation
	return strings.Join(parts, "/"), nil
}

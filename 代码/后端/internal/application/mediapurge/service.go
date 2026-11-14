// Package mediapurge performs only durable, exact-key permanent cleanup jobs.
package mediapurge

import (
	"context"
	"errors"
	"time"
)

type Object struct {
	ID                                                                       int64
	MediaID, Store, StorageKey, DerivedKey, Bucket, ObjectKey, SHA256, CRC64 string
	Size                                                                     int64
	Planned                                                                  bool
}
type Plan func(context.Context, string, string) (string, error)
type Remove func(context.Context, Object) error
type Repository interface {
	StepPermanent(context.Context, Plan, Remove) (bool, error)
}
type Local interface {
	PlanPermanent(context.Context, string, string) (string, error)
	RemovePermanent(context.Context, string, string, string) error
}
type Legacy interface {
	DeleteReplica(context.Context, Object) error
}
type Worker struct {
	Repository Repository
	Local      Local
	Legacy     Legacy
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return w.Repository.StepPermanent(ctx, w.Local.PlanPermanent, func(ctx context.Context, o Object) error {
		switch o.Store {
		case "local":
			return w.Local.RemovePermanent(ctx, o.MediaID, o.StorageKey, o.DerivedKey)
		case "legacy_oss":
			if w.Legacy != nil {
				return w.Legacy.DeleteReplica(ctx, o)
			}
		}
		return errors.New("permanent cleanup storage unavailable")
	})
}

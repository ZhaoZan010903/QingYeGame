package mediapurge

import (
	"context"
	"testing"
)

type disabledLegacyRepository struct{ completed bool }

func (r *disabledLegacyRepository) StepPermanent(ctx context.Context, _ Plan, remove Remove) (bool, error) {
	err := remove(ctx, Object{Store: "legacy_oss"})
	r.completed = err == nil
	return true, err
}

type localFixture struct{}

func (localFixture) PlanPermanent(context.Context, string, string) (string, error) { return "", nil }
func (localFixture) RemovePermanent(context.Context, string, string, string) error { return nil }
func TestDisabledLegacyDeletionNeverAcknowledgesPhysicalRemoval(t *testing.T) {
	repo := &disabledLegacyRepository{}
	worker := &Worker{Repository: repo, Local: localFixture{}}
	if worked, err := worker.RunOnce(context.Background()); !worked || err == nil || repo.completed {
		t.Fatal("disabled deletion acknowledged as complete")
	}
}

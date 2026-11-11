package security

import (
	"context"
	"errors"
	"testing"
)

func TestPasswordCapacityAndCancellation(t *testing.T) {
	v := NewPasswordVerifier(1)
	v.slots <- struct{}{}
	if err := v.Compare(context.Background(), "unused", "unused"); !errors.Is(err, ErrPasswordBusy) {
		t.Fatal("password capacity unbounded")
	}
	<-v.slots
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := v.Compare(ctx, "unused", "unused"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled verification started")
	}
	if len(v.slots) != 0 {
		t.Fatal("verification slot leaked")
	}
}

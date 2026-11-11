package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"qingye/backend/internal/domain/recommendation"
	"testing"
	"time"
)

type recommendationProbe struct {
	Repository
	disabled               bool
	scoreErr, errorRefresh error
	historyCalls           int
}

func (p *recommendationProbe) Preferences(context.Context, string) (bool, time.Time, error) {
	return !p.disabled, time.Unix(0, 0), nil
}
func (p *recommendationProbe) History(context.Context, string, time.Time) ([]recommendation.View, error) {
	p.historyCalls++
	return []recommendation.View{{ID: "private-product-id", Category: "one", Name: "One", Weight: 3}, {ID: "other", Category: "two", Name: "Two", Weight: 1}}, nil
}
func (p *recommendationProbe) ItemScores(context.Context, map[string]float64) (map[string]float64, error) {
	return nil, p.scoreErr
}
func (p *recommendationProbe) RefreshPopular(context.Context, time.Time, string) error {
	return p.errorRefresh
}

func TestDisabledProfileAvoidsHistoryAndInternalWeightsStayPrivate(t *testing.T) {
	p := &recommendationProbe{disabled: true}
	s := New(p, func() string { return "batch" }, nil)
	profile, source, err := s.Personalized(context.Background(), "id")
	if err != nil || profile.Enabled || source != "disabled" || p.historyCalls != 0 {
		t.Fatalf("disabled result %+v %s %v", profile, source, err)
	}
	p.disabled = false
	profile, err = s.Profile(context.Background(), "id")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Products["private-product-id"] != 0.75 {
		t.Fatalf("bad weight %v", profile.Products)
	}
	raw, _ := json.Marshal(profile)
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	if len(wire) != 3 {
		t.Fatalf("internal profile leaked: %s", raw)
	}
}
func TestDerivedIndexFailureFallsBackButCancellationPropagates(t *testing.T) {
	p := &recommendationProbe{scoreErr: errors.New("index unavailable")}
	s := New(p, func() string { return "batch" }, nil)
	profile, source, err := s.Personalized(context.Background(), "id")
	if err != nil || source != "browsing" || len(profile.Weights["itemcf"]) != 0 {
		t.Fatalf("fallback failed: %s %v", source, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = s.Personalized(ctx, "id")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation swallowed: %v", err)
	}
}
func TestBatchPointerPublishedOnlyAfterCommit(t *testing.T) {
	p := &recommendationProbe{errorRefresh: errors.New("commit failed")}
	published := 0
	s := New(p, func() string { return "batch" }, func(context.Context, string) { published++ })
	if err := s.RefreshPopular(context.Background(), time.Time{}); err == nil || published != 0 {
		t.Fatalf("uncommitted pointer published: %v %d", err, published)
	}
	p.errorRefresh = nil
	if err := s.RefreshPopular(context.Background(), time.Time{}); err != nil || published != 1 {
		t.Fatalf("pointer missing: %v %d", err, published)
	}
}

package management

import (
	"context"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"testing"
)

func TestRecommendationRejectsBeforePersistence(t *testing.T) {
	service := New(nil, nil, nil)
	boost := 20
	_, err := service.Recommendation(context.Background(), management.Actor{ID: "member", Role: "member"}, "p", management.RecommendationInput{Boost: &boost, ExpectedVersion: 1})
	if f, ok := fault.As(err); !ok || f.Kind != fault.Forbidden {
		t.Fatalf("member error: %v", err)
	}
	_, err = service.Recommendation(context.Background(), management.Actor{ID: "admin", Role: "admin"}, "p", management.RecommendationInput{})
	if f, ok := fault.As(err); !ok || f.Code != "INVALID_RECOMMENDATION_BOOST" {
		t.Fatalf("input error: %v", err)
	}
}

package management

import (
	"qingye/backend/internal/domain/fault"
	"testing"
)

func TestRecommendationInputBounds(t *testing.T) {
	for _, value := range []int{-1, 0, 1, 100, 101} {
		in := RecommendationInput{Boost: &value, ExpectedVersion: 1}
		err := ValidateRecommendationInput(in)
		if value >= 0 && value <= 100 {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if f, ok := fault.As(err); !ok || f.Code != "INVALID_RECOMMENDATION_BOOST" {
			t.Fatalf("value %d: %v", value, err)
		}
	}
	if f, ok := fault.As(ValidateRecommendationInput(RecommendationInput{ExpectedVersion: 1})); !ok || f.Code != "INVALID_RECOMMENDATION_BOOST" {
		t.Fatal("missing boost accepted")
	}
	zero := 0
	for _, version := range []int64{0, -1} {
		if f, ok := fault.As(ValidateRecommendationInput(RecommendationInput{Boost: &zero, ExpectedVersion: version})); !ok || f.Code != "INVALID_VERSION" {
			t.Fatal("invalid version accepted")
		}
	}
}

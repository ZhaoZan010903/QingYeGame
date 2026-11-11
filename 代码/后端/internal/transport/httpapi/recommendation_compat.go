package httpapi

import (
	"qingye/backend/internal/domain/recommendation"
)

type recommendationConfig = recommendation.Config

func defaultRecommendations() recommendationConfig { return recommendation.DefaultConfig() }
func parseRecommendations(raw []byte) (recommendationConfig, error) {
	return recommendation.ParseConfig(raw)
}

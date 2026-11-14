package management

import "qingye/backend/internal/domain/fault"

type RecommendationInput struct {
	Boost           *int  `json:"boost"`
	ExpectedVersion int64 `json:"expected_version"`
}

func ValidateRecommendationInput(in RecommendationInput) error {
	if in.Boost == nil || *in.Boost < 0 || *in.Boost > 100 {
		return fault.New(fault.Invalid, "INVALID_RECOMMENDATION_BOOST", "推荐加分需为0至100的整数")
	}
	if in.ExpectedVersion < 1 {
		return fault.New(fault.Invalid, "INVALID_VERSION", "请提供当前推荐配置版本")
	}
	return nil
}

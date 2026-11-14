package httpapi

import (
	"encoding/json"
	"net/http"
	"qingye/backend/internal/domain/management"
)

func (a *App) adminProductRecommendation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var raw struct {
		Boost           json.RawMessage `json:"boost"`
		ExpectedVersion json.RawMessage `json:"expected_version"`
	}
	if !readJSON(w, r, &raw) {
		return
	}
	var in management.RecommendationInput
	if len(raw.Boost) > 0 && json.Unmarshal(raw.Boost, &in.Boost) != nil {
		fail(w, 422, "INVALID_RECOMMENDATION_BOOST", "推荐加分需为0至100的整数")
		return
	}
	if len(raw.ExpectedVersion) > 0 && json.Unmarshal(raw.ExpectedVersion, &in.ExpectedVersion) != nil {
		fail(w, 422, "INVALID_VERSION", "请提供当前推荐配置版本")
		return
	}
	data, err := a.managementService().Recommendation(r.Context(), managementActor(u), r.PathValue("id"), in)
	memberResponse(w, data, err)
}

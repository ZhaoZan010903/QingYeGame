package httpapi

import (
	"net/http"
	"net/url"
	"qingye/backend/internal/domain/management"
)

func (a *App) adminDashboard(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	query, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		fail(w, http.StatusBadRequest, "INVALID_DASHBOARD_QUERY", "看板查询参数格式无效")
		return
	}
	for name, values := range query {
		if (name != "days" && name != "rank_by") || len(values) != 1 || values[0] == "" {
			fail(w, http.StatusBadRequest, "INVALID_DASHBOARD_QUERY", "看板仅支持单个有效统计范围和排行参数")
			return
		}
	}
	in, err := management.DecodeDashboardQuery(query.Get("days"), query.Get("rank_by"))
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_DASHBOARD_QUERY", "请选择7天或30天，以及有效互动排行")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	data, err := a.managementService().Dashboard(r.Context(), managementActor(u), in)
	memberResponse(w, data, err)
}

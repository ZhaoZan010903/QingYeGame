package management

import (
	"qingye/backend/internal/domain/fault"
	"strconv"
)

type DashboardQuery struct {
	Days   int
	RankBy string
}

// A small fixed range and ranking vocabulary keep this aggregate read bounded.
func DecodeDashboardQuery(days, rankBy string) (DashboardQuery, error) {
	in := DashboardQuery{Days: 7, RankBy: "views"}
	if days != "" {
		parsed, err := strconv.Atoi(days)
		if err != nil || strconv.Itoa(parsed) != days {
			return in, fault.New(fault.Invalid, "INVALID_DASHBOARD_QUERY", "统计范围仅支持7天或30天")
		}
		in.Days = parsed
	}
	if rankBy != "" {
		in.RankBy = rankBy
	}
	return in, ValidateDashboardQuery(in)
}

func ValidateDashboardQuery(in DashboardQuery) error {
	if in.Days != 7 && in.Days != 30 {
		return fault.New(fault.Invalid, "INVALID_DASHBOARD_QUERY", "统计范围仅支持7天或30天")
	}
	if in.RankBy != "views" && in.RankBy != "favorites" && in.RankBy != "consultations" {
		return fault.New(fault.Invalid, "INVALID_DASHBOARD_QUERY", "请选择有效的互动排行")
	}
	return nil
}

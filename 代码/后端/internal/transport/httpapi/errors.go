package httpapi

import (
	"log/slog"
	"net/http"
	"qingye/backend/internal/domain/fault"
)

// 领域错误只公开稳定代码和文案；SQL、凭据和内部路径留在服务端。
func writeAppError(w http.ResponseWriter, err error) {
	e, ok := fault.As(err)
	if !ok {
		slog.Error("application failure", "error", err)
		fail(w, 500, "INTERNAL_ERROR", "服务暂时不可用")
		return
	}
	status := http.StatusInternalServerError
	switch e.Kind {
	case fault.Invalid:
		status = 422
	case fault.Unauthenticated:
		status = 401
	case fault.Forbidden:
		status = 403
	case fault.NotFound:
		status = 404
	case fault.Conflict:
		status = 409
	case fault.RateLimited:
		status = 429
		w.Header().Set("Retry-After", "60")
	case fault.Unavailable:
		status = 503
	case fault.Gone:
		status = 410
	case fault.TooLarge:
		status = 413
	case fault.UnsupportedMedia:
		status = 415
	}
	if e.Cause != nil {
		slog.Error("application operation failed", "code", e.Code, "error", e.Cause)
	}
	fail(w, status, e.Code, e.Message)
}

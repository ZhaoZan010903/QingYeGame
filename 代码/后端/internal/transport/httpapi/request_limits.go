package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (a *App) requestLimits(next http.Handler) http.Handler {
	c := a.Config.Defaults()
	slots := make(chan struct{}, c.MaxConcurrentRequests)
	healthSlots := make(chan struct{}, 2)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 长连接由聊天租约限制，健康检查不与业务请求争抢容量。
		if r.URL.Path == "/healthz" {
			select {
			case healthSlots <- struct{}{}:
				defer func() { <-healthSlots }()
			default:
				fail(w, 503, "HEALTH_BUSY", "健康检查繁忙")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/v1/chat/socket" || r.URL.Path == "/api/v1/admin/chat/socket" {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			if c.ResponseCrypto.Enabled {
				responseProtocolError(w, 503, "RESPONSE_BUSY")
			} else {
				fail(w, 503, "SERVER_BUSY", "服务繁忙，请稍后重试")
			}
			return
		}
		// 文件请求保留较长处理窗口，仍为数据库等待设上限；网络读取另有Server超时。
		budget := c.RequestTimeout
		if strings.HasPrefix(r.URL.Path, "/api/v1/media/") || strings.HasPrefix(r.URL.Path, "/api/v1/uploads/") {
			budget = 60 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

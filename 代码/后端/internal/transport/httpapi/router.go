package httpapi

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// routeRegistrar lets the HTTP adapters declare routes without leaking Gin into services.
type routeRegistrar interface {
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
}
type ginRouter struct{ *gin.Engine }

var ginModeOnce sync.Once

func newRouter() *ginRouter {
	ginModeOnce.Do(func() { gin.SetMode(gin.ReleaseMode) })
	e := gin.New()
	e.RedirectTrailingSlash = false
	e.RedirectFixedPath = false
	e.HandleMethodNotAllowed = true
	e.UseRawPath = true
	e.UnescapePathValues = false
	// 真实IP只由requestIP解析；不使用Gin默认的全信任代理配置。
	if err := e.SetTrustedProxies(nil); err != nil {
		panic(err)
	}
	e.NoRoute(func(c *gin.Context) { fail(c.Writer, 404, "NOT_FOUND", "接口不存在") })
	e.NoMethod(func(c *gin.Context) { fail(c.Writer, 405, "METHOD_NOT_ALLOWED", "请求方法不允许") })
	return &ginRouter{e}
}

func (m *ginRouter) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || path == "" {
		panic("invalid HTTP route: " + pattern)
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			parts[i] = ":" + p[1:len(p)-1]
		}
	}
	path = strings.Join(parts, "/")
	adapter := func(c *gin.Context) {
		for _, p := range c.Params {
			value := p.Value
			// RawPath为空时Gin已经使用解码后的Path，不能再次解码百分号。
			if c.Request.URL.RawPath != "" {
				var err error
				value, err = url.PathUnescape(value)
				if err != nil {
					fail(c.Writer, 400, "INVALID_PATH", "请求路径无效")
					return
				}
			}
			c.Request.SetPathValue(p.Key, value)
		}
		handler(c.Writer, c.Request)
	}
	m.Handle(method, path, adapter)
	if method == http.MethodGet {
		m.Handle(http.MethodHead, path, adapter)
	}
}

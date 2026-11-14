package httpapi

import (
	"compress/gzip"
	"mime"
	"net/http"
	"strings"
)

// Only published catalogue responses may combine compression with encryption.
// Private fields, authentication results and chat never enter this path.
func (a *App) compressPublicResponse(r *http.Request, capture *responseCapture, contentType string) string {
	if a.surface() != "public" || r.Method != http.MethodGet || capture.status != http.StatusOK || r.Header.Get("X-QY-Accept-Encoding") != "gzip" || len(capture.body) < 1024 || len(capture.body) > 4<<20 || !publicCompressionPath(r) {
		return "identity"
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return "identity"
	}
	// Reserve compressor workspace as well as both live payload buffers.
	const workspace = 2 << 20
	if capture.budget != nil {
		if !capture.budget.take(workspace) {
			return "identity"
		}
		defer capture.budget.release(workspace)
	}
	compressed := &responseCapture{header: http.Header{}, limit: len(capture.body) - 1, budget: capture.budget}
	defer compressed.release()
	w, err := gzip.NewWriterLevel(compressed, gzip.BestSpeed)
	if err != nil {
		return "identity"
	}
	_, writeErr := w.Write(capture.body)
	closeErr := w.Close()
	if writeErr != nil || closeErr != nil || compressed.err != nil || len(compressed.body) >= len(capture.body) {
		return "identity"
	}
	capture.release()
	capture.body, compressed.body = compressed.body, nil
	return "gzip"
}

func publicCompressionPath(r *http.Request) bool {
	path := r.URL.Path
	if r.URL.EscapedPath() != path {
		return false
	}
	switch path {
	case "/api/v1/categories", "/api/v1/home", "/api/v1/products":
		return true
	}
	const prefix = "/api/v1/products/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	id := strings.TrimPrefix(path, prefix)
	if strings.HasSuffix(id, "/images") {
		id = strings.TrimSuffix(id, "/images")
	}
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

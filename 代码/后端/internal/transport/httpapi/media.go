package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	service "qingye/backend/internal/application/media"
	domain "qingye/backend/internal/domain/media"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/postgres"
	"strings"
)

const maxImagePixels = domain.MaxImagePixels

var imageProcessors = mediafiles.ImageProcessors

func validUploadName(name, mime string) bool         { return domain.ValidUploadName(name, mime) }
func mediaPath(root, key string) (string, error)     { return mediafiles.Path(root, key) }
func readMediaPath(root, key string) (string, error) { return mediafiles.ReadPath(root, key) }
func reencodeImage(source io.ReadSeeker, dest io.Writer, mime string) (string, error) {
	return mediafiles.ReencodeImage(source, dest, mime)
}

type limitedImageWriter struct {
	w         io.Writer
	remaining int64
}

func (w *limitedImageWriter) Write(p []byte) (int, error) {
	limited := mediafiles.LimitedWriter{W: w.w, Remaining: w.remaining}
	n, err := limited.Write(p)
	w.remaining = limited.Remaining
	return n, err
}
func (a *App) mediaService() *service.Service {
	// R20 仅由 bootstrap 注入新商品对象适配；旧迁移读取预备仍不启用。
	// mediafilesoss 继续受 oss_read 标签隔离，不能据此切换历史图片路径。
	// 所有适配都必须先经过 Service.Open 的用途与资源权限校验。
	files := a.MediaFiles
	if files == nil {
		files = &mediafiles.Store{Root: a.Config.MediaDir}
	}
	return service.New(&postgres.MediaStore{DB: a.DB, ManagedEnabled: a.Config.MediaR20.Enabled}, files, cache.Limiter{Client: a.Redis}, a.surface(), a.uploadMax(), imageProcessors, newID, randomToken, tokenHash)
}
func (a *App) mediaActor(u *User) domain.Actor {
	return domain.Actor{ID: u.ID, Role: u.Role, Surface: a.surface()}
}
func (a *App) registerMedia(mux routeRegistrar) {
	mux.HandleFunc("POST /api/v1/uploads", a.memberMutation(a.createUpload))
	mux.HandleFunc("PUT /api/v1/uploads/{id}/content", a.memberMutation(a.receiveUpload))
	mux.HandleFunc("POST /api/v1/uploads/{id}/complete", a.memberMutation(a.completeUpload))
	mux.HandleFunc("GET /api/v1/uploads/{id}/status", a.uploadStatus)
	mux.HandleFunc("DELETE /api/v1/uploads/{id}", a.memberMutation(a.cancelUpload))
	mux.HandleFunc("GET /api/v1/media/{id}", a.serveMedia)
	mux.HandleFunc("GET /api/v1/media/{id}/file", a.serveMedia)
	mux.HandleFunc("GET /api/v1/media/{id}/preview", a.serveMedia)
}
func (a *App) uploadMax() int64 {
	if a.Config.MaxUploadBytes > 0 {
		return a.Config.MaxUploadBytes
	}
	return 10 << 20
}

func (a *App) createUpload(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var input domain.CreateInput
	if !readJSON(w, r, &input) {
		return
	}
	out, err := a.mediaService().Create(r.Context(), a.mediaActor(u), input)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 201, out)
}
func (a *App) receiveUpload(w http.ResponseWriter, r *http.Request) {
	var err error
	if header := r.Header.Get("Content-Range"); header != "" {
		err = a.mediaService().ReceiveRange(r.Context(), r.PathValue("id"), r.URL.Query().Get("token"), header, r.ContentLength, r.Body)
	} else {
		err = a.mediaService().Receive(r.Context(), r.PathValue("id"), r.URL.Query().Get("token"), r.ContentLength, r.Body)
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"ok": true}})
}
func (a *App) completeUpload(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	out, err := a.mediaService().Complete(r.Context(), a.mediaActor(u), r.PathValue("id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) serveMedia(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	file, err := a.mediaService().Open(r.Context(), id, r.URL.Query().Get("variant") == "thumb" || strings.HasSuffix(r.URL.Path, "/preview"), func(ctx context.Context) (domain.Actor, error) {
		u, err := a.currentUserBySession(ctx, a.requestSessionHash(r))
		if err != nil {
			return domain.Actor{}, err
		}
		return a.mediaActor(u), nil
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	if file.RedirectURL != "" {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, file.RedirectURL, http.StatusFound)
		return
	}
	defer file.Reader.Close()
	w.Header().Set("Content-Type", file.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", id))
	http.ServeContent(w, r, id, file.Modified, file.Reader)
}

func (a *App) uploadStatus(w http.ResponseWriter, r *http.Request) {
	// This GET refreshes a capability; it requires the same trusted source as upload writes.
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref, e := url.Parse(r.Referer()); e == nil && ref.Host != "" {
			origin = ref.Scheme + "://" + ref.Host
		}
	}
	allowed := false
	for _, v := range a.Config.AllowedOrigins {
		if origin != "" && origin == strings.TrimRight(v, "/") {
			allowed = true
			break
		}
	}
	if !allowed || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "ORIGIN_REJECTED", "请求来源不允许")
		return
	}

	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	out, err := a.mediaService().Status(r.Context(), a.mediaActor(u), r.PathValue("id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (a *App) cancelUpload(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	if err := a.mediaService().Cancel(r.Context(), a.mediaActor(u), r.PathValue("id")); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"ok": true}})
}

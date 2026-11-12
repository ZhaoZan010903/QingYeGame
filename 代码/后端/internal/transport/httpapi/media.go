package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	service "qingye/backend/internal/application/media"
	domain "qingye/backend/internal/domain/media"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/postgres"
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
	// OSS 待读图迁移完成后再显式装配，当前继续本地读取；不要取消此注释直接切换。
	// 预备实现：internal/infrastructure/mediafilesoss（仅 oss_read 标签编译）。
	// 未来在 bootstrap 复用已验证清单和只读客户端，再注入 service.New 的 Files；
	// 必须保持 Service.Open 的商品/聊天/头像权限检查先于任何远端读，缩略图仍走本地。
	return service.New(&postgres.MediaStore{DB: a.DB}, &mediafiles.Store{Root: a.Config.MediaDir}, cache.Limiter{Client: a.Redis}, a.surface(), a.uploadMax(), imageProcessors, newID, randomToken, tokenHash)
}
func (a *App) mediaActor(u *User) domain.Actor {
	return domain.Actor{ID: u.ID, Role: u.Role, Surface: a.surface()}
}
func (a *App) registerMedia(mux routeRegistrar) {
	mux.HandleFunc("POST /api/v1/uploads", a.memberMutation(a.createUpload))
	mux.HandleFunc("PUT /api/v1/uploads/{id}/content", a.memberMutation(a.receiveUpload))
	mux.HandleFunc("POST /api/v1/uploads/{id}/complete", a.memberMutation(a.completeUpload))
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
	err := a.mediaService().Receive(r.Context(), r.PathValue("id"), r.URL.Query().Get("token"), r.ContentLength, r.Body)
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
	file, err := a.mediaService().Open(r.Context(), id, r.URL.Query().Get("variant") == "thumb", func(ctx context.Context) (domain.Actor, error) {
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
	defer file.Reader.Close()
	w.Header().Set("Content-Type", file.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", id))
	http.ServeContent(w, r, id, file.Modified, file.Reader)
}

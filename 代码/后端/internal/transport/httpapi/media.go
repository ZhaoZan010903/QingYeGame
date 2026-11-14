package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	service "qingye/backend/internal/application/media"
	securityapp "qingye/backend/internal/application/security"
	domain "qingye/backend/internal/domain/media"
	securitydomain "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/postgres"
	"strings"
	"time"
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
		files = &mediafiles.Store{Root: a.Config.MediaDir, ThumbnailCommit: (&postgres.MediaPermanentStore{DB: a.DB}).CommitThumbnail}
	}
	return service.New(&postgres.MediaStore{DB: a.DB, ManagedEnabled: a.Config.MediaR20.Enabled}, files, cache.Limiter{Client: a.Redis}, a.surface(), a.uploadMax(), imageProcessors, newID, randomToken, tokenHash)
}
func (a *App) mediaActor(u *User) domain.Actor {
	return domain.Actor{ID: u.ID, Role: u.Role, Surface: a.surface()}
}
func (a *App) registerMedia(mux routeRegistrar) {
	mux.HandleFunc("POST /api/v1/media/access-plan", a.mediaAccessPlan)
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
	item, itemErr := mediaItemFromRequest(r)
	if itemErr != nil {
		writeAppError(w, itemErr)
		return
	}
	checkVersion := func() error {
		return a.mediaService().CheckProductVersion(r.Context(), id, item.ProductID, item.ProductVersion)
	}
	var before func(domain.Upload) error
	before = func(domain.Upload) error { return checkVersion() }
	var expand func(int64) (securitydomain.ReadDecision, error)
	var decision securitydomain.ReadDecision
	denied := errors.New("media access denied")
	measured := &measuredMediaWriter{ResponseWriter: w, Context: r.Context()}
	if a.Config.MediaSafety.Enabled {
		if !a.safetyReady(w) {
			return
		}
		who, e := a.mediaSubject(w, r)
		if e != nil {
			a.mediaSafetyError(w, e)
			return
		}
		lease := newID()
		acquired := false
		defer func() {
			if acquired {
				finish := func(sent int64) {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					_ = a.ReadSafety.Store.Finish(ctx, who, lease, sent)
				}
				if !afterResponseWrite(r, finish) {
					finish(measured.Bytes)
				}
			}
		}()
		before = func(m domain.Upload) error {
			if err := checkVersion(); err != nil {
				return err
			}
			var e error
			if grant := r.URL.Query().Get("grant"); grant != "" {
				store, ok := a.ReadSafety.Store.(securityapp.DeliveryStore)
				if !ok {
					return securitydomain.ErrUnavailable
				}
				decision, e = store.BeginGrant(r.Context(), who, grant, a.mediaPlanDescriptor(who, item, m), lease)
			} else {
				decision, e = a.ReadSafety.Store.Begin(r.Context(), who, lease, m.Size)
			}
			if e != nil {
				return e
			}
			if decision.State != "allowed" {
				return denied
			}
			acquired = true
			reserved := m.Size
			if reserved == 0 {
				reserved = 64 << 20
			}
			expand = func(actual int64) (securitydomain.ReadDecision, error) {
				return a.ReadSafety.Store.Expand(r.Context(), who, actual-reserved)
			}
			return nil
		}
	}
	file, err := a.mediaService().OpenChecked(r.Context(), id, r.URL.Query().Get("variant") == "thumb" || strings.HasSuffix(r.URL.Path, "/preview"), func(ctx context.Context) (domain.Actor, error) {
		u, err := a.currentUser(r)
		if err != nil {
			return domain.Actor{}, err
		}
		return a.mediaActor(u), nil
	}, before)
	if err != nil {
		if errors.Is(err, denied) {
			writeReadDecision(w, decision)
			return
		}
		if errors.Is(err, securitydomain.ErrUnavailable) {
			a.mediaSafetyError(w, err)
			return
		}
		writeAppError(w, err)
		return
	}
	if file.RedirectURL != "" {
		if a.Config.MediaSafety.Enabled {
			a.mediaSafetyError(w, securitydomain.ErrUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, file.RedirectURL, http.StatusFound)
		return
	}
	defer file.Reader.Close()
	if expand != nil {
		size, e := file.Reader.Seek(0, io.SeekEnd)
		if e != nil || size < 0 || size > 64<<20 {
			a.mediaSafetyError(w, securitydomain.ErrUnavailable)
			return
		}
		if _, e = file.Reader.Seek(0, io.SeekStart); e != nil {
			a.mediaSafetyError(w, securitydomain.ErrUnavailable)
			return
		}
		reservedSize := size
		if a.Config.ResponseCrypto.Enabled && !file.ServerLocal {
			reservedSize += 16
		}
		d, e := expand(reservedSize)
		if e != nil {
			a.mediaSafetyError(w, e)
			return
		}
		if d.State != "allowed" {
			writeReadDecision(w, d)
			return
		}
	}
	if a.Config.MediaSafety.Enabled && strings.Contains(r.Header.Get("Range"), ",") {
		fail(w, http.StatusRequestedRangeNotSatisfiable, "SINGLE_RANGE_REQUIRED", "请按单一范围读取图片")
		return
	}
	w.Header().Set("Content-Type", file.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", id))
	if file.ServerLocal {
		markServerLocalImage(r)
	}
	http.ServeContent(measured, r, id, file.Modified, file.Reader)
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

package httpapi

import (
	"net/http"
	memberapp "qingye/backend/internal/application/member"
	memberdomain "qingye/backend/internal/domain/member"
	"qingye/backend/internal/infrastructure/postgres"
	"strconv"
	"strings"
)

func (a *App) memberService() *memberapp.Service {
	return memberapp.New(&postgres.MemberStore{DB: a.DB, NewID: newID})
}
func memberActor(u *User, admin bool) memberdomain.Actor {
	return memberdomain.Actor{ID: u.ID, Role: u.Role, Admin: admin}
}
func memberResponse(w http.ResponseWriter, data map[string]any, err error) {
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, data)
}
func (a *App) registerMember(mux routeRegistrar) {
	mux.HandleFunc("GET /api/v1/me/favorites", a.memberList)
	mux.HandleFunc("PUT /api/v1/me/favorites/{id}", a.memberMutation(a.favoriteChange))
	mux.HandleFunc("DELETE /api/v1/me/favorites/{id}", a.memberMutation(a.favoriteChange))
	mux.HandleFunc("GET /api/v1/me/products", a.memberList)
	mux.HandleFunc("POST /api/v1/me/products", a.memberMutation(a.createDraft))
	mux.HandleFunc("GET /api/v1/me/products/{id}", a.memberDetail)
	mux.HandleFunc("PATCH /api/v1/me/products/{id}", a.memberMutation(a.editDraft))
	mux.HandleFunc("POST /api/v1/me/products/{id}/submit", a.memberMutation(a.memberTransition))
	mux.HandleFunc("POST /api/v1/me/products/{id}/withdraw", a.memberMutation(a.memberTransition))
	mux.HandleFunc("GET /api/v1/me/products/{id}/images", a.memberImageList)
	mux.HandleFunc("POST /api/v1/me/products/{id}/images", a.memberMutation(a.appendMemberImages))
	mux.HandleFunc("PATCH /api/v1/me/products/{id}/images/{media_id}", a.memberMutation(a.changeMemberImage))
	mux.HandleFunc("DELETE /api/v1/me/products/{id}/images/{media_id}", a.memberMutation(a.changeMemberImage))
}
func (a *App) registerAdminImages(mux routeRegistrar) {
	mux.HandleFunc("GET /api/v1/admin/products/{id}/images", a.memberImageList)
	mux.HandleFunc("POST /api/v1/admin/products/{id}/images", a.memberMutation(a.appendMemberImages))
	mux.HandleFunc("PATCH /api/v1/admin/products/{id}/images/{media_id}", a.memberMutation(a.changeMemberImage))
	mux.HandleFunc("DELETE /api/v1/admin/products/{id}/images/{media_id}", a.memberMutation(a.changeMemberImage))
}
func (a *App) imageActor(w http.ResponseWriter, r *http.Request) (*User, bool) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
		return a.requireAdmin(w, r)
	}
	return a.requireUser(w, r)
}
func imageAdmin(r *http.Request, u *User) bool {
	return strings.HasPrefix(r.URL.Path, "/api/v1/admin/") && u.Role == "admin"
}
func memberPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	offset := 0
	limit := 20
	var err error
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 1000000 {
			fail(w, 422, "INVALID_CURSOR", "分页参数无效")
			return 0, 0, false
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			fail(w, 422, "INVALID_LIMIT", "每页应为1至50条")
			return 0, 0, false
		}
	}
	return offset, limit, true
}
func (a *App) memberDetail(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	data, err := a.memberService().Detail(r.Context(), memberActor(u, false), r.PathValue("id"))
	memberResponse(w, data, err)
}
func checkMemberVersion(w http.ResponseWriter, actual, wanted int) bool {
	if wanted < 1 || actual != wanted {
		fail(w, 409, "VERSION_CONFLICT", "资料已更新，请重新加载后操作")
		return false
	}
	return true
}
func (a *App) memberList(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	actor := memberActor(u, false)
	offset, limit, ok := memberPagination(w, r)
	if !ok {
		return
	}
	in := memberdomain.ListInput{Offset: offset, Limit: limit, Status: r.URL.Query().Get("status"), Favorites: strings.HasSuffix(r.URL.Path, "/favorites")}
	data, err := a.memberService().List(r.Context(), actor, in)
	memberResponse(w, data, err)
}
func (a *App) favoriteChange(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	actor := memberActor(u, false)
	data, err := a.memberService().Favorite(r.Context(), actor, r.PathValue("id"), r.Method == "PUT")
	memberResponse(w, data, err)
}
func (a *App) createDraft(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	actor := memberActor(u, false)
	var input ProductInput
	if !readJSON(w, r, &input) {
		return
	}
	data, err := a.memberService().Create(r.Context(), actor, input)
	memberResponse(w, data, err)
}
func (a *App) editDraft(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	actor := memberActor(u, false)
	var input ProductInput
	if !readJSON(w, r, &input) {
		return
	}
	data, err := a.memberService().Edit(r.Context(), actor, r.PathValue("id"), input)
	memberResponse(w, data, err)
}
func (a *App) memberTransition(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	actor := memberActor(u, false)
	var input memberdomain.TransitionInput
	if !readJSON(w, r, &input) {
		return
	}
	data, err := a.memberService().Transition(r.Context(), actor, r.PathValue("id"), input, strings.HasSuffix(r.URL.Path, "/submit"))
	memberResponse(w, data, err)
}
func (a *App) memberImageList(w http.ResponseWriter, r *http.Request) {
	u, ok := a.imageActor(w, r)
	if !ok {
		return
	}
	actor := memberActor(u, imageAdmin(r, u))
	offset, limit, ok := memberPagination(w, r)
	if !ok {
		return
	}
	in := memberdomain.GalleryInput{Offset: offset, Limit: limit, ReviewID: r.URL.Query().Get("review_id")}
	if raw := r.URL.Query().Get("expected_version"); raw != "" {
		v, _ := strconv.Atoi(raw)
		in.ExpectedVersion = &v
	}
	data, err := a.memberService().Gallery(r.Context(), actor, r.PathValue("id"), in)
	memberResponse(w, data, err)
}
func (a *App) appendMemberImages(w http.ResponseWriter, r *http.Request) {
	u, ok := a.imageActor(w, r)
	if !ok {
		return
	}
	actor := memberActor(u, imageAdmin(r, u))
	var input memberdomain.AppendImagesInput
	if !readJSON(w, r, &input) {
		return
	}
	data, err := a.memberService().AppendImages(r.Context(), actor, r.PathValue("id"), input)
	memberResponse(w, data, err)
}
func (a *App) changeMemberImage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.imageActor(w, r)
	if !ok {
		return
	}
	actor := memberActor(u, imageAdmin(r, u))
	var input memberdomain.ChangeImageInput
	if !readJSON(w, r, &input) {
		return
	}
	data, err := a.memberService().ChangeImage(r.Context(), actor, r.PathValue("id"), r.PathValue("media_id"), input, r.Method == "DELETE")
	memberResponse(w, data, err)
}

package httpapi

import (
	"context"
	"net/http"
	service "qingye/backend/internal/application/chat"
	domain "qingye/backend/internal/domain/chat"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/postgres"
	"strconv"
	"time"
)

const chatCapacity = domain.Capacity

func validChatID(value string, maxBytes int) bool { return domain.ValidID(value, maxBytes) }
func chatChannel(id string) string                { return cache.ChatChannel(id) }
func (a *App) notifyChat(ctx context.Context, ids ...string) {
	cache.ChatNotifier{Client: a.Redis}.Notify(ctx, ids...)
}
func (a *App) chatService() *service.Service {
	return service.New(&postgres.ChatStore{DB: a.DB}, cache.Limiter{Client: a.Redis}, cache.ChatNotifier{Client: a.Redis}, a.surface(), newID)
}
func (a *App) chatActor(w http.ResponseWriter, r *http.Request) (domain.Actor, bool) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return domain.Actor{}, false
	}
	return domain.Actor{ID: u.ID, Role: u.Role, Surface: a.surface(), SessionHash: a.requestSessionHash(r)}, true
}
func chatResult(w http.ResponseWriter, out map[string]any, err error) {
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) registerChat(m routeRegistrar) {
	prefix := "/api/v1/chat"
	if a.surface() == "admin" {
		prefix = "/api/v1/admin/chat"
	}
	m.HandleFunc("GET "+prefix+"/conversations", a.chatList)
	m.HandleFunc("GET "+prefix+"/conversations/{id}", a.chatDetail)
	m.HandleFunc("GET "+prefix+"/conversations/{id}/messages", a.chatMessages)
	m.HandleFunc("POST "+prefix+"/conversations/{id}/messages", a.memberMutation(a.chatSend))
	m.HandleFunc("POST "+prefix+"/conversations/{id}/read", a.memberMutation(a.chatRead))
	m.HandleFunc("POST "+prefix+"/conversations/{id}/close", a.memberMutation(a.chatClose))
	m.HandleFunc("POST "+prefix+"/transport-ticket", a.memberMutation(a.chatTransportTicket))
	m.HandleFunc("GET "+prefix+"/socket", a.chatWebSocket)
	if a.surface() == "admin" {
		m.HandleFunc("GET "+prefix+"/presence", a.chatPresence)
		m.HandleFunc("POST "+prefix+"/presence", a.memberMutation(a.chatPresence))
	} else {
		m.HandleFunc("POST "+prefix+"/conversations", a.memberMutation(a.chatStart))
	}
}

func (a *App) chatStart(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.chatActor(w, r)
	if !ok {
		return
	}
	var in domain.StartInput
	if !readJSON(w, r, &in) {
		return
	}
	out, err := a.chatService().Start(r.Context(), actor, in)
	chatResult(w, out, err)
}
func (a *App) chatList(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.chatActor(w, r)
	if !ok {
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			fail(w, 422, "INVALID_CURSOR", "会话分页位置无效")
			return
		}
		offset = n
	}
	out, err := a.chatService().List(r.Context(), actor, offset, r.URL.Query().Get("cursor"))
	chatResult(w, out, err)
}
func (a *App) chatDetail(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.chatActor(w, r)
	if !ok {
		return
	}
	out, err := a.chatService().Detail(r.Context(), actor, r.PathValue("id"))
	chatResult(w, out, err)
}
func (a *App) chatMessages(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.chatActor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	out, err := a.chatService().Messages(r.Context(), actor, r.PathValue("id"), q.Get("before"), q.Get("after"), q.Has("after"))
	chatResult(w, out, err)
}
func (a *App) chatSend(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.chatActor(w, r)
	if !ok {
		return
	}
	var in domain.SendInput
	if !readJSON(w, r, &in) {
		return
	}
	out, err := a.chatService().Send(r.Context(), actor, r.PathValue("id"), in)
	chatResult(w, out, err)
}
func (a *App) chatRead(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.chatActor(w, r)
	if !ok {
		return
	}
	var in domain.ReadInput
	if !readJSON(w, r, &in) {
		return
	}
	out, err := a.chatService().Read(r.Context(), actor, r.PathValue("id"), in)
	chatResult(w, out, err)
}
func (a *App) chatClose(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.chatActor(w, r)
	if !ok {
		return
	}
	out, err := a.chatService().Close(r.Context(), actor, r.PathValue("id"))
	chatResult(w, out, err)
}
func (a *App) chatPresence(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.chatActor(w, r)
	if !ok {
		return
	}
	var input *domain.PresenceInput
	if r.Method == http.MethodPost {
		input = &domain.PresenceInput{}
		if !readJSON(w, r, input) {
			return
		}
	}
	out, err := a.chatService().Presence(r.Context(), actor, input)
	chatResult(w, out, err)
}
func (a *App) DispatchChats(ctx context.Context) error { return a.chatService().Dispatch(ctx) }
func (a *App) RunChatDispatch(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			job, cancel := context.WithTimeout(ctx, 8*time.Second)
			_ = a.DispatchChats(job)
			cancel()
		}
	}
}

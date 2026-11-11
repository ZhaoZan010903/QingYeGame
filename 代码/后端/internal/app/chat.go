package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const chatCapacity = 3
const chatAssignmentLock = 1834181

func (a *App) registerChat(m *http.ServeMux) {
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

// The predicate comes only from the process surface, never from a client role.
func (a *App) chatOwnerColumn() string {
	if a.surface() == "admin" {
		return "admin_id"
	}
	return "member_id"
}
func (a *App) chatReadColumn() string {
	if a.surface() == "admin" {
		return "admin_read_id"
	}
	return "member_read_id"
}
func chatChannel(id string) string { return "qingye:chat:notify:" + id }
func (a *App) notifyChat(ctx context.Context, ids ...string) {
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			_ = a.Redis.Publish(ctx, chatChannel(id), "changed").Err()
		}
	}
}
func (a *App) notifyConversation(ctx context.Context, id string) {
	var member, admin string
	if a.DB.QueryRow(ctx, "SELECT member_id,coalesce(admin_id,'') FROM chat_conversations WHERE id=$1", id).Scan(&member, &admin) == nil {
		a.notifyChat(ctx, member, admin)
	}
}

func (a *App) chatStart(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var in struct {
		ProductID string `json:"product_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.ProductID) > 100 || in.ProductID == "" {
		fail(w, 422, "INVALID_PRODUCT", "请选择要咨询的商品")
		return
	}
	if !a.memberLimit(w, r, "chat-start:"+u.ID, 12, time.Minute) {
		return
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	if _, e = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock($1)", chatAssignmentLock); e != nil {
		a.dbError(w, e)
		return
	}
	var id string
	e = tx.QueryRow(r.Context(), "SELECT id FROM chat_conversations WHERE member_id=$1 AND product_id=$2 AND status<>'closed'", u.ID, in.ProductID).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		var snapshot []byte
		// Lock visibility while taking the snapshot; no private owner/legacy fields leave this boundary.
		e = tx.QueryRow(r.Context(), `SELECT jsonb_build_object('id',p.id,'code',p.product_code,'title',p.title,'price',p.price_amount::text,'unit',p.price_unit,
    'cover_url',coalesce((SELECT '/api/v1/media/'||i.media_id||'/file?variant=thumb' FROM product_images i JOIN media_files f ON f.id=i.media_id AND f.status='ready' WHERE i.product_id=p.id ORDER BY i.sort_order,i.media_id LIMIT 1),''))
    FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1 AND p.status='published' AND p.approved_version=p.version AND c.enabled FOR SHARE OF p,c`, in.ProductID).Scan(&snapshot)
		if errors.Is(e, pgx.ErrNoRows) {
			fail(w, 404, "PRODUCT_UNAVAILABLE", "商品暂不可咨询，请刷新商品页面")
			return
		}
		if e != nil {
			a.dbError(w, e)
			return
		}
		var open int
		if e = tx.QueryRow(r.Context(), "SELECT count(*) FROM chat_conversations WHERE member_id=$1 AND status<>'closed'", u.ID).Scan(&open); e != nil {
			a.dbError(w, e)
			return
		}
		if open >= 10 {
			fail(w, 409, "CHAT_LIMIT", "同时咨询的商品较多，请先结束不再需要的咨询")
			return
		}
		id = newID()
		if _, e = tx.Exec(r.Context(), "INSERT INTO chat_conversations(id,member_id,product_id,product_snapshot) VALUES($1,$2,$3,$4)", id, u.ID, in.ProductID, snapshot); e != nil {
			a.dbError(w, e)
			return
		}
		if _, e = tx.Exec(r.Context(), "INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,kind) VALUES($1,$2,'member','product-context','product')", id, u.ID); e != nil {
			a.dbError(w, e)
			return
		}
	} else if e != nil {
		a.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.dbError(w, e)
		return
	}
	// Committed even when notification/dispatch is temporarily unavailable; retry returns the same open conversation.
	_ = a.DispatchChats(r.Context())
	a.notifyConversation(r.Context(), id)
	writeJSON(w, 200, map[string]any{"data": map[string]any{"id": id}})
}

const chatProjection = `jsonb_build_object('id',c.id,'status',c.status,'product',c.product_snapshot,'created_at',c.created_at,'updated_at',c.updated_at,
 'member_name',m.nickname,'admin_name',coalesce(ad.nickname,''),
 'last_message_mine',coalesce((SELECT x.sender_id=$1 FROM chat_messages x WHERE x.conversation_id=c.id ORDER BY x.id DESC LIMIT 1),false),
 'last_message',coalesce((SELECT CASE WHEN x.kind='product' THEN '咨询这个商品' ELSE left(x.body,80) END FROM chat_messages x WHERE x.conversation_id=c.id ORDER BY x.id DESC LIMIT 1),''),
 'last_message_id',coalesce((SELECT max(x.id)::text FROM chat_messages x WHERE x.conversation_id=c.id),'0'),
 'unread',(SELECT count(*) FROM chat_messages x WHERE x.conversation_id=c.id AND x.sender_id<>$1 AND x.id>c.%s))`

func (a *App) chatList(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 0 || n > 100000 {
			fail(w, 422, "INVALID_CURSOR", "会话分页位置无效")
			return
		}
		offset = n
	}
	where := "c." + a.chatOwnerColumn() + "=$1"
	var total, unread int
	e := a.DB.QueryRow(r.Context(), "SELECT count(*) FROM chat_conversations c WHERE "+where, u.ID).Scan(&total)
	if e == nil {
		e = a.DB.QueryRow(r.Context(), "SELECT count(*) FROM chat_messages x JOIN chat_conversations c ON c.id=x.conversation_id WHERE "+where+" AND x.sender_id<>$1 AND x.id>c."+a.chatReadColumn(), u.ID).Scan(&unread)
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	sql := "SELECT " + fmt.Sprintf(chatProjection, a.chatReadColumn()) + " FROM chat_conversations c JOIN users m ON m.id=c.member_id LEFT JOIN users ad ON ad.id=c.admin_id WHERE " + where + " ORDER BY c.updated_at DESC,c.id LIMIT $2 OFFSET $3"
	rows, e := a.jsonRows(r.Context(), sql, u.ID, limit, offset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	var next any
	if offset+len(rows) < total {
		next = offset + len(rows)
	}
	writeJSON(w, 200, map[string]any{"data": rows, "total": total, "unread": unread, "next_offset": next})
}
func (a *App) chatDetail(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	sql := "SELECT " + fmt.Sprintf(chatProjection, a.chatReadColumn()) + " FROM chat_conversations c JOIN users m ON m.id=c.member_id LEFT JOIN users ad ON ad.id=c.admin_id WHERE c." + a.chatOwnerColumn() + "=$1 AND c.id=$2"
	rows, e := a.jsonRows(r.Context(), sql, u.ID, r.PathValue("id"))
	if e != nil {
		a.dbError(w, e)
		return
	}
	if len(rows) == 0 {
		fail(w, 404, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows[0]})
}
func (a *App) chatMessages(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	before := int64(9223372036854775807)
	after := int64(0)
	if raw := r.URL.Query().Get("before"); raw != "" {
		v, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || v < 1 {
			fail(w, 422, "INVALID_CURSOR", "消息分页位置无效")
			return
		}
		before = v
	}
	if raw := r.URL.Query().Get("after"); raw != "" {
		v, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || v < 0 || r.URL.Query().Get("before") != "" {
			fail(w, 422, "INVALID_CURSOR", "消息分页位置无效")
			return
		}
		after = v
	}
	// Ownership and rows use one SQL statement so reassignment cannot expose a stale owner's new messages.
	var exists bool
	if e := a.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM chat_conversations WHERE id=$1 AND "+a.chatOwnerColumn()+"=$2)", r.PathValue("id"), u.ID).Scan(&exists); e != nil {
		a.dbError(w, e)
		return
	}
	if !exists {
		fail(w, 404, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
		return
	}
	predicate, order, cursor := "x.id<$3", "DESC", before
	forward := r.URL.Query().Has("after")
	if forward {
		predicate, order, cursor = "x.id>$3", "ASC", after
	}
	rows, e := a.jsonRows(r.Context(), `SELECT jsonb_build_object('id',x.id::text,'body',x.body,'kind',x.kind,'sender_role',x.sender_role,'mine',x.sender_id=$2,'created_at',x.created_at,'client_id',CASE WHEN x.sender_id=$2 THEN x.client_id ELSE '' END)
 FROM chat_messages x JOIN chat_conversations c ON c.id=x.conversation_id WHERE c.id=$1 AND c.`+a.chatOwnerColumn()+`=$2 AND `+predicate+` ORDER BY x.id `+order+` LIMIT 51`, r.PathValue("id"), u.ID, cursor)
	if e != nil {
		a.dbError(w, e)
		return
	}
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	var next any
	if more {
		next = rows[len(rows)-1]["id"]
	}
	if !forward {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	writeJSON(w, 200, map[string]any{"data": rows, "before": map[bool]any{true: nil, false: next}[forward], "after": map[bool]any{true: next, false: nil}[forward]})
}
func (a *App) chatSend(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var in struct {
		Body     string `json:"body"`
		ClientID string `json:"client_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || utf8.RuneCountInString(in.Body) > 2000 || len(in.ClientID) < 1 || len(in.ClientID) > 80 || strings.ContainsRune(in.Body, 0) {
		fail(w, 422, "INVALID_MESSAGE", "消息需为 1–2000 字，请重新输入")
		return
	}
	if !a.memberLimit(w, r, "chat-send:"+u.ID, 60, time.Minute) {
		return
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var status string
	e = tx.QueryRow(r.Context(), "SELECT status FROM chat_conversations WHERE id=$1 AND "+a.chatOwnerColumn()+"=$2 FOR UPDATE", r.PathValue("id"), u.ID).Scan(&status)
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
		return
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	var id int64
	var oldBody string
	e = tx.QueryRow(r.Context(), "SELECT id,body FROM chat_messages WHERE conversation_id=$1 AND sender_id=$2 AND client_id=$3", r.PathValue("id"), u.ID, in.ClientID).Scan(&id, &oldBody)
	if e == nil {
		if oldBody != in.Body {
			fail(w, 409, "MESSAGE_REPLAY_CONFLICT", "重试标识已用于另一条消息")
			return
		}
	} else if errors.Is(e, pgx.ErrNoRows) {
		if status == "closed" {
			fail(w, 409, "CHAT_CLOSED", "咨询已结束，请从商品页重新发起")
			return
		}
		if e = tx.QueryRow(r.Context(), "INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body) VALUES($1,$2,$3,$4,$5) RETURNING id", r.PathValue("id"), u.ID, u.Role, in.ClientID, in.Body).Scan(&id); e != nil {
			a.dbError(w, e)
			return
		}
		if _, e = tx.Exec(r.Context(), "UPDATE chat_conversations SET updated_at=now() WHERE id=$1", r.PathValue("id")); e != nil {
			a.dbError(w, e)
			return
		}
	} else {
		a.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.dbError(w, e)
		return
	}
	a.notifyConversation(r.Context(), r.PathValue("id"))
	writeJSON(w, 200, map[string]any{"data": map[string]any{"id": strconv.FormatInt(id, 10)}})
}
func (a *App) chatRead(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	if !a.memberLimit(w, r, "chat-read:"+u.ID, 240, time.Minute) {
		return
	}
	var in struct {
		Through string `json:"through"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	through, e := strconv.ParseInt(in.Through, 10, 64)
	if e != nil || through < 1 {
		fail(w, 422, "INVALID_MESSAGE", "已读位置无效")
		return
	}
	// Never mark messages arriving after the last one actually rendered by the client.
	tag, e := a.DB.Exec(r.Context(), "UPDATE chat_conversations c SET "+a.chatReadColumn()+"=GREATEST(c."+a.chatReadColumn()+",coalesce((SELECT max(x.id) FROM chat_messages x WHERE x.conversation_id=c.id AND x.id<=$3),0)) WHERE c.id=$1 AND c."+a.chatOwnerColumn()+"=$2", r.PathValue("id"), u.ID, through)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
		return
	}
	a.notifyChat(r.Context(), u.ID)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *App) chatClose(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	tag, e := a.DB.Exec(r.Context(), "UPDATE chat_conversations SET status='closed',updated_at=now() WHERE id=$1 AND "+a.chatOwnerColumn()+"=$2 AND status<>'closed'", r.PathValue("id"), u.ID)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 409, "CHAT_CHANGED", "咨询已结束或已重新分配，请刷新")
		return
	}
	a.notifyConversation(r.Context(), r.PathValue("id"))
	_ = a.DispatchChats(r.Context())
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) chatPresence(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var in struct {
			Accepting *bool `json:"accepting"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if !a.memberLimit(w, r, "chat-presence:"+u.ID, 20, time.Minute) {
			return
		}
		_, e := a.DB.Exec(r.Context(), `INSERT INTO chat_presence(admin_id,session_hash,accepting,last_seen) VALUES($1,$2,coalesce($3,false),now())
   ON CONFLICT(admin_id) DO UPDATE SET session_hash=EXCLUDED.session_hash,accepting=coalesce($3,chat_presence.accepting),last_seen=now()`, u.ID, a.requestSessionHash(r), in.Accepting)
		if e != nil {
			a.dbError(w, e)
			return
		}
		_ = a.DispatchChats(r.Context())
	}
	var accepting, online bool
	var active, waiting int
	e := a.DB.QueryRow(r.Context(), `SELECT coalesce((SELECT accepting FROM chat_presence WHERE admin_id=$1),false),
 coalesce((SELECT last_seen>now()-interval '75 seconds' FROM chat_presence WHERE admin_id=$1),false),
 (SELECT count(*) FROM chat_conversations WHERE admin_id=$1 AND status='active'),(SELECT count(*) FROM chat_conversations WHERE status='waiting')`, u.ID).Scan(&accepting, &online, &active, &waiting)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"accepting": accepting, "online": online, "active": active, "capacity": chatCapacity, "waiting": waiting}})
}

// Dispatch is short and serial per database, so concurrent processes cannot exceed capacity.
func (a *App) DispatchChats(ctx context.Context) error {
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", chatAssignmentLock); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, `SELECT c.id,c.member_id,c.admin_id FROM chat_conversations c WHERE c.status='active' AND NOT EXISTS
 (SELECT 1 FROM chat_presence p JOIN users u ON u.id=p.admin_id JOIN sessions s ON s.token_hash=p.session_hash WHERE p.admin_id=c.admin_id AND p.last_seen>now()-interval '75 seconds' AND u.enabled AND u.role='admin' AND s.audience='admin' AND s.expires_at>now()) FOR UPDATE OF c`)
	if e != nil {
		return e
	}
	type expired struct{ id, member, admin string }
	var expiredRows []expired
	for rows.Next() {
		var item expired
		if e = rows.Scan(&item.id, &item.member, &item.admin); e != nil {
			rows.Close()
			return e
		}
		expiredRows = append(expiredRows, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	notify := []string{}
	for _, item := range expiredRows {
		if _, e = tx.Exec(ctx, "UPDATE chat_conversations SET admin_id=NULL,status='waiting',admin_read_id=0,updated_at=now() WHERE id=$1", item.id); e != nil {
			return e
		}
		notify = append(notify, item.member, item.admin)
	}
	rows, e = tx.Query(ctx, "SELECT id,member_id FROM chat_conversations WHERE status='waiting' ORDER BY created_at,id LIMIT 50 FOR UPDATE")
	if e != nil {
		return e
	}
	var waiting [][2]string
	for rows.Next() {
		var item [2]string
		if e = rows.Scan(&item[0], &item[1]); e != nil {
			rows.Close()
			return e
		}
		waiting = append(waiting, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, item := range waiting {
		var admin string
		e = tx.QueryRow(ctx, `SELECT p.admin_id FROM chat_presence p JOIN users u ON u.id=p.admin_id JOIN sessions s ON s.token_hash=p.session_hash
   WHERE p.accepting AND p.last_seen>now()-interval '75 seconds' AND u.enabled AND u.role='admin' AND s.audience='admin' AND s.expires_at>now()
   AND (SELECT count(*) FROM chat_conversations c WHERE c.admin_id=p.admin_id AND c.status='active')<$1 ORDER BY random() LIMIT 1`, chatCapacity).Scan(&admin)
		if errors.Is(e, pgx.ErrNoRows) {
			break
		}
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE chat_conversations SET admin_id=$2,status='active',admin_read_id=0,updated_at=now() WHERE id=$1 AND status='waiting'", item[0], admin); e != nil {
			return e
		}
		notify = append(notify, item[1], admin)
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	a.notifyChat(ctx, notify...)
	return nil
}
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

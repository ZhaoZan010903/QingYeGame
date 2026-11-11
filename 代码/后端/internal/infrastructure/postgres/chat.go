// Package postgres 实现持久化；会话归属条件与事务锁必须随 SQL 一起保留。
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/chat"
	"qingye/backend/internal/domain/fault"
	"strconv"
)

type ChatStore struct{ DB *pgxpool.Pool }

const chatAssignmentLock = 1834181

func chatOwnerColumn(actor chat.Actor) string {
	if actor.Surface == "admin" {
		return "admin_id"
	}
	return "member_id"
}
func chatReadColumn(actor chat.Actor) string {
	if actor.Surface == "admin" {
		return "admin_read_id"
	}
	return "member_read_id"
}

const chatProjection = `jsonb_build_object('id',c.id,'status',c.status,'product',c.product_snapshot,'created_at',c.created_at,'updated_at',c.updated_at,
 'member_name',m.nickname,'admin_name',coalesce(ad.nickname,''),
 'last_message_mine',coalesce((SELECT x.sender_id=$1 FROM chat_messages x WHERE x.conversation_id=c.id ORDER BY x.id DESC LIMIT 1),false),
 'last_message',coalesce((SELECT CASE WHEN x.kind='product' THEN '咨询这个商品' ELSE left(x.body,80) END FROM chat_messages x WHERE x.conversation_id=c.id ORDER BY x.id DESC LIMIT 1),''),
 'last_message_id',coalesce((SELECT max(x.id)::text FROM chat_messages x WHERE x.conversation_id=c.id),'0'),
 'unread',(SELECT count(*) FROM chat_messages x WHERE x.conversation_id=c.id AND x.sender_id<>$1 AND x.id>c.%s))`

func (s *ChatStore) Start(ctx context.Context, actor chat.Actor, in chat.StartInput, generatedID string) (string, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", chatAssignmentLock); e != nil {
		return "", e
	}
	var id string
	e = tx.QueryRow(ctx, "SELECT id FROM chat_conversations WHERE member_id=$1 AND product_id=$2 AND status<>'closed'", actor.ID, in.ProductID).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		var snapshot []byte
		// 锁住商品可见性再生成公开快照，避免泄露私有字段。
		e = tx.QueryRow(ctx, `SELECT jsonb_build_object('id',p.id,'code',p.product_code,'title',p.title,'price',p.price_amount::text,'unit',p.price_unit,
    'cover_url',coalesce((SELECT '/api/v1/media/'||i.media_id||'/file?variant=thumb' FROM product_images i JOIN media_files f ON f.id=i.media_id AND f.status='ready' WHERE i.product_id=p.id ORDER BY i.sort_order,i.media_id LIMIT 1),''))
    FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1 AND p.status='published' AND p.approved_version=p.version AND c.enabled FOR SHARE OF p,c`, in.ProductID).Scan(&snapshot)
		if errors.Is(e, pgx.ErrNoRows) {
			return "", fault.New(fault.NotFound, "PRODUCT_UNAVAILABLE", "商品暂不可咨询，请刷新商品页面")
		}
		if e != nil {
			return "", e
		}
		var open int
		if e = tx.QueryRow(ctx, "SELECT count(*) FROM chat_conversations WHERE member_id=$1 AND status<>'closed'", actor.ID).Scan(&open); e != nil {
			return "", e
		}
		if open >= 10 {
			return "", fault.New(fault.Conflict, "CHAT_LIMIT", "同时咨询的商品较多，请先结束不再需要的咨询")
		}
		id = generatedID
		if _, e = tx.Exec(ctx, "INSERT INTO chat_conversations(id,member_id,product_id,product_snapshot) VALUES($1,$2,$3,$4)", id, actor.ID, in.ProductID, snapshot); e != nil {
			return "", e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,kind) VALUES($1,$2,'member','product-context','product')", id, actor.ID); e != nil {
			return "", e
		}
	} else if e != nil {
		return "", e
	}
	if e = tx.Commit(ctx); e != nil {
		return "", e
	}
	return id, nil
}

func (s *ChatStore) List(ctx context.Context, actor chat.Actor, offset int) (map[string]any, error) {
	const limit = 50
	where := "c." + chatOwnerColumn(actor) + "=$1"
	var total, unread int
	e := s.DB.QueryRow(ctx, "SELECT count(*) FROM chat_conversations c WHERE "+where, actor.ID).Scan(&total)
	if e == nil {
		e = s.DB.QueryRow(ctx, "SELECT count(*) FROM chat_messages x JOIN chat_conversations c ON c.id=x.conversation_id WHERE "+where+" AND x.sender_id<>$1 AND x.id>c."+chatReadColumn(actor), actor.ID).Scan(&unread)
	}
	if e != nil {
		return nil, e
	}
	sql := "SELECT " + fmt.Sprintf(chatProjection, chatReadColumn(actor)) + " FROM chat_conversations c JOIN users m ON m.id=c.member_id LEFT JOIN users ad ON ad.id=c.admin_id WHERE " + where + " ORDER BY c.updated_at DESC,c.id LIMIT $2 OFFSET $3"
	rows, e := s.chatJSONRows(ctx, sql, actor.ID, limit, offset)
	if e != nil {
		return nil, e
	}
	var next any
	if offset+len(rows) < total {
		next = offset + len(rows)
	}
	return map[string]any{"data": rows, "total": total, "unread": unread, "next_offset": next}, nil
}

func (s *ChatStore) Detail(ctx context.Context, actor chat.Actor, id string) (map[string]any, error) {
	sql := "SELECT " + fmt.Sprintf(chatProjection, chatReadColumn(actor)) + " FROM chat_conversations c JOIN users m ON m.id=c.member_id LEFT JOIN users ad ON ad.id=c.admin_id WHERE c." + chatOwnerColumn(actor) + "=$1 AND c.id=$2"
	rows, e := s.chatJSONRows(ctx, sql, actor.ID, id)
	if e != nil {
		return nil, e
	}
	if len(rows) == 0 {
		return nil, fault.New(fault.NotFound, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
	}
	return map[string]any{"data": rows[0]}, nil
}

func (s *ChatStore) Messages(ctx context.Context, actor chat.Actor, id string, page chat.MessagePage) (map[string]any, error) {
	// 返回消息时再次限定归属，避免重分配后旧客服继续读取。
	var exists bool
	if e := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM chat_conversations WHERE id=$1 AND "+chatOwnerColumn(actor)+"=$2)", id, actor.ID).Scan(&exists); e != nil {
		return nil, e
	}
	if !exists {
		return nil, fault.New(fault.NotFound, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
	}
	predicate, order, cursor := "x.id<$3", "DESC", page.Before
	forward := page.Forward
	if forward {
		predicate, order, cursor = "x.id>$3", "ASC", page.After
	}
	rows, e := s.chatJSONRows(ctx, `SELECT jsonb_build_object('id',x.id::text,'body',x.body,'kind',x.kind,'sender_role',x.sender_role,'mine',x.sender_id=$2,'created_at',x.created_at,'client_id',CASE WHEN x.sender_id=$2 THEN x.client_id ELSE '' END)
 FROM chat_messages x JOIN chat_conversations c ON c.id=x.conversation_id WHERE c.id=$1 AND c.`+chatOwnerColumn(actor)+`=$2 AND `+predicate+` ORDER BY x.id `+order+` LIMIT 51`, id, actor.ID, cursor)
	if e != nil {
		return nil, e
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
	return map[string]any{"data": rows, "before": map[bool]any{true: nil, false: next}[forward], "after": map[bool]any{true: next, false: nil}[forward]}, nil
}

func (s *ChatStore) Send(ctx context.Context, actor chat.Actor, id string, in chat.SendInput) (map[string]any, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var status string
	e = tx.QueryRow(ctx, "SELECT status FROM chat_conversations WHERE id=$1 AND "+chatOwnerColumn(actor)+"=$2 FOR UPDATE", id, actor.ID).Scan(&status)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, fault.New(fault.NotFound, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
	}
	if e != nil {
		return nil, e
	}
	var messageID int64
	var oldBody string
	e = tx.QueryRow(ctx, "SELECT id,body FROM chat_messages WHERE conversation_id=$1 AND sender_id=$2 AND client_id=$3", id, actor.ID, in.ClientID).Scan(&messageID, &oldBody)
	if e == nil {
		if oldBody != in.Body {
			return nil, fault.New(fault.Conflict, "MESSAGE_REPLAY_CONFLICT", "重试标识已用于另一条消息")
		}
	} else if errors.Is(e, pgx.ErrNoRows) {
		if status == "closed" {
			return nil, fault.New(fault.Conflict, "CHAT_CLOSED", "咨询已结束，请从商品页重新发起")
		}
		if e = tx.QueryRow(ctx, "INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body) VALUES($1,$2,$3,$4,$5) RETURNING id", id, actor.ID, actor.Role, in.ClientID, in.Body).Scan(&messageID); e != nil {
			return nil, e
		}
		if _, e = tx.Exec(ctx, "UPDATE chat_conversations SET updated_at=now() WHERE id=$1", id); e != nil {
			return nil, e
		}
	} else {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return map[string]any{"data": map[string]any{"id": strconv.FormatInt(messageID, 10)}}, nil
}

func (s *ChatStore) Read(ctx context.Context, actor chat.Actor, id string, through int64) (map[string]any, error) {
	// 已读位置不越过客户端实际展示的消息。
	tag, e := s.DB.Exec(ctx, "UPDATE chat_conversations c SET "+chatReadColumn(actor)+"=GREATEST(c."+chatReadColumn(actor)+",coalesce((SELECT max(x.id) FROM chat_messages x WHERE x.conversation_id=c.id AND x.id<=$3),0)) WHERE c.id=$1 AND c."+chatOwnerColumn(actor)+"=$2", id, actor.ID, through)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() == 0 {
		return nil, fault.New(fault.NotFound, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
	}
	return map[string]any{"ok": true}, nil
}

func (s *ChatStore) Close(ctx context.Context, actor chat.Actor, id string) (map[string]any, error) {
	tag, e := s.DB.Exec(ctx, "UPDATE chat_conversations SET status='closed',updated_at=now() WHERE id=$1 AND "+chatOwnerColumn(actor)+"=$2 AND status<>'closed'", id, actor.ID)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() == 0 {
		return nil, fault.New(fault.Conflict, "CHAT_CHANGED", "咨询已结束或已重新分配，请刷新")
	}
	return map[string]any{"ok": true}, nil
}

func (s *ChatStore) Presence(ctx context.Context, actor chat.Actor) (map[string]any, error) {
	var accepting, online bool
	var active, waiting int
	e := s.DB.QueryRow(ctx, `SELECT coalesce((SELECT accepting FROM chat_presence WHERE admin_id=$1),false),
 coalesce((SELECT last_seen>now()-interval '75 seconds' FROM chat_presence WHERE admin_id=$1),false),
 (SELECT count(*) FROM chat_conversations WHERE admin_id=$1 AND status='active'),(SELECT count(*) FROM chat_conversations WHERE status='waiting')`, actor.ID).Scan(&accepting, &online, &active, &waiting)
	if e != nil {
		return nil, e
	}
	return map[string]any{"data": map[string]any{"accepting": accepting, "online": online, "active": active, "capacity": chat.Capacity, "waiting": waiting}}, nil
}

func (s *ChatStore) UpdatePresence(ctx context.Context, actor chat.Actor, in chat.PresenceInput) error {
	_, e := s.DB.Exec(ctx, `INSERT INTO chat_presence(admin_id,session_hash,accepting,last_seen) VALUES($1,$2,coalesce($3,false),now())
   ON CONFLICT(admin_id) DO UPDATE SET session_hash=EXCLUDED.session_hash,accepting=coalesce($3,chat_presence.accepting),last_seen=now()`, actor.ID, actor.SessionHash, in.Accepting)
	if e != nil {
		return e
	}
	return nil
}

func (s *ChatStore) Dispatch(ctx context.Context) ([]string, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", chatAssignmentLock); e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT c.id,c.member_id,c.admin_id FROM chat_conversations c WHERE c.status='active' AND NOT EXISTS
 (SELECT 1 FROM chat_presence p JOIN users u ON u.id=p.admin_id JOIN sessions s ON s.token_hash=p.session_hash WHERE p.admin_id=c.admin_id AND p.last_seen>now()-interval '75 seconds' AND u.enabled AND u.role='admin' AND s.audience='admin' AND s.expires_at>now()) FOR UPDATE OF c`)
	if e != nil {
		return nil, e
	}
	type expired struct{ id, member, admin string }
	var expiredRows []expired
	for rows.Next() {
		var item expired
		if e = rows.Scan(&item.id, &item.member, &item.admin); e != nil {
			rows.Close()
			return nil, e
		}
		expiredRows = append(expiredRows, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	notify := []string{}
	for _, item := range expiredRows {
		if _, e = tx.Exec(ctx, "UPDATE chat_conversations SET admin_id=NULL,status='waiting',admin_read_id=0,updated_at=now() WHERE id=$1", item.id); e != nil {
			return nil, e
		}
		notify = append(notify, item.member, item.admin)
	}
	rows, e = tx.Query(ctx, "SELECT id,member_id FROM chat_conversations WHERE status='waiting' ORDER BY created_at,id LIMIT 50 FOR UPDATE")
	if e != nil {
		return nil, e
	}
	var waiting [][2]string
	for rows.Next() {
		var item [2]string
		if e = rows.Scan(&item[0], &item[1]); e != nil {
			rows.Close()
			return nil, e
		}
		waiting = append(waiting, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, item := range waiting {
		var admin string
		e = tx.QueryRow(ctx, `SELECT p.admin_id FROM chat_presence p JOIN users u ON u.id=p.admin_id JOIN sessions s ON s.token_hash=p.session_hash
   WHERE p.accepting AND p.last_seen>now()-interval '75 seconds' AND u.enabled AND u.role='admin' AND s.audience='admin' AND s.expires_at>now()
   AND (SELECT count(*) FROM chat_conversations c WHERE c.admin_id=p.admin_id AND c.status='active')<$1 ORDER BY random() LIMIT 1`, chat.Capacity).Scan(&admin)
		if errors.Is(e, pgx.ErrNoRows) {
			break
		}
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec(ctx, "UPDATE chat_conversations SET admin_id=$2,status='active',admin_read_id=0,updated_at=now() WHERE id=$1 AND status='waiting'", item[0], admin); e != nil {
			return nil, e
		}
		notify = append(notify, item[1], admin)
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return notify, nil
}

func (s *ChatStore) Participants(ctx context.Context, id string) ([]string, error) {
	var member, admin string
	err := s.DB.QueryRow(ctx, "SELECT member_id,coalesce(admin_id,'') FROM chat_conversations WHERE id=$1", id).Scan(&member, &admin)
	return []string{member, admin}, err
}
func (s *ChatStore) chatJSONRows(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var row map[string]any
		if err = json.Unmarshal(data, &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

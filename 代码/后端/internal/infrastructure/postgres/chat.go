// Package postgres 实现持久化；会话归属条件与事务锁必须随 SQL 一起保留。
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/chat"
	"qingye/backend/internal/domain/fault"
	"slices"
	"strconv"
	"time"
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
 'member_avatar',coalesce('/api/v1/media/'||m.avatar_media_id||'/file?variant=thumb',''),'admin_avatar',coalesce('/api/v1/media/'||ad.avatar_media_id||'/file?variant=thumb',''),
 'product_count',context_count.n,
 'last_message_mine',coalesce(last.sender_id=$1,false),
 'last_message',coalesce(CASE WHEN last.kind='product' THEN '咨询这个商品' WHEN last.kind='image' THEN '[图片] '||left(last.body,70) ELSE left(last.body,80) END,''),
 'last_message_id',coalesce(last.id::text,'0'),
 'unread',unread.n)`

// Last-message fields share one lookup; product history stays inside its partial index.
const chatSummaryJoins = ` JOIN users m ON m.id=c.member_id LEFT JOIN users ad ON ad.id=c.admin_id
 LEFT JOIN LATERAL (SELECT id,sender_id,kind,body FROM chat_messages x WHERE x.conversation_id=c.id ORDER BY id DESC LIMIT 1) last ON true
 CROSS JOIN LATERAL (SELECT count(*) AS n FROM (
   SELECT DISTINCT x.product_snapshot->>'id' AS product_id FROM chat_messages x
   WHERE x.conversation_id=c.id AND x.kind='product' AND x.product_snapshot->>'id' IS NOT NULL
 ) contexts) context_count`

func (s *ChatStore) Start(ctx context.Context, actor chat.Actor, in chat.StartInput, generatedID string) (string, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", chatAssignmentLock); e != nil {
		return "", e
	}
	var snapshot []byte
	var own bool
	// 每次发起都重新校验可见性；已有会话不能绕过商品下架权限。
	e = tx.QueryRow(ctx, `SELECT jsonb_build_object('id',p.id,'code',p.product_code,'title',p.title,'price',p.price_amount::text,'unit',p.price_unit,
	    'trade_type',p.trade_type,'rental_category',p.rental_category,'rental_quotes',p.rental_quotes,
	    'category_name',c.name,'server',coalesce((SELECT o.label FROM product_dictionary_options o WHERE o.dictionary_key='server' AND o.value=p.attributes->>'server'),p.attributes->>'server',''),
    'cover_url',coalesce((SELECT '/api/v1/media/'||i.media_id||'/file?variant=thumb' FROM product_images i JOIN media_files f ON f.id=i.media_id AND f.status='ready' WHERE i.product_id=p.id ORDER BY i.sort_order,i.media_id LIMIT 1),''))
	  ,coalesce(p.owner_id=$2,false)
    FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1 AND p.status='published' AND p.approved_version=p.version AND c.enabled FOR SHARE OF p,c`, in.ProductID, actor.ID).Scan(&snapshot, &own)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", fault.New(fault.NotFound, "PRODUCT_UNAVAILABLE", "商品暂不可咨询，请刷新商品页面")
	}
	if e != nil {
		return "", e
	}
	var id, status string
	e = tx.QueryRow(ctx, "SELECT id,status FROM chat_conversations WHERE member_id=$1 AND merged_into IS NULL FOR UPDATE", actor.ID).Scan(&id, &status)
	if errors.Is(e, pgx.ErrNoRows) {
		id = generatedID
		_, e = tx.Exec(ctx, "INSERT INTO chat_conversations(id,member_id,product_id,product_snapshot) VALUES($1,$2,$3,$4)", id, actor.ID, in.ProductID, snapshot)
	}
	if e != nil {
		return "", e
	}
	// 连续双击复用卡片；更换商品或再次展开已结束咨询会保留新的上下文。
	var repeated bool
	if e = tx.QueryRow(ctx, `SELECT coalesce((SELECT kind='product' AND product_snapshot->>'id'=$2 AND created_at>now()-interval '2 minutes'
 FROM chat_messages WHERE conversation_id=$1 ORDER BY id DESC LIMIT 1),false)`, id, in.ProductID).Scan(&repeated); e != nil {
		return "", e
	}
	if !repeated || status == "closed" {
		if _, e = tx.Exec(ctx, `INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,kind,product_snapshot)
 VALUES($1,$2,'member',$3,'product',$4)`, id, actor.ID, "context:"+generatedID, snapshot); e != nil {
			return "", e
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE chat_conversations SET product_id=$2,product_snapshot=$3,updated_at=now(),
 admin_id=CASE WHEN status='closed' THEN NULL ELSE admin_id END,
 admin_read_id=CASE WHEN status='closed' THEN 0 ELSE admin_read_id END,
 status=CASE WHEN status='closed' THEN 'waiting' ELSE status END WHERE id=$1`, id, in.ProductID, snapshot); e != nil {
		return "", e
	}
	if !own {
		// 按商品和真实会员去重；首次咨询时间不因重复点击刷新热门权重。
		if _, e = tx.Exec(ctx, `INSERT INTO product_consultations(product_id,member_id) VALUES($1,$2)
 ON CONFLICT(product_id,member_id) DO UPDATE SET last_consulted_at=now()`, in.ProductID, actor.ID); e != nil {
			return "", e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return "", e
	}
	return id, nil
}

func (s *ChatStore) List(ctx context.Context, actor chat.Actor, page chat.ConversationPage) (map[string]any, error) {
	const limit = 50
	where := "c." + chatOwnerColumn(actor) + "=$1 AND c.merged_into IS NULL"
	args := []any{actor.ID, page.Offset}
	boundary := ""
	position := page.Offset
	if page.Cursor != nil {
		boundary = " AND (c.updated_at,c.id)<($3,$4)"
		args = append(args, page.Cursor.UpdatedAt, page.Cursor.ID)
		position = page.Cursor.Position
	}
	// Page before projecting summaries. Total, badges and rows share one MVCC snapshot,
	// and each conversation's unread range is counted once (also reused by the page).
	sql := `WITH owned AS MATERIALIZED (
 SELECT c.id,c.` + chatReadColumn(actor) + ` AS read_id FROM chat_conversations c WHERE ` + where + `
), unread_counts AS MATERIALIZED (
 SELECT c.id,u.n FROM owned c CROSS JOIN LATERAL (
 SELECT count(*) AS n FROM chat_messages x WHERE x.conversation_id=c.id AND x.id>c.read_id AND x.sender_id<>$1) u
), page AS MATERIALIZED (
 SELECT c.* FROM chat_conversations c WHERE ` + where + boundary + ` ORDER BY c.updated_at DESC,c.id DESC LIMIT 51 OFFSET $2
), visible AS MATERIALIZED (SELECT * FROM page ORDER BY updated_at DESC,id DESC LIMIT 50)
 SELECT jsonb_build_object('total',(SELECT count(*) FROM owned),
 'unread',coalesce((SELECT sum(n) FROM unread_counts),0),
 '_has_more',(SELECT count(*)>50 FROM page),
 'data',coalesce((SELECT jsonb_agg(result.data ORDER BY result.at DESC,result.id DESC) FROM (
 SELECT ` + chatProjection + ` AS data,c.updated_at AS at,c.id FROM visible c
 JOIN unread_counts unread ON unread.id=c.id ` + chatSummaryJoins + `
 ) result),'[]'::jsonb))`
	rows, e := s.chatJSONRows(ctx, sql, args...)
	if e != nil {
		return nil, e
	}
	out := rows[0]
	data := out["data"].([]any)
	out["next_offset"], out["next_cursor"] = nil, nil
	if out["_has_more"] == true && len(data) == limit {
		last := data[len(data)-1].(map[string]any)
		at, err := time.Parse(time.RFC3339Nano, last["updated_at"].(string))
		if err != nil {
			return nil, err
		}
		out["next_offset"] = position + len(data)
		out["next_cursor"] = chat.EncodeConversationCursor(chat.ConversationCursor{UpdatedAt: at, ID: last["id"].(string), Position: position + len(data)})
	}
	delete(out, "_has_more")
	return out, nil
}

func (s *ChatStore) Detail(ctx context.Context, actor chat.Actor, id string) (map[string]any, error) {
	id, err := s.resolveConversation(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	readColumn, peerReadColumn := chatReadColumn(actor), "staff_read_id"
	if actor.Surface == "admin" {
		peerReadColumn = "member_read_id"
	}
	sql := "SELECT " + chatProjection + " || jsonb_build_object('read_through',c." + readColumn + "::text,'peer_read_through',c." + peerReadColumn + "::text) FROM chat_conversations c " + chatSummaryJoins +
		" CROSS JOIN LATERAL (SELECT count(*) AS n FROM chat_messages x WHERE x.conversation_id=c.id AND x.sender_id<>$1 AND x.id>c." + readColumn + ") unread WHERE c." + chatOwnerColumn(actor) + "=$1 AND c.id=$2"
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
	id, err := s.resolveConversation(ctx, actor, id)
	if err != nil {
		return nil, err
	}
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
	rows, e := s.chatJSONRows(ctx, `SELECT jsonb_build_object('id',x.id::text,'body',x.body,'kind',x.kind,'product',x.product_snapshot,'sender_role',x.sender_role,'mine',x.sender_id=$2,'created_at',x.created_at,'client_id',CASE WHEN x.sender_id=$2 THEN x.client_id ELSE '' END,
 'sender_name',u.nickname,'avatar_url',coalesce('/api/v1/media/'||u.avatar_media_id||'/file?variant=thumb',''),
 'reply',CASE WHEN q.id IS NOT NULL THEN jsonb_build_object('id',q.id::text,'kind',q.kind,'sender_name',qu.nickname,'sender_role',q.sender_role,'mine',q.sender_id=$2,
 'excerpt',left(CASE WHEN q.kind='product' THEN coalesce(q.product_snapshot->>'title','') ELSE q.body END,120)) END,
 'images',coalesce((SELECT jsonb_agg(jsonb_build_object('id',a.media_id,'url','/api/v1/media/'||a.media_id||'/file','thumbnail_url','/api/v1/media/'||a.media_id||'/file?variant=thumb') ORDER BY a.position) FROM chat_message_images a WHERE a.message_id=x.id),'[]'::jsonb))
 FROM chat_messages x JOIN users u ON u.id=x.sender_id JOIN chat_conversations c ON c.id=x.conversation_id
 LEFT JOIN chat_messages q ON q.id=x.reply_to_id AND q.conversation_id=x.conversation_id LEFT JOIN users qu ON qu.id=q.sender_id
 WHERE c.id=$1 AND c.`+chatOwnerColumn(actor)+`=$2 AND `+predicate+` ORDER BY x.id `+order+` LIMIT 51`, id, actor.ID, cursor)
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
	id, err := s.resolveConversation(ctx, actor, id)
	if err != nil {
		return nil, err
	}
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
	var oldBody, oldReply string
	e = tx.QueryRow(ctx, "SELECT id,body,coalesce(reply_to_id::text,'') FROM chat_messages WHERE conversation_id=$1 AND sender_id=$2 AND client_id=$3", id, actor.ID, in.ClientID).Scan(&messageID, &oldBody, &oldReply)
	if e == nil {
		var oldImages []string
		if e = tx.QueryRow(ctx, `SELECT coalesce(array_agg(media_id ORDER BY position),'{}') FROM chat_message_images WHERE message_id=$1`, messageID).Scan(&oldImages); e != nil {
			return nil, e
		}
		if oldBody != in.Body || oldReply != in.ReplyToID || !slices.Equal(oldImages, in.ImageIDs) {
			return nil, fault.New(fault.Conflict, "MESSAGE_REPLAY_CONFLICT", "重试标识已用于另一条消息")
		}
	} else if errors.Is(e, pgx.ErrNoRows) {
		if status == "closed" {
			return nil, fault.New(fault.Conflict, "CHAT_CLOSED", "咨询已结束，请从商品页重新发起")
		}
		if in.ReplyToID != "" {
			var belongs bool
			if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM chat_messages WHERE conversation_id=$1 AND id=$2::text::bigint)", id, in.ReplyToID).Scan(&belongs); e != nil {
				return nil, e
			}
			if !belongs {
				return nil, fault.New(fault.Invalid, "INVALID_REPLY", "引用的消息不存在或不在当前会话")
			}
		}
		kind := "text"
		if len(in.ImageIDs) > 0 {
			kind = "image"
			// Lock files in a stable order against orphan cleanup; only this sender's
			// ready uploads in this conversation can become message attachments.
			rows, err := tx.Query(ctx, `SELECT m.id FROM media_files m WHERE m.id=ANY($1::text[]) AND m.owner_id=$2 AND m.purpose='chat' AND m.conversation_id=$3 AND m.status='ready' AND m.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM chat_message_images a WHERE a.media_id=m.id) ORDER BY m.id FOR UPDATE OF m`, in.ImageIDs, actor.ID, id)
			if err != nil {
				return nil, err
			}
			count := 0
			for rows.Next() {
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			if count != len(in.ImageIDs) {
				return nil, fault.New(fault.Invalid, "INVALID_IMAGES", "图片未上传完成、已发送或不属于本次咨询，请重新选择")
			}
		}
		if e = tx.QueryRow(ctx, "INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body,kind,reply_to_id) VALUES($1,$2,$3,$4,$5,$6,nullif($7,'')::bigint) RETURNING id", id, actor.ID, actor.Role, in.ClientID, in.Body, kind, in.ReplyToID).Scan(&messageID); e != nil {
			return nil, e
		}
		for position, mid := range in.ImageIDs {
			if _, e = tx.Exec(ctx, `INSERT INTO chat_message_images(message_id,media_id,position) VALUES($1,$2,$3)`, messageID, mid, position); e != nil {
				return nil, e
			}
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

func (s *ChatStore) Read(ctx context.Context, actor chat.Actor, id string, through int64) (chat.ReadResult, error) {
	var result chat.ReadResult
	id, err := s.resolveConversation(ctx, actor, id)
	if err != nil {
		return result, err
	}
	column := chatReadColumn(actor)
	owner := `c.id=$1 AND c.` + chatOwnerColumn(actor) + `=$2`
	assign := column + `=target.id`
	if actor.Surface == "admin" {
		assign += `,staff_read_id=GREATEST(c.staff_read_id,target.id)`
	}
	// 向前推进时才写入并通知；会话排序仍按最后一次业务活动。
	var current *int64
	e := s.DB.QueryRow(ctx, `WITH target AS (
 SELECT coalesce((SELECT id FROM chat_messages WHERE conversation_id=$1 AND id<=$3 ORDER BY id DESC LIMIT 1),0) AS id
), advanced AS (
 UPDATE chat_conversations c SET `+assign+` FROM target WHERE `+owner+` AND c.`+column+`<target.id RETURNING c.`+column+` AS id
)
SELECT coalesce((SELECT id FROM advanced),(SELECT c.`+column+` FROM chat_conversations c WHERE `+owner+`)),EXISTS(SELECT 1 FROM advanced)`, id, actor.ID, through).Scan(&current, &result.Advanced)
	if e != nil {
		return result, e
	}
	if current == nil {
		return result, fault.New(fault.NotFound, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
	}
	result.Through = *current
	return result, nil
}

func (s *ChatStore) Close(ctx context.Context, actor chat.Actor, id string) (map[string]any, error) {
	id, err := s.resolveConversation(ctx, actor, id)
	if err != nil {
		// 保留既有关闭接口的冲突语义，不暴露会话归属。
		var known *fault.Error
		if errors.As(err, &known) {
			return nil, fault.New(fault.Conflict, "CHAT_CHANGED", "咨询已结束或已重新分配，请刷新")
		}
		return nil, err
	}
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
	err := s.DB.QueryRow(ctx, `SELECT c.member_id,coalesce(c.admin_id,'') FROM chat_conversations old
 JOIN chat_conversations c ON c.id=coalesce(old.merged_into,old.id) WHERE old.id=$1`, id).Scan(&member, &admin)
	return []string{member, admin}, err
}

// 历史链接转到合并后的会话，始终按当前归属鉴权，原客服不能借旧链接绕过重新分配。
func (s *ChatStore) resolveConversation(ctx context.Context, actor chat.Actor, id string) (string, error) {
	var canonical string
	err := s.DB.QueryRow(ctx, `SELECT c.id FROM chat_conversations old
 JOIN chat_conversations c ON c.id=coalesce(old.merged_into,old.id)
 WHERE old.id=$1 AND c.`+chatOwnerColumn(actor)+`=$2`, id, actor.ID).Scan(&canonical)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fault.New(fault.NotFound, "CHAT_NOT_FOUND", "会话不存在或不属于当前账户")
	}
	return canonical, err
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

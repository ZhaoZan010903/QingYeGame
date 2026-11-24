// Package chat 统一聊天业务，HTTP、WebSocket 和 WebTransport 共用同一规则。
package chat

import (
	"context"
	"errors"
	domain "qingye/backend/internal/domain/chat"
	"qingye/backend/internal/domain/fault"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Repository 的写方法是完整事务，禁止将锁、去重和状态更新拆成独立提交。
type Repository interface {
	Start(context.Context, domain.Actor, domain.StartInput, string) (string, error)
	List(context.Context, domain.Actor, domain.ConversationPage) (map[string]any, error)
	Detail(context.Context, domain.Actor, string) (map[string]any, error)
	Messages(context.Context, domain.Actor, string, domain.MessagePage) (map[string]any, error)
	Send(context.Context, domain.Actor, string, domain.SendInput) (map[string]any, error)
	Read(context.Context, domain.Actor, string, int64) (domain.ReadResult, error)
	Close(context.Context, domain.Actor, string) (map[string]any, error)
	Presence(context.Context, domain.Actor) (map[string]any, error)
	UpdatePresence(context.Context, domain.Actor, domain.PresenceInput) error
	Participants(context.Context, string) ([]string, error)
	Dispatch(context.Context) ([]string, error)
	Profile(context.Context, domain.Actor, *domain.ProfileInput) (map[string]any, error)
}
type Limiter interface {
	Allow(context.Context, string, int, time.Duration) error
}
type Notifier interface {
	Notify(context.Context, ...string)
}
type Service struct {
	repo     Repository
	limiter  Limiter
	notifier Notifier
	surface  string
	newID    func() string
}

func New(repo Repository, limiter Limiter, notifier Notifier, surface string, newID func() string) *Service {
	return &Service{repo: repo, limiter: limiter, notifier: notifier, surface: surface, newID: newID}
}
func (s *Service) authorize(actor domain.Actor) error {
	role := "member"
	if s.surface == "admin" {
		role = "admin"
	}
	if (s.surface != "public" && s.surface != "admin") || actor.Surface != s.surface || actor.Role != role || !domain.ValidID(actor.ID, 100) || actor.SessionHash == "" {
		return fault.New(fault.Unauthenticated, "UNAUTHENTICATED", "请先登录")
	}
	return nil
}
func conversation(id string) error {
	if !domain.ValidID(id, 100) {
		return fault.New(fault.Invalid, "INVALID_CONVERSATION", "会话编号无效")
	}
	return nil
}
func databaseError(err error) error {
	if err == nil {
		return nil
	}
	var known *fault.Error
	if errors.As(err, &known) {
		return err
	}
	return fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试", err)
}
func (s *Service) allow(ctx context.Context, key string, n int) error {
	if s.limiter == nil {
		return fault.New(fault.Unavailable, "RATE_LIMIT_UNAVAILABLE", "请求限流服务暂不可用")
	}
	return s.limiter.Allow(ctx, key, n, time.Minute)
}
func (s *Service) notifyConversation(ctx context.Context, id string) {
	// 消息提交后通知失败不回滚，客户端重试仍使用原幂等键。
	if s.notifier == nil {
		return
	}
	if ids, err := s.repo.Participants(ctx, id); err == nil {
		s.notifier.Notify(ctx, ids...)
	}
}
func (s *Service) Dispatch(ctx context.Context) error {
	ids, err := s.repo.Dispatch(ctx)
	if err == nil && s.notifier != nil {
		s.notifier.Notify(ctx, ids...)
	}
	return err
}
func (s *Service) LimitCommand(ctx context.Context, actor domain.Actor) error {
	if err := s.authorize(actor); err != nil {
		return err
	}
	return s.allow(ctx, "chat-command:"+actor.SessionHash, 360)
}
func (s *Service) Start(ctx context.Context, actor domain.Actor, in domain.StartInput) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if actor.Surface != "public" {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "需要会员身份")
	}
	if !domain.ValidID(in.ProductID, 100) {
		return nil, fault.New(fault.Invalid, "INVALID_PRODUCT", "请选择要咨询的商品")
	}
	if err := s.allow(ctx, "chat-start:"+actor.ID, 12); err != nil {
		return nil, err
	}
	id, err := s.repo.Start(ctx, actor, in, s.newID())
	if err != nil {
		return nil, databaseError(err)
	}
	_ = s.Dispatch(ctx)
	s.notifyConversation(ctx, id)
	return map[string]any{"data": map[string]any{"id": id}}, nil
}
func (s *Service) List(ctx context.Context, actor domain.Actor, offset int, cursors ...string) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if offset < 0 || offset > 100000 {
		return nil, fault.New(fault.Invalid, "INVALID_CURSOR", "会话分页位置无效")
	}
	page := domain.ConversationPage{Offset: offset}
	if len(cursors) > 0 && cursors[0] != "" {
		var err error
		page.Cursor, err = domain.DecodeConversationCursor(cursors[0])
		if err != nil || offset != 0 {
			return nil, fault.New(fault.Invalid, "INVALID_CURSOR", "会话分页位置无效")
		}
	}
	out, err := s.repo.List(ctx, actor, page)
	return out, databaseError(err)
}
func (s *Service) Detail(ctx context.Context, actor domain.Actor, id string) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if err := conversation(id); err != nil {
		return nil, err
	}
	out, err := s.repo.Detail(ctx, actor, id)
	return out, databaseError(err)
}
func (s *Service) Messages(ctx context.Context, actor domain.Actor, id, before, after string, forward bool) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if err := conversation(id); err != nil {
		return nil, err
	}
	page := domain.MessagePage{Before: 9223372036854775807, Forward: forward}
	var err error
	invalid := func() (map[string]any, error) {
		return nil, fault.New(fault.Invalid, "INVALID_CURSOR", "消息分页位置无效")
	}
	if before != "" {
		page.Before, err = strconv.ParseInt(before, 10, 64)
		if err != nil || page.Before < 1 {
			return invalid()
		}
	}
	if after != "" {
		page.After, err = strconv.ParseInt(after, 10, 64)
		if err != nil || page.After < 0 || before != "" {
			return invalid()
		}
	}
	out, err := s.repo.Messages(ctx, actor, id, page)
	return out, databaseError(err)
}
func (s *Service) Send(ctx context.Context, actor domain.Actor, id string, in domain.SendInput) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if err := conversation(id); err != nil {
		return nil, err
	}
	in.Body = strings.TrimSpace(in.Body)
	if (in.Body == "" && len(in.ImageIDs) == 0) || len(in.ImageIDs) > 9 || utf8.RuneCountInString(in.Body) > 2000 || !utf8.ValidString(in.Body) || !domain.ValidID(in.ClientID, 80) || strings.ContainsRune(in.Body, 0) {
		return nil, fault.New(fault.Invalid, "INVALID_MESSAGE", "请填写文字或添加图片，文字最多2000字、图片最多9张")
	}
	seen := map[string]bool{}
	for _, mid := range in.ImageIDs {
		if !domain.ValidID(mid, 100) || seen[mid] {
			return nil, fault.New(fault.Invalid, "INVALID_IMAGES", "每条消息最多9张图片，不能重复添加")
		}
		seen[mid] = true
	}
	if in.ReplyToID != "" {
		id, err := strconv.ParseInt(in.ReplyToID, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != in.ReplyToID {
			return nil, fault.New(fault.Invalid, "INVALID_REPLY", "引用的消息无效")
		}
	}
	if err := s.allow(ctx, "chat-send:"+actor.ID, 60); err != nil {
		return nil, err
	}
	out, err := s.repo.Send(ctx, actor, id, in)
	if err != nil {
		return nil, databaseError(err)
	}
	s.notifyConversation(ctx, id)
	return out, nil
}
func (s *Service) Profile(ctx context.Context, actor domain.Actor, in *domain.ProfileInput) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if in != nil {
		if in.AvatarID == nil || (*in.AvatarID != "" && !domain.ValidID(*in.AvatarID, 100)) {
			return nil, fault.New(fault.Invalid, "INVALID_AVATAR", "请选择头像")
		}
		if err := s.allow(ctx, "chat-profile:"+actor.ID, 20); err != nil {
			return nil, err
		}
	}
	out, err := s.repo.Profile(ctx, actor, in)
	return out, databaseError(err)
}
func (s *Service) Read(ctx context.Context, actor domain.Actor, id string, in domain.ReadInput) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if err := conversation(id); err != nil {
		return nil, err
	}
	if err := s.allow(ctx, "chat-read:"+actor.ID, 240); err != nil {
		return nil, err
	}
	through, err := strconv.ParseInt(in.Through, 10, 64)
	if err != nil || through < 1 {
		return nil, fault.New(fault.Invalid, "INVALID_MESSAGE", "已读位置无效")
	}
	result, err := s.repo.Read(ctx, actor, id, through)
	if err != nil {
		return nil, databaseError(err)
	}
	if result.Advanced {
		s.notifyConversation(ctx, id)
	}
	return map[string]any{"ok": true, "read_through": strconv.FormatInt(result.Through, 10)}, nil
}
func (s *Service) Close(ctx context.Context, actor domain.Actor, id string) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if err := conversation(id); err != nil {
		return nil, err
	}
	out, err := s.repo.Close(ctx, actor, id)
	if err != nil {
		return nil, databaseError(err)
	}
	s.notifyConversation(ctx, id)
	_ = s.Dispatch(ctx)
	return out, nil
}
func (s *Service) Presence(ctx context.Context, actor domain.Actor, in *domain.PresenceInput) (map[string]any, error) {
	if err := s.authorize(actor); err != nil {
		return nil, err
	}
	if actor.Surface != "admin" {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "需要管理员权限")
	}
	if in != nil {
		if err := s.allow(ctx, "chat-presence:"+actor.ID, 20); err != nil {
			return nil, err
		}
		if err := s.repo.UpdatePresence(ctx, actor, *in); err != nil {
			return nil, databaseError(err)
		}
		_ = s.Dispatch(ctx)
	}
	out, err := s.repo.Presence(ctx, actor)
	return out, databaseError(err)
}

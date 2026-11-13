package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	domain "qingye/backend/internal/domain/security"
	"strings"
)

type HumanVerifier interface {
	Verify(context.Context, string, string, string, string, string) (bool, error)
}
type HumanChallengeStore interface {
	CreateChallenge(context.Context, domain.ReadSubject, string, string, string) (domain.HumanChallenge, error)
	ClaimChallenge(context.Context, domain.ReadSubject, string, string, string) (domain.HumanChallenge, error)
	CompleteChallenge(context.Context, domain.ReadSubject, domain.HumanChallenge, bool, bool) error
}
type HumanService struct {
	Store    HumanChallengeStore
	Verifier HumanVerifier
	NewID    func() string
}

func (s *HumanService) Create(ctx context.Context, who domain.ReadSubject, hostname string) (domain.HumanChallenge, error) {
	return s.Store.CreateChallenge(ctx, who, s.NewID(), domain.MediaReadAction, hostname)
}
func (s *HumanService) Verify(ctx context.Context, who domain.ReadSubject, id, token, ip string) error {
	if len(id) != 36 || len(token) == 0 || len(token) > 2048 || strings.TrimSpace(token) != token {
		return domain.ErrChallenge
	}
	sum := sha256.Sum256([]byte(token))
	c, err := s.Store.ClaimChallenge(ctx, who, id, hex.EncodeToString(sum[:]), s.NewID())
	if err != nil {
		return err
	}
	ok, verifyErr := s.Verifier.Verify(ctx, token, c.Action, c.Hostname, c.ID, ip)
	// 网络重试保留同一挑战和供应商幂等键；失败结果不能提升访问权限。
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err = s.Store.CompleteChallenge(finishCtx, who, c, ok, verifyErr != nil); err != nil {
		return err
	}
	if verifyErr != nil {
		return domain.ErrUnavailable
	}
	if !ok {
		return domain.ErrChallenge
	}
	return nil
}

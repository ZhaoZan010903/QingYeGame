package registration

import (
	"context"
	"encoding/json"
	domain "qingye/backend/internal/domain/registration"
	"time"
)

func (s *Service) provider(purpose string) Provider {
	if purpose == "login" {
		return s.LoginProvider
	}
	if purpose == "register" {
		return s.Provider
	}
	return nil
}

func (s *Service) loginFingerprint(in domain.Input) string {
	raw, _ := json.Marshal(struct{ Purpose, Phone, Code, ChallengeID string }{"login", in.Phone, in.Code, in.ChallengeID})
	return s.MAC(string(raw))
}

// Account existence is resolved only after proof of phone ownership.
func (s *Service) Login(ctx context.Context, who domain.Subject, in domain.Input) (Session, bool, error) {
	in.Purpose = "login"
	if err := domain.ValidateCode(in); err != nil {
		return Session{}, false, err
	}
	if err := s.Limits.Check(ctx, who); err != nil {
		return Session{}, false, err
	}
	fingerprint := s.loginFingerprint(in)
	if err := s.check(ctx, who, in, fingerprint); err != nil {
		return Session{}, false, err
	}
	token := s.NewToken()
	expires := time.Now().Add(7 * 24 * time.Hour)
	user, pending, err := s.Store.Login(ctx, in, who.Binding, fingerprint, s.Hash(token), expires)
	if err != nil || pending {
		return Session{}, pending, err
	}
	return Session{User: user, Token: token, Expires: expires}, false, nil
}

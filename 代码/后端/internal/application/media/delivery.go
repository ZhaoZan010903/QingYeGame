package media

import (
	"context"
	"time"

	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/media"
)

// PublishedSigner signs only an exact, verified published object mapping.
// A false result means this resource uses controlled local/staging delivery.
type PublishedSigner interface {
	SignPublished(context.Context, domain.Upload, time.Duration) (string, bool, error)
}

func (s *Service) CheckProductVersion(ctx context.Context, mediaID, productID string, version int64) error {
	if productID == "" && version == 0 {
		return nil
	}
	if validID(productID) != nil || version < 1 {
		return fault.New(fault.Invalid, "INVALID_MEDIA_VERSION", "商品版本无效")
	}
	checker, ok := s.repo.(interface {
		CheckProductVersion(context.Context, string, string, int64) error
	})
	if !ok {
		return fault.New(fault.Unavailable, "MEDIA_UNAVAILABLE", "图片暂不可用")
	}
	return checker.CheckProductVersion(ctx, mediaID, productID, version)
}

func (s *Service) SignPublished(ctx context.Context, u domain.Upload, ttl time.Duration) (string, bool, error) {
	if !u.Public || !u.PublishedProduct || u.Purpose != "product" {
		return "", false, nil
	}
	signer, ok := s.files.(PublishedSigner)
	if !ok {
		return "", false, nil
	}
	return signer.SignPublished(ctx, u, ttl)
}

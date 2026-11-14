package mediaobject

import (
	"context"
	"strings"
	"time"

	"qingye/backend/internal/domain/media"
)

// Signing never fetches image bytes. Public eligibility comes from the current
// media permission query, while this adapter validates the exact object map.
func (s *Files) SignPublished(ctx context.Context, u media.Upload, ttl time.Duration) (string, bool, error) {
	if !u.Public || !u.PublishedProduct || u.Purpose != "product" {
		return "", false, nil
	}
	if ttl != 5*time.Second {
		return "", true, ErrIntegrity
	}
	if !strings.HasPrefix(u.Key, "r20/") {
		if s.Legacy == nil {
			return "", false, nil
		}
		o, found, err := s.Legacy.Repository.ReadLegacyReplica(ctx, u.ID, u.Key)
		if err != nil || !found {
			return "", found, err
		}
		if o.MediaID != u.ID || o.StorageKey != u.Key || o.MIME != u.MIME || o.Size != u.Size || !validLegacyReplica(o) {
			return "", true, ErrIntegrity
		}
		signer, ok := s.Legacy.Store.(interface {
			SignReplica(context.Context, media.LegacyReplica, time.Duration) (string, error)
		})
		if !ok {
			return "", false, nil
		}
		url, err := signer.SignReplica(ctx, o, ttl)
		return url, true, err
	}
	o, err := s.Repository.ReadObject(ctx, u.ID, "original")
	if err != nil {
		return "", true, err
	}
	if o.Store != "oss" {
		return "", false, nil
	}
	if o.MediaID != u.ID || o.Key != u.Key || o.Size != u.Size || o.MIME != u.MIME || !validDigest(o.Size, o.SHA256) {
		return "", true, ErrIntegrity
	}
	url, err := s.Published.SignedURL(ctx, o.Key, ttl)
	return url, true, err
}

func (d *LegacyReader) SignReplica(ctx context.Context, o media.LegacyReplica, ttl time.Duration) (string, error) {
	if ttl != 5*time.Second || !validLegacyReplica(o) || o.ObjectKey != d.prefix+o.StorageKey || o.Bucket != d.backend.bucket ||
		(d.legacyOnly && !strings.HasPrefix(o.StorageKey, "legacy/")) {
		return "", ErrIntegrity
	}
	url, err := d.backend.sign(ctx, o.ObjectKey, ttl)
	if err != nil {
		return "", safe(ctx, err)
	}
	return url, nil
}

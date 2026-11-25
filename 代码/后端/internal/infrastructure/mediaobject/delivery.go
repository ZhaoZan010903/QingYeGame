package mediaobject

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"qingye/backend/internal/domain/media"
)

// The processing recipe is server-owned and included in the OSS signature.
// Animated GIFs support resizing but not the same output-format pipeline.
func imageRepresentation(variant, mime string, size int64) (process, outputMIME string) {
	const resize = "image/resize,m_lfit,w_720,h_720,limit_1"
	if variant == "original" || size > 20<<20 { // OSS basic image processing source limit
		return "", mime
	}
	if mime == "image/gif" {
		return resize, mime
	}
	return resize + "/format,webp", "image/webp"
}

// Every caller has already resolved current product visibility. Only verified
// original object mappings may become inputs to this exact OSS representation.
func (s *Files) SignPublished(ctx context.Context, u media.Upload, variant string, ttl time.Duration) (media.SignedImage, bool, error) {
	var image media.SignedImage
	if !u.Public || !u.PublishedProduct || u.Purpose != "product" {
		return image, false, nil
	}
	if ttl != 5*time.Second || variant != "original" && variant != "thumb" {
		return image, true, ErrIntegrity
	}
	process, mime := imageRepresentation(variant, u.MIME, u.Size)
	var key, sha, target string
	var err error
	if !strings.HasPrefix(u.Key, "r20/") {
		if s.Legacy == nil {
			return image, false, nil
		}
		o, found, e := s.Legacy.Repository.ReadLegacyReplica(ctx, u.ID, u.Key)
		if e != nil || !found {
			return image, found, e
		}
		if o.MediaID != u.ID || o.StorageKey != u.Key || o.MIME != u.MIME || o.Size != u.Size || !validLegacyReplica(o) {
			return image, true, ErrIntegrity
		}
		signer, ok := s.Legacy.Store.(interface {
			SignReplica(context.Context, media.LegacyReplica, string, time.Duration) (string, error)
		})
		if !ok {
			return image, false, nil
		}
		key, sha = o.ObjectKey, o.SHA256
		target, err = signer.SignReplica(ctx, o, variant, ttl)
	} else {
		o, e := s.Repository.ReadObject(ctx, u.ID, "original")
		if e != nil {
			return image, true, e
		}
		if o.Store != "oss" {
			return image, false, nil
		}
		if o.MediaID != u.ID || o.Key != u.Key || o.Size != u.Size || o.MIME != u.MIME || !validDigest(o.Size, o.SHA256) {
			return image, true, ErrIntegrity
		}
		signer, ok := s.Published.(interface {
			signedImageURL(context.Context, string, string, time.Duration) (string, error)
		})
		if !ok {
			return image, false, nil
		}
		key, sha = o.Key, o.SHA256
		target, err = signer.signedImageURL(ctx, key, process, ttl)
	}
	if err != nil {
		return image, true, err
	}
	version := sha256.Sum256([]byte(strings.Join([]string{"public-image-v1", u.ID, key, sha, variant, process, mime}, "\x00")))
	image = media.SignedImage{URL: target, MIME: mime, CacheKey: hex.EncodeToString(version[:])}
	return image, true, nil
}

func (d *LegacyReader) SignReplica(ctx context.Context, o media.LegacyReplica, variant string, ttl time.Duration) (string, error) {
	if ttl != 5*time.Second || variant != "original" && variant != "thumb" || !validLegacyReplica(o) ||
		o.ObjectKey != d.prefix+o.StorageKey || o.Bucket != d.backend.bucket || (d.legacyOnly && !strings.HasPrefix(o.StorageKey, "legacy/")) {
		return "", ErrIntegrity
	}
	process, _ := imageRepresentation(variant, o.MIME, o.Size)
	url, err := d.backend.sign(ctx, o.ObjectKey, process, ttl)
	if err != nil {
		return "", safe(ctx, err)
	}
	return url, nil
}

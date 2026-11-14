package mediafiles

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"qingye/backend/internal/domain/media"
	"time"

	imagedraw "golang.org/x/image/draw"
)

// 固定缩略图规格，限制派生文件和解码内存。
const thumbnailEdge = media.ThumbnailEdge

func ThumbnailDimensions(width, height int) (int, int) {
	if width <= thumbnailEdge && height <= thumbnailEdge {
		return width, height
	}
	if width >= height {
		return thumbnailEdge, max(1, height*thumbnailEdge/width)
	}
	return max(1, width*thumbnailEdge/height), thumbnailEdge
}

// 原图可读权限必须在调用前校验，缓存命中不绕过权限。
func (s *Store) Thumbnail(ctx context.Context, source, id string) (string, bool) {
	info, err := os.Stat(source)
	if err != nil {
		return "", false
	}
	cacheKey := fmt.Sprintf("%s:%s:%d:%d:v1", id, source, info.Size(), info.ModTime().UnixNano())
	target, err := Path(s.Root, "derived/"+hash(cacheKey)+".jpg")
	if err != nil {
		return "", false
	}
	if cached, e := os.Stat(target); e == nil && cached.Size() > 0 {
		return target, true
	}
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	select {
	case ImageProcessors <- struct{}{}:
		defer func() { <-ImageProcessors }()
	case <-ctx.Done():
		return "", false
	case <-timeout.C:
		return "", false
	}
	// 等待解码槽期间，其他请求可能已生成此缩略图。
	if cached, e := os.Stat(target); e == nil && cached.Size() > 0 {
		return target, true
	}
	src, err := os.Open(source)
	if err != nil {
		return "", false
	}
	defer src.Close()
	cfg, format, err := image.DecodeConfig(src)
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 16000 || cfg.Height > 16000 || int64(cfg.Width)*int64(cfg.Height) > media.MaxImagePixels {
		return "", false
	}
	if format != "jpeg" && format != "png" && format != "webp" && format != "gif" {
		return "", false
	}
	if _, err = src.Seek(0, 0); err != nil {
		return "", false
	}
	original, _, err := image.Decode(src)
	if err != nil {
		return "", false
	}
	if ctx.Err() != nil {
		return "", false
	}
	width, height := ThumbnailDimensions(cfg.Width, cfg.Height)
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(scaled, scaled.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	imagedraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), original, original.Bounds(), draw.Over, nil)
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return "", false
	}
	out, err := os.CreateTemp(filepath.Dir(target), ".thumb-*")
	if err != nil {
		return "", false
	}
	temp := out.Name()
	defer os.Remove(temp)
	err = jpeg.Encode(out, scaled, &jpeg.Options{Quality: 80})
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		return "", false
	}
	commit := func() error { return os.Rename(temp, target) }
	if s.ThumbnailCommit != nil {
		err = s.ThumbnailCommit(ctx, id, commit)
	} else {
		err = commit()
	}
	if err != nil {
		return "", false
	}
	return target, true
}

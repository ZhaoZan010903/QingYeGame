package app

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"time"

	imagedraw "golang.org/x/image/draw"
)

// A single fixed-size variant keeps derived storage bounded and prevents
// arbitrary resize requests from multiplying memory and disk consumption.
const thumbnailEdge = 720

func thumbnailDimensions(width, height int) (int, int) {
	if width <= thumbnailEdge && height <= thumbnailEdge {
		return width, height
	}
	if width >= height {
		return thumbnailEdge, max(1, height*thumbnailEdge/width)
	}
	return max(1, width*thumbnailEdge/height), thumbnailEdge
}

// Thumbnail failures leave the original usable. Permission checks are performed
// by serveMedia before this function is reached, including on disk-cache hits.
func (a *App) thumbnailPath(ctx context.Context, source, id string) (string, bool) {
	info, err := os.Stat(source)
	if err != nil {
		return "", false
	}
	cacheKey := fmt.Sprintf("%s:%s:%d:%d:v1", id, source, info.Size(), info.ModTime().UnixNano())
	target, err := mediaPath(a.Config.MediaDir, "derived/"+tokenHash(cacheKey)+".jpg")
	if err != nil {
		return "", false
	}
	if cached, e := os.Stat(target); e == nil && cached.Size() > 0 {
		return target, true
	}
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	select {
	case imageProcessors <- struct{}{}:
		defer func() { <-imageProcessors }()
	case <-ctx.Done():
		return "", false
	case <-timeout.C:
		return "", false
	}
	// Another request for this same image may have produced the file while waiting.
	if cached, e := os.Stat(target); e == nil && cached.Size() > 0 {
		return target, true
	}
	src, err := os.Open(source)
	if err != nil {
		return "", false
	}
	defer src.Close()
	cfg, format, err := image.DecodeConfig(src)
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 16000 || cfg.Height > 16000 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
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
	width, height := thumbnailDimensions(cfg.Width, cfg.Height)
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
	if err = os.Rename(temp, target); err != nil {
		return "", false
	}
	return target, true
}

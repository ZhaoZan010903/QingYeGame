// Package mediafiles 管理受控本地图片及唯一解码槽。
package mediafiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/media"
	"strings"
)

var ImageProcessors = make(chan struct{}, 1)

type Store struct {
	Root         string
	MinFreeBytes int64
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func Path(root, key string) (string, error) {
	if key == "" || strings.ContainsAny(key, "\\:\x00") || strings.HasPrefix(key, "/") {
		return "", errors.New("invalid storage key")
	}
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid storage key")
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid storage path")
	}
	return target, nil
}
func ReadPath(root, key string) (string, error) {
	target, err := Path(root, key)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", err
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("storage symlink escapes root")
	}
	return real, nil
}
func ReencodeImage(source io.ReadSeeker, dest io.Writer, expectedMIME string) (string, error) {
	cfg, format, err := image.DecodeConfig(source)
	if err != nil {
		return "", errors.New("图片无法解码")
	}
	mime := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}[format]
	if mime == "" || mime != expectedMIME {
		return "", errors.New("图片实际格式与声明不一致")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 16000 || cfg.Height > 16000 || int64(cfg.Width)*int64(cfg.Height) > media.MaxImagePixels {
		return "", errors.New("图片像素过大，请缩小后上传")
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	decoded, _, err := image.Decode(source)
	if err != nil {
		return "", errors.New("图片内容损坏")
	}
	if format == "jpeg" {
		err = jpeg.Encode(dest, decoded, &jpeg.Options{Quality: 90})
		return "image/jpeg", err
	}
	err = png.Encode(dest, decoded)
	return "image/png", err
}

type LimitedWriter struct {
	W         io.Writer
	Remaining int64
}

func (w *LimitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.Remaining {
		return 0, errors.New("处理后的图片过大")
	}
	n, e := w.W.Write(p)
	w.Remaining -= int64(n)
	return n, e
}

// 临时文件传输期间不占用数据库连接。
type incoming struct{ temp, path string }

func (f *incoming) Install() error {
	if err := os.Rename(f.temp, f.path); err != nil {
		return fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "保存图片失败", err)
	}
	return nil
}
func (f *incoming) Discard() { _ = os.Remove(f.temp) }

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}
func (s *Store) Receive(ctx context.Context, key string, size int64, source io.Reader) (media.Staged, error) {
	if size < 1 || size > math.MaxInt64/2 {
		return nil, fault.New(fault.Invalid, "UPLOAD_SIZE_MISMATCH", "图片上传大小无效")
	}
	path, err := Path(s.Root, key)
	if err != nil {
		return nil, fault.Wrap(fault.Internal, "STORAGE_ERROR", "图片存储不可用", err)
	}
	if err = CheckAvailable(filepath.Dir(path), s.MinFreeBytes, size*2); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "图片存储不可用", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".incoming-*")
	if err != nil {
		return nil, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "图片存储不可用", err)
	}
	staged := &incoming{temp: f.Name(), path: path}
	n, copyErr := io.Copy(f, io.LimitReader(contextReader{ctx, source}, size+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n != size {
		staged.Discard()
		return nil, fault.New(fault.Invalid, "UPLOAD_SIZE_MISMATCH", "图片上传不完整或大小不一致")
	}
	return staged, nil
}
func (s *Store) Sanitize(_ context.Context, upload media.Upload) (media.Processed, error) {
	return s.sanitize(upload, s.Root)
}

// SanitizeTo keeps R20 intermediate copies inside a disposable processing directory.
func (s *Store) SanitizeTo(_ context.Context, upload media.Upload, outputRoot string) (media.Processed, error) {
	return s.sanitize(upload, outputRoot)
}

func (s *Store) sanitize(upload media.Upload, outputRoot string) (media.Processed, error) {
	sourcePath, err := ReadPath(s.Root, upload.Key)
	if err != nil {
		return media.Processed{}, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "找不到已上传的图片，请重新上传", err)
	}
	src, err := os.Open(sourcePath)
	if err != nil {
		return media.Processed{}, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "读取图片失败", err)
	}
	defer src.Close()
	outputID := upload.ID
	if upload.Resumable && upload.ProcessingToken != "" {
		outputID += "-" + hash(upload.ProcessingToken)[:16]
	}
	outputKey := "images/" + outputID + ".png"
	if upload.MIME == "image/jpeg" {
		outputKey = "images/" + outputID + ".jpg"
	}
	outputPath, err := Path(outputRoot, outputKey)
	if err != nil {
		return media.Processed{}, fault.Wrap(fault.Internal, "STORAGE_ERROR", "存储路径无效", err)
	}
	if err = CheckAvailable(filepath.Dir(outputPath), s.MinFreeBytes, 64<<20); err != nil {
		return media.Processed{}, err
	}
	if err = os.MkdirAll(filepath.Dir(outputPath), 0700); err != nil {
		return media.Processed{}, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "图片存储不可用", err)
	}
	dst, err := os.CreateTemp(filepath.Dir(outputPath), ".processed-*")
	if err != nil {
		return media.Processed{}, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "图片存储不可用", err)
	}
	temp := dst.Name()
	defer os.Remove(temp)
	mime, processErr := ReencodeImage(src, &LimitedWriter{W: dst, Remaining: 64 << 20}, upload.MIME)
	closeErr := dst.Close()
	if processErr != nil {
		return media.Processed{}, fault.Wrap(fault.Invalid, "INVALID_IMAGE", "图片无法处理，请检查格式、像素或文件大小", processErr)
	}
	if closeErr != nil {
		return media.Processed{}, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "保存处理图片失败", closeErr)
	}
	info, err := os.Stat(temp)
	if err != nil {
		return media.Processed{}, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "读取处理图片失败", err)
	}
	if err = os.Rename(temp, outputPath); err != nil {
		return media.Processed{}, fault.Wrap(fault.Unavailable, "STORAGE_ERROR", "保存处理图片失败", err)
	}
	return media.Processed{Key: outputKey, MIME: mime, Size: info.Size()}, nil
}
func (s *Store) RemoveRaw(key string) {
	if path, err := ReadPath(s.Root, key); err == nil {
		_ = os.Remove(path)
	}
}
func (s *Store) Open(ctx context.Context, key, id string, thumbnail bool) (*media.File, error) {
	missing := func() (*media.File, error) {
		return nil, fault.New(fault.NotFound, "MEDIA_FILE_MISSING", "图片暂不可用")
	}
	path, err := ReadPath(s.Root, key)
	if err != nil {
		return missing()
	}
	if thumbnail {
		if derived, ok := s.Thumbnail(ctx, path, id); ok {
			path = derived
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return missing()
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return missing()
	}
	sample := make([]byte, 512)
	n, _ := f.Read(sample)
	mime := http.DetectContentType(sample[:n])
	_, _ = f.Seek(0, io.SeekStart)
	allowed := map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true, "image/avif": true}
	if !allowed[mime] {
		f.Close()
		return nil, fault.New(fault.UnsupportedMedia, "INVALID_IMAGE_TYPE", "该媒体不是受支持的图片")
	}
	return &media.File{Reader: f, MIME: mime, Modified: info.ModTime()}, nil
}

package mediafiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"

	"qingye/backend/internal/application/legacysync"
)

type LegacyImages struct {
	Directory string
	MinFree   int64
}

type legacyTempFile struct{ *os.File }

func (f legacyTempFile) Close() error {
	return errors.Join(f.File.Close(), os.Remove(f.Name()))
}

func (s LegacyImages) Prepare(ctx context.Context, limit int64, fetch func(context.Context, io.Writer) (int64, error)) (legacysync.PreparedImage, int64, error) {
	var result legacysync.PreparedImage
	if err := os.MkdirAll(s.Directory, 0700); err != nil {
		return result, 0, err
	}
	if err := CheckAvailable(s.Directory, s.MinFree, limit+legacysync.MaxImageBytes); err != nil {
		return result, 0, err
	}
	file, err := os.CreateTemp(s.Directory, "legacy-source-*")
	if err != nil {
		return result, 0, err
	}
	raw := legacyTempFile{file}
	defer raw.Close()
	digest := sha256.New()
	input := &LimitedWriter{W: io.MultiWriter(raw, digest), Remaining: limit}
	received, err := fetch(ctx, input)
	read := limit - input.Remaining
	if err != nil || received != read || read == 0 {
		return result, read, legacysync.ErrImage
	}
	if _, err = raw.Seek(0, io.SeekStart); err != nil {
		return result, read, err
	}
	var signature [512]byte
	n, err := io.ReadFull(raw, signature[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return result, read, err
	}
	if _, err = raw.Seek(0, io.SeekStart); err != nil {
		return result, read, err
	}
	clean, err := os.CreateTemp(s.Directory, "legacy-clean-*")
	if err != nil {
		return result, read, err
	}
	prepared := legacyTempFile{clean}
	defer func() {
		if result.Body == nil {
			_ = prepared.Close()
		}
	}()
	select {
	case ImageProcessors <- struct{}{}:
		defer func() { <-ImageProcessors }()
	case <-ctx.Done():
		return result, read, ctx.Err()
	}
	hash := sha256.New()
	output := &LimitedWriter{W: io.MultiWriter(prepared, hash), Remaining: legacysync.MaxImageBytes}
	mime, err := ReencodeImage(raw, output, http.DetectContentType(signature[:n]))
	if err != nil {
		return result, read, legacysync.ErrImage
	}
	if _, err = prepared.Seek(0, io.SeekStart); err != nil {
		return result, read, err
	}
	result = legacysync.PreparedImage{
		Body: prepared, RawSHA256: hex.EncodeToString(digest.Sum(nil)),
		MIME: mime, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: legacysync.MaxImageBytes - output.Remaining,
	}
	return result, read, nil
}

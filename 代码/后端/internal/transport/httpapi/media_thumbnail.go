package httpapi

import (
	"context"
	"qingye/backend/internal/domain/media"
	"qingye/backend/internal/infrastructure/mediafiles"
)

const thumbnailEdge = media.ThumbnailEdge

func thumbnailDimensions(width, height int) (int, int) {
	return mediafiles.ThumbnailDimensions(width, height)
}
func (a *App) thumbnailPath(ctx context.Context, source, id string) (string, bool) {
	return (&mediafiles.Store{Root: a.Config.MediaDir}).Thumbnail(ctx, source, id)
}

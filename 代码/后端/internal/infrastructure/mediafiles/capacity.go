package mediafiles

import (
	"context"
	"math"
	"qingye/backend/internal/domain/fault"
)

// Reject a full disk before issuing a ticket. Receive/install still recheck because free space can change.
func (s *Store) CheckCapacity(ctx context.Context, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if size <= 0 || size > math.MaxInt64/3 {
		return fault.New(fault.Invalid, "INVALID_UPLOAD_SIZE", "图片大小无效")
	}
	return CheckAvailable(s.Root, s.MinFreeBytes, size*3)
}

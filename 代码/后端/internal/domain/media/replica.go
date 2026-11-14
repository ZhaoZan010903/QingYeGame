package media

import "time"

// LegacyReplica identifies one verified copy of a historical product original.
// The live media row and the replica must still be usable before each read.
type LegacyReplica struct {
	MediaID, StorageKey, Bucket, ObjectKey, MIME, SHA256, CRC64 string
	Size                                                        int64
	Modified                                                    time.Time
}

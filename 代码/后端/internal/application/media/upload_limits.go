package media

const productUploadLimit = 600

func productChunkLimit(maxBytes int64) int {
	chunks := int64(1)
	if maxBytes > 0 {
		chunks = 1 + (maxBytes-1)/ChunkSize
	}
	// Match the create window at the configured file size, allowing one lost-response retry per chunk.
	const allowance = productUploadLimit * 2
	maxInt := int(^uint(0) >> 1)
	if chunks > int64(maxInt/allowance) {
		return maxInt
	}
	return int(chunks) * allowance
}

// Package diskspace reports filesystem capacity without exposing
// platform-specific system calls to callers.
package diskspace

const maxSignedInt64 = int64(^uint64(0) >> 1)

func clampAvailableBytes(available uint64) int64 {
	if available > uint64(maxSignedInt64) {
		return maxSignedInt64
	}
	return int64(available)
}

func availableBytesFromBlocks(availableBlocks, blockSize uint64) int64 {
	if blockSize != 0 && availableBlocks > uint64(maxSignedInt64)/blockSize {
		return maxSignedInt64
	}
	return clampAvailableBytes(availableBlocks * blockSize)
}

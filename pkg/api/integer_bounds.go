package api

const (
	maxPlatformInt  = int(^uint(0) >> 1)
	maxCapacityUnit = int(^uint32(0) >> 1)
)

// saturatingAddNonNegative keeps capacity/accounting arithmetic fail-closed.
// Callers use it for externally configurable non-negative counts: wrapping to a
// small or negative integer could otherwise make an over-capacity workload look
// admissible.
func saturatingAddNonNegative(left, right int) int {
	if left < 0 || right < 0 || left > maxPlatformInt-right {
		return maxPlatformInt
	}
	return left + right
}

func saturatingMultiplyNonNegative(left, right int) int {
	if left < 0 || right < 0 {
		return maxPlatformInt
	}
	if left == 0 || right == 0 {
		return 0
	}
	if left > maxPlatformInt/right {
		return maxPlatformInt
	}
	return left * right
}

// nonNegativeSumExceedsLimit compares a+b to limit without first computing a+b.
func nonNegativeSumExceedsLimit(left, right, limit int) bool {
	if left < 0 || right < 0 || limit < 0 || left > limit {
		return true
	}
	return right > limit-left
}

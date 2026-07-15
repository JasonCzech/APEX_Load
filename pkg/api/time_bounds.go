package api

import "time"

const maxDurationMagnitude uint64 = 1<<63 - 1

// checkedDuration converts an externally supplied integer and unit without
// allowing time.Duration's int64 multiplication to wrap. Callers decide whether
// zero is disabled/defaulted or invalid for the individual setting.
func checkedDuration(value int, unit time.Duration) (time.Duration, bool) {
	if value < 0 || unit <= 0 || uint64(value) > maxDurationMagnitude/uint64(unit) {
		return 0, false
	}
	return time.Duration(value) * unit, true
}

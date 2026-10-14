package protocol

import (
	"math"
	"strconv"
)

// AssertionInt reads an assertion's integer operand: an int, an integral
// JSON number, or a decimal string such as a resolved {{param.status}}.
func AssertionInt(v any) (int, bool) {
	switch n := v.(type) {
	case string:
		// A decimal integer, as a resolved {{param.status}} reads.
		parsed, err := strconv.ParseInt(n, 10, 32)
		if err != nil || strconv.FormatInt(parsed, 10) != n {
			return 0, false
		}
		return int(parsed), true
	case int:
		return n, true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n ||
			n < -2147483648 || n > 2147483647 {
			return 0, false
		}
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

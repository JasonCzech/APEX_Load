package kubeops

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kubernetes resource quantities are parsed here by hand: this package speaks
// raw REST and deliberately carries no k8s.io/apimachinery dependency, so the
// small subset of the quantity grammar that matters for CPU and memory limits
// is implemented directly.
//
// Only the forms an operator actually types for a load generator are accepted.
// Exotic-but-legal spellings (exponent notation, negative values, fractional
// millicores) are rejected rather than silently mis-parsed, because a wrong
// answer here changes how much hardware a test run consumes.

// binarySuffixes are powers of 1024; decimalSuffixes are powers of 1000. The
// casing is significant and intentionally not normalized: "M" (10^6) and "Mi"
// (2^20) are different quantities, and accepting lowercase "mi" would invite
// confusion with the CPU milli suffix.
var (
	binarySuffixes  = map[string]int{"Ki": 1, "Mi": 2, "Gi": 3, "Ti": 4, "Pi": 5, "Ei": 6}
	decimalSuffixes = map[string]int{"k": 1, "K": 1, "M": 2, "G": 3, "T": 4, "P": 5, "E": 6}
)

// ParseCPUMillis converts a CPU quantity to whole millicores. "500m" is 500,
// "2" is 2000, "0.5" is 500. An empty string means "unset" and yields (0,
// false, nil) so callers can distinguish it from an explicit zero.
func ParseCPUMillis(raw string) (millis int64, present bool, err error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false, nil
	}
	if strings.HasSuffix(value, "m") {
		digits := strings.TrimSuffix(value, "m")
		parsed, parseErr := strconv.ParseInt(digits, 10, 64)
		if parseErr != nil {
			return 0, false, fmt.Errorf("invalid CPU quantity %q: millicores must be a whole number", raw)
		}
		if parsed < 0 {
			return 0, false, fmt.Errorf("invalid CPU quantity %q: must not be negative", raw)
		}
		return parsed, true, nil
	}
	cores, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil || math.IsNaN(cores) || math.IsInf(cores, 0) {
		return 0, false, fmt.Errorf("invalid CPU quantity %q: expected a value like \"500m\" or \"2\"", raw)
	}
	if cores < 0 {
		return 0, false, fmt.Errorf("invalid CPU quantity %q: must not be negative", raw)
	}
	// Reject anything finer than a millicore instead of rounding: silently
	// truncating 0.0005 to 0 would hand the pod an unrequested CPU share.
	scaled := cores * 1000
	rounded := math.Round(scaled)
	if math.Abs(scaled-rounded) > 1e-6 {
		return 0, false, fmt.Errorf("invalid CPU quantity %q: cannot be finer than 1m", raw)
	}
	return int64(rounded), true, nil
}

// ParseMemoryBytes converts a memory quantity to bytes. Binary suffixes (Ki,
// Mi, Gi, …) are powers of 1024 and decimal suffixes (k, M, G, …) powers of
// 1000; a bare number is bytes. An empty string means "unset".
func ParseMemoryBytes(raw string) (bytes int64, present bool, err error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false, nil
	}
	digits, multiplier := value, float64(1)
	switch {
	case len(value) >= 2 && isBinarySuffix(value[len(value)-2:]):
		suffix := value[len(value)-2:]
		digits = value[:len(value)-2]
		multiplier = math.Pow(1024, float64(binarySuffixes[suffix]))
	case len(value) >= 1 && isDecimalSuffix(value[len(value)-1:]):
		suffix := value[len(value)-1:]
		digits = value[:len(value)-1]
		multiplier = math.Pow(1000, float64(decimalSuffixes[suffix]))
	}
	amount, parseErr := strconv.ParseFloat(strings.TrimSpace(digits), 64)
	if parseErr != nil || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, false, fmt.Errorf("invalid memory quantity %q: expected a value like \"512Mi\" or \"4Gi\"", raw)
	}
	if amount < 0 {
		return 0, false, fmt.Errorf("invalid memory quantity %q: must not be negative", raw)
	}
	total := amount * multiplier
	if total > math.MaxInt64 {
		return 0, false, fmt.Errorf("invalid memory quantity %q: exceeds the maximum representable size", raw)
	}
	return int64(total), true, nil
}

func isBinarySuffix(suffix string) bool {
	_, ok := binarySuffixes[suffix]
	return ok
}

func isDecimalSuffix(suffix string) bool {
	_, ok := decimalSuffixes[suffix]
	return ok
}

// FormatCPUMillis renders millicores back into the canonical Kubernetes
// spelling: whole cores as a bare integer, anything else with the "m" suffix.
func FormatCPUMillis(millis int64) string {
	if millis%1000 == 0 {
		return strconv.FormatInt(millis/1000, 10)
	}
	return strconv.FormatInt(millis, 10) + "m"
}

// FormatMemoryBytes renders bytes using the largest binary suffix that divides
// evenly, so a round-trip through ParseMemoryBytes preserves the value exactly.
func FormatMemoryBytes(bytes int64) string {
	if bytes == 0 {
		return "0"
	}
	units := []struct {
		suffix string
		scale  int64
	}{
		{"Ei", 1 << 60}, {"Pi", 1 << 50}, {"Ti", 1 << 40},
		{"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
	}
	for _, unit := range units {
		if bytes >= unit.scale && bytes%unit.scale == 0 {
			return strconv.FormatInt(bytes/unit.scale, 10) + unit.suffix
		}
	}
	return strconv.FormatInt(bytes, 10)
}

// ResourceList is the decoded form of a Kubernetes resource map (the JSON
// values are quantity strings). Only the CPU and memory keys are interpreted;
// everything else is preserved verbatim so a round-trip does not drop
// extended resources such as GPUs.
type ResourceList map[string]string

// CPUMillis returns the parsed CPU value, preferring the requests-style key
// the caller passes in.
func (r ResourceList) CPUMillis() (int64, bool, error) {
	return ParseCPUMillis(r["cpu"])
}

// MemoryBytes returns the parsed memory value.
func (r ResourceList) MemoryBytes() (int64, bool, error) {
	return ParseMemoryBytes(r["memory"])
}

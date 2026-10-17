package analysis

// durationNanoseconds defines physical duration units only. Provider spellings
// and selector/rate semantics belong to their adapters, not this table. Integer
// factors keep the established conversion ratios exact before float rounding.
func durationNanoseconds(unit string) (float64, bool) {
	switch unit {
	case "ns":
		return 1, true
	case "us":
		return 1e3, true
	case "ms":
		return 1e6, true
	case "s":
		return 1e9, true
	case "min":
		return 60e9, true
	}
	return 0, false
}

// DurationSeconds returns the seconds in one canonical duration unit.
func DurationSeconds(unit string) (float64, bool) {
	factor, ok := durationNanoseconds(unit)
	return factor / 1e9, ok
}

// DurationScale returns the multiplier between two canonical duration units.
func DurationScale(from, to string) (float64, bool) {
	source, ok := durationNanoseconds(from)
	if !ok {
		return 0, false
	}
	target, ok := durationNanoseconds(to)
	if !ok {
		return 0, false
	}
	return source / target, true
}

// ConvertDuration preserves division for subunit-to-unit conversions, avoiding
// avoidable intermediate overflow and matching the source adapters' arithmetic.
func ConvertDuration(value float64, from, to string) (float64, bool) {
	source, ok := durationNanoseconds(from)
	if !ok {
		return 0, false
	}
	target, ok := durationNanoseconds(to)
	if !ok {
		return 0, false
	}
	if source < target {
		return value / (target / source), true
	}
	return value * (source / target), true
}

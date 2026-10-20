package analysis

import "time"

// LocalTimestampResolution describes whether naive wall-clock components name
// exactly one instant in the selected timezone. It does not choose an offset
// for a repeated clock time or normalize a time skipped by a clock change.
type LocalTimestampResolution string

const (
	LocalTimestampUnique      LocalTimestampResolution = "unique"
	LocalTimestampAmbiguous   LocalTimestampResolution = "ambiguous"
	LocalTimestampNonexistent LocalTimestampResolution = "nonexistent"
	LocalTimestampUnqualified LocalTimestampResolution = "unqualified"
)

// ResolveLocalTimestamp interprets wall's calendar/clock fields, ignoring its
// Location. It traverses timezone intervals, not guessed one-hour DST steps:
// half-hour changes and skipped/repeated dates use the same rule. Work is
// bounded to 32 intervals within 48 hours on either side. Offsets outside
// +/-24 hours and inputs outside years 1..9999 are unqualified. A bounded
// search that cannot finish must not claim a unique or nonexistent instant.
func ResolveLocalTimestamp(wall time.Time, location *time.Location) (time.Time, LocalTimestampResolution) {
	if location == nil || wall.Year() < 1 || wall.Year() > 9999 {
		return time.Time{}, LocalTimestampUnqualified
	}
	naive := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), time.UTC)
	const offsetLimit = 24 * time.Hour
	const searchRadius = 2 * offsetLimit
	cursor, limit := naive.Add(-searchRadius), naive.Add(searchRadius)
	var found time.Time
	hasFound := false
	for i := 0; i < 32; i++ {
		local := cursor.In(location)
		_, offset := local.Zone()
		if offset < -int(offsetLimit/time.Second) || offset > int(offsetLimit/time.Second) {
			return time.Time{}, LocalTimestampUnqualified
		}
		start, end := local.ZoneBounds()
		candidate := naive.Add(-time.Duration(offset) * time.Second)
		if (start.IsZero() || !candidate.Before(start)) && (end.IsZero() || candidate.Before(end)) {
			observed := candidate.In(location)
			components := time.Date(observed.Year(), observed.Month(), observed.Day(), observed.Hour(), observed.Minute(), observed.Second(), observed.Nanosecond(), time.UTC)
			if components.Equal(naive) {
				if hasFound && !candidate.Equal(found) {
					return time.Time{}, LocalTimestampAmbiguous
				}
				found, hasFound = candidate, true
			}
		}
		if end.IsZero() || !end.Before(limit) {
			if hasFound {
				return found.UTC(), LocalTimestampUnique
			}
			return time.Time{}, LocalTimestampNonexistent
		}
		if !end.After(cursor) {
			return time.Time{}, LocalTimestampUnqualified
		}
		cursor = end
	}
	return time.Time{}, LocalTimestampUnqualified
}

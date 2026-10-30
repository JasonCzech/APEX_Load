package importreview

import "time"

// importReferenceTimeBudget scales a reference-guard scaling test's wall-clock
// bound. The bounds are generous for slow runners and still catch quadratic
// work; the race detector slows these passes several times over, more on a
// shared CI runner.
func importReferenceTimeBudget(normal time.Duration) time.Duration {
	if raceDetectorEnabled {
		return 5 * normal
	}
	return normal
}

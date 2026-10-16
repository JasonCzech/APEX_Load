package enterprisearchive

import "time"

// javaAnalysisTimeBudget scales a large-archive analysis regression's wall-clock
// bound. The bounds are generous for slow runners and still catch quadratic
// work; the race detector slows these passes several times over.
func javaAnalysisTimeBudget(normal time.Duration) time.Duration {
	if raceDetectorEnabled {
		return 5 * normal
	}
	return normal
}

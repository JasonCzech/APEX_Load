package enterprisearchive

import "time"

// javaPreflightTimeBudget bounds the large-archive analysis regressions. It is
// generous enough for slow runners and catches quadratic work; the race
// detector slows these passes several times over.
func javaPreflightTimeBudget() time.Duration {
	if raceDetectorEnabled {
		return 25 * time.Second
	}
	return 5 * time.Second
}

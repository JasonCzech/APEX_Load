package api

// The metrics handler emits a map with either message or metrics/history.
// Name its bounded read shape so inference retains typed evidence, without
// requiring telemetry on historical runs or metrics before first publication.
type liveMetricsReadResponseContract struct {
	TestID  string          `json:"test_id"`
	Status  string          `json:"status"`
	Message string          `json:"message,omitempty"`
	Metrics *LiveMetrics    `json:"metrics,omitempty"`
	History *[]*LiveMetrics `json:"history,omitempty"`
}

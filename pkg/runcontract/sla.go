// Package runcontract contains shared client/server contracts without controller runtime dependencies.
package runcontract

type TransactionSLARuleResult struct {
	Rule         TransactionSLARule `json:"rule"`
	Status       string             `json:"status"`
	Observed     *float64           `json:"observed,omitempty"`
	MatchedCount int64              `json:"matched_count"`
	Message      string             `json:"message"`
	// PercentileQuality is "approximate" when a percentile rule's observed
	// value came from the duration sketch, within 0.5% of the value at its
	// rank, and empty when it used every matching observation.
	PercentileQuality string `json:"percentile_quality,omitempty"`
}

type TransactionResponseTimeSLAResult struct {
	Group                string  `json:"group"`
	Transaction          string  `json:"transaction"`
	ThresholdMs          float64 `json:"threshold_ms"`
	TotalCount           int64   `json:"total_count"`
	EligibleSuccessCount int64   `json:"eligible_success_count"`
	ExcludedErrorCount   int64   `json:"excluded_error_count"`
	MissedCount          int64   `json:"missed_count"`
	MissedPct            float64 `json:"missed_pct"`
	MetPct               float64 `json:"met_pct"`
	RequiredMetPct       float64 `json:"required_met_pct"`
	Status               string  `json:"status"`
}

type SLABreachReport struct {
	TestID                            string                             `json:"test_id"`
	Status                            string                             `json:"status"`
	SLAEvaluated                      bool                               `json:"sla_evaluated"`
	SLABreached                       bool                               `json:"sla_breached"`
	Details                           []string                           `json:"details,omitempty"`
	TransactionRuleResults            []TransactionSLARuleResult         `json:"transaction_rule_results,omitempty"`
	TransactionResponseTimeSLAResults []TransactionResponseTimeSLAResult `json:"transaction_response_time_sla_results,omitempty"`
}

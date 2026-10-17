// Package runcontract contains shared client/server contracts without controller runtime dependencies.
package runcontract

import (
	"github.com/jasonczech/apexload/pkg/analysis"
)

type GoalConfig struct {
	MonitorRules             []analysis.MonitorRule `json:"monitor_rules,omitempty"`
	TargetAverageTPS         *float64               `json:"target_average_tps,omitempty"`
	P95LatencyMs             *float64               `json:"p95_latency_ms,omitempty"`
	MaxErrorPct              *float64               `json:"max_error_pct,omitempty"`
	MaxAvgServerProcessingMs *float64               `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs         *float64               `json:"max_avg_transfer_ms,omitempty"`
	MaxErrorCount            *int                   `json:"max_error_count,omitempty"`
	MinVUsers                *int                   `json:"min_vusers,omitempty"`
	MaxVUsers                *int                   `json:"max_vusers,omitempty"`
	// Web Vitals experience thresholds, evaluated against the aggregated
	// browser metrics emitted by browser VUsers and experience canaries.
	MaxLCPMs *float64 `json:"max_lcp_ms,omitempty"`
	MaxINPMs *float64 `json:"max_inp_ms,omitempty"`
	MaxCLS   *float64 `json:"max_cls,omitempty"`

	// Streaming-ingestion thresholds (EH.4), evaluated against the messaging
	// server-telemetry summary (Event Hubs / Kafka producer runs).
	MaxThrottledRequests *int     `json:"max_throttled_requests,omitempty"`    // run-total throttle/ServerBusy/quota responses
	MaxEndToEndLatencyMs *float64 `json:"max_end_to_end_latency_ms,omitempty"` // avg producer->observed-consume latency
	MaxPartitionSkewPct  *float64 `json:"max_partition_skew_pct,omitempty"`    // coefficient of variation of per-partition produce volume
	MaxRedeliveryRatePct *float64 `json:"max_redelivery_rate_pct,omitempty"`   // percent of received messages redelivered
	MaxDeadLetterRatePct *float64 `json:"max_dead_letter_rate_pct,omitempty"`  // percent of received messages dead-lettered

	StopOnSLABreach bool   `json:"stop_on_sla_breach,omitempty"`
	StopOnMaxErrors bool   `json:"stop_on_max_errors,omitempty"`
	SLABreachWindow string `json:"sla_breach_window,omitempty"`
	// TransactionResponseTimeSLAMs is the scenario-wide default response-time
	// SLA for named business transactions. It is independent from P95LatencyMs,
	// which remains a fleet-level goal-controller guardrail.
	TransactionResponseTimeSLAMs *float64 `json:"transaction_response_time_sla_ms,omitempty"`
	// EarlyStop is a scenario-specific, VUser-mode-compatible error-rate stop.
	// It deliberately lives beside the existing guardrails so legacy durable
	// goal_config_json columns can round-trip it without a database migration.
	EarlyStop *EarlyStopConfig `json:"early_stop,omitempty"`
	// WorkloadShapeConfig retains authoring intent that cannot be reconstructed
	// losslessly from derived stage rows (for example an uneven final step).
	WorkloadShapeConfig *WorkloadShapeConfig `json:"workload_shape_config,omitempty"`
	// TransactionRules are terminal-only SLA rules evaluated from the complete
	// archive. They do not participate in live stop-on-breach decisions.
	TransactionRules []TransactionSLARule `json:"transaction_rules,omitempty"`
}

type EarlyStopConfig struct {
	Enabled      bool    `json:"enabled"`
	ErrorRatePct float64 `json:"error_rate_pct"`
	BreachWindow string  `json:"breach_window"`
}

type SteppedRampDefinition struct {
	StartVUsers   int    `json:"start_vusers"`
	EndVUsers     int    `json:"end_vusers"`
	IncrementBy   string `json:"increment_by"`
	StepCount     int    `json:"step_count,omitempty"`
	VUsersPerStep int    `json:"vusers_per_step,omitempty"`
}

type SteppedRampConfig struct {
	Scope    string                           `json:"scope"`
	Scenario *SteppedRampDefinition           `json:"scenario,omitempty"`
	Groups   map[string]SteppedRampDefinition `json:"groups,omitempty"`
}

type ClosedVUserDefinition struct {
	PeakVUsers      int    `json:"peak_vusers"`
	RampUp          string `json:"ramp_up"`
	Sustain         string `json:"sustain"`
	RampDownEnabled bool   `json:"ramp_down_enabled"`
	RampDown        string `json:"ramp_down"`
}

type ClosedVUserConfig struct {
	Scope    string                           `json:"scope"`
	Scenario *ClosedVUserDefinition           `json:"scenario,omitempty"`
	Groups   map[string]ClosedVUserDefinition `json:"groups,omitempty"`
}

type GoalDrivenARTConfig struct {
	Basis          string  `json:"basis"`
	ARTMs          float64 `json:"art_ms"`
	BaselineTestID string  `json:"baseline_test_id,omitempty"`
	BaselineGroup  string  `json:"baseline_group,omitempty"`
}

type GoalDrivenGroupDefinition struct {
	TargetTPS float64              `json:"target_tps"`
	ART       *GoalDrivenARTConfig `json:"art,omitempty"`
}

type GoalDrivenConfig struct {
	Groups map[string]GoalDrivenGroupDefinition `json:"groups"`
}

type WorkloadShapeConfig struct {
	SteppedRamp *SteppedRampConfig `json:"stepped_ramp,omitempty"`
	ClosedVUser *ClosedVUserConfig `json:"closed_vuser,omitempty"`
	GoalDriven  *GoalDrivenConfig  `json:"goal_driven,omitempty"`
}

type TransactionSLARule struct {
	Population  string  `json:"population,omitempty"`
	Protocol    string  `json:"protocol,omitempty"`
	Group       string  `json:"group,omitempty"`
	Transaction string  `json:"transaction"`
	Metric      string  `json:"metric"`
	Comparator  string  `json:"comparator"`
	Threshold   float64 `json:"threshold"`
	Severity    string  `json:"severity,omitempty"`
	// Iterations names the browser iterations judged: warm ones by default
	// ("" or "warm"), "all", or "cold" ones only. Samples from sessions
	// that never run warm are judged by every value but "cold".
	Iterations string `json:"iterations,omitempty"`
}

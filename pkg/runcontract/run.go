package runcontract

import (
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// CreateTestRequest is the single-script client projection of the create-run
// wire contract. Controller-only creation state does not belong in clients.
type CreateTestRequest struct {
	Name         string      `json:"name"`
	Duration     string      `json:"duration"`
	ScenarioMode string      `json:"scenario_mode,omitempty"`
	GoalConfig   *GoalConfig `json:"goal_config,omitempty"`
	ScriptID     string      `json:"script_id,omitempty"`
	VUsers       int         `json:"vusers,omitempty"`
	RampUp       string      `json:"ramp_up,omitempty"`
}

// TestStatus is the managed-run projection consumed by first-party clients.
// Duration is the server's nanosecond duration, not its human-readable label.
type TestStatus struct {
	ID          string        `json:"id"`
	RefKey      string        `json:"ref_key,omitempty"`
	Name        string        `json:"name"`
	Status      string        `json:"status"`
	ProjectID   string        `json:"project_id,omitempty"`
	Duration    time.Duration `json:"duration_ns"`
	LiveMetrics *LiveMetrics  `json:"live_metrics,omitempty"`
}

// LiveMetrics contains the metrics used by run/wait clients. Extra server
// metrics remain additive and are deliberately not re-emitted by this view.
type LiveMetrics struct {
	TotalTx      int64           `json:"total_transactions"`
	TotalErrors  int64           `json:"total_errors"`
	TPS          float64         `json:"tps"`
	HTTPVersions *HTTPVersionMix `json:"http_versions,omitempty"`
}

// HTTPVersionMix retains the wire's embedded version counts; ratios are
// calculated from these counts by clients rather than copied from findings.
type HTTPVersionMix struct{ protocol.HTTPVersionMix }

func IsTerminalTestStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

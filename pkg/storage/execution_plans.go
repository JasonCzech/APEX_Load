package storage

import (
	"encoding/json"
	"time"
)

// ExecutionPlan is an ordered collection of scenario runs — the LoadRunner
// Enterprise "test set / test instance" analog. Unlike a scenario (whose groups
// run within a single test), an execution plan orchestrates multiple test runs,
// in parallel or in sequence. Entries are stored inline as JSON.
type ExecutionPlan struct {
	ID          string               `json:"id"`
	ProjectID   string               `json:"project_id,omitempty"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Entries     []ExecutionPlanEntry `json:"entries"`
	RunMode     string               `json:"run_mode"` // parallel | sequential
	TimeslotID  *string              `json:"timeslot_id,omitempty"`
	Status      string               `json:"status,omitempty"` // draft | ...
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

// ExecutionPlanEntry references a scenario to run, with optional runtime
// overrides. RuntimeOverrides is opaque JSON so the storage layer stays
// decoupled from the api package's RuntimeSettingsOverride type.
type ExecutionPlanEntry struct {
	Order            int             `json:"order"`
	ScenarioID       string          `json:"scenario_id"`
	ScenarioName     string          `json:"scenario_name,omitempty"`
	Enabled          *bool           `json:"enabled,omitempty"` // nil = enabled (default)
	RuntimeOverrides json.RawMessage `json:"runtime_overrides,omitempty"`
	FailurePolicy    string          `json:"failure_policy,omitempty"` // halt | skip
}

// IsEnabled reports whether the entry participates in a run. Entries are enabled
// by default; only an explicit "enabled": false disables one.
func (e ExecutionPlanEntry) IsEnabled() bool {
	return e.Enabled == nil || *e.Enabled
}

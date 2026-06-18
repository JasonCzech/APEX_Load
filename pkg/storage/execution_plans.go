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
	// DataPools, when non-empty, overrides the scenario's shared data-pool
	// bindings for this entry's run (the materialize step passes them through,
	// and an explicit override wins over the scenario defaults).
	DataPools []ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	// TimeslotID, when set, binds this entry's run to an approved capacity
	// timeslot at create (strict); falls back to the plan-level TimeslotID.
	TimeslotID *string `json:"timeslot_id,omitempty"`
}

// IsEnabled reports whether the entry participates in a run. Entries are enabled
// by default; only an explicit "enabled": false disables one.
func (e ExecutionPlanEntry) IsEnabled() bool {
	return e.Enabled == nil || *e.Enabled
}

// ExecutionPlanRun is a single materialization of an execution plan. Sequential
// runs advance entry-by-entry, gated on each run reaching a terminal state; the
// row is persisted at every transition so progress survives a dashboard reload.
// Live orchestration is in-memory on the TestRunner, so an in-flight run is
// orphaned on controller restart exactly as in-flight tests are.
type ExecutionPlanRun struct {
	ID          string                  `json:"id"`
	PlanID      string                  `json:"plan_id"`
	ProjectID   string                  `json:"project_id,omitempty"`
	RunMode     string                  `json:"run_mode"` // parallel | sequential
	Status      string                  `json:"status"`   // running | completed | halted | failed
	Entries     []ExecutionPlanRunEntry `json:"entries"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
}

// ExecutionPlanRunEntry is the per-entry status within a plan run.
type ExecutionPlanRunEntry struct {
	Order        int    `json:"order"`
	ScenarioID   string `json:"scenario_id"`
	ScenarioName string `json:"scenario_name,omitempty"`
	TestID       string `json:"test_id,omitempty"`
	Status       string `json:"status"` // pending | running | complete | failed | skipped
	Detail       string `json:"detail,omitempty"`
}

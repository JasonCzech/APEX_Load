package storage

import (
	"encoding/json"
	"time"
)

// ExecutionPlanApproval gates an execution-plan run that governance flagged
// (e.g. its aggregate peak VUsers would exceed the project quota). A pending
// approval blocks the run until a project admin approves; an approved, unconsumed
// approval (RunID empty) authorizes the next run, which then stamps its RunID.
type ExecutionPlanApproval struct {
	ID             string     `json:"id"`
	PlanID         string     `json:"plan_id"`
	ProjectID      string     `json:"project_id,omitempty"`
	Policy         string     `json:"policy"`
	Status         string     `json:"status"` // pending | approved | rejected
	RequestedBy    string     `json:"requested_by,omitempty"`
	RequestedAt    time.Time  `json:"requested_at"`
	ResolvedBy     string     `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	DecisionReason string     `json:"decision_reason,omitempty"`
	RequiredRole   string     `json:"required_role,omitempty"`
	RunID          string     `json:"run_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ExecutionPlanSchedule is a recurring trigger for an execution plan — the
// plan-level analog of ScenarioSchedule. It reuses the same cron grammar,
// timezone evaluation, missed-run policy, and date-range pause; materialization
// starts an ExecutionPlanRun (recorded as LastRunRunID) rather than a single test.
type ExecutionPlanSchedule struct {
	ID                 string     `json:"id"`
	PlanID             string     `json:"plan_id"`
	ProjectID          string     `json:"project_id,omitempty"`
	Revision           int        `json:"revision,omitempty"`
	Name               string     `json:"name"`
	CronExpr           string     `json:"cron_expr"`
	Timezone           string     `json:"timezone,omitempty"`
	RunNameTemplate    string     `json:"run_name_template,omitempty"`
	Enabled            bool       `json:"enabled"`
	MissedRunPolicy    string     `json:"missed_run_policy,omitempty"`
	ConflictSummary    string     `json:"conflict_summary,omitempty"`
	NextRequiredAction string     `json:"next_required_action,omitempty"`
	LastRunRunID       string     `json:"last_run_run_id,omitempty"`
	LastRunAt          *time.Time `json:"last_run_at,omitempty"`
	NextRunAt          *time.Time `json:"next_run_at,omitempty"`
	// DisabledFrom/DisabledUntil pause the schedule over an absolute date range
	// without disabling it; either bound may be nil for an open-ended pause.
	DisabledFrom  *time.Time `json:"disabled_from,omitempty"`
	DisabledUntil *time.Time `json:"disabled_until,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ExecutionPlanScheduleOccurrence records one materialization (or skip/block) of
// an execution-plan schedule. UNIQUE(schedule_id, planned_at) makes the sweep
// idempotent. RunID points at the ExecutionPlanRun that was started.
type ExecutionPlanScheduleOccurrence struct {
	ID               string     `json:"id"`
	ScheduleID       string     `json:"schedule_id"`
	PlanID           string     `json:"plan_id"`
	ProjectID        string     `json:"project_id,omitempty"`
	ScheduleRevision int        `json:"schedule_revision,omitempty"`
	PlannedAt        time.Time  `json:"planned_at"`
	MaterializedAt   *time.Time `json:"materialized_at,omitempty"`
	Status           string     `json:"status"`
	ReasonCode       string     `json:"reason_code,omitempty"`
	RunID            string     `json:"run_id,omitempty"`
	ConflictSummary  string     `json:"conflict_summary,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

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

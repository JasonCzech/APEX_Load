package storage

import (
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

// Store defines the interface for persisting Stormrunner data.
type Store interface {
	// Scripts
	SaveScript(script *dsl.Script) error
	GetScript(id string) (*dsl.Script, error)
	ListScripts() ([]*dsl.Script, error)
	DeleteScript(id string) error

	// Tests
	SaveTest(test *TestRecord) error
	GetTest(id string) (*TestRecord, error)
	ListTests() ([]*TestRecord, error)
	UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error
	DeleteTest(id string) error

	// History
	SaveHistory(testID string, timestamp time.Time, metrics any) error
	GetHistory(testID string) ([]any, error)

	Close() error
}

// TestRecord represents a persisted test run.
type TestRecord struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	ScenarioMode    string     `json:"scenario_mode,omitempty"`
	ScriptShakeout  bool       `json:"script_shakeout,omitempty"`
	ResourceBaseline bool      `json:"resource_baseline,omitempty"`
	TotalVUsers     int        `json:"total_vusers"`
	Duration        int64      `json:"duration_ns"`
	StartTime       time.Time  `json:"start_time"`
	EndTime         *time.Time `json:"end_time,omitempty"`
	Error           string     `json:"error,omitempty"`
	Groups          any        `json:"groups"`                     // JSON blob
	RuntimeSettings any        `json:"runtime_settings,omitempty"` // JSON blob
	GoalConfig      any        `json:"goal_config,omitempty"`      // JSON blob
}

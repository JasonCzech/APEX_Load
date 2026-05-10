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
	SaveRunArchive(record *RunArchiveRecord) error
	GetRunArchive(testID string) (*RunArchiveRecord, error)

	Close() error
}

type RunArchiveRecord struct {
	TestID               string    `json:"test_id"`
	SchemaVersion        string    `json:"schema_version"`
	ArchivePath          string    `json:"archive_path"`
	ManifestPath         string    `json:"manifest_path"`
	TransactionCount     int64     `json:"transaction_count"`
	ResourceSampleCount  int64     `json:"resource_sample_count"`
	IntervalSummaryCount int64     `json:"interval_summary_count"`
	CreatedAt            time.Time `json:"created_at"`
	CompletedAt          time.Time `json:"completed_at"`
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

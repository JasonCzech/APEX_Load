package storage

import (
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

const DefaultProjectID = "unassigned"
const LegacyApplicationID = "legacy"
const LegacyReleaseID = "legacy-imported"

// Store defines the interface for persisting Stormrunner data.
type Store interface {
	// Applications and releases
	SaveApplication(application *Application) error
	GetApplication(id string) (*Application, error)
	ListApplications() ([]*Application, error)
	DeleteApplication(id string) error
	SaveRelease(release *Release) error
	GetRelease(id string) (*Release, error)
	ListReleases(applicationID string, includeArchived bool) ([]*Release, error)
	ArchiveRelease(id string) error
	DeleteRelease(id string) error

	// Reusable scenarios
	SaveScenario(scenario *Scenario, scripts []ScenarioScript) error
	GetScenario(id string) (*Scenario, []ScenarioScript, error)
	ListScenarios() ([]*Scenario, error)
	DeleteScenario(id string) error
	SaveScenarioDataPoolBindings(scenarioID string, bindings []ScenarioDataPoolBinding) error
	ListScenarioDataPoolBindings(scenarioID string) ([]ScenarioDataPoolBinding, error)
	DeleteScenarioDataPoolBinding(id string) error

	// Shared data pools
	SaveDataPool(pool *SharedDataPool) error
	GetDataPool(id string) (*SharedDataPool, error)
	ListDataPools(includeArchived bool) ([]*SharedDataPool, error)
	DeleteDataPool(id string) error
	SaveDataPoolRow(row *SharedDataPoolRow) error
	ListDataPoolRows(poolID string) ([]*SharedDataPoolRow, error)
	DeleteDataPoolRow(id string) error

	// Projects
	SaveProject(project *Project) error
	GetProject(id string) (*Project, error)
	ListProjects() ([]*Project, error)
	DeleteProject(id string) error

	// Folders
	SaveFolder(folder *Folder) error
	GetFolder(id string) (*Folder, error)
	ListFolders(projectID string, category string) ([]*Folder, error)
	MoveFolder(id string, newParentID *string) error
	DeleteFolder(id string) error

	// Scripts
	SaveScript(script *dsl.Script) error
	GetScript(id string) (*dsl.Script, error)
	ListScripts(projectID string, folderID *string) ([]*dsl.Script, error)
	DeleteScript(id string) error
	MoveScript(id string, folderID *string) error

	// Tests
	SaveTest(test *TestRecord) error
	GetTest(id string) (*TestRecord, error)
	ListTests(projectID string, folderID *string) ([]*TestRecord, error)
	ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*TestRecord, error)
	UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error
	DeleteTest(id string) error
	MoveTest(id string, folderID *string) error

	// History
	SaveHistory(testID string, timestamp time.Time, metrics any) error
	GetHistory(testID string) ([]any, error)
	SaveRunArchive(record *RunArchiveRecord) error
	GetRunArchive(testID string) (*RunArchiveRecord, error)

	// System settings
	SaveSetting(key string, value string) error
	GetSetting(key string) (string, error)

	Close() error
}

type Application struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Release struct {
	ID            string     `json:"id"`
	ApplicationID string     `json:"application_id"`
	Name          string     `json:"name"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
}

type Scenario struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Duration         string    `json:"duration"`
	ScenarioMode     string    `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool      `json:"script_shakeout,omitempty"`
	ResourceBaseline bool      `json:"resource_baseline,omitempty"`
	GoalConfig       any       `json:"goal_config,omitempty"`
	RuntimeSettings  any       `json:"runtime_settings,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ScenarioScript struct {
	ScenarioID     string `json:"scenario_id,omitempty"`
	Order          int    `json:"order"`
	Name           string `json:"name,omitempty"`
	ScriptID       string `json:"script_id"`
	VUsers         int    `json:"vusers"`
	RampUp         string `json:"ramp_up,omitempty"`
	RampDown       string `json:"ramp_down,omitempty"`
	Initialization string `json:"initialization,omitempty"`
}

type SharedDataPool struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Schema      map[string]string `json:"schema,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	ArchivedAt  *time.Time        `json:"archived_at,omitempty"`
}

type SharedDataPoolRow struct {
	ID        string            `json:"id"`
	PoolID    string            `json:"pool_id"`
	Data      map[string]string `json:"data"`
	Status    string            `json:"status,omitempty"`
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type ScenarioDataPoolBinding struct {
	ID                string    `json:"id"`
	ScenarioID        string    `json:"scenario_id,omitempty"`
	PoolID            string    `json:"pool_id"`
	PoolName          string    `json:"pool_name,omitempty"`
	Alias             string    `json:"alias"`
	AccessMode        string    `json:"access_mode"`
	SelectionStrategy string    `json:"selection_strategy"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Folder struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	ParentID  *string   `json:"parent_id,omitempty"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Status           string     `json:"status"`
	ApplicationID    string     `json:"application_id,omitempty"`
	ReleaseID        string     `json:"release_id,omitempty"`
	ScenarioID       string     `json:"scenario_id,omitempty"`
	ProjectID        string     `json:"project_id,omitempty"`
	FolderID         *string    `json:"folder_id,omitempty"`
	ScenarioMode     string     `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool       `json:"script_shakeout,omitempty"`
	ResourceBaseline bool       `json:"resource_baseline,omitempty"`
	TotalVUsers      int        `json:"total_vusers"`
	Duration         int64      `json:"duration_ns"`
	StartTime        time.Time  `json:"start_time"`
	EndTime          *time.Time `json:"end_time,omitempty"`
	Error            string     `json:"error,omitempty"`
	Groups           any        `json:"groups"`                     // JSON blob
	RuntimeSettings  any        `json:"runtime_settings,omitempty"` // JSON blob
	GoalConfig       any        `json:"goal_config,omitempty"`      // JSON blob
}

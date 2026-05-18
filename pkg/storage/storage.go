package storage

import (
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
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
	SaveScenarioSchedule(schedule *ScenarioSchedule) error
	GetScenarioSchedule(id string) (*ScenarioSchedule, error)
	ListScenarioSchedules(scenarioID string) ([]*ScenarioSchedule, error)
	ListAllScenarioSchedules() ([]*ScenarioSchedule, error)
	DeleteScenarioSchedule(id string) error
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
	SaveServiceAccount(account *ServiceAccount) error
	GetServiceAccount(id string) (*ServiceAccount, error)
	GetServiceAccountByKeyHash(keyHash string) (*ServiceAccount, error)
	ListServiceAccounts() ([]*ServiceAccount, error)
	RotateServiceAccountKey(id string, keyHash string, updatedAt time.Time) error
	UpdateServiceAccountLastUsed(id string, lastUsedAt time.Time) error
	UpdateServiceAccountActive(id string, active bool, updatedAt time.Time) error
	DeleteServiceAccount(id string) error
	SaveAuditEvent(event *AuditEvent) error
	ListAuditEvents(filter AuditEventFilter) ([]*AuditEvent, error)

	Close() error
}

type ServiceAccount struct {
	ID                   string              `json:"id"`
	Name                 string              `json:"name"`
	Description          string              `json:"description,omitempty"`
	KeyHash              string              `json:"-"`
	Active               bool                `json:"active"`
	GlobalRoles          []string            `json:"global_roles,omitempty"`
	ProjectRoles         map[string][]string `json:"project_roles,omitempty"`
	Scopes               []string            `json:"scopes,omitempty"`
	CreatedAt            time.Time           `json:"created_at"`
	UpdatedAt            time.Time           `json:"updated_at"`
	LastUsedAt           *time.Time          `json:"last_used_at,omitempty"`
	ExpiresAt            *time.Time          `json:"expires_at,omitempty"`
	KeyRotatedAt         *time.Time          `json:"key_rotated_at,omitempty"`
	RotationIntervalDays int                 `json:"rotation_interval_days,omitempty"`
	RotationDueAt        *time.Time          `json:"rotation_due_at,omitempty"`
}

type AuditEventFilter struct {
	Limit        int
	Offset       int
	Action       string
	ActorType    string
	ProjectID    string
	ResourceType string
	ResourceID   string
	Status       string
}

type AuditEvent struct {
	ID               string         `json:"id"`
	Action           string         `json:"action"`
	ActorType        string         `json:"actor_type"`
	ActorSubject     string         `json:"actor_subject"`
	ActorDisplayName string         `json:"actor_display_name,omitempty"`
	ProjectID        string         `json:"project_id,omitempty"`
	ResourceType     string         `json:"resource_type,omitempty"`
	ResourceID       string         `json:"resource_id,omitempty"`
	Status           string         `json:"status"`
	Details          map[string]any `json:"details,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

type AuthoringSession struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"project_id,omitempty"`
	Source         string         `json:"source"`
	Name           string         `json:"name"`
	Status         string         `json:"status"`
	Draft          *dsl.Script    `json:"draft,omitempty"`
	ReviewState    map[string]any `json:"review_state,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	LastAccessedAt time.Time      `json:"last_accessed_at"`
}

type Application struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id,omitempty"`
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
	StartDelay       string    `json:"start_delay,omitempty"`
	ScenarioMode     string    `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool      `json:"script_shakeout,omitempty"`
	ResourceBaseline bool      `json:"resource_baseline,omitempty"`
	Assignment       any       `json:"assignment,omitempty"`
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
	Iterations     *int   `json:"iterations,omitempty"`
	Initialization string `json:"initialization,omitempty"`
}

type ScenarioSchedule struct {
	ID              string     `json:"id"`
	ScenarioID      string     `json:"scenario_id"`
	Name            string     `json:"name"`
	CronExpr        string     `json:"cron_expr"`
	Timezone        string     `json:"timezone,omitempty"`
	ReleaseID       string     `json:"release_id,omitempty"`
	RunNameTemplate string     `json:"run_name_template,omitempty"`
	Enabled         bool       `json:"enabled"`
	LastRunTestID   string     `json:"last_run_test_id,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type SharedDataPool struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Schema      map[string]string `json:"schema,omitempty"`
	ProjectID   string            `json:"project_id,omitempty"`
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
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Settings  ProjectSettings `json:"settings,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type ProjectSettings struct {
	RedactionRules     []engine.RedactionRule `json:"redaction_rules,omitempty"`
	MaxConcurrentTests int                    `json:"max_concurrent_tests,omitempty"`
	MaxVUsers          int                    `json:"max_vusers,omitempty"`
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
	StartDelay       string     `json:"start_delay,omitempty"`
	Assignment       any        `json:"assignment,omitempty"` // JSON blob
	TotalVUsers      int        `json:"total_vusers"`
	Duration         int64      `json:"duration_ns"`
	StartTime        time.Time  `json:"start_time"`
	PendingSince     *time.Time `json:"pending_since,omitempty"`
	BaselinePinned   bool       `json:"baseline_pinned,omitempty"`
	BaselinePinnedAt *time.Time `json:"baseline_pinned_at,omitempty"`
	EndTime          *time.Time `json:"end_time,omitempty"`
	Error            string     `json:"error,omitempty"`
	Groups           any        `json:"groups"`                     // JSON blob
	RuntimeSettings  any        `json:"runtime_settings,omitempty"` // JSON blob
	GoalConfig       any        `json:"goal_config,omitempty"`      // JSON blob
}

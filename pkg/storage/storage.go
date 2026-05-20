package storage

import (
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

const DefaultProjectID = "unassigned"
const DefaultDomainID = "default"
const DefaultApplicationRepositoryID = "default-application-repository"
const DefaultRepositoryCategoryID = "uncategorized"
const LegacyApplicationID = "legacy"
const LegacyReleaseID = "legacy-imported"

// Store defines the interface for persisting APEX Load data.
type Store interface {
	// Domains and application repository hierarchy
	SaveDomain(domain *Domain) error
	GetDomain(id string) (*Domain, error)
	ListDomains() ([]*Domain, error)
	DeleteDomain(id string) error
	SaveApplicationRepository(repository *ApplicationRepository) error
	GetApplicationRepository(domainID string) (*ApplicationRepository, error)
	SaveRepositoryCategory(category *RepositoryCategory) error
	GetRepositoryCategory(id string) (*RepositoryCategory, error)
	ListRepositoryCategories(domainID string) ([]*RepositoryCategory, error)
	DeleteRepositoryCategory(id string) error

	// Applications and releases
	SaveApplication(application *Application) error
	GetApplication(id string) (*Application, error)
	ListApplications() ([]*Application, error)
	DeleteApplication(id string) error
	SaveRelease(release *Release) error
	GetRelease(id string) (*Release, error)
	ListReleases(applicationID string, includeArchived bool) ([]*Release, error)
	ListReleasesByProject(projectID string, includeArchived bool) ([]*Release, error)
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
	SaveScenarioScheduleOccurrence(occurrence *ScenarioScheduleOccurrence) error
	GetScenarioScheduleOccurrence(id string) (*ScenarioScheduleOccurrence, error)
	GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*ScenarioScheduleOccurrence, error)
	ListScenarioScheduleOccurrences(scheduleID string) ([]*ScenarioScheduleOccurrence, error)
	ListAllScenarioScheduleOccurrences() ([]*ScenarioScheduleOccurrence, error)
	SaveScenarioScheduleApproval(approval *ScenarioScheduleApproval) error
	GetScenarioScheduleApproval(id string) (*ScenarioScheduleApproval, error)
	ListScenarioScheduleApprovals(scheduleID string) ([]*ScenarioScheduleApproval, error)
	TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error)
	GetSchedulerLease(name string) (*SchedulerLease, error)
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
	SaveUser(user *User) error
	GetUser(id string) (*User, error)
	GetUserBySubject(subject string) (*User, error)
	ListUsers(filter UserFilter) ([]*User, error)
	UpdateUserActive(id string, active bool, updatedAt time.Time) error
	DeleteUser(id string) error
	SaveUserRoleBinding(binding *UserRoleBinding) error
	DeleteUserRoleBinding(id string) error
	ListUserRoleBindings(userID string) ([]*UserRoleBinding, error)
	SaveAuditEvent(event *AuditEvent) error
	ListAuditEvents(filter AuditEventFilter) ([]*AuditEvent, error)
	SaveUsageAccountingSnapshot(snapshot *UsageAccountingSnapshot) error
	ListUsageAccountingSnapshots(projectID string, limit int, offset int) ([]*UsageAccountingSnapshot, error)

	Close() error
}

type UserFilter struct {
	Query  string
	Active *bool
	Limit  int
	Offset int
}

type User struct {
	ID          string             `json:"id"`
	Subject     string             `json:"subject"`
	Email       string             `json:"email,omitempty"`
	DisplayName string             `json:"display_name,omitempty"`
	Active      bool               `json:"active"`
	Bindings    []*UserRoleBinding `json:"bindings,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	LastSeenAt  *time.Time         `json:"last_seen_at,omitempty"`
	DisabledAt  *time.Time         `json:"disabled_at,omitempty"`
}

type UserRoleBinding struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ScopeType string    `json:"scope_type"`
	ScopeID   string    `json:"scope_id,omitempty"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ServiceAccount struct {
	ID                   string              `json:"id"`
	Name                 string              `json:"name"`
	Description          string              `json:"description,omitempty"`
	KeyHash              string              `json:"-"`
	Active               bool                `json:"active"`
	GlobalRoles          []string            `json:"global_roles,omitempty"`
	ProjectRoles         map[string][]string `json:"project_roles,omitempty"`
	DomainRoles          map[string][]string `json:"domain_roles,omitempty"`
	RepositoryRoles      map[string][]string `json:"application_repository_roles,omitempty"`
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
	Limit         int
	Offset        int
	Action        string
	ActorType     string
	ActorSubject  string
	ProjectID     string
	DomainID      string
	ResourceType  string
	ResourceID    string
	Status        string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

type AuditEvent struct {
	ID               string         `json:"id"`
	Action           string         `json:"action"`
	ActorType        string         `json:"actor_type"`
	ActorSubject     string         `json:"actor_subject"`
	ActorDisplayName string         `json:"actor_display_name,omitempty"`
	ProjectID        string         `json:"project_id,omitempty"`
	DomainID         string         `json:"domain_id,omitempty"`
	ResourceType     string         `json:"resource_type,omitempty"`
	ResourceID       string         `json:"resource_id,omitempty"`
	Status           string         `json:"status"`
	Details          map[string]any `json:"details,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

type ReleaseSLAWaiver struct {
	ID         string     `json:"id"`
	ReleaseID  string     `json:"release_id"`
	ProjectID  string     `json:"project_id,omitempty"`
	Scope      string     `json:"scope"`
	Reason     string     `json:"reason"`
	ApprovedBy string     `json:"approved_by,omitempty"`
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type ExternalResultImportFilter struct {
	ProjectID string
	ReleaseID string
	Provider  string
	Limit     int
	Offset    int
}

type ExternalResultImport struct {
	ID         string         `json:"id"`
	Provider   string         `json:"provider"`
	Name       string         `json:"name"`
	ProjectID  string         `json:"project_id,omitempty"`
	ReleaseID  string         `json:"release_id,omitempty"`
	ScenarioID string         `json:"scenario_id,omitempty"`
	Source     string         `json:"source,omitempty"`
	Summary    map[string]any `json:"summary,omitempty"`
	RawResult  map[string]any `json:"raw_result,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type UsageAccountingSnapshot struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
	PayloadJSON string    `json:"payload_json"`
	CreatedAt   time.Time `json:"created_at"`
}

type AuthoringSession struct {
	ID                   string         `json:"id"`
	ProjectID            string         `json:"project_id,omitempty"`
	Source               string         `json:"source"`
	Name                 string         `json:"name"`
	Status               string         `json:"status"`
	Draft                *dsl.Script    `json:"draft,omitempty"`
	ReviewState          map[string]any `json:"review_state,omitempty"`
	NextActions          []string       `json:"next_actions,omitempty"`
	BlockingHealthInputs []string       `json:"blocking_health_inputs,omitempty"`
	ApprovalReady        bool           `json:"approval_ready"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	LastAccessedAt       time.Time      `json:"last_accessed_at"`
}

type Domain struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ApplicationRepository struct {
	ID          string    `json:"id"`
	DomainID    string    `json:"domain_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type RepositoryCategory struct {
	ID           string    `json:"id"`
	DomainID     string    `json:"domain_id"`
	RepositoryID string    `json:"repository_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	SortOrder    int       `json:"sort_order,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
	ProjectID     string     `json:"project_id,omitempty"`
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
	ScenarioID     string      `json:"scenario_id,omitempty"`
	Order          int         `json:"order"`
	Name           string      `json:"name,omitempty"`
	ScriptID       string      `json:"script_id"`
	VUsers         int         `json:"vusers"`
	RampUp         string      `json:"ramp_up,omitempty"`
	RampDown       string      `json:"ramp_down,omitempty"`
	Stages         []LoadStage `json:"stages,omitempty"`
	Iterations     *int        `json:"iterations,omitempty"`
	Initialization string      `json:"initialization,omitempty"`
}

type LoadStage struct {
	Name         string   `json:"name,omitempty"`
	Type         string   `json:"type"`
	Duration     string   `json:"duration"`
	TargetVUsers int      `json:"target_vusers,omitempty"`
	TargetTPS    *float64 `json:"target_tps,omitempty"`
}

type ScenarioSchedule struct {
	ID                  string                      `json:"id"`
	ScenarioID          string                      `json:"scenario_id"`
	ProjectID           string                      `json:"project_id,omitempty"`
	Revision            int                         `json:"revision,omitempty"`
	Name                string                      `json:"name"`
	CronExpr            string                      `json:"cron_expr"`
	Timezone            string                      `json:"timezone,omitempty"`
	ReleaseID           string                      `json:"release_id,omitempty"`
	RunNameTemplate     string                      `json:"run_name_template,omitempty"`
	Enabled             bool                        `json:"enabled"`
	ApprovalPolicy      string                      `json:"approval_policy,omitempty"`
	MissedRunPolicy     string                      `json:"missed_run_policy,omitempty"`
	ConflictSummary     string                      `json:"conflict_summary,omitempty"`
	LastOccurrence      *ScenarioScheduleOccurrence `json:"last_occurrence,omitempty"`
	NextRequiredAction  string                      `json:"next_required_action,omitempty"`
	PendingApprovalID   string                      `json:"pending_approval_id,omitempty"`
	OwnerSubject        string                      `json:"owner_subject,omitempty"`
	OwnerDisplayName    string                      `json:"owner_display_name,omitempty"`
	Governance          *ScenarioScheduleGovernance `json:"governance,omitempty"`
	BlackoutWindows     []ScenarioScheduleWindow    `json:"blackout_windows,omitempty"`
	MaintenanceWindows  []ScenarioScheduleWindow    `json:"maintenance_windows,omitempty"`
	AvailabilityWindows []ScenarioScheduleWindow    `json:"availability_windows,omitempty"`
	LastRunTestID       string                      `json:"last_run_test_id,omitempty"`
	LastRunAt           *time.Time                  `json:"last_run_at,omitempty"`
	NextRunAt           *time.Time                  `json:"next_run_at,omitempty"`
	LastError           string                      `json:"last_error,omitempty"`
	CreatedAt           time.Time                   `json:"created_at"`
	UpdatedAt           time.Time                   `json:"updated_at"`
}

type ScenarioScheduleGovernance struct {
	RBACReview    ScenarioScheduleRBACReview  `json:"rbac_review"`
	QuotaReview   ScenarioScheduleQuotaReview `json:"quota_review"`
	PolicyVersion string                      `json:"policy_version,omitempty"`
}

type ScenarioScheduleRBACReview struct {
	Decision      string    `json:"decision"`
	ReviewedAt    time.Time `json:"reviewed_at"`
	ReviewedBy    string    `json:"reviewed_by,omitempty"`
	RequiredRoles []string  `json:"required_roles,omitempty"`
	ReviewSource  string    `json:"review_source,omitempty"`
}

type ScenarioScheduleQuotaReview struct {
	Decision             string   `json:"decision"`
	ProjectMaxConcurrent int      `json:"project_max_concurrent_tests,omitempty"`
	ProjectMaxVUsers     int      `json:"project_max_vusers,omitempty"`
	EstimatedPeakVUsers  int      `json:"estimated_peak_vusers,omitempty"`
	Findings             []string `json:"findings,omitempty"`
}

type ScenarioScheduleWindow struct {
	Name       string `json:"name,omitempty"`
	DaysOfWeek []int  `json:"days_of_week,omitempty"`
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time"`
	Timezone   string `json:"timezone,omitempty"`
}

type ScenarioScheduleOccurrence struct {
	ID               string     `json:"id"`
	ScheduleID       string     `json:"schedule_id"`
	ScenarioID       string     `json:"scenario_id"`
	ProjectID        string     `json:"project_id,omitempty"`
	ScheduleRevision int        `json:"schedule_revision,omitempty"`
	PlannedAt        time.Time  `json:"planned_at"`
	MaterializedAt   *time.Time `json:"materialized_at,omitempty"`
	Status           string     `json:"status"`
	ReasonCode       string     `json:"reason_code,omitempty"`
	TestID           string     `json:"test_id,omitempty"`
	ApprovalID       string     `json:"approval_id,omitempty"`
	ConflictSummary  string     `json:"conflict_summary,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type ScenarioScheduleApproval struct {
	ID               string     `json:"id"`
	ScheduleID       string     `json:"schedule_id"`
	ScenarioID       string     `json:"scenario_id"`
	ProjectID        string     `json:"project_id,omitempty"`
	ScheduleRevision int        `json:"schedule_revision,omitempty"`
	Policy           string     `json:"policy"`
	Status           string     `json:"status"`
	RequestedBy      string     `json:"requested_by,omitempty"`
	RequestedAt      time.Time  `json:"requested_at"`
	ResolvedBy       string     `json:"resolved_by,omitempty"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	DecisionReason   string     `json:"decision_reason,omitempty"`
	RequiredRole     string     `json:"required_role,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type SchedulerLease struct {
	Name       string    `json:"name"`
	OwnerID    string    `json:"owner_id"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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
	ID         string          `json:"id"`
	DomainID   string          `json:"domain_id,omitempty"`
	CategoryID string          `json:"category_id,omitempty"`
	Name       string          `json:"name"`
	Settings   ProjectSettings `json:"settings,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
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
	ArchiveBackend       string    `json:"archive_backend,omitempty"`
	ArchivePath          string    `json:"archive_path"`
	ManifestPath         string    `json:"manifest_path"`
	ArchiveURI           string    `json:"archive_uri,omitempty"`
	ManifestURI          string    `json:"manifest_uri,omitempty"`
	TransactionCount     int64     `json:"transaction_count"`
	ResourceSampleCount  int64     `json:"resource_sample_count"`
	IntervalSummaryCount int64     `json:"interval_summary_count"`
	CreatedAt            time.Time `json:"created_at"`
	CompletedAt          time.Time `json:"completed_at"`
}

// TestRecord represents a persisted test run.
type TestRecord struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	Status               string     `json:"status"`
	ApplicationID        string     `json:"application_id,omitempty"`
	ReleaseID            string     `json:"release_id,omitempty"`
	ScenarioID           string     `json:"scenario_id,omitempty"`
	ProjectID            string     `json:"project_id,omitempty"`
	FolderID             *string    `json:"folder_id,omitempty"`
	ScenarioMode         string     `json:"scenario_mode,omitempty"`
	ScriptShakeout       bool       `json:"script_shakeout,omitempty"`
	ResourceBaseline     bool       `json:"resource_baseline,omitempty"`
	StartDelay           string     `json:"start_delay,omitempty"`
	Assignment           any        `json:"assignment,omitempty"` // JSON blob
	TotalVUsers          int        `json:"total_vusers"`
	Duration             int64      `json:"duration_ns"`
	StartTime            time.Time  `json:"start_time"`
	PendingSince         *time.Time `json:"pending_since,omitempty"`
	BaselinePinned       bool       `json:"baseline_pinned,omitempty"`
	BaselinePinnedAt     *time.Time `json:"baseline_pinned_at,omitempty"`
	EndTime              *time.Time `json:"end_time,omitempty"`
	Error                string     `json:"error,omitempty"`
	CreatedBySubject     string     `json:"created_by_subject,omitempty"`
	CreatedByDisplayName string     `json:"created_by_display_name,omitempty"`
	StartedBySubject     string     `json:"started_by_subject,omitempty"`
	StartedByDisplayName string     `json:"started_by_display_name,omitempty"`
	Groups               any        `json:"groups"`                     // JSON blob
	RuntimeSettings      any        `json:"runtime_settings,omitempty"` // JSON blob
	GoalConfig           any        `json:"goal_config,omitempty"`      // JSON blob
}

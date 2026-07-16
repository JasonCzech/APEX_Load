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
	DeleteScenarioScheduleOccurrence(id string) error
	SaveScenarioScheduleApproval(approval *ScenarioScheduleApproval) error
	GetScenarioScheduleApproval(id string) (*ScenarioScheduleApproval, error)
	ListScenarioScheduleApprovals(scheduleID string) ([]*ScenarioScheduleApproval, error)
	TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error)
	GetSchedulerLease(name string) (*SchedulerLease, error)
	SaveScenarioDataPoolBindings(scenarioID string, bindings []ScenarioDataPoolBinding) error
	ListScenarioDataPoolBindings(scenarioID string) ([]ScenarioDataPoolBinding, error)
	DeleteScenarioDataPoolBinding(id string) error

	// Capacity reservations (timeslots) — forward-booking of a future window.
	SaveCapacityReservation(reservation *CapacityReservation) error
	GetCapacityReservation(id string) (*CapacityReservation, error)
	ListCapacityReservations(projectID string, limit int, offset int) ([]*CapacityReservation, error)
	ListCapacityReservationsByTimeRange(projectID string, startAt, endAt time.Time) ([]*CapacityReservation, error)
	// UpdateCapacityReservationDetails updates editor-owned reservation fields
	// without rewriting approval/binding state from a stale read. When resetApproval
	// is true, approval and binding are deliberately cleared for changed terms.
	UpdateCapacityReservationDetails(reservation *CapacityReservation, resetApproval bool) error
	// UpdateCapacityReservationBinding atomically binds both the reservation and
	// test rows only if neither side is already bound; the bool reports success.
	UpdateCapacityReservationBinding(id string, testID *string, testStatus *string, updatedAt time.Time) (bool, error)
	// ClearCapacityReservationBinding releases a reservation only if it is still
	// bound to the supplied test id.
	ClearCapacityReservationBinding(id string, testID string, updatedAt time.Time) (bool, error)
	// UpdateCapacityReservationApprovalState atomically flips approval_state only
	// when the row is currently in one of fromStates, closing the read-modify-write
	// TOCTOU in the approve/reject handler. Returns false if no row matched.
	UpdateCapacityReservationApprovalState(id string, fromStates []string, toState string, updatedAt time.Time) (bool, error)
	// DeleteCapacityReservationsEndedBefore prunes reservations whose window has
	// fully elapsed (end_at < cutoff). Returns the number of rows removed.
	DeleteCapacityReservationsEndedBefore(cutoff time.Time) (int64, error)
	// DeleteRejectedCapacityReservationsBefore prunes rejected reservations last
	// touched before cutoff. Returns the number of rows removed.
	DeleteRejectedCapacityReservationsBefore(cutoff time.Time) (int64, error)
	DeleteCapacityReservation(id string) error

	// Fleet pools, generator metadata, and cloud provisioning.
	SaveFleetPool(pool *FleetPool) error
	GetFleetPool(id string) (*FleetPool, error)
	ListFleetPools(limit int, offset int) ([]*FleetPool, error)
	// DeleteFleetPool removes the pool and clears the pool reference from its
	// members and generator metadata (application-level integrity).
	DeleteFleetPool(id string) error
	SaveFleetPoolMembers(poolID string, members []*FleetPoolMember) error
	ListFleetPoolMembers(poolID string) ([]*FleetPoolMember, error)
	SaveGeneratorMetadata(metadata *GeneratorMetadata) error
	GetGeneratorMetadata(generatorID string) (*GeneratorMetadata, error)
	ListGeneratorMetadata(limit int, offset int) ([]*GeneratorMetadata, error)
	DeleteGeneratorMetadata(generatorID string) error
	SaveProvisioningRequest(req *ProvisioningRequest) error
	GetProvisioningRequest(id string) (*ProvisioningRequest, error)
	ListProvisioningRequests(fleetPoolID string, limit int, offset int) ([]*ProvisioningRequest, error)
	SaveProvisioningEvent(event *ProvisioningEvent) error
	ListProvisioningEvents(requestID string) ([]*ProvisioningEvent, error)
	SaveFleetAlertAck(ack *FleetAlertAck) error
	DeleteFleetAlertAck(id string) error
	ListFleetAlertAcks() ([]*FleetAlertAck, error)

	// Execution plans (ordered cross-run orchestration).
	SaveExecutionPlan(plan *ExecutionPlan) error
	GetExecutionPlan(id string) (*ExecutionPlan, error)
	ListExecutionPlans(projectID string, limit int, offset int) ([]*ExecutionPlan, error)
	DeleteExecutionPlan(id string) error

	// Execution plan runs (per-materialization progress; updated at each transition).
	SaveExecutionPlanRun(run *ExecutionPlanRun) error
	GetExecutionPlanRun(id string) (*ExecutionPlanRun, error)
	ListExecutionPlanRuns(planID string, limit int, offset int) ([]*ExecutionPlanRun, error)

	// Execution plan schedules (recurring plan triggers) + their occurrences.
	SaveExecutionPlanSchedule(schedule *ExecutionPlanSchedule) error
	GetExecutionPlanSchedule(id string) (*ExecutionPlanSchedule, error)
	ListExecutionPlanSchedules(planID string) ([]*ExecutionPlanSchedule, error)
	ListAllExecutionPlanSchedules() ([]*ExecutionPlanSchedule, error)
	DeleteExecutionPlanSchedule(id string) error
	CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence *ExecutionPlanScheduleOccurrence) (bool, error)
	SaveExecutionPlanScheduleOccurrence(occurrence *ExecutionPlanScheduleOccurrence) error
	GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*ExecutionPlanScheduleOccurrence, error)
	ListExecutionPlanScheduleOccurrences(scheduleID string) ([]*ExecutionPlanScheduleOccurrence, error)
	ListAllExecutionPlanScheduleOccurrences() ([]*ExecutionPlanScheduleOccurrence, error)

	// Execution plan run approvals (governance gate).
	SaveExecutionPlanApproval(approval *ExecutionPlanApproval) error
	GetExecutionPlanApproval(id string) (*ExecutionPlanApproval, error)
	ListExecutionPlanApprovals(planID string) ([]*ExecutionPlanApproval, error)
	// ClaimExecutionPlanApproval atomically consumes an approved, unconsumed
	// approval (single-use); UpdateExecutionPlanApprovalResolution atomically
	// resolves a still-pending approval. Both report whether they won the race.
	ClaimExecutionPlanApproval(id, runID string, updatedAt time.Time) (bool, error)
	UpdateExecutionPlanApprovalResolution(id, toStatus, resolvedBy, reason string, resolvedAt time.Time) (bool, error)

	// Object repositories (project-scoped reusable element-descriptor catalogs).
	SaveObjectRepository(repo *ObjectRepository) error
	GetObjectRepository(id string) (*ObjectRepository, error)
	ListObjectRepositories(projectID string, limit int, offset int) ([]*ObjectRepository, error)
	DeleteObjectRepository(id string) error

	// Correlation rulesets: project-scoped catalogs of learned correlation rules.
	SaveCorrelationRuleset(ruleset *CorrelationRuleset) error
	GetCorrelationRuleset(id string) (*CorrelationRuleset, error)
	ListCorrelationRulesets(projectID string, limit int, offset int) ([]*CorrelationRuleset, error)
	DeleteCorrelationRuleset(id string) error

	// Monitoring collectors (restricted-network agent control plane).
	SaveMonitoringCollector(collector *MonitoringCollector) error
	GetMonitoringCollector(id string) (*MonitoringCollector, error)
	ListMonitoringCollectors(projectID string, limit int, offset int) ([]*MonitoringCollector, error)
	DeleteMonitoringCollector(id string) error
	// UpdateMonitoringCollectorHealth atomically records a collector heartbeat
	// (health + last-seen) without rewriting the rest of the row.
	UpdateMonitoringCollectorHealth(id string, health string, lastSeenAt time.Time) error

	// Reusable blocks: user-saved authoring components, scoped per project.
	SaveReusableBlock(block *ReusableBlock) error
	GetReusableBlock(id string) (*ReusableBlock, error)
	ListReusableBlocks(projectID string) ([]*ReusableBlock, error)
	DeleteReusableBlock(id string) error

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

	// Recorded baselines (the original recorded exchanges a script was generated
	// from, used to diagnose record-vs-replay correlation issues).
	SaveScriptRecording(record *ScriptRecordingRecord) error
	GetScriptRecording(scriptID string) (*ScriptRecordingRecord, error)
	DeleteScriptRecording(scriptID string) error

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
	CreateTestNote(note *TestNote) error
	ListTestNotes(testID string) ([]*TestNote, error)
	GetTestNote(id string) (*TestNote, error)
	UpdateTestNote(note *TestNote) error
	DeleteTestNote(id string) error

	// System settings
	SaveSetting(key string, value string) error
	GetSetting(key string) (string, error)
	SaveServiceAccount(account *ServiceAccount) error
	// UpdateServiceAccountMetadata atomically updates non-secret service-account
	// fields only when the row still has expectedUpdatedAt. It deliberately never
	// writes key material, key-rotation state, or last-used state. A false result
	// means the account was deleted or changed concurrently.
	UpdateServiceAccountMetadata(account *ServiceAccount, expectedUpdatedAt time.Time) (bool, error)
	GetServiceAccount(id string) (*ServiceAccount, error)
	GetServiceAccountByKeyHash(keyHash string) (*ServiceAccount, error)
	ListServiceAccounts() ([]*ServiceAccount, error)
	RotateServiceAccountKey(id string, keyHash string, updatedAt time.Time) error
	UpdateServiceAccountLastUsed(id string, lastUsedAt time.Time) error
	UpdateServiceAccountActive(id string, active bool, updatedAt time.Time) error
	DeleteServiceAccount(id string) error
	SaveUser(user *User) error
	// UpdateUserGuardLastAdmin atomically updates administrator-managed profile
	// fields while refusing any change that would make the last active global
	// system administrator unreachable or inactive.
	UpdateUserGuardLastAdmin(user *User) (bool, error)
	UpdateUserIdentitySeen(id string, displayName string, email string, lastSeenAt time.Time, updatedAt time.Time) error
	GetUser(id string) (*User, error)
	GetUserBySubject(subject string) (*User, error)
	ListUsers(filter UserFilter) ([]*User, error)
	// CountUsers returns the total number of users matching the filter's query/active
	// predicates, ignoring limit/offset — used to report a real total for pagination.
	CountUsers(filter UserFilter) (int, error)
	UpdateUserActive(id string, active bool, updatedAt time.Time) error
	// BumpUserSessionEpoch increments the user's session epoch (revoking existing OIDC
	// sessions) and returns the new value.
	BumpUserSessionEpoch(id string, updatedAt time.Time) (int, error)
	DeleteUser(id string) error
	// CountActiveGlobalRoleHolders returns the number of distinct active users holding
	// the given role via a global-scope binding. Used to prevent removing the last
	// system administrator (self-lockout protection).
	CountActiveGlobalRoleHolders(role string) (int, error)
	SaveUserRoleBinding(binding *UserRoleBinding) error
	DeleteUserRoleBinding(id string) error
	ListUserRoleBindings(userID string) ([]*UserRoleBinding, error)
	SaveAuditEvent(event *AuditEvent) error
	ListAuditEvents(filter AuditEventFilter) ([]*AuditEvent, error)
	// DeleteAuditEventsBefore prunes audit rows created strictly before cutoff and
	// returns how many were removed (retention enforcement).
	DeleteAuditEventsBefore(cutoff time.Time) (int64, error)
	SaveUsageAccountingSnapshot(snapshot *UsageAccountingSnapshot) error
	ListUsageAccountingSnapshots(projectID string, limit int, offset int) ([]*UsageAccountingSnapshot, error)
	// DeleteUsageAccountingSnapshotsBefore prunes snapshot rows created strictly
	// before cutoff and returns how many were removed (retention enforcement).
	DeleteUsageAccountingSnapshotsBefore(cutoff time.Time) (int64, error)

	Close() error
}

type UserFilter struct {
	Query  string
	Active *bool
	Role   string
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
	// SessionEpoch is a monotonic counter embedded in the user's OIDC session cookie
	// at login and compared on each request; bumping it (BumpUserSessionEpoch) revokes
	// that user's existing sessions without disabling the account or rotating the
	// global session secret.
	SessionEpoch int `json:"session_epoch,omitempty"`
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
	OwnerID              string              `json:"owner_id,omitempty"`
	ContactEmail         string              `json:"contact_email,omitempty"`
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

// GitHubConnection is a per-user GitHub OAuth connection. Token fields hold
// already-encrypted ciphertext — the storage layer never sees plaintext tokens;
// encryption/decryption happens in the API layer.
type GitHubConnection struct {
	UserID          string     `json:"user_id"`
	GitHubLogin     string     `json:"github_login"`
	GitHubUserID    string     `json:"github_user_id"`
	AccessTokenEnc  string     `json:"-"`
	RefreshTokenEnc string     `json:"-"`
	TokenType       string     `json:"token_type,omitempty"`
	Scopes          string     `json:"scopes,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	ConnectedAt     time.Time  `json:"connected_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// GitHubConnectionStore is an optional capability (type-asserted from Store,
// like BackendInfoProvider) for persisting per-user GitHub OAuth connections.
type GitHubConnectionStore interface {
	SaveGitHubConnection(conn *GitHubConnection) error
	// GetGitHubConnection returns (nil, nil) when the user has no connection.
	GetGitHubConnection(userID string) (*GitHubConnection, error)
	DeleteGitHubConnection(userID string) error
}

// GitHubScriptLink records the GitHub location a local script is synced to. It is
// keyed by ScriptID and drives idempotent push (reusing LastCommitSHA for
// optimistic concurrency), drift detection (ContentHash vs local, LastCommitSHA
// vs remote), and the "View on GitHub" deep link.
type GitHubScriptLink struct {
	ScriptID        string
	RefKey          string
	Owner           string
	Repo            string
	Branch          string
	Path            string
	LastCommitSHA   string
	RemoteBlobSHA   string
	RemoteCommitSHA string
	ContentHash     string
	SyncedAt        time.Time
	UpdatedAt       time.Time
}

// GitHubScriptLinkStore is an optional capability (type-asserted from Store) for
// persisting the local-script ↔ GitHub-file mapping.
type GitHubScriptLinkStore interface {
	SaveGitHubScriptLink(link *GitHubScriptLink) error
	// GetGitHubScriptLink returns (nil, nil) when the script has no link.
	GetGitHubScriptLink(scriptID string) (*GitHubScriptLink, error)
	DeleteGitHubScriptLink(scriptID string) error
}

type GitHubProjectPolicy struct {
	Enabled              bool                `json:"enabled,omitempty"`
	AllowedRepos         []string            `json:"allowed_repos,omitempty"`
	AllowedBranches      []string            `json:"allowed_branches,omitempty"`
	PathTemplate         string              `json:"path_template,omitempty"`
	RequirePullRequest   bool                `json:"require_pull_request,omitempty"`
	AllowDirectCommit    bool                `json:"allow_direct_commit,omitempty"`
	AllowImport          bool                `json:"allow_import,omitempty"`
	AllowPush            bool                `json:"allow_push,omitempty"`
	BulkSyncEnabled      bool                `json:"bulk_sync_enabled,omitempty"`
	AutoSyncEnabled      bool                `json:"auto_sync_enabled,omitempty"`
	ChecksEnabled        bool                `json:"checks_enabled,omitempty"`
	AppInstallationID    int64               `json:"app_installation_id,omitempty"`
	AutomationIdentity   string              `json:"automation_identity,omitempty"`
	DefaultRepo          string              `json:"default_repo,omitempty"`
	DefaultBranch        string              `json:"default_branch,omitempty"`
	DefaultSyncDirectory string              `json:"default_sync_directory,omitempty"`
	Actions              GitHubActionsPolicy `json:"actions,omitempty"`
}

// GitHubActionsPolicy is the project-scoped trust boundary for controller-backed
// GitHub Actions runs. It is deliberately separate from the source-sync policy
// above: enabling repository import or push must never implicitly authorize a
// workflow to start load against project targets.
type GitHubActionsPolicy struct {
	Enabled                   bool      `json:"enabled,omitempty"`
	Issuer                    string    `json:"issuer,omitempty"`
	ServerURL                 string    `json:"server_url,omitempty"`
	Audience                  string    `json:"audience,omitempty"`
	AllowedRepositoryIDs      []string  `json:"allowed_repository_ids,omitempty"`
	AllowedRepositories       []string  `json:"allowed_repositories,omitempty"`
	AllowedRefs               []string  `json:"allowed_refs,omitempty"`
	AllowedWorkflowRefs       []string  `json:"allowed_workflow_refs,omitempty"`
	AllowedEnvironments       []string  `json:"allowed_environments,omitempty"`
	RequiredRunnerEnvironment string    `json:"required_runner_environment,omitempty"`
	MaxVUsers                 int       `json:"max_vusers,omitempty"`
	MaxDurationMinutes        int       `json:"max_duration_minutes,omitempty"`
	AllowedProtocols          []string  `json:"allowed_protocols,omitempty"`
	AllowedTargetHosts        []string  `json:"allowed_target_hosts,omitempty"`
	ChecksEnabled             bool      `json:"checks_enabled,omitempty"`
	Revision                  int64     `json:"revision,omitempty"`
	UpdatedAt                 time.Time `json:"updated_at,omitempty"`
	UpdatedBy                 string    `json:"updated_by,omitempty"`
}

type GitHubAppInstallation struct {
	InstallationID      int64      `json:"installation_id"`
	AccountLogin        string     `json:"account_login"`
	AccountType         string     `json:"account_type"`
	RepositorySelection string     `json:"repository_selection"`
	PermissionsJSON     string     `json:"permissions_json,omitempty"`
	EventsJSON          string     `json:"events_json,omitempty"`
	HTMLURL             string     `json:"html_url,omitempty"`
	SuspendedAt         *time.Time `json:"suspended_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type GitHubSyncJob struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Owner       string     `json:"owner,omitempty"`
	Repo        string     `json:"repo,omitempty"`
	Branch      string     `json:"branch,omitempty"`
	Path        string     `json:"path,omitempty"`
	Total       int        `json:"total"`
	Processed   int        `json:"processed"`
	Created     int        `json:"created"`
	Updated     int        `json:"updated"`
	Conflicts   int        `json:"conflicts"`
	Errors      int        `json:"errors"`
	SummaryJSON string     `json:"summary_json,omitempty"`
	StartedBy   string     `json:"started_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type GitHubWebhookDelivery struct {
	DeliveryID  string     `json:"delivery_id"`
	Event       string     `json:"event"`
	Action      string     `json:"action,omitempty"`
	Owner       string     `json:"owner,omitempty"`
	Repo        string     `json:"repo,omitempty"`
	Ref         string     `json:"ref,omitempty"`
	CommitSHA   string     `json:"commit_sha,omitempty"`
	Status      string     `json:"status"`
	SummaryJSON string     `json:"summary_json,omitempty"`
	ReceivedAt  time.Time  `json:"received_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

type GitHubCheckLink struct {
	ID          string `json:"id"`
	Kind        string `json:"kind,omitempty"`
	TestID      string `json:"test_id"`
	ScriptID    string `json:"script_id,omitempty"`
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	CommitSHA   string `json:"commit_sha"`
	CheckRunID  int64  `json:"check_run_id"`
	CheckRunURL string `json:"check_run_url,omitempty"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion,omitempty"`
	// PendingConclusion is the durable, compare-and-set terminal conclusion
	// claimed before the controller calls GitHub. It is intentionally not
	// exposed through the API: callers should only observe GitHub's status and
	// the final persisted conclusion.
	PendingConclusion string    `json:"-"`
	PublishedAt       time.Time `json:"published_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// GitHubCheckIntent is the durable outbox record created before a controller-
// backed CI test is materialized. It closes the crash window between accepting
// a required Check and persisting the GitHub Check link.
type GitHubCheckIntent struct {
	TestID                 string     `json:"test_id"`
	ProjectID              string     `json:"project_id"`
	InstallationID         int64      `json:"installation_id"`
	Owner                  string     `json:"owner"`
	Repo                   string     `json:"repo"`
	CommitSHA              string     `json:"commit_sha"`
	CreationClaim          string     `json:"-"`
	CreationClaimExpiresAt *time.Time `json:"-"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type GitHubEnterpriseStore interface {
	SaveGitHubAppInstallation(installation *GitHubAppInstallation) error
	GetGitHubAppInstallation(installationID int64) (*GitHubAppInstallation, error)
	ListGitHubAppInstallations() ([]*GitHubAppInstallation, error)
	SaveGitHubSyncJob(job *GitHubSyncJob) error
	GetGitHubSyncJob(id string) (*GitHubSyncJob, error)
	ListGitHubSyncJobs(projectID string, limit int) ([]*GitHubSyncJob, error)
	SaveGitHubWebhookDelivery(delivery *GitHubWebhookDelivery) error
	GetGitHubWebhookDelivery(deliveryID string) (*GitHubWebhookDelivery, error)
	ListGitHubWebhookDeliveries(limit int) ([]*GitHubWebhookDelivery, error)
	SaveGitHubCheckLink(link *GitHubCheckLink) error
	SaveGitHubCheckLinkIfCreationClaimHeld(link *GitHubCheckLink, claimant string) (bool, error)
	GetGitHubCheckLink(id string) (*GitHubCheckLink, error)
	ListGitHubCheckLinks(testID string, scriptID string, limit int) ([]*GitHubCheckLink, error)
	ListIncompleteGitHubCheckLinks(limit int) ([]*GitHubCheckLink, error)
	TouchGitHubCheckLink(id string, updatedAt time.Time) error
	ClaimGitHubCheckConclusion(id string, conclusion string) (*GitHubCheckLink, error)
	SaveGitHubCheckIntent(intent *GitHubCheckIntent) error
	GetGitHubCheckIntent(testID string) (*GitHubCheckIntent, error)
	ListPendingGitHubCheckIntents(limit int) ([]*GitHubCheckIntent, error)
	TouchGitHubCheckIntent(testID string, updatedAt time.Time) error
	ClaimGitHubCheckCreation(testID string, claimant string, lease time.Duration) (bool, error)
	RenewGitHubCheckCreation(testID string, claimant string, lease time.Duration) (bool, error)
	ReleaseGitHubCheckCreation(testID string, claimant string) error
	DeleteGitHubCheckIntent(testID string) error
}

type AuditEventFilter struct {
	Limit        int
	Offset       int
	Action       string
	ActorType    string
	ActorSubject string
	ProjectID    string
	// ProjectIDs, when non-nil, restricts results to events whose project_id is in
	// the set — an authorization allow-list applied on top of the other filters. It is
	// the load-bearing scope guard for non-global admins reading user activity; nil
	// means unrestricted/global visibility, while an empty slice means authorized-none.
	ProjectIDs    []string
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
	RefKey           string    `json:"ref_key,omitempty"`
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
	// Role distinguishes load groups ("load", default) from browser experience
	// canaries ("observer"); validated against the script protocol at test
	// creation.
	Role string `json:"role,omitempty"`
	// RuntimeSettings holds per-group runtime overrides as an opaque JSON value
	// (mirrors Scenario.RuntimeSettings); decoded into *RuntimeSettingsOverride upstream.
	RuntimeSettings any `json:"runtime_settings,omitempty"`
	// Assignment holds per-group generator placement overrides as opaque JSON
	// (mirrors Scenario.Assignment); decoded into *GeneratorAssignmentSettings upstream.
	Assignment any `json:"assignment,omitempty"`
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
	TimeslotID          string                      `json:"timeslot_id,omitempty"`
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
	// DisabledFrom/DisabledUntil define an optional date-range pause: while a
	// candidate run time falls within [DisabledFrom, DisabledUntil) the schedule
	// is suppressed without disabling it outright. Either bound may be nil
	// (open-ended pause start/end).
	DisabledFrom  *time.Time `json:"disabled_from,omitempty"`
	DisabledUntil *time.Time `json:"disabled_until,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
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
	ID               string `json:"id"`
	ScheduleID       string `json:"schedule_id"`
	ScenarioID       string `json:"scenario_id"`
	ProjectID        string `json:"project_id,omitempty"`
	ScheduleRevision int    `json:"schedule_revision,omitempty"`
	Revision         int64  `json:"revision,omitempty"`
	// TransitionToken is an internal compare-and-swap fence. It must never be
	// exposed through the REST representations that marshal this storage type.
	TransitionToken string     `json:"-"`
	PlannedAt       time.Time  `json:"planned_at"`
	MaterializedAt  *time.Time `json:"materialized_at,omitempty"`
	Status          string     `json:"status"`
	ReasonCode      string     `json:"reason_code,omitempty"`
	TestID          string     `json:"test_id,omitempty"`
	ApprovalID      string     `json:"approval_id,omitempty"`
	ConflictSummary string     `json:"conflict_summary,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ScheduleDeleteResult distinguishes a missing schedule from a schedule whose
// durable occurrence anchor is still in flight. Callers must surface the latter
// as a conflict; cascading it would erase the scheduler's idempotency fence.
type ScheduleDeleteResult string

const (
	ScheduleDeleteDeleted                   ScheduleDeleteResult = "deleted"
	ScheduleDeleteMissing                   ScheduleDeleteResult = "missing"
	ScheduleDeleteBlockedByActiveOccurrence ScheduleDeleteResult = "blocked_active_occurrence"
)

// ScheduleOccurrenceDeleteResult permits pruning only terminal history. An
// active occurrence is the durable correlation boundary for materialization and
// cannot be removed safely while a test/run side effect may still be pending.
type ScheduleOccurrenceDeleteResult string

const (
	ScheduleOccurrenceDeleteDeleted         ScheduleOccurrenceDeleteResult = "deleted"
	ScheduleOccurrenceDeleteMissing         ScheduleOccurrenceDeleteResult = "missing"
	ScheduleOccurrenceDeleteBlockedByStatus ScheduleOccurrenceDeleteResult = "blocked_nonterminal_status"
)

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

// CapacityReservation is a forward-booking of a capacity window (the
// LoadRunner Enterprise "timeslot" analog). It reuses the project/approval
// vocabulary of scenario schedules: ApprovalState moves pending → approved →
// rejected, and a reservation may be bound to exactly one test run.
type CapacityReservation struct {
	ID                string                      `json:"id"`
	ProjectID         string                      `json:"project_id"`
	ScenarioID        *string                     `json:"scenario_id,omitempty"`
	StartAt           time.Time                   `json:"start_at"`
	EndAt             time.Time                   `json:"end_at"`
	Timezone          string                      `json:"timezone,omitempty"`
	RequestedCapacity *CapacityReservationRequest `json:"requested_capacity,omitempty"`
	ApprovalState     string                      `json:"approval_state"`
	ApprovalID        *string                     `json:"approval_id,omitempty"`
	BoundTestID       *string                     `json:"bound_test_id,omitempty"`
	BoundTestStatus   *string                     `json:"bound_test_status,omitempty"`
	ConflictSummary   string                      `json:"conflict_summary,omitempty"`
	CreatedAt         time.Time                   `json:"created_at"`
	UpdatedAt         time.Time                   `json:"updated_at"`
}

// CapacityReservationRequest is the requested capacity envelope for a timeslot.
type CapacityReservationRequest struct {
	Generators *int   `json:"generators,omitempty"`
	VUsers     *int   `json:"vusers,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	LoadZone   string `json:"load_zone,omitempty"`
	// PoolID scopes a forward-booked timeslot to a specific fleet pool's capacity
	// envelope. Empty means a global timeslot (unchanged). Stored in the
	// requested_capacity_json blob, so no schema migration is required.
	PoolID string `json:"pool_id,omitempty"`
}

type SchedulerLease struct {
	Name       string    `json:"name"`
	OwnerID    string    `json:"owner_id"`
	Generation uint64    `json:"generation"`
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
	GitHub             GitHubProjectPolicy    `json:"github,omitempty"`
	// Per-test hard caps (reject at creation, not queue). MaxVUsersPerTest bounds
	// a single test's VUser count; MaxTestDurationMinutes bounds a single test's
	// duration. Zero means no limit.
	MaxVUsersPerTest       int `json:"max_vusers_per_test,omitempty"`
	MaxTestDurationMinutes int `json:"max_test_duration_minutes,omitempty"`
	// PoolQuotas caps this project's footprint on a specific fleet pool, keyed by
	// fleet-pool ID. It is an ADDITIVE admission gate: a pool-assigned run must pass
	// both the project-wide limits above and the matching pool quota here. Zero
	// fields mean no limit; an absent key means no pool-specific cap.
	PoolQuotas map[string]PoolProjectQuota `json:"pool_quotas,omitempty"`
}

// PoolProjectQuota bounds a single project's concurrent footprint on one fleet
// pool (admission-time, queue-on-exceed — same semantics as the project-wide
// concurrency/VUser limits).
type PoolProjectQuota struct {
	MaxVUsers          int `json:"max_vusers,omitempty"`
	MaxConcurrentTests int `json:"max_concurrent_tests,omitempty"`
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
	RefKey               string    `json:"ref_key,omitempty"`
	SchemaVersion        string    `json:"schema_version"`
	ArchiveBackend       string    `json:"archive_backend,omitempty"`
	ArchivePath          string    `json:"archive_path"`
	ManifestPath         string    `json:"manifest_path"`
	ManifestSHA256       string    `json:"manifest_sha256,omitempty"`
	ArchiveURI           string    `json:"archive_uri,omitempty"`
	ManifestURI          string    `json:"manifest_uri,omitempty"`
	TransactionCount     int64     `json:"transaction_count"`
	ResourceSampleCount  int64     `json:"resource_sample_count"`
	IntervalSummaryCount int64     `json:"interval_summary_count"`
	CreatedAt            time.Time `json:"created_at"`
	CompletedAt          time.Time `json:"completed_at"`
}

// ArchiveCleanupJob is a durable outbox entry for archive object deletion.
// Test and project metadata can be removed transactionally, while the serialized
// archive record remains available until physical backend/local cleanup succeeds.
type ArchiveCleanupJob struct {
	TestID        string           `json:"test_id"`
	ProjectID     string           `json:"project_id"`
	ArchiveRecord RunArchiveRecord `json:"archive_record"`
	Attempts      int              `json:"attempts"`
	LastError     string           `json:"last_error,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// ArchiveVersionCleanupJob durably retains a superseded immutable object-store
// publication until its exact URI prefix has been removed. It is separate from
// ArchiveCleanupJob because one test can accumulate several retired versions,
// while test/project deletion has only one current catalog pointer.
type ArchiveVersionCleanupJob struct {
	ID            string           `json:"id"`
	TestID        string           `json:"test_id"`
	ProjectID     string           `json:"project_id"`
	ArchiveRecord RunArchiveRecord `json:"archive_record"`
	Attempts      int              `json:"attempts"`
	LastError     string           `json:"last_error,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// ArchiveFinalizationJob is the durable intent to finish publishing an archive
// and commit its catalog record. The local archive is already committed before
// this row is written; retries may therefore safely repeat adjunct generation,
// object upload, and the final RunArchiveRecord UPSERT after a restart.
type ArchiveFinalizationJob struct {
	TestID           string           `json:"test_id"`
	ProjectID        string           `json:"project_id"`
	ArchiveRecord    RunArchiveRecord `json:"archive_record"`
	PersistArtifacts bool             `json:"persist_artifacts"`
	Generation       int64            `json:"generation"`
	// IntentToken is regenerated for every logical upsert. Unlike Generation,
	// it cannot suffer ABA when a completed row is deleted and later recreated.
	IntentToken string `json:"-"`
	// RefreshToken is the archive-refresh intent observed while this publication
	// was created. Completion conditionally acknowledges only that token.
	RefreshToken string    `json:"-"`
	Attempts     int       `json:"attempts"`
	LastError    string    `json:"last_error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ArchiveRefreshJob records that archive adjunct artifacts (currently test
// notes and the generated manifest) must be rebuilt. It deliberately carries
// no archive pointer: note CRUD can commit the dirty bit even before an archive
// exists, and the leader resolves the current catalog/finalization record while
// holding the per-test publication fence.
type ArchiveRefreshJob struct {
	TestID      string    `json:"test_id"`
	ProjectID   string    `json:"project_id"`
	Generation  int64     `json:"generation"`
	IntentToken string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type TestNote struct {
	ID        string    `json:"id"`
	TestID    string    `json:"test_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedBy string    `json:"created_by,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// ScriptRecordingObservation is one recorded request/response exchange in a
// script's baseline. It mirrors correlate.Observation but keeps the storage
// layer free of a dependency on the convert/correlate package.
type ScriptRecordingObservation struct {
	ID              int               `json:"id"`
	RequestURL      string            `json:"request_url,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
}

// ScriptRecordingRecord is a per-script recorded baseline: the original recorded
// exchanges that the script was generated from, keyed by the DSL action index
// each exchange produced. Observations is the scrubbed persistence view;
// CorrelationObservations is a server-side raw sidecar used for diagnosis.
type ScriptRecordingRecord struct {
	ScriptID                string                       `json:"script_id"`
	SchemaVersion           string                       `json:"schema_version,omitempty"`
	Source                  string                       `json:"source,omitempty"`
	Sensitivity             string                       `json:"sensitivity,omitempty"`
	RequestCount            int                          `json:"request_count"`
	Observations            []ScriptRecordingObservation `json:"observations"`
	CorrelationObservations []ScriptRecordingObservation `json:"correlation_observations,omitempty"`
	// EncryptedCorrelationObservations is an opaque API-layer AES-GCM payload.
	// Storage never decrypts it and never persists CorrelationObservations as
	// plaintext. The field is intentionally excluded from API serialization.
	EncryptedCorrelationObservations string    `json:"-"`
	CreatedAt                        time.Time `json:"created_at"`
	UpdatedAt                        time.Time `json:"updated_at"`
}

// TestRecord represents a persisted test run.
type TestRecord struct {
	ID                    string     `json:"id"`
	RefKey                string     `json:"ref_key,omitempty"`
	MaterializationKey    string     `json:"materialization_key,omitempty"`
	Name                  string     `json:"name"`
	Status                string     `json:"status"`
	ApplicationID         string     `json:"application_id,omitempty"`
	ReleaseID             string     `json:"release_id,omitempty"`
	ScenarioID            string     `json:"scenario_id,omitempty"`
	ProjectID             string     `json:"project_id,omitempty"`
	FolderID              *string    `json:"folder_id,omitempty"`
	ScenarioMode          string     `json:"scenario_mode,omitempty"`
	ScriptShakeout        bool       `json:"script_shakeout,omitempty"`
	ResourceBaseline      bool       `json:"resource_baseline,omitempty"`
	StartDelay            string     `json:"start_delay,omitempty"`
	Assignment            any        `json:"assignment,omitempty"` // JSON blob
	TotalVUsers           int        `json:"total_vusers"`
	Duration              int64      `json:"duration_ns"`
	StartTime             time.Time  `json:"start_time"`
	ScheduledStartAt      *time.Time `json:"scheduled_start_at,omitempty"`
	PendingSince          *time.Time `json:"pending_since,omitempty"`
	PausedAt              *time.Time `json:"paused_at,omitempty"`
	CumulativePause       int64      `json:"cumulative_pause_ns,omitempty"`
	CapacityWindowStartAt *time.Time `json:"capacity_window_start_at,omitempty"`
	CapacityWindowEndAt   *time.Time `json:"capacity_window_end_at,omitempty"`
	BaselinePinned        bool       `json:"baseline_pinned,omitempty"`
	BaselinePinnedAt      *time.Time `json:"baseline_pinned_at,omitempty"`
	EndTime               *time.Time `json:"end_time,omitempty"`
	Error                 string     `json:"error,omitempty"`
	CreatedBySubject      string     `json:"created_by_subject,omitempty"`
	CreatedByDisplayName  string     `json:"created_by_display_name,omitempty"`
	StartedBySubject      string     `json:"started_by_subject,omitempty"`
	StartedByDisplayName  string     `json:"started_by_display_name,omitempty"`
	StoppedBySubject      string     `json:"stopped_by_subject,omitempty"`
	StoppedByDisplayName  string     `json:"stopped_by_display_name,omitempty"`
	Groups                any        `json:"groups"`                            // JSON blob
	RuntimeSettings       any        `json:"runtime_settings,omitempty"`        // JSON blob
	GoalConfig            any        `json:"goal_config,omitempty"`             // JSON blob
	ExecutionPlanApproval any        `json:"execution_plan_approval,omitempty"` // JSON blob
	// TimeslotID persists the capacity reservation a run is bound to, so the
	// binding survives a controller restart / HA failover and can be re-validated
	// at launch time.
	TimeslotID *string `json:"timeslot_id,omitempty"`
}

// TestBaselineState is the durable baseline projection returned after an
// atomic comparison-scope pin/unpin operation.
type TestBaselineState struct {
	ID       string
	Pinned   bool
	PinnedAt *time.Time
}

type TestStatusTransitionResult string

const (
	TestStatusTransitionUpdated   TestStatusTransitionResult = "updated"
	TestStatusTransitionUnchanged TestStatusTransitionResult = "unchanged"
	TestStatusTransitionFenced    TestStatusTransitionResult = "fenced"
	TestStatusTransitionMissing   TestStatusTransitionResult = "missing"
)

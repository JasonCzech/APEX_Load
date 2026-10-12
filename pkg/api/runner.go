package api

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	pb "github.com/jasonczech/apexload/api/proto"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
	"golang.org/x/sync/singleflight"
)

type scriptPersistence interface {
	SaveScript(script *dsl.Script) error
	DeleteScript(id string) error
	ListScripts(projectID string, folderID *string) ([]*dsl.Script, error)
	MoveScript(id string, folderID *string) error
}

type testPersistence interface {
	SaveTest(test *storage.TestRecord) error
	GetTest(id string) (*storage.TestRecord, error)
	ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error)
	ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*storage.TestRecord, error)
	UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error
	DeleteTest(id string) error
	MoveTest(id string, folderID *string) error
}

type createOnlyTestPersistence interface {
	CreateTestIfAbsent(test *storage.TestRecord) (bool, error)
	CreateTestIfAbsentIfLeaseHeld(test *storage.TestRecord, leaseName, leaseOwnerID string) (bool, error)
}

type leaseTermCreateOnlyTestPersistence interface {
	CreateTestIfAbsentIfLeaseTermHeld(test *storage.TestRecord, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

type leaseFencedTestDeletePersistence interface {
	DeleteTestIfLeaseHeld(id, leaseName, leaseOwnerID string) (storage.LeaseFencedDeleteResult, error)
}

type leaseFencedProjectDeletePersistence interface {
	DeleteProjectIfLeaseHeld(id, leaseName, leaseOwnerID string) (storage.LeaseFencedDeleteResult, error)
}

type auditPersistence interface {
	ListAuditEvents(filter storage.AuditEventFilter) ([]*storage.AuditEvent, error)
}

type scenarioPersistence interface {
	SaveScenario(scenario *storage.Scenario, scripts []storage.ScenarioScript) error
	GetScenario(id string) (*storage.Scenario, []storage.ScenarioScript, error)
	ListScenarios() ([]*storage.Scenario, error)
	DeleteScenario(id string) error
	SaveScenarioSchedule(schedule *storage.ScenarioSchedule) error
	GetScenarioSchedule(id string) (*storage.ScenarioSchedule, error)
	ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, error)
	ListAllScenarioSchedules() ([]*storage.ScenarioSchedule, error)
	DeleteScenarioSchedule(id string) error
	SaveScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence) error
	GetScenarioScheduleOccurrence(id string) (*storage.ScenarioScheduleOccurrence, error)
	GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*storage.ScenarioScheduleOccurrence, error)
	ListScenarioScheduleOccurrences(scheduleID string) ([]*storage.ScenarioScheduleOccurrence, error)
	ListAllScenarioScheduleOccurrences() ([]*storage.ScenarioScheduleOccurrence, error)
	DeleteScenarioScheduleOccurrence(id string) error
	SaveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval) error
	GetScenarioScheduleApproval(id string) (*storage.ScenarioScheduleApproval, error)
	ListScenarioScheduleApprovals(scheduleID string) ([]*storage.ScenarioScheduleApproval, error)
	TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error)
	GetSchedulerLease(name string) (*storage.SchedulerLease, error)
	SaveScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) error
	ListScenarioDataPoolBindings(scenarioID string) ([]storage.ScenarioDataPoolBinding, error)
	SaveAuditEvent(event *storage.AuditEvent) error
}

type atomicScenarioPersistence interface {
	SaveScenarioWithDataPoolBindings(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error
}

type dataPoolPersistence interface {
	GetDataPool(id string) (*storage.SharedDataPool, error)
	ListDataPoolRows(poolID string) ([]*storage.SharedDataPoolRow, error)
}

type projectPersistence interface {
	GetProject(id string) (*storage.Project, error)
}

type releasePersistence interface {
	GetRelease(id string) (*storage.Release, error)
}

type historyPersistence interface {
	SaveHistory(testID string, timestamp time.Time, metrics any) error
	GetHistory(testID string) ([]any, error)
}

type limitedHistoryPersistence interface {
	GetHistoryLimit(testID string, limit int) ([]any, error)
}

type archivePersistence interface {
	SaveRunArchive(record *storage.RunArchiveRecord) error
	GetRunArchive(testID string) (*storage.RunArchiveRecord, error)
}

type archiveTrendPointPersistence interface {
	UpdateRunArchiveTrendPointIfUnchanged(expected *storage.RunArchiveRecord, trendPointJSON string) (bool, error)
}

type testNotesPersistence interface {
	ListTestNotes(testID string) ([]*storage.TestNote, error)
}

type settingsPersistence interface {
	SaveSetting(key string, value string) error
	GetSetting(key string) (string, error)
}

type settingsCompareAndSwapper interface {
	CompareAndSwapSetting(key, expected, value string) (bool, error)
}

type capacityFencedSettingsCompareAndSwapper interface {
	CompareAndSwapSettingWithCapacityFence(
		key, expected, value string,
		updatedAt time.Time,
		validate func(current string, approved []*storage.CapacityReservation) error,
	) (bool, error)
}

type usageSnapshotPersistence interface {
	SaveUsageAccountingSnapshot(snapshot *storage.UsageAccountingSnapshot) error
	ListUsageAccountingSnapshots(projectID string, limit int, offset int) ([]*storage.UsageAccountingSnapshot, error)
}

// reservationPersistence is the slice of storage.Store used to bind a capacity
// timeslot to a test at creation and to make admission control reservation-aware.
type reservationPersistence interface {
	GetCapacityReservation(id string) (*storage.CapacityReservation, error)
	UpdateCapacityReservationBinding(id string, testID *string, testStatus *string, updatedAt time.Time) (bool, error)
	ClearCapacityReservationBinding(id string, testID string, updatedAt time.Time) (bool, error)
	ListCapacityReservationsByTimeRange(projectID string, startAt, endAt time.Time) ([]*storage.CapacityReservation, error)
}

type atomicCapacityBindingPersistence interface {
	CreateTestAndBindCapacityReservation(*storage.TestRecord, string, time.Time) (storage.CapacityBindingResult, error)
	CreateTestAndBindCapacityReservationIfLeaseHeld(*storage.TestRecord, string, time.Time, string, string) (storage.CapacityBindingResult, error)
	BindCapacityReservationToTest(string, string, time.Time) (storage.CapacityBindingResult, error)
	BindCapacityReservationToTestIfLeaseHeld(string, string, time.Time, string, string) (storage.CapacityBindingResult, error)
	ClearCapacityReservationBindingAndTestIfLeaseHeld(string, string, time.Time, string, string) (storage.CapacityBindingReleaseResult, error)
	RepairCapacityReservationBindings() (int64, error)
	RepairCapacityReservationBindingsIfLeaseHeld(string, string) (bool, int64, error)
}

type leaseTermAtomicCapacityBindingPersistence interface {
	CreateTestAndBindCapacityReservationIfLeaseTermHeld(*storage.TestRecord, string, time.Time, string, string, uint64) (storage.CapacityBindingResult, error)
}

type fullWindowAtomicCapacityBindingPersistence interface {
	BindCapacityReservationToTestWithWindow(string, string, time.Time, time.Time, time.Time) (storage.CapacityBindingResult, error)
	BindCapacityReservationToTestWithWindowIfLeaseHeld(string, string, time.Time, time.Time, time.Time, string, string) (storage.CapacityBindingResult, error)
}

type runtimeSecretRecoveryState struct {
	mu         sync.Mutex
	timer      *time.Timer
	at         time.Time
	generation uint64
	stopped    bool
	active     map[string]struct{}
	// running/drained account for admitted recovery scans and timer callbacks.
	// Admission and shutdown share mu; a stopped timer alone cannot join work.
	running int
	drained chan struct{}
}

type TestRunner struct {
	// evidenceAcknowledgements tracks background acknowledgements of
	// published run evidence to generators.
	evidenceAcknowledgements sync.WaitGroup
	// acknowledgedEvidence remembers which published evidence generators
	// confirmed, so later commits of a run do not acknowledge it again.
	acknowledgedEvidence  acknowledgedEvidenceSet
	elasticPrepare        func(context.Context, storage.MonitorSource, *storage.ExpandedElasticConnectionSnapshot) error
	prometheusPrepare     func(context.Context, storage.MonitorSource, *storage.PrometheusConnectionSnapshot, []analysis.MonitorRule) error
	dynatracePrepare      func(context.Context, storage.MonitorSource, *storage.DynatraceConnectionSnapshot) error
	thresholdJournal      *thresholdJournalWriter
	tests                 map[string]*ManagedTest
	scripts               map[string]*StoredScript
	privateCISnapshots    map[string]int
	scenarios             map[string]*StoredScenario
	archives              map[string]*storage.RunArchiveRecord
	deletingProjects      map[string]struct{}
	projectDeletionGates  projectMutationCoordinator
	projectDeletionUnlock map[string]func()
	// secretReferenceMutations is shared with Server so every durable script
	// mutation path fences the usage snapshots used by secret create/rotation.
	secretReferenceMutations *projectMutationCoordinator
	shuttingDown             bool
	mu                       sync.RWMutex
	scriptMutationMu         sync.Mutex
	scenarioMutationMu       sync.Mutex
	compatCreateMu           sync.Mutex
	reservedLaunchCreateMu   sync.Mutex
	podCapacityMutationMu    sync.Mutex
	contentRevision          uint64
	contentReloadMu          sync.Mutex
	haScriptRefresh          haRefreshCoalescer
	testReloadMu             sync.Mutex
	haTestRefresh            haRefreshCoalescer
	capacityBindingRefresh   haRefreshCoalescer
	haScenarioRefreshMu      sync.Mutex
	haScenarioRefreshAt      time.Time
	schedulerContentMu       sync.Mutex
	schedulerContentToken    string
	schedulerContentKnown    bool
	activeStopMu             sync.Mutex
	runtimeAdjustmentMu      sync.Mutex
	remoteControlMu          sync.Mutex
	remoteControlGates       map[string]*distributedControlGate
	hub                      *Hub
	maxTests                 int
	defaultMaxTests          int
	settings                 SystemSettings
	settingsRefreshMu        sync.Mutex
	settingsWriteMu          sync.Mutex
	settingsAuthRefreshAt    time.Time
	settingsRefreshInFlight  chan struct{}
	systemSettingsRaw        string
	settingsRevision         uint64
	// readinessRuntime is a fixed-size immutable projection published only
	// while mu is held. readinessScheduler is the mutable counter source for
	// that combined projection and is likewise guarded by mu.
	readinessRuntime            atomic.Pointer[runnerReadinessSnapshot]
	readinessScheduler          schedulerReadinessProjection
	scriptStore                 scriptPersistence
	testStore                   testPersistence
	startAdmissionRevisionStore startAdmissionRevisionPersistence
	scenarioStore               scenarioPersistence
	dataPoolStore               dataPoolPersistence
	projectStore                projectPersistence
	releaseStore                releasePersistence
	historyStore                historyPersistence
	storedHistoryCache          *storedTestHistoryCache
	archiveStore                archivePersistence
	githubCheckStore            storage.GitHubEnterpriseStore
	testNotesStore              testNotesPersistence
	auditStore                  auditPersistence
	settingsStore               settingsPersistence
	usageStore                  usageSnapshotPersistence
	projectSecretStore          secretstore.Store
	applicationSecretStore      secretstore.Store
	applicationStore            applicationPersistence
	libraryAssignmentStore      storage.LibraryAssignmentStore
	// continuityKeyWrapper seals the run-supervision token into HA run-continuity
	// checkpoints (run_continuity.go). Nil disables arming.
	continuityKeyWrapper  keywrap.KeyWrapper
	elkStore              storage.ELKConnectionStore
	dynatraceStore        storage.DynatraceConnectionStore
	dynatraceOAuthMu      sync.Mutex
	dynatraceOAuthTokens  map[string]dynatraceOAuthToken
	dynatraceOAuthRefresh singleflight.Group
	secretAttemptStore    storage.RuntimeSecretProvisioningAttemptStore
	secretRecovery        runtimeSecretRecoveryState
	// objectRepoStore resolves object_ref bindings into concrete selectors when a
	// load run is compiled (generators have no store, so binding must resolve here).
	objectRepoStore objectRepositoryReader
	// reservationStore binds capacity timeslots at test create and feeds
	// reservation-aware admission control.
	reservationStore reservationPersistence
	// execPlanRunStore persists sequential execution-plan run progress; seqMu
	// guards the in-memory orchestration state (kept off tr.mu since advancing
	// a plan calls CreateTest/StartTest, which take tr.mu).
	execPlanRunStore executionPlanRunPersistence
	seqMu            sync.Mutex
	seqPlanRuns      map[string]*seqPlanRunState
	seqPlanByTest    map[string]string
	seqPlanRetrying  map[string]struct{}
	// Accepted CI retries share one term-bound coordinator. Per-row goroutines
	// would create an unbounded timer/provider herd during a shared outage.
	ciRecoveryRetrying        map[string]queuedCIRecoveryRetryState
	ciRecoveryRetryWorkerTerm executionLeaseTerm
	ciRecoveryRetryWorkerUp   bool
	ciRecoveryRetryWake       chan struct{}
	// pendingPromotionRetryActive/Term guard one bounded-backoff queue sweep per
	// execution term. Promotion scans the whole queue, so per-test workers would
	// add contention without improving liveness.
	pendingPromotionRetryActive bool
	pendingPromotionRetryTerm   executionLeaseTerm
	// scriptShakeoutFingerprintKey authenticates exact executable inputs only
	// inside this controller process. Durable evidence receives a separately
	// disclosure-safe digest so low-entropy credentials never become a public
	// offline guessing oracle.
	scriptShakeoutFingerprintKey [32]byte
	terminalRetryMu              sync.Mutex
	terminalRetrying             map[string]struct{}
	// stopTeardownRetries records the strongest pending teardown intent per
	// test. Versioning lets the deduplicated worker detect a force-abort upgrade
	// that arrives while a graceful attempt is in flight before it retires.
	stopTeardownRetries map[string]stopTeardownRetryIntent
	terminalObserverMu  sync.RWMutex
	terminalObserver    func(string)
	// archiveCommitGates serialize archive publication with deletion per test.
	// Entries are reference-counted and removed after their last waiter exits.
	archiveCommitMu    sync.Mutex
	archiveCommitGates map[string]*archiveCommitGate
	// Archive orphan recovery retains a directory stream across bounded sweeps.
	// A new leader starts at the beginning, and a completed pass starts again
	// so archives created during an earlier pass are still found.
	orphanArchiveScanMu sync.Mutex
	orphanArchiveScan   *orphanArchiveScanner
	// Serialize sweeps while allowing leadership loss and shutdown to close
	// the directory stream without waiting for archive/DB processing.
	orphanRecoveryMu sync.Mutex
	// execPlanScheduleStore persists recurring execution-plan schedules and their
	// occurrences; the leader-gated sweep materializes due plan schedules.
	execPlanScheduleStore executionPlanSchedulePersistence
	// fleetStore resolves fleet-pool membership for pool-aware generator assignment.
	fleetStore fleetMembershipReader
	// leadership gates live test-execution mutations to a single replica when
	// HA leader election is enabled. nil means "always leader" (single-node /
	// test default), so behavior is unchanged unless election is wired in.
	leadership            *executionLeadership
	durableExecutionFence bool
	// runAdoptionDoneGeneration is the durable execution generation whose
	// acquire-time run adoption (Phase A) has finished. The generator epoch
	// keeper waits for it instead of for the whole reconcile.
	runAdoptionDoneGeneration atomic.Uint64
	// testOnlyAllowUnfencedDeploymentLeaseStart is never enabled by production
	// construction. A test-only option defined in a _test.go file enables it for
	// narrow fault-injection stores that intentionally implement only SaveTest.
	testOnlyAllowUnfencedDeploymentLeaseStart bool
	// beforeExecutionPlanStartMutation is a deterministic test barrier between
	// an execution-plan caller's outer term check and the term-aware StartTest
	// mutation. Production leaves it nil.
	beforeExecutionPlanStartMutation func()
	// beforeLocalBuildFailureFinalize is a deterministic test barrier between a
	// local engine build failure and the lifecycle-fenced terminal transition.
	// Production leaves it nil.
	beforeLocalBuildFailureFinalize func()
	// beforeQueuedCIRecoveryEmptyRelease is a deterministic test barrier inside
	// the coordinator's atomic empty-queue/worker-ownership handoff. Production
	// leaves it nil.
	beforeQueuedCIRecoveryEmptyRelease func()
	// beforePassiveTransitionRescan is a deterministic test barrier before a
	// successor reloads one old-term passive transition. Production leaves it nil.
	beforePassiveTransitionRescan func(string)
	// beforeScenarioScheduleStartLifecycleRelease is a deterministic test
	// barrier after the scheduler has reacquired mu and committed occurrence /
	// parent correlation, but before it releases the per-test lifecycle gate.
	// Production leaves it nil.
	beforeScenarioScheduleStartLifecycleRelease func()
	liveState                                   LiveStateStore
	snapshotWriteMu                             sync.Mutex
	snapshotWrites                              map[string]*runSnapshotWriteState
	execution                                   testExecutionBackend
	notifier                                    *notificationDispatcher
	notifications                               atomic.Value
	// archiveAttachedHook, installed by Server, runs (in its own goroutine)
	// after a finalized archive is attached to its run.
	archiveAttachedHook func(testID string)
	routeCache          map[string]latencyRouteCacheEntry
	// importPublicationSnapshot is installed by Server after construction. It
	// reads the durable direct-import journal and returns assets that have been
	// created but whose containing batch has not committed. Keeping the callback
	// off the storage interfaces lets create-only persistence continue to use raw
	// rows for recovery while every user-visible runner read fails closed.
	importPublicationMu       sync.RWMutex
	importPublicationSnapshot func() (map[string]struct{}, error)
	importPublicationStore    storage.Store
	// afterImportPublicationSnapshot is a deterministic test barrier used to
	// prove that rollback between the claim read and asset revalidation cannot
	// publish a stale pre-rollback snapshot.
	afterImportPublicationSnapshot func()

	// Reference-key state. envCode, script/scenario counters, and the index are
	// guarded by mu. Run sequences use a shared dedicated allocator so request
	// snapshots can reserve unique keys without holding mu across settings I/O.
	// envCode is the short,
	// environment-aware prefix resolved once at startup; the seq counters are
	// monotonic per (env, artifact type) and backed by persisted settings so
	// they survive restarts. refKeyIndex maps a reference key to its UUID.
	envCode                 string
	refScriptSeq            int
	refScenarioSeq          int
	scriptRefSeqAllocator   *artifactReferenceSequenceAllocator
	scenarioRefSeqAllocator *artifactReferenceSequenceAllocator
	runRefSeqAllocator      *runReferenceSequenceAllocator
	refKeyIndex             map[string]string

	// capacityAdvisoryCache memoizes estimateCapacityAdvisoryForAssignment. Its
	// inputs are settings (generation-invalidated) and reservation-window time
	// (bounded by a short TTL); live-inventory availability bypasses the cache,
	// see capacity_advisory_cache.go. The generation is guarded by tr.mu (same
	// contract as tr.settings); the cache map has its own mutex because it is
	// written even when callers hold only tr.mu.RLock (estimate/preflight); the
	// hit/miss counters are atomic.
	capacityAdvisoryCache   map[string]cachedCapacityAdvisory
	capacityAdvisoryCacheMu sync.Mutex
	capacityAdvisoryGen     uint64
	capacityAdvisoryHits    uint64
	capacityAdvisoryMisses  uint64

	// durableWritesInFlight counts the runner's durable writes that are currently
	// inside the storage driver. The run-driver panic backstop reads it to tell a
	// transition blocked on slow durable I/O (which waiting does recover from)
	// from one blocked on an orphaned lock (which it never does); the liveness
	// probe reads it to tell a runner read that would block behind such a write
	// from one that would not.
	//
	// Every durable test-store write the runner issues while holding tr.mu is
	// counted, not only the status write: stopTest, failClosedUncertainVUserScale
	// and every VUser-scale reservation hold tr.mu across a store call, and so do
	// the operator-triggered paths (comparison-baseline pin/unpin, folder move,
	// delete). A gauge blind to any of those classifies a healthy-but-stalled
	// mutex as orphaned and re-panics the controller into exactly the crash-loop
	// the backstop's extension exists to prevent. The enumeration is enforced by
	// TestEveryTestStoreWriteIsCountedOrDocumented rather than by this comment:
	// every tr.testStore write call site must go through trackDurableWrite or be
	// listed there with the reason it runs off-lock.
	//
	// The gauge is deliberately NOT scoped to the test store, because neither
	// reader is asking about a table - both are asking whether the goroutine
	// holding tr.mu is inside the storage driver. Ordinary script/scenario saves
	// and their reference allocation now run outside this mutex, but several
	// compatibility and lifecycle paths still issue lock-held writes: deletes,
	// folder moves, GitHub import, runtime-profile application, and some archive
	// transitions. Those sections are bracketed by beginLockHeldDurableWrite rather
	// than wrapped call by call, because they reach storage through shared helpers
	// that a per-call wrapper would silently miss;
	// TestEveryLockHeldDurableWriteIsCounted enforces that no lock-held write to
	// any store escapes the gauge.
	//
	// It is a process-wide gauge, not a per-run one, so it can only delay the
	// backstop's re-panic, never suppress it: under a healthy store a write
	// clears in milliseconds, and the extension count is capped regardless.
	durableWritesInFlight atomic.Int64

	liveInventoryMu sync.RWMutex
	liveInventory   []GeneratorInventoryEntry
	liveInventoryAt time.Time
	// The pod list contains StatefulSet ordinals absent from headless DNS while
	// Pending, NotReady, or terminating. It is published with liveInventory.
	livePodSnapshot recoveryPodSnapshot

	// clockObservationSpacing keeps at most one clock observation per source
	// in each window; zero means a minute. Tests shorten it before a run.
	clockObservationSpacing time.Duration
	// phaseLogs holds each run's *phaseEventLog by test ID. It is a sync.Map,
	// not a ManagedTest field, because lifecycle broadcasts record phases and
	// some run while holding mu.
	phaseLogs sync.Map
	// businessKeys caches each project's business-key digester.
	businessKeys sync.Map
}

type archiveCommitGate struct {
	mu   sync.Mutex
	refs int
}

func (tr *TestRunner) lockArchiveCommitFence(testID string) func() {
	tr.archiveCommitMu.Lock()
	if tr.archiveCommitGates == nil {
		tr.archiveCommitGates = make(map[string]*archiveCommitGate)
	}
	gate := tr.archiveCommitGates[testID]
	if gate == nil {
		gate = &archiveCommitGate{}
		tr.archiveCommitGates[testID] = gate
	}
	gate.refs++
	tr.archiveCommitMu.Unlock()

	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()
		tr.archiveCommitMu.Lock()
		gate.refs--
		if gate.refs == 0 && tr.archiveCommitGates[testID] == gate {
			delete(tr.archiveCommitGates, testID)
		}
		tr.archiveCommitMu.Unlock()
	}
}

type ManagedTest struct {
	MonitorBindings *storage.MonitorBindings `json:"monitor_bindings,omitempty"`
	TraceContext    *RunTraceContextSettings `json:"trace_context,omitempty"`
	MonitorWarnings []string                 `json:"monitor_warnings,omitempty"`
	Activity        *ObjectActivitySummary   `json:"activity,omitempty"`
	preparationID   string
	// Immutable controller-resolved placement, protected by tr.mu. Preparations
	// retain the authored assignment in their revision while provisioned targets
	// are used for execution.
	preparationCapacityAssignment *preparationCapacityAssignment
	// Protected by tr.mu; checks only generator control-plane reservations.
	preparationPeerCheck func(context.Context) error
	// Protected by tr.mu; retained through terminal persistence retries.
	localArchiveFinalizationFailed bool
	// http3Preflight is the scenario HTTP/3 preflight, set once before the
	// run starts and replaced, never mutated (protected by tr.mu).
	http3Preflight *RunArchiveHTTP3Preflight

	TerminationMode       string `json:"termination_mode,omitempty"`
	podCapacityEnabled    bool
	ID                    string                            `json:"id"`
	RefKey                string                            `json:"ref_key,omitempty"`
	MaterializationKey    string                            `json:"-"`
	Name                  string                            `json:"name"`
	Status                string                            `json:"status"`
	ApplicationID         string                            `json:"application_id,omitempty"`
	ReleaseID             string                            `json:"release_id,omitempty"`
	ScenarioID            string                            `json:"scenario_id,omitempty"`
	ProjectID             string                            `json:"project_id,omitempty"`
	FolderID              *string                           `json:"folder_id,omitempty"`
	DataPools             []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	Groups                []TestGroup                       `json:"groups"`
	CapacityAdvisory      *CapacityAdvisory                 `json:"capacity_advisory,omitempty"`
	RuntimeSettingsReview *RuntimeSettingsReview            `json:"runtime_settings_review,omitempty"`
	Assignment            *GeneratorAssignmentSettings      `json:"assignment,omitempty"`
	ScenarioMode          string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout        bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline      bool                              `json:"resource_baseline,omitempty"`
	StartDelay            string                            `json:"start_delay,omitempty"`
	GoalConfig            *GoalConfig                       `json:"goal_config,omitempty"`
	GoalTargets           map[string]GoalTargetStatus       `json:"goal_targets,omitempty"`
	// Continuity is the HA run-continuity state (docs/HA_RUN_CONTINUITY_PLAN.md);
	// derived on read, never persisted from this field.
	Continuity     *RunContinuityStatus `json:"continuity,omitempty"`
	TrafficProfile *TrafficProfile      `json:"traffic_profile,omitempty"`
	// JointEvidence is the run's joint evidence v4 projection (B5), frozen
	// into provenance at Start.
	JointEvidence         *engine.JointProjection  `json:"joint_evidence,omitempty"`
	RuntimeSettings       *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
	ELKCapture            *ELKCaptureConfig        `json:"elk_capture,omitempty"`
	elkArtifacts          *runArtifacts
	DynatraceCapture      *DynatraceCaptureConfig `json:"dynatrace_capture,omitempty"`
	dynatraceArtifacts    *runArtifacts
	clockObservations     *clockObservationLog                     // measured source clocks; created on first observation
	telemetrySources      *telemetrySourceLedger                   // source-epoch publication coverage; created on first publication
	telemetryStages       *telemetryStageRecorder                  // per-stage latency distributions; created on first sample
	historyTrimmed        int64                                    // history points dropped by the in-memory bounds
	clockCalibrations     *clockCalibrations                       // current generator clock offsets; created on first probe
	TotalVUsers           int                                      `json:"total_vusers"`
	GeneratorShards       []GeneratorShardSummary                  `json:"generator_shards,omitempty"`
	Duration              time.Duration                            `json:"duration_ns"`
	DurationStr           string                                   `json:"duration"`
	CreatedAt             time.Time                                `json:"created_at"`
	ScheduledStartAt      *time.Time                               `json:"scheduled_start_at,omitempty"`
	TimeslotID            *string                                  `json:"timeslot_id,omitempty"`
	PendingSince          *time.Time                               `json:"pending_since,omitempty"`
	PendingReason         string                                   `json:"pending_reason,omitempty"`
	QueuePosition         int                                      `json:"queue_position,omitempty"`
	BaselinePinned        bool                                     `json:"baseline_pinned,omitempty"`
	BaselinePinnedAt      *time.Time                               `json:"baseline_pinned_at,omitempty"`
	StartedAt             *time.Time                               `json:"started_at,omitempty"`
	PausedAt              *time.Time                               `json:"paused_at,omitempty"`
	CumulativePause       time.Duration                            `json:"cumulative_pause_ns,omitempty"`
	CapacityWindowStartAt *time.Time                               `json:"capacity_window_start_at,omitempty"`
	CapacityWindowEndAt   *time.Time                               `json:"capacity_window_end_at,omitempty"`
	EndedAt               *time.Time                               `json:"ended_at,omitempty"`
	CreatedBySubject      string                                   `json:"created_by_subject,omitempty"`
	CreatedByDisplayName  string                                   `json:"created_by_display_name,omitempty"`
	StartedBySubject      string                                   `json:"started_by_subject,omitempty"`
	StartedByDisplayName  string                                   `json:"started_by_display_name,omitempty"`
	StoppedBySubject      string                                   `json:"stopped_by_subject,omitempty"`
	StoppedByDisplayName  string                                   `json:"stopped_by_display_name,omitempty"`
	StartAuthorization    *QueuedStartAuthorization                `json:"start_authorization,omitempty"`
	ExecutionPlanApproval *RunArchiveExecutionPlanApprovalSnapshot `json:"execution_plan_approval,omitempty"`
	VUserRamps            map[string]VUserRampStatus               `json:"vuser_ramps,omitempty"`
	LiveMetrics           *LiveMetrics                             `json:"live_metrics,omitempty"`
	History               []*LiveMetrics                           `json:"history,omitempty"`
	Sparkline             []TestSparklinePoint                     `json:"sparkline,omitempty"`
	ComparisonBaseline    *RunComparisonBaseline                   `json:"comparison_baseline,omitempty"`
	ArchiveFailure        *storage.RunArchiveFailure               `json:"archive_failure,omitempty"`
	ArchiveRecovery       *RunArchiveRecovery                      `json:"archive_recovery,omitempty"`
	Archive               *storage.RunArchiveRecord                `json:"archive,omitempty"`
	TelemetryHealth       *TelemetryRunHealth                      `json:"telemetry_health,omitempty"`
	Error                 string                                   `json:"error,omitempty"`

	// deprecated single-script fields kept for backwards compat in JSON
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`

	multiEngine         *engine.MultiGroupEngine
	executionCtx        context.Context
	executionGeneration uint64
	// continuityCheckpoint is the stored HA adoption checkpoint (opaque JSON,
	// never serialized); continuityArmedGeneration is its decoded term (0 when
	// absent). Set both through setContinuityCheckpointLocked. adoptedResume is
	// set between a successful adoption claim and the runtime rebuild that
	// follows admission (run_adoption.go).
	continuityCheckpoint      string
	continuityArmedGeneration uint64
	adoptedResume             *adoptedRunResume
	// stageScheduleResume makes stage schedules started after adoption
	// fast-forward to the run clock instead of replaying from stage 0.
	stageScheduleResume bool
	// continuityHandoff marks a run a successor will adopt when this replica
	// steps down voluntarily (graceful shutdown or failed recovery): shutdown
	// does not stop it and leadership-loss teardown detaches it without a
	// generator Stop. Set only by markContinuityHandoff.
	continuityHandoff bool
	// continuityFailovers is the run's failover history: loaded from the
	// checkpoint, appended on adoption, written into every re-arm. It is the
	// single source for RunContinuityStatus, so a failed re-arm cannot hide a
	// failover the run survived.
	continuityFailovers          []RunFailover
	pendingScriptShakeoutOutcome *pendingScriptShakeoutOutcome
	validatedScriptShakeoutPass  string
	validatedScriptShakeoutTerm  executionLeaseTerm
	// shakeoutAdmissionReservation belongs to one active start operation. Its
	// context retires the private handoff reservation on leadership loss.
	shakeoutAdmissionReservation  *shakeoutAdmissionReservation
	durationAdjustmentPrevious    time.Duration
	scaleAdjustmentPrevious       map[string]int
	cancel                        context.CancelFunc
	groupSpecs                    []engine.GroupConfig
	pinnedSecrets                 *runPinnedSecretState
	runtimeHydrationError         string
	runtimeSnapshotRepairRequired bool
	creationAdopted               bool
	// Expected parent revisions are create-boundary CAS evidence. Storage locks
	// and verifies these rows in the same transaction that inserts the test, so
	// off-lock project/release/application reads cannot become stale authority.
	expectedProjectUpdatedAt       time.Time
	expectedApplicationUpdatedAt   time.Time
	expectedReleaseUpdatedAt       time.Time
	expectedStartAdmissionRevision int64
	historyBytes                   int64
	historyWeights                 []int64
	// terminalGroupRuntimeTrimmed records that terminal compaction discarded
	// process-private group material needed to reconstruct groups_json. Legacy
	// compatibility stores that still require a full-row SaveTest use it to
	// preserve the immutable durable group snapshot instead of writing the
	// compacted in-memory projection back over it.
	terminalGroupRuntimeTrimmed bool
	// comparisonBreakdown retains the transaction and dependency-health
	// breakdown of the run's last metric point across terminal compaction, which
	// releases those cardinality-unbounded maps from the resident tail. The
	// run-comparison baseline is built exclusively from in-memory candidates
	// (buildRunComparisonBaselineLocked), so without this a run that terminalized
	// in this process would stop serving as a baseline the instant it finished.
	// It is bounded by maxComparisonBaselineBreakdownEntries per map, so it
	// cannot reintroduce the unbounded residency compaction exists to prevent.
	comparisonBreakdown   *runComparisonBreakdown
	goalControl           *goalControllerState
	executionBackend      testExecutionBackend
	lastNetworkLatency    *NetworkLatency
	latencyProbeInFlight  bool
	latencyEndpointsCache latencyEndpointCache
	latencyProbeSamples   map[string][]latencyProbeSample
	remoteTarget          string
	remoteClient          generatorClient
	remoteMetricsStream   *generatorrpc.MetricsStream
	remoteGroupTargets    map[string]string
	remoteGroupShards     map[string][]GeneratorShardSummary
	remoteClientsByTarget map[string]generatorClient
	// remoteArchiveClientsByTarget retains positively stopped evacuation
	// sources for the final archive merge. They are deliberately excluded from
	// active topology and every mutable-control fan-out.
	remoteArchiveClientsByTarget     map[string]generatorClient
	remoteMetricsStreamsByTarget     map[string]*generatorrpc.MetricsStream
	remoteMetricsBatchesByTarget     map[string]generatorrpc.MetricsBatch
	remoteTransactionResponseTimeSLA map[string]*transactionResponseTimeSLAAccumulator
	// remoteTransactionResponseTimeSLALegacyByTarget retains cumulative
	// controller-side counters for generators that do not yet publish the
	// cumulative SLA contract. Keeping these counters per target lets a rolling
	// upgrade merge legacy transaction windows with upgraded generator counters
	// without either double-counting or discarding prior evidence.
	remoteTransactionResponseTimeSLALegacyByTarget map[string]map[string]*transactionResponseTimeSLAAccumulator
	remoteTargetStates                             map[string]string
	remoteFinalizing                               bool
	remoteWorkloadCompleted                        bool
	remoteLifecycleCtx                             context.Context
	remoteLifecycleCancel                          context.CancelFunc
	remoteStopFinalizerRunning                     bool
	remoteMetricsLastPublishedAt                   time.Time
	remoteMetricsFlushScheduled                    bool
	// Accepted/published revisions and the gate make distributed aggregate
	// publication monotonic when per-target consumers finish out of order.
	remoteMetricsAcceptedRevision uint64
	// pendingJointPublications hold the joint evidence of source batches
	// accepted since the last published point (B5); the next installed
	// publication carries them (pendingJointBytes estimates their size).
	// jointPublicationsDropped counts those dropped past the bound and those
	// released because no point or archive of this controller will carry
	// them (the run's archive was committed, none was collected here, or
	// leadership was lost).
	// jointHeldFromStart is set when this controller starts the distributed
	// run, so it has held every batch accepted since; a run it loaded after a
	// restart, or adopted, cannot know what another controller held.
	pendingJointPublications        []*storage.NativePublicationEvidence
	pendingJointBytes               int64
	jointPublicationsDropped        int64
	jointHeldFromStart              bool
	remoteMetricsPublishedRevision  uint64
	remoteMetricsPublishGate        chan struct{}
	remoteGoalAcceptedErrorWindows  map[string]distributedGoalErrorAccumulator
	remoteGoalPublishedErrorWindows map[string]distributedGoalErrorAccumulator
	remoteGoalUpdateRunning         bool
	remoteGoalUpdateRevision        uint64
	// remoteSecretSnapshots are bounded, run-lifetime-only controller snapshots
	// shared by initial and evacuation assignments. They are never serialized.
	remoteSecretSnapshots map[string]distributedRunSecretSnapshot
	// remoteActivated is set only after every assigned generator acknowledges
	// CommitWork. The durable RUNNING transition intentionally precedes those
	// acknowledgements, so non-stop controls must remain closed while this is
	// false to avoid mutating a partially committed fleet.
	remoteActivated                      bool
	remoteActivationPending              bool
	remoteControlGeneration              uint64
	remotePauseOperationSequence         uint64
	remoteRunSupervisionToken            [generatorrpc.RunSupervisionTokenSize]byte
	remoteRunSupervisionCancel           context.CancelFunc
	remoteRunSupervisionDone             chan struct{}
	remoteRunSupervisionKeeperGeneration uint64
	remoteRunSupervisionFencedTargets    map[string]bool
	// executionFence is the scheduler-lease owner token captured by the epoch
	// that created/claimed this run. It is intentionally process-private and is
	// checked transactionally for every production full-row save.
	executionFence string
	// passiveOperationToken serializes process-local transitions that persist a
	// startable row after releasing tr.mu. The HA execution fence cannot provide
	// this protection when durable fencing is disabled, so every controller mode
	// claims this independent token before terminalizing or otherwise mutating a
	// passive row. It is never serialized.
	passiveOperationToken string
	telemetryDiagnostic   telemetryDiagnosticState
	// Threshold-alert webhook dedupe (guarded by tr.mu): alerts rebuild every
	// metrics tick while breached, so webhook dispatch fires once per breach
	// episode. thresholdAlertActive marks episode keys (metric["|"transaction])
	// already notified for their ongoing episode (cleared on recovery, so it is
	// bounded by the ≤5 alerts a tick can produce); thresholdAlertNotifiedAt is
	// the re-notify cooldown keyed PER METRIC (bounded by the fixed metric set)
	// so rotating hottest-transaction keys cannot amplify webhook volume.
	// In-memory only — restart/failover may re-notify once.
	thresholdAlertActive     map[string]bool
	thresholdAlertNotifiedAt map[string]time.Time
	// p99BreachConsecutive counts consecutive metrics evaluations with P99
	// above thresholds.max_p99_latency_ms for the opt-in sustained auto-stop.
	p99BreachConsecutive       int
	earlyStopBreachStartedAt   *time.Time
	errorPatterns              map[string]*LiveErrorPattern
	liveErrorEvidence          map[string][]LiveErrorEvidence
	vuserScaleRamps            map[string]*vuserScaleRamp
	openWorkloadSheddingWarned bool
	// runTotals keeps the run-to-date counters (from published samples for a
	// local run, from accepted generator windows for a distributed one) so
	// dashboards can show a run's error rate instead of the last second's.
	runTotals      runTotalsAccumulator
	ciSLAEvaluated bool
	ciSLABreached  bool
	ciSLADetails   []string
	// initialArchiveProvenance is an immutable, masked start-of-run snapshot.
	// Persisting it keeps distributed finalization honest across restart/failover.
	initialArchiveProvenance *RunArchiveProvenance
}

type TestSparklinePoint struct {
	Timestamp time.Time `json:"timestamp"`
	TPS       float64   `json:"tps"`
}

type ScaleVUsersOptions struct {
	RampDuration time.Duration
}

type GoalTargetUpdate struct {
	TestID           string                      `json:"test_id"`
	Group            string                      `json:"group"`
	TargetTPS        float64                     `json:"target_tps"`
	TargetAverageTPS float64                     `json:"target_average_tps"`
	GoalTargets      map[string]GoalTargetStatus `json:"goal_targets"`
	Status           string                      `json:"status"`
}

type vuserScaleRamp struct {
	cancel context.CancelFunc
	status VUserRampStatus
	done   chan error
}

type VUserRampStatus struct {
	Group               string    `json:"group"`
	StartVUsers         int       `json:"start_vusers"`
	TargetVUsers        int       `json:"target_vusers"`
	StartedAt           time.Time `json:"started_at"`
	RampDuration        string    `json:"ramp_duration"`
	RampDurationSeconds int64     `json:"ramp_duration_seconds"`
}

type TestGroup struct {
	Name        string `json:"name"`
	ScriptID    string `json:"script_id"`
	ScriptName  string `json:"script_name"`
	Protocol    string `json:"protocol"`
	Role        string `json:"role,omitempty"`
	VUsers      int    `json:"vusers"`
	StartOffset string `json:"start_offset,omitempty"`
	// TargetDuration is this group's operator-controlled active runtime. Empty
	// legacy snapshots inherit the enclosing test duration.
	TargetDuration string              `json:"target_duration,omitempty"`
	EffectiveEndAt *time.Time          `json:"effective_end_at,omitempty"`
	RuntimeState   string              `json:"runtime_state,omitempty"`
	EndedAt        *time.Time          `json:"ended_at,omitempty"`
	RampUp         string              `json:"ramp_up,omitempty"`
	RampDown       string              `json:"ramp_down,omitempty"`
	Stages         []storage.LoadStage `json:"stages,omitempty"`
	ActiveStage    *ActiveLoadLevel    `json:"active_stage,omitempty"`
	// Iterations is the effective root+group value retained for API compatibility.
	Iterations *int `json:"iterations,omitempty"`
	// IterationOverride records group-owned intent supplied through the compact
	// CreateGroupEntry.iterations field. Canonical group runtime settings retain
	// their own provenance in RuntimeSettings. A known nil means this compact
	// layer inherits root/group runtime settings; older snapshots omit the known
	// marker because they persisted only the effective Iterations value.
	IterationOverride      *int                         `json:"iteration_override,omitempty"`
	IterationOverrideKnown bool                         `json:"iteration_override_known,omitempty"`
	RuntimeSettings        *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	Assignment             *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	RunLogicGoals          *RunLogicGoalSnapshot        `json:"run_logic_goals,omitempty"`
	Initialization         string                       `json:"initialization,omitempty"`
	// TransactionResponseTimeSLAMs overrides the scenario default for every
	// named business transaction emitted by this group. Nil inherits the root.
	TransactionResponseTimeSLAMs *float64 `json:"transaction_response_time_sla_ms,omitempty"`
	// ContinueOnError records how the scenario's enabled-by-default
	// continue-on-error policy resolved for this group, including whether a more
	// specific layer diverged from it. The test run manifest reads it to surface
	// conflicts.
	ContinueOnError *ContinueOnErrorResolution `json:"continue_on_error,omitempty"`
	rampUpDur       time.Duration
	stageDriven     bool
	initialVUsers   int
	currentVUsers   int
	// Frozen execution intent survives terminal compaction and durable run snapshots.
	BrowserDocumentCaptureRequested bool `json:"browser_document_capture_requested,omitempty"`
	script                          *dsl.Script
	// scheduleOverriddenScriptIterations is the script-sourced per-VUser
	// iteration cap that applyScheduleIterationAuthority removed from the
	// compiled script because the scenario schedule owns run length (0 = none).
	// It is review/logging provenance only: it is recomputed whenever the group
	// is compiled or restored, so it is deliberately not part of the API JSON or
	// the stored group snapshot.
	scheduleOverriddenScriptIterations int
	// javaVuserCapacityPlan is the compact immutable admission projection for
	// this group's executable script. Request-scoped capacity snapshots retain it
	// instead of retaining the full parsed script graph.
	javaVuserCapacityPlan      engine.JavaVuserCapacityPlan
	javaVuserCapacityPlanKnown bool
	runtimeResourcePlan        engine.RuntimeResourcePlan
	runtimeResourcePlanKnown   bool
	runtimeResourcePlanError   string
	messagingRuntimeFeatures   []string
	// requiredRuntimeFeature is the complete compact feature contract captured
	// only by a lightweight capacity snapshot that drops executable scripts.
	// A present script always remains authoritative when resolving features.
	requiredRuntimeFeature      string
	requiredRuntimeFeatureKnown bool
	// baseScript is the immutable executable input before test/group runtime
	// overrides. It lets a revised queued copy apply intentional runtime edits
	// without recompiling mutable script or data-pool resources. Older rows may
	// lack it; their exact compiled script remains sufficient for a no-op copy.
	baseScript *dsl.Script
	// sharedDataSources is the exact shared-pool projection appended at creation.
	// It is stored separately from baseScript so a revision can change only pool
	// bindings without reloading mutable script code.
	sharedDataSources []dsl.DataSource
	redactionRules    []engine.RedactionRule
	// runtimeSecretValues is an execution-only overlay. It is deliberately
	// unexported so runtime override credentials cannot enter API JSON, stored
	// group snapshots, archives, script caches, or assignment fingerprints.
	runtimeSecretValues map[string]string
}

type GeneratorShardSummary struct {
	RuntimeResourceClaim     *pb.RuntimeResourceClaim     `json:"runtime_resource_claim,omitempty"`
	RuntimeResourceAdmission *pb.RuntimeResourceAdmission `json:"runtime_resource_admission,omitempty"`
	JavaHost                 *JavaHostPlacementSummary    `json:"java_host,omitempty"`
	Group                    string                       `json:"group"`
	Target                   string                       `json:"target"`
	VUsers                   int                          `json:"vusers"`
	ShardStrategy            string                       `json:"shard_strategy,omitempty"`
	ShardOrdinal             uint32                       `json:"shard_ordinal,omitempty"`
	ShardCount               uint32                       `json:"shard_count,omitempty"`
	VUserIDOffset            uint64                       `json:"vuser_id_offset,omitempty"`
}

type RunLogicGoalSnapshot struct {
	GlobalIterations int   `json:"global_iterations,omitempty"`
	MaxTransactions  int64 `json:"max_transactions,omitempty"`
}

type LiveMetrics struct {
	BrowserDocuments    *protocol.BrowserDocumentSummary      `json:"browser_documents,omitempty"`
	MessagingProgress   *protocol.MessagingProgressSnapshot   `json:"messaging_progress,omitempty"`
	Messaging           map[string]*protocol.MessagingSummary `json:"messaging,omitempty"`
	NativeEvidence      *storage.NativeIntervalEvidence       `json:"native_evidence,omitempty"`
	StatisticalFindings []StatisticalFinding                  `json:"statistical_findings,omitempty"`
	// ObservationState is stale for expired active observations and historical for
	// terminal projections. Values and sample timestamps remain unchanged.
	// ObservationValidUntil bounds freshness, including the oldest distributed
	// contributor; reading or relaying a snapshot never extends this deadline.
	ObservationValidUntil  *time.Time `json:"observation_valid_until,omitempty"`
	ObservationState       string     `json:"observation_state,omitempty"`
	CompletedIterations    int64      `json:"completed_iterations"`
	Timestamp              time.Time  `json:"timestamp"`
	ActiveVUsers           int64      `json:"active_vusers"`
	TPS                    float64    `json:"tps"`
	TargetTPS              float64    `json:"target_tps,omitempty"`
	ThroughputGapPct       float64    `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool       `json:"throughput_target_missed,omitempty"`
	AvgResponseMs          float64    `json:"avg_response_ms"`
	ErrorPct               float64    `json:"error_pct"`
	TotalTx                int64      `json:"total_transactions"`
	TotalErrors            int64      `json:"total_errors"`
	// ErrorPct, TotalTx and TotalErrors describe one sample window
	// (SampleWindowSeconds, normally 1 s). The Run* fields are the same
	// counters accumulated from the start of the run, so a run-level error
	// rate is not dominated by whichever second was sampled last. They are
	// pointers so samples recorded before run counters existed stay absent
	// (not a misleading zero) while new samples always carry them.
	RunTotalTx     *int64   `json:"run_total_transactions,omitempty"`
	RunTotalErrors *int64   `json:"run_total_errors,omitempty"`
	RunErrorPct    *float64 `json:"run_error_pct,omitempty"`
	// Requests counts every attempted request in the sample window, failed
	// ones included: every result that is not a business transaction, which
	// is the report's request population. RequestRate is Requests per
	// second. A script without transactions still sends requests, so these,
	// not TPS and the transaction counters, are the run's live throughput
	// and error rate whenever it has sent any. The RunRequest* fields are
	// the same counters from the start of the run, absent like the other
	// run counters on samples recorded before they existed. A run whose
	// request counts are incomplete (a generator in its fleet does not count
	// them, or a controller took it over from history without them) carries
	// none, run-to-date or window, so it is read in transactions.
	Requests                   int64                                 `json:"requests,omitempty"`
	RequestErrors              int64                                 `json:"request_errors,omitempty"`
	RequestRate                float64                               `json:"request_rate,omitempty"`
	RunTotalRequests           *int64                                `json:"run_total_requests,omitempty"`
	RunRequestErrors           *int64                                `json:"run_request_errors,omitempty"`
	RunRequestErrorPct         *float64                              `json:"run_request_error_pct,omitempty"`
	P50Ms                      float64                               `json:"p50_ms"`
	P90Ms                      float64                               `json:"p90_ms"`
	P95Ms                      float64                               `json:"p95_ms"`
	P99Ms                      float64                               `json:"p99_ms"`
	AvgDNSMs                   float64                               `json:"avg_dns_ms,omitempty"`
	AvgConnectMs               float64                               `json:"avg_connect_ms,omitempty"`
	AvgTLSMs                   float64                               `json:"avg_tls_ms,omitempty"`
	AvgTTFBMs                  float64                               `json:"avg_ttfb_ms,omitempty"`
	AvgWaitMs                  float64                               `json:"avg_wait_ms,omitempty"`
	ErrorBreakdown             map[string]int64                      `json:"error_breakdown,omitempty"`
	Transactions               map[string]*TxMetrics                 `json:"transactions,omitempty"`
	TransactionResponseTimeSLA []TransactionResponseTimeSLAResult    `json:"transaction_response_time_sla,omitempty"`
	HTTPVersions               *RunArchiveHTTPVersionMix             `json:"http_versions,omitempty"` // run-to-date version mix, ratios and Protocol Mix finding
	DependencyHealth           map[string]*TxMetrics                 `json:"dependency_health,omitempty"`
	DependencyMap              *LiveDependencyMap                    `json:"dependency_map,omitempty"`
	ErrorPatterns              []LiveErrorPattern                    `json:"error_patterns,omitempty"`
	SOAPOperations             map[string]*TxMetrics                 `json:"soap_operations,omitempty"`
	NetworkProfiles            map[string]*TxMetrics                 `json:"network_profiles,omitempty"`
	Properties                 map[string]*TxMetrics                 `json:"properties,omitempty"`
	ByRegion                   map[string]*TxMetrics                 `json:"by_region,omitempty"`
	GroupMetrics               map[string]*GroupLiveMetrics          `json:"groups,omitempty"`
	Browser                    *BrowserLiveMetrics                   `json:"browser,omitempty"`
	BrowserByDevice            map[string]*BrowserDeviceLiveMetrics  `json:"browser_by_device,omitempty"`
	GoalStatus                 *GoalStatus                           `json:"goal_status,omitempty"`
	CapacityStatus             *LiveCapacityStatus                   `json:"capacity_status,omitempty"`
	TelemetryPolicy            *TelemetryPolicyEvidence              `json:"telemetry_policy,omitempty"`
	TelemetryStatus            *TelemetryRunHealth                   `json:"telemetry_status,omitempty"`
	Telemetry                  *TelemetryHealth                      `json:"telemetry,omitempty"`
	Resources                  *engine.SystemResources               `json:"resources,omitempty"`
	GeneratorResources         map[string]*GeneratorResourceSnapshot `json:"generator_resources,omitempty"`
	Latency                    *NetworkLatency                       `json:"latency,omitempty"`
	ELK                        *ELKCaptureStatus                     `json:"elk,omitempty"`
	Dynatrace                  *DynatraceCaptureStatus               `json:"dynatrace,omitempty"`
	LoadLevels                 []ActiveLoadLevel                     `json:"load_levels,omitempty"`
	ActiveRuntimeSeconds       float64                               `json:"-"`
	SampleWindowSeconds        float64                               `json:"-"`
	ErrorEvidence              []LiveErrorEvidence                   `json:"-"`
	// slaTelemetryPeaks retains, per SLA metric evaluated against
	// per-transaction server telemetry, the worst transaction's summary. It is
	// written only by terminal compaction (compactTerminalLiveMetricsTail), which
	// drops Transactions from the resident terminal point, and read only through
	// ciGitHubCheckMaxServerTelemetry. Keys are the slaTelemetryMetric*
	// constants; values are immutable once retained. It is bounded by the fixed
	// metric set and is process-local: a point rehydrated from storage carries
	// none, exactly as it carries no Transactions.
	slaTelemetryPeaks map[string]*ServerTelemetrySummary
}

type GeneratorResourceSnapshot struct {
	PodCapacity     *protocol.CapacitySnapshot `json:"pod_capacity,omitempty"`
	GeneratorID     string                     `json:"generator_id"`
	Target          string                     `json:"target"`
	Region          string                     `json:"region,omitempty"`
	ActiveVUsers    int32                      `json:"active_vusers"`
	TPS             float64                    `json:"tps,omitempty"`
	ErrorPct        float64                    `json:"error_pct,omitempty"`
	P95Ms           float64                    `json:"p95_ms,omitempty"`
	P99Ms           float64                    `json:"p99_ms,omitempty"`
	SaturationState string                     `json:"saturation_state,omitempty"`
	ImpactReasons   []string                   `json:"impact_reasons,omitempty"`
	Resources       *engine.SystemResources    `json:"resources,omitempty"`
	Timestamp       time.Time                  `json:"timestamp"`
}

type LiveCapacityStatus struct {
	AllocatedVUsers       int                               `json:"allocated_vusers"`
	AvailableGenerators   int                               `json:"available_generators"`
	RecommendedGenerators int                               `json:"recommended_generators"`
	Saturated             bool                              `json:"saturated"`
	QueuedVUsers          int                               `json:"queued_vusers"`
	RejectedVUsers        int                               `json:"rejected_vusers"`
	ByProtocol            map[string]*LiveCapacityBreakdown `json:"by_protocol,omitempty"`
	ByRegion              map[string]*LiveCapacityBreakdown `json:"by_region,omitempty"`
}

type LiveCapacityBreakdown struct {
	AllocatedVUsers int `json:"allocated_vusers,omitempty"`
	QueuedVUsers    int `json:"queued_vusers,omitempty"`
	RejectedVUsers  int `json:"rejected_vusers,omitempty"`
}

type BrowserLiveMetrics struct {
	Samples              int64   `json:"samples"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type BrowserDeviceLiveMetrics struct {
	DeviceProfile        string  `json:"device_profile,omitempty"`
	DevicePlatform       string  `json:"device_platform,omitempty"`
	DeviceTier           string  `json:"device_tier,omitempty"`
	BrowserEngine        string  `json:"browser_engine,omitempty"`
	Samples              int64   `json:"samples"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type ServerTelemetrySummary struct {
	Messaging                 *protocol.MessagingSummary   `json:"messaging,omitempty"`
	ConsumerLagSamples        int64                        `json:"consumer_lag_samples,omitempty"`
	ConsumerOffsetLagSamples  int64                        `json:"consumer_offset_lag_samples,omitempty"`
	DiagnosticFindings        []protocol.DiagnosticFinding `json:"diagnostic_findings,omitempty"`
	FirstEventSamples         int64                        `json:"first_event_samples,omitempty"`
	WebSocket                 *protocol.WebSocketSummary   `json:"websocket,omitempty"`
	GRPC                      *protocol.GRPCSummary        `json:"grpc,omitempty"`
	Samples                   int64                        `json:"samples"`
	AvgServerProcessingMs     float64                      `json:"avg_server_processing_ms,omitempty"`
	AvgTransferMs             float64                      `json:"avg_transfer_ms,omitempty"`
	AvgRequestPayloadKB       float64                      `json:"avg_request_payload_kb,omitempty"`
	AvgResponsePayloadKB      float64                      `json:"avg_response_payload_kb,omitempty"`
	RequestPayloadHistogram   map[string]int64             `json:"request_payload_histogram,omitempty"`
	ResponsePayloadHistogram  map[string]int64             `json:"response_payload_histogram,omitempty"`
	AvgFirstEventMs           float64                      `json:"avg_first_event_ms,omitempty"`
	MaxFirstEventMs           float64                      `json:"max_first_event_ms,omitempty"`
	EventCount                int64                        `json:"event_count,omitempty"`
	AvgMessageLatencyMs       float64                      `json:"avg_message_latency_ms,omitempty"`
	MaxMessageLatencyMs       float64                      `json:"max_message_latency_ms,omitempty"`
	MessagesSent              int64                        `json:"messages_sent,omitempty"`
	MessagesReceived          int64                        `json:"messages_received,omitempty"`
	AvgMessagesSentPerSec     float64                      `json:"avg_messages_sent_per_sec,omitempty"`
	AvgMessagesReceivedPerSec float64                      `json:"avg_messages_received_per_sec,omitempty"`
	AvgConsumerLagMs          float64                      `json:"avg_consumer_lag_ms,omitempty"`
	MaxConsumerLagMs          float64                      `json:"max_consumer_lag_ms,omitempty"`
	AvgConsumerOffsetLag      float64                      `json:"avg_consumer_offset_lag,omitempty"`
	MaxConsumerOffsetLag      int64                        `json:"max_consumer_offset_lag,omitempty"`
	SendFailureCount          int64                        `json:"send_failure_count,omitempty"`
	RedeliveryCount           int64                        `json:"redelivery_count,omitempty"`
	DeadLetteredCount         int64                        `json:"dead_lettered_count,omitempty"`
	DuplicateCount            int64                        `json:"duplicate_count,omitempty"`
	OutOfOrderCount           int64                        `json:"out_of_order_count,omitempty"`
	CommitCount               int64                        `json:"commit_count,omitempty"`
	AbortCount                int64                        `json:"abort_count,omitempty"`
	// Derived reliability rates (P4.1), percent of received messages.
	RedeliveryRatePct float64 `json:"redelivery_rate_pct,omitempty"`
	DeadLetterRatePct float64 `json:"dead_letter_rate_pct,omitempty"`
	DuplicateRatePct  float64 `json:"duplicate_rate_pct,omitempty"`
	// Event Hubs / streaming ingestion aggregates (EH.4).
	TotalThrottledRequests  int64            `json:"total_throttled_requests,omitempty"`
	TotalServerBusyCount    int64            `json:"total_server_busy_count,omitempty"`
	TotalQuotaExceededCount int64            `json:"total_quota_exceeded_count,omitempty"`
	TotalEventTooLargeCount int64            `json:"total_event_too_large_count,omitempty"`
	AvgEndToEndLatencyMs    float64          `json:"avg_end_to_end_latency_ms,omitempty"`
	MaxEndToEndLatencyMs    float64          `json:"max_end_to_end_latency_ms,omitempty"`
	PartitionEvents         map[string]int64 `json:"partition_events,omitempty"`
	PartitionSkewPct        float64          `json:"partition_skew_pct,omitempty"`
	RequestSizeClasses      map[string]int64 `json:"request_size_classes,omitempty"`
	ResponseSizeClasses     map[string]int64 `json:"response_size_classes,omitempty"`
	ConnectionReusedCount   int64            `json:"connection_reused_count,omitempty"`
	ConnectionReusedPct     float64          `json:"connection_reused_pct,omitempty"`
}

type TelemetryHealth struct {
	DroppedResultsAvailable  bool      `json:"dropped_results_available,omitempty"`
	DroppedResults           int64     `json:"dropped_results,omitempty"`
	SheddedArrivals          int64     `json:"shedded_arrivals,omitempty"`
	DroppedSubscriberBatches int64     `json:"dropped_subscriber_batches,omitempty"`
	MissingMetricWindows     int64     `json:"missing_metric_windows,omitempty"`
	HistoryPoints            int       `json:"history_points,omitempty"`
	WebsocketClients         int       `json:"websocket_clients,omitempty"`
	WebsocketDroppedMessages int64     `json:"websocket_dropped_messages,omitempty"`
	WebsocketDroppedEvents   int64     `json:"websocket_dropped_events,omitempty"`
	GeneratorPublishedAt     time.Time `json:"generator_published_at,omitempty"`
	ControllerReceivedAt     time.Time `json:"controller_received_at,omitempty"`
	CollectedAt              time.Time `json:"collected_at,omitempty"`
	PublishedAt              time.Time `json:"published_at,omitempty"`
	GeneratorProcessingLagMs float64   `json:"generator_processing_lag_ms,omitempty"`
	TransportLagMs           float64   `json:"transport_lag_ms,omitempty"`
	ControllerFanoutLagMs    float64   `json:"controller_fanout_lag_ms,omitempty"`
	BrowserRenderLagMs       float64   `json:"browser_render_lag_ms,omitempty"`
	SampleAgeMs              float64   `json:"sample_age_ms,omitempty"`
	// StageDistributions are the run's per-stage latency distributions so
	// far (C5), one entry per stage; percentiles only where sampled.
	StageDistributions []TelemetryStageDistribution `json:"stage_distributions,omitempty"`
	// Sources rolls up per-generator source epochs: publications lost
	// between received sequence numbers, exact or a lower bound.
	Sources *TelemetrySourceSummary `json:"sources,omitempty"`
	// Loss is the run's total of each loss counter, exact, an observed lower
	// bound or unavailable, with the reason it is not exact. The cumulative
	// fields above stay as reported.
	Loss *TelemetryLossAccounting `json:"loss,omitempty"`
	// Coverage is the run's wall-time observation coverage so far: the
	// time every required generator was observed, and the unknown rest.
	Coverage *TelemetryCoverage `json:"coverage,omitempty"`
	// Acceptance is the run's provisional telemetry verdict under its pinned
	// acceptance policy; the finalized verdict is in the run report.
	Acceptance *TelemetryAcceptanceVerdict `json:"acceptance,omitempty"`
	// restoredFromHistory marks health rebuilt from a stored interval
	// summary (a restarted controller, a follower): it carries none of the
	// loss, coverage or stage evidence a verdict reads, so none is shown.
	restoredFromHistory bool
	// TransportLagQuality and SampleAgeQuality qualify the stages measured
	// across a generator's clock and the controller's: qualified when a
	// current offset calibration corrected them. Absent for a run executed
	// in the controller's own process.
	TransportLagQuality *generatorrpc.ClockQuality `json:"transport_lag_quality,omitempty"`
	SampleAgeQuality    *generatorrpc.ClockQuality `json:"sample_age_quality,omitempty"`
	// collectedAtQuality and collectedAtOffsetMs map the oldest sample's
	// collection time to the controller's clock when the age is computed.
	collectedAtQuality  *generatorrpc.ClockQuality
	collectedAtOffsetMs float64
	// collectedAtBoundUnixMs is the controller-clock instant the qualified
	// generators' oldest samples were certainly collected by (0 = none).
	collectedAtBoundUnixMs float64
	// SampleAgeUnavailable: no generator reported a sample, so no sample-age
	// budget decision is made. It is retained with the interval so a later
	// re-evaluation makes the same decision.
	SampleAgeUnavailable bool `json:"sample_age_unavailable,omitempty"`
	// qualifiedAt and qualifiedMonotonic are the controller's wall time and
	// monotonic reading when collectedAtQuality was judged; a step between
	// then and publication voids it.
	qualifiedAt        time.Time
	qualifiedMonotonic time.Duration
}

type TelemetryRunHealth struct {
	Status           string   `json:"status"`
	Severity         string   `json:"severity"`
	Summary          string   `json:"summary,omitempty"`
	CurrentSignature string   `json:"current_signature,omitempty"`
	Conditions       []string `json:"conditions,omitempty"`
	// Unscored lists the budget decisions on cross-host durations that could
	// not be scored (stage:reason); with no breach the status is "unscored".
	Unscored          []string   `json:"unscored,omitempty"`
	BreachActive      bool       `json:"breach_active,omitempty"`
	SustainedBreach   bool       `json:"sustained_breach,omitempty"`
	BreachDurationSec int        `json:"breach_duration_sec,omitempty"`
	BreachStartedAt   *time.Time `json:"breach_started_at,omitempty"`
	LastObservedAt    time.Time  `json:"last_observed_at"`
	LastTransitionAt  time.Time  `json:"last_transition_at,omitempty"`
	// recovered: this healthy status ends a degraded or breached spell that
	// was announced (an unscored stretch in between does not end it);
	// announce: this degraded or breached status is not the one announced.
	recovered, announce bool
}

type NetworkLatency struct {
	Targets []LatencyTarget `json:"targets"`
}

type LatencyTarget struct {
	Name           string    `json:"name"`
	Endpoint       string    `json:"endpoint"`
	LatencyMs      float64   `json:"latency_ms"`
	ProbeTotalMs   float64   `json:"probe_total_ms,omitempty"`
	DNSMs          float64   `json:"dns_ms,omitempty"`
	ConnectMs      float64   `json:"connect_ms,omitempty"`
	TLSMs          float64   `json:"tls_ms,omitempty"`
	TTFBMs         float64   `json:"ttfb_ms,omitempty"`
	HTTPStatus     int       `json:"http_status,omitempty"`
	ProbeMethod    string    `json:"probe_method,omitempty"`
	IP             string    `json:"ip,omitempty"`
	Hops           int       `json:"hops,omitempty"`
	LastSuccess    bool      `json:"last_success"`
	JitterMs       float64   `json:"jitter_ms,omitempty"`
	SuccessRatePct float64   `json:"success_rate_pct,omitempty"`
	SampleCount    int       `json:"sample_count,omitempty"`
	RouteStatus    string    `json:"route_status,omitempty"`
	Error          string    `json:"error,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
}

type GoalStatus struct {
	Mode                      string     `json:"mode"`
	CurrentTPS                float64    `json:"current_tps"`
	CurrentTotalVUsers        int        `json:"current_total_vusers"`
	TargetAverageTPS          *float64   `json:"target_average_tps,omitempty"`
	MaxErrorCount             *int       `json:"max_error_count,omitempty"`
	MinVUsers                 *int       `json:"min_vusers,omitempty"`
	MaxVUsers                 *int       `json:"max_vusers,omitempty"`
	SuggestedTotalVUsers      int        `json:"suggested_total_vusers,omitempty"`
	LastScaleDirection        string     `json:"last_scale_direction,omitempty"`
	LastScaleReason           string     `json:"last_scale_reason,omitempty"`
	LastScaleAt               *time.Time `json:"last_scale_at,omitempty"`
	LatencyBreached           bool       `json:"latency_breached,omitempty"`
	ErrorBreached             bool       `json:"error_breached,omitempty"`
	ServerProcessingBreached  bool       `json:"server_processing_breached,omitempty"`
	TransferBreached          bool       `json:"transfer_breached,omitempty"`
	ErrorCountBreached        bool       `json:"error_count_breached,omitempty"`
	LCPBreached               bool       `json:"lcp_breached,omitempty"`
	INPBreached               bool       `json:"inp_breached,omitempty"`
	CLSBreached               bool       `json:"cls_breached,omitempty"`
	ThrottledRequestsBreached bool       `json:"throttled_requests_breached,omitempty"`
	EndToEndLatencyBreached   bool       `json:"end_to_end_latency_breached,omitempty"`
	PartitionSkewBreached     bool       `json:"partition_skew_breached,omitempty"`
	RedeliveryRateBreached    bool       `json:"redelivery_rate_breached,omitempty"`
	DeadLetterRateBreached    bool       `json:"dead_letter_rate_breached,omitempty"`
	BreachActive              bool       `json:"breach_active,omitempty"`
	BreachDurationSec         int        `json:"breach_duration_sec,omitempty"`
	StopTriggered             bool       `json:"stop_triggered,omitempty"`
	StopReason                string     `json:"stop_reason,omitempty"`
}

type GoalTargetStatus struct {
	Group     string    `json:"group"`
	TargetTPS float64   `json:"target_tps"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type RunComparisonBaseline struct {
	SourceTestID     string                `json:"source_test_id"`
	SourceTestName   string                `json:"source_test_name"`
	Policy           string                `json:"policy,omitempty"`
	CompletedAt      time.Time             `json:"completed_at,omitempty"`
	Transactions     map[string]*TxMetrics `json:"transactions,omitempty"`
	DependencyHealth map[string]*TxMetrics `json:"dependency_health,omitempty"`
}

type TelemetryDiagnostic struct {
	Severity   string   `json:"severity"`
	Summary    string   `json:"summary"`
	Conditions []string `json:"conditions,omitempty"`
	// Unscored lists budget decisions on cross-host durations that could not
	// be made, with the reason (an unqualified clock, or an uncertainty that
	// straddles the threshold): neither a breach nor a pass.
	Unscored  []string           `json:"unscored,omitempty"`
	Samples   []DiagnosticSample `json:"samples,omitempty"`
	Telemetry *TelemetryHealth   `json:"telemetry,omitempty"`
}

type DiagnosticSample struct {
	Issue                     string   `json:"issue"`
	Transaction               string   `json:"transaction"`
	DependencyType            string   `json:"dependency_type,omitempty"`
	DependencyTarget          string   `json:"dependency_target,omitempty"`
	CorrelationIDs            []string `json:"correlation_ids,omitempty"`
	Count                     int64    `json:"count,omitempty"`
	Errors                    int64    `json:"errors,omitempty"`
	ErrorPct                  float64  `json:"error_pct,omitempty"`
	AvgMs                     float64  `json:"avg_ms,omitempty"`
	P99Ms                     float64  `json:"p99_ms,omitempty"`
	AvgMessageLatencyMs       float64  `json:"avg_message_latency_ms,omitempty"`
	AvgConsumerLagMs          float64  `json:"avg_consumer_lag_ms,omitempty"`
	AvgMessagesSentPerSec     float64  `json:"avg_messages_sent_per_sec,omitempty"`
	AvgMessagesReceivedPerSec float64  `json:"avg_messages_received_per_sec,omitempty"`
	AvgEndToEndLatencyMs      float64  `json:"avg_end_to_end_latency_ms,omitempty"`
	ThrottledRequests         int64    `json:"throttled_requests,omitempty"`
	PartitionSkewPct          float64  `json:"partition_skew_pct,omitempty"`
}

type goalControllerState struct {
	breachStartedAt         *time.Time
	lastScaleAt             *time.Time
	lastScaleDirection      string
	lastScaleReason         string
	cumulativeErrors        int64
	errorWindowsTracked     bool
	lastErrorWindowBySource map[string]time.Time
}

type telemetryDiagnosticState struct {
	LastSignature   string
	LastPublishedAt time.Time
	Health          *TelemetryRunHealth
	BreachStartedAt *time.Time
	// announced is the degraded or breached status last announced, until a
	// healthy status ends the spell (an unscored stretch does not).
	announced string
}

type goalControllerUpdate struct {
	Status         *GoalStatus
	ShouldScale    bool
	TargetTotal    int
	ScaleDirection string
	ScaleReason    string
	ShouldStop     bool
	StopReason     string
}

type TxMetrics struct {
	messagingAggregate bool
	DurationDigest     *engine.TDigestSnapshot `json:"-"`
	Name               string                  `json:"name"`
	TPS                float64                 `json:"tps"`
	DependencyType     string                  `json:"dependency_type,omitempty"`
	DependencyTarget   string                  `json:"dependency_target,omitempty"`
	LastErrorMessage   string                  `json:"last_error_message,omitempty"`
	LastStatusCode     int                     `json:"last_status_code,omitempty"`
	LastErrorAt        time.Time               `json:"last_error_at,omitempty"`
	ServerTelemetry    *ServerTelemetrySummary `json:"server_telemetry,omitempty"`
	AvgDNSMs           float64                 `json:"avg_dns_ms,omitempty"`
	AvgConnectMs       float64                 `json:"avg_connect_ms,omitempty"`
	AvgTLSMs           float64                 `json:"avg_tls_ms,omitempty"`
	AvgTTFBMs          float64                 `json:"avg_ttfb_ms,omitempty"`
	CorrelationIDs     []string                `json:"correlation_ids,omitempty"`
	CorrelationCount   int64                   `json:"correlation_count,omitempty"`
	Count              int64                   `json:"count"`
	Errors             int64                   `json:"errors"`
	AvgMs              float64                 `json:"avg_ms"`
	P50Ms              float64                 `json:"p50_ms"`
	P90Ms              float64                 `json:"p90_ms"`
	P95Ms              float64                 `json:"p95_ms,omitempty"`
	P99Ms              float64                 `json:"p99_ms"`
	// Exemplars are this window's requests whose trace context was sent.
	Exemplars []engine.TraceExemplar `json:"exemplars,omitempty"`
}

type LiveErrorPattern struct {
	Signature      string    `json:"signature"`
	Sample         string    `json:"sample"`
	Count          int64     `json:"count"`
	FirstSeen      time.Time `json:"first_seen"`
	LatestSeen     time.Time `json:"latest_seen"`
	Actions        []string  `json:"actions,omitempty"`
	AffectedGroups []string  `json:"affected_groups,omitempty"`
	StatusCode     int       `json:"status_code,omitempty"`
	// Deprecated: no longer set (it was one action's one-second error rate).
	ErrorPct float64 `json:"error_pct,omitempty"`
	// ShareOfErrorsPct is this signature's share of the errors counted across
	// the run's tracked signatures (they add up to 100).
	ShareOfErrorsPct float64 `json:"share_of_errors_pct,omitempty"`
	SnapshotURL      string  `json:"snapshot_url,omitempty"`
	EvidenceURL      string  `json:"evidence_url,omitempty"`
	EvidenceCount    int     `json:"evidence_count,omitempty"`
	LatestEvidenceID string  `json:"latest_evidence_id,omitempty"`
}

type LiveErrorEvidence struct {
	ID             string                  `json:"id"`
	Signature      string                  `json:"signature"`
	Action         string                  `json:"action"`
	Group          string                  `json:"group,omitempty"`
	Timestamp      time.Time               `json:"timestamp"`
	DurationMs     float64                 `json:"duration_ms"`
	Status         string                  `json:"status"`
	StatusCode     int                     `json:"status_code,omitempty"`
	ErrorMessage   string                  `json:"error_message,omitempty"`
	Snapshot       *protocol.ErrorSnapshot `json:"snapshot,omitempty"`
	Region         string                  `json:"region,omitempty"`
	NetworkProfile string                  `json:"network_profile,omitempty"`
	Hierarchy      string                  `json:"hierarchy,omitempty"`
	Phase          string                  `json:"phase,omitempty"`
}

type LiveDependencyMap struct {
	Nodes []LiveDependencyNode `json:"nodes"`
	Edges []LiveDependencyEdge `json:"edges"`
}

type LiveDependencyNode struct {
	ID             string  `json:"id"`
	Label          string  `json:"label"`
	Kind           string  `json:"kind"`
	DependencyType string  `json:"dependency_type,omitempty"`
	Status         string  `json:"status,omitempty"`
	Transactions   int64   `json:"transactions,omitempty"`
	Errors         int64   `json:"errors,omitempty"`
	ErrorPct       float64 `json:"error_pct,omitempty"`
	P95Ms          float64 `json:"p95_ms,omitempty"`
}

type LiveDependencyEdge struct {
	Source         string  `json:"source"`
	Target         string  `json:"target"`
	DependencyType string  `json:"dependency_type,omitempty"`
	Transactions   int64   `json:"transactions,omitempty"`
	Errors         int64   `json:"errors,omitempty"`
	ErrorPct       float64 `json:"error_pct,omitempty"`
	P95Ms          float64 `json:"p95_ms,omitempty"`
}

type GroupLiveMetrics struct {
	Name            string   `json:"name"`
	VUsers          int      `json:"vusers"`
	Transactions    int64    `json:"transactions"`
	Errors          int64    `json:"errors"`
	RunTransactions *int64   `json:"run_transactions,omitempty"`
	RunErrors       *int64   `json:"run_errors,omitempty"`
	RunErrorPct     *float64 `json:"run_error_pct,omitempty"`
	// Request counters as on LiveMetrics, for the group's own requests.
	Requests               int64    `json:"requests,omitempty"`
	RequestErrors          int64    `json:"request_errors,omitempty"`
	RequestRate            float64  `json:"request_rate,omitempty"`
	RunRequests            *int64   `json:"run_requests,omitempty"`
	RunRequestErrors       *int64   `json:"run_request_errors,omitempty"`
	RunRequestErrorPct     *float64 `json:"run_request_error_pct,omitempty"`
	TPS                    float64  `json:"tps"`
	TargetTPS              float64  `json:"target_tps,omitempty"`
	ThroughputGapPct       float64  `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool     `json:"throughput_target_missed,omitempty"`
	AvgResponseMs          float64  `json:"avg_response_ms"`
	P50Ms                  float64  `json:"p50_ms"`
	P90Ms                  float64  `json:"p90_ms"`
	P95Ms                  float64  `json:"p95_ms,omitempty"`
	P99Ms                  float64  `json:"p99_ms"`
	// PercentileQuality has the same provenance vocabulary as an archived
	// interval summary. It is normally omitted from live metrics and populated
	// when distributed archive finalization must distinguish a source-generator
	// quantile from an exact raw-event recomputation or an unavailable fleet
	// distribution.
	PercentileQuality string                  `json:"percentile_quality,omitempty"`
	DurationDigest    *engine.TDigestSnapshot `json:"-"`
}

type StoredScript struct {
	ContentRevision     string                     `json:"content_revision,omitempty"`
	AuthoringProjection *ScriptAuthoringProjection `json:"authoring_projection,omitempty"`
	ArchivedAt          *time.Time                 `json:"archived_at,omitempty"`
	Activity            *ObjectActivitySummary     `json:"activity,omitempty"`
	LibraryAssignment   *storage.LibraryAssignment `json:"library_assignment,omitempty"`
	ID                  string                     `json:"id"`
	RefKey              string                     `json:"ref_key,omitempty"`
	Name                string                     `json:"name"`
	Protocol            string                     `json:"protocol"`
	Version             string                     `json:"version"`
	ProjectID           string                     `json:"project_id,omitempty"`
	FolderID            *string                    `json:"folder_id,omitempty"`
	CreatedAt           time.Time                  `json:"created_at"`
	ScenarioCount       int                        `json:"scenario_count,omitempty"`
	AssociatedScenarios []ScenarioRef              `json:"associated_scenarios,omitempty"`
	AssociatedReleases  []ReleaseRef               `json:"associated_releases,omitempty"`
	ScenarioRunReleases []ReleaseRef               `json:"scenario_run_releases,omitempty"`
	LastRunAt           *time.Time                 `json:"last_run_at,omitempty"`
	AuthoringHealth     *AuthoringHealth           `json:"authoring_health,omitempty"`
	Script              *dsl.Script                `json:"script"`
}

type StoredScenario struct {
	ArchivedAt         *time.Time                        `json:"archived_at,omitempty"`
	Activity           *ObjectActivitySummary            `json:"activity,omitempty"`
	TerminationMode    string                            `json:"termination_mode,omitempty"`
	LibraryAssignment  *storage.LibraryAssignment        `json:"library_assignment,omitempty"`
	ID                 string                            `json:"id"`
	RefKey             string                            `json:"ref_key,omitempty"`
	Name               string                            `json:"name"`
	WorkloadShapeID    string                            `json:"workload_shape_id,omitempty"`
	ProjectID          string                            `json:"project_id,omitempty"`
	Duration           string                            `json:"duration"`
	StartDelay         string                            `json:"start_delay,omitempty"`
	ScenarioMode       string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout     bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline   bool                              `json:"resource_baseline,omitempty"`
	Assignment         *GeneratorAssignmentSettings      `json:"assignment,omitempty"`
	GoalConfig         *GoalConfig                       `json:"goal_config,omitempty"`
	TrafficProfile     *TrafficProfile                   `json:"traffic_profile,omitempty"`
	RuntimeSettings    *RuntimeSettingsOverride          `json:"runtime_settings,omitempty"`
	Scripts            []ScenarioScriptRef               `json:"scripts"`
	Schedules          []*storage.ScenarioSchedule       `json:"schedules,omitempty"`
	DataPools          []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	CreatedAt          time.Time                         `json:"created_at"`
	UpdatedAt          time.Time                         `json:"updated_at"`
	ScriptCount        int                               `json:"script_count"`
	ResultCount        int                               `json:"result_count,omitempty"`
	AssociatedReleases []ReleaseRef                      `json:"associated_releases,omitempty"`
	LastRunTestID      string                            `json:"last_run_test_id,omitempty"`
	LastRunAt          *time.Time                        `json:"last_run_at,omitempty"`
	LastRunEndedAt     *time.Time                        `json:"last_run_ended_at,omitempty"`
	// ContentRevision and AuthoringProjection are set on create and save
	// responses from the snapshot read in the write transaction, and on an
	// idempotent create replay only when the durable snapshot is the state in
	// the body (equal updated_at).
	ContentRevision     string                       `json:"content_revision,omitempty"`
	AuthoringProjection *ScenarioAuthoringProjection `json:"authoring_projection,omitempty"`
}

type ScenarioScriptRef struct {
	Order                        int                          `json:"order"`
	Name                         string                       `json:"name,omitempty"`
	ScriptID                     string                       `json:"script_id"`
	ScriptName                   string                       `json:"script_name,omitempty"`
	Protocol                     string                       `json:"protocol,omitempty"`
	Role                         string                       `json:"role,omitempty"`
	VUsers                       int                          `json:"vusers"`
	RampUp                       string                       `json:"ramp_up,omitempty"`
	RampDown                     string                       `json:"ramp_down,omitempty"`
	StartOffset                  string                       `json:"start_offset,omitempty"`
	Stages                       []storage.LoadStage          `json:"stages,omitempty"`
	Iterations                   *int                         `json:"iterations,omitempty"`
	Initialization               string                       `json:"initialization,omitempty"`
	RuntimeSettings              *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	Assignment                   *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	TransactionResponseTimeSLAMs *float64                     `json:"transaction_response_time_sla_ms,omitempty"`
}

const (
	generatorAssignmentModeInherit          = "inherit"
	generatorAssignmentModeAuto             = "auto"
	generatorAssignmentModeSpecificHosts    = "specific_hosts"
	generatorAssignmentModeRegionWeighted   = "region_weighted"
	generatorAssignmentModeLoadZoneWeighted = "load_zone_weighted"
	generatorShardStrategyCapacityWeighted  = "capacity_weighted"
	maxManagedTestHistory                   = 3600
	maxManagedTestHistoryBytes              = 64 << 20
	telemetryDiagnosticSampleInterval       = 5 * time.Second
	// thresholdAlertRenotifyInterval bounds threshold-alert webhook re-fires: a
	// metric oscillating around its threshold re-notifies at most this often.
	thresholdAlertRenotifyInterval       = 5 * time.Minute
	telemetrySustainedBreachWindow       = 15 * time.Second
	maxTelemetryDiagnosticSamples        = 3
	maxLiveErrorEvidencePerRun           = 256
	maxLiveErrorEvidencePerSignature     = 50
	defaultLiveErrorEvidenceLimit        = 20
	maxLiveErrorEvidenceLimit            = 100
	maxLiveErrorPatternsPerRun           = 256
	slowTransactionDiagnosticThresholdMs = 1000.0
	jmsBrokerPressureMinLagMs            = 200.0
	jmsBrokerPressureLagMultiplier       = 2.0
	jmsBrokerPressureSendRecvRatio       = 1.5
	// eventHubsPartitionSkewWarnPct flags a producer transaction whose per-partition
	// produce volume has a coefficient of variation at/above this percent — a hot
	// partition that will throttle before the namespace aggregate does.
	eventHubsPartitionSkewWarnPct = 50.0
	latencyStabilityWindowSize    = 12
	latencyRouteCacheTTL          = 5 * time.Minute
)

type latencyProbeSample struct {
	totalMs   float64
	success   bool
	timestamp time.Time
}

type latencyRouteCacheEntry struct {
	hops      int
	status    string
	checkedAt time.Time
}

type GeneratorAssignmentSettings struct {
	// RecoverySpareVUsers opts into single-worker spare admission. It is a quota
	// envelope for dedicated idle spare pods, not additional executable VUsers.
	RecoverySpareVUsers int `json:"recovery_spare_vusers,omitempty"`

	SourceAffinity  string                      `json:"source_affinity,omitempty"` // single_generator: preserved source-engine requirement; execution currently gated.
	SharingMode     string                      `json:"sharing_mode,omitempty"`
	Mode            string                      `json:"mode,omitempty"`
	SelectedTargets []string                    `json:"selected_targets,omitempty"`
	RegionWeights   map[string]int              `json:"region_weights,omitempty"`
	LoadZoneWeights map[string]int              `json:"load_zone_weights,omitempty"`
	ShardStrategy   string                      `json:"shard_strategy,omitempty"`
	RequiredLabels  map[string]string           `json:"required_labels,omitempty"`
	JavaHost        *JavaHostAssignmentSettings `json:"java_host,omitempty"`
	// TolerateGeneratorLoss allows the controller to keep a distributed run alive
	// when one generator disappears by resharding safe stateless groups onto
	// surviving generators. Strict failure remains the default.
	TolerateGeneratorLoss bool `json:"tolerate_generator_loss,omitempty"`
	// PoolID restricts a run to the generators in a managed fleet pool. It is
	// resolved to the pool's member targets at create time (as specific_hosts),
	// then enforced leniently at placement (offline members are skipped).
	// Supported only on the TEST-level assignment; a pool_id on a group-level
	// assignment is rejected (it would never be resolved — see
	// normalizeGroupGeneratorAssignmentSettings).
	PoolID string `json:"pool_id,omitempty"`
	// FailoverPolicy selects what happens to this run if the HA execution leader
	// is lost mid-run: "fail" (default; the successor marks it FAILED) or
	// "continue" (the successor adopts the running generators). It is
	// test-scoped and only takes effect for distributed runs on an HA
	// controller with a configured key wrapper; see
	// docs/HA_RUN_CONTINUITY_PLAN.md.
	FailoverPolicy string `json:"failover_policy,omitempty"`
}

// fleetMembershipReader resolves a fleet pool's member generator targets (the
// store satisfies it). Used to make generator assignment pool-aware.
type fleetMembershipReader interface {
	GetFleetPool(id string) (*storage.FleetPool, error)
	ListFleetPools(limit int, offset int) ([]*storage.FleetPool, error)
	ListFleetPoolMembers(poolID string) ([]*storage.FleetPoolMember, error)
	ListGeneratorMetadata(limit int, offset int) ([]*storage.GeneratorMetadata, error)
}

type ScenarioFootprint struct {
	TotalVUsers      int     `json:"total_vusers"`
	Duration         string  `json:"duration"`
	ComplexityScore  float64 `json:"complexity_score"`
	EstimatedCPU     float64 `json:"estimated_cpu_cores"`
	EstimatedMemory  float64 `json:"estimated_memory_mb"`
	EstimatedNetwork float64 `json:"estimated_network_mbps"`
}

type AssignmentRecommendation struct {
	Mode               string              `json:"mode"`
	AvailableTargets   []string            `json:"available_targets,omitempty"`
	AvailableRegions   []string            `json:"available_regions,omitempty"`
	AvailableLabels    map[string][]string `json:"available_labels,omitempty"`
	RecommendedTargets []string            `json:"recommended_targets,omitempty"`
	Summary            string              `json:"summary,omitempty"`
	Warnings           []string            `json:"warnings,omitempty"`
}

type ReleaseRef struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ApplicationID string `json:"application_id"`
}

type ScenarioRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type archiveRecorder interface {
	engine.ResultSink
	RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error
	RecordIntervalSummary(live *LiveMetrics) error
	Finalize(status string, completedAt time.Time, errStr string, slaEvaluated bool, slaBreached bool, slaDetails []string) (*storage.RunArchiveRecord, error)
}

type runArtifacts struct {
	runner                   *TestRunner
	test                     *ManagedTest
	recorder                 archiveRecorder
	saveRecord               func(record *storage.RunArchiveRecord) error
	externalMetrics          *externalMetricCollector
	monitorMu                sync.Mutex
	monitorStarted           bool
	monitorStopped           bool
	monitorCancel            context.CancelFunc
	monitorWorkers           sync.WaitGroup
	monitorHTTPClient        *http.Client // Optional fixture client; production uses the restricted client.
	elkPollMu                sync.Mutex
	elkMu                    sync.Mutex
	elkLastPoll              time.Time
	elkStatus                *ELKCaptureStatus
	elkSampleIDs             map[string]struct{}
	elkLookups               exemplarLookupSet // guarded by elkMu
	elkTraceIDs              map[string]struct{}
	elasticTraceEnabled      bool
	elasticTraceLimitReached bool
	elkSamples               []ELKLogSample
	dynatracePollMu          sync.Mutex
	dynatraceMu              sync.Mutex
	dynatraceLastPoll        time.Time
	dynatraceStatus          *DynatraceCaptureStatus
	dynatraceEvidenceIDs     map[string]struct{}
	dynatraceLookups         exemplarLookupSet // legacy capture polls; guarded by dynatraceMu
	nativeTraceLookups       exemplarLookupSet // native trace proof; guarded by dynatraceMu
	nativeTraceStored        map[string]bool   // guarded by dynatraceMu
	nativeDynatrace          atomic.Bool
	nativeTraceChecked       bool // guarded by monitorMu
	dynatraceLogs            []DynatraceLogSample
	dynatraceProblems        []DynatraceProblemSummary
	slaEvaluated             bool
	slaBreached              bool
	slaDetails               []string
	// Provider lookup outcomes for distributed archives; local runs also
	// stream them through the archive writer.
	lookupMu                 sync.Mutex
	providerLookups          []ProviderLookupRecord
	providerLookupsTruncated bool
}

// TestRunnerOption customizes a TestRunner at construction. Options are applied
// before stored state is loaded so they can influence boot reconciliation (e.g.
// suppressing it on a non-leader replica under HA leader election).
type TestRunnerOption func(*TestRunner)

// WithExecutionLeadership attaches an HA execution-leader coordinator. When the
// replica is not (yet) the leader, boot reconciliation and all execution
// mutations are suppressed until leadership is acquired.
func WithExecutionLeadership(leadership *executionLeadership) TestRunnerOption {
	return func(tr *TestRunner) {
		tr.leadership = leadership
		tr.durableExecutionFence = leadership != nil && sameStoreInstance(leadership.store, tr.testStore)
	}
}

// WithExecutionBackend installs the execution backend before persisted state is
// loaded and reconciled. Pending tests bind their backend when promoted.
func WithExecutionBackend(backend testExecutionBackend) TestRunnerOption {
	return func(tr *TestRunner) {
		if backend != nil {
			tr.execution = backend
		}
	}
}

func WithProjectSecretStore(store secretstore.Store) TestRunnerOption {
	return func(tr *TestRunner) {
		tr.projectSecretStore = store
	}
}

func WithApplicationSecretStore(store secretstore.Store) TestRunnerOption {
	return func(tr *TestRunner) { tr.applicationSecretStore = store }
}

func withSecretReferenceMutationCoordinator(coordinator *projectMutationCoordinator) TestRunnerOption {
	return func(tr *TestRunner) {
		if coordinator != nil {
			tr.secretReferenceMutations = coordinator
		}
	}
}

func (tr *TestRunner) secretResolverForProject(projectID string) engine.SecretResolver {
	if secretBindingEnforcementDisabled() {
		// Explicitly opted-in development mode keeps secret placeholders runnable
		// without governed project secret setup. A configured secret always wins,
		// and ONLY a genuine miss falls back to the placeholder: the store is
		// probed directly rather than through EngineResolver, because that helper
		// collapses provider errors (timeout, 5xx, throttling) into the same
		// ("", false) it returns for a miss. Substituting a fake credential on a
		// transient failure would be cached for the whole run and silently mask a
		// real configured secret, so provider errors fail closed instead.
		var store secretstore.Store
		if tr != nil {
			store = tr.projectSecretStore
		}
		envFallback := engine.NewProjectSecretResolver(projectID)
		return engine.CacheSecretResolverForRun(engine.ContextSecretResolverFunc(func(parent context.Context, name string) (string, bool) {
			if strings.TrimSpace(name) == "" {
				return "", false
			}
			if parent == nil {
				parent = context.Background()
			}
			if store != nil {
				ctx, cancel := context.WithTimeout(parent, 5*time.Second)
				value, _, err := store.Get(ctx, projectID, name)
				cancel()
				switch {
				case err == nil:
					return value, true
				case !errors.Is(err, secretstore.ErrNotFound):
					slog.Error("project secret store failed; refusing to substitute a development placeholder",
						"secret", name, "project_id", projectID, "err", err)
					return "", false
				}
			}
			if value, ok := engine.ResolveSecret(parent, envFallback, name); ok {
				return value, true
			}
			slog.Warn("substituting placeholder for unconfigured project secret",
				"secret", name, "project_id", projectID, "env", devFakeSecretsEnv)
			return "apexload-dev-secret", true
		}))
	}
	if tr == nil {
		return engine.CacheSecretResolverForRun(engine.NewProjectSecretResolver(projectID))
	}
	return engine.CacheSecretResolverForRun(secretstore.EngineResolver(tr.projectSecretStore, projectID))
}

// A run binds the application selected for that run. Script-only operations
// use the library assignment instead, allowing one shared application binding
// to serve every script in that application.
func (tr *TestRunner) secretResolverForScope(projectID, applicationID string) engine.SecretResolver {
	project := tr.secretResolverForProject(projectID)
	applicationID = strings.TrimSpace(applicationID)
	if applicationID == "" || tr == nil || tr.applicationSecretStore == nil {
		return project
	}
	if tr.applicationStore != nil {
		app, err := tr.applicationStore.GetApplication(applicationID)
		if err != nil || app == nil || normalizeProjectID(app.ProjectID) != normalizeProjectID(projectID) {
			return engine.SecretResolverFunc(func(string) (string, bool) { return "", false })
		}
	}
	scopeID := storage.ApplicationSecretScopeID(applicationID)
	return engine.CacheSecretResolverForRun(engine.ContextSecretResolverFunc(func(parent context.Context, name string) (string, bool) {
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		value, _, err := tr.applicationSecretStore.Get(ctx, scopeID, name)
		cancel()
		switch {
		case err == nil:
			if engine.IsUsableResolvedSecretValue(value) {
				return value, true
			}
			return engine.ResolveSecret(parent, project, name)
		case errors.Is(err, secretstore.ErrNotFound):
			return engine.ResolveSecret(parent, project, name)
		default:
			return "", false
		}
	}))
}

func (tr *TestRunner) secretResolverForScript(script *dsl.Script) engine.SecretResolver {
	if script == nil {
		return tr.secretResolverForProject("")
	}
	if tr != nil && tr.libraryAssignmentStore != nil && strings.TrimSpace(script.ID) != "" {
		assignments, err := tr.libraryAssignmentStore.ListLibraryAssignments("script", []string{script.ID})
		if err != nil {
			return engine.SecretResolverFunc(func(string) (string, bool) { return "", false })
		}
		if assignment := assignments[script.ID]; assignment != nil && assignment.Application != nil {
			return tr.secretResolverForScope(script.ProjectID, assignment.Application.ID)
		}
	}
	return tr.secretResolverForProject(script.ProjectID)
}

func NewTestRunner(maxTests int, hub *Hub, store storage.Store, opts ...TestRunnerOption) *TestRunner {
	tr := &TestRunner{
		tests:                        make(map[string]*ManagedTest),
		scripts:                      make(map[string]*StoredScript),
		privateCISnapshots:           make(map[string]int),
		scenarios:                    make(map[string]*StoredScenario),
		archives:                     make(map[string]*storage.RunArchiveRecord),
		deletingProjects:             make(map[string]struct{}),
		projectDeletionUnlock:        make(map[string]func()),
		secretReferenceMutations:     &projectMutationCoordinator{},
		hub:                          hub,
		maxTests:                     maxTests,
		defaultMaxTests:              maxTests,
		scriptStore:                  store,
		testStore:                    store,
		scenarioStore:                store,
		dataPoolStore:                store,
		projectStore:                 store,
		releaseStore:                 store,
		historyStore:                 store,
		storedHistoryCache:           newStoredTestHistoryCache(),
		archiveStore:                 store,
		testNotesStore:               store,
		auditStore:                   store,
		settingsStore:                store,
		usageStore:                   store,
		objectRepoStore:              store,
		reservationStore:             store,
		execPlanRunStore:             store,
		execPlanScheduleStore:        store,
		seqPlanRuns:                  make(map[string]*seqPlanRunState),
		seqPlanByTest:                make(map[string]string),
		seqPlanRetrying:              make(map[string]struct{}),
		ciRecoveryRetrying:           make(map[string]queuedCIRecoveryRetryState),
		ciRecoveryRetryWake:          make(chan struct{}, 1),
		dynatraceOAuthTokens:         make(map[string]dynatraceOAuthToken),
		scriptShakeoutFingerprintKey: newScriptShakeoutFingerprintKey(),
		terminalRetrying:             make(map[string]struct{}),
		stopTeardownRetries:          make(map[string]stopTeardownRetryIntent),
		fleetStore:                   store,
		execution:                    localExecutionBackend{},
		notifier:                     newNotificationDispatcher(nil),
		routeCache:                   make(map[string]latencyRouteCacheEntry),
	}
	if assignments, ok := store.(storage.LibraryAssignmentStore); ok {
		tr.libraryAssignmentStore = assignments
	}
	if applications, ok := store.(applicationPersistence); ok {
		tr.applicationStore = applications
	}
	if journal, ok := store.(storage.ThresholdIncidentStore); ok {
		tr.thresholdJournal = &thresholdJournalWriter{store: journal}
	}
	if elkStore, ok := any(store).(storage.ELKConnectionStore); ok {
		tr.elkStore = elkStore
	}
	if dynatraceStore, ok := any(store).(storage.DynatraceConnectionStore); ok {
		tr.dynatraceStore = dynatraceStore
	}
	if githubStore, ok := store.(storage.GitHubEnterpriseStore); ok {
		tr.githubCheckStore = githubStore
	}
	if revisionStore, ok := any(store).(startAdmissionRevisionPersistence); ok {
		tr.startAdmissionRevisionStore = revisionStore
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tr)
		}
	}
	if attemptStore, ok := any(store).(storage.RuntimeSecretProvisioningAttemptStore); ok {
		tr.secretAttemptStore = attemptStore
	}
	if !tr.durableExecutionFence || tr.holdsExecutionLeadership() {
		if err := tr.repairCapacityReservationBindings(); err != nil {
			slog.Warn("capacity timeslot binding repair failed during startup", "error", err)
		}
	}
	tr.loadSystemSettings(maxTests)
	tr.loadStoredScripts()
	tr.loadStoredScenarios()
	tr.scheduleRuntimeOverrideSecretAttemptReconciliation()
	tr.loadStoredTests()
	tr.initRefKeys()
	if tr.holdsExecutionLeadership() {
		if err := tr.cleanupLeakedCIScriptSnapshots(); err != nil {
			slog.Warn("leaked CI script snapshot cleanup failed during startup", "error", err)
		}
	}
	if err := tr.recoverQueuedCIRuns(); err != nil && !errors.Is(err, errNotExecutionLeader) {
		slog.Warn("queued CI run recovery failed during startup", "error", err)
	}
	tr.ReconcilePendingTests()
	tr.mu.Lock()
	tr.resetSchedulerReadinessLocked(time.Now().UTC())
	tr.mu.Unlock()

	return tr
}

func newScriptShakeoutFingerprintKey() [32]byte {
	var key [32]byte
	if _, err := cryptorand.Read(key[:]); err != nil {
		// Without an unpredictable process key, hashing exact executable inputs
		// could expose low-entropy embedded credentials to offline guessing.
		panic("generate script shakeout fingerprint key: " + err.Error())
	}
	return key
}

func (tr *TestRunner) setExecutionBackend(backend testExecutionBackend) {
	if backend == nil {
		backend = localExecutionBackend{}
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.execution = backend
	tr.publishRuntimeReadinessLocked(time.Now().UTC())
}

func (tr *TestRunner) setLiveStateStore(store LiveStateStore) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.liveState = store
}

func (tr *TestRunner) getExecutionBackend() testExecutionBackend {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return tr.execution
}

// errNotExecutionLeader is returned by execution-mutating runner methods when
// this replica does not hold the HA execution lease. Handlers translate it into
// an HTTP 503 so clients retry against the active leader.
var errNotExecutionLeader = errors.New("controller is not the active execution leader")

type executionLeaseTermContextKey struct{}

func executionLeaseTermFromContext(ctx context.Context) (executionLeaseTerm, bool) {
	if ctx == nil {
		return executionLeaseTerm{}, false
	}
	term, ok := ctx.Value(executionLeaseTermContextKey{}).(executionLeaseTerm)
	return term, ok && term.valid()
}

// isExecutionLeader reports whether this replica may mutate live test-execution
// state. With no leadership coordinator (single-node / tests) it is always true.
func (tr *TestRunner) isExecutionLeader() bool {
	if tr == nil {
		return true
	}
	if tr.leadership == nil {
		return true
	}
	return tr.leadership.IsAdmitted()
}

// holdsExecutionLeadership reports whether this replica owns the current lease,
// including the acquire-time recovery window before public mutation admission
// opens. Recovery code must use this predicate; request paths must continue to
// use isExecutionLeader so they remain closed until recovery succeeds.
func (tr *TestRunner) holdsExecutionLeadership() bool {
	if tr == nil || tr.leadership == nil {
		return true
	}
	return tr.leadership.IsLeader()
}

// executionOperationContext combines caller cancellation with the current
// leadership epoch and its immutable durable lease term. Single-controller
// deployments retain ordinary caller cancellation semantics.
func (tr *TestRunner) executionOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if tr == nil || tr.leadership == nil {
		return context.WithCancel(parent)
	}
	return tr.leadership.operationContext(parent)
}

func (tr *TestRunner) currentExecutionFenceToken() string {
	if tr == nil || !tr.durableExecutionFence || tr.leadership == nil {
		return ""
	}
	return tr.leadership.executionFenceToken()
}

func (tr *TestRunner) claimTestExecutionFence(test *ManagedTest) error {
	if tr == nil || test == nil || !tr.durableExecutionFence {
		return nil
	}
	if tr.leadership == nil {
		return errNotExecutionLeader
	}
	token, generation := tr.leadership.executionLeaseTerm()
	if token == "" || generation == 0 {
		return errNotExecutionLeader
	}
	// A non-empty token belongs to the operation that created or previously
	// claimed this in-memory runtime. Never let an old goroutine adopt a later
	// term after same-process leadership reacquisition. Rehydrated durable tests
	// start with an empty process-private token and may be claimed normally.
	if test.executionFence != "" && test.executionFence != token {
		return errNotExecutionLeader
	}
	test.executionFence = token
	test.executionGeneration = generation
	return nil
}

// claimTestExecutionFenceWithTerm binds a test to one immutable execution
// lease term. Unlike claimTestExecutionFence it never reads and adopts whatever
// term happens to be current after a same-process leadership reacquisition.
// Callers that use this for a start transition must hold tr.mu.
func (tr *TestRunner) claimTestExecutionFenceWithTerm(test *ManagedTest, term executionLeaseTerm) error {
	if tr == nil || test == nil || !tr.durableExecutionFence {
		return nil
	}
	if !tr.executionLeaseTermLocallyCurrent(term) {
		return errNotExecutionLeader
	}
	if test.executionFence != "" && test.executionFence != term.ownerID {
		return errNotExecutionLeader
	}
	test.executionFence = term.ownerID
	test.executionGeneration = term.generation
	return nil
}

const (
	refRunDateLayout = "20060102"
)

// Helper to convert map back to LiveMetrics (since GetHistory returns any)

type CreateTestRequest struct {
	MonitorBindings *[]storage.MonitorBinding `json:"monitor_bindings,omitempty"`
	TraceContext    *RunTraceContextSettings  `json:"trace_context,omitempty"`
	TerminationMode string                    `json:"termination_mode,omitempty"`
	Name            string                    `json:"name"`
	// RequestedTestID is used by controller-to-controller run creation to make
	// the remote side effect recoverable across a lost response. It must be a
	// UUID and is always persisted through the create-only test path.
	RequestedTestID       string                         `json:"requested_test_id,omitempty"`
	MaterializationKey    string                         `json:"-"`
	ExecutionPlanApproval *storage.ExecutionPlanApproval `json:"-"`
	Duration              string                         `json:"duration"`
	StartDelay            string                         `json:"start_delay,omitempty"`
	ReserveLaunchWindow   bool                           `json:"reserve_launch_window,omitempty"`
	ApplicationID         string                         `json:"application_id,omitempty"`
	ReleaseID             string                         `json:"release_id,omitempty"`
	ScenarioID            string                         `json:"scenario_id,omitempty"`
	// RevisionSourceTestID identifies the immutable queued-run snapshot used to
	// reconcile masked runtime credentials while creating a revised copy. It is
	// never persisted on the new run and is accepted only for a QUEUED source in
	// the same project as every other referenced resource.
	RevisionSourceTestID string     `json:"revision_source_test_id,omitempty"`
	ProjectID            string     `json:"project_id,omitempty"`
	FolderID             *string    `json:"folder_id,omitempty"`
	ScheduledStartAt     *time.Time `json:"scheduled_start_at,omitempty"`
	// TimeslotID, when set, binds the run to an approved capacity timeslot as part
	// of creation; if the timeslot can't be bound the create fails (strict).
	TimeslotID       *string                      `json:"timeslot_id,omitempty"`
	ScenarioMode     string                       `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                         `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                         `json:"resource_baseline,omitempty"`
	Assignment       *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	GoalConfig       *GoalConfig                  `json:"goal_config,omitempty"`
	TrafficProfile   *TrafficProfile              `json:"traffic_profile,omitempty"`
	// JointEvidence chooses the run's joint evidence v4 projection.
	JointEvidence *engine.JointProjection           `json:"joint_evidence,omitempty"`
	Groups        []CreateGroupEntry                `json:"groups,omitempty"`
	DataPools     []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	// Single-script shorthand (backwards compatible)
	ScriptID string `json:"script_id,omitempty"`
	VUsers   int    `json:"vusers,omitempty"`
	RampUp   string `json:"ramp_up,omitempty"`
	// Test-level runtime overrides (mirrors LR Controller override behavior)
	RuntimeSettings  *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
	ELKCapture       *ELKCaptureConfig        `json:"elk_capture,omitempty"`
	DynatraceCapture *DynatraceCaptureConfig  `json:"dynatrace_capture,omitempty"`

	scriptShakeoutProvided   bool
	resourceBaselineProvided bool
	creationActor            testCreationActor
	// allowLegacyDataPoolModes is set only by trusted materializers reading
	// already-durable snapshots (currently execution plans). New API payloads
	// must never set it and remain subject to the strict read-only contract.
	allowLegacyDataPoolModes bool
	projectQuotaEvidence     projectQuotaEvidence
	releaseContextEvidence   createTestReleaseEvidence
	deferRunRefKey           bool
}

type CreateScenarioRequest struct {
	TerminationMode  string                            `json:"termination_mode,omitempty"`
	Name             string                            `json:"name"`
	ProjectID        string                            `json:"project_id,omitempty"`
	WorkloadShapeID  *string                           `json:"workload_shape_id,omitempty"`
	Duration         string                            `json:"duration"`
	StartDelay       string                            `json:"start_delay,omitempty"`
	ScenarioMode     string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                              `json:"resource_baseline,omitempty"`
	Assignment       *GeneratorAssignmentSettings      `json:"assignment,omitempty"`
	GoalConfig       *GoalConfig                       `json:"goal_config,omitempty"`
	TrafficProfile   *TrafficProfile                   `json:"traffic_profile,omitempty"`
	RuntimeSettings  *RuntimeSettingsOverride          `json:"runtime_settings,omitempty"`
	Groups           []CreateGroupEntry                `json:"groups"`
	DataPools        []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`

	creationActor testCreationActor
}

type GoalConfig struct {
	MonitorRules             []analysis.MonitorRule `json:"monitor_rules,omitempty"`
	TargetAverageTPS         *float64               `json:"target_average_tps,omitempty"`
	P95LatencyMs             *float64               `json:"p95_latency_ms,omitempty"`
	MaxErrorPct              *float64               `json:"max_error_pct,omitempty"`
	MaxAvgServerProcessingMs *float64               `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs         *float64               `json:"max_avg_transfer_ms,omitempty"`
	MaxErrorCount            *int                   `json:"max_error_count,omitempty"`
	MinVUsers                *int                   `json:"min_vusers,omitempty"`
	MaxVUsers                *int                   `json:"max_vusers,omitempty"`
	// Web Vitals experience thresholds, evaluated against the aggregated
	// browser metrics emitted by browser VUsers and experience canaries.
	MaxLCPMs *float64 `json:"max_lcp_ms,omitempty"`
	MaxINPMs *float64 `json:"max_inp_ms,omitempty"`
	MaxCLS   *float64 `json:"max_cls,omitempty"`

	// Streaming-ingestion thresholds (EH.4), evaluated against the messaging
	// server-telemetry summary (Event Hubs / Kafka producer runs).
	MaxThrottledRequests *int     `json:"max_throttled_requests,omitempty"`    // run-total throttle/ServerBusy/quota responses
	MaxEndToEndLatencyMs *float64 `json:"max_end_to_end_latency_ms,omitempty"` // avg producer->observed-consume latency
	MaxPartitionSkewPct  *float64 `json:"max_partition_skew_pct,omitempty"`    // coefficient of variation of per-partition produce volume
	MaxRedeliveryRatePct *float64 `json:"max_redelivery_rate_pct,omitempty"`   // percent of received messages redelivered
	MaxDeadLetterRatePct *float64 `json:"max_dead_letter_rate_pct,omitempty"`  // percent of received messages dead-lettered

	StopOnSLABreach bool   `json:"stop_on_sla_breach,omitempty"`
	StopOnMaxErrors bool   `json:"stop_on_max_errors,omitempty"`
	SLABreachWindow string `json:"sla_breach_window,omitempty"`
	// TransactionResponseTimeSLAMs is the scenario-wide default response-time
	// SLA for named business transactions. It is independent from P95LatencyMs,
	// which remains a fleet-level goal-controller guardrail.
	TransactionResponseTimeSLAMs *float64 `json:"transaction_response_time_sla_ms,omitempty"`
	// EarlyStop is a scenario-specific, VUser-mode-compatible error-rate stop.
	// It deliberately lives beside the existing guardrails so legacy durable
	// goal_config_json columns can round-trip it without a database migration.
	EarlyStop *EarlyStopConfig `json:"early_stop,omitempty"`
	// WorkloadShapeConfig retains authoring intent that cannot be reconstructed
	// losslessly from derived stage rows (for example an uneven final step).
	WorkloadShapeConfig *WorkloadShapeConfig `json:"workload_shape_config,omitempty"`
	// TransactionRules are terminal-only SLA rules evaluated from the complete
	// archive. They do not participate in live stop-on-breach decisions.
	TransactionRules []TransactionSLARule `json:"transaction_rules,omitempty"`
}

type EarlyStopConfig struct {
	Enabled      bool    `json:"enabled"`
	ErrorRatePct float64 `json:"error_rate_pct"`
	BreachWindow string  `json:"breach_window"`
}

type SteppedRampDefinition struct {
	StartVUsers   int    `json:"start_vusers"`
	EndVUsers     int    `json:"end_vusers"`
	IncrementBy   string `json:"increment_by"`
	StepCount     int    `json:"step_count,omitempty"`
	VUsersPerStep int    `json:"vusers_per_step,omitempty"`
}

type SteppedRampConfig struct {
	Scope    string                           `json:"scope"`
	Scenario *SteppedRampDefinition           `json:"scenario,omitempty"`
	Groups   map[string]SteppedRampDefinition `json:"groups,omitempty"`
}

type ClosedVUserDefinition struct {
	PeakVUsers      int    `json:"peak_vusers"`
	RampUp          string `json:"ramp_up"`
	Sustain         string `json:"sustain"`
	RampDownEnabled bool   `json:"ramp_down_enabled"`
	RampDown        string `json:"ramp_down"`
}

type ClosedVUserConfig struct {
	Scope    string                           `json:"scope"`
	Scenario *ClosedVUserDefinition           `json:"scenario,omitempty"`
	Groups   map[string]ClosedVUserDefinition `json:"groups,omitempty"`
}

type GoalDrivenARTConfig struct {
	Basis          string  `json:"basis"`
	ARTMs          float64 `json:"art_ms"`
	BaselineTestID string  `json:"baseline_test_id,omitempty"`
	BaselineGroup  string  `json:"baseline_group,omitempty"`
}

type GoalDrivenGroupDefinition struct {
	TargetTPS float64              `json:"target_tps"`
	ART       *GoalDrivenARTConfig `json:"art,omitempty"`
}

type GoalDrivenConfig struct {
	Groups map[string]GoalDrivenGroupDefinition `json:"groups"`
}

type WorkloadShapeConfig struct {
	SteppedRamp *SteppedRampConfig `json:"stepped_ramp,omitempty"`
	ClosedVUser *ClosedVUserConfig `json:"closed_vuser,omitempty"`
	GoalDriven  *GoalDrivenConfig  `json:"goal_driven,omitempty"`
}

type ActiveLoadLevel struct {
	Group        string    `json:"group"`
	StageIndex   int       `json:"stage_index"`
	Name         string    `json:"name,omitempty"`
	TargetVUsers int       `json:"target_vusers"`
	Duration     string    `json:"duration"`
	StartedAt    time.Time `json:"started_at"`
}

type TransactionSLARule struct {
	Population  string  `json:"population,omitempty"`
	Protocol    string  `json:"protocol,omitempty"`
	Group       string  `json:"group,omitempty"`
	Transaction string  `json:"transaction"`
	Metric      string  `json:"metric"`
	Comparator  string  `json:"comparator"`
	Threshold   float64 `json:"threshold"`
	Severity    string  `json:"severity,omitempty"`
}

// RuntimeSettingsOverride allows overriding script-level runtime settings at test time,
// mirroring LoadRunner Controller's override capability.
type RuntimeBrowserOverride struct {
	Headless      *bool  `json:"headless,omitempty"`
	DeviceProfile string `json:"device_profile,omitempty"`
	TouchMode     *bool  `json:"touch_mode,omitempty"`
	Orientation   string `json:"orientation,omitempty"`
}

// RuntimeJavaVuserOverride carries the script-owned Java Env settings that a
// scenario may override per group. It is deliberately a closed struct: the
// operator-owned runner fields (enablement, runner command, harness jar, work
// directory, capacity) have no counterpart here and cannot be smuggled in.
// Overridden values re-enter dsl.ValidateScriptIssues (VM-parameter denylist,
// classpath validation, JDK selection checks) before test creation succeeds.
type RuntimeJavaVuserOverride struct {
	JDKSelection            string   `json:"jdk_selection,omitempty"`
	JDKHome                 string   `json:"jdk_home,omitempty"`
	AdditionalVMParameters  []string `json:"additional_vm_parameters,omitempty"`
	Classpath               []string `json:"classpath,omitempty"`
	UseXBootClasspath       *bool    `json:"use_xbootclasspath,omitempty"`
	DedicatedClassLoader    *bool    `json:"dedicated_class_loader,omitempty"`
	SkipParameterEvaluation *bool    `json:"skip_parameter_evaluation,omitempty"`
}

type RuntimeSettingsOverride struct {
	Results       *dsl.ResultsConfig        `json:"results,omitempty"`
	WebSocket     *dsl.WebSocketConfig      `json:"websocket,omitempty"`
	Iterations    *int                      `json:"iterations,omitempty"`
	Pacing        *dsl.PacingConfig         `json:"pacing,omitempty"`
	ThinkTime     *dsl.ThinkTimeConfig      `json:"think_time,omitempty"`
	ErrorHandling *dsl.ErrorHandlingConfig  `json:"error_handling,omitempty"`
	Log           *dsl.LogConfig            `json:"log,omitempty"`
	RunLogic      *dsl.RunLogicConfig       `json:"run_logic,omitempty"`
	HTTP          *dsl.HTTPConfig           `json:"http,omitempty"`
	WebRuntime    *dsl.WebRuntimeConfig     `json:"web_runtime,omitempty"`
	JavaVuser     *RuntimeJavaVuserOverride `json:"java_vuser,omitempty"`
	// BrowserConfig is the full browser section (whole-section replace); it wins over
	// the legacy 4-field Browser partial-merge below when both are present.
	BrowserConfig *dsl.BrowserConfig      `json:"browser_config,omitempty"`
	Browser       *RuntimeBrowserOverride `json:"browser,omitempty"`
	// BrowserEmulation is the HTTP-transport browser-emulation section
	// (whole-section replace). It is distinct from BrowserConfig, which is the
	// Playwright browser protocol's device/runtime section.
	BrowserEmulation *dsl.BrowserEmulationConfig `json:"browser_emulation,omitempty"`
	// LogLevel/Iterations/AutoTransactions are legacy single-field aliases for the
	// Log/RunLogic sections above; the section wins when both are present.
	LogLevel         string                 `json:"log_level,omitempty"`
	AutoTransactions string                 `json:"auto_transactions,omitempty"`
	BaseURL          string                 `json:"base_url,omitempty"`
	Env              map[string]string      `json:"env,omitempty"`
	RedactionRules   []engine.RedactionRule `json:"redaction_rules,omitempty"`
}

type CreateGroupEntry struct {
	Name     string `json:"name"`
	ScriptID string `json:"script_id"`
	// RevisionSourceGroupIndex is an immutable request-only identity for a group
	// in RevisionSourceTestID. Display names are editable and therefore cannot
	// safely reconcile masked credentials when one script appears more than once.
	RevisionSourceGroupIndex *int `json:"revision_source_group_index,omitempty"`
	// RevisionNewGroup disambiguates an intentionally added/replaced group from
	// a source group whose immutable index was accidentally omitted.
	RevisionNewGroup             bool                         `json:"revision_new_group,omitempty"`
	VUsers                       int                          `json:"vusers"`
	RampUp                       string                       `json:"ramp_up,omitempty"`
	RampDown                     string                       `json:"ramp_down,omitempty"`
	StartOffset                  string                       `json:"start_offset,omitempty"`
	Stages                       []storage.LoadStage          `json:"stages,omitempty"`
	Iterations                   *int                         `json:"iterations,omitempty"`
	RuntimeSettings              *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	Assignment                   *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	TransactionResponseTimeSLAMs *float64                     `json:"transaction_response_time_sla_ms,omitempty"`
	InitStrategy                 string                       `json:"initialization,omitempty"` // "at_runtime", "at_once"
	// Role distinguishes load-bearing groups ("load", default) from experience
	// canaries ("observer", alias "canary"): small browser groups that emit
	// Web Vitals continuously, are pinned during goal-based scaling, and stay
	// within the diagnostic canary density envelope. Use the group Assignment
	// to place observers on dedicated capacity or a specific load zone.
	Role string `json:"role,omitempty"`
	// runtimeSecretScope is a process-only namespace (normally the preassigned
	// test UUID) used to derive non-colliding governed runtime secret names.
	// preserveJMeterDraft is set only while saving a scenario, never by JSON
	// requests or run compilation. It retains blocked messaging library drafts.
	preserveJMeterDraft bool
	runtimeSecretScope  string
	iterationCompletion bool
}

type StartTestOptions struct {
	PreparationID                string `json:"preparation_id,omitempty"`
	ExpectedPreparationRevision  string `json:"expected_preparation_revision,omitempty"`
	prepareOnly                  bool
	shakeoutAdmissionReservation *shakeoutAdmissionReservation
	OverrideCapacityShortfall    bool `json:"override_capacity_shortfall,omitempty"`
	OverrideScriptShakeoutError  bool `json:"override_script_shakeout_failure,omitempty"`
	SkipLaunchWindowPreflight    bool `json:"-"`
	// deferPostShakeoutRecoveryEnqueue lets recurring schedules durably publish
	// their occurrence and parent-schedule projections before the shared
	// recovery coordinator is allowed to launch the prepared run.
	deferPostShakeoutRecoveryEnqueue bool
	// expectedRecoveryIdentity confines an asynchronous recovery attempt to the
	// durable test incarnation that originally entered its coordinator.
	expectedRecoveryIdentity *queuedStartRecoveryIdentity
	actor                    testCreationActor
	// traceContext carries only a detached SpanContext from an inbound request.
	// It never carries request cancellation, credentials, or arbitrary values.
	traceContext context.Context
}

type shakeoutAdmissionReservation struct {
	ctx context.Context
}

// QueuedStartAuthorization is the durable provenance for explicit start-gate
// overrides that must survive a fair-share queue delay and HA failover. Capacity
// approval is bounded to the workload/shortfall the operator actually reviewed;
// a worse advisory requires a new explicit override instead of inheriting a
// stale boolean forever.
type QueuedStartAuthorization struct {
	CapacityShortfall  *CapacityShortfallStartAuthorization `json:"capacity_shortfall,omitempty"`
	ScriptShakeout     *ScriptShakeoutStartAuthorization    `json:"script_shakeout,omitempty"`
	ScriptShakeoutPass *ScriptShakeoutPassEvidence          `json:"script_shakeout_pass,omitempty"`
}

type CapacityShortfallStartAuthorization struct {
	ApprovedAt            time.Time `json:"approved_at"`
	ApprovedBySubject     string    `json:"approved_by_subject,omitempty"`
	ApprovedByDisplayName string    `json:"approved_by_display_name,omitempty"`
	TotalVUsers           int       `json:"total_vusers"`
	RecommendedGenerators int       `json:"recommended_generators"`
	AvailableGenerators   int       `json:"available_generators"`
	ShortfallGenerators   int       `json:"shortfall_generators"`
}

type ScriptShakeoutStartAuthorization struct {
	ApprovedAt            time.Time `json:"approved_at"`
	ApprovedBySubject     string    `json:"approved_by_subject,omitempty"`
	ApprovedByDisplayName string    `json:"approved_by_display_name,omitempty"`
	ReviewEvidenceSHA256  string    `json:"review_evidence_sha256,omitempty"`
}

// ScriptShakeoutPassEvidence is controller-authored proof that the immutable
// runtime snapshot already completed its one-shot shakeout. It is intentionally
// distinct from ScriptShakeoutStartAuthorization, which records a human
// decision to override a failed shakeout.
type ScriptShakeoutPassEvidence struct {
	PassedAt             time.Time `json:"passed_at"`
	EvidenceSHA256       string    `json:"evidence_sha256"`
	ReusableAfterRestart bool      `json:"reusable_after_restart,omitempty"`
}

type CapacityShortfallError struct {
	TestID   string
	Advisory *CapacityAdvisory
	Message  string
}

// BrowserDensityGateError is returned by the start chokepoints when a test's
// browser-VUser density exceeds the diagnostic-canary envelope and the
// benchmark-evidence + override release gate has not been satisfied. It mirrors
// the blocking conflict the preflight endpoint reports so a direct call to
// POST /tests/{id}/start cannot bypass the governance gate.
type BrowserDensityGateError struct {
	TestID   string
	Advisory *CapacityAdvisory
	Message  string
}

func (e *BrowserDensityGateError) Error() string {
	if e == nil {
		return "browser density release gate"
	}
	if e.Message != "" {
		return e.Message
	}
	return "browser VUser density exceeds the diagnostic canary envelope"
}

type ScriptShakeoutGateError struct {
	TestID string
	Report *ScriptShakeoutReport
}

func (e *ScriptShakeoutGateError) Error() string {
	if e == nil || e.Report == nil || e.Report.Summary == "" {
		return "script shakeout failed; full load test has not started"
	}
	return e.Report.Summary
}

type ScriptShakeoutReport struct {
	Passed    bool                   `json:"passed"`
	Summary   string                 `json:"summary"`
	StartedAt time.Time              `json:"started_at"`
	EndedAt   time.Time              `json:"ended_at"`
	Results   []ScriptShakeoutResult `json:"results"`
}

// pendingScriptShakeoutOutcome retains a completed one-shot shakeout result
// while its durable state transition is retryable. It is deliberately
// process-private: a controller that cannot prove the outcome after a crash
// must fail closed rather than replay externally visible shakeout actions.
type pendingScriptShakeoutOutcome struct {
	Report                ScriptShakeoutReport
	ExecutableFingerprint scriptShakeoutInputFingerprint
	ExecutionTerm         executionLeaseTerm
	PassStatus            string
	PendingSince          *time.Time
	PendingReason         string
	// VerificationPending means the externally visible one-shot actions have
	// completed successfully, but the controller could not re-read the exact
	// executable-input fingerprint after those actions. The outcome remains
	// process-private in SHAKEOUT until that comparison can be completed; the
	// actions must never be replayed to obtain the missing evidence.
	VerificationPending bool
}

type ScriptShakeoutResult struct {
	GroupName  string `json:"group_name"`
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

type DraftCapacityEstimate struct {
	TerminationMode       string                       `json:"termination_mode,omitempty"`
	PodPlacements         []PodCapacityPreview         `json:"pod_placements,omitempty"`
	PodCapacityWarnings   []string                     `json:"pod_capacity_warnings,omitempty"`
	ScenarioMode          string                       `json:"scenario_mode,omitempty"`
	TotalVUsers           int                          `json:"total_vusers"`
	Duration              string                       `json:"duration"`
	ResourceBaseline      bool                         `json:"resource_baseline,omitempty"`
	Groups                []TestGroup                  `json:"groups"`
	Assignment            *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	Footprint             *ScenarioFootprint           `json:"footprint,omitempty"`
	Recommendation        *AssignmentRecommendation    `json:"assignment_recommendation,omitempty"`
	CapacityAdvisory      *CapacityAdvisory            `json:"capacity_advisory,omitempty"`
	RuntimeSettingsReview *RuntimeSettingsReview       `json:"runtime_settings_review,omitempty"`
}

func pluralSuffix(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
}

func (e *CapacityShortfallError) Error() string {
	if e == nil {
		return "capacity shortfall"
	}
	if e.Message != "" {
		return e.Message
	}
	return "insufficient load generator capacity"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func normalizeProjectID(projectID string) string {
	if projectID == "" {
		return storage.DefaultProjectID
	}
	return projectID
}

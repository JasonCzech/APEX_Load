package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
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

type usageSnapshotPersistence interface {
	SaveUsageAccountingSnapshot(snapshot *storage.UsageAccountingSnapshot) error
}

// reservationPersistence is the slice of storage.Store used to bind a capacity
// timeslot to a test at creation and to make admission control reservation-aware.
type reservationPersistence interface {
	GetCapacityReservation(id string) (*storage.CapacityReservation, error)
	UpdateCapacityReservationBinding(id string, testID *string, testStatus *string, updatedAt time.Time) (bool, error)
	ClearCapacityReservationBinding(id string, testID string, updatedAt time.Time) (bool, error)
	ListCapacityReservationsByTimeRange(projectID string, startAt, endAt time.Time) ([]*storage.CapacityReservation, error)
}

type TestRunner struct {
	tests                 map[string]*ManagedTest
	scripts               map[string]*StoredScript
	scenarios             map[string]*StoredScenario
	archives              map[string]*storage.RunArchiveRecord
	deletingProjects      map[string]struct{}
	shuttingDown          bool
	mu                    sync.RWMutex
	activeStopMu          sync.Mutex
	remoteControlMu       sync.Mutex
	remoteControlGates    map[string]*distributedControlGate
	hub                   *Hub
	maxTests              int
	settings              SystemSettings
	settingsRefreshMu     sync.Mutex
	settingsAuthRefreshAt time.Time
	systemSettingsRaw     string
	scriptStore           scriptPersistence
	testStore             testPersistence
	scenarioStore         scenarioPersistence
	dataPoolStore         dataPoolPersistence
	projectStore          projectPersistence
	releaseStore          releasePersistence
	historyStore          historyPersistence
	archiveStore          archivePersistence
	testNotesStore        testNotesPersistence
	auditStore            auditPersistence
	settingsStore         settingsPersistence
	usageStore            usageSnapshotPersistence
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
	terminalRetryMu  sync.Mutex
	terminalRetrying map[string]struct{}
	// archiveCommitGates serialize archive publication with deletion per test.
	// Entries are reference-counted and removed after their last waiter exits.
	archiveCommitMu    sync.Mutex
	archiveCommitGates map[string]*archiveCommitGate
	// execPlanScheduleStore persists recurring execution-plan schedules and their
	// occurrences; the leader-gated sweep materializes due plan schedules.
	execPlanScheduleStore executionPlanSchedulePersistence
	// fleetStore resolves fleet-pool membership for pool-aware generator assignment.
	fleetStore fleetMembershipReader
	// leadership gates live test-execution mutations to a single replica when
	// HA leader election is enabled. nil means "always leader" (single-node /
	// test default), so behavior is unchanged unless election is wired in.
	leadership    *executionLeadership
	liveState     LiveStateStore
	execution     testExecutionBackend
	notifier      *notificationDispatcher
	notifications atomic.Value
	routeCache    map[string]latencyRouteCacheEntry

	// Reference-key state (all guarded by mu). envCode is the short,
	// environment-aware prefix resolved once at startup; the seq counters are
	// monotonic per (env, artifact type) and backed by persisted settings so
	// they survive restarts. refKeyIndex maps a reference key to its UUID.
	envCode        string
	refScriptSeq   int
	refScenarioSeq int
	refRunSeqByDay map[string]int
	refKeyIndex    map[string]string

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

	liveInventoryMu sync.RWMutex
	liveInventory   []GeneratorInventoryEntry
	liveInventoryAt time.Time
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
	ID                    string                                   `json:"id"`
	RefKey                string                                   `json:"ref_key,omitempty"`
	Name                  string                                   `json:"name"`
	Status                string                                   `json:"status"`
	ApplicationID         string                                   `json:"application_id,omitempty"`
	ReleaseID             string                                   `json:"release_id,omitempty"`
	ScenarioID            string                                   `json:"scenario_id,omitempty"`
	ProjectID             string                                   `json:"project_id,omitempty"`
	FolderID              *string                                  `json:"folder_id,omitempty"`
	Groups                []TestGroup                              `json:"groups"`
	CapacityAdvisory      *CapacityAdvisory                        `json:"capacity_advisory,omitempty"`
	RuntimeSettingsReview *RuntimeSettingsReview                   `json:"runtime_settings_review,omitempty"`
	Assignment            *GeneratorAssignmentSettings             `json:"assignment,omitempty"`
	ScenarioMode          string                                   `json:"scenario_mode,omitempty"`
	ScriptShakeout        bool                                     `json:"script_shakeout,omitempty"`
	ResourceBaseline      bool                                     `json:"resource_baseline,omitempty"`
	StartDelay            string                                   `json:"start_delay,omitempty"`
	GoalConfig            *GoalConfig                              `json:"goal_config,omitempty"`
	GoalTargets           map[string]GoalTargetStatus              `json:"goal_targets,omitempty"`
	RuntimeSettings       *RuntimeSettingsOverride                 `json:"runtime_settings,omitempty"`
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
	EndedAt               *time.Time                               `json:"ended_at,omitempty"`
	CreatedBySubject      string                                   `json:"created_by_subject,omitempty"`
	CreatedByDisplayName  string                                   `json:"created_by_display_name,omitempty"`
	StartedBySubject      string                                   `json:"started_by_subject,omitempty"`
	StartedByDisplayName  string                                   `json:"started_by_display_name,omitempty"`
	StoppedBySubject      string                                   `json:"stopped_by_subject,omitempty"`
	StoppedByDisplayName  string                                   `json:"stopped_by_display_name,omitempty"`
	ExecutionPlanApproval *RunArchiveExecutionPlanApprovalSnapshot `json:"execution_plan_approval,omitempty"`
	VUserRamps            map[string]VUserRampStatus               `json:"vuser_ramps,omitempty"`
	LiveMetrics           *LiveMetrics                             `json:"live_metrics,omitempty"`
	History               []*LiveMetrics                           `json:"history,omitempty"`
	Sparkline             []TestSparklinePoint                     `json:"sparkline,omitempty"`
	ComparisonBaseline    *RunComparisonBaseline                   `json:"comparison_baseline,omitempty"`
	Archive               *storage.RunArchiveRecord                `json:"archive,omitempty"`
	TelemetryHealth       *TelemetryRunHealth                      `json:"telemetry_health,omitempty"`
	Error                 string                                   `json:"error,omitempty"`

	// deprecated single-script fields kept for backwards compat in JSON
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`

	multiEngine                  *engine.MultiGroupEngine
	executionCtx                 context.Context
	cancel                       context.CancelFunc
	groupSpecs                   []engine.GroupConfig
	goalControl                  *goalControllerState
	executionBackend             testExecutionBackend
	lastNetworkLatency           *NetworkLatency
	latencyEndpointsCache        latencyEndpointCache
	latencyProbeSamples          map[string][]latencyProbeSample
	remoteTarget                 string
	remoteClient                 generatorClient
	remoteMetricsStream          *generatorrpc.MetricsStream
	remoteGroupTargets           map[string]string
	remoteGroupShards            map[string][]GeneratorShardSummary
	remoteClientsByTarget        map[string]generatorClient
	remoteMetricsStreamsByTarget map[string]*generatorrpc.MetricsStream
	remoteMetricsBatchesByTarget map[string]generatorrpc.MetricsBatch
	remoteTargetStates           map[string]string
	remoteFinalizing             bool
	remoteMetricsLastPublishedAt time.Time
	remoteMetricsFlushScheduled  bool
	remoteGoalUpdateRunning      bool
	remoteControlGeneration      uint64
	telemetryDiagnostic          telemetryDiagnosticState
	errorPatterns                map[string]*LiveErrorPattern
	liveErrorEvidence            map[string][]LiveErrorEvidence
	vuserScaleRamps              map[string]*vuserScaleRamp
	openWorkloadSheddingWarned   bool
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
	Name            string                       `json:"name"`
	ScriptID        string                       `json:"script_id"`
	ScriptName      string                       `json:"script_name"`
	Protocol        string                       `json:"protocol"`
	Role            string                       `json:"role,omitempty"`
	VUsers          int                          `json:"vusers"`
	RampUp          string                       `json:"ramp_up,omitempty"`
	RampDown        string                       `json:"ramp_down,omitempty"`
	Stages          []storage.LoadStage          `json:"stages,omitempty"`
	Iterations      *int                         `json:"iterations,omitempty"`
	RuntimeSettings *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	Assignment      *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	RunLogicGoals   *RunLogicGoalSnapshot        `json:"run_logic_goals,omitempty"`
	Initialization  string                       `json:"initialization,omitempty"`
	rampUpDur       time.Duration
	stageDriven     bool
	initialVUsers   int
	currentVUsers   int
	script          *dsl.Script
	redactionRules  []engine.RedactionRule
}

type GeneratorShardSummary struct {
	Group         string `json:"group"`
	Target        string `json:"target"`
	VUsers        int    `json:"vusers"`
	ShardStrategy string `json:"shard_strategy,omitempty"`
}

type RunLogicGoalSnapshot struct {
	GlobalIterations int   `json:"global_iterations,omitempty"`
	MaxTransactions  int64 `json:"max_transactions,omitempty"`
}

type LiveMetrics struct {
	Timestamp              time.Time                             `json:"timestamp"`
	ActiveVUsers           int64                                 `json:"active_vusers"`
	TPS                    float64                               `json:"tps"`
	TargetTPS              float64                               `json:"target_tps,omitempty"`
	ThroughputGapPct       float64                               `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool                                  `json:"throughput_target_missed,omitempty"`
	AvgResponseMs          float64                               `json:"avg_response_ms"`
	ErrorPct               float64                               `json:"error_pct"`
	TotalTx                int64                                 `json:"total_transactions"`
	TotalErrors            int64                                 `json:"total_errors"`
	P50Ms                  float64                               `json:"p50_ms"`
	P90Ms                  float64                               `json:"p90_ms"`
	P95Ms                  float64                               `json:"p95_ms"`
	P99Ms                  float64                               `json:"p99_ms"`
	AvgDNSMs               float64                               `json:"avg_dns_ms,omitempty"`
	AvgConnectMs           float64                               `json:"avg_connect_ms,omitempty"`
	AvgTLSMs               float64                               `json:"avg_tls_ms,omitempty"`
	AvgTTFBMs              float64                               `json:"avg_ttfb_ms,omitempty"`
	AvgWaitMs              float64                               `json:"avg_wait_ms,omitempty"`
	ErrorBreakdown         map[string]int64                      `json:"error_breakdown,omitempty"`
	Transactions           map[string]*TxMetrics                 `json:"transactions,omitempty"`
	DependencyHealth       map[string]*TxMetrics                 `json:"dependency_health,omitempty"`
	DependencyMap          *LiveDependencyMap                    `json:"dependency_map,omitempty"`
	ErrorPatterns          []LiveErrorPattern                    `json:"error_patterns,omitempty"`
	SOAPOperations         map[string]*TxMetrics                 `json:"soap_operations,omitempty"`
	NetworkProfiles        map[string]*TxMetrics                 `json:"network_profiles,omitempty"`
	GroupMetrics           map[string]*GroupLiveMetrics          `json:"groups,omitempty"`
	Browser                *BrowserLiveMetrics                   `json:"browser,omitempty"`
	BrowserByDevice        map[string]*BrowserDeviceLiveMetrics  `json:"browser_by_device,omitempty"`
	GoalStatus             *GoalStatus                           `json:"goal_status,omitempty"`
	CapacityStatus         *LiveCapacityStatus                   `json:"capacity_status,omitempty"`
	TelemetryStatus        *TelemetryRunHealth                   `json:"telemetry_status,omitempty"`
	Telemetry              *TelemetryHealth                      `json:"telemetry,omitempty"`
	Resources              *engine.SystemResources               `json:"resources,omitempty"`
	GeneratorResources     map[string]*GeneratorResourceSnapshot `json:"generator_resources,omitempty"`
	Latency                *NetworkLatency                       `json:"latency,omitempty"`
	ErrorEvidence          []LiveErrorEvidence                   `json:"-"`
}

type GeneratorResourceSnapshot struct {
	GeneratorID     string                  `json:"generator_id"`
	Target          string                  `json:"target"`
	Region          string                  `json:"region,omitempty"`
	ActiveVUsers    int32                   `json:"active_vusers"`
	TPS             float64                 `json:"tps,omitempty"`
	ErrorPct        float64                 `json:"error_pct,omitempty"`
	P95Ms           float64                 `json:"p95_ms,omitempty"`
	P99Ms           float64                 `json:"p99_ms,omitempty"`
	SaturationState string                  `json:"saturation_state,omitempty"`
	ImpactReasons   []string                `json:"impact_reasons,omitempty"`
	Resources       *engine.SystemResources `json:"resources,omitempty"`
	Timestamp       time.Time               `json:"timestamp"`
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
	Samples                   int64            `json:"samples"`
	AvgServerProcessingMs     float64          `json:"avg_server_processing_ms,omitempty"`
	AvgTransferMs             float64          `json:"avg_transfer_ms,omitempty"`
	AvgRequestPayloadKB       float64          `json:"avg_request_payload_kb,omitempty"`
	AvgResponsePayloadKB      float64          `json:"avg_response_payload_kb,omitempty"`
	RequestPayloadHistogram   map[string]int64 `json:"request_payload_histogram,omitempty"`
	ResponsePayloadHistogram  map[string]int64 `json:"response_payload_histogram,omitempty"`
	AvgFirstEventMs           float64          `json:"avg_first_event_ms,omitempty"`
	MaxFirstEventMs           float64          `json:"max_first_event_ms,omitempty"`
	EventCount                int64            `json:"event_count,omitempty"`
	AvgMessageLatencyMs       float64          `json:"avg_message_latency_ms,omitempty"`
	MaxMessageLatencyMs       float64          `json:"max_message_latency_ms,omitempty"`
	MessagesSent              int64            `json:"messages_sent,omitempty"`
	MessagesReceived          int64            `json:"messages_received,omitempty"`
	AvgMessagesSentPerSec     float64          `json:"avg_messages_sent_per_sec,omitempty"`
	AvgMessagesReceivedPerSec float64          `json:"avg_messages_received_per_sec,omitempty"`
	AvgConsumerLagMs          float64          `json:"avg_consumer_lag_ms,omitempty"`
	MaxConsumerLagMs          float64          `json:"max_consumer_lag_ms,omitempty"`
	AvgConsumerOffsetLag      float64          `json:"avg_consumer_offset_lag,omitempty"`
	MaxConsumerOffsetLag      int64            `json:"max_consumer_offset_lag,omitempty"`
	SendFailureCount          int64            `json:"send_failure_count,omitempty"`
	RedeliveryCount           int64            `json:"redelivery_count,omitempty"`
	DeadLetteredCount         int64            `json:"dead_lettered_count,omitempty"`
	DuplicateCount            int64            `json:"duplicate_count,omitempty"`
	OutOfOrderCount           int64            `json:"out_of_order_count,omitempty"`
	CommitCount               int64            `json:"commit_count,omitempty"`
	AbortCount                int64            `json:"abort_count,omitempty"`
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
	DroppedResults           int64     `json:"dropped_results,omitempty"`
	SheddedArrivals          int64     `json:"shedded_arrivals,omitempty"`
	DroppedSubscriberBatches int64     `json:"dropped_subscriber_batches,omitempty"`
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
}

type TelemetryRunHealth struct {
	Status            string     `json:"status"`
	Severity          string     `json:"severity"`
	Summary           string     `json:"summary,omitempty"`
	CurrentSignature  string     `json:"current_signature,omitempty"`
	Conditions        []string   `json:"conditions,omitempty"`
	BreachActive      bool       `json:"breach_active,omitempty"`
	SustainedBreach   bool       `json:"sustained_breach,omitempty"`
	BreachDurationSec int        `json:"breach_duration_sec,omitempty"`
	BreachStartedAt   *time.Time `json:"breach_started_at,omitempty"`
	LastObservedAt    time.Time  `json:"last_observed_at"`
	LastTransitionAt  time.Time  `json:"last_transition_at,omitempty"`
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
	Severity   string             `json:"severity"`
	Summary    string             `json:"summary"`
	Conditions []string           `json:"conditions,omitempty"`
	Samples    []DiagnosticSample `json:"samples,omitempty"`
	Telemetry  *TelemetryHealth   `json:"telemetry,omitempty"`
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
	Name             string                  `json:"name"`
	DependencyType   string                  `json:"dependency_type,omitempty"`
	DependencyTarget string                  `json:"dependency_target,omitempty"`
	LastErrorMessage string                  `json:"last_error_message,omitempty"`
	LastStatusCode   int                     `json:"last_status_code,omitempty"`
	LastErrorAt      time.Time               `json:"last_error_at,omitempty"`
	ServerTelemetry  *ServerTelemetrySummary `json:"server_telemetry,omitempty"`
	AvgDNSMs         float64                 `json:"avg_dns_ms,omitempty"`
	AvgConnectMs     float64                 `json:"avg_connect_ms,omitempty"`
	AvgTLSMs         float64                 `json:"avg_tls_ms,omitempty"`
	AvgTTFBMs        float64                 `json:"avg_ttfb_ms,omitempty"`
	CorrelationIDs   []string                `json:"correlation_ids,omitempty"`
	CorrelationCount int64                   `json:"correlation_count,omitempty"`
	Count            int64                   `json:"count"`
	Errors           int64                   `json:"errors"`
	AvgMs            float64                 `json:"avg_ms"`
	P50Ms            float64                 `json:"p50_ms"`
	P90Ms            float64                 `json:"p90_ms"`
	P95Ms            float64                 `json:"p95_ms,omitempty"`
	P99Ms            float64                 `json:"p99_ms"`
}

type LiveErrorPattern struct {
	Signature        string    `json:"signature"`
	Sample           string    `json:"sample"`
	Count            int64     `json:"count"`
	FirstSeen        time.Time `json:"first_seen"`
	LatestSeen       time.Time `json:"latest_seen"`
	Actions          []string  `json:"actions,omitempty"`
	AffectedGroups   []string  `json:"affected_groups,omitempty"`
	StatusCode       int       `json:"status_code,omitempty"`
	ErrorPct         float64   `json:"error_pct,omitempty"`
	SnapshotURL      string    `json:"snapshot_url,omitempty"`
	EvidenceURL      string    `json:"evidence_url,omitempty"`
	EvidenceCount    int       `json:"evidence_count,omitempty"`
	LatestEvidenceID string    `json:"latest_evidence_id,omitempty"`
}

type LiveErrorEvidence struct {
	ID           string                  `json:"id"`
	Signature    string                  `json:"signature"`
	Action       string                  `json:"action"`
	Group        string                  `json:"group,omitempty"`
	Timestamp    time.Time               `json:"timestamp"`
	DurationMs   float64                 `json:"duration_ms"`
	Status       string                  `json:"status"`
	StatusCode   int                     `json:"status_code,omitempty"`
	ErrorMessage string                  `json:"error_message,omitempty"`
	Snapshot     *protocol.ErrorSnapshot `json:"snapshot,omitempty"`
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
	Name                   string                  `json:"name"`
	VUsers                 int                     `json:"vusers"`
	Transactions           int64                   `json:"transactions"`
	Errors                 int64                   `json:"errors"`
	TPS                    float64                 `json:"tps"`
	TargetTPS              float64                 `json:"target_tps,omitempty"`
	ThroughputGapPct       float64                 `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool                    `json:"throughput_target_missed,omitempty"`
	AvgResponseMs          float64                 `json:"avg_response_ms"`
	P50Ms                  float64                 `json:"p50_ms"`
	P90Ms                  float64                 `json:"p90_ms"`
	P95Ms                  float64                 `json:"p95_ms,omitempty"`
	P99Ms                  float64                 `json:"p99_ms"`
	DurationDigest         *engine.TDigestSnapshot `json:"-"`
}

type StoredScript struct {
	ID                  string           `json:"id"`
	RefKey              string           `json:"ref_key,omitempty"`
	Name                string           `json:"name"`
	Protocol            string           `json:"protocol"`
	Version             string           `json:"version"`
	ProjectID           string           `json:"project_id,omitempty"`
	FolderID            *string          `json:"folder_id,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	ScenarioCount       int              `json:"scenario_count,omitempty"`
	AssociatedScenarios []ScenarioRef    `json:"associated_scenarios,omitempty"`
	AssociatedReleases  []ReleaseRef     `json:"associated_releases,omitempty"`
	ScenarioRunReleases []ReleaseRef     `json:"scenario_run_releases,omitempty"`
	LastRunAt           *time.Time       `json:"last_run_at,omitempty"`
	AuthoringHealth     *AuthoringHealth `json:"authoring_health,omitempty"`
	Script              *dsl.Script      `json:"script"`
}

type StoredScenario struct {
	ID                 string                            `json:"id"`
	RefKey             string                            `json:"ref_key,omitempty"`
	Name               string                            `json:"name"`
	ProjectID          string                            `json:"project_id,omitempty"`
	Duration           string                            `json:"duration"`
	StartDelay         string                            `json:"start_delay,omitempty"`
	ScenarioMode       string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout     bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline   bool                              `json:"resource_baseline,omitempty"`
	Assignment         *GeneratorAssignmentSettings      `json:"assignment,omitempty"`
	GoalConfig         *GoalConfig                       `json:"goal_config,omitempty"`
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
}

type ScenarioScriptRef struct {
	Order           int                          `json:"order"`
	Name            string                       `json:"name,omitempty"`
	ScriptID        string                       `json:"script_id"`
	ScriptName      string                       `json:"script_name,omitempty"`
	Protocol        string                       `json:"protocol,omitempty"`
	Role            string                       `json:"role,omitempty"`
	VUsers          int                          `json:"vusers"`
	RampUp          string                       `json:"ramp_up,omitempty"`
	RampDown        string                       `json:"ramp_down,omitempty"`
	Stages          []storage.LoadStage          `json:"stages,omitempty"`
	Iterations      *int                         `json:"iterations,omitempty"`
	Initialization  string                       `json:"initialization,omitempty"`
	RuntimeSettings *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	Assignment      *GeneratorAssignmentSettings `json:"assignment,omitempty"`
}

const (
	generatorAssignmentModeAuto             = "auto"
	generatorAssignmentModeSpecificHosts    = "specific_hosts"
	generatorAssignmentModeRegionWeighted   = "region_weighted"
	generatorAssignmentModeLoadZoneWeighted = "load_zone_weighted"
	generatorShardStrategyCapacityWeighted  = "capacity_weighted"
	maxManagedTestHistory                   = 3600
	telemetryDiagnosticSampleInterval       = 5 * time.Second
	telemetrySustainedBreachWindow          = 15 * time.Second
	maxTelemetryDiagnosticSamples           = 3
	maxLiveErrorEvidencePerRun              = 256
	maxLiveErrorEvidencePerSignature        = 50
	defaultLiveErrorEvidenceLimit           = 20
	maxLiveErrorEvidenceLimit               = 100
	maxLiveErrorPatternsPerRun              = 256
	slowTransactionDiagnosticThresholdMs    = 1000.0
	jmsBrokerPressureMinLagMs               = 200.0
	jmsBrokerPressureLagMultiplier          = 2.0
	jmsBrokerPressureSendRecvRatio          = 1.5
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
	Mode            string            `json:"mode,omitempty"`
	SelectedTargets []string          `json:"selected_targets,omitempty"`
	RegionWeights   map[string]int    `json:"region_weights,omitempty"`
	LoadZoneWeights map[string]int    `json:"load_zone_weights,omitempty"`
	ShardStrategy   string            `json:"shard_strategy,omitempty"`
	RequiredLabels  map[string]string `json:"required_labels,omitempty"`
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
	Finalize(status string, completedAt time.Time, errStr string, slaBreached bool, slaDetails []string) (*storage.RunArchiveRecord, error)
}

type runArtifacts struct {
	runner          *TestRunner
	test            *ManagedTest
	recorder        archiveRecorder
	saveRecord      func(record *storage.RunArchiveRecord) error
	externalMetrics *externalMetricCollector
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

func NewTestRunner(maxTests int, hub *Hub, store storage.Store, opts ...TestRunnerOption) *TestRunner {
	tr := &TestRunner{
		tests:                 make(map[string]*ManagedTest),
		scripts:               make(map[string]*StoredScript),
		scenarios:             make(map[string]*StoredScenario),
		archives:              make(map[string]*storage.RunArchiveRecord),
		deletingProjects:      make(map[string]struct{}),
		hub:                   hub,
		maxTests:              maxTests,
		scriptStore:           store,
		testStore:             store,
		scenarioStore:         store,
		dataPoolStore:         store,
		projectStore:          store,
		releaseStore:          store,
		historyStore:          store,
		archiveStore:          store,
		testNotesStore:        store,
		auditStore:            store,
		settingsStore:         store,
		usageStore:            store,
		objectRepoStore:       store,
		reservationStore:      store,
		execPlanRunStore:      store,
		execPlanScheduleStore: store,
		seqPlanRuns:           make(map[string]*seqPlanRunState),
		seqPlanByTest:         make(map[string]string),
		seqPlanRetrying:       make(map[string]struct{}),
		terminalRetrying:      make(map[string]struct{}),
		fleetStore:            store,
		execution:             localExecutionBackend{},
		notifier:              newNotificationDispatcher(nil),
		routeCache:            make(map[string]latencyRouteCacheEntry),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tr)
		}
	}
	tr.loadSystemSettings(maxTests)
	tr.loadStoredScripts()
	tr.loadStoredScenarios()
	tr.loadStoredTests()
	tr.initRefKeys()
	tr.ReconcilePendingTests()

	return tr
}

func (tr *TestRunner) setExecutionBackend(backend testExecutionBackend) {
	if backend == nil {
		backend = localExecutionBackend{}
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.execution = backend
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
// leadership epoch. Single-controller deployments retain ordinary caller
// cancellation semantics.
func (tr *TestRunner) executionOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if tr == nil || tr.leadership == nil {
		return context.WithCancel(parent)
	}
	return tr.leadership.operationContext(parent)
}

const (
	refRunDateLayout = "20060102"
)

// Helper to convert map back to LiveMetrics (since GetHistory returns any)

type CreateTestRequest struct {
	Name                string     `json:"name"`
	Duration            string     `json:"duration"`
	StartDelay          string     `json:"start_delay,omitempty"`
	ReserveLaunchWindow bool       `json:"reserve_launch_window,omitempty"`
	ApplicationID       string     `json:"application_id,omitempty"`
	ReleaseID           string     `json:"release_id,omitempty"`
	ScenarioID          string     `json:"scenario_id,omitempty"`
	ProjectID           string     `json:"project_id,omitempty"`
	FolderID            *string    `json:"folder_id,omitempty"`
	ScheduledStartAt    *time.Time `json:"scheduled_start_at,omitempty"`
	// TimeslotID, when set, binds the run to an approved capacity timeslot as part
	// of creation; if the timeslot can't be bound the create fails (strict).
	TimeslotID       *string                           `json:"timeslot_id,omitempty"`
	ScenarioMode     string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                              `json:"resource_baseline,omitempty"`
	Assignment       *GeneratorAssignmentSettings      `json:"assignment,omitempty"`
	GoalConfig       *GoalConfig                       `json:"goal_config,omitempty"`
	Groups           []CreateGroupEntry                `json:"groups,omitempty"`
	DataPools        []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	// Single-script shorthand (backwards compatible)
	ScriptID string `json:"script_id,omitempty"`
	VUsers   int    `json:"vusers,omitempty"`
	RampUp   string `json:"ramp_up,omitempty"`
	// Test-level runtime overrides (mirrors LR Controller override behavior)
	RuntimeSettings *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`

	scriptShakeoutProvided   bool
	resourceBaselineProvided bool
}

type CreateScenarioRequest struct {
	Name             string                            `json:"name"`
	Duration         string                            `json:"duration"`
	StartDelay       string                            `json:"start_delay,omitempty"`
	ScenarioMode     string                            `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                              `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                              `json:"resource_baseline,omitempty"`
	Assignment       *GeneratorAssignmentSettings      `json:"assignment,omitempty"`
	GoalConfig       *GoalConfig                       `json:"goal_config,omitempty"`
	RuntimeSettings  *RuntimeSettingsOverride          `json:"runtime_settings,omitempty"`
	Groups           []CreateGroupEntry                `json:"groups"`
	DataPools        []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
}

type GoalConfig struct {
	TargetAverageTPS         *float64 `json:"target_average_tps,omitempty"`
	P95LatencyMs             *float64 `json:"p95_latency_ms,omitempty"`
	MaxErrorPct              *float64 `json:"max_error_pct,omitempty"`
	MaxAvgServerProcessingMs *float64 `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs         *float64 `json:"max_avg_transfer_ms,omitempty"`
	MaxErrorCount            *int     `json:"max_error_count,omitempty"`
	MinVUsers                *int     `json:"min_vusers,omitempty"`
	MaxVUsers                *int     `json:"max_vusers,omitempty"`
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
}

// RuntimeSettingsOverride allows overriding script-level runtime settings at test time,
// mirroring LoadRunner Controller's override capability.
type RuntimeBrowserOverride struct {
	Headless      *bool  `json:"headless,omitempty"`
	DeviceProfile string `json:"device_profile,omitempty"`
	TouchMode     *bool  `json:"touch_mode,omitempty"`
	Orientation   string `json:"orientation,omitempty"`
}

type RuntimeSettingsOverride struct {
	Iterations    *int                     `json:"iterations,omitempty"`
	Pacing        *dsl.PacingConfig        `json:"pacing,omitempty"`
	ThinkTime     *dsl.ThinkTimeConfig     `json:"think_time,omitempty"`
	ErrorHandling *dsl.ErrorHandlingConfig `json:"error_handling,omitempty"`
	Log           *dsl.LogConfig           `json:"log,omitempty"`
	RunLogic      *dsl.RunLogicConfig      `json:"run_logic,omitempty"`
	HTTP          *dsl.HTTPConfig          `json:"http,omitempty"`
	WebRuntime    *dsl.WebRuntimeConfig    `json:"web_runtime,omitempty"`
	// BrowserConfig is the full browser section (whole-section replace); it wins over
	// the legacy 4-field Browser partial-merge below when both are present.
	BrowserConfig *dsl.BrowserConfig      `json:"browser_config,omitempty"`
	Browser       *RuntimeBrowserOverride `json:"browser,omitempty"`
	// LogLevel/Iterations/AutoTransactions are legacy single-field aliases for the
	// Log/RunLogic sections above; the section wins when both are present.
	LogLevel         string                 `json:"log_level,omitempty"`
	AutoTransactions string                 `json:"auto_transactions,omitempty"`
	BaseURL          string                 `json:"base_url,omitempty"`
	Env              map[string]string      `json:"env,omitempty"`
	RedactionRules   []engine.RedactionRule `json:"redaction_rules,omitempty"`
}

type CreateGroupEntry struct {
	Name            string                       `json:"name"`
	ScriptID        string                       `json:"script_id"`
	VUsers          int                          `json:"vusers"`
	RampUp          string                       `json:"ramp_up,omitempty"`
	RampDown        string                       `json:"ramp_down,omitempty"`
	Stages          []storage.LoadStage          `json:"stages,omitempty"`
	Iterations      *int                         `json:"iterations,omitempty"`
	RuntimeSettings *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	Assignment      *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	InitStrategy    string                       `json:"initialization,omitempty"` // "at_runtime", "at_once"
	// Role distinguishes load-bearing groups ("load", default) from experience
	// canaries ("observer", alias "canary"): small browser groups that emit
	// Web Vitals continuously, are pinned during goal-based scaling, and stay
	// within the diagnostic canary density envelope. Use the group Assignment
	// to place observers on dedicated capacity or a specific load zone.
	Role string `json:"role,omitempty"`
}

type StartTestOptions struct {
	OverrideCapacityShortfall   bool `json:"override_capacity_shortfall,omitempty"`
	OverrideScriptShakeoutError bool `json:"override_script_shakeout_failure,omitempty"`
	SkipLaunchWindowPreflight   bool `json:"-"`
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

type ScriptShakeoutResult struct {
	GroupName  string `json:"group_name"`
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

type DraftCapacityEstimate struct {
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

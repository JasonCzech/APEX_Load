package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/distributed/generatorrpc"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type scriptPersistence interface {
	SaveScript(script *dsl.Script) error
	DeleteScript(id string) error
	ListScripts(projectID string, folderID *string) ([]*dsl.Script, error)
	MoveScript(id string, folderID *string) error
}

type testPersistence interface {
	SaveTest(test *storage.TestRecord) error
	ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error)
	ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*storage.TestRecord, error)
	UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error
	DeleteTest(id string) error
	MoveTest(id string, folderID *string) error
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
	SaveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval) error
	GetScenarioScheduleApproval(id string) (*storage.ScenarioScheduleApproval, error)
	ListScenarioScheduleApprovals(scheduleID string) ([]*storage.ScenarioScheduleApproval, error)
	TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error)
	GetSchedulerLease(name string) (*storage.SchedulerLease, error)
	SaveScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) error
	ListScenarioDataPoolBindings(scenarioID string) ([]storage.ScenarioDataPoolBinding, error)
	SaveAuditEvent(event *storage.AuditEvent) error
}

type dataPoolPersistence interface {
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

type archivePersistence interface {
	SaveRunArchive(record *storage.RunArchiveRecord) error
	GetRunArchive(testID string) (*storage.RunArchiveRecord, error)
}

type settingsPersistence interface {
	SaveSetting(key string, value string) error
	GetSetting(key string) (string, error)
}

type usageSnapshotPersistence interface {
	SaveUsageAccountingSnapshot(snapshot *storage.UsageAccountingSnapshot) error
}

type TestRunner struct {
	tests         map[string]*ManagedTest
	scripts       map[string]*StoredScript
	scenarios     map[string]*StoredScenario
	archives      map[string]*storage.RunArchiveRecord
	mu            sync.RWMutex
	hub           *Hub
	maxTests      int
	settings      SystemSettings
	scriptStore   scriptPersistence
	testStore     testPersistence
	scenarioStore scenarioPersistence
	dataPoolStore dataPoolPersistence
	projectStore  projectPersistence
	releaseStore  releasePersistence
	historyStore  historyPersistence
	archiveStore  archivePersistence
	settingsStore settingsPersistence
	usageStore    usageSnapshotPersistence
	liveState     LiveStateStore
	execution     testExecutionBackend
	notifier      *notificationDispatcher
	notifications atomic.Value
}

type ManagedTest struct {
	ID                    string                       `json:"id"`
	Name                  string                       `json:"name"`
	Status                string                       `json:"status"`
	ApplicationID         string                       `json:"application_id,omitempty"`
	ReleaseID             string                       `json:"release_id,omitempty"`
	ScenarioID            string                       `json:"scenario_id,omitempty"`
	ProjectID             string                       `json:"project_id,omitempty"`
	FolderID              *string                      `json:"folder_id,omitempty"`
	Groups                []TestGroup                  `json:"groups"`
	CapacityAdvisory      *CapacityAdvisory            `json:"capacity_advisory,omitempty"`
	RuntimeSettingsReview *RuntimeSettingsReview       `json:"runtime_settings_review,omitempty"`
	Assignment            *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	ScenarioMode          string                       `json:"scenario_mode,omitempty"`
	ScriptShakeout        bool                         `json:"script_shakeout,omitempty"`
	ResourceBaseline      bool                         `json:"resource_baseline,omitempty"`
	StartDelay            string                       `json:"start_delay,omitempty"`
	GoalConfig            *GoalConfig                  `json:"goal_config,omitempty"`
	RuntimeSettings       *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	TotalVUsers           int                          `json:"total_vusers"`
	Duration              time.Duration                `json:"duration_ns"`
	DurationStr           string                       `json:"duration"`
	CreatedAt             time.Time                    `json:"created_at"`
	PendingSince          *time.Time                   `json:"pending_since,omitempty"`
	PendingReason         string                       `json:"pending_reason,omitempty"`
	QueuePosition         int                          `json:"queue_position,omitempty"`
	BaselinePinned        bool                         `json:"baseline_pinned,omitempty"`
	BaselinePinnedAt      *time.Time                   `json:"baseline_pinned_at,omitempty"`
	StartedAt             *time.Time                   `json:"started_at,omitempty"`
	EndedAt               *time.Time                   `json:"ended_at,omitempty"`
	LiveMetrics           *LiveMetrics                 `json:"live_metrics,omitempty"`
	History               []*LiveMetrics               `json:"history,omitempty"`
	ComparisonBaseline    *RunComparisonBaseline       `json:"comparison_baseline,omitempty"`
	Archive               *storage.RunArchiveRecord    `json:"archive,omitempty"`
	TelemetryHealth       *TelemetryRunHealth          `json:"telemetry_health,omitempty"`
	Error                 string                       `json:"error,omitempty"`

	// deprecated single-script fields kept for backwards compat in JSON
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`

	multiEngine                  *engine.MultiGroupEngine
	cancel                       context.CancelFunc
	groupSpecs                   []engine.GroupConfig
	goalControl                  *goalControllerState
	executionBackend             testExecutionBackend
	remoteTarget                 string
	remoteClient                 generatorClient
	remoteMetricsStream          *generatorrpc.MetricsStream
	remoteGroupTargets           map[string]string
	remoteClientsByTarget        map[string]generatorClient
	remoteMetricsStreamsByTarget map[string]*generatorrpc.MetricsStream
	remoteMetricsBatchesByTarget map[string]generatorrpc.MetricsBatch
	remoteTargetStates           map[string]string
	remoteFinalizing             bool
	telemetryDiagnostic          telemetryDiagnosticState
	errorPatterns                map[string]*LiveErrorPattern
}

type TestGroup struct {
	Name           string                `json:"name"`
	ScriptID       string                `json:"script_id"`
	ScriptName     string                `json:"script_name"`
	Protocol       string                `json:"protocol"`
	VUsers         int                   `json:"vusers"`
	RampUp         string                `json:"ramp_up,omitempty"`
	RampDown       string                `json:"ramp_down,omitempty"`
	Stages         []storage.LoadStage   `json:"stages,omitempty"`
	Iterations     *int                  `json:"iterations,omitempty"`
	RunLogicGoals  *RunLogicGoalSnapshot `json:"run_logic_goals,omitempty"`
	Initialization string                `json:"initialization,omitempty"`
	rampUpDur      time.Duration
	script         *dsl.Script
}

type RunLogicGoalSnapshot struct {
	GlobalIterations int   `json:"global_iterations,omitempty"`
	MaxTransactions  int64 `json:"max_transactions,omitempty"`
}

type LiveMetrics struct {
	Timestamp              time.Time                            `json:"timestamp"`
	ActiveVUsers           int64                                `json:"active_vusers"`
	TPS                    float64                              `json:"tps"`
	TargetTPS              float64                              `json:"target_tps,omitempty"`
	ThroughputGapPct       float64                              `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool                                 `json:"throughput_target_missed,omitempty"`
	AvgResponseMs          float64                              `json:"avg_response_ms"`
	ErrorPct               float64                              `json:"error_pct"`
	TotalTx                int64                                `json:"total_transactions"`
	TotalErrors            int64                                `json:"total_errors"`
	P50Ms                  float64                              `json:"p50_ms"`
	P90Ms                  float64                              `json:"p90_ms"`
	P95Ms                  float64                              `json:"p95_ms"`
	P99Ms                  float64                              `json:"p99_ms"`
	Transactions           map[string]*TxMetrics                `json:"transactions,omitempty"`
	DependencyHealth       map[string]*TxMetrics                `json:"dependency_health,omitempty"`
	DependencyMap          *LiveDependencyMap                   `json:"dependency_map,omitempty"`
	ErrorPatterns          []LiveErrorPattern                   `json:"error_patterns,omitempty"`
	SOAPOperations         map[string]*TxMetrics                `json:"soap_operations,omitempty"`
	NetworkProfiles        map[string]*TxMetrics                `json:"network_profiles,omitempty"`
	GroupMetrics           map[string]*GroupLiveMetrics         `json:"groups,omitempty"`
	Browser                *BrowserLiveMetrics                  `json:"browser,omitempty"`
	BrowserByDevice        map[string]*BrowserDeviceLiveMetrics `json:"browser_by_device,omitempty"`
	GoalStatus             *GoalStatus                          `json:"goal_status,omitempty"`
	CapacityStatus         *LiveCapacityStatus                  `json:"capacity_status,omitempty"`
	TelemetryStatus        *TelemetryRunHealth                  `json:"telemetry_status,omitempty"`
	Telemetry              *TelemetryHealth                     `json:"telemetry,omitempty"`
	Resources              *engine.SystemResources              `json:"resources,omitempty"`
	Latency                *NetworkLatency                      `json:"latency,omitempty"`
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
	AvgMessageLatencyMs       float64          `json:"avg_message_latency_ms,omitempty"`
	MaxMessageLatencyMs       float64          `json:"max_message_latency_ms,omitempty"`
	MessagesSent              int64            `json:"messages_sent,omitempty"`
	MessagesReceived          int64            `json:"messages_received,omitempty"`
	AvgMessagesSentPerSec     float64          `json:"avg_messages_sent_per_sec,omitempty"`
	AvgMessagesReceivedPerSec float64          `json:"avg_messages_received_per_sec,omitempty"`
	AvgConsumerLagMs          float64          `json:"avg_consumer_lag_ms,omitempty"`
	MaxConsumerLagMs          float64          `json:"max_consumer_lag_ms,omitempty"`
	RequestSizeClasses        map[string]int64 `json:"request_size_classes,omitempty"`
	ResponseSizeClasses       map[string]int64 `json:"response_size_classes,omitempty"`
	ConnectionReusedCount     int64            `json:"connection_reused_count,omitempty"`
	ConnectionReusedPct       float64          `json:"connection_reused_pct,omitempty"`
}

type TelemetryHealth struct {
	DroppedResults           int64     `json:"dropped_results,omitempty"`
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
	Name        string    `json:"name"`
	Endpoint    string    `json:"endpoint"`
	LatencyMs   float64   `json:"latency_ms"`
	IP          string    `json:"ip,omitempty"`
	Hops        int       `json:"hops,omitempty"`
	LastSuccess bool      `json:"last_success"`
	Timestamp   time.Time `json:"timestamp"`
}

type GoalStatus struct {
	Mode                     string     `json:"mode"`
	CurrentTPS               float64    `json:"current_tps"`
	CurrentTotalVUsers       int        `json:"current_total_vusers"`
	TargetAverageTPS         *float64   `json:"target_average_tps,omitempty"`
	MaxErrorCount            *int       `json:"max_error_count,omitempty"`
	MinVUsers                *int       `json:"min_vusers,omitempty"`
	MaxVUsers                *int       `json:"max_vusers,omitempty"`
	SuggestedTotalVUsers     int        `json:"suggested_total_vusers,omitempty"`
	LastScaleDirection       string     `json:"last_scale_direction,omitempty"`
	LastScaleReason          string     `json:"last_scale_reason,omitempty"`
	LastScaleAt              *time.Time `json:"last_scale_at,omitempty"`
	LatencyBreached          bool       `json:"latency_breached,omitempty"`
	ErrorBreached            bool       `json:"error_breached,omitempty"`
	ServerProcessingBreached bool       `json:"server_processing_breached,omitempty"`
	TransferBreached         bool       `json:"transfer_breached,omitempty"`
	ErrorCountBreached       bool       `json:"error_count_breached,omitempty"`
	BreachActive             bool       `json:"breach_active,omitempty"`
	BreachDurationSec        int        `json:"breach_duration_sec,omitempty"`
	StopTriggered            bool       `json:"stop_triggered,omitempty"`
	StopReason               string     `json:"stop_reason,omitempty"`
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
}

type goalControllerState struct {
	breachStartedAt    *time.Time
	lastScaleAt        *time.Time
	lastScaleDirection string
	lastScaleReason    string
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
	Signature      string    `json:"signature"`
	Sample         string    `json:"sample"`
	Count          int64     `json:"count"`
	FirstSeen      time.Time `json:"first_seen"`
	LatestSeen     time.Time `json:"latest_seen"`
	Actions        []string  `json:"actions,omitempty"`
	AffectedGroups []string  `json:"affected_groups,omitempty"`
	StatusCode     int       `json:"status_code,omitempty"`
	ErrorPct       float64   `json:"error_pct,omitempty"`
	SnapshotURL    string    `json:"snapshot_url,omitempty"`
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
	Name                   string  `json:"name"`
	VUsers                 int     `json:"vusers"`
	Transactions           int64   `json:"transactions"`
	Errors                 int64   `json:"errors"`
	TPS                    float64 `json:"tps"`
	TargetTPS              float64 `json:"target_tps,omitempty"`
	ThroughputGapPct       float64 `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool    `json:"throughput_target_missed,omitempty"`
	AvgResponseMs          float64 `json:"avg_response_ms"`
	P50Ms                  float64 `json:"p50_ms"`
	P90Ms                  float64 `json:"p90_ms"`
	P99Ms                  float64 `json:"p99_ms"`
}

type StoredScript struct {
	ID                  string           `json:"id"`
	Name                string           `json:"name"`
	Protocol            string           `json:"protocol"`
	Version             string           `json:"version"`
	ProjectID           string           `json:"project_id,omitempty"`
	FolderID            *string          `json:"folder_id,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	ScenarioCount       int              `json:"scenario_count,omitempty"`
	AssociatedReleases  []ReleaseRef     `json:"associated_releases,omitempty"`
	ScenarioRunReleases []ReleaseRef     `json:"scenario_run_releases,omitempty"`
	LastRunAt           *time.Time       `json:"last_run_at,omitempty"`
	AuthoringHealth     *AuthoringHealth `json:"authoring_health,omitempty"`
	Script              *dsl.Script      `json:"script"`
}

type StoredScenario struct {
	ID                 string                            `json:"id"`
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
	LastRunAt          *time.Time                        `json:"last_run_at,omitempty"`
}

type ScenarioScriptRef struct {
	Order          int                 `json:"order"`
	Name           string              `json:"name,omitempty"`
	ScriptID       string              `json:"script_id"`
	ScriptName     string              `json:"script_name,omitempty"`
	Protocol       string              `json:"protocol,omitempty"`
	VUsers         int                 `json:"vusers"`
	RampUp         string              `json:"ramp_up,omitempty"`
	RampDown       string              `json:"ramp_down,omitempty"`
	Stages         []storage.LoadStage `json:"stages,omitempty"`
	Iterations     *int                `json:"iterations,omitempty"`
	Initialization string              `json:"initialization,omitempty"`
}

const (
	generatorAssignmentModeAuto           = "auto"
	generatorAssignmentModeSpecificHosts  = "specific_hosts"
	generatorAssignmentModeRegionWeighted = "region_weighted"
	maxManagedTestHistory                 = 3600
	telemetryDiagnosticSampleInterval     = 5 * time.Second
	telemetrySustainedBreachWindow        = 15 * time.Second
	maxTelemetryDiagnosticSamples         = 3
	slowTransactionDiagnosticThresholdMs  = 1000.0
	jmsBrokerPressureMinLagMs             = 200.0
	jmsBrokerPressureLagMultiplier        = 2.0
	jmsBrokerPressureSendRecvRatio        = 1.5
)

var runtimeOverrideEnvPattern = regexp.MustCompile(`\{\{\s*env\.([^{}\s]+)\s*\}\}`)

type GeneratorAssignmentSettings struct {
	Mode            string         `json:"mode,omitempty"`
	SelectedTargets []string       `json:"selected_targets,omitempty"`
	RegionWeights   map[string]int `json:"region_weights,omitempty"`
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
	Mode               string   `json:"mode"`
	AvailableTargets   []string `json:"available_targets,omitempty"`
	AvailableRegions   []string `json:"available_regions,omitempty"`
	RecommendedTargets []string `json:"recommended_targets,omitempty"`
	Summary            string   `json:"summary,omitempty"`
	Warnings           []string `json:"warnings,omitempty"`
}

type ReleaseRef struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ApplicationID string `json:"application_id"`
}

type archiveRecorder interface {
	engine.ResultSink
	RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error
	RecordIntervalSummary(live *LiveMetrics) error
	Finalize(status string, completedAt time.Time, errStr string, slaBreached bool, slaDetails []string) (*storage.RunArchiveRecord, error)
}

type runArtifacts struct {
	recorder        archiveRecorder
	saveRecord      func(record *storage.RunArchiveRecord) error
	externalMetrics *externalMetricCollector
}

func NewTestRunner(maxTests int, hub *Hub, store storage.Store) *TestRunner {
	tr := &TestRunner{
		tests:         make(map[string]*ManagedTest),
		scripts:       make(map[string]*StoredScript),
		scenarios:     make(map[string]*StoredScenario),
		archives:      make(map[string]*storage.RunArchiveRecord),
		hub:           hub,
		maxTests:      maxTests,
		scriptStore:   store,
		testStore:     store,
		scenarioStore: store,
		dataPoolStore: store,
		projectStore:  store,
		releaseStore:  store,
		historyStore:  store,
		archiveStore:  store,
		settingsStore: store,
		usageStore:    store,
		execution:     localExecutionBackend{},
		notifier:      newNotificationDispatcher(nil),
	}
	tr.loadSystemSettings(maxTests)
	tr.loadStoredScripts()
	tr.loadStoredScenarios()
	tr.loadStoredTests()
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

func (tr *TestRunner) loadStoredScripts() {
	if tr.scriptStore == nil {
		return
	}
	scripts, err := tr.scriptStore.ListScripts("", nil)
	if err != nil {
		return
	}
	for _, script := range scripts {
		tr.scripts[script.ID] = &StoredScript{
			ID:              script.ID,
			Name:            script.Name,
			Protocol:        script.Protocol,
			Version:         script.Version,
			ProjectID:       script.ProjectID,
			FolderID:        script.FolderID,
			CreatedAt:       script.CreatedAt,
			AuthoringHealth: buildStoredScriptAuthoringHealth(script),
			Script:          script,
		}
	}
}

func (tr *TestRunner) loadStoredScenarios() {
	if tr.scenarioStore == nil {
		return
	}
	scenarios, err := tr.scenarioStore.ListScenarios()
	if err != nil {
		return
	}
	for _, scenario := range scenarios {
		full, scripts, err := tr.scenarioStore.GetScenario(scenario.ID)
		if err != nil {
			full = scenario
		}
		tr.scenarios[scenario.ID] = tr.buildStoredScenario(full, scripts)
	}
}

func (tr *TestRunner) buildStoredScenario(scenario *storage.Scenario, scripts []storage.ScenarioScript) *StoredScenario {
	if scenario == nil {
		return nil
	}
	refs := make([]ScenarioScriptRef, 0, len(scripts))
	projectID, singleProject := tr.scenarioProjectIDFromScriptsLocked(scripts)
	for _, script := range scripts {
		ref := ScenarioScriptRef{
			Order:          script.Order,
			Name:           script.Name,
			ScriptID:       script.ScriptID,
			VUsers:         script.VUsers,
			RampUp:         script.RampUp,
			RampDown:       script.RampDown,
			Stages:         cloneLoadStages(script.Stages),
			Iterations:     script.Iterations,
			Initialization: script.Initialization,
		}
		if stored, ok := tr.scripts[script.ScriptID]; ok {
			ref.ScriptName = stored.Name
			ref.Protocol = stored.Protocol
			if ref.Name == "" {
				ref.Name = stored.Name
			}
		}
		refs = append(refs, ref)
	}
	dataPools := []storage.ScenarioDataPoolBinding{}
	if tr.scenarioStore != nil {
		if bindings, err := tr.scenarioStore.ListScenarioDataPoolBindings(scenario.ID); err == nil {
			dataPools = bindings
		}
	}
	schedules := []*storage.ScenarioSchedule{}
	if tr.scenarioStore != nil {
		if storedSchedules, err := tr.scenarioStore.ListScenarioSchedules(scenario.ID); err == nil {
			for _, schedule := range storedSchedules {
				schedules = append(schedules, cloneScenarioSchedule(schedule))
			}
		}
	}
	derivedProjectID := ""
	if singleProject {
		derivedProjectID = projectID
	}
	return &StoredScenario{
		ID:               scenario.ID,
		Name:             scenario.Name,
		ProjectID:        derivedProjectID,
		Duration:         scenario.Duration,
		StartDelay:       scenario.StartDelay,
		ScenarioMode:     normalizeScenarioMode(scenario.ScenarioMode),
		ScriptShakeout:   scenario.ScriptShakeout,
		ResourceBaseline: scenario.ResourceBaseline,
		Assignment:       decodeStoredGeneratorAssignment(scenario.Assignment),
		GoalConfig:       decodeStoredGoalConfig(scenario.GoalConfig),
		RuntimeSettings:  decodeStoredRuntimeSettings(scenario.RuntimeSettings),
		Scripts:          refs,
		Schedules:        schedules,
		DataPools:        dataPools,
		CreatedAt:        scenario.CreatedAt,
		UpdatedAt:        scenario.UpdatedAt,
		ScriptCount:      len(refs),
	}
}

func (tr *TestRunner) scenarioProjectIDFromScriptsLocked(scripts []storage.ScenarioScript) (string, bool) {
	projectID := ""
	for _, scenarioScript := range scripts {
		stored, ok := tr.scripts[scenarioScript.ScriptID]
		if !ok || stored == nil {
			continue
		}
		candidate := normalizeProjectID(stored.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", false
		}
	}
	if projectID == "" {
		return "", false
	}
	return projectID, true
}

func (tr *TestRunner) loadStoredTests() {
	if tr.testStore == nil {
		return
	}
	tests, err := tr.testStore.ListTests("", nil)
	if err != nil {
		return
	}
	for _, record := range tests {
		managedTest := managedTestFromRecord(record)
		tr.loadStoredTestHistory(managedTest)
		tr.loadStoredTestArchive(managedTest)
		tr.tests[record.ID] = managedTest
	}
}

func managedTestFromRecord(record *storage.TestRecord) *ManagedTest {
	managedTest := &ManagedTest{
		ID:               record.ID,
		Name:             record.Name,
		Status:           record.Status,
		ApplicationID:    record.ApplicationID,
		ReleaseID:        record.ReleaseID,
		ScenarioID:       record.ScenarioID,
		ProjectID:        record.ProjectID,
		FolderID:         record.FolderID,
		Groups:           decodeStoredGroups(record.Groups),
		Assignment:       decodeStoredGeneratorAssignment(record.Assignment),
		ScenarioMode:     normalizeScenarioMode(record.ScenarioMode),
		ScriptShakeout:   record.ScriptShakeout,
		ResourceBaseline: record.ResourceBaseline,
		StartDelay:       record.StartDelay,
		GoalConfig:       decodeStoredGoalConfig(record.GoalConfig),
		RuntimeSettings:  decodeStoredRuntimeSettings(record.RuntimeSettings),
		TotalVUsers:      record.TotalVUsers,
		Duration:         time.Duration(record.Duration),
		DurationStr:      time.Duration(record.Duration).String(),
		CreatedAt:        record.StartTime,
		PendingSince:     record.PendingSince,
		BaselinePinned:   record.BaselinePinned,
		BaselinePinnedAt: record.BaselinePinnedAt,
		Error:            record.Error,
	}
	if record.EndTime != nil {
		managedTest.EndedAt = record.EndTime
	}
	return managedTest
}

func (tr *TestRunner) loadStoredTestHistory(test *ManagedTest) {
	if tr.historyStore == nil {
		return
	}
	history, err := tr.historyStore.GetHistory(test.ID)
	if err != nil {
		return
	}
	for _, entry := range history {
		switch metrics := entry.(type) {
		case storage.IntervalSummaryRecord:
			test.History = append(test.History, intervalSummaryRecordToLiveMetrics(metrics))
		case *storage.IntervalSummaryRecord:
			if metrics != nil {
				test.History = append(test.History, intervalSummaryRecordToLiveMetrics(*metrics))
			}
		case map[string]any:
			// This is a bit lossy due to any -> map conversion,
			// but works for basic history display.
			test.History = append(test.History, mapToLiveMetrics(metrics))
		}
	}
	if len(test.History) > maxManagedTestHistory {
		test.History = append([]*LiveMetrics(nil), test.History[len(test.History)-maxManagedTestHistory:]...)
	}
	if len(test.History) > 0 {
		test.LiveMetrics = test.History[len(test.History)-1]
	}
}

func (tr *TestRunner) loadStoredTestArchive(test *ManagedTest) {
	if tr.archiveStore == nil {
		return
	}
	archive, err := tr.archiveStore.GetRunArchive(test.ID)
	if err != nil {
		return
	}
	if !runArchiveRecordAvailable(archive) {
		return
	}
	test.Archive = archive
	tr.archives[test.ID] = archive
}

// Helper to convert map back to LiveMetrics (since GetHistory returns any)
func mapToLiveMetrics(m map[string]any) *LiveMetrics {
	// Re-marshal to JSON and unmarshal to struct to handle all fields robustly
	data, err := json.Marshal(m)
	if err != nil {
		return &LiveMetrics{}
	}
	var lm LiveMetrics
	if err := json.Unmarshal(data, &lm); err != nil {
		return &LiveMetrics{}
	}
	return &lm
}

func intervalSummaryRecordToLiveMetrics(record storage.IntervalSummaryRecord) *LiveMetrics {
	live := &LiveMetrics{
		Timestamp:       record.Timestamp,
		ActiveVUsers:    record.ActiveVUsers,
		TPS:             record.TPS,
		AvgResponseMs:   record.AvgResponseMs,
		ErrorPct:        record.ErrorPct,
		TotalTx:         record.TotalTx,
		TotalErrors:     record.TotalErrors,
		P50Ms:           record.P50Ms,
		P90Ms:           record.P90Ms,
		P95Ms:           record.P95Ms,
		P99Ms:           record.P99Ms,
		Browser:         browserSummaryRecordToLiveMetrics(record.Browser),
		BrowserByDevice: browserDeviceSummaryRecordToLiveMetrics(record.BrowserByDevice),
	}
	if record.DroppedResults > 0 || record.WebsocketClients > 0 || record.WebsocketDroppedMessages > 0 || record.WebsocketDroppedEvents > 0 || !record.CollectedAt.IsZero() || !record.GeneratorPublishedAt.IsZero() || !record.ControllerReceivedAt.IsZero() || !record.PublishedAt.IsZero() || record.SampleAgeMs > 0 || record.GeneratorProcessingLagMs > 0 || record.TransportLagMs > 0 || record.ControllerFanoutLagMs > 0 {
		live.Telemetry = &TelemetryHealth{
			DroppedResults:           record.DroppedResults,
			WebsocketClients:         record.WebsocketClients,
			WebsocketDroppedMessages: record.WebsocketDroppedMessages,
			WebsocketDroppedEvents:   record.WebsocketDroppedEvents,
			GeneratorPublishedAt:     record.GeneratorPublishedAt,
			ControllerReceivedAt:     record.ControllerReceivedAt,
			CollectedAt:              record.CollectedAt,
			PublishedAt:              record.PublishedAt,
			GeneratorProcessingLagMs: record.GeneratorProcessingLagMs,
			TransportLagMs:           record.TransportLagMs,
			ControllerFanoutLagMs:    record.ControllerFanoutLagMs,
			SampleAgeMs:              record.SampleAgeMs,
		}
	}
	return live
}

func browserSummaryRecordToLiveMetrics(record *storage.BrowserMetricsSummaryRecord) *BrowserLiveMetrics {
	if record == nil {
		return nil
	}
	return &BrowserLiveMetrics{
		Samples:              record.Samples,
		AvgFCPMs:             record.AvgFCPMs,
		AvgLCPMs:             record.AvgLCPMs,
		AvgCLSScore:          record.AvgCLSScore,
		AvgINPMs:             record.AvgINPMs,
		AvgBlockingResources: record.AvgBlockingResources,
		AvgLongTaskCount:     record.AvgLongTaskCount,
		AvgJSHeapUsedMB:      record.AvgJSHeapUsedMB,
		AvgBrowserProcessCPU: record.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: record.AvgBrowserProcessMem,
	}
}

func browserDeviceSummaryRecordToLiveMetrics(records map[string]*storage.BrowserDeviceMetricsRecord) map[string]*BrowserDeviceLiveMetrics {
	if len(records) == 0 {
		return nil
	}
	result := make(map[string]*BrowserDeviceLiveMetrics, len(records))
	for name, record := range records {
		if record == nil {
			continue
		}
		result[name] = &BrowserDeviceLiveMetrics{
			DeviceProfile:        record.DeviceProfile,
			DevicePlatform:       record.DevicePlatform,
			DeviceTier:           record.DeviceTier,
			BrowserEngine:        record.BrowserEngine,
			Samples:              record.Samples,
			AvgFCPMs:             record.AvgFCPMs,
			AvgLCPMs:             record.AvgLCPMs,
			AvgCLSScore:          record.AvgCLSScore,
			AvgINPMs:             record.AvgINPMs,
			AvgBlockingResources: record.AvgBlockingResources,
			AvgLongTaskCount:     record.AvgLongTaskCount,
			AvgJSHeapUsedMB:      record.AvgJSHeapUsedMB,
			AvgBrowserProcessCPU: record.AvgBrowserProcessCPU,
			AvgBrowserProcessMem: record.AvgBrowserProcessMem,
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (tr *TestRunner) StoreScript(script *dsl.Script) *StoredScript {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	script.ProjectID = normalizeProjectID(script.ProjectID)

	stored := &StoredScript{
		ID:              script.ID,
		Name:            script.Name,
		Protocol:        script.Protocol,
		Version:         script.Version,
		ProjectID:       script.ProjectID,
		FolderID:        script.FolderID,
		CreatedAt:       time.Now(),
		AuthoringHealth: buildStoredScriptAuthoringHealth(script),
		Script:          script,
	}

	tr.saveStoredScript(script)

	tr.scripts[stored.ID] = stored
	return stored
}

func (tr *TestRunner) GetScript(id string) (*StoredScript, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	s, ok := tr.scripts[id]
	return s, ok
}

func (tr *TestRunner) ReplayScript(id string, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	tr.mu.RLock()
	stored, ok := tr.scripts[id]
	tr.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("script not found")
	}
	return tr.ReplayScriptDefinition(stored.Script, opts)
}

func (tr *TestRunner) ReplayScriptDefinition(script *dsl.Script, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	effectiveRedactionRules, err := tr.resolveEffectiveRedactionRules(script.ProjectID, opts.RedactionRules)
	if err != nil {
		return nil, err
	}
	opts.RedactionRules = effectiveRedactionRules
	return engine.ReplayScript(context.Background(), script, opts)
}

func (tr *TestRunner) ListScripts() []*StoredScript {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
		out = append(out, s)
	}
	return out
}

func (tr *TestRunner) ListScriptsFiltered(projectID string, folderID *string) []*StoredScript {
	projectID = normalizeProjectID(projectID)
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
		if s.ProjectID != projectID {
			continue
		}
		if folderID != nil {
			if *folderID == "" {
				if s.FolderID != nil && *s.FolderID != "" {
					continue
				}
			} else if s.FolderID == nil || *s.FolderID != *folderID {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func (tr *TestRunner) MoveScript(id string, folderID *string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	script, ok := tr.scripts[id]
	if !ok {
		return false
	}
	script.FolderID = folderID
	if script.Script != nil {
		script.Script.FolderID = folderID
	}
	if tr.scriptStore != nil {
		_ = tr.scriptStore.MoveScript(id, folderID)
	}
	return true
}

func (tr *TestRunner) UpdateScript(id string, script *dsl.Script) (*StoredScript, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	existing, ok := tr.scripts[id]
	if !ok {
		return nil, false
	}
	existing.Name = script.Name
	existing.Protocol = script.Protocol
	existing.Version = script.Version
	if script.ProjectID == "" {
		script.ProjectID = existing.ProjectID
	}
	if script.FolderID == nil {
		script.FolderID = existing.FolderID
	}
	existing.ProjectID = script.ProjectID
	existing.FolderID = script.FolderID
	existing.AuthoringHealth = buildStoredScriptAuthoringHealth(script)
	existing.Script = script
	tr.saveStoredScript(script)
	return existing, true
}

func (tr *TestRunner) DeleteScript(id string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	_, ok := tr.scripts[id]
	if !ok {
		return false
	}
	delete(tr.scripts, id)
	tr.deleteStoredScript(id)
	return true
}

func (tr *TestRunner) StoreScenario(req CreateScenarioRequest) (*StoredScenario, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.storeScenarioLocked(uuid.New().String(), req)
}

func (tr *TestRunner) UpdateScenario(id string, req CreateScenarioRequest) (*StoredScenario, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if _, ok := tr.scenarios[id]; !ok {
		return nil, false, nil
	}
	scenario, err := tr.storeScenarioLocked(id, req)
	return scenario, true, err
}

func (tr *TestRunner) storeScenarioLocked(id string, req CreateScenarioRequest) (*StoredScenario, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("scenario name is required")
	}
	if strings.TrimSpace(req.Duration) == "" {
		return nil, fmt.Errorf("scenario duration is required")
	}
	if _, err := validateScenarioRequest(CreateTestRequest{
		ScenarioMode: req.ScenarioMode,
		Assignment:   req.Assignment,
		GoalConfig:   req.GoalConfig,
	}); err != nil {
		return nil, err
	}
	groups, err := normalizeCreateTestGroups(CreateTestRequest{Groups: req.Groups})
	if err != nil {
		return nil, err
	}
	testGroups, engineGroups, _, err := tr.compileManagedTestGroupsLocked(groups, req.RuntimeSettings)
	if err != nil {
		return nil, err
	}
	if runtimeSettingsReview := buildRuntimeSettingsScenarioReview(testGroups, engineGroups); runtimeSettingsReview != nil && runtimeSettingsReview.Status == "blocked" {
		return nil, fmt.Errorf("runtime settings blocked: %s", runtimeSettingsReview.Summary)
	}
	now := time.Now()
	existing := tr.scenarios[id]
	createdAt := now
	if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	scenario := &storage.Scenario{
		ID:               id,
		Name:             req.Name,
		Duration:         req.Duration,
		StartDelay:       req.StartDelay,
		ScenarioMode:     normalizeScenarioMode(req.ScenarioMode),
		ScriptShakeout:   req.ScriptShakeout,
		ResourceBaseline: req.ResourceBaseline,
		Assignment:       cloneGeneratorAssignmentSettings(req.Assignment),
		GoalConfig:       req.GoalConfig,
		RuntimeSettings:  req.RuntimeSettings,
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	scripts := make([]storage.ScenarioScript, 0, len(groups))
	for i, group := range groups {
		scripts = append(scripts, storage.ScenarioScript{
			ScenarioID:     id,
			Order:          i,
			Name:           group.Name,
			ScriptID:       group.ScriptID,
			VUsers:         group.VUsers,
			RampUp:         group.RampUp,
			RampDown:       group.RampDown,
			Stages:         cloneLoadStages(group.Stages),
			Iterations:     group.Iterations,
			Initialization: group.InitStrategy,
		})
	}
	dataPools, err := normalizeScenarioDataPoolBindings(id, req.DataPools)
	if err != nil {
		return nil, err
	}
	if tr.scenarioStore != nil {
		if err := tr.scenarioStore.SaveScenario(scenario, scripts); err != nil {
			return nil, err
		}
		if err := tr.scenarioStore.SaveScenarioDataPoolBindings(id, dataPools); err != nil {
			return nil, err
		}
	}
	stored := tr.buildStoredScenario(scenario, scripts)
	stored.DataPools = dataPools
	tr.scenarios[id] = stored
	return stored, nil
}

func normalizeScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) ([]storage.ScenarioDataPoolBinding, error) {
	out := make([]storage.ScenarioDataPoolBinding, 0, len(bindings))
	seenAliases := map[string]struct{}{}
	now := time.Now()
	for _, binding := range bindings {
		binding.ScenarioID = scenarioID
		binding.PoolID = strings.TrimSpace(binding.PoolID)
		binding.Alias = strings.TrimSpace(binding.Alias)
		binding.AccessMode = strings.TrimSpace(binding.AccessMode)
		binding.SelectionStrategy = strings.TrimSpace(binding.SelectionStrategy)
		if binding.PoolID == "" {
			return nil, fmt.Errorf("data pool id is required")
		}
		if binding.Alias == "" {
			return nil, fmt.Errorf("data pool binding alias is required")
		}
		aliasKey := strings.ToLower(binding.Alias)
		if _, ok := seenAliases[aliasKey]; ok {
			return nil, fmt.Errorf("duplicate data pool binding alias %q", binding.Alias)
		}
		seenAliases[aliasKey] = struct{}{}
		if binding.ID == "" {
			binding.ID = uuid.New().String()
		}
		if binding.AccessMode == "" {
			binding.AccessMode = "read_only"
		}
		switch binding.AccessMode {
		case "read_only", "read_write", "consume":
		default:
			return nil, fmt.Errorf("unsupported data pool access mode %q", binding.AccessMode)
		}
		if binding.SelectionStrategy == "" {
			binding.SelectionStrategy = "sequential"
		}
		switch binding.SelectionStrategy {
		case "sequential", "random", "unique", "pop":
		default:
			return nil, fmt.Errorf("unsupported data pool selection strategy %q", binding.SelectionStrategy)
		}
		if binding.CreatedAt.IsZero() {
			binding.CreatedAt = now
		}
		binding.UpdatedAt = now
		out = append(out, binding)
	}
	return out, nil
}

func (tr *TestRunner) GetScenario(id string) (*StoredScenario, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	scenario, ok := tr.scenarios[id]
	if !ok {
		return nil, false
	}
	return tr.enrichScenarioSummaryLocked(scenario), true
}

func (tr *TestRunner) ListScenarios() []*StoredScenario {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		out = append(out, tr.enrichScenarioSummaryLocked(scenario))
	}
	return out
}

func (tr *TestRunner) ListScenariosFiltered(projectID string) []*StoredScenario {
	projectID = normalizeProjectID(projectID)
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		enriched := tr.enrichScenarioSummaryLocked(scenario)
		if enriched.ProjectID != projectID {
			continue
		}
		out = append(out, enriched)
	}
	return out
}

func (tr *TestRunner) DeleteScenario(id string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if _, ok := tr.scenarios[id]; !ok {
		return false
	}
	for _, test := range tr.tests {
		if test.ScenarioID == id {
			return false
		}
	}
	delete(tr.scenarios, id)
	if tr.scenarioStore != nil {
		_ = tr.scenarioStore.DeleteScenario(id)
	}
	return true
}

func (tr *TestRunner) saveScenarioScheduleLocked(schedule *storage.ScenarioSchedule) error {
	if schedule == nil {
		return fmt.Errorf("schedule is required")
	}
	scenario, ok := tr.scenarios[schedule.ScenarioID]
	if !ok {
		return fmt.Errorf("scenario not found: %s", schedule.ScenarioID)
	}
	if tr.scenarioStore != nil {
		if err := tr.scenarioStore.SaveScenarioSchedule(schedule); err != nil {
			return err
		}
	}
	for index, existing := range scenario.Schedules {
		if existing != nil && existing.ID == schedule.ID {
			scenario.Schedules[index] = cloneScenarioSchedule(schedule)
			return nil
		}
	}
	scenario.Schedules = append(scenario.Schedules, cloneScenarioSchedule(schedule))
	return nil
}

func (tr *TestRunner) saveScenarioScheduleOccurrenceLocked(occurrence *storage.ScenarioScheduleOccurrence) error {
	if occurrence == nil {
		return fmt.Errorf("schedule occurrence is required")
	}
	if tr.scenarioStore != nil {
		if existing, err := tr.scenarioStore.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(occurrence.ScheduleID, occurrence.PlannedAt); err != nil {
			return err
		} else if existing != nil && existing.ID != "" {
			occurrence.ID = existing.ID
			occurrence.CreatedAt = existing.CreatedAt
		}
		if err := tr.scenarioStore.SaveScenarioScheduleOccurrence(occurrence); err != nil {
			return err
		}
		tr.recordScenarioScheduleOccurrenceAuditLocked(occurrence)
		return nil
	}
	return nil
}

func (tr *TestRunner) recordScenarioScheduleOccurrenceAuditLocked(occurrence *storage.ScenarioScheduleOccurrence) {
	if tr == nil || tr.scenarioStore == nil || occurrence == nil {
		return
	}
	action, status, ok := scenarioScheduleOccurrenceAuditAction(occurrence.Status)
	if !ok {
		return
	}
	details := map[string]any{
		"schedule_id":       occurrence.ScheduleID,
		"scenario_id":       occurrence.ScenarioID,
		"schedule_revision": occurrence.ScheduleRevision,
		"planned_at":        occurrence.PlannedAt.UTC().Format(time.RFC3339),
		"occurrence_status": occurrence.Status,
		"reason_code":       occurrence.ReasonCode,
	}
	if occurrence.MaterializedAt != nil {
		details["materialized_at"] = occurrence.MaterializedAt.UTC().Format(time.RFC3339)
	}
	if occurrence.TestID != "" {
		details["test_id"] = occurrence.TestID
	}
	if occurrence.ApprovalID != "" {
		details["approval_id"] = occurrence.ApprovalID
	}
	if occurrence.ConflictSummary != "" {
		details["conflict_summary"] = occurrence.ConflictSummary
	}
	_ = tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           action,
		ActorType:        "system",
		ActorSubject:     "scenario-scheduler",
		ActorDisplayName: "Scenario Scheduler",
		ProjectID:        normalizeProjectID(occurrence.ProjectID),
		ResourceType:     "scenario_schedule_occurrence",
		ResourceID:       occurrence.ID,
		Status:           status,
		Details:          details,
		CreatedAt:        time.Now().UTC(),
	})
}

func scenarioScheduleOccurrenceAuditAction(occurrenceStatus string) (string, string, bool) {
	switch strings.TrimSpace(occurrenceStatus) {
	case "materialized", "queued", "completed", "approved":
		return "scenario.schedule.occurrence.materialized", "success", true
	case "blocked":
		return "scenario.schedule.occurrence.blocked", "blocked", true
	case "skipped":
		return "scenario.schedule.occurrence.skipped", "success", true
	case "failed":
		return "scenario.schedule.occurrence.failed", "failure", true
	default:
		return "", "", false
	}
}

func (tr *TestRunner) ListScenarioScheduleOccurrences(scenarioID string, scheduleID string) ([]*storage.ScenarioScheduleOccurrence, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, false
	}
	found := false
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			found = true
			break
		}
	}
	if !found {
		return nil, false
	}
	if tr.scenarioStore == nil {
		return []*storage.ScenarioScheduleOccurrence{}, true
	}
	occurrences, err := tr.scenarioStore.ListScenarioScheduleOccurrences(scheduleID)
	if err != nil {
		return []*storage.ScenarioScheduleOccurrence{}, true
	}
	out := make([]*storage.ScenarioScheduleOccurrence, 0, len(occurrences))
	for _, occurrence := range occurrences {
		out = append(out, cloneScenarioScheduleOccurrence(occurrence))
	}
	return out, true
}

func (tr *TestRunner) ScenarioScheduleHealth(now time.Time) ScenarioScheduleHealth {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	health := ScenarioScheduleHealth{StatusCounts: map[string]int{}}
	for _, schedule := range tr.listAllScenarioSchedulesLocked() {
		if schedule == nil {
			continue
		}
		health.Total++
		if schedule.Enabled {
			health.Enabled++
		}
		if schedule.NextRunAt != nil && !schedule.NextRunAt.After(now) {
			health.Due++
		}
		if strings.TrimSpace(schedule.ConflictSummary) != "" {
			health.Blocked++
		}
		if strings.TrimSpace(schedule.LastError) != "" {
			health.Failed++
		}
		if schedule.NextRequiredAction == "approval_required" {
			health.PendingApproval++
		}
	}
	if tr.scenarioStore != nil {
		if occurrences, err := tr.scenarioStore.ListAllScenarioScheduleOccurrences(); err == nil {
			for _, occurrence := range occurrences {
				if occurrence != nil {
					health.StatusCounts[occurrence.Status]++
				}
			}
		}
	}
	health.Queued = health.StatusCounts["queued"]
	health.Materialized = health.StatusCounts["materialized"]
	health.Skipped = health.StatusCounts["skipped"]
	health.Completed = health.StatusCounts["completed"]
	return health
}

func (tr *TestRunner) ListScriptsWithUsage() []*StoredScript {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, script := range tr.scripts {
		out = append(out, tr.enrichScriptSummaryLocked(script))
	}
	return out
}

func (tr *TestRunner) enrichScriptSummaryLocked(script *StoredScript) *StoredScript {
	if script == nil {
		return nil
	}
	copy := *script
	scenarioSet := map[string]bool{}
	releaseSet := map[string]ReleaseRef{}
	scenarioRunReleaseSet := map[string]ReleaseRef{}
	var lastRun *time.Time
	for _, scenario := range tr.scenarios {
		for _, scenarioScript := range scenario.Scripts {
			if scenarioScript.ScriptID == script.ID {
				scenarioSet[scenario.ID] = true
			}
		}
	}
	for _, test := range tr.tests {
		usesScript := false
		for _, group := range test.Groups {
			if group.ScriptID == script.ID {
				usesScript = true
				break
			}
		}
		if !usesScript {
			continue
		}
		if test.ReleaseID != "" {
			releaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
			if test.ScenarioID != "" {
				scenarioRunReleaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
			}
		}
		if lastRun == nil || test.CreatedAt.After(*lastRun) {
			t := test.CreatedAt
			lastRun = &t
		}
	}
	copy.ScenarioCount = len(scenarioSet)
	copy.AssociatedReleases = releaseRefsFromSet(releaseSet)
	copy.ScenarioRunReleases = releaseRefsFromSet(scenarioRunReleaseSet)
	copy.LastRunAt = lastRun
	return &copy
}

func (tr *TestRunner) enrichScenarioSummaryLocked(scenario *StoredScenario) *StoredScenario {
	if scenario == nil {
		return nil
	}
	copy := *scenario
	copy.Scripts = append([]ScenarioScriptRef(nil), scenario.Scripts...)
	releaseSet := map[string]ReleaseRef{}
	var lastRun *time.Time
	resultCount := 0
	for _, test := range tr.tests {
		if test.ScenarioID != scenario.ID {
			continue
		}
		if test.Status == "COMPLETE" || test.Status == "FAILED" || test.Status == "ABORTED" {
			resultCount++
		}
		if test.ReleaseID != "" {
			releaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
		}
		if lastRun == nil || test.CreatedAt.After(*lastRun) {
			t := test.CreatedAt
			lastRun = &t
		}
	}
	copy.ResultCount = resultCount
	copy.AssociatedReleases = releaseRefsFromSet(releaseSet)
	copy.LastRunAt = lastRun
	return &copy
}

func (tr *TestRunner) releaseRefLocked(id string) ReleaseRef {
	ref := ReleaseRef{ID: id, Name: id}
	if tr.releaseStore == nil {
		return ref
	}
	release, err := tr.releaseStore.GetRelease(id)
	if err != nil || release == nil {
		return ref
	}
	return ReleaseRef{ID: release.ID, Name: release.Name, ApplicationID: release.ApplicationID}
}

func releaseRefsFromSet(set map[string]ReleaseRef) []ReleaseRef {
	out := make([]ReleaseRef, 0, len(set))
	for _, release := range set {
		out = append(out, release)
	}
	return out
}

type CreateTestRequest struct {
	Name             string                            `json:"name"`
	Duration         string                            `json:"duration"`
	StartDelay       string                            `json:"start_delay,omitempty"`
	ApplicationID    string                            `json:"application_id,omitempty"`
	ReleaseID        string                            `json:"release_id,omitempty"`
	ScenarioID       string                            `json:"scenario_id,omitempty"`
	ProjectID        string                            `json:"project_id,omitempty"`
	FolderID         *string                           `json:"folder_id,omitempty"`
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
	StopOnSLABreach          bool     `json:"stop_on_sla_breach,omitempty"`
	StopOnMaxErrors          bool     `json:"stop_on_max_errors,omitempty"`
	SLABreachWindow          string   `json:"sla_breach_window,omitempty"`
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
	Iterations       *int                     `json:"iterations,omitempty"`
	Pacing           *dsl.PacingConfig        `json:"pacing,omitempty"`
	ThinkTime        *dsl.ThinkTimeConfig     `json:"think_time,omitempty"`
	ErrorHandling    *dsl.ErrorHandlingConfig `json:"error_handling,omitempty"`
	Browser          *RuntimeBrowserOverride  `json:"browser,omitempty"`
	LogLevel         string                   `json:"log_level,omitempty"`
	AutoTransactions string                   `json:"auto_transactions,omitempty"`
	BaseURL          string                   `json:"base_url,omitempty"`
	Env              map[string]string        `json:"env,omitempty"`
	RedactionRules   []engine.RedactionRule   `json:"redaction_rules,omitempty"`
}

type CreateGroupEntry struct {
	Name         string              `json:"name"`
	ScriptID     string              `json:"script_id"`
	VUsers       int                 `json:"vusers"`
	RampUp       string              `json:"ramp_up,omitempty"`
	RampDown     string              `json:"ramp_down,omitempty"`
	Stages       []storage.LoadStage `json:"stages,omitempty"`
	Iterations   *int                `json:"iterations,omitempty"`
	InitStrategy string              `json:"initialization,omitempty"` // "at_runtime", "at_once"
}

type StartTestOptions struct {
	OverrideCapacityShortfall bool `json:"override_capacity_shortfall,omitempty"`
}

type CapacityShortfallError struct {
	TestID   string
	Advisory *CapacityAdvisory
	Message  string
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

func normalizeGeneratorAssignmentSettings(in *GeneratorAssignmentSettings) (*GeneratorAssignmentSettings, error) {
	if in == nil {
		return nil, nil
	}
	out := &GeneratorAssignmentSettings{Mode: strings.TrimSpace(in.Mode)}
	if out.Mode == "" {
		out.Mode = generatorAssignmentModeAuto
	}
	switch out.Mode {
	case generatorAssignmentModeAuto:
		out.SelectedTargets = nil
		out.RegionWeights = nil
		return out, nil
	case generatorAssignmentModeSpecificHosts:
		seen := make(map[string]struct{})
		for _, raw := range in.SelectedTargets {
			target := strings.TrimSpace(raw)
			if target == "" {
				continue
			}
			if _, exists := seen[target]; exists {
				continue
			}
			seen[target] = struct{}{}
			out.SelectedTargets = append(out.SelectedTargets, target)
		}
		if len(out.SelectedTargets) == 0 {
			return nil, fmt.Errorf("assignment.selected_targets must include at least one hostname when mode is specific_hosts")
		}
		out.RegionWeights = nil
		return out, nil
	case generatorAssignmentModeRegionWeighted:
		seen := make(map[string]struct{})
		for region, weight := range in.RegionWeights {
			normalizedRegion := strings.TrimSpace(region)
			if normalizedRegion == "" {
				continue
			}
			if _, exists := seen[normalizedRegion]; exists {
				continue
			}
			seen[normalizedRegion] = struct{}{}
			if weight <= 0 {
				return nil, fmt.Errorf("assignment.region_weights[%q] must be greater than zero", normalizedRegion)
			}
			if out.RegionWeights == nil {
				out.RegionWeights = make(map[string]int, len(in.RegionWeights))
			}
			out.RegionWeights[normalizedRegion] = weight
		}
		if len(out.RegionWeights) == 0 {
			return nil, fmt.Errorf("assignment.region_weights must include at least one region when mode is region_weighted")
		}
		out.SelectedTargets = nil
		return out, nil
	default:
		return nil, fmt.Errorf("assignment.mode must be auto, specific_hosts, or region_weighted")
	}
}

func cloneGeneratorAssignmentSettings(in *GeneratorAssignmentSettings) *GeneratorAssignmentSettings {
	if in == nil {
		return nil
	}
	out := *in
	out.SelectedTargets = append([]string(nil), in.SelectedTargets...)
	if len(in.RegionWeights) > 0 {
		out.RegionWeights = make(map[string]int, len(in.RegionWeights))
		for region, weight := range in.RegionWeights {
			out.RegionWeights[region] = weight
		}
	}
	return &out
}

func buildScenarioFootprint(test *ManagedTest) *ScenarioFootprint {
	if test == nil || test.CapacityAdvisory == nil {
		return nil
	}
	return &ScenarioFootprint{
		TotalVUsers:      test.TotalVUsers,
		Duration:         test.DurationStr,
		ComplexityScore:  test.CapacityAdvisory.ComplexityScore,
		EstimatedCPU:     test.CapacityAdvisory.EstimatedTotal.CPUCores,
		EstimatedMemory:  test.CapacityAdvisory.EstimatedTotal.MemoryMB,
		EstimatedNetwork: test.CapacityAdvisory.EstimatedTotal.NetworkMbps,
	}
}

func pluralSuffix(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
}

func (tr *TestRunner) assignmentTargets() []string {
	provider, ok := tr.execution.(interface{ configuredTargets() []string })
	if !ok {
		return nil
	}
	return provider.configuredTargets()
}

func (tr *TestRunner) assignmentTargetSpecs() []generatorTargetSpec {
	provider, ok := tr.execution.(interface{ configuredTargetSpecs() []generatorTargetSpec })
	if !ok {
		return nil
	}
	return provider.configuredTargetSpecs()
}

func assignmentAvailableRegions(specs []generatorTargetSpec) []string {
	set := make(map[string]struct{})
	for _, spec := range specs {
		if spec.Region == "" {
			continue
		}
		set[spec.Region] = struct{}{}
	}
	regions := make([]string, 0, len(set))
	for region := range set {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return regions
}

func matchingRegionWeightedTargets(specs []generatorTargetSpec, weights map[string]int) int {
	if len(specs) == 0 || len(weights) == 0 {
		return 0
	}
	count := 0
	for _, spec := range specs {
		if spec.Region == "" {
			continue
		}
		if weights[spec.Region] > 0 {
			count++
		}
	}
	return count
}

func missingWeightedRegions(specs []generatorTargetSpec, weights map[string]int) []string {
	if len(weights) == 0 {
		return nil
	}
	available := make(map[string]struct{})
	for _, spec := range specs {
		if spec.Region == "" {
			continue
		}
		available[spec.Region] = struct{}{}
	}
	missing := make([]string, 0)
	for region := range weights {
		if _, ok := available[region]; ok {
			continue
		}
		missing = append(missing, region)
	}
	sort.Strings(missing)
	return missing
}

func formatRegionWeightSummary(weights map[string]int) string {
	if len(weights) == 0 {
		return ""
	}
	total := 0
	regions := make([]string, 0, len(weights))
	for region, weight := range weights {
		if weight <= 0 {
			continue
		}
		total += weight
		regions = append(regions, region)
	}
	if total <= 0 || len(regions) == 0 {
		return ""
	}
	sort.Strings(regions)
	parts := make([]string, 0, len(regions))
	for _, region := range regions {
		parts = append(parts, fmt.Sprintf("%s %d%%", region, int(math.Round(float64(weights[region])*100/float64(total)))))
	}
	return strings.Join(parts, ", ")
}

func adjustCapacityAdvisoryForAssignment(advisory *CapacityAdvisory, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) *CapacityAdvisory {
	if advisory == nil {
		return nil
	}
	assignment, err := normalizeGeneratorAssignmentSettings(assignment)
	if err != nil {
		cloned := cloneCapacityAdvisory(advisory)
		cloned.RequiresOverride = true
		cloned.Warnings = append(cloned.Warnings, err.Error())
		return cloned
	}
	if assignment == nil {
		return advisory
	}
	availableTargets := len(targetSpecs)
	selected := 0
	warning := ""
	switch assignment.Mode {
	case generatorAssignmentModeSpecificHosts:
		selected = len(assignment.SelectedTargets)
		warning = fmt.Sprintf("Selected hosts provide %d generator slot%s, but the scenario footprint recommends %%d.", selected, pluralSuffix(selected))
	case generatorAssignmentModeRegionWeighted:
		selected = matchingRegionWeightedTargets(targetSpecs, assignment.RegionWeights)
		warning = fmt.Sprintf("Configured region weights match %d generator slot%s, but the scenario footprint recommends %%d.", selected, pluralSuffix(selected))
	default:
		return advisory
	}
	if selected == 0 {
		cloned := cloneCapacityAdvisory(advisory)
		cloned.RequiresOverride = true
		if assignment.Mode == generatorAssignmentModeRegionWeighted {
			cloned.Warnings = append(cloned.Warnings, "No configured generators match the requested region weights on this controller.")
		}
		return cloned
	}
	cloned := cloneCapacityAdvisory(advisory)
	if availableTargets > 0 && selected > availableTargets {
		selected = availableTargets
	}
	cloned.AvailableGenerators = selected
	cloned.ConfiguredAvailableGenerators = selected
	cloned.LiveAvailableGenerators = nil
	cloned.ShortfallGenerators = 0
	if cloned.RecommendedGenerators > selected {
		cloned.ShortfallGenerators = cloned.RecommendedGenerators - selected
		cloned.RequiresOverride = true
		cloned.Warnings = append(cloned.Warnings, fmt.Sprintf(warning, cloned.RecommendedGenerators))
		if assignment.Mode == generatorAssignmentModeRegionWeighted {
			if missing := missingWeightedRegions(targetSpecs, assignment.RegionWeights); len(missing) > 0 {
				cloned.Warnings = append(cloned.Warnings, fmt.Sprintf("No connected generator targets are tagged for region%s %s.", pluralSuffix(len(missing)), strings.Join(missing, ", ")))
			}
		}
		return cloned
	}
	cloned.RequiresOverride = false
	if assignment.Mode == generatorAssignmentModeRegionWeighted {
		if missing := missingWeightedRegions(targetSpecs, assignment.RegionWeights); len(missing) > 0 {
			cloned.Warnings = append(cloned.Warnings, fmt.Sprintf("No connected generator targets are tagged for region%s %s.", pluralSuffix(len(missing)), strings.Join(missing, ", ")))
		}
	}
	return cloned
}

func (tr *TestRunner) estimateCapacityAdvisoryForAssignment(projectID string, groups []TestGroup, totalVUsers int, duration time.Duration, resourceBaseline bool, runtimeSettings *RuntimeSettingsOverride, assignment *GeneratorAssignmentSettings) *CapacityAdvisory {
	advisory := estimateCapacityAdvisoryForProject(tr.settings, projectID, groups, totalVUsers, duration, resourceBaseline, runtimeSettings)
	return adjustCapacityAdvisoryForAssignment(advisory, assignment, tr.assignmentTargetSpecs())
}

func (tr *TestRunner) buildAssignmentRecommendation(test *ManagedTest) *AssignmentRecommendation {
	if test == nil || test.CapacityAdvisory == nil {
		return nil
	}
	availableTargets := tr.assignmentTargets()
	availableRegions := assignmentAvailableRegions(tr.assignmentTargetSpecs())
	assignment := cloneGeneratorAssignmentSettings(test.Assignment)
	if assignment == nil {
		assignment = &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto}
	}
	recommendedCount := test.CapacityAdvisory.RecommendedGenerators
	if recommendedCount < 1 {
		recommendedCount = 1
	}
	recommendedTargets := append([]string(nil), availableTargets...)
	if len(recommendedTargets) > recommendedCount {
		recommendedTargets = recommendedTargets[:recommendedCount]
	}
	recommendation := &AssignmentRecommendation{
		Mode:               assignment.Mode,
		AvailableTargets:   availableTargets,
		AvailableRegions:   availableRegions,
		RecommendedTargets: recommendedTargets,
	}
	if assignment.Mode == generatorAssignmentModeSpecificHosts {
		recommendation.Summary = fmt.Sprintf("Manual host selection uses %d configured host%s for this scenario footprint.", len(assignment.SelectedTargets), pluralSuffix(len(assignment.SelectedTargets)))
		if test.CapacityAdvisory.RequiresOverride {
			recommendation.Warnings = append(recommendation.Warnings, test.CapacityAdvisory.Warnings...)
		}
		return recommendation
	}
	if assignment.Mode == generatorAssignmentModeRegionWeighted {
		recommendation.Summary = fmt.Sprintf("Region-weighted assignment will place groups across tagged generators using %s.", formatRegionWeightSummary(assignment.RegionWeights))
		if test.CapacityAdvisory.RequiresOverride {
			recommendation.Warnings = append(recommendation.Warnings, test.CapacityAdvisory.Warnings...)
		}
		return recommendation
	}
	recommendation.Summary = fmt.Sprintf("Auto-assignment recommends %d host%s for the current scenario footprint.", test.CapacityAdvisory.RecommendedGenerators, pluralSuffix(test.CapacityAdvisory.RecommendedGenerators))
	return recommendation
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

func (tr *TestRunner) EstimateDraftCapacity(req CreateTestRequest) (*DraftCapacityEstimate, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	preview, err := tr.buildManagedTestLocked(req, "draft-estimate", "DRAFT")
	if err != nil {
		return nil, err
	}

	return &DraftCapacityEstimate{
		ScenarioMode:          preview.ScenarioMode,
		TotalVUsers:           preview.TotalVUsers,
		Duration:              preview.DurationStr,
		ResourceBaseline:      preview.ResourceBaseline,
		Groups:                preview.Groups,
		Assignment:            cloneGeneratorAssignmentSettings(preview.Assignment),
		Footprint:             buildScenarioFootprint(preview),
		Recommendation:        tr.buildAssignmentRecommendation(preview),
		CapacityAdvisory:      cloneCapacityAdvisory(preview.CapacityAdvisory),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(preview.RuntimeSettingsReview),
	}, nil
}

func (tr *TestRunner) CreateTest(req CreateTestRequest) (*ManagedTest, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, err := tr.buildManagedTestLocked(req, uuid.New().String(), "QUEUED")
	if err != nil {
		return nil, err
	}

	tr.tests[test.ID] = test

	tr.saveManagedTestRecord(test)

	tr.broadcastEvent("test_created", test)
	return test, nil
}

func (tr *TestRunner) buildManagedTestLocked(req CreateTestRequest, id string, status string) (*ManagedTest, error) {
	if req.ScenarioID != "" {
		scenario, ok := tr.scenarios[req.ScenarioID]
		if !ok {
			return nil, fmt.Errorf("scenario not found: %s", req.ScenarioID)
		}
		req = createTestRequestFromScenario(req, scenario)
	}

	dur, err := time.ParseDuration(req.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}

	scenarioMode, err := validateScenarioRequest(req)
	if err != nil {
		return nil, err
	}

	groups, err := normalizeCreateTestGroups(req)
	if err != nil {
		return nil, err
	}

	sharedDataSources, err := tr.buildSharedDataSourcesLocked(req.DataPools)
	if err != nil {
		return nil, err
	}

	testGroups, engineGroups, totalVUsers, err := tr.compileManagedTestGroupsWithDataSourcesLocked(groups, req.RuntimeSettings, sharedDataSources)
	if err != nil {
		return nil, err
	}
	runtimeSettingsReview := buildRuntimeSettingsScenarioReview(testGroups, engineGroups)
	if status != "DRAFT" && runtimeSettingsReview != nil && runtimeSettingsReview.Status == "blocked" {
		return nil, fmt.Errorf("runtime settings blocked: %s", runtimeSettingsReview.Summary)
	}

	testName := deriveManagedTestName(req.Name, testGroups)

	projectID, err := tr.resolveCreateTestProjectID(req.ProjectID, groups)
	if err != nil {
		return nil, err
	}
	effectiveRuntimeSettings, err := tr.resolveEffectiveRuntimeSettings(projectID, req.RuntimeSettings)
	if err != nil {
		return nil, err
	}

	if req.ReleaseID == "" && status != "DRAFT" {
		req.ReleaseID = storage.LegacyReleaseID
	}
	applicationID := req.ApplicationID
	if req.ReleaseID != "" && tr.releaseStore != nil {
		if release, err := tr.releaseStore.GetRelease(req.ReleaseID); err == nil && release != nil {
			applicationID = release.ApplicationID
		}
	}
	if applicationID == "" && req.ReleaseID != "" {
		applicationID = storage.LegacyApplicationID
	}
	test := &ManagedTest{
		ID:                    id,
		Name:                  testName,
		Status:                status,
		ApplicationID:         applicationID,
		ReleaseID:             req.ReleaseID,
		ScenarioID:            req.ScenarioID,
		ProjectID:             projectID,
		FolderID:              req.FolderID,
		Groups:                testGroups,
		Assignment:            cloneGeneratorAssignmentSettings(req.Assignment),
		CapacityAdvisory:      tr.estimateCapacityAdvisoryForAssignment(projectID, testGroups, totalVUsers, dur, req.ResourceBaseline, effectiveRuntimeSettings, req.Assignment),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(runtimeSettingsReview),
		ScenarioMode:          scenarioMode,
		ScriptShakeout:        req.ScriptShakeout,
		ResourceBaseline:      req.ResourceBaseline,
		StartDelay:            req.StartDelay,
		GoalConfig:            cloneGoalConfig(req.GoalConfig),
		RuntimeSettings:       cloneRuntimeSettings(effectiveRuntimeSettings),
		TotalVUsers:           totalVUsers,
		Duration:              dur,
		DurationStr:           req.Duration,
		CreatedAt:             time.Now(),
		groupSpecs:            engineGroups,
	}

	if len(testGroups) == 1 {
		test.ScriptID = testGroups[0].ScriptID
		test.ScriptName = testGroups[0].ScriptName
	}

	return test, nil
}

func createTestRequestFromScenario(req CreateTestRequest, scenario *StoredScenario) CreateTestRequest {
	req.Name = firstNonEmpty(req.Name, scenario.Name)
	req.Duration = firstNonEmpty(req.Duration, scenario.Duration)
	req.ScenarioMode = firstNonEmpty(req.ScenarioMode, scenario.ScenarioMode)
	if !req.ScriptShakeout {
		req.ScriptShakeout = scenario.ScriptShakeout
	}
	if !req.ResourceBaseline {
		req.ResourceBaseline = scenario.ResourceBaseline
	}
	if req.StartDelay == "" {
		req.StartDelay = scenario.StartDelay
	}
	if req.GoalConfig == nil {
		req.GoalConfig = cloneGoalConfig(scenario.GoalConfig)
	}
	if req.RuntimeSettings == nil {
		req.RuntimeSettings = cloneRuntimeSettings(scenario.RuntimeSettings)
	}
	if req.Assignment == nil {
		req.Assignment = cloneGeneratorAssignmentSettings(scenario.Assignment)
	}
	if len(req.DataPools) == 0 {
		req.DataPools = append([]storage.ScenarioDataPoolBinding(nil), scenario.DataPools...)
	}
	if len(req.Groups) == 0 && req.ScriptID == "" {
		req.Groups = make([]CreateGroupEntry, 0, len(scenario.Scripts))
		for _, script := range scenario.Scripts {
			req.Groups = append(req.Groups, CreateGroupEntry{
				Name:         script.Name,
				ScriptID:     script.ScriptID,
				VUsers:       script.VUsers,
				RampUp:       script.RampUp,
				RampDown:     script.RampDown,
				Stages:       cloneLoadStages(script.Stages),
				Iterations:   script.Iterations,
				InitStrategy: script.Initialization,
			})
		}
	}
	return req
}

func firstNonEmpty(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func validateScenarioRequest(req CreateTestRequest) (string, error) {
	scenarioMode := normalizeScenarioMode(req.ScenarioMode)
	if _, err := normalizeGeneratorAssignmentSettings(req.Assignment); err != nil {
		return "", err
	}
	if req.StartDelay != "" {
		if _, err := time.ParseDuration(req.StartDelay); err != nil {
			return "", fmt.Errorf("invalid start_delay: %w", err)
		}
	}
	if scenarioMode != "goal_oriented" {
		return scenarioMode, nil
	}

	if req.GoalConfig == nil || !hasGoalConfig(req.GoalConfig) {
		return "", fmt.Errorf("goal-oriented scenarios require at least one goal or guardrail setting")
	}
	if req.GoalConfig.P95LatencyMs != nil && *req.GoalConfig.P95LatencyMs <= 0 {
		return "", fmt.Errorf("goal-oriented scenarios require p95_latency_ms to be greater than 0")
	}
	if req.GoalConfig.MaxErrorPct != nil && (*req.GoalConfig.MaxErrorPct < 0 || *req.GoalConfig.MaxErrorPct > 100) {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_pct to be between 0 and 100")
	}
	if req.GoalConfig.MaxAvgServerProcessingMs != nil && *req.GoalConfig.MaxAvgServerProcessingMs <= 0 {
		return "", fmt.Errorf("goal-oriented scenarios require max_avg_server_processing_ms to be greater than 0")
	}
	if req.GoalConfig.MaxAvgTransferMs != nil && *req.GoalConfig.MaxAvgTransferMs <= 0 {
		return "", fmt.Errorf("goal-oriented scenarios require max_avg_transfer_ms to be greater than 0")
	}
	if req.GoalConfig.MinVUsers != nil && *req.GoalConfig.MinVUsers < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require min_vusers to be greater than 0")
	}
	if req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MaxVUsers < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require max_vusers to be greater than 0")
	}
	if req.GoalConfig.MinVUsers != nil && req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MinVUsers > *req.GoalConfig.MaxVUsers {
		return "", fmt.Errorf("goal-oriented scenarios require min_vusers to be less than or equal to max_vusers")
	}
	if req.GoalConfig.MaxErrorCount != nil && *req.GoalConfig.MaxErrorCount < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_count to be greater than 0")
	}
	if req.GoalConfig.SLABreachWindow != "" {
		if _, err := time.ParseDuration(req.GoalConfig.SLABreachWindow); err != nil {
			return "", fmt.Errorf("invalid sla_breach_window: %w", err)
		}
	}
	if req.GoalConfig.StopOnSLABreach && req.GoalConfig.P95LatencyMs == nil && req.GoalConfig.MaxErrorPct == nil && req.GoalConfig.MaxAvgServerProcessingMs == nil && req.GoalConfig.MaxAvgTransferMs == nil {
		return "", fmt.Errorf("goal-oriented scenarios require a latency, error, server processing, or transfer threshold when stop_on_sla_breach is enabled")
	}
	if req.GoalConfig.StopOnMaxErrors && req.GoalConfig.MaxErrorCount == nil {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_count when stop_on_max_errors is enabled")
	}
	return scenarioMode, nil
}

func normalizeCreateTestGroups(req CreateTestRequest) ([]CreateGroupEntry, error) {
	if len(req.Groups) > 0 {
		return req.Groups, nil
	}
	if req.ScriptID != "" {
		return []CreateGroupEntry{{
			ScriptID: req.ScriptID,
			VUsers:   req.VUsers,
			RampUp:   req.RampUp,
		}}, nil
	}
	return nil, fmt.Errorf("at least one group (or script_id) is required")
}

func (tr *TestRunner) compileManagedTestGroupsLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride) ([]TestGroup, []engine.GroupConfig, int, error) {
	return tr.compileManagedTestGroupsWithDataSourcesLocked(groups, runtimeSettings, nil)
}

func (tr *TestRunner) compileManagedTestGroupsWithDataSourcesLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource) ([]TestGroup, []engine.GroupConfig, int, error) {
	testGroups := make([]TestGroup, 0, len(groups))
	engineGroups := make([]engine.GroupConfig, 0, len(groups))
	totalVUsers := 0

	for i, group := range groups {
		testGroup, engineGroup, err := tr.compileManagedTestGroupLocked(i, len(groups), group, runtimeSettings, sharedDataSources)
		if err != nil {
			return nil, nil, 0, err
		}
		testGroups = append(testGroups, testGroup)
		engineGroups = append(engineGroups, engineGroup)
		totalVUsers += testGroup.VUsers
	}

	return testGroups, engineGroups, totalVUsers, nil
}

func (tr *TestRunner) compileManagedTestGroupLocked(index int, totalGroups int, group CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource) (TestGroup, engine.GroupConfig, error) {
	script, ok := tr.scripts[group.ScriptID]
	if !ok {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: script not found: %s", index, group.ScriptID)
	}

	vusers := group.VUsers
	derivedRampUp, derivedRampDown, derivedVUsers, err := loadStageCompatibility(group.Stages, index)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	if vusers <= 0 && derivedVUsers > 0 {
		vusers = derivedVUsers
	}
	if vusers <= 0 {
		vusers = 10
	}
	rampUpValue := firstNonEmpty(group.RampUp, derivedRampUp)
	rampDownValue := firstNonEmpty(group.RampDown, derivedRampDown)
	if len(group.Stages) > 0 && derivedVUsers > 0 && vusers != derivedVUsers {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: vusers must match the maximum target_vusers from stages (%d)", index, derivedVUsers)
	}
	if group.Iterations != nil && *group.Iterations < 1 {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: iterations must be greater than 0", index)
	}

	rampUp, err := parseOptionalGroupDuration(rampUpValue, index, "ramp_up")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	rampDown, err := parseOptionalGroupDuration(rampDownValue, index, "ramp_down")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}

	name := group.Name
	if name == "" {
		name = script.Name
		if totalGroups > 1 {
			name = fmt.Sprintf("%s (#%d)", script.Name, index+1)
		}
	}

	initStrategy := strings.ToLower(strings.TrimSpace(group.InitStrategy))
	if initStrategy == "" {
		initStrategy = "at_runtime"
	}
	switch initStrategy {
	case "at_runtime", "at_once":
	default:
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: initialization must be at_runtime or at_once", index)
	}

	testGroup := TestGroup{
		Name:           name,
		ScriptID:       group.ScriptID,
		ScriptName:     script.Name,
		Protocol:       script.Protocol,
		VUsers:         vusers,
		RampUp:         rampUpValue,
		RampDown:       rampDownValue,
		Stages:         cloneLoadStages(group.Stages),
		Iterations:     group.Iterations,
		RunLogicGoals:  runLogicGoalSnapshot(script.Script),
		Initialization: initStrategy,
		rampUpDur:      rampUp,
		script:         script.Script,
	}

	scriptCopy := *script.Script
	if len(sharedDataSources) > 0 {
		scriptCopy.DataSources = append(cloneDataSources(scriptCopy.DataSources), sharedDataSources...)
	}
	effectiveRuntimeSettings := cloneRuntimeSettings(runtimeSettings)
	if group.Iterations != nil {
		if effectiveRuntimeSettings == nil {
			effectiveRuntimeSettings = &RuntimeSettingsOverride{}
		}
		iterations := *group.Iterations
		effectiveRuntimeSettings.Iterations = &iterations
	}
	if effectiveRuntimeSettings != nil {
		if err := applyRuntimeOverrides(&scriptCopy, effectiveRuntimeSettings); err != nil {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: apply runtime overrides: %w", index, err)
		}
		if issues := dsl.ValidateScriptIssues(&scriptCopy); len(issues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
		}
	}
	effectiveRuntimeSettings, err = tr.resolveEffectiveRuntimeSettings(script.ProjectID, effectiveRuntimeSettings)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: resolve redaction rules: %w", index, err)
	}
	redactionConfig, err := buildRuntimeRedactionConfig(effectiveRuntimeSettings)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: build redaction config: %w", index, err)
	}

	engineGroup := engine.GroupConfig{
		Name:            name,
		Script:          &scriptCopy,
		VUsers:          vusers,
		RampUp:          rampUp,
		RampDown:        rampDown,
		InitStrategy:    initStrategy,
		RedactionConfig: redactionConfig,
	}

	return testGroup, engineGroup, nil
}

func (tr *TestRunner) buildSharedDataSourcesLocked(bindings []storage.ScenarioDataPoolBinding) ([]dsl.DataSource, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if tr.dataPoolStore == nil {
		return nil, fmt.Errorf("data pool storage is not configured")
	}
	sources := make([]dsl.DataSource, 0, len(bindings))
	for _, binding := range bindings {
		alias := strings.TrimSpace(binding.Alias)
		if alias == "" {
			return nil, fmt.Errorf("data pool binding alias is required")
		}
		rows, err := tr.dataPoolStore.ListDataPoolRows(binding.PoolID)
		if err != nil {
			return nil, fmt.Errorf("load data pool %q: %w", alias, err)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("data pool %q has no rows", alias)
		}
		inlineData, columns, err := inlineCSVFromDataPoolRows(rows)
		if err != nil {
			return nil, fmt.Errorf("prepare data pool %q: %w", alias, err)
		}
		strategy := binding.SelectionStrategy
		if strategy == "" {
			strategy = "sequential"
		}
		sources = append(sources, dsl.DataSource{
			Name:           alias,
			Type:           "csv",
			InlineData:     inlineData,
			Strategy:       strategy,
			FirstRowHeader: true,
			Columns:        columns,
		})
	}
	return sources, nil
}

func inlineCSVFromDataPoolRows(rows []*storage.SharedDataPoolRow) (string, []string, error) {
	columnSet := map[string]struct{}{}
	for _, row := range rows {
		for key := range row.Data {
			key = strings.TrimSpace(key)
			if key != "" {
				columnSet[key] = struct{}{}
			}
		}
	}
	if len(columnSet) == 0 {
		return "", nil, fmt.Errorf("data pool rows must include at least one column")
	}
	columns := make([]string, 0, len(columnSet))
	for key := range columnSet {
		columns = append(columns, key)
	}
	sort.Strings(columns)

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(columns); err != nil {
		return "", nil, err
	}
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, column := range columns {
			values[i] = row.Data[column]
		}
		if err := writer.Write(values); err != nil {
			return "", nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", nil, err
	}
	return buf.String(), columns, nil
}

func cloneDataSources(sources []dsl.DataSource) []dsl.DataSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]dsl.DataSource, len(sources))
	copy(out, sources)
	return out
}

func parseOptionalGroupDuration(value string, index int, fieldName string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("group[%d]: invalid %s: %w", index, fieldName, err)
	}
	return duration, nil
}

func loadStageCompatibility(stages []storage.LoadStage, groupIndex int) (string, string, int, error) {
	if len(stages) == 0 {
		return "", "", 0, nil
	}
	var rampUp time.Duration
	var rampDown time.Duration
	maxVUsers := 0
	for index, stage := range stages {
		stageType := strings.ToLower(strings.TrimSpace(stage.Type))
		if stageType == "" {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: type is required", groupIndex, index)
		}
		if stageType != "ramp_up" && stageType != "hold" && stageType != "ramp_down" {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: type must be ramp_up, hold, or ramp_down", groupIndex, index)
		}
		duration, err := time.ParseDuration(stage.Duration)
		if err != nil {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: duration is invalid: %w", groupIndex, index, err)
		}
		if duration <= 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: duration must be greater than zero", groupIndex, index)
		}
		if stage.TargetVUsers < 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_vusers must be zero or greater", groupIndex, index)
		}
		if stage.TargetTPS != nil && *stage.TargetTPS <= 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_tps must be greater than zero", groupIndex, index)
		}
		if stage.TargetVUsers == 0 && stage.TargetTPS == nil {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_vusers or target_tps is required", groupIndex, index)
		}
		if stage.TargetVUsers > maxVUsers {
			maxVUsers = stage.TargetVUsers
		}
		switch stageType {
		case "ramp_up":
			rampUp += duration
		case "ramp_down":
			rampDown += duration
		}
	}
	return durationStringOrEmpty(rampUp), durationStringOrEmpty(rampDown), maxVUsers, nil
}

func durationStringOrEmpty(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.String()
}

func cloneLoadStages(stages []storage.LoadStage) []storage.LoadStage {
	if len(stages) == 0 {
		return nil
	}
	out := make([]storage.LoadStage, len(stages))
	copy(out, stages)
	for index := range out {
		if stages[index].TargetTPS != nil {
			value := *stages[index].TargetTPS
			out[index].TargetTPS = &value
		}
	}
	return out
}

func deriveManagedTestName(requestName string, groups []TestGroup) string {
	if requestName != "" {
		return requestName
	}
	if len(groups) == 1 {
		return groups[0].ScriptName
	}
	return fmt.Sprintf("Multi-group (%d groups)", len(groups))
}

func (tr *TestRunner) GetTest(id string) (*ManagedTest, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
	t, ok := tr.tests[id]
	if ok && t.Status == "QUEUED" {
		tr.refreshCapacityAdvisoryLocked(t)
	}
	if ok && t.Status == "PENDING" {
		tr.refreshCapacityAdvisoryLocked(t)
	}
	if !ok {
		return nil, false
	}
	cloned := *t
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(t)
	return &cloned, true
}

func (tr *TestRunner) buildRunComparisonBaselineLocked(test *ManagedTest) *RunComparisonBaseline {
	if test == nil {
		return nil
	}
	referenceTime := test.CreatedAt
	if test.StartedAt != nil && !test.StartedAt.IsZero() {
		referenceTime = *test.StartedAt
	}
	var pinned *ManagedTest
	var pinnedMarkedAt time.Time
	var pinnedCompletedAt time.Time
	var latestSuccessful *ManagedTest
	var latestSuccessfulCompletedAt time.Time
	var latestFinished *ManagedTest
	var latestFinishedCompletedAt time.Time
	for _, candidate := range tr.tests {
		if !isComparableBaselineCandidate(test, candidate) {
			continue
		}
		completedAt, ok := baselineCandidateCompletedAt(candidate)
		if !ok || (!referenceTime.IsZero() && !completedAt.Before(referenceTime)) {
			continue
		}
		if candidate.BaselinePinned {
			markedAt := completedAt
			if candidate.BaselinePinnedAt != nil && !candidate.BaselinePinnedAt.IsZero() {
				markedAt = *candidate.BaselinePinnedAt
			}
			if pinned == nil || markedAt.After(pinnedMarkedAt) || (markedAt.Equal(pinnedMarkedAt) && completedAt.After(pinnedCompletedAt)) {
				pinned = candidate
				pinnedMarkedAt = markedAt
				pinnedCompletedAt = completedAt
			}
			continue
		}
		if candidate.Status == "COMPLETE" {
			if latestSuccessful == nil || completedAt.After(latestSuccessfulCompletedAt) {
				latestSuccessful = candidate
				latestSuccessfulCompletedAt = completedAt
			}
			continue
		}
		if latestFinished == nil || completedAt.After(latestFinishedCompletedAt) {
			latestFinished = candidate
			latestFinishedCompletedAt = completedAt
		}
	}
	policy := "latest_successful"
	selected := latestSuccessful
	selectedCompletedAt := latestSuccessfulCompletedAt
	if pinned != nil {
		selected = pinned
		selectedCompletedAt = pinnedCompletedAt
		policy = "pinned"
	} else if selected == nil {
		selected = latestFinished
		selectedCompletedAt = latestFinishedCompletedAt
		policy = "latest_finished"
	}
	if selected == nil {
		return nil
	}
	metrics := latestMetricsWithComparableBreakdown(selected)
	if metrics == nil {
		return nil
	}
	return &RunComparisonBaseline{
		SourceTestID:     selected.ID,
		SourceTestName:   selected.Name,
		Policy:           policy,
		CompletedAt:      selectedCompletedAt,
		Transactions:     cloneTxMetricsMap(metrics.Transactions),
		DependencyHealth: cloneTxMetricsMap(metrics.DependencyHealth),
	}
}

func isComparableBaselineCandidate(current *ManagedTest, candidate *ManagedTest) bool {
	if current == nil || candidate == nil || current.ID == candidate.ID {
		return false
	}
	switch candidate.Status {
	case "COMPLETE", "FAILED", "ABORTED":
	default:
		return false
	}
	if current.ScenarioID != "" || candidate.ScenarioID != "" {
		return current.ScenarioID != "" && current.ScenarioID == candidate.ScenarioID
	}
	if current.ReleaseID != "" || candidate.ReleaseID != "" {
		return current.ReleaseID != "" && current.ReleaseID == candidate.ReleaseID && current.Name == candidate.Name
	}
	if current.ApplicationID != "" || candidate.ApplicationID != "" {
		return current.ApplicationID != "" && current.ApplicationID == candidate.ApplicationID && current.Name == candidate.Name
	}
	return current.Name != "" && current.Name == candidate.Name
}

func sharesComparisonScope(left *ManagedTest, right *ManagedTest) bool {
	if left == nil || right == nil {
		return false
	}
	if left.ScenarioID != "" || right.ScenarioID != "" {
		return left.ScenarioID != "" && left.ScenarioID == right.ScenarioID
	}
	if left.ReleaseID != "" || right.ReleaseID != "" {
		return left.ReleaseID != "" && left.ReleaseID == right.ReleaseID && left.Name == right.Name
	}
	if left.ApplicationID != "" || right.ApplicationID != "" {
		return left.ApplicationID != "" && left.ApplicationID == right.ApplicationID && left.Name == right.Name
	}
	return left.Name != "" && left.Name == right.Name
}

func canPinComparisonBaseline(test *ManagedTest) bool {
	if test == nil {
		return false
	}
	switch test.Status {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

func (tr *TestRunner) PinComparisonBaseline(id string) (*ManagedTest, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if !canPinComparisonBaseline(test) {
		return nil, fmt.Errorf("comparison baseline pinning requires a completed, failed, or aborted test")
	}
	pinnedAt := time.Now().UTC()
	for _, candidate := range tr.tests {
		if !sharesComparisonScope(test, candidate) {
			continue
		}
		if candidate.ID == test.ID {
			candidate.BaselinePinned = true
			candidate.BaselinePinnedAt = &pinnedAt
		} else if candidate.BaselinePinned || candidate.BaselinePinnedAt != nil {
			candidate.BaselinePinned = false
			candidate.BaselinePinnedAt = nil
		}
		tr.saveManagedTestRecord(candidate)
	}
	cloned := *test
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
	return &cloned, nil
}

func (tr *TestRunner) UnpinComparisonBaseline(id string) (*ManagedTest, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	test.BaselinePinned = false
	test.BaselinePinnedAt = nil
	tr.saveManagedTestRecord(test)
	cloned := *test
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
	return &cloned, nil
}

func baselineCandidateCompletedAt(candidate *ManagedTest) (time.Time, bool) {
	if candidate == nil {
		return time.Time{}, false
	}
	if candidate.EndedAt != nil && !candidate.EndedAt.IsZero() {
		return *candidate.EndedAt, true
	}
	for index := len(candidate.History) - 1; index >= 0; index-- {
		if candidate.History[index] != nil && !candidate.History[index].Timestamp.IsZero() {
			return candidate.History[index].Timestamp, true
		}
	}
	return time.Time{}, false
}

func latestMetricsWithComparableBreakdown(test *ManagedTest) *LiveMetrics {
	if test == nil {
		return nil
	}
	if test.LiveMetrics != nil && (len(test.LiveMetrics.Transactions) > 0 || len(test.LiveMetrics.DependencyHealth) > 0) {
		return test.LiveMetrics
	}
	for index := len(test.History) - 1; index >= 0; index-- {
		if test.History[index] != nil && (len(test.History[index].Transactions) > 0 || len(test.History[index].DependencyHealth) > 0) {
			return test.History[index]
		}
	}
	return nil
}

func cloneTxMetricsMap(in map[string]*TxMetrics) map[string]*TxMetrics {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*TxMetrics, len(in))
	for key, tx := range in {
		if tx == nil {
			continue
		}
		clone := *tx
		if tx.ServerTelemetry != nil {
			telemetry := *tx.ServerTelemetry
			telemetry.RequestSizeClasses = cloneTelemetryClassCounts(tx.ServerTelemetry.RequestSizeClasses)
			telemetry.ResponseSizeClasses = cloneTelemetryClassCounts(tx.ServerTelemetry.ResponseSizeClasses)
			telemetry.RequestPayloadHistogram = cloneTelemetryClassCounts(tx.ServerTelemetry.RequestPayloadHistogram)
			telemetry.ResponsePayloadHistogram = cloneTelemetryClassCounts(tx.ServerTelemetry.ResponsePayloadHistogram)
			clone.ServerTelemetry = &telemetry
		}
		if len(tx.CorrelationIDs) > 0 {
			clone.CorrelationIDs = append([]string(nil), tx.CorrelationIDs...)
		}
		out[key] = &clone
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (tr *TestRunner) ListTests() []*ManagedTest {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		if t.Status == "PENDING" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) ListTestsFiltered(projectID string, folderID *string) []*ManagedTest {
	projectID = normalizeProjectID(projectID)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if t.ProjectID != projectID {
			continue
		}
		if folderID != nil {
			if *folderID == "" {
				if t.FolderID != nil && *t.FolderID != "" {
					continue
				}
			} else if t.FolderID == nil || *t.FolderID != *folderID {
				continue
			}
		}
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		if t.Status == "PENDING" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) ListTestsByContext(applicationID string, releaseID string, scenarioID string) []*ManagedTest {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if applicationID != "" && t.ApplicationID != applicationID {
			continue
		}
		if releaseID != "" && t.ReleaseID != releaseID {
			continue
		}
		if scenarioID != "" && t.ScenarioID != scenarioID {
			continue
		}
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		if t.Status == "PENDING" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) MoveTest(id string, folderID *string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test, ok := tr.tests[id]
	if !ok {
		return false
	}
	test.FolderID = folderID
	if tr.testStore != nil {
		_ = tr.testStore.MoveTest(id, folderID)
	}
	return true
}

func (tr *TestRunner) GetRunArchive(id string) (*storage.RunArchiveRecord, bool) {
	tr.mu.RLock()
	record, ok := tr.archives[id]
	tr.mu.RUnlock()
	if ok {
		return record, true
	}
	record, ok = tr.loadRunArchiveFromStore(id)
	if !ok {
		return nil, false
	}
	if !runArchiveRecordAvailable(record) {
		tr.mu.Lock()
		if test, exists := tr.tests[id]; exists {
			test.Archive = nil
		}
		tr.mu.Unlock()
		return nil, false
	}
	tr.mu.Lock()
	tr.archives[id] = record
	if test, exists := tr.tests[id]; exists {
		test.Archive = record
	}
	tr.mu.Unlock()
	return record, true
}

func runArchiveRecordAvailable(record *storage.RunArchiveRecord) bool {
	if record == nil || record.ManifestPath == "" {
		return false
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		return false
	}
	_, err := os.Stat(record.ManifestPath)
	return err == nil
}

func (tr *TestRunner) GetRunArchiveManifest(id string) (*RunArchiveManifest, error) {
	record, ok := tr.GetRunArchive(id)
	if !ok {
		return nil, fmt.Errorf("archive not found: %s", id)
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, err
	}
	return loadRunArchiveManifest(record.ManifestPath)
}

func (tr *TestRunner) DeleteTest(id string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test, ok := tr.tests[id]
	if !ok {
		return false
	}
	if test.Status == "RUNNING" || test.Status == "INITIALIZING" || test.Status == "PAUSED" || test.Status == "STOPPING" {
		return false // Can't delete active tests
	}
	archivePath := ""
	if archive, exists := tr.archives[id]; exists {
		archivePath = archive.ArchivePath
	} else if test.Archive != nil {
		archivePath = test.Archive.ArchivePath
	}
	delete(tr.tests, id)
	delete(tr.archives, id)
	tr.deleteStoredTest(id)
	if archivePath != "" {
		if err := os.RemoveAll(archivePath); err != nil {
			slog.Warn("failed to remove archive directory", "test_id", id, "archive_path", archivePath, "error", err)
		}
	}
	return true
}

func (tr *TestRunner) StartTest(id string) error {
	return tr.StartTestWithOptions(id, StartTestOptions{})
}

func (tr *TestRunner) StartTestWithOptions(id string, opts StartTestOptions) error {
	tr.mu.Lock()
	test, err := tr.prepareTestStartLocked(id, opts)
	tr.mu.Unlock()
	if err != nil {
		return err
	}
	if test == nil {
		return nil
	}

	go tr.runTest(test)
	return nil
}

func (tr *TestRunner) prepareTestStartLocked(id string, opts StartTestOptions) (*ManagedTest, error) {
	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "QUEUED" && test.Status != "PENDING" {
		return nil, fmt.Errorf("test is %s, must be QUEUED or PENDING to start", test.Status)
	}

	tr.refreshCapacityAdvisoryLocked(test)
	if err := buildCapacityShortfallStartError(id, test.CapacityAdvisory, opts); err != nil {
		return nil, err
	}
	if blocked, reason := tr.evaluateQuotaBlockLocked(test, ""); blocked {
		now := time.Now()
		test.Status = "PENDING"
		if test.PendingSince == nil || test.PendingSince.IsZero() {
			test.PendingSince = &now
		}
		test.PendingReason = reason
		test.QueuePosition = 0
		test.Error = ""
		tr.saveManagedTestRecord(test)
		tr.broadcastEvent("test_pending", test)
		return nil, nil
	}

	test.Status = "INITIALIZING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	test.executionBackend = tr.execution
	if test.executionBackend == nil {
		test.executionBackend = localExecutionBackend{}
	}
	tr.saveManagedTestRecord(test)
	return test, nil
}

func (tr *TestRunner) countActiveTestsLocked() int {
	active := 0
	for _, test := range tr.tests {
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			active++
		}
	}
	return active
}

func (tr *TestRunner) countActiveTestsForProjectLocked(projectID string) int {
	projectID = normalizeProjectID(projectID)
	active := 0
	for _, test := range tr.tests {
		if normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			active++
		}
	}
	return active
}

func (tr *TestRunner) countActiveVUsersLocked() int {
	total := 0
	for _, test := range tr.tests {
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			total += test.TotalVUsers
		}
	}
	return total
}

func (tr *TestRunner) countActiveVUsersForProjectLocked(projectID string) int {
	projectID = normalizeProjectID(projectID)
	total := 0
	for _, test := range tr.tests {
		if normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			total += test.TotalVUsers
		}
	}
	return total
}

func (tr *TestRunner) validateConcurrencyLimitsLocked(projectID string) error {
	maxConcurrent := tr.settings.General.MaxConcurrentTests
	if tr.countActiveTestsLocked() >= maxConcurrent {
		return fmt.Errorf("max concurrent tests (%d) reached", maxConcurrent)
	}
	projectMaxConcurrent := tr.projectMaxConcurrentTests(projectID)
	if projectMaxConcurrent > 0 && tr.countActiveTestsForProjectLocked(projectID) >= projectMaxConcurrent {
		return fmt.Errorf("project max concurrent tests (%d) reached", projectMaxConcurrent)
	}
	return nil
}

func (tr *TestRunner) evaluateQuotaBlockLocked(test *ManagedTest, excludeTestID string) (bool, string) {
	if test == nil {
		return false, ""
	}
	globalActiveTests := tr.countActiveTestsLocked()
	projectActiveTests := tr.countActiveTestsForProjectLocked(test.ProjectID)
	globalActiveVUsers := tr.countActiveVUsersLocked()
	projectActiveVUsers := tr.countActiveVUsersForProjectLocked(test.ProjectID)
	if excludeTestID != "" {
		if existing, ok := tr.tests[excludeTestID]; ok && (existing.Status == "RUNNING" || existing.Status == "INITIALIZING") {
			globalActiveTests--
			projectActiveTests--
			globalActiveVUsers -= existing.TotalVUsers
			projectActiveVUsers -= existing.TotalVUsers
		}
	}
	maxConcurrent := tr.settings.General.MaxConcurrentTests
	if globalActiveTests >= maxConcurrent {
		return true, fmt.Sprintf("waiting for a global test slot (%d max concurrent tests)", maxConcurrent)
	}
	projectMaxConcurrent := tr.projectMaxConcurrentTests(test.ProjectID)
	if projectMaxConcurrent > 0 && projectActiveTests >= projectMaxConcurrent {
		return true, fmt.Sprintf("waiting for a project test slot (%d max concurrent tests)", projectMaxConcurrent)
	}
	projectMaxVUsers := tr.projectMaxVUsers(test.ProjectID)
	if projectMaxVUsers > 0 && projectActiveVUsers+test.TotalVUsers > projectMaxVUsers {
		return true, fmt.Sprintf("waiting for project VUser capacity (%d max vusers)", projectMaxVUsers)
	}
	return false, ""
}

func (tr *TestRunner) projectMaxConcurrentTests(projectID string) int {
	if tr.projectStore == nil {
		return 0
	}
	projectID = normalizeProjectID(projectID)
	project, err := tr.projectStore.GetProject(projectID)
	if err != nil || project == nil {
		return 0
	}
	if project.Settings.MaxConcurrentTests < 1 {
		return 0
	}
	return project.Settings.MaxConcurrentTests
}

func (tr *TestRunner) projectMaxVUsers(projectID string) int {
	if tr.projectStore == nil {
		return 0
	}
	projectID = normalizeProjectID(projectID)
	project, err := tr.projectStore.GetProject(projectID)
	if err != nil || project == nil {
		return 0
	}
	if project.Settings.MaxVUsers < 1 {
		return 0
	}
	return project.Settings.MaxVUsers
}

func pendingAnchor(test *ManagedTest) time.Time {
	if test != nil && test.PendingSince != nil && !test.PendingSince.IsZero() {
		return *test.PendingSince
	}
	if test != nil {
		return test.CreatedAt
	}
	return time.Time{}
}

type pendingFairShareRank struct {
	reservationTier int
	activeTests     int
	activeVUsers    int
	anchor          time.Time
	createdAt       time.Time
	id              string
}

func (tr *TestRunner) sortPendingTestsForFairShareLocked(pending []*ManagedTest) {
	sort.Slice(pending, func(i, j int) bool {
		left := tr.pendingFairShareRankLocked(pending[i])
		right := tr.pendingFairShareRankLocked(pending[j])
		if left.reservationTier != right.reservationTier {
			return left.reservationTier < right.reservationTier
		}
		if left.activeTests != right.activeTests {
			return left.activeTests < right.activeTests
		}
		if left.activeVUsers != right.activeVUsers {
			return left.activeVUsers < right.activeVUsers
		}
		if !left.anchor.Equal(right.anchor) {
			return left.anchor.Before(right.anchor)
		}
		if !left.createdAt.Equal(right.createdAt) {
			return left.createdAt.Before(right.createdAt)
		}
		return left.id < right.id
	})
}

func (tr *TestRunner) pendingFairShareRankLocked(test *ManagedTest) pendingFairShareRank {
	if test == nil {
		return pendingFairShareRank{reservationTier: 1, id: ""}
	}
	projectID := normalizeProjectID(test.ProjectID)
	reservationTier := 1
	_, projectReserved := capacityReservationTotals(tr.settings, projectID)
	if projectReserved > 0 && tr.countActiveRecommendedGeneratorsForProjectLocked(projectID) < projectReserved {
		reservationTier = 0
	}
	return pendingFairShareRank{
		reservationTier: reservationTier,
		activeTests:     tr.countActiveTestsForProjectLocked(projectID),
		activeVUsers:    tr.countActiveVUsersForProjectLocked(projectID),
		anchor:          pendingAnchor(test),
		createdAt:       test.CreatedAt,
		id:              test.ID,
	}
}

func (tr *TestRunner) countActiveRecommendedGeneratorsForProjectLocked(projectID string) int {
	projectID = normalizeProjectID(projectID)
	total := 0
	for _, test := range tr.tests {
		if test == nil || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if test.Status != "RUNNING" && test.Status != "INITIALIZING" && test.Status != "PAUSED" {
			continue
		}
		total += recommendedGeneratorCountForTest(test)
	}
	return total
}

func recommendedGeneratorCountForTest(test *ManagedTest) int {
	if test == nil {
		return 0
	}
	if test.CapacityAdvisory != nil && test.CapacityAdvisory.RecommendedGenerators > 0 {
		return test.CapacityAdvisory.RecommendedGenerators
	}
	if test.TotalVUsers > 0 {
		return 1
	}
	return 0
}

func runLogicGoalSnapshot(script *dsl.Script) *RunLogicGoalSnapshot {
	if script == nil {
		return nil
	}
	goals := RunLogicGoalSnapshot{
		GlobalIterations: script.Config.RunLogic.GlobalIterations,
		MaxTransactions:  script.Config.RunLogic.MaxTransactions,
	}
	if goals.GlobalIterations <= 0 && goals.MaxTransactions <= 0 {
		return nil
	}
	return &goals
}

func (tr *TestRunner) refreshPendingQueueMetadataLocked() {
	pending := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if test.Status != "PENDING" {
			test.PendingReason = ""
			test.QueuePosition = 0
			continue
		}
		if test.PendingSince == nil || test.PendingSince.IsZero() {
			pendingSince := test.CreatedAt
			test.PendingSince = &pendingSince
		}
		pending = append(pending, test)
	}
	tr.sortPendingTestsForFairShareLocked(pending)
	for index, test := range pending {
		test.QueuePosition = index + 1
		blocked, reason := tr.evaluateQuotaBlockLocked(test, "")
		if blocked {
			test.PendingReason = reason
		} else {
			test.PendingReason = "ready to start when the queue advances"
		}
	}
}

func buildCapacityShortfallStartError(id string, advisory *CapacityAdvisory, opts StartTestOptions) error {
	if advisory == nil || !advisory.RequiresOverride || opts.OverrideCapacityShortfall {
		return nil
	}
	cloned := cloneCapacityAdvisory(advisory)
	return &CapacityShortfallError{
		TestID:   id,
		Advisory: cloned,
		Message:  fmt.Sprintf("capacity advisory recommends %d load generators but only %d are currently available", cloned.RecommendedGenerators, cloned.AvailableGenerators),
	}
}

func (tr *TestRunner) refreshCapacityAdvisoryLocked(test *ManagedTest) {
	if test == nil {
		return
	}
	groups := tr.hydrateCapacityGroupsLocked(test.Groups)
	test.CapacityAdvisory = tr.estimateCapacityAdvisoryForAssignment(test.ProjectID, groups, test.TotalVUsers, test.Duration, test.ResourceBaseline, test.RuntimeSettings, test.Assignment)
}

func (tr *TestRunner) loadRunArchiveFromStore(id string) (*storage.RunArchiveRecord, bool) {
	if tr.archiveStore == nil {
		return nil, false
	}
	record, err := tr.archiveStore.GetRunArchive(id)
	if err != nil {
		return nil, false
	}
	return record, true
}

func (tr *TestRunner) saveStoredScript(script *dsl.Script) {
	if tr.scriptStore != nil {
		tr.scriptStore.SaveScript(script)
	}
}

func (tr *TestRunner) deleteStoredScript(id string) {
	if tr.scriptStore != nil {
		tr.scriptStore.DeleteScript(id)
	}
}

func buildManagedTestRecord(test *ManagedTest) *storage.TestRecord {
	if test == nil {
		return nil
	}
	return &storage.TestRecord{
		ID:               test.ID,
		Name:             test.Name,
		Status:           test.Status,
		ApplicationID:    test.ApplicationID,
		ReleaseID:        test.ReleaseID,
		ScenarioID:       test.ScenarioID,
		ProjectID:        normalizeProjectID(test.ProjectID),
		FolderID:         test.FolderID,
		ScenarioMode:     test.ScenarioMode,
		ScriptShakeout:   test.ScriptShakeout,
		ResourceBaseline: test.ResourceBaseline,
		StartDelay:       test.StartDelay,
		Assignment:       test.Assignment,
		TotalVUsers:      test.TotalVUsers,
		Duration:         int64(test.Duration),
		StartTime:        test.CreatedAt,
		PendingSince:     test.PendingSince,
		BaselinePinned:   test.BaselinePinned,
		BaselinePinnedAt: test.BaselinePinnedAt,
		Groups:           test.Groups,
		GoalConfig:       test.GoalConfig,
		RuntimeSettings:  test.RuntimeSettings,
	}
}

func (tr *TestRunner) ReconcilePendingTests() {
	tr.promotePendingTests()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
}

func normalizeProjectID(projectID string) string {
	if projectID == "" {
		return storage.DefaultProjectID
	}
	return projectID
}

func (tr *TestRunner) saveManagedTestRecord(test *ManagedTest) {
	if tr.testStore != nil {
		tr.testStore.SaveTest(buildManagedTestRecord(test))
	}
}

func (tr *TestRunner) deleteStoredTest(id string) {
	if tr.testStore != nil {
		tr.testStore.DeleteTest(id)
	}
}

func (tr *TestRunner) saveLiveMetricsHistory(testID string, timestamp time.Time, live *LiveMetrics) {
	if tr.historyStore != nil {
		tr.historyStore.SaveHistory(testID, timestamp, live)
	}
}

func (tr *TestRunner) updateStoredTestStatus(testID string, status string, endTime *time.Time, errStr string) {
	if tr.testStore != nil {
		tr.testStore.UpdateTestStatus(testID, status, endTime, errStr)
	}
}

func (tr *TestRunner) saveRunArchiveRecord(record *storage.RunArchiveRecord) error {
	if tr.archiveStore == nil || record == nil {
		return nil
	}
	return tr.archiveStore.SaveRunArchive(record)
}

func (tr *TestRunner) hydrateCapacityGroupsLocked(groups []TestGroup) []TestGroup {
	if len(groups) == 0 {
		return nil
	}
	hydrated := make([]TestGroup, len(groups))
	for i := range groups {
		hydrated[i] = groups[i]
		if hydrated[i].script != nil {
			continue
		}
		if script, ok := tr.scripts[hydrated[i].ScriptID]; ok {
			hydrated[i].script = script.Script
			if hydrated[i].Protocol == "" {
				hydrated[i].Protocol = script.Protocol
			}
			if hydrated[i].ScriptName == "" {
				hydrated[i].ScriptName = script.Name
			}
		}
	}
	return hydrated
}

func (tr *TestRunner) StopTest(id string) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.multiEngine == nil {
		backend := test.executionBackend
		if backend == nil {
			backend = tr.execution
		}
		if stoppable, ok := backend.(stoppableExecutionBackend); ok {
			if err := stoppable.StopTest(tr, test); err != nil {
				return err
			}
		} else if test.cancel == nil {
			return fmt.Errorf("test backend does not support stop")
		}
	}
	if test.cancel != nil {
		test.cancel()
	}
	if test.Status == "COMPLETE" || test.Status == "FAILED" {
		return nil
	}
	test.Status = "STOPPING"
	tr.broadcastEvent("test_stopping", test)
	return nil
}

func (tr *TestRunner) AbortTest(id string) error {
	return tr.StopTest(id)
}

func (tr *TestRunner) ScaleVUsers(id string, groupName string, targetVUsers int) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to scale", test.Status)
	}
	if targetVUsers < 0 {
		return fmt.Errorf("target vusers must be non-negative")
	}
	if test.multiEngine == nil {
		backend := test.executionBackend
		if backend == nil {
			backend = tr.execution
		}
		scalable, ok := backend.(scalableExecutionBackend)
		if !ok {
			return fmt.Errorf("test backend does not support scaling vusers")
		}
		if err := scalable.ScaleVUsers(tr, test, groupName, targetVUsers); err != nil {
			return err
		}
	} else {
		if err := test.multiEngine.ScaleVUsers(groupName, targetVUsers); err != nil {
			return err
		}
	}

	// Update the test's group metadata
	tr.mu.Lock()
	totalVUsers := 0
	for i := range test.Groups {
		if test.Groups[i].Name == groupName {
			test.Groups[i].VUsers = targetVUsers
		}
		totalVUsers += test.Groups[i].VUsers
	}
	test.TotalVUsers = totalVUsers
	tr.mu.Unlock()

	tr.hub.BroadcastJSON(map[string]any{
		"type":          "vusers_changed",
		"test_id":       id,
		"project_id":    normalizeProjectID(test.ProjectID),
		"group":         groupName,
		"target_vusers": targetVUsers,
		"total_vusers":  totalVUsers,
		"timestamp":     time.Now(),
	})

	return nil
}

func (tr *TestRunner) AdjustDuration(id string, newDuration time.Duration) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to adjust duration", test.Status)
	}
	if test.multiEngine == nil {
		backend := test.executionBackend
		if backend == nil {
			backend = tr.execution
		}
		adjustable, ok := backend.(durationAdjustableExecutionBackend)
		if !ok {
			return fmt.Errorf("test backend does not support duration changes")
		}
		if err := adjustable.AdjustDuration(tr, test, newDuration); err != nil {
			return err
		}
	} else {
		if err := test.multiEngine.AdjustDuration(newDuration); err != nil {
			return err
		}
	}

	tr.mu.Lock()
	test.Duration = newDuration
	test.DurationStr = newDuration.String()
	tr.mu.Unlock()

	tr.hub.BroadcastJSON(map[string]any{
		"type":         "duration_changed",
		"test_id":      id,
		"project_id":   normalizeProjectID(test.ProjectID),
		"new_duration": newDuration.String(),
		"timestamp":    time.Now(),
	})

	return nil
}

func (tr *TestRunner) PauseTest(id string) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to pause", test.Status)
	}
	if test.multiEngine == nil {
		backend := test.executionBackend
		if backend == nil {
			backend = tr.execution
		}
		pausable, ok := backend.(pausableExecutionBackend)
		if !ok {
			return fmt.Errorf("test backend does not support pause")
		}
		if err := pausable.PauseTest(tr, test); err != nil {
			return err
		}
	} else {
		if err := test.multiEngine.Pause(); err != nil {
			return err
		}
	}

	tr.mu.Lock()
	test.Status = "PAUSED"
	tr.mu.Unlock()

	tr.broadcastEvent("test_paused", test)

	return nil
}

func (tr *TestRunner) ResumeTest(id string) error {
	tr.mu.RLock()
	test, ok := tr.tests[id]
	tr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "PAUSED" {
		return fmt.Errorf("test is %s, must be PAUSED to resume", test.Status)
	}
	if test.multiEngine == nil {
		backend := test.executionBackend
		if backend == nil {
			backend = tr.execution
		}
		pausable, ok := backend.(pausableExecutionBackend)
		if !ok {
			return fmt.Errorf("test backend does not support resume")
		}
		if err := pausable.ResumeTest(tr, test); err != nil {
			return err
		}
	} else {
		if err := test.multiEngine.Resume(); err != nil {
			return err
		}
	}

	tr.mu.Lock()
	test.Status = "RUNNING"
	tr.mu.Unlock()

	tr.broadcastEvent("test_resumed", test)

	return nil
}

func (tr *TestRunner) runTest(test *ManagedTest) {
	backend := test.executionBackend
	if backend == nil {
		backend = tr.execution
	}
	if backend == nil {
		backend = localExecutionBackend{}
	}
	backend.Run(tr, test)
}

func (tr *TestRunner) buildRunEngine(test *ManagedTest, resultSink engine.ResultSink) (*engine.MultiGroupEngine, error) {
	preBaseline, postBaseline := resourceBaselineWindows(test.ResourceBaseline)
	preBaseline += startDelayWindow(test.StartDelay)
	return engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:       cloneGroupSpecsWithResultSink(test.groupSpecs, resultSink),
		Duration:     test.Duration,
		PreBaseline:  preBaseline,
		PostBaseline: postBaseline,
	})
}

func cloneGroupSpecsWithResultSink(groupSpecs []engine.GroupConfig, resultSink engine.ResultSink) []engine.GroupConfig {
	cloned := make([]engine.GroupConfig, len(groupSpecs))
	copy(cloned, groupSpecs)
	for i := range cloned {
		cloned[i].ResultSink = resultSink
	}
	return cloned
}

func resourceBaselineWindows(enabled bool) (time.Duration, time.Duration) {
	if !enabled {
		return 0, 0
	}
	const baselineWindow = 5 * time.Minute
	return baselineWindow, baselineWindow
}

func startDelayWindow(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0
	}
	return duration
}

func buildLiveMetricsFromSnapshot(snap engine.MetricsSnapshot, groupVUsers map[string]int) *LiveMetrics {
	live := &LiveMetrics{
		Timestamp:              snap.Timestamp,
		ActiveVUsers:           snap.ActiveVUsers,
		TPS:                    snap.TPS,
		TargetTPS:              snap.TargetTPS,
		ThroughputGapPct:       snap.ThroughputGapPct,
		ThroughputTargetMissed: snap.ThroughputTargetMissed,
		AvgResponseMs:          float64(snap.AvgDuration.Microseconds()) / 1000.0,
		TotalTx:                snap.Transactions,
		TotalErrors:            snap.Errors,
		P50Ms:                  float64(snap.P50.Microseconds()) / 1000.0,
		P90Ms:                  float64(snap.P90.Microseconds()) / 1000.0,
		P95Ms:                  float64(snap.P95.Microseconds()) / 1000.0,
		P99Ms:                  float64(snap.P99.Microseconds()) / 1000.0,
		Transactions:           make(map[string]*TxMetrics),
		DependencyHealth:       make(map[string]*TxMetrics),
		SOAPOperations:         make(map[string]*TxMetrics),
		GroupMetrics:           make(map[string]*GroupLiveMetrics),
		NetworkProfiles:        make(map[string]*TxMetrics),
		Resources:              snap.Resources,
	}
	if snap.Browser != nil {
		live.Browser = &BrowserLiveMetrics{
			Samples:              snap.Browser.Samples,
			AvgFCPMs:             snap.Browser.AvgFCPMs,
			AvgLCPMs:             snap.Browser.AvgLCPMs,
			AvgCLSScore:          snap.Browser.AvgCLSScore,
			AvgINPMs:             snap.Browser.AvgINPMs,
			AvgBlockingResources: snap.Browser.AvgBlockingResources,
			AvgLongTaskCount:     snap.Browser.AvgLongTaskCount,
			AvgJSHeapUsedMB:      snap.Browser.AvgJSHeapUsedMB,
			AvgBrowserProcessCPU: snap.Browser.AvgBrowserProcessCPU,
			AvgBrowserProcessMem: snap.Browser.AvgBrowserProcessMem,
		}
	}
	if len(snap.BrowserByDevice) > 0 {
		live.BrowserByDevice = make(map[string]*BrowserDeviceLiveMetrics, len(snap.BrowserByDevice))
		for name, metrics := range snap.BrowserByDevice {
			live.BrowserByDevice[name] = &BrowserDeviceLiveMetrics{
				DeviceProfile:        metrics.DeviceProfile,
				DevicePlatform:       metrics.DevicePlatform,
				DeviceTier:           metrics.DeviceTier,
				BrowserEngine:        metrics.BrowserEngine,
				Samples:              metrics.Samples,
				AvgFCPMs:             metrics.AvgFCPMs,
				AvgLCPMs:             metrics.AvgLCPMs,
				AvgCLSScore:          metrics.AvgCLSScore,
				AvgINPMs:             metrics.AvgINPMs,
				AvgBlockingResources: metrics.AvgBlockingResources,
				AvgLongTaskCount:     metrics.AvgLongTaskCount,
				AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
				AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
				AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
			}
		}
	}
	if snap.Telemetry != nil {
		live.Telemetry = &TelemetryHealth{
			DroppedResults: snap.Telemetry.DroppedResults,
			CollectedAt:    snap.Timestamp,
		}
	}

	if live.TotalTx > 0 {
		live.ErrorPct = float64(live.TotalErrors) / float64(live.TotalTx) * 100
	}

	for name, am := range snap.ByAction {
		live.Transactions[name] = buildTxMetricsFromAction(name, am)
	}

	for name, am := range snap.ByDependencyTarget {
		live.DependencyHealth[name] = buildTxMetricsFromAction(name, am)
	}
	live.DependencyMap = buildLiveDependencyMap(live.DependencyHealth)

	for name, am := range snap.BySOAPOperation {
		live.SOAPOperations[name] = buildTxMetricsFromAction(name, am)
	}

	for name, am := range snap.ByNetworkProfile {
		live.NetworkProfiles[name] = buildTxMetricsFromAction(name, am)
	}

	for name, gm := range snap.ByGroup {
		live.GroupMetrics[name] = &GroupLiveMetrics{
			Name:                   name,
			VUsers:                 groupVUsers[name],
			Transactions:           gm.Transactions,
			Errors:                 gm.Errors,
			TPS:                    gm.TPS,
			TargetTPS:              gm.TargetTPS,
			ThroughputGapPct:       gm.ThroughputGapPct,
			ThroughputTargetMissed: gm.ThroughputTargetMissed,
			AvgResponseMs:          float64(gm.AvgDuration.Microseconds()) / 1000.0,
			P50Ms:                  float64(gm.P50.Microseconds()) / 1000.0,
			P90Ms:                  float64(gm.P90.Microseconds()) / 1000.0,
			P99Ms:                  float64(gm.P99.Microseconds()) / 1000.0,
		}
	}

	return live
}

func buildLiveDependencyMap(dependencies map[string]*TxMetrics) *LiveDependencyMap {
	if len(dependencies) == 0 {
		return nil
	}
	targets := make([]string, 0, len(dependencies))
	for target, tx := range dependencies {
		if tx == nil {
			continue
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil
	}
	sort.Strings(targets)
	nodes := []LiveDependencyNode{{
		ID:     "stormrunner",
		Label:  "Stormrunner VUsers",
		Kind:   "source",
		Status: "healthy",
	}}
	edges := make([]LiveDependencyEdge, 0, len(targets))
	for _, target := range targets {
		tx := dependencies[target]
		nodeID := "dependency:" + target
		status := dependencyHealthStatus(tx)
		errorPct := txErrorPct(tx)
		nodes = append(nodes, LiveDependencyNode{
			ID:             nodeID,
			Label:          target,
			Kind:           "dependency",
			DependencyType: firstNonEmpty(tx.DependencyType, "unknown"),
			Status:         status,
			Transactions:   tx.Count,
			Errors:         tx.Errors,
			ErrorPct:       errorPct,
			P95Ms:          tx.P95Ms,
		})
		edges = append(edges, LiveDependencyEdge{
			Source:         "stormrunner",
			Target:         nodeID,
			DependencyType: firstNonEmpty(tx.DependencyType, "unknown"),
			Transactions:   tx.Count,
			Errors:         tx.Errors,
			ErrorPct:       errorPct,
			P95Ms:          tx.P95Ms,
		})
	}
	return &LiveDependencyMap{Nodes: nodes, Edges: edges}
}

func dependencyHealthStatus(tx *TxMetrics) string {
	if tx == nil {
		return "unknown"
	}
	if tx.Errors > 0 {
		return "danger"
	}
	if tx.P95Ms >= slowTransactionDiagnosticThresholdMs {
		return "warning"
	}
	return "healthy"
}

func txErrorPct(tx *TxMetrics) float64 {
	if tx == nil || tx.Count <= 0 {
		return 0
	}
	return float64(tx.Errors) / float64(tx.Count) * 100
}

func updateLiveErrorPatterns(existing map[string]*LiveErrorPattern, live *LiveMetrics, testID string) map[string]*LiveErrorPattern {
	if existing == nil {
		existing = make(map[string]*LiveErrorPattern)
	}
	if live == nil || len(live.Transactions) == 0 {
		return existing
	}

	affectedGroups := groupsWithErrors(live.GroupMetrics)
	actions := make([]string, 0, len(live.Transactions))
	for action, tx := range live.Transactions {
		if tx == nil || tx.Errors <= 0 {
			continue
		}
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		tx := live.Transactions[action]
		source := liveErrorPatternSource(tx)
		signature := normalizeArchiveErrorSignature(source)
		observedAt := tx.LastErrorAt
		if observedAt.IsZero() {
			observedAt = live.Timestamp
		}
		pattern, ok := existing[signature]
		if !ok {
			pattern = &LiveErrorPattern{
				Signature: signature,
				Sample:    source,
				FirstSeen: observedAt,
			}
			existing[signature] = pattern
		}
		pattern.Count += tx.Errors
		pattern.LatestSeen = observedAt
		if pattern.FirstSeen.IsZero() || observedAt.Before(pattern.FirstSeen) {
			pattern.FirstSeen = observedAt
		}
		if pattern.StatusCode == 0 {
			pattern.StatusCode = tx.LastStatusCode
		}
		pattern.ErrorPct = txErrorPct(tx)
		pattern.Actions = appendUniqueSorted(pattern.Actions, action)
		pattern.AffectedGroups = mergeStringSets(pattern.AffectedGroups, affectedGroups)
		pattern.SnapshotURL = liveErrorPatternSnapshotURL(testID, pattern.Actions)
	}
	return existing
}

func liveErrorPatternSource(tx *TxMetrics) string {
	if tx == nil {
		return "transaction failed"
	}
	if message := strings.TrimSpace(tx.LastErrorMessage); message != "" {
		return message
	}
	if tx.LastStatusCode > 0 {
		return fmt.Sprintf("request failed with status %d", tx.LastStatusCode)
	}
	return "transaction failed"
}

func groupsWithErrors(groups map[string]*GroupLiveMetrics) []string {
	if len(groups) == 0 {
		return nil
	}
	result := make([]string, 0, len(groups))
	for name, group := range groups {
		if group != nil && group.Errors > 0 {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func appendUniqueSorted(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	sort.Strings(values)
	return values
}

func mergeStringSets(left []string, right []string) []string {
	for _, value := range right {
		left = appendUniqueSorted(left, value)
	}
	return left
}

func liveErrorPatternSnapshotURL(testID string, actions []string) string {
	if strings.TrimSpace(testID) == "" {
		return ""
	}
	query := url.Values{}
	query.Set("success", "false")
	query.Set("limit", "20")
	if len(actions) == 1 {
		query.Set("action", actions[0])
	}
	return fmt.Sprintf("/api/v1/tests/%s/archive/transactions?%s", url.PathEscape(testID), query.Encode())
}

func liveErrorPatternsList(patterns map[string]*LiveErrorPattern) []LiveErrorPattern {
	if len(patterns) == 0 {
		return nil
	}
	result := make([]LiveErrorPattern, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == nil {
			continue
		}
		clone := *pattern
		clone.Actions = append([]string(nil), pattern.Actions...)
		clone.AffectedGroups = append([]string(nil), pattern.AffectedGroups...)
		result = append(result, clone)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Count == result[right].Count {
			return result[left].FirstSeen.Before(result[right].FirstSeen)
		}
		return result[left].Count > result[right].Count
	})
	return result
}

func buildTxMetricsFromAction(name string, am *engine.ActionMetrics) *TxMetrics {
	if am == nil {
		return nil
	}
	return &TxMetrics{
		Name:             name,
		DependencyType:   am.DependencyType,
		DependencyTarget: am.DependencyTarget,
		LastErrorMessage: am.LastErrorMessage,
		LastStatusCode:   am.LastStatusCode,
		LastErrorAt:      am.LastErrorAt,
		ServerTelemetry:  cloneEngineServerTelemetrySummary(am.ServerTelemetry),
		AvgDNSMs:         am.AvgDNSMs,
		AvgConnectMs:     am.AvgConnectMs,
		AvgTLSMs:         am.AvgTLSMs,
		AvgTTFBMs:        am.AvgTTFBMs,
		CorrelationIDs:   append([]string(nil), am.CorrelationIDs...),
		CorrelationCount: am.CorrelationCount,
		Count:            am.Count,
		Errors:           am.Errors,
		AvgMs:            float64(am.AvgDuration.Microseconds()) / 1000.0,
		P50Ms:            float64(am.P50.Microseconds()) / 1000.0,
		P90Ms:            float64(am.P90.Microseconds()) / 1000.0,
		P95Ms:            float64(am.P95.Microseconds()) / 1000.0,
		P99Ms:            float64(am.P99.Microseconds()) / 1000.0,
	}
}

func buildLiveMetricsForBroadcast(snap engine.MetricsSnapshot, groupVUsers map[string]int, now time.Time) *LiveMetrics {
	live := buildLiveMetricsFromSnapshot(snap, groupVUsers)
	live.Timestamp = now
	if live.Telemetry == nil {
		live.Telemetry = &TelemetryHealth{}
	}
	if live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.CollectedAt = snap.Timestamp
	}
	live.Telemetry.PublishedAt = now
	if !live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.SampleAgeMs = float64(now.Sub(live.Telemetry.CollectedAt).Microseconds()) / 1000.0
	}
	return live
}

func buildTelemetryDiagnostic(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings) (*TelemetryDiagnostic, string) {
	if live == nil {
		return nil, "healthy"
	}
	thresholds = normalizeThresholdSettings(thresholds)

	conditions := make([]string, 0, 5)
	summaries := make([]string, 0, 5)
	signatureParts := make([]string, 0, 8)
	severity := "healthy"

	if live.Telemetry != nil {
		if live.Telemetry.DroppedResults > 0 {
			conditions = append(conditions, "collector_drops")
			summaries = append(summaries, fmt.Sprintf("collector dropped %d metric samples", live.Telemetry.DroppedResults))
			signatureParts = append(signatureParts, "collector_drops")
			severity = maxDiagnosticSeverity(severity, "danger")
		}
		if live.Telemetry.WebsocketDroppedMessages > 0 || live.Telemetry.WebsocketDroppedEvents > 0 {
			conditions = append(conditions, "websocket_drops")
			summaries = append(summaries, fmt.Sprintf("websocket dropped %d client messages and %d broadcast events", live.Telemetry.WebsocketDroppedMessages, live.Telemetry.WebsocketDroppedEvents))
			signatureParts = append(signatureParts, "websocket_drops")
			severity = maxDiagnosticSeverity(severity, "danger")
		}
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "stale_sample", "sample age", live.Telemetry.SampleAgeMs, thresholds.MaxTelemetrySampleAgeMs)
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "generator_lag", "generator processing lag", live.Telemetry.GeneratorProcessingLagMs, thresholds.MaxTelemetryGeneratorLagMs)
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "transport_lag", "transport lag", live.Telemetry.TransportLagMs, thresholds.MaxTelemetryTransportLagMs)
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "controller_fanout_lag", "controller fan-out lag", live.Telemetry.ControllerFanoutLagMs, thresholds.MaxTelemetryControllerFanoutMs)
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "browser_render_lag", "browser render lag", live.Telemetry.BrowserRenderLagMs, thresholds.MaxTelemetryBrowserRenderLagMs)
	}

	samples, sampleConditions, sampleSummaries, sampleSeverity, sampleSignature := sampleTransactionDiagnostics(test, live)
	if len(sampleConditions) > 0 {
		conditions = append(conditions, sampleConditions...)
		summaries = append(summaries, sampleSummaries...)
		signatureParts = append(signatureParts, sampleSignature...)
		severity = maxDiagnosticSeverity(severity, sampleSeverity)
	}

	jmsSamples, jmsConditions, jmsSummaries, jmsSeverity, jmsSignature := sampleJMSBrokerDiagnostics(live)
	if len(jmsConditions) > 0 {
		samples = append(samples, jmsSamples...)
		conditions = append(conditions, jmsConditions...)
		summaries = append(summaries, jmsSummaries...)
		signatureParts = append(signatureParts, jmsSignature...)
		severity = maxDiagnosticSeverity(severity, jmsSeverity)
	}

	dlqSamples, dlqConditions, dlqSummaries, dlqSeverity, dlqSignature := sampleJMSDeadLetterDiagnostics(live)
	if len(dlqConditions) > 0 {
		samples = append(samples, dlqSamples...)
		conditions = append(conditions, dlqConditions...)
		summaries = append(summaries, dlqSummaries...)
		signatureParts = append(signatureParts, dlqSignature...)
		severity = maxDiagnosticSeverity(severity, dlqSeverity)
	}

	if len(conditions) == 0 {
		return &TelemetryDiagnostic{
			Severity:  "healthy",
			Summary:   "live telemetry healthy",
			Samples:   nil,
			Telemetry: live.Telemetry,
		}, "healthy"
	}

	signature := strings.Join(signatureParts, "|")
	return &TelemetryDiagnostic{
		Severity:   severity,
		Summary:    strings.Join(summaries, "; "),
		Conditions: conditions,
		Samples:    samples,
		Telemetry:  live.Telemetry,
	}, signature
}

func appendTelemetryBudgetDiagnostic(conditions *[]string, summaries *[]string, signatureParts *[]string, severity *string, condition string, label string, value float64, threshold *float64) {
	if threshold == nil || value <= 0 || value <= *threshold {
		return
	}
	*conditions = append(*conditions, condition)
	*summaries = append(*summaries, fmt.Sprintf("%s %.0f ms exceeds the %.0f ms telemetry budget", label, value, *threshold))
	*signatureParts = append(*signatureParts, condition)
	*severity = maxDiagnosticSeverity(*severity, "warning")
}

func sampleTransactionDiagnostics(test *ManagedTest, live *LiveMetrics) ([]DiagnosticSample, []string, []string, string, []string) {
	if live == nil || len(live.Transactions) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	latencyThreshold := slowTransactionDiagnosticThresholdMs
	if test != nil && test.GoalConfig != nil && test.GoalConfig.P95LatencyMs != nil && *test.GoalConfig.P95LatencyMs > 0 {
		latencyThreshold = *test.GoalConfig.P95LatencyMs
	}

	type candidate struct {
		sample DiagnosticSample
		rank   int
		key    string
	}

	candidates := make([]candidate, 0, len(live.Transactions))
	hasErrors := false
	hasSlow := false
	for name, tx := range live.Transactions {
		if tx == nil {
			continue
		}
		errorPct := 0.0
		if tx.Count > 0 {
			errorPct = float64(tx.Errors) / float64(tx.Count) * 100
		}
		issue := ""
		rank := 0
		if tx.Errors > 0 {
			issue = "error"
			rank = 2
			hasErrors = true
		}
		if tx.P99Ms >= latencyThreshold || tx.AvgMs >= latencyThreshold {
			if issue == "" {
				issue = "slow"
				rank = 1
			}
			hasSlow = true
		}
		if issue == "" {
			continue
		}
		candidates = append(candidates, candidate{
			sample: DiagnosticSample{
				Issue:            issue,
				Transaction:      name,
				DependencyType:   tx.DependencyType,
				DependencyTarget: tx.DependencyTarget,
				CorrelationIDs:   append([]string(nil), tx.CorrelationIDs...),
				Count:            tx.Count,
				Errors:           tx.Errors,
				ErrorPct:         errorPct,
				AvgMs:            tx.AvgMs,
				P99Ms:            tx.P99Ms,
			},
			rank: rank,
			key:  name,
		})
	}

	if len(candidates) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.rank != right.rank {
			return left.rank > right.rank
		}
		if left.sample.Errors != right.sample.Errors {
			return left.sample.Errors > right.sample.Errors
		}
		if left.sample.P99Ms != right.sample.P99Ms {
			return left.sample.P99Ms > right.sample.P99Ms
		}
		if left.sample.AvgMs != right.sample.AvgMs {
			return left.sample.AvgMs > right.sample.AvgMs
		}
		return strings.Compare(left.key, right.key) < 0
	})

	limit := maxTelemetryDiagnosticSamples
	if len(candidates) < limit {
		limit = len(candidates)
	}
	samples := make([]DiagnosticSample, 0, limit)
	signatureParts := make([]string, 0, limit+2)
	for _, candidate := range candidates[:limit] {
		samples = append(samples, candidate.sample)
		signatureParts = append(signatureParts, fmt.Sprintf("%s:%s:%d:%.0f", candidate.sample.Issue, candidate.sample.Transaction, candidate.sample.Errors, candidate.sample.P99Ms))
	}

	conditions := make([]string, 0, 2)
	summaries := make([]string, 0, 2)
	severity := "healthy"
	if hasErrors {
		conditions = append(conditions, "error_transactions")
		summaries = append(summaries, fmt.Sprintf("sampled %d transaction error hotspots", countSamplesByIssue(samples, "error")))
		signatureParts = append(signatureParts, "condition:error_transactions")
		severity = maxDiagnosticSeverity(severity, "danger")
	}
	if hasSlow {
		conditions = append(conditions, "slow_transactions")
		summaries = append(summaries, fmt.Sprintf("sampled %d slow transaction hotspots at or above %.0f ms", countSlowSamples(samples, latencyThreshold), latencyThreshold))
		signatureParts = append(signatureParts, fmt.Sprintf("condition:slow_transactions:%.0f", latencyThreshold))
		severity = maxDiagnosticSeverity(severity, "warning")
	}

	return samples, conditions, summaries, severity, signatureParts
}

func countSamplesByIssue(samples []DiagnosticSample, issue string) int {
	count := 0
	for _, sample := range samples {
		if sample.Issue == issue {
			count++
		}
	}
	return count
}

func countSlowSamples(samples []DiagnosticSample, latencyThreshold float64) int {
	count := 0
	for _, sample := range samples {
		if sample.Issue == "slow" || sample.P99Ms >= latencyThreshold || sample.AvgMs >= latencyThreshold {
			count++
		}
	}
	return count
}

func sampleJMSBrokerDiagnostics(live *LiveMetrics) ([]DiagnosticSample, []string, []string, string, []string) {
	if live == nil || len(live.Transactions) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	type candidate struct {
		sample DiagnosticSample
		key    string
	}

	candidates := make([]candidate, 0, len(live.Transactions))
	for name, tx := range live.Transactions {
		if tx == nil || !strings.HasPrefix(tx.DependencyType, "jms_") || tx.ServerTelemetry == nil {
			continue
		}
		telemetry := tx.ServerTelemetry
		if telemetry.AvgConsumerLagMs < jmsBrokerPressureMinLagMs {
			continue
		}
		baselineLatency := telemetry.AvgMessageLatencyMs
		if baselineLatency <= 0 {
			baselineLatency = telemetry.AvgTransferMs
		}
		if baselineLatency <= 0 {
			baselineLatency = jmsBrokerPressureMinLagMs / jmsBrokerPressureLagMultiplier
		}
		if telemetry.AvgConsumerLagMs < baselineLatency*jmsBrokerPressureLagMultiplier {
			continue
		}
		sendRate := telemetry.AvgMessagesSentPerSec
		recvRate := telemetry.AvgMessagesReceivedPerSec
		if recvRate > 0 && sendRate < recvRate*jmsBrokerPressureSendRecvRatio {
			continue
		}
		if sendRate == 0 && recvRate == 0 && telemetry.MessagesSent == 0 && telemetry.MessagesReceived == 0 {
			continue
		}
		candidates = append(candidates, candidate{
			sample: DiagnosticSample{
				Issue:                     "broker",
				Transaction:               name,
				DependencyType:            tx.DependencyType,
				DependencyTarget:          tx.DependencyTarget,
				CorrelationIDs:            append([]string(nil), tx.CorrelationIDs...),
				Count:                     tx.Count,
				AvgMs:                     tx.AvgMs,
				P99Ms:                     tx.P99Ms,
				AvgMessageLatencyMs:       telemetry.AvgMessageLatencyMs,
				AvgConsumerLagMs:          telemetry.AvgConsumerLagMs,
				AvgMessagesSentPerSec:     sendRate,
				AvgMessagesReceivedPerSec: recvRate,
			},
			key: name,
		})
	}

	if len(candidates) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sample.AvgConsumerLagMs != candidates[j].sample.AvgConsumerLagMs {
			return candidates[i].sample.AvgConsumerLagMs > candidates[j].sample.AvgConsumerLagMs
		}
		if candidates[i].sample.AvgMessagesSentPerSec != candidates[j].sample.AvgMessagesSentPerSec {
			return candidates[i].sample.AvgMessagesSentPerSec > candidates[j].sample.AvgMessagesSentPerSec
		}
		return strings.Compare(candidates[i].key, candidates[j].key) < 0
	})

	limit := maxTelemetryDiagnosticSamples
	if len(candidates) < limit {
		limit = len(candidates)
	}
	samples := make([]DiagnosticSample, 0, limit)
	signatureParts := make([]string, 0, limit+1)
	for _, candidate := range candidates[:limit] {
		samples = append(samples, candidate.sample)
		signatureParts = append(signatureParts, fmt.Sprintf("broker:%s:%.0f:%.1f:%.1f", candidate.sample.Transaction, candidate.sample.AvgConsumerLagMs, candidate.sample.AvgMessagesSentPerSec, candidate.sample.AvgMessagesReceivedPerSec))
	}

	conditions := []string{"jms_broker_pressure"}
	summaries := []string{fmt.Sprintf("sampled %d JMS broker pressure hotspots where consumer lag materially exceeds request-reply latency", len(samples))}
	signatureParts = append(signatureParts, "condition:jms_broker_pressure")
	return samples, conditions, summaries, "warning", signatureParts
}

func sampleJMSDeadLetterDiagnostics(live *LiveMetrics) ([]DiagnosticSample, []string, []string, string, []string) {
	if live == nil || len(live.Transactions) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	type candidate struct {
		sample DiagnosticSample
		key    string
	}

	candidates := make([]candidate, 0, len(live.Transactions))
	for name, tx := range live.Transactions {
		if tx == nil || !strings.HasPrefix(tx.DependencyType, "jms_dead_letter_") || tx.Count == 0 {
			continue
		}
		candidates = append(candidates, candidate{
			sample: DiagnosticSample{
				Issue:            "dead_letter",
				Transaction:      name,
				DependencyType:   tx.DependencyType,
				DependencyTarget: tx.DependencyTarget,
				CorrelationIDs:   append([]string(nil), tx.CorrelationIDs...),
				Count:            tx.Count,
				Errors:           tx.Errors,
				AvgMs:            tx.AvgMs,
				P99Ms:            tx.P99Ms,
			},
			key: name,
		})
	}

	if len(candidates) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sample.Count != candidates[j].sample.Count {
			return candidates[i].sample.Count > candidates[j].sample.Count
		}
		if candidates[i].sample.Errors != candidates[j].sample.Errors {
			return candidates[i].sample.Errors > candidates[j].sample.Errors
		}
		return strings.Compare(candidates[i].key, candidates[j].key) < 0
	})

	limit := maxTelemetryDiagnosticSamples
	if len(candidates) < limit {
		limit = len(candidates)
	}
	samples := make([]DiagnosticSample, 0, limit)
	signatureParts := make([]string, 0, limit+1)
	for _, candidate := range candidates[:limit] {
		samples = append(samples, candidate.sample)
		signatureParts = append(signatureParts, fmt.Sprintf("dead_letter:%s:%d:%d", candidate.sample.Transaction, candidate.sample.Count, candidate.sample.Errors))
	}

	conditions := []string{"jms_dead_letter_activity"}
	summaries := []string{fmt.Sprintf("observed %d JMS dead-letter destinations with live traffic", len(samples))}
	signatureParts = append(signatureParts, "condition:jms_dead_letter_activity")
	return samples, conditions, summaries, "warning", signatureParts
}

func maxDiagnosticSeverity(current string, next string) string {
	if diagnosticSeverityRank(next) > diagnosticSeverityRank(current) {
		return next
	}
	return current
}

func diagnosticSeverityRank(severity string) int {
	switch severity {
	case "danger":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

func shouldPublishTelemetryDiagnostic(state *telemetryDiagnosticState, signature string, now time.Time) bool {
	if state == nil {
		return signature != "healthy"
	}
	if signature == "healthy" {
		if state.LastSignature == "" || state.LastSignature == "healthy" {
			return false
		}
		state.LastSignature = "healthy"
		state.LastPublishedAt = now
		return true
	}
	if signature != state.LastSignature || state.LastPublishedAt.IsZero() || now.Sub(state.LastPublishedAt) >= telemetryDiagnosticSampleInterval {
		state.LastSignature = signature
		state.LastPublishedAt = now
		return true
	}
	return false
}

func updateTelemetryRunHealth(state *telemetryDiagnosticState, diagnostic *TelemetryDiagnostic, signature string, now time.Time) *TelemetryRunHealth {
	if state == nil {
		return nil
	}
	if diagnostic == nil {
		diagnostic = &TelemetryDiagnostic{Severity: "healthy", Summary: "live telemetry healthy"}
		signature = "healthy"
	}
	if signature == "" {
		signature = "healthy"
	}

	status := "healthy"
	sustained := false
	breachActive := false
	breachDurationSec := 0
	if diagnostic.Severity != "" && diagnostic.Severity != "healthy" {
		breachActive = true
		if state.BreachStartedAt == nil {
			startedAt := now
			state.BreachStartedAt = &startedAt
		}
		breachDurationSec = int(now.Sub(*state.BreachStartedAt).Seconds())
		if diagnostic.Severity == "danger" || now.Sub(*state.BreachStartedAt) >= telemetrySustainedBreachWindow {
			status = "breached"
			sustained = diagnostic.Severity != "danger"
		} else {
			status = "degraded"
		}
	} else {
		state.BreachStartedAt = nil
	}

	lastTransitionAt := now
	if state.Health != nil && state.Health.Status == status && !state.Health.LastTransitionAt.IsZero() {
		lastTransitionAt = state.Health.LastTransitionAt
	}
	health := &TelemetryRunHealth{
		Status:            status,
		Severity:          normalizedTelemetrySeverity(diagnostic.Severity),
		Summary:           diagnostic.Summary,
		CurrentSignature:  signature,
		Conditions:        append([]string(nil), diagnostic.Conditions...),
		BreachActive:      breachActive,
		SustainedBreach:   sustained,
		BreachDurationSec: breachDurationSec,
		LastObservedAt:    now,
		LastTransitionAt:  lastTransitionAt,
	}
	if state.BreachStartedAt != nil {
		startedAt := *state.BreachStartedAt
		health.BreachStartedAt = &startedAt
	}
	state.Health = health
	return cloneTelemetryRunHealth(health)
}

func normalizedTelemetrySeverity(severity string) string {
	switch severity {
	case "danger", "warning":
		return severity
	default:
		return "healthy"
	}
}

func cloneTelemetryRunHealth(in *TelemetryRunHealth) *TelemetryRunHealth {
	if in == nil {
		return nil
	}
	out := *in
	if in.Conditions != nil {
		out.Conditions = append([]string(nil), in.Conditions...)
	}
	if in.BreachStartedAt != nil {
		startedAt := *in.BreachStartedAt
		out.BreachStartedAt = &startedAt
	}
	return &out
}

func cloneTelemetryHealth(in *TelemetryHealth) *TelemetryHealth {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func buildThresholdAlerts(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings) []map[string]any {
	alerts := make([]map[string]any, 0, 5)
	if thresholds.MaxP99LatencyMs != nil && live.P99Ms > *thresholds.MaxP99LatencyMs {
		alerts = append(alerts, map[string]any{
			"type":      "threshold_alert",
			"test_id":   test.ID,
			"test_name": test.Name,
			"timestamp": live.Timestamp,
			"metric":    "p99_ms",
			"value":     live.P99Ms,
			"threshold": *thresholds.MaxP99LatencyMs,
		})
	}
	if thresholds.MaxErrorRatePct != nil && live.ErrorPct > *thresholds.MaxErrorRatePct {
		alerts = append(alerts, map[string]any{
			"type":      "threshold_alert",
			"test_id":   test.ID,
			"test_name": test.Name,
			"timestamp": live.Timestamp,
			"metric":    "error_pct",
			"value":     live.ErrorPct,
			"threshold": *thresholds.MaxErrorRatePct,
			"action":    "stop_requested",
		})
	}
	if live != nil && live.TargetTPS > 0 && live.ThroughputTargetMissed {
		alerts = append(alerts, map[string]any{
			"type":       "threshold_alert",
			"test_id":    test.ID,
			"test_name":  test.Name,
			"timestamp":  live.Timestamp,
			"metric":     "throughput_gap_pct",
			"value":      live.TPS,
			"threshold":  live.TargetTPS,
			"target_tps": live.TargetTPS,
			"gap_pct":    live.ThroughputGapPct,
		})
	}
	if alert := buildServerTelemetryThresholdAlert(test, live, thresholds.MaxAvgServerProcessingMs, "avg_server_processing_ms", func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgServerProcessingMs
	}); alert != nil {
		alerts = append(alerts, alert)
	}
	if alert := buildServerTelemetryThresholdAlert(test, live, thresholds.MaxAvgTransferMs, "avg_transfer_ms", func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgTransferMs
	}); alert != nil {
		alerts = append(alerts, alert)
	}
	return alerts
}

func buildServerTelemetryThresholdAlert(test *ManagedTest, live *LiveMetrics, threshold *float64, metric string, pickValue func(summary *ServerTelemetrySummary) float64) map[string]any {
	if threshold == nil || live == nil || len(live.Transactions) == 0 {
		return nil
	}

	var hotTx *TxMetrics
	hotValue := 0.0
	hotName := ""
	for name, tx := range live.Transactions {
		if tx == nil || tx.ServerTelemetry == nil {
			continue
		}
		value := pickValue(tx.ServerTelemetry)
		if value <= *threshold {
			continue
		}
		if hotTx == nil || value > hotValue {
			hotTx = tx
			hotValue = value
			hotName = name
		}
	}
	if hotTx == nil {
		return nil
	}

	return map[string]any{
		"type":              "threshold_alert",
		"test_id":           test.ID,
		"test_name":         test.Name,
		"timestamp":         live.Timestamp,
		"metric":            metric,
		"value":             hotValue,
		"threshold":         *threshold,
		"transaction":       hotName,
		"dependency_type":   hotTx.DependencyType,
		"dependency_target": hotTx.DependencyTarget,
	}
}

func shouldStopForErrorThreshold(live *LiveMetrics, thresholds ThresholdSettings) bool {
	return thresholds.MaxErrorRatePct != nil && live.ErrorPct > *thresholds.MaxErrorRatePct
}

func shouldStopForGoalStatus(live *LiveMetrics) bool {
	return live.GoalStatus != nil && live.GoalStatus.StopTriggered
}

func buildLiveCapacityStatus(current *ManagedTest, live *LiveMetrics, tests []*ManagedTest) *LiveCapacityStatus {
	if current == nil || current.CapacityAdvisory == nil {
		return nil
	}
	status := &LiveCapacityStatus{
		AllocatedVUsers:       int(liveActiveVUsers(live, current)),
		AvailableGenerators:   current.CapacityAdvisory.AvailableGenerators,
		RecommendedGenerators: current.CapacityAdvisory.RecommendedGenerators,
		Saturated:             current.CapacityAdvisory.RequiresOverride || current.CapacityAdvisory.ShortfallGenerators > 0 || current.CapacityAdvisory.RecommendedGenerators >= current.CapacityAdvisory.AvailableGenerators,
		ByProtocol:            make(map[string]*LiveCapacityBreakdown),
		ByRegion:              make(map[string]*LiveCapacityBreakdown),
	}
	for _, test := range tests {
		if test == nil {
			continue
		}
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED":
			addCapacityDemand(status, test, "allocated")
		case "QUEUED", "PENDING":
			status.QueuedVUsers += test.TotalVUsers
			addCapacityDemand(status, test, "queued")
		}
		if test.CapacityAdvisory != nil && test.CapacityAdvisory.RequiresOverride {
			status.RejectedVUsers += test.TotalVUsers
			addCapacityDemand(status, test, "rejected")
		}
	}
	if len(status.ByProtocol) == 0 {
		status.ByProtocol = nil
	}
	if len(status.ByRegion) == 0 {
		status.ByRegion = nil
	}
	return status
}

func liveActiveVUsers(live *LiveMetrics, test *ManagedTest) int64 {
	if live != nil && live.ActiveVUsers > 0 {
		return live.ActiveVUsers
	}
	if test != nil && (test.Status == "INITIALIZING" || test.Status == "RUNNING" || test.Status == "PAUSED") {
		return int64(test.TotalVUsers)
	}
	return 0
}

func addCapacityDemand(status *LiveCapacityStatus, test *ManagedTest, bucket string) {
	for protocol, vusers := range capacityDemandByProtocol(test.Groups) {
		addCapacityBreakdown(status.ByProtocol, protocol, bucket, vusers)
	}
	for region, vusers := range capacityDemandByRegion(test) {
		addCapacityBreakdown(status.ByRegion, region, bucket, vusers)
	}
}

func addCapacityBreakdown(target map[string]*LiveCapacityBreakdown, key string, bucket string, vusers int) {
	if vusers <= 0 {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "unknown"
	}
	breakdown := target[key]
	if breakdown == nil {
		breakdown = &LiveCapacityBreakdown{}
		target[key] = breakdown
	}
	switch bucket {
	case "allocated":
		breakdown.AllocatedVUsers += vusers
	case "queued":
		breakdown.QueuedVUsers += vusers
	case "rejected":
		breakdown.RejectedVUsers += vusers
	}
}

func capacityDemandByProtocol(groups []TestGroup) map[string]int {
	out := make(map[string]int)
	for _, group := range groups {
		protocol := strings.TrimSpace(group.Protocol)
		if protocol == "" {
			protocol = "unknown"
		}
		out[protocol] += group.VUsers
	}
	return out
}

func capacityDemandByRegion(test *ManagedTest) map[string]int {
	out := make(map[string]int)
	if test == nil || test.TotalVUsers <= 0 {
		return out
	}
	assignment := test.Assignment
	if assignment == nil || assignment.Mode == "" || assignment.Mode == generatorAssignmentModeAuto {
		out["auto"] = test.TotalVUsers
		return out
	}
	if assignment.Mode == generatorAssignmentModeSpecificHosts {
		if len(assignment.SelectedTargets) == 0 {
			out["selected_hosts"] = test.TotalVUsers
			return out
		}
		for _, target := range assignment.SelectedTargets {
			out[firstNonEmpty(strings.TrimSpace(target), "selected_hosts")] += test.TotalVUsers / len(assignment.SelectedTargets)
		}
		remainder := test.TotalVUsers % len(assignment.SelectedTargets)
		if remainder > 0 {
			out[firstNonEmpty(strings.TrimSpace(assignment.SelectedTargets[0]), "selected_hosts")] += remainder
		}
		return out
	}
	if assignment.Mode == generatorAssignmentModeRegionWeighted && len(assignment.RegionWeights) > 0 {
		totalWeight := 0
		regions := make([]string, 0, len(assignment.RegionWeights))
		for region, weight := range assignment.RegionWeights {
			if weight <= 0 {
				continue
			}
			regions = append(regions, region)
			totalWeight += weight
		}
		sort.Strings(regions)
		allocated := 0
		for index, region := range regions {
			vusers := 0
			if index == len(regions)-1 {
				vusers = test.TotalVUsers - allocated
			} else {
				vusers = test.TotalVUsers * assignment.RegionWeights[region] / totalWeight
				allocated += vusers
			}
			out[firstNonEmpty(strings.TrimSpace(region), "unassigned")] += vusers
		}
		if len(out) > 0 {
			return out
		}
	}
	out["unassigned"] = test.TotalVUsers
	return out
}

func (tr *TestRunner) publishLiveMetrics(test *ManagedTest, live *LiveMetrics, artifacts *runArtifacts) {
	now := time.Now()
	hubStats := HubStats{}
	if tr.hub != nil {
		hubStats = tr.hub.Stats()
	}
	if live.Telemetry == nil {
		live.Telemetry = &TelemetryHealth{}
	}
	if live.Telemetry.ControllerReceivedAt.IsZero() {
		live.Telemetry.ControllerReceivedAt = now
	}
	if live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.CollectedAt = live.Timestamp
	}
	live.Telemetry.PublishedAt = now
	if !live.Telemetry.ControllerReceivedAt.IsZero() {
		live.Telemetry.ControllerFanoutLagMs = float64(now.Sub(live.Telemetry.ControllerReceivedAt).Microseconds()) / 1000.0
	}
	if !live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.SampleAgeMs = float64(now.Sub(live.Telemetry.CollectedAt).Microseconds()) / 1000.0
	}
	live.Telemetry.WebsocketClients = hubStats.ConnectedClients
	live.Telemetry.WebsocketDroppedMessages = hubStats.DroppedClientMessages
	live.Telemetry.WebsocketDroppedEvents = hubStats.DroppedBroadcasts
	artifacts.enrichLiveMetrics(test, live)
	diagnostic, diagnosticSignature := buildTelemetryDiagnostic(test, live, tr.GetSystemSettings().Thresholds)
	publishDiagnostic := false
	var telemetryRunHealth *TelemetryRunHealth
	var previousTelemetryRunHealth *TelemetryRunHealth

	tr.mu.Lock()
	tests := make([]*ManagedTest, 0, len(tr.tests))
	for _, candidate := range tr.tests {
		tests = append(tests, candidate)
	}
	previousTelemetryRunHealth = cloneTelemetryRunHealth(test.TelemetryHealth)
	telemetryRunHealth = updateTelemetryRunHealth(&test.telemetryDiagnostic, diagnostic, diagnosticSignature, now)
	live.TelemetryStatus = cloneTelemetryRunHealth(telemetryRunHealth)
	live.CapacityStatus = buildLiveCapacityStatus(test, live, tests)
	test.errorPatterns = updateLiveErrorPatterns(test.errorPatterns, live, test.ID)
	live.ErrorPatterns = liveErrorPatternsList(test.errorPatterns)
	test.TelemetryHealth = cloneTelemetryRunHealth(telemetryRunHealth)
	test.LiveMetrics = live
	test.History = append(test.History, live)
	if len(test.History) > maxManagedTestHistory {
		test.History = append([]*LiveMetrics(nil), test.History[len(test.History)-maxManagedTestHistory:]...)
	}
	live.Telemetry.HistoryPoints = len(test.History)
	if diagnostic != nil {
		publishDiagnostic = shouldPublishTelemetryDiagnostic(&test.telemetryDiagnostic, diagnosticSignature, now)
	}
	snapshot := buildRunLiveStateSnapshotLocked(test, now)
	tr.mu.Unlock()

	tr.cacheRunSnapshot(snapshot)
	tr.saveLiveMetricsHistory(test.ID, live.Timestamp, live)
	artifacts.recordLiveMetrics(test, live)

	tr.hub.BroadcastJSON(map[string]any{
		"type":       "metrics_update",
		"test_id":    test.ID,
		"project_id": normalizeProjectID(test.ProjectID),
		"test_name":  test.Name,
		"timestamp":  live.Timestamp,
		"data":       live,
	})
	if publishDiagnostic {
		tr.hub.BroadcastJSON(map[string]any{
			"type":       "telemetry_diagnostic",
			"test_id":    test.ID,
			"project_id": normalizeProjectID(test.ProjectID),
			"test_name":  test.Name,
			"timestamp":  now,
			"data":       diagnostic,
		})
	}
	tr.broadcastTelemetryHealthTransition(test, previousTelemetryRunHealth, telemetryRunHealth, now)
}

func buildRunLiveStateSnapshotLocked(test *ManagedTest, updatedAt time.Time) RunLiveStateSnapshot {
	if test == nil {
		return RunLiveStateSnapshot{UpdatedAt: updatedAt}
	}
	return RunLiveStateSnapshot{
		TestID:        test.ID,
		TestName:      test.Name,
		ProjectID:     normalizeProjectID(test.ProjectID),
		Status:        test.Status,
		UpdatedAt:     updatedAt.UTC(),
		StartedAt:     test.StartedAt,
		EndedAt:       test.EndedAt,
		LiveMetrics:   test.LiveMetrics,
		HistoryPoints: len(test.History),
	}
}

func (tr *TestRunner) cacheRunSnapshot(snapshot RunLiveStateSnapshot) {
	if tr == nil || strings.TrimSpace(snapshot.TestID) == "" {
		return
	}
	tr.mu.RLock()
	store := tr.liveState
	tr.mu.RUnlock()
	if store == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), defaultCacheWriteTimeout)
		defer cancel()
		if err := store.SetRunSnapshot(ctx, snapshot, defaultRunSnapshotTTL); err != nil {
			slog.Warn("failed to cache live run snapshot", "test_id", snapshot.TestID, "err", err)
		}
	}()
}

func (tr *TestRunner) broadcastTelemetryHealthTransition(test *ManagedTest, previous *TelemetryRunHealth, current *TelemetryRunHealth, now time.Time) {
	if tr == nil || test == nil || current == nil {
		return
	}
	previousStatus := ""
	if previous != nil {
		previousStatus = previous.Status
	}
	if previousStatus == current.Status {
		return
	}

	eventType := ""
	switch current.Status {
	case "degraded":
		eventType = "telemetry_degraded"
	case "breached":
		eventType = "telemetry_breached"
	case "healthy":
		if previousStatus == "" || previousStatus == "healthy" {
			return
		}
		eventType = "telemetry_recovered"
	default:
		return
	}

	message := map[string]any{
		"type":       "telemetry_health_changed",
		"event_type": eventType,
		"test_id":    test.ID,
		"project_id": normalizeProjectID(test.ProjectID),
		"test_name":  test.Name,
		"timestamp":  now,
		"data":       current,
	}
	if tr.hub != nil {
		tr.hub.BroadcastJSON(message)
	}
	if tr.notifier != nil {
		tr.notifier.Dispatch(tr.currentNotificationSettings(), NotificationEvent{
			Type:       eventType,
			TestID:     test.ID,
			ProjectID:  normalizeProjectID(test.ProjectID),
			TestName:   test.Name,
			Status:     current.Status,
			Severity:   current.Severity,
			Summary:    current.Summary,
			Conditions: append([]string(nil), current.Conditions...),
			Timestamp:  now,
		})
	}
}

func applyGoalControllerUpdate(test *ManagedTest, live *LiveMetrics, groupVUsers map[string]int, update goalControllerUpdate, scaleVUsers func(groupName string, target int) error) {
	live.GoalStatus = update.Status
	if !update.ShouldScale {
		return
	}

	targets := distributeTargetVUsers(test.Groups, groupVUsers, update.TargetTotal)
	for _, group := range test.Groups {
		target, exists := targets[group.Name]
		if !exists || groupVUsers[group.Name] == target {
			continue
		}
		if err := scaleVUsers(group.Name, target); err != nil {
			slog.Warn("goal-oriented scale failed", "test_id", test.ID, "group", group.Name, "target_vusers", target, "error", err)
			live.GoalStatus.LastScaleReason = fmt.Sprintf("scale failed: %v", err)
			break
		}
		groupVUsers[group.Name] = target
		for i := range test.Groups {
			if test.Groups[i].Name == group.Name {
				test.Groups[i].VUsers = target
				break
			}
		}
	}

	now := live.Timestamp
	test.goalControl.lastScaleAt = &now
	test.goalControl.lastScaleDirection = update.ScaleDirection
	test.goalControl.lastScaleReason = update.ScaleReason
	live.GoalStatus.LastScaleAt = &now
	live.GoalStatus.LastScaleDirection = update.ScaleDirection
	live.GoalStatus.LastScaleReason = update.ScaleReason
	live.GoalStatus.CurrentTotalVUsers = totalGroupVUsers(groupVUsers)
	live.GoalStatus.SuggestedTotalVUsers = live.GoalStatus.CurrentTotalVUsers
}

func (tr *TestRunner) requestStopIfRunning(test *ManagedTest, warningMessage string) {
	tr.mu.RLock()
	status := test.Status
	tr.mu.RUnlock()
	if status != "RUNNING" {
		return
	}
	if err := tr.StopTest(test.ID); err != nil {
		slog.Warn(warningMessage, "test_id", test.ID, "error", err)
	}
}

func (tr *TestRunner) startManagedTestRun(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts) (context.Context, chan struct{}, chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	test.cancel = cancel
	test.multiEngine = mge

	now := time.Now()
	test.StartedAt = &now
	test.Status = "RUNNING"
	tr.broadcastEvent("test_started", test)

	metricsDone := make(chan struct{})
	metricsStopped := make(chan struct{})
	test.goalControl = &goalControllerState{}
	go func() {
		tr.broadcastMetrics(test, mge, artifacts, metricsDone)
		close(metricsStopped)
	}()

	return ctx, metricsDone, metricsStopped
}

func (tr *TestRunner) failTestBeforeRun(test *ManagedTest, message string) {
	now := time.Now()
	tr.mu.Lock()
	test.Status = "FAILED"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = message
	test.EndedAt = &now
	tr.mu.Unlock()
	tr.updateStoredTestStatus(test.ID, "FAILED", &now, message)
	tr.broadcastEvent("test_failed", test)
	tr.promotePendingTests()
}

func (tr *TestRunner) completeTestRun(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord) {
	tr.mu.Lock()
	test.Status = finalStatus
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = finalError
	test.EndedAt = &endTime
	test.Archive = archiveRecord
	if archiveRecord != nil {
		tr.archives[test.ID] = archiveRecord
	}
	tr.captureUsageSnapshotForTerminalRunLocked(test.ID, test.ProjectID)
	tr.mu.Unlock()
	tr.updateStoredTestStatus(test.ID, finalStatus, &endTime, finalError)
	if finalStatus == "FAILED" {
		tr.broadcastEvent("test_failed", test)
		tr.promotePendingTests()
		return
	}
	tr.broadcastEvent("test_complete", test)
	tr.promotePendingTests()
}

func (tr *TestRunner) captureUsageSnapshotForTerminalRunLocked(testID string, projectID string) {
	if tr == nil || tr.usageStore == nil {
		return
	}
	projectID = normalizeProjectID(projectID)
	tests := make([]*ManagedTest, 0, len(tr.tests))
	for _, test := range tr.tests {
		tests = append(tests, test)
	}
	if _, err := tr.saveUsageAccountingSnapshotFromTests(projectID, tests); err != nil {
		slog.Warn("failed to save usage accounting snapshot", "test_id", testID, "project_id", projectID, "error", err)
	}
}

func (tr *TestRunner) captureUsageSnapshotForCadence(projectID string) (*storage.UsageAccountingSnapshot, error) {
	if tr == nil || tr.usageStore == nil {
		return nil, nil
	}
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	tr.mu.RLock()
	tests := make([]*ManagedTest, 0, len(tr.tests))
	for _, test := range tr.tests {
		tests = append(tests, test)
	}
	tr.mu.RUnlock()
	return tr.saveUsageAccountingSnapshotFromTests(projectID, tests)
}

func (tr *TestRunner) saveUsageAccountingSnapshotFromTests(projectID string, tests []*ManagedTest) (*storage.UsageAccountingSnapshot, error) {
	usage := usageAccountingFromTests(projectID, tests)
	payload, err := json.Marshal(usage)
	if err != nil {
		return nil, fmt.Errorf("encode usage accounting snapshot: %w", err)
	}
	now := time.Now().UTC()
	snapshot := &storage.UsageAccountingSnapshot{
		ID:          uuid.NewString(),
		ProjectID:   projectID,
		GeneratedAt: usage.GeneratedAt,
		PayloadJSON: string(payload),
		CreatedAt:   now,
	}
	if err := tr.usageStore.SaveUsageAccountingSnapshot(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (tr *TestRunner) UsageAccountingSnapshotInterval() time.Duration {
	if tr == nil {
		return 0
	}
	seconds := tr.GetSystemSettings().General.UsageSnapshotIntervalSeconds
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func (tr *TestRunner) RunUsageAccountingSnapshotCadence(ctx context.Context, ticks <-chan time.Time) {
	if tr == nil || ticks == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if _, err := tr.captureUsageSnapshotForCadence(""); err != nil {
				slog.Warn("failed to save cadence usage accounting snapshot", "error", err)
			}
		}
	}
}

func (tr *TestRunner) promotePendingTests() {
	for {
		tr.mu.Lock()
		pending := tr.nextPromotablePendingTestLocked()
		if pending == nil {
			tr.mu.Unlock()
			return
		}
		pending.Status = "INITIALIZING"
		pending.PendingSince = nil
		pending.PendingReason = ""
		pending.QueuePosition = 0
		pending.Error = ""
		pending.executionBackend = tr.execution
		if pending.executionBackend == nil {
			pending.executionBackend = localExecutionBackend{}
		}
		tr.mu.Unlock()
		tr.saveManagedTestRecord(pending)
		tr.recordSystemTestAudit("test.pending.promote", "success", pending, map[string]any{
			"status": pending.Status,
			"reason": "fair_share_capacity_available",
		})
		tr.broadcastEvent("test_started", pending)
		go tr.runTest(pending)
	}
}

func (tr *TestRunner) recordSystemTestAudit(action string, status string, test *ManagedTest, details map[string]any) {
	if tr == nil || tr.scenarioStore == nil || test == nil {
		return
	}
	_ = tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           action,
		ActorType:        "system",
		ActorSubject:     "test-runner",
		ActorDisplayName: "Test Runner",
		ProjectID:        normalizeProjectID(test.ProjectID),
		ResourceType:     "test",
		ResourceID:       test.ID,
		Status:           status,
		Details:          details,
		CreatedAt:        time.Now().UTC(),
	})
}

func (tr *TestRunner) nextPromotablePendingTestLocked() *ManagedTest {
	pending := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if test.Status == "PENDING" {
			pending = append(pending, test)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	tr.sortPendingTestsForFairShareLocked(pending)
	for _, test := range pending {
		tr.refreshCapacityAdvisoryLocked(test)
		if buildCapacityShortfallStartError(test.ID, test.CapacityAdvisory, StartTestOptions{}) != nil {
			continue
		}
		if blocked, _ := tr.evaluateQuotaBlockLocked(test, ""); blocked {
			continue
		}
		return test
	}
	return nil
}

func appendRunError(existing string, prefix string, err error) string {
	if err == nil {
		return existing
	}
	message := prefix + ": " + err.Error()
	if existing == "" {
		return message
	}
	return existing + "; " + message
}

func (tr *TestRunner) runScriptShakeout(test *ManagedTest) error {
	for _, group := range test.groupSpecs {
		if group.Script == nil {
			return fmt.Errorf("script shakeout failed for %s: script is missing", group.Name)
		}

		shakeoutScript := *group.Script
		shakeoutScript.Config.RunLogic.Iterations = 1

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := engine.RunScriptShakeout(ctx, group.Name, &shakeoutScript)
		cancel()
		if err != nil {
			return fmt.Errorf("script shakeout failed for %s: %w", group.Name, err)
		}
	}

	return nil
}

func (tr *TestRunner) broadcastMetrics(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts, done chan struct{}) {
	groupVUsers := make(map[string]int)
	for _, g := range test.Groups {
		groupVUsers[g.Name] = g.VUsers
	}

	intervalSec := tr.GetSystemSettings().General.MetricsIntervalSeconds
	if intervalSec < 1 {
		intervalSec = 1
	}
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	lastLatencyCheck := time.Time{}

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			snap, ok := mge.LatestSnapshot()
			if !ok {
				continue
			}

			live := buildLiveMetricsForBroadcast(snap, groupVUsers, time.Now())

			if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
				currentTotalVUsers := totalGroupVUsers(groupVUsers)
				update := evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
				applyGoalControllerUpdate(test, live, groupVUsers, update, mge.ScaleVUsers)
			}

			// Throttle network latency pings to every 5 seconds
			if time.Since(lastLatencyCheck) >= 5*time.Second {
				go tr.measureNetworkLatency(test, live)
				lastLatencyCheck = time.Now()
			}

			tr.publishLiveMetrics(test, live, artifacts)
		}
	}
}

func (tr *TestRunner) processMetricsSnapshot(test *ManagedTest, snap engine.MetricsSnapshot, groupVUsers map[string]int, scaleVUsers func(groupName string, target int) error, artifacts *runArtifacts) {
	live := buildLiveMetricsFromSnapshot(snap, groupVUsers)

	if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
		currentTotalVUsers := totalGroupVUsers(groupVUsers)
		update := evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
		applyGoalControllerUpdate(test, live, groupVUsers, update, scaleVUsers)
	}

	tr.publishLiveMetrics(test, live, artifacts)

	// Measure network latency in background if not already measured for this interval
	go tr.measureNetworkLatency(test, live)

	settings := tr.GetSystemSettings()
	for _, alert := range buildThresholdAlerts(test, live, settings.Thresholds) {
		alert["project_id"] = normalizeProjectID(test.ProjectID)
		tr.hub.BroadcastJSON(alert)
	}

	if shouldStopForErrorThreshold(live, settings.Thresholds) {
		tr.requestStopIfRunning(test, "threshold stop failed")
	}

	if shouldStopForGoalStatus(live) {
		tr.requestStopIfRunning(test, "goal-oriented stop failed")
	}
}

func (tr *TestRunner) broadcastEvent(eventType string, test *ManagedTest) {
	now := time.Now()
	if tr.hub != nil {
		tr.hub.BroadcastJSON(map[string]any{
			"type":       eventType,
			"test_id":    test.ID,
			"project_id": normalizeProjectID(test.ProjectID),
			"test_name":  test.Name,
			"status":     test.Status,
			"timestamp":  now,
		})
	}
	if tr.notifier != nil {
		tr.notifier.Dispatch(tr.currentNotificationSettings(), NotificationEvent{
			Type:      eventType,
			TestID:    test.ID,
			ProjectID: normalizeProjectID(test.ProjectID),
			TestName:  test.Name,
			Status:    test.Status,
			Timestamp: now,
		})
	}
}

// applyRuntimeOverrides merges test-level runtime settings into a script config,
// mirroring LoadRunner Controller's override behavior (test-level wins where specified).
func applyRuntimeOverrides(script *dsl.Script, overrides *RuntimeSettingsOverride) error {
	if script == nil || overrides == nil {
		return nil
	}
	if len(overrides.Env) > 0 {
		overridden, err := applyRuntimeEnvOverrides(script, overrides.Env)
		if err != nil {
			return err
		}
		*script = *overridden
	}
	if overrides.BaseURL != "" {
		script.Config.BaseURL = overrides.BaseURL
	}
	if overrides.Iterations != nil {
		script.Config.RunLogic.Iterations = *overrides.Iterations
	}
	if overrides.Pacing != nil {
		script.Config.Pacing = *overrides.Pacing
	}
	if overrides.ThinkTime != nil {
		script.Config.ThinkTime = *overrides.ThinkTime
	}
	if overrides.ErrorHandling != nil {
		script.Config.ErrorHandling = *overrides.ErrorHandling
	}
	if overrides.Browser != nil {
		if overrides.Browser.Headless != nil {
			headless := *overrides.Browser.Headless
			script.Config.Browser.Headless = &headless
		}
		if overrides.Browser.DeviceProfile != "" {
			script.Config.Browser.DeviceProfile = overrides.Browser.DeviceProfile
		}
		if overrides.Browser.TouchMode != nil {
			touchMode := *overrides.Browser.TouchMode
			script.Config.Browser.TouchMode = &touchMode
		}
		if overrides.Browser.Orientation != "" {
			script.Config.Browser.Orientation = overrides.Browser.Orientation
		}
	}
	if overrides.LogLevel != "" {
		script.Config.Log.Level = overrides.LogLevel
	}
	if overrides.AutoTransactions != "" {
		script.Config.RunLogic.AutoTransactions = overrides.AutoTransactions
	}
	return nil
}

func applyRuntimeEnvOverrides(script *dsl.Script, overrides map[string]string) (*dsl.Script, error) {
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, fmt.Errorf("marshal runtime env overrides: %w", err)
	}
	var generic any
	if err := json.Unmarshal(payload, &generic); err != nil {
		return nil, fmt.Errorf("decode runtime env overrides: %w", err)
	}
	generic = replaceRuntimeEnvTemplates(generic, overrides)
	payload, err = json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("marshal overridden runtime script: %w", err)
	}
	var overridden dsl.Script
	if err := json.Unmarshal(payload, &overridden); err != nil {
		return nil, fmt.Errorf("decode overridden runtime script: %w", err)
	}
	return &overridden, nil
}

func replaceRuntimeEnvTemplates(value any, overrides map[string]string) any {
	switch typed := value.(type) {
	case string:
		return runtimeOverrideEnvPattern.ReplaceAllStringFunc(typed, func(token string) string {
			match := runtimeOverrideEnvPattern.FindStringSubmatch(token)
			if len(match) != 2 {
				return token
			}
			key := strings.TrimSpace(match[1])
			if replacement, ok := overrides[key]; ok {
				return replacement
			}
			return token
		})
	case []any:
		resolved := make([]any, len(typed))
		for i := range typed {
			resolved[i] = replaceRuntimeEnvTemplates(typed[i], overrides)
		}
		return resolved
	case map[string]any:
		resolved := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved[key] = replaceRuntimeEnvTemplates(item, overrides)
		}
		return resolved
	default:
		return value
	}
}

func cloneRuntimeSettings(overrides *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if overrides == nil {
		return nil
	}
	clone := *overrides
	if overrides.Iterations != nil {
		iterations := *overrides.Iterations
		clone.Iterations = &iterations
	}
	if overrides.Pacing != nil {
		pacing := *overrides.Pacing
		clone.Pacing = &pacing
	}
	if overrides.ThinkTime != nil {
		thinkTime := *overrides.ThinkTime
		clone.ThinkTime = &thinkTime
	}
	if overrides.ErrorHandling != nil {
		errorHandling := *overrides.ErrorHandling
		clone.ErrorHandling = &errorHandling
	}
	if overrides.Browser != nil {
		browser := *overrides.Browser
		if overrides.Browser.Headless != nil {
			headless := *overrides.Browser.Headless
			browser.Headless = &headless
		}
		if overrides.Browser.TouchMode != nil {
			touchMode := *overrides.Browser.TouchMode
			browser.TouchMode = &touchMode
		}
		clone.Browser = &browser
	}
	if len(overrides.Env) > 0 {
		clone.Env = make(map[string]string, len(overrides.Env))
		for key, value := range overrides.Env {
			clone.Env[key] = value
		}
	}
	if len(overrides.RedactionRules) > 0 {
		clone.RedactionRules = make([]engine.RedactionRule, len(overrides.RedactionRules))
		copy(clone.RedactionRules, overrides.RedactionRules)
	}
	return &clone
}

func buildRuntimeRedactionConfig(runtimeSettings *RuntimeSettingsOverride) (*engine.RedactionConfig, error) {
	if runtimeSettings == nil || len(runtimeSettings.RedactionRules) == 0 {
		return nil, nil
	}
	return engine.NewRedactionConfig(runtimeSettings.RedactionRules)
}

func cloneRedactionRules(rules []engine.RedactionRule) []engine.RedactionRule {
	if len(rules) == 0 {
		return nil
	}
	clone := make([]engine.RedactionRule, len(rules))
	copy(clone, rules)
	return clone
}

func (tr *TestRunner) resolveCreateTestProjectID(requestProjectID string, groups []CreateGroupEntry) (string, error) {
	if requestProjectID != "" {
		return normalizeProjectID(requestProjectID), nil
	}
	projectID := ""
	for _, group := range groups {
		script, ok := tr.scripts[group.ScriptID]
		if !ok {
			continue
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return storage.DefaultProjectID, nil
		}
	}
	return normalizeProjectID(projectID), nil
}

func (tr *TestRunner) resolveEffectiveRuntimeSettings(projectID string, runtimeSettings *RuntimeSettingsOverride) (*RuntimeSettingsOverride, error) {
	effective := cloneRuntimeSettings(runtimeSettings)
	rules, err := tr.resolveEffectiveRedactionRules(projectID, nilIfNoRules(runtimeSettings))
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return effective, nil
	}
	if effective == nil {
		effective = &RuntimeSettingsOverride{}
	}
	effective.RedactionRules = rules
	return effective, nil
}

func nilIfNoRules(runtimeSettings *RuntimeSettingsOverride) []engine.RedactionRule {
	if runtimeSettings == nil {
		return nil
	}
	return runtimeSettings.RedactionRules
}

func (tr *TestRunner) resolveEffectiveRedactionRules(projectID string, explicit []engine.RedactionRule) ([]engine.RedactionRule, error) {
	merged := cloneRedactionRules(explicit)
	if tr.projectStore == nil {
		return merged, nil
	}
	projectID = normalizeProjectID(projectID)
	project, err := tr.projectStore.GetProject(projectID)
	if err != nil || project == nil || len(project.Settings.RedactionRules) == 0 {
		return merged, nil
	}
	merged = append(merged, cloneRedactionRules(project.Settings.RedactionRules)...)
	return merged, nil
}

func cloneGoalConfig(config *GoalConfig) *GoalConfig {
	if config == nil {
		return nil
	}
	clone := *config
	if config.TargetAverageTPS != nil {
		value := *config.TargetAverageTPS
		clone.TargetAverageTPS = &value
	}
	if config.P95LatencyMs != nil {
		value := *config.P95LatencyMs
		clone.P95LatencyMs = &value
	}
	if config.MaxErrorPct != nil {
		value := *config.MaxErrorPct
		clone.MaxErrorPct = &value
	}
	if config.MaxAvgServerProcessingMs != nil {
		value := *config.MaxAvgServerProcessingMs
		clone.MaxAvgServerProcessingMs = &value
	}
	if config.MaxAvgTransferMs != nil {
		value := *config.MaxAvgTransferMs
		clone.MaxAvgTransferMs = &value
	}
	if config.MaxErrorCount != nil {
		value := *config.MaxErrorCount
		clone.MaxErrorCount = &value
	}
	if config.MinVUsers != nil {
		value := *config.MinVUsers
		clone.MinVUsers = &value
	}
	if config.MaxVUsers != nil {
		value := *config.MaxVUsers
		clone.MaxVUsers = &value
	}
	return &clone
}

func normalizeScenarioMode(mode string) string {
	if mode == "goal_oriented" {
		return mode
	}
	return "vuser_driven"
}

func hasGoalConfig(config *GoalConfig) bool {
	if config == nil {
		return false
	}
	return config.TargetAverageTPS != nil ||
		config.P95LatencyMs != nil ||
		config.MaxErrorPct != nil ||
		config.MaxAvgServerProcessingMs != nil ||
		config.MaxAvgTransferMs != nil ||
		config.MaxErrorCount != nil ||
		config.MinVUsers != nil ||
		config.MaxVUsers != nil ||
		config.StopOnSLABreach ||
		config.StopOnMaxErrors ||
		config.SLABreachWindow != ""
}

func evaluateGoalController(now time.Time, currentTotalVUsers int, live *LiveMetrics, config *GoalConfig, state *goalControllerState) goalControllerUpdate {
	status := &GoalStatus{
		Mode:                 "goal_oriented",
		CurrentTPS:           live.TPS,
		CurrentTotalVUsers:   currentTotalVUsers,
		TargetAverageTPS:     config.TargetAverageTPS,
		MaxErrorCount:        config.MaxErrorCount,
		MinVUsers:            config.MinVUsers,
		MaxVUsers:            config.MaxVUsers,
		SuggestedTotalVUsers: currentTotalVUsers,
	}
	if state != nil {
		status.LastScaleAt = state.lastScaleAt
		status.LastScaleDirection = state.lastScaleDirection
		status.LastScaleReason = state.lastScaleReason
	}

	latencyBreached := config.P95LatencyMs != nil && live.P95Ms > *config.P95LatencyMs
	errorBreached := config.MaxErrorPct != nil && live.ErrorPct > *config.MaxErrorPct
	serverProcessingBreached := hasServerTelemetryBreach(live, config.MaxAvgServerProcessingMs, func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgServerProcessingMs
	})
	transferBreached := hasServerTelemetryBreach(live, config.MaxAvgTransferMs, func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgTransferMs
	})
	errorCountBreached := config.MaxErrorCount != nil && live.TotalErrors > int64(*config.MaxErrorCount)
	status.LatencyBreached = latencyBreached
	status.ErrorBreached = errorBreached
	status.ServerProcessingBreached = serverProcessingBreached
	status.TransferBreached = transferBreached
	status.ErrorCountBreached = errorCountBreached
	status.BreachActive = latencyBreached || errorBreached || serverProcessingBreached || transferBreached

	if status.BreachActive {
		if state != nil && state.breachStartedAt == nil {
			started := now
			state.breachStartedAt = &started
		}
		if state != nil && state.breachStartedAt != nil {
			status.BreachDurationSec = int(now.Sub(*state.breachStartedAt).Seconds())
		}
	} else if state != nil {
		state.breachStartedAt = nil
	}

	if config.StopOnMaxErrors && errorCountBreached {
		status.StopTriggered = true
		status.StopReason = goalStopReason(latencyBreached, errorBreached, serverProcessingBreached, transferBreached, errorCountBreached)
		return goalControllerUpdate{
			Status:     status,
			ShouldStop: true,
			StopReason: status.StopReason,
		}
	}

	if config.StopOnSLABreach && status.BreachActive {
		window := goalBreachWindow(config)
		if time.Duration(status.BreachDurationSec)*time.Second >= window {
			status.StopTriggered = true
			status.StopReason = goalStopReason(latencyBreached, errorBreached, serverProcessingBreached, transferBreached, errorCountBreached)
			return goalControllerUpdate{
				Status:     status,
				ShouldStop: true,
				StopReason: status.StopReason,
			}
		}
	}

	if config.TargetAverageTPS != nil && shouldEvaluateScale(now, state) {
		minTPS := *config.TargetAverageTPS * 0.95
		maxTPS := *config.TargetAverageTPS * 1.05
		desiredTotal := currentTotalVUsers
		scaleDirection := ""
		scaleReason := ""

		if live.TPS < minTPS {
			deltaRatio := (*config.TargetAverageTPS - live.TPS) / math.Max(*config.TargetAverageTPS, 1)
			step := goalScaleStep(currentTotalVUsers, deltaRatio)
			desiredTotal = currentTotalVUsers + step
			scaleDirection = "up"
			scaleReason = "below_target_tps"
		} else if live.TPS > maxTPS {
			deltaRatio := (live.TPS - *config.TargetAverageTPS) / math.Max(live.TPS, 1)
			step := goalScaleStep(currentTotalVUsers, deltaRatio)
			desiredTotal = currentTotalVUsers - step
			scaleDirection = "down"
			scaleReason = "above_target_tps"
		}

		desiredTotal = clampGoalVUsers(desiredTotal, config)
		status.SuggestedTotalVUsers = desiredTotal
		if desiredTotal != currentTotalVUsers {
			return goalControllerUpdate{
				Status:         status,
				ShouldScale:    true,
				TargetTotal:    desiredTotal,
				ScaleDirection: scaleDirection,
				ScaleReason:    scaleReason,
			}
		}
	}

	return goalControllerUpdate{Status: status}
}

func hasServerTelemetryBreach(live *LiveMetrics, threshold *float64, pickValue func(summary *ServerTelemetrySummary) float64) bool {
	if live == nil || threshold == nil || len(live.Transactions) == 0 {
		return false
	}
	for _, tx := range live.Transactions {
		if tx == nil || tx.ServerTelemetry == nil {
			continue
		}
		if pickValue(tx.ServerTelemetry) > *threshold {
			return true
		}
	}
	return false
}

func shouldEvaluateScale(now time.Time, state *goalControllerState) bool {
	if state == nil || state.lastScaleAt == nil {
		return true
	}
	return now.Sub(*state.lastScaleAt) >= 5*time.Second
}

func goalScaleStep(currentTotalVUsers int, deltaRatio float64) int {
	base := currentTotalVUsers
	if base < 1 {
		base = 1
	}
	step := int(math.Ceil(float64(base) * math.Min(math.Max(deltaRatio, 0.05), 0.25)))
	if step < 1 {
		return 1
	}
	return step
}

func clampGoalVUsers(value int, config *GoalConfig) int {
	if config == nil {
		if value < 0 {
			return 0
		}
		return value
	}
	if config.MinVUsers != nil && value < *config.MinVUsers {
		value = *config.MinVUsers
	}
	if config.MaxVUsers != nil && value > *config.MaxVUsers {
		value = *config.MaxVUsers
	}
	if value < 0 {
		return 0
	}
	return value
}

func goalBreachWindow(config *GoalConfig) time.Duration {
	if config == nil || config.SLABreachWindow == "" {
		return 30 * time.Second
	}
	window, err := time.ParseDuration(config.SLABreachWindow)
	if err != nil || window <= 0 {
		return 30 * time.Second
	}
	return window
}

func goalStopReason(latencyBreached bool, errorBreached bool, serverProcessingBreached bool, transferBreached bool, errorCountBreached bool) string {
	switch {
	case errorCountBreached:
		return "max error count reached"
	case latencyBreached && errorBreached:
		return "latency and error SLA breached"
	case serverProcessingBreached && transferBreached:
		return "server processing and transfer SLA breached"
	case latencyBreached:
		return "p95 latency SLA breached"
	case errorBreached:
		return "error rate SLA breached"
	case serverProcessingBreached:
		return "server processing SLA breached"
	case transferBreached:
		return "transfer SLA breached"
	default:
		return "SLA breached"
	}
}

func totalGroupVUsers(groupVUsers map[string]int) int {
	total := 0
	for _, vusers := range groupVUsers {
		total += vusers
	}
	return total
}

func distributeTargetVUsers(groups []TestGroup, current map[string]int, desiredTotal int) map[string]int {
	if len(groups) == 0 {
		return nil
	}
	if desiredTotal < 0 {
		desiredTotal = 0
	}

	totalCurrent := 0
	for _, group := range groups {
		totalCurrent += current[group.Name]
	}

	targets := make(map[string]int, len(groups))
	if totalCurrent <= 0 {
		remaining := desiredTotal
		for idx, group := range groups {
			share := 0
			if len(groups)-idx > 0 {
				share = remaining / (len(groups) - idx)
			}
			targets[group.Name] = share
			remaining -= share
		}
		return targets
	}

	type allocation struct {
		name      string
		base      int
		remainder float64
	}
	allocations := make([]allocation, 0, len(groups))
	assigned := 0
	for _, group := range groups {
		exact := float64(desiredTotal) * float64(current[group.Name]) / float64(totalCurrent)
		base := int(math.Floor(exact))
		allocations = append(allocations, allocation{name: group.Name, base: base, remainder: exact - float64(base)})
		assigned += base
	}

	remaining := desiredTotal - assigned
	for i := 0; i < len(allocations) && remaining > 0; i++ {
		bestIdx := i
		for j := i + 1; j < len(allocations); j++ {
			if allocations[j].remainder > allocations[bestIdx].remainder {
				bestIdx = j
			}
		}
		allocations[i], allocations[bestIdx] = allocations[bestIdx], allocations[i]
		allocations[i].base++
		remaining--
	}

	for _, allocation := range allocations {
		targets[allocation.name] = allocation.base
	}
	return targets
}

func decodeStoredGroups(value any) []TestGroup {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var groups []TestGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		return nil
	}
	return groups
}

func decodeStoredRuntimeSettings(value any) *RuntimeSettingsOverride {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var overrides RuntimeSettingsOverride
	if err := json.Unmarshal(data, &overrides); err != nil {
		return nil
	}
	return cloneRuntimeSettings(&overrides)
}

func decodeStoredGoalConfig(value any) *GoalConfig {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var config GoalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil
	}
	return cloneGoalConfig(&config)
}

func decodeStoredGeneratorAssignment(value any) *GeneratorAssignmentSettings {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var assignment GeneratorAssignmentSettings
	if err := json.Unmarshal(data, &assignment); err != nil {
		return nil
	}
	normalized, err := normalizeGeneratorAssignmentSettings(&assignment)
	if err != nil {
		return nil
	}
	return normalized
}

func (tr *TestRunner) measureNetworkLatency(test *ManagedTest, live *LiveMetrics) {
	// Identify unique base URLs across all groups
	endpoints := make(map[string]string)
	for _, g := range test.Groups {
		if g.script != nil && g.script.Config.BaseURL != "" {
			u, err := url.Parse(g.script.Config.BaseURL)
			if err == nil {
				endpoints[u.Host] = g.script.Config.BaseURL
			}
		}
	}

	if len(endpoints) == 0 {
		return
	}

	latency := &NetworkLatency{
		Targets: make([]LatencyTarget, 0, len(endpoints)),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for host, raw := range endpoints {
		wg.Add(1)
		go func(h, r string) {
			defer wg.Done()
			target := tr.measureEndpointLatency(h, r)
			mu.Lock()
			latency.Targets = append(latency.Targets, target)
			mu.Unlock()
		}(host, raw)
	}
	wg.Wait()

	tr.mu.Lock()
	live.Latency = latency
	tr.mu.Unlock()

	// Broadcast the latency update specifically if needed,
	// though it will be part of the next metrics broadcast too.
	tr.hub.BroadcastJSON(map[string]any{
		"type":       "latency_update",
		"test_id":    test.ID,
		"project_id": normalizeProjectID(test.ProjectID),
		"latency":    latency,
	})
}

func (tr *TestRunner) measureEndpointLatency(host, endpoint string) LatencyTarget {
	target := LatencyTarget{
		Name:      host,
		Endpoint:  endpoint,
		Timestamp: time.Now(),
	}

	start := time.Now()

	// We'll use a simple TCP dial to measure base R/T latency to the host.
	// This is more reliable than ICMP ping which is often blocked.
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	// Default to port 80 or 443 if not specified
	dialHost := host
	if !strings.Contains(host, ":") {
		if strings.HasPrefix(endpoint, "https") {
			dialHost += ":443"
		} else {
			dialHost += ":80"
		}
	}

	conn, err := dialer.Dial("tcp", dialHost)
	if err != nil {
		target.LastSuccess = false
		return target
	}
	defer conn.Close()

	target.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
	target.LastSuccess = true

	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		target.IP = addr.IP.String()
	}

	// Note: hop count/route info typically requires raw sockets or external tools.
	// We'll leave it as 0 for now or implement a basic traceroute later if requested.

	return target
}

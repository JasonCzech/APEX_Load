package generatorrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type DataFile struct {
	Name    string
	Content []byte
	SHA256  string
}

// DistributedVUserIDNamespaceSize is the durable, disjoint ID range reserved
// for each logical generator shard. It matches the generator's maximum int32
// VUser capacity and leaves every shard free to scale without entering another
// shard's namespace.
const DistributedVUserIDNamespaceSize = uint64(^uint32(0) >> 1)

type Assignment struct {
	LiveTableBindingsJSON []byte
	RuntimeResourceClaim  *pb.RuntimeResourceClaim
	PrepareOnly           bool
	JavaHost              *pb.JavaHostSelection
	// AdoptionGrace > 0 opts the run into HA continuity (WorkAssignment.adoption_grace).
	AdoptionGrace         time.Duration
	ArchiveSourceTarget   string
	SourcePlan            *pb.SourcePlanManifest
	TerminationMode       string
	TestID                string
	ExecutionGeneration   uint64
	ScenarioID            string
	RunCorrelationOrigins []string
	CorrelationCarriers   []string
	CorrelationTestName   string
	// JointEvidenceProjection is the run's frozen joint evidence v4
	// projection; send only to FeatureJointEvidenceV4 peers.
	JointEvidenceProjection      *engine.JointProjection
	RequireTestExecutionLease    bool
	GroupName                    string
	TargetDuration               time.Duration
	TargetDurationSeconds        int64
	DurationOffset               time.Duration
	ScriptID                     string
	ExpectedScriptHash           string
	ScriptContent                []byte
	VUserCount                   int32
	RampUp                       time.Duration
	RampUpSeconds                int32
	RampDown                     time.Duration
	InitStrategy                 string
	PreBaseline                  time.Duration
	PostBaseline                 time.Duration
	ShardOrdinal                 uint32
	ShardCount                   uint32
	VUserIDOffset                uint64
	ParameterPopulation          uint32
	ParameterOffset              uint32
	StartPaused                  bool
	Environment                  map[string]string
	DataFiles                    []DataFile
	RedactionRules               []engine.RedactionRule
	SecretBundle                 map[string]string
	SecretBundleID               string
	RunSupervisionToken          []byte
	Region                       string
	StartOffset                  time.Duration
	MessageDetailMaxItems        int
	MessageDetailMaxItemBytes    int64
	MessageDetailMaxTotalBytes   int64
	TransactionResponseTimeSLAMs float64
}

type AssignmentResult struct {
	RuntimeResourceAdmission *pb.RuntimeResourceAdmission
	JavaHost                 *pb.JavaHostPlacement
	Accepted                 bool
	ErrorMessage             string
	AvailableCapacity        int32
}

type WorkCommitResult struct {
	Accepted            bool
	ErrorMessage        string
	RunSupervisionArmed bool
	RunSupervisionTTL   time.Duration
}

type RunSupervisionClaim struct {
	TestID                string
	ExecutionGeneration   uint64
	Token                 []byte
	RequestedTTL          time.Duration
	ExecutionLeaseOwnerID string
}

type RunSupervisionResult struct {
	Accepted          bool
	ContractVersion   uint32
	CurrentGeneration uint64
	GrantedTTL        time.Duration
	ExpiresAt         time.Time
	State             pb.RunSupervisionState
	ErrorMessage      string
}

type VUserAdjustment struct {
	TestID              string
	ExecutionGeneration uint64
	GroupName           string
	TargetVUsers        int32
	RampDuration        time.Duration
	RampSeconds         int32
	RunSupervisionToken []byte
}

type AdjustmentResult struct {
	Accepted      bool
	CurrentVUsers int32
}

type DurationAdjustment struct {
	TestID                string
	ExecutionGeneration   uint64
	TargetDuration        time.Duration
	TargetDurationSeconds int64
	RunSupervisionToken   []byte
}

type DurationResult struct {
	Accepted               bool
	CurrentDuration        time.Duration
	CurrentDurationSeconds int64
}

type PauseControl struct {
	// RequireQuiescence requests the strict acknowledged runtime pause boundary.
	RequireQuiescence   bool
	TestID              string
	ExecutionGeneration uint64
	Pause               bool
	OperationID         string
	OperationSequence   uint64
	RunSupervisionToken []byte
}

type PauseResult struct {
	// Nil means the peer did not provide the optional quiescence contract.
	QuiescenceConfirmed      *bool
	Accepted                 bool
	CurrentPaused            bool
	AppliedOperationID       string
	AppliedOperationSequence uint64
	ErrorMessage             string
}

type MetricsBatch struct {
	BrowserDocuments         *protocol.BrowserDocumentSummary
	BrowserDocumentsVersion  uint32
	MessagingProgress        *protocol.MessagingProgressSnapshot
	MessagingProgressVersion uint32
	TestID                   string
	ExecutionGeneration      uint64
	BatchSequence            uint64
	// SourceEpoch identifies the generator process that numbered
	// BatchSequence; sequences restart with each epoch. Empty for older peers.
	SourceEpoch              string
	SourceEpochStartedUnixMs int64
	// SequencePrefixStateOnly marks a registration snapshot whose earlier
	// sequences were all state-only: nothing before it was lost.
	SequencePrefixStateOnly bool
	GeneratorID             string
	TimestampUnixMs         int64
	WindowMs                int64
	Stats                   GeneratorStats
	Transactions            []TransactionMetric
	FleetMetrics            *MetricAggregate
	TransactionMetrics      []MetricAggregate
	DependencyMetrics       []MetricAggregate
	SOAPMetrics             []MetricAggregate
	NetworkProfileMetrics   []MetricAggregate
	ActionMetrics           []MetricAggregate
	ActionDimensionMetrics  []MetricAggregate
	// JointMetrics are the tuple-keyed joint evidence v4 aggregates (each
	// named by the JSON array of its values) described by JointInfo; both
	// are empty from older peers.
	JointMetrics               []MetricAggregate
	JointInfo                  *JointMetricsInfo
	ActionPropertyMetrics      []MetricAggregate
	ActionMetricsVersion       uint32
	PropertyMetrics            []MetricAggregate
	RegionMetrics              []MetricAggregate
	Browser                    *BrowserMetrics
	BrowserByDevice            []BrowserDeviceMetrics
	Groups                     []GroupMetric
	LifecycleState             string
	LifecycleError             string
	Telemetry                  *MetricsTelemetry
	TransactionResponseTimeSLA []TransactionResponseTimeSLACompliance
	// HTTPVersionMix is the generator's run-to-date HTTP version mix; nil
	// from older generators or before any HTTP action.
	HTTPVersionMix *protocol.HTTPVersionMix
}

type TransactionResponseTimeSLACompliance struct {
	Group                string
	Transaction          string
	ThresholdMs          float64
	TotalCount           int64
	EligibleSuccessCount int64
	ExcludedErrorCount   int64
	MissedCount          int64
}

type MetricsTelemetry struct {
	// DroppedResultsUnavailable preserves source absence when controller metadata
	// creates telemetry or merges a cohort containing a legacy peer. It is not
	// sent over the generator wire protocol.
	DroppedResultsUnavailable  bool `json:"-"`
	DroppedResults             int64
	SheddedArrivals            int64
	CollectedAtUnixMs          int64
	PublishedAtUnixMs          int64
	ControllerReceivedAtUnixMs int64
	SampleAgeMs                float64
	GeneratorProcessingLagMs   float64
	TransportLagMs             float64
	DroppedSubscriberBatches   int64
	// TestDroppedSubscriberBatches counts this test's batches dropped for slow
	// subscribers in this process; TestDroppedSubscriberBatchesReported is
	// false for older generators that report only the process-wide count.
	TestDroppedSubscriberBatches         int64
	TestDroppedSubscriberBatchesReported bool
	// CollectionSequence identifies the collection that produced the batch's
	// window within its source epoch; a batch that re-sends the last window
	// repeats it. Zero from older generators.
	CollectionSequence uint64
	// The controller qualifies the cross-host stages on receipt:
	// TransportLagMs is corrected by the generator's clock offset when
	// TransportLagQuality is qualified, and CollectedAtOffsetMs is the offset
	// that maps CollectedAtUnixMs (generator clock) to the controller's.
	TransportLagQuality *ClockQuality `json:"transport_lag_quality,omitempty"`
	CollectedAtQuality  *ClockQuality `json:"collected_at_quality,omitempty"`
	CollectedAtOffsetMs float64       `json:"collected_at_offset_ms,omitempty"`
	// CollectedAtBoundUnixMs, on a fleet aggregate, is the earliest
	// controller-clock instant by which every qualified generator's oldest
	// sample was certainly collected (corrected time plus uncertainty); the
	// fleet's sample age is at least now minus it. Controller-internal.
	CollectedAtBoundUnixMs float64 `json:"-"`
	// ReceivedAt and ReceivedMonotonic are the controller's wall time and
	// monotonic reading at receipt: a controller wall-clock step after them
	// voids the receipt-time qualification of CollectedAt for later
	// publications. Controller-internal.
	ReceivedAt        time.Time     `json:"-"`
	ReceivedMonotonic time.Duration `json:"-"`
	// MissingMetricWindows is controller-computed from gaps in BatchSequence.
	// It is cumulative for the target stream and remains zero for older
	// generators that do not publish a sequence.
	MissingMetricWindows int64
	// WindowMs is the elapsed wall-clock duration this group's drained
	// transactions cover. It is generator-internal (merged to the batch's
	// window_ms) and is not transmitted on the telemetry proto message.
	WindowMs int64
}

type GroupMetric struct {
	Name         string
	ActiveVUsers int32
	Metrics      *MetricAggregate
}

type MetricAggregate struct {
	Messaging        *protocol.MessagingSummary
	MessagingVersion uint32
	Name             string
	DependencyType   string
	DependencyTarget string
	Count            int64
	Errors           int64
	AvgMs            float64
	P50Ms            float64
	P90Ms            float64
	P95Ms            float64
	P99Ms            float64
	AvgDNSMs         float64
	AvgConnectMs     float64
	AvgTLSMs         float64
	AvgTTFBMs        float64
	BytesSent        int64
	BytesReceived    int64
	TPS              float64
	DurationDigest   *engine.TDigestSnapshot
	Exemplars        []engine.TraceExemplar
}

type BrowserMetrics struct {
	Samples              int64
	AvgFCPMs             float64
	AvgLCPMs             float64
	AvgCLSScore          float64
	AvgINPMs             float64
	AvgTTFBMs            float64
	AvgBlockingResources float64
	AvgLongTaskCount     float64
	AvgJSHeapUsedMB      float64
	AvgBrowserProcessCPU float64
	AvgBrowserProcessMem float64
}

type BrowserDeviceMetrics struct {
	DeviceProfile  string
	DevicePlatform string
	DeviceTier     string
	BrowserEngine  string
	Metrics        BrowserMetrics
}

type ServerTelemetry struct {
	Messaging                 *protocol.MessagingDiagnostics
	SubscriptionID            string
	AcknowledgementMs         *float64
	ConsumerLagObserved       bool
	ConsumerOffsetLagObserved bool
	GRPC                      *protocol.GRPCDiagnostics
	WebSocket                 *protocol.WebSocketDiagnostics
	ServerProcessingMs        float64
	TransferMs                float64
	RequestPayloadKB          float64
	ResponsePayloadKB         float64
	ConnectionReused          bool
	RequestSizeClass          string
	ResponseSizeClass         string
	FirstEventMs              float64
	EventCount                int32
	MessageLatencyMs          float64
	MessagesSent              int32
	MessagesReceived          int32
	MessagesSentPerSec        float64
	MessagesReceivedPerSec    float64
	ConsumerLagMs             float64
	SendFailureCount          int32
	RedeliveryCount           int32
	DeadLetteredCount         int32
	DuplicateCount            int32
	OutOfOrderCount           int32
	CommitCount               int32
	AbortCount                int32
	ConsumerOffsetLag         int64
	ThrottledRequests         int32
	ServerBusyCount           int32
	QuotaExceededCount        int32
	EventTooLargeCount        int32
	EndToEndLatencyMs         float64
	PartitionID               string
}

type TransactionMetric struct {
	WorkerDurationMs float64
	Name             string
	StartTimeUnixMs  int64
	Group            string
	Region           string
	NetworkProfile   string
	Hierarchy        string
	Phase            string
	PageName         string
	DurationMs       float64
	DNSMs            float64
	ConnectMs        float64
	TLSMs            float64
	TTFBMs           float64
	StatusCode       int32
	Success          bool
	ErrorMessage     string
	BytesSent        int64
	BytesReceived    int64
	CorrelationID    string
	DependencyType   string
	DependencyTarget string
	SOAPOperation    string
	ServerTelemetry  *ServerTelemetry
	Snapshot         *protocol.ErrorSnapshot
}

type StopResult struct {
	Acknowledged  bool
	PendingVUsers int32
}

const (
	FeatureRunSupervisionV1  = "run_supervision_v1"
	RunSupervisionContractV1 = uint32(1)
	RunSupervisionTokenSize  = 32
)

type ArchiveArtifactRequest struct {
	TestID         string
	Name           string
	SourceIdentity string
}

// EvidenceAcknowledgement names a run archive's evidence artifacts the
// controller durably holds, each with its SHA-256.
type EvidenceAcknowledgement struct {
	TestID         string
	SourceIdentity string
	Artifacts      []AcknowledgedEvidence
}

type AcknowledgedEvidence struct {
	Name   string
	SHA256 string
}

// EvidenceAckResult lists the acknowledged artifacts the generator deleted
// and those it retained.
type EvidenceAckResult struct {
	Deleted  []string
	Retained []string
}

type Script struct {
	ID                  string
	ExecutionGeneration uint64
	Content             []byte
	ContentHash         string
}

type ScriptResult struct {
	Cached      bool
	ContentHash string
}

type ExecutionEpochResult struct {
	Accepted          bool
	CurrentGeneration uint64
	ExpiresAt         time.Time
	ErrorMessage      string
	// AdoptionSupported is false on generators that predate HA run adoption;
	// they fence every lower-generation run regardless of Adoptions.
	AdoptionSupported bool
	Adoptions         []RunAdoptionResult
}

// RunAdoption asks a generator to carry one run into a higher execution
// generation (see ClaimExecutionEpochWithAdoptions).
type RunAdoption struct {
	TestID          string
	PriorGeneration uint64
	PriorToken      []byte
	NewToken        []byte
	PriorProofs     []RunAdoptionProof
}

type RunAdoptionProof struct {
	Generation uint64
	Token      []byte
}

type RunAdoptionResult struct {
	TestID        string
	Outcome       pb.RunAdoptionOutcome
	Detail        string
	TerminalState string
}

type Heartbeat struct {
	GeneratorID     string
	TimestampUnixMs int64
	Stats           GeneratorStats
}

// Runtime occupancy modes distinguish mutually exclusive users of a shared
// protocol-specific process pool.
const (
	RuntimeOccupancyModeIdle       = "idle"
	RuntimeOccupancyModePersistent = "persistent"
	RuntimeOccupancyModeLegacy     = "legacy"
)

// ProtocolRuntimeCapacity is a heartbeat snapshot of a protocol-specific
// external runtime. It is independent from the generator's general MaxVUsers:
// a JVM sidecar may have substantially fewer process slots than the Go engine.
type ProtocolRuntimeCapacity struct {
	ResourceContract string                            `json:"resource_contract,omitempty"`
	Resources        *protocol.RuntimeResourceCapacity `json:"resources,omitempty"`
	Protocol         string                            `json:"protocol"`
	Available        bool                              `json:"available"`
	MaxVUsers        int64                             `json:"max_vusers"`
	AvailableVUsers  int64                             `json:"available_vusers"`
	Features         []string                          `json:"features,omitempty"`
	OccupancyMode    string                            `json:"occupancy_mode,omitempty"`
}

type HeartbeatResponse struct {
	JavaHost                       *pb.JavaHostCapabilities
	PodCapacity                    *pb.PodCapacitySnapshot
	GeneratorID                    string
	TimestampUnixMs                int64
	TimestampUnixNano              int64
	ActiveTestIDs                  []string
	Stats                          GeneratorStats
	AvailableCapacity              int32
	CPULimitCores                  float64
	MemoryLimitBytes               int64
	MaxVUsers                      int32
	Version                        string
	SupportedProtocols             []string
	CompatibleFeatures             []string
	RuntimeCapacities              []ProtocolRuntimeCapacity
	RunSupervisionExpirationsTotal uint64
	RunSupervisionEscalationsTotal uint64
	LastSafetyExit                 *GeneratorSafetyExitEvidence
	SafetyExits                    []GeneratorSafetyExitEvidence
}

// GeneratorSafetyExitEvidence is the durable, token-free terminal explanation
// advertised by a generator after its process safety backstop was required.
type GeneratorSafetyExitEvidence struct {
	TestID                string    `json:"test_id"`
	ExecutionGeneration   uint64    `json:"execution_generation"`
	LifecycleState        string    `json:"lifecycle_state"`
	Reason                string    `json:"reason"`
	RequestedAt           time.Time `json:"requested_at"`
	OutstandingExecutions int32     `json:"outstanding_executions"`
}

// FeatureEvacuationAssignmentStateV1 means AssignWork understands the
// evacuation-only StartPaused and DurationOffset fields. Controllers must not
// reshard a live run onto a generator that does not advertise this capability:
// protobuf's unknown-field compatibility would otherwise make an older
// generator silently start paused traffic or restart the full logical duration.
const FeatureEvacuationAssignmentStateV1 = "evacuation_assignment_state_v1"

// FeatureSecretBundleV1 means AssignWork accepts a bounded, memory-only map of
// referenced secret values and injects it as the assignment's runtime resolver.
const FeatureSecretBundleV1 = "secret_bundle_v1"

// FeatureMessageDetailCaptureV1 means the generator honors deterministic
// per-execution success-capture hints and enforces cumulative item/byte budgets
// in its archive recorder.
const FeatureMessageDetailCaptureV1 = "message_detail_capture_v1"

// FeatureGRPCDiagnosticsV1 advertises typed client RPC evidence with optional measurements.
const FeatureGRPCDiagnosticsV1 = "grpc_diagnostics_v1"

const FeatureControlParallelV1 = "control_parallel_v1"

// FeatureMessageDetailControlParallelV1 is advertised by generators that can
// enforce both contracts on the same immutable assignment.
const FeatureMessageDetailControlParallelV1 = "message_detail_control_parallel_v1"

type GeneratorStats struct {
	CompletedIterations int64
	ActiveVUsers        int32
	CPUPercent          float64
	// CPUProcessPctMax is the worst-pod process CPU% across the fleet. It is
	// populated controller-side during multi-generator aggregation; per a single
	// generator it is left zero (the per-pod value is CPUPercent).
	CPUProcessPctMax                  float64
	MemoryBytes                       int64
	GoroutineCount                    int32
	CPUUserPct                        float64
	CPUSystemPct                      float64
	CPUIOWaitPct                      float64
	CPUStealPct                       float64
	Load1                             float64
	Load5                             float64
	Load15                            float64
	ThreadCount                       int32
	OpenFDCount                       int32
	HeapAllocMB                       float64
	HeapInUseMB                       float64
	GCCountDelta                      uint32
	GCPauseMs                         float64
	ContainerCPULimitCores            float64
	ContainerCPUUsagePct              float64
	ContainerCPUThrottledPeriodsDelta uint64
	ContainerCPUThrottledSecondsRate  float64
	ContainerMemoryWorkingSetMB       float64
	ContainerMemoryLimitMB            float64
	ContainerMemoryUsagePct           float64
	ContainerMemoryFailCountDelta     uint64
	ContainerOOMKillCountDelta        uint64
	NetDropInPerSec                   float64
	NetDropOutPerSec                  float64
	NetErrorInPerSec                  float64
	NetErrorOutPerSec                 float64
	TCPRetransmitsPerSec              float64
	TCPEstablished                    int32
	TCPTimeWait                       int32
	EphemeralPortUtilizationPct       float64
	DiskIOTimePct                     float64
	DiskWeightedIOTimePct             float64
	DiskInProgress                    uint64
}

type sourcePlanClientKey struct {
	testID     string
	generation uint64
}

type Client struct {
	sourcePlans sync.Map
	conn        *grpc.ClientConn
	rpc         pb.GeneratorControlClient
	connection  *sharedClientConnection
	closeMu     sync.Mutex
	closed      bool

	clockMu       sync.RWMutex
	clockEstimate generatorClockEstimate
}

type sharedClientConnection struct {
	runtimeResources         bool
	runtimeResourcesObserved time.Time
	javaHostCaps             *pb.JavaHostCapabilities
	javaHostObserved         time.Time
	javaHostPrepared         bool
	mu                       sync.Mutex
	refs                     int
	closed                   bool
	onClose                  func()
}

type HeartbeatStream struct {
	stream pb.GeneratorControl_HeartbeatClient
	owner  *Client

	clockMu           sync.Mutex
	clockSentAt       time.Time
	clockPingUnixMS   int64
	clockSampleActive bool
	lastClock         generatorClockEstimate
	lastRoundTrip     time.Duration
}

// ClockSample is one heartbeat's measurement of a generator's clock: the
// generator's clock minus the controller's, the exchange's round trip, and the
// uncertainty that bounds the offset error (half the round trip).
type ClockSample struct {
	Offset      time.Duration
	RoundTrip   time.Duration
	Uncertainty time.Duration
	ObservedAt  time.Time
}

// ClockQuality qualifies a duration measured across two hosts' clocks. It is
// qualified when a current calibration of the source's clock offset
// corrected it; UncertaintyMs bounds the remaining error. Reason says why an
// unqualified duration could not be corrected.
type ClockQuality struct {
	Qualified     bool    `json:"qualified"`
	UncertaintyMs float64 `json:"uncertainty_ms,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	// LowerBoundMs, on a figure drawn from several generators, is the
	// largest value the qualified ones prove (corrected value minus its
	// uncertainty): a definite breach stays visible when another generator
	// is unqualified or less certain.
	LowerBoundMs *float64 `json:"lower_bound_ms,omitempty"`
}

// ClockProbeGeneratorID names the controller's clock-probe heartbeats.
const ClockProbeGeneratorID = "controller-clock-probe"

type generatorClockEstimate struct {
	offset      time.Duration
	uncertainty time.Duration
	observedAt  time.Time
	known       bool
}

const (
	coordinatedStartClockEstimateMaxAge         = 5 * time.Minute
	coordinatedStartClockEstimateMaxUncertainty = 250 * time.Millisecond
)

type MetricsStream struct {
	stream    pb.GeneratorControl_StreamMetricsClient
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

func Dial(ctx context.Context, target string, opts ...grpc.DialOption) (*Client, error) {
	// Transport security (TLS/mTLS/bearer token) comes from the APEXLOAD_GRPC_*
	// env contract; plaintext without a token is the unconfigured fallback.
	generatorID, reverse := ReverseGeneratorID(target)
	dialOptions, err := dialOptionsFromEnv(reverse)
	if err != nil {
		return nil, fmt.Errorf("generator transport config: %w", err)
	}
	dialTarget := target
	registry := DefaultReverseRegistry
	if reverse {
		dialTarget = "passthrough:///reverse-" + generatorID
		dialOptions = append(dialOptions, grpc.WithContextDialer(func(dialCtx context.Context, _ string) (net.Conn, error) {
			return registry.Dial(dialCtx, generatorID)
		}))
	}
	dialOptions = append(dialOptions, opts...)

	create := func() (*Client, error) {
		conn, err := grpc.DialContext(ctx, dialTarget, dialOptions...)
		if err != nil {
			return nil, err
		}
		return NewClient(conn), nil
	}
	// The enrolled tunnel is one physical byte stream. All default controller
	// users must multiplex on the same HTTP/2 connection instead of consuming
	// a second tunnel for inventory, another run, or execution-epoch renewal.
	// Explicit dial options keep their existing private-transport semantics.
	if reverse && len(opts) == 0 {
		return registry.sharedClient(ctx, generatorID, create)
	}
	return create()
}

func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{
		conn:       conn,
		rpc:        pb.NewGeneratorControlClient(conn),
		connection: &sharedClientConnection{refs: 1},
	}
}

// Retain returns an independently closeable client on the same transport. This
// lets a batched adoption hand each run its own lifetime, including on a reverse
// connection where opening another physical connection is not possible.
func (c *Client) Retain() (*Client, error) {
	if c == nil {
		return nil, fmt.Errorf("generator client is unavailable")
	}
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed || c.connection == nil {
		return nil, fmt.Errorf("generator client is closed or has no shareable connection")
	}
	c.connection.mu.Lock()
	if c.connection.closed {
		c.connection.mu.Unlock()
		return nil, fmt.Errorf("generator client connection is closed")
	}
	c.connection.refs++
	c.connection.mu.Unlock()
	return &Client{conn: c.conn, rpc: c.rpc, connection: c.connection}, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.sourcePlans.Clear()
	var onClose func()
	if c.connection != nil {
		c.connection.mu.Lock()
		c.connection.refs--
		last := c.connection.refs == 0
		if last {
			c.connection.closed = true
			onClose = c.connection.onClose
		}
		c.connection.mu.Unlock()
		if !last {
			return nil
		}
	}
	if onClose != nil {
		defer onClose()
	}
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) DistributeScript(ctx context.Context, script Script) (ScriptResult, error) {
	ack, err := c.rpc.DistributeScript(ctx, &pb.ScriptPayload{
		ScriptId:            script.ID,
		Content:             script.Content,
		ContentHash:         script.ContentHash,
		ExecutionGeneration: script.ExecutionGeneration,
	})
	if err != nil {
		return ScriptResult{}, err
	}

	return ScriptResult{
		Cached:      ack.GetCached(),
		ContentHash: ack.GetContentHash(),
	}, nil
}

// ClaimExecutionEpoch establishes or renews the controller's execution term
// on a generator. A generator accepts test mutations only for the exact active
// generation until ttl expires.
func (c *Client) ClaimExecutionEpoch(ctx context.Context, generation uint64, leaseOwnerID string, ttl time.Duration) (ExecutionEpochResult, error) {
	return c.claimExecutionEpoch(ctx, generation, leaseOwnerID, ttl, "")
}

// ClaimExecutionEpochWithAdoptions claims generation and, atomically with it,
// adopts orphaned runs from a lost predecessor. It must be the first claim of
// the generation on this generator: a higher generation fences every
// lower-generation run it does not adopt.
func (c *Client) ClaimExecutionEpochWithAdoptions(ctx context.Context, generation uint64, leaseOwnerID string, ttl time.Duration, adoptions []RunAdoption) (ExecutionEpochResult, error) {
	return c.claimExecutionEpoch(ctx, generation, leaseOwnerID, ttl, "", adoptions...)
}

// ClaimTestExecutionEpoch establishes or renews the volatile lease for one
// assignment. The explicit acknowledgement prevents an older server, which
// ignores the additive test_id field, from being treated as loss-fence capable.
func (c *Client) ClaimTestExecutionEpoch(ctx context.Context, testID string, generation uint64, leaseOwnerID string, ttl time.Duration) (ExecutionEpochResult, error) {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return ExecutionEpochResult{}, fmt.Errorf("test id is required")
	}
	return c.claimExecutionEpoch(ctx, generation, leaseOwnerID, ttl, testID)
}

func (c *Client) ClaimRunSupervision(ctx context.Context, claim RunSupervisionClaim) (RunSupervisionResult, error) {
	if strings.TrimSpace(claim.TestID) == "" {
		return RunSupervisionResult{}, fmt.Errorf("test id is required")
	}
	if len(claim.Token) != RunSupervisionTokenSize {
		return RunSupervisionResult{}, fmt.Errorf("run supervision token must be %d bytes", RunSupervisionTokenSize)
	}
	if claim.RequestedTTL <= 0 || claim.RequestedTTL.Milliseconds() <= 0 {
		return RunSupervisionResult{}, fmt.Errorf("run supervision ttl must be positive")
	}
	ack, err := c.rpc.ClaimRunSupervision(ctx, &pb.RunSupervisionClaim{
		TestId:                   strings.TrimSpace(claim.TestID),
		ExecutionGeneration:      claim.ExecutionGeneration,
		RunSupervisionToken:      append([]byte(nil), claim.Token...),
		RequestedTtlMilliseconds: claim.RequestedTTL.Milliseconds(),
		ExecutionLeaseOwnerId:    strings.TrimSpace(claim.ExecutionLeaseOwnerID),
	})
	if err != nil {
		return RunSupervisionResult{}, err
	}
	return RunSupervisionResult{
		Accepted:          ack.GetAccepted(),
		ContractVersion:   ack.GetContractVersion(),
		CurrentGeneration: ack.GetCurrentGeneration(),
		GrantedTTL:        time.Duration(ack.GetGrantedTtlMilliseconds()) * time.Millisecond,
		ExpiresAt:         time.UnixMilli(ack.GetExpiresAtUnixMs()),
		State:             ack.GetState(),
		ErrorMessage:      ack.GetErrorMessage(),
	}, nil
}

func (c *Client) claimExecutionEpoch(ctx context.Context, generation uint64, leaseOwnerID string, ttl time.Duration, testID string, adoptions ...RunAdoption) (ExecutionEpochResult, error) {
	if ttl <= 0 {
		return ExecutionEpochResult{}, fmt.Errorf("execution epoch ttl must be positive")
	}
	if err := validateExecutionLeaseOwner(generation, leaseOwnerID, true); err != nil {
		return ExecutionEpochResult{}, fmt.Errorf("execution lease owner: %w", err)
	}
	if ttl > time.Duration(^uint64(0)>>1) {
		return ExecutionEpochResult{}, fmt.Errorf("execution epoch ttl is too large")
	}
	protoAdoptions := make([]*pb.RunAdoption, 0, len(adoptions))
	for _, adoption := range adoptions {
		proofs := make([]*pb.RunAdoptionProof, 0, len(adoption.PriorProofs))
		for _, proof := range adoption.PriorProofs {
			proofs = append(proofs, &pb.RunAdoptionProof{Generation: proof.Generation, RunSupervisionToken: append([]byte(nil), proof.Token...)})
		}
		protoAdoptions = append(protoAdoptions, &pb.RunAdoption{
			TestId:                   strings.TrimSpace(adoption.TestID),
			PriorGeneration:          adoption.PriorGeneration,
			PriorRunSupervisionToken: append([]byte(nil), adoption.PriorToken...),
			NewRunSupervisionToken:   append([]byte(nil), adoption.NewToken...),
			PriorProofs:              proofs,
		})
	}
	ack, err := c.rpc.ClaimExecutionEpoch(ctx, &pb.ExecutionEpochClaim{
		ExecutionGeneration:   generation,
		LeaseTtlMilliseconds:  ttl.Milliseconds(),
		ExecutionLeaseOwnerId: strings.TrimSpace(leaseOwnerID),
		TestId:                testID,
		Adoptions:             protoAdoptions,
	})
	if err != nil {
		return ExecutionEpochResult{}, err
	}
	result := ExecutionEpochResult{
		Accepted:          ack.GetAccepted(),
		CurrentGeneration: ack.GetCurrentGeneration(),
		ExpiresAt:         time.UnixMilli(ack.GetExpiresAtUnixMs()),
		ErrorMessage:      ack.GetErrorMessage(),
		AdoptionSupported: ack.GetAdoptionSupported(),
	}
	for _, adoption := range ack.GetAdoptions() {
		result.Adoptions = append(result.Adoptions, RunAdoptionResult{
			TestID: adoption.GetTestId(), Outcome: adoption.GetOutcome(),
			Detail: adoption.GetDetail(), TerminalState: adoption.GetTerminalState(),
		})
	}
	if testID != "" && !ack.GetTestLeaseAccepted() {
		return result, fmt.Errorf("generator does not acknowledge assignment-scoped execution leases")
	}
	return result, nil
}

func (c *Client) AssignWork(ctx context.Context, assignment Assignment) (AssignmentResult, error) {
	request := toProtoAssignment(assignment)
	if err := validateJavaHostSelection(request); err != nil {
		return AssignmentResult{}, err
	}
	if request.GetJavaHost() != nil && !c.javaHostSelectionNegotiated(request) {
		return AssignmentResult{}, fmt.Errorf("java_host_capability_not_negotiated")
	}
	if err := validateRuntimeResourceClaim(request.RuntimeResourceClaim, request.VuserCount); err != nil {
		return AssignmentResult{}, err
	}
	if request.RuntimeResourceClaim != nil && !c.runtimeResourcesNegotiated() {
		return AssignmentResult{}, fmt.Errorf("runtime_resource_capability_not_negotiated")
	}
	ack, err := c.rpc.AssignWork(ctx, request)
	if err == nil && ack.GetAccepted() && request.RuntimeResourceClaim != nil && !ValidRuntimeResourceAdmission(request.RuntimeResourceClaim, ack.GetRuntimeResourceAdmission()) {
		return AssignmentResult{}, fmt.Errorf("runtime_resource_negotiation_failed")
	}
	if err == nil && ack.GetAccepted() && request.GetJavaHost() != nil {
		placement := ack.GetJavaHost()
		if placement == nil || !proto.Equal(placement.GetSelection(), request.GetJavaHost()) || placement.ReservationId == "" || len(placement.ReservationId) > 128 || placement.Mode != "dedicated" || placement.Containment != "execution_pod" || placement.MaxHosts < javaHostPreparedVUsers(request) || placement.MaxVusers < javaHostPreparedVUsers(request) {
			return AssignmentResult{}, fmt.Errorf("java_host_negotiation_failed")
		}
	}
	if err == nil && ack.GetAccepted() && assignment.SourcePlan != nil {
		c.sourcePlans.Store(sourcePlanClientKey{assignment.TestID, assignment.ExecutionGeneration}, SourcePlanDigest(assignment.SourcePlan))
	}
	if err != nil {
		return AssignmentResult{}, err
	}

	return AssignmentResult{
		RuntimeResourceAdmission: CloneRuntimeResourceAdmission(ack.GetRuntimeResourceAdmission()),
		JavaHost:                 cloneJavaHostPlacement(ack.GetJavaHost()),
		Accepted:                 ack.GetAccepted(),
		ErrorMessage:             ack.GetErrorMessage(),
		AvailableCapacity:        ack.GetAvailableCapacity(),
	}, nil
}

func (c *Client) CommitWork(ctx context.Context, testID string, generation uint64) (WorkCommitResult, error) {
	return c.CommitWorkAt(ctx, testID, generation, time.Time{})
}

func (c *Client) CommitWorkAt(ctx context.Context, testID string, generation uint64, startAt time.Time) (WorkCommitResult, error) {
	return c.commitWorkAt(ctx, testID, generation, startAt, nil)
}

func (c *Client) CommitWorkSupervisedAt(ctx context.Context, testID string, generation uint64, startAt time.Time, token []byte) (WorkCommitResult, error) {
	if len(token) != RunSupervisionTokenSize {
		return WorkCommitResult{}, fmt.Errorf("run supervision token must be %d bytes", RunSupervisionTokenSize)
	}
	return c.commitWorkAt(ctx, testID, generation, startAt, token)
}

func (c *Client) commitWorkAt(ctx context.Context, testID string, generation uint64, startAt time.Time, token []byte) (WorkCommitResult, error) {
	request := &pb.WorkCommitRequest{TestId: testID, ExecutionGeneration: generation}
	if digest, ok := c.sourcePlans.Load(sourcePlanClientKey{testID, generation}); ok {
		request.SourcePlanDigest = digest.(string)
	}
	request.RunSupervisionToken = append([]byte(nil), token...)
	if !startAt.IsZero() {
		clockEstimate, err := c.ensureCoordinatedStartClockEstimate(ctx)
		if err != nil {
			return WorkCommitResult{}, err
		}
		request.StartAtUnixNano = startAt.UnixNano()
		// Keep sent_at for rolling compatibility, but current generators translate
		// the absolute controller barrier with the heartbeat-derived clock offset.
		// The deadline therefore does not move later by this RPC's one-way transit.
		ctx = outgoingCoordinatedStartContextWithClockEstimate(
			ctx,
			time.Now(),
			clockEstimate.offset,
			clockEstimate.uncertainty,
		)
	}
	ack, err := c.rpc.CommitWork(ctx, request)
	if err != nil {
		return WorkCommitResult{}, err
	}
	return WorkCommitResult{
		Accepted:            ack.GetAccepted(),
		ErrorMessage:        ack.GetErrorMessage(),
		RunSupervisionArmed: ack.GetRunSupervisionArmed(),
		RunSupervisionTTL:   time.Duration(ack.GetRunSupervisionTtlMilliseconds()) * time.Millisecond,
	}, nil
}

// generatorClockSample estimates a generator's offset from one heartbeat: its
// pong timestamp against the midpoint of the controller's send and receive.
func generatorClockSample(controllerSentAt, controllerReceivedAt, generatorAt time.Time) (generatorClockEstimate, bool) {
	if controllerSentAt.IsZero() || controllerReceivedAt.Before(controllerSentAt) || generatorAt.IsZero() {
		return generatorClockEstimate{}, false
	}
	roundTrip := controllerReceivedAt.Sub(controllerSentAt)
	midpoint := controllerSentAt.Add(roundTrip / 2)
	return generatorClockEstimate{
		offset:      generatorAt.Sub(midpoint),
		uncertainty: roundTrip / 2,
		observedAt:  controllerReceivedAt,
		known:       true,
	}, true
}

func (c *Client) observeGeneratorClock(controllerSentAt, controllerReceivedAt, generatorAt time.Time) {
	estimate, ok := generatorClockSample(controllerSentAt, controllerReceivedAt, generatorAt)
	if c == nil || !ok {
		return
	}
	c.clockMu.Lock()
	current := c.clockEstimate
	// Preserve a more precise recent sample when an unrelated heartbeat is
	// briefly delayed. A stale estimate is always replaced so clock correction
	// can follow long-lived drift.
	if !current.known || controllerReceivedAt.Sub(current.observedAt) > coordinatedStartClockEstimateMaxAge || estimate.uncertainty <= current.uncertainty {
		c.clockEstimate = estimate
	}
	c.clockMu.Unlock()
}

func (c *Client) coordinatedStartClockEstimate(now time.Time) (generatorClockEstimate, bool) {
	if c == nil {
		return generatorClockEstimate{}, false
	}
	c.clockMu.RLock()
	estimate := c.clockEstimate
	c.clockMu.RUnlock()
	if !estimate.known || estimate.observedAt.IsZero() {
		return generatorClockEstimate{}, false
	}
	age := now.Sub(estimate.observedAt)
	if age < 0 || age > coordinatedStartClockEstimateMaxAge ||
		estimate.offset < -maxCoordinatedStartClockOffset || estimate.offset > maxCoordinatedStartClockOffset ||
		estimate.uncertainty < 0 || estimate.uncertainty > coordinatedStartClockEstimateMaxUncertainty {
		return generatorClockEstimate{}, false
	}
	return estimate, true
}

func (c *Client) ensureCoordinatedStartClockEstimate(ctx context.Context) (generatorClockEstimate, error) {
	if estimate, ok := c.coordinatedStartClockEstimate(time.Now()); ok {
		return estimate, nil
	}
	if c == nil || c.rpc == nil {
		return generatorClockEstimate{}, fmt.Errorf("coordinated start clock probe is unavailable")
	}
	heartbeat, err := c.OpenHeartbeat(ctx)
	if err != nil {
		return generatorClockEstimate{}, fmt.Errorf("open coordinated start clock probe: %w", err)
	}
	defer heartbeat.CloseSend()
	// Zero is deliberate for rolling compatibility: legacy generators return
	// max(generatorNow,pingTimestamp), so a controller timestamp here would hide
	// a generator clock that is behind the controller.
	if err := heartbeat.Send(Heartbeat{GeneratorID: ClockProbeGeneratorID}); err != nil {
		return generatorClockEstimate{}, fmt.Errorf("send coordinated start clock probe: %w", err)
	}
	if _, err := heartbeat.Receive(); err != nil {
		return generatorClockEstimate{}, fmt.Errorf("receive coordinated start clock probe: %w", err)
	}
	estimate, ok := c.coordinatedStartClockEstimate(time.Now())
	if !ok {
		return generatorClockEstimate{}, fmt.Errorf(
			"coordinated start clock estimate is unavailable, outside the %s offset bound, or exceeds %s uncertainty",
			maxCoordinatedStartClockOffset,
			coordinatedStartClockEstimateMaxUncertainty,
		)
	}
	return estimate, nil
}

func (c *Client) AdjustVUsers(ctx context.Context, adjustment VUserAdjustment) (AdjustmentResult, error) {
	ack, err := c.rpc.AdjustVUsers(ctx, &pb.VUserAdjustment{
		TestId:              adjustment.TestID,
		ExecutionGeneration: adjustment.ExecutionGeneration,
		GroupName:           adjustment.GroupName,
		TargetVusers:        adjustment.TargetVUsers,
		RampSeconds:         adjustment.RampSeconds,
		RampDuration:        preciseDurationProto(adjustment.RampDuration, int64(adjustment.RampSeconds)),
		RunSupervisionToken: append([]byte(nil), adjustment.RunSupervisionToken...),
	})
	if err != nil {
		if isInitialRampInProgressStatus(err) {
			return AdjustmentResult{}, &initialRampInProgressRPCError{cause: err}
		}
		return AdjustmentResult{}, err
	}

	return AdjustmentResult{
		Accepted:      ack.GetAccepted(),
		CurrentVUsers: ack.GetCurrentVusers(),
	}, nil
}

func (c *Client) AdjustDuration(ctx context.Context, adjustment DurationAdjustment) (DurationResult, error) {
	targetDuration := resolvedPreciseDuration(adjustment.TargetDuration, adjustment.TargetDurationSeconds)
	ack, err := c.rpc.AdjustDuration(ctx, &pb.DurationAdjustment{
		TestId:                adjustment.TestID,
		ExecutionGeneration:   adjustment.ExecutionGeneration,
		TargetDurationSeconds: compatibilityDurationSeconds(targetDuration),
		TargetDuration:        preciseDurationProto(targetDuration, 0),
		RunSupervisionToken:   append([]byte(nil), adjustment.RunSupervisionToken...),
	})
	if err != nil {
		return DurationResult{}, err
	}

	return DurationResult{
		Accepted:               ack.GetAccepted(),
		CurrentDurationSeconds: ack.GetCurrentDurationSeconds(),
		CurrentDuration:        durationFromProtoOrSeconds(ack.GetCurrentDuration(), ack.GetCurrentDurationSeconds()),
	}, nil
}

// SetPaused is the acknowledged control path. Unlike the legacy metrics-stream
// signal, its result identifies the applied engine state and operation. Explicit
// strict requests additionally require committed runtime quiescence proof.
// OperationID makes retries after a lost response idempotent.
func (c *Client) SetPaused(ctx context.Context, control PauseControl) (PauseResult, error) {
	if err := validateStrictPauseRequest(control); err != nil {
		return PauseResult{}, err
	}
	ack, err := c.rpc.SetPaused(ctx, &pb.PauseRequest{
		RequireQuiescence:   control.RequireQuiescence,
		TestId:              control.TestID,
		ExecutionGeneration: control.ExecutionGeneration,
		Pause:               control.Pause,
		OperationId:         control.OperationID,
		OperationSequence:   control.OperationSequence,
		RunSupervisionToken: append([]byte(nil), control.RunSupervisionToken...),
	})
	if err != nil {
		return PauseResult{}, err
	}
	result := pauseResultFromProto(ack)
	return result, ValidatePauseAcknowledgement(control, result)
}

func (c *Client) OpenMetricsStream(ctx context.Context) (*MetricsStream, error) {
	return c.OpenMetricsStreamForGeneration(ctx, 0)
}

func (c *Client) OpenMetricsStreamForGeneration(ctx context.Context, generation uint64) (*MetricsStream, error) {
	return c.openMetricsStreamForTestGeneration(ctx, "", generation)
}

func (c *Client) OpenMetricsStreamForTestGeneration(ctx context.Context, testID string, generation uint64) (*MetricsStream, error) {
	if strings.TrimSpace(testID) == "" {
		return nil, fmt.Errorf("metrics stream test id is required")
	}
	return c.openMetricsStreamForTestGeneration(ctx, testID, generation)
}

func (c *Client) openMetricsStreamForTestGeneration(ctx context.Context, testID string, generation uint64) (*MetricsStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = outgoingExecutionGenerationContext(ctx, generation)
	ctx = outgoingMetricsTestContext(ctx, testID)
	lifetimeCtx, cancel := context.WithCancel(ctx)
	stream, err := c.rpc.StreamMetrics(lifetimeCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	awaitMetricsSubscription(ctx, stream, metricsSubscriptionWait)
	return &MetricsStream{stream: stream, cancel: cancel}, nil
}

// metricsSubscribedHeader is the header a generator sends once a metrics
// stream's subscription is installed.
const metricsSubscribedHeader = "apexload-metrics-subscribed"

// metricsSubscriptionWait bounds the wait for that header; an older
// generator sends none until its first publication.
const metricsSubscriptionWait = 5 * time.Second

// awaitMetricsSubscription returns once the generator confirms the stream's
// subscription (or its first publication's headers arrive), so the run is
// activated only after the controller is subscribed and no metric window can
// be published before it. Past wait it returns anyway.
func awaitMetricsSubscription(ctx context.Context, stream pb.GeneratorControl_StreamMetricsClient, wait time.Duration) {
	done := make(chan struct{})
	go func() {
		_, _ = stream.Header()
		close(done)
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// OpenMetricsStreamWithSetupTimeout bounds creation of the gRPC stream without
// attaching the setup deadline to the stream's lifetime. Once setup succeeds,
// the stream remains live until its parent context is canceled or CloseSend is
// called.
func (c *Client) OpenMetricsStreamWithSetupTimeout(parent context.Context, setupTimeout time.Duration) (*MetricsStream, error) {
	return c.OpenMetricsStreamWithSetupTimeoutForGeneration(parent, setupTimeout, 0)
}

func (c *Client) OpenMetricsStreamWithSetupTimeoutForGeneration(parent context.Context, setupTimeout time.Duration, generation uint64) (*MetricsStream, error) {
	return c.openMetricsStreamWithSetupTimeoutForTestGeneration(parent, setupTimeout, "", generation)
}

func (c *Client) OpenMetricsStreamWithSetupTimeoutForTestGeneration(parent context.Context, setupTimeout time.Duration, testID string, generation uint64) (*MetricsStream, error) {
	if strings.TrimSpace(testID) == "" {
		return nil, fmt.Errorf("metrics stream test id is required")
	}
	return c.openMetricsStreamWithSetupTimeoutForTestGeneration(parent, setupTimeout, testID, generation)
}

func (c *Client) openMetricsStreamWithSetupTimeoutForTestGeneration(parent context.Context, setupTimeout time.Duration, testID string, generation uint64) (*MetricsStream, error) {
	if parent == nil {
		parent = context.Background()
	}
	if setupTimeout <= 0 {
		return c.openMetricsStreamForTestGeneration(parent, testID, generation)
	}

	parent = outgoingExecutionGenerationContext(parent, generation)
	parent = outgoingMetricsTestContext(parent, testID)
	lifetimeCtx, cancel := context.WithCancel(parent)
	type openResult struct {
		stream pb.GeneratorControl_StreamMetricsClient
		err    error
	}
	resultCh := make(chan openResult, 1)
	go func() {
		stream, err := c.rpc.StreamMetrics(lifetimeCtx)
		resultCh <- openResult{stream: stream, err: err}
	}()

	start := time.Now()
	timer := time.NewTimer(setupTimeout)
	defer timer.Stop()
	select {
	case <-parent.Done():
		cancel()
		return nil, parent.Err()
	case <-timer.C:
		cancel()
		return nil, fmt.Errorf("open metrics stream setup: %w", context.DeadlineExceeded)
	case result := <-resultCh:
		if result.err != nil {
			cancel()
			return nil, result.err
		}
		if remaining := time.Until(start.Add(setupTimeout)); remaining > 0 {
			awaitMetricsSubscription(parent, result.stream, min(remaining, metricsSubscriptionWait))
		}
		return &MetricsStream{stream: result.stream, cancel: cancel}, nil
	}
}

func (c *Client) StopTest(ctx context.Context, testID string, graceful bool) (StopResult, error) {
	return c.StopTestAtGeneration(ctx, testID, graceful, 0)
}

func (c *Client) StopTestAtGeneration(ctx context.Context, testID string, graceful bool, generation uint64) (StopResult, error) {
	return c.StopTestAtGenerationWithToken(ctx, testID, graceful, generation, nil)
}

func (c *Client) StopTestAtGenerationWithToken(ctx context.Context, testID string, graceful bool, generation uint64, token []byte) (StopResult, error) {
	ack, err := c.rpc.StopTest(ctx, &pb.StopRequest{
		TestId: testID, Graceful: graceful, ExecutionGeneration: generation,
		RunSupervisionToken: append([]byte(nil), token...),
	})
	if err != nil {
		return StopResult{}, err
	}

	if ack.GetAcknowledged() {
		c.sourcePlans.Range(func(key, _ any) bool {
			source := key.(sourcePlanClientKey)
			if source.testID == testID && (generation == 0 || source.generation == generation) {
				c.sourcePlans.Delete(key)
			}
			return true
		})
	}
	return StopResult{
		Acknowledged:  ack.GetAcknowledged(),
		PendingVUsers: ack.GetPendingVusers(),
	}, nil
}

func (c *Client) GetArchiveArtifact(ctx context.Context, request ArchiveArtifactRequest) ([]byte, error) {
	artifact, err := c.rpc.GetArchiveArtifact(
		ctx,
		&pb.ArchiveArtifactRequest{TestId: request.TestID, Name: request.Name, SourceIdentity: request.SourceIdentity},
		grpc.MaxCallRecvMsgSize(grpcMessageSize(archiveArtifactUnaryReceiveMaxSize)),
	)
	if err != nil {
		return nil, err
	}
	if request.SourceIdentity != "" && artifact.GetSourceIdentity() != request.SourceIdentity {
		return nil, fmt.Errorf("generator did not confirm the requested archive source identity")
	}
	return artifact.GetContent(), nil
}

// StreamArchiveArtifact copies an artifact to destination while keeping memory
// bounded to one server chunk. maxBytes is a mandatory controller-side safety
// limit; total-size and offset checks reject incomplete or reordered streams.
func (c *Client) StreamArchiveArtifact(ctx context.Context, request ArchiveArtifactRequest, destination io.Writer, maxBytes int64) (int64, error) {
	if destination == nil {
		return 0, fmt.Errorf("archive artifact destination is required")
	}
	if maxBytes <= 0 {
		return 0, fmt.Errorf("archive artifact max bytes must be positive")
	}
	stream, err := c.rpc.StreamArchiveArtifact(ctx, &pb.ArchiveArtifactRequest{TestId: request.TestID, Name: request.Name, SourceIdentity: request.SourceIdentity})
	if err != nil {
		return 0, err
	}

	var (
		written      int64
		declaredSize int64 = -1
	)
	for {
		chunk, recvErr := stream.Recv()
		if recvErr == io.EOF {
			return written, fmt.Errorf("archive artifact stream ended before eof at %d of %d bytes", written, declaredSize)
		}
		if recvErr != nil {
			return written, recvErr
		}
		if request.SourceIdentity != "" && chunk.GetSourceIdentity() != request.SourceIdentity {
			return written, fmt.Errorf("generator did not confirm the requested archive source identity")
		}
		if chunk.GetName() != request.Name {
			return written, fmt.Errorf("archive artifact stream name %q does not match %q", chunk.GetName(), request.Name)
		}
		if chunk.GetTotalSize() < 0 {
			return written, fmt.Errorf("archive artifact stream declared a negative size")
		}
		if declaredSize < 0 {
			declaredSize = chunk.GetTotalSize()
			if declaredSize > maxBytes {
				return written, fmt.Errorf("archive artifact is %d bytes, limit is %d", declaredSize, maxBytes)
			}
		} else if chunk.GetTotalSize() != declaredSize {
			return written, fmt.Errorf("archive artifact total size changed from %d to %d", declaredSize, chunk.GetTotalSize())
		}
		if chunk.GetOffset() != written {
			return written, fmt.Errorf("archive artifact chunk offset is %d, expected %d", chunk.GetOffset(), written)
		}
		content := chunk.GetContent()
		if len(content) == 0 && !chunk.GetEof() {
			return written, fmt.Errorf("archive artifact stream made no progress at offset %d", written)
		}
		if int64(len(content)) > maxBytes-written || int64(len(content)) > declaredSize-written {
			return written, fmt.Errorf("archive artifact chunk exceeds declared or configured size")
		}
		if len(content) > 0 {
			n, writeErr := destination.Write(content)
			written += int64(n)
			if writeErr != nil {
				return written, writeErr
			}
			if n != len(content) {
				return written, io.ErrShortWrite
			}
		}
		if chunk.GetEof() {
			if written != declaredSize {
				return written, fmt.Errorf("archive artifact eof at %d of %d bytes", written, declaredSize)
			}
			return written, nil
		}
		if written == declaredSize {
			return written, fmt.Errorf("archive artifact reached declared size without eof")
		}
	}
}

func (c *Client) OpenHeartbeat(ctx context.Context) (*HeartbeatStream, error) {
	stream, err := c.rpc.Heartbeat(ctx)
	if err != nil {
		return nil, err
	}

	return &HeartbeatStream{stream: stream, owner: c}, nil
}

func (s *HeartbeatStream) Send(heartbeat Heartbeat) error {
	sentAt := time.Now()
	s.clockMu.Lock()
	s.clockSentAt = sentAt
	s.clockPingUnixMS = heartbeat.TimestampUnixMs
	s.clockSampleActive = true
	s.clockMu.Unlock()
	err := s.stream.Send(&pb.HeartbeatPing{
		GeneratorId:     heartbeat.GeneratorID,
		TimestampUnixMs: heartbeat.TimestampUnixMs,
		Stats:           toProtoStats(heartbeat.Stats),
	})
	if err != nil {
		s.clockMu.Lock()
		s.clockSampleActive = false
		s.clockMu.Unlock()
	}
	return err
}

// ClockSample returns the clock measurement of this stream's latest exchange,
// if its pong carried a generator timestamp.
func (s *HeartbeatStream) ClockSample() (ClockSample, bool) {
	if s == nil {
		return ClockSample{}, false
	}
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	if !s.lastClock.known {
		return ClockSample{}, false
	}
	return ClockSample{Offset: s.lastClock.offset, RoundTrip: s.lastRoundTrip, Uncertainty: s.lastClock.uncertainty, ObservedAt: s.lastClock.observedAt}, true
}

func (s *HeartbeatStream) Receive() (HeartbeatResponse, error) {
	pong, err := s.stream.Recv()
	receivedAt := time.Now()
	if err != nil {
		if s.owner != nil {
			s.owner.observeJavaHosts(nil)
			s.owner.observeRuntimeResources(nil)
		}
		return HeartbeatResponse{}, err
	}
	if s.owner != nil {
		s.owner.observeJavaHosts(pong)
		s.owner.observeRuntimeResources(pong)
	}
	s.clockMu.Lock()
	sentAt := s.clockSentAt
	pingUnixMS := s.clockPingUnixMS
	sampleActive := s.clockSampleActive
	s.clockSampleActive = false
	s.clockMu.Unlock()
	if sampleActive && s.owner != nil {
		generatorAt := time.Time{}
		if pong.GetTimestampUnixNano() > 0 {
			generatorAt = time.Unix(0, pong.GetTimestampUnixNano())
		} else if pingUnixMS == 0 && pong.GetTimestampUnixMs() > 0 {
			// An older generator has only millisecond precision. A zero ping
			// timestamp prevents its legacy max(now,ping) response behavior from
			// substituting the controller clock for the generator clock.
			generatorAt = time.UnixMilli(pong.GetTimestampUnixMs())
		}
		if !generatorAt.IsZero() {
			s.owner.observeGeneratorClock(sentAt, receivedAt, generatorAt)
			if sample, ok := generatorClockSample(sentAt, receivedAt, generatorAt); ok {
				s.clockMu.Lock()
				s.lastClock, s.lastRoundTrip = sample, receivedAt.Sub(sentAt)
				s.clockMu.Unlock()
			}
		}
	}
	stats := generatorStatsFromProto(pong.GetStats())

	lastSafetyExit := generatorSafetyExitEvidenceFromProto(pong.GetLastSafetyExit())
	safetyExits := generatorSafetyExitEvidenceListFromProto(pong.GetSafetyExits())
	if len(safetyExits) == 0 && lastSafetyExit != nil {
		safetyExits = []GeneratorSafetyExitEvidence{*lastSafetyExit}
	}
	if lastSafetyExit == nil && len(safetyExits) > 0 {
		copy := safetyExits[len(safetyExits)-1]
		lastSafetyExit = &copy
	}

	return HeartbeatResponse{
		GeneratorID:                    pong.GetGeneratorId(),
		JavaHost:                       cloneJavaHostCapabilities(pong.GetJavaHost()),
		TimestampUnixMs:                pong.GetTimestampUnixMs(),
		TimestampUnixNano:              pong.GetTimestampUnixNano(),
		ActiveTestIDs:                  pong.GetActiveTestIds(),
		Stats:                          stats,
		AvailableCapacity:              pong.GetAvailableCapacity(),
		CPULimitCores:                  pong.GetCpuLimitCores(),
		MemoryLimitBytes:               pong.GetMemoryLimitBytes(),
		MaxVUsers:                      pong.GetMaxVusers(),
		Version:                        pong.GetVersion(),
		SupportedProtocols:             append([]string(nil), pong.GetSupportedProtocols()...),
		CompatibleFeatures:             append([]string(nil), pong.GetCompatibleFeatures()...),
		PodCapacity:                    pong.GetPodCapacity(),
		RuntimeCapacities:              protocolRuntimeCapacitiesFromProto(pong.GetRuntimeCapacities()),
		RunSupervisionExpirationsTotal: pong.GetRunSupervisionExpirationsTotal(),
		RunSupervisionEscalationsTotal: pong.GetRunSupervisionEscalationsTotal(),
		LastSafetyExit:                 lastSafetyExit,
		SafetyExits:                    safetyExits,
	}, nil
}

func generatorSafetyExitEvidenceFromProto(value *pb.GeneratorSafetyExitEvidence) *GeneratorSafetyExitEvidence {
	if value == nil {
		return nil
	}
	requestedAt := time.Time{}
	if raw := strings.TrimSpace(value.GetRequestedAtRfc3339Nano()); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			requestedAt = parsed.UTC()
		}
	}
	if requestedAt.IsZero() && value.GetRequestedAtUnixMs() > 0 {
		requestedAt = time.UnixMilli(value.GetRequestedAtUnixMs()).UTC()
	}
	return &GeneratorSafetyExitEvidence{
		TestID:                value.GetTestId(),
		ExecutionGeneration:   value.GetExecutionGeneration(),
		LifecycleState:        value.GetLifecycleState(),
		Reason:                value.GetReason(),
		RequestedAt:           requestedAt,
		OutstandingExecutions: value.GetOutstandingExecutions(),
	}
}

func generatorSafetyExitEvidenceListFromProto(values []*pb.GeneratorSafetyExitEvidence) []GeneratorSafetyExitEvidence {
	if len(values) == 0 {
		return nil
	}
	out := make([]GeneratorSafetyExitEvidence, 0, len(values))
	for _, value := range values {
		converted := generatorSafetyExitEvidenceFromProto(value)
		if converted != nil {
			out = append(out, *converted)
		}
	}
	return out
}

func protocolRuntimeCapacitiesFromProto(values []*pb.ProtocolRuntimeCapacity) []ProtocolRuntimeCapacity {
	if len(values) == 0 {
		return nil
	}
	out := make([]ProtocolRuntimeCapacity, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		out = append(out, ProtocolRuntimeCapacity{
			ResourceContract: value.GetResourceContract(),
			Resources:        RuntimeResourceCapacityFromProto(value.GetResources()), Protocol: value.GetProtocol(),
			Available:       value.GetAvailable(),
			MaxVUsers:       value.GetMaxVusers(),
			AvailableVUsers: value.GetAvailableVusers(),
			Features:        append([]string(nil), value.GetFeatures()...),
			OccupancyMode:   value.GetOccupancyMode(),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *HeartbeatStream) CloseSend() error {
	return s.stream.CloseSend()
}

func (s *MetricsStream) SendPause(control PauseControl) error {
	if control.RequireQuiescence {
		return fmt.Errorf("strict pause requires the acknowledged SetPaused RPC")
	}
	return s.stream.Send(&pb.ControlSignal{
		Signal: &pb.ControlSignal_Pause{
			Pause: &pb.PauseRequest{
				TestId:              control.TestID,
				ExecutionGeneration: control.ExecutionGeneration,
				Pause:               control.Pause,
				OperationId:         control.OperationID,
				OperationSequence:   control.OperationSequence,
				RunSupervisionToken: append([]byte(nil), control.RunSupervisionToken...),
			},
		},
	})
}

func (s *MetricsStream) CloseSend() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.stream != nil {
			s.closeErr = s.stream.CloseSend()
		}
		if s.cancel != nil {
			s.cancel()
		}
	})
	return s.closeErr
}

func (s *MetricsStream) Receive() (MetricsBatch, error) {
	batch, err := s.stream.Recv()
	if err != nil {
		return MetricsBatch{}, err
	}

	transactions := make([]TransactionMetric, 0, len(batch.GetTransactions()))
	for _, tx := range batch.GetTransactions() {
		var serverTelemetry *ServerTelemetry
		if tx.GetServerTelemetry() != nil {
			serverTelemetry = &ServerTelemetry{
				SubscriptionID:            tx.GetServerTelemetry().GetSubscriptionId(),
				AcknowledgementMs:         cloneMessagingAck(tx.GetServerTelemetry().AcknowledgementMs),
				Messaging:                 messagingDiagnosticsFromJSON(tx.GetServerTelemetry().GetMessagingDiagnosticsJson(), tx.GetServerTelemetry().GetMessagingDiagnosticsVersion()),
				GRPC:                      grpcDiagnosticsFromProto(tx.GetServerTelemetry().GetGrpc()),
				WebSocket:                 websocketDiagnosticsFromProto(tx.GetServerTelemetry().GetWebsocket()),
				ServerProcessingMs:        tx.GetServerTelemetry().GetServerProcessingMs(),
				TransferMs:                tx.GetServerTelemetry().GetTransferMs(),
				RequestPayloadKB:          tx.GetServerTelemetry().GetRequestPayloadKb(),
				ResponsePayloadKB:         tx.GetServerTelemetry().GetResponsePayloadKb(),
				ConnectionReused:          tx.GetServerTelemetry().GetConnectionReused(),
				RequestSizeClass:          tx.GetServerTelemetry().GetRequestSizeClass(),
				ResponseSizeClass:         tx.GetServerTelemetry().GetResponseSizeClass(),
				FirstEventMs:              tx.GetServerTelemetry().GetFirstEventMs(),
				EventCount:                tx.GetServerTelemetry().GetEventCount(),
				MessageLatencyMs:          tx.GetServerTelemetry().GetMessageLatencyMs(),
				MessagesSent:              tx.GetServerTelemetry().GetMessagesSent(),
				MessagesReceived:          tx.GetServerTelemetry().GetMessagesReceived(),
				MessagesSentPerSec:        tx.GetServerTelemetry().GetMessagesSentPerSec(),
				MessagesReceivedPerSec:    tx.GetServerTelemetry().GetMessagesReceivedPerSec(),
				ConsumerLagMs:             tx.GetServerTelemetry().GetConsumerLagMs(),
				ConsumerLagObserved:       tx.GetServerTelemetry().GetConsumerLagObserved(),
				ConsumerOffsetLagObserved: tx.GetServerTelemetry().GetConsumerOffsetLagObserved(),
				SendFailureCount:          tx.GetServerTelemetry().GetSendFailureCount(),
				RedeliveryCount:           tx.GetServerTelemetry().GetRedeliveryCount(),
				DeadLetteredCount:         tx.GetServerTelemetry().GetDeadLetteredCount(),
				DuplicateCount:            tx.GetServerTelemetry().GetDuplicateCount(),
				OutOfOrderCount:           tx.GetServerTelemetry().GetOutOfOrderCount(),
				CommitCount:               tx.GetServerTelemetry().GetCommitCount(),
				AbortCount:                tx.GetServerTelemetry().GetAbortCount(),
				ConsumerOffsetLag:         tx.GetServerTelemetry().GetConsumerOffsetLag(),
				ThrottledRequests:         tx.GetServerTelemetry().GetThrottledRequests(),
				ServerBusyCount:           tx.GetServerTelemetry().GetServerBusyCount(),
				QuotaExceededCount:        tx.GetServerTelemetry().GetQuotaExceededCount(),
				EventTooLargeCount:        tx.GetServerTelemetry().GetEventTooLargeCount(),
				EndToEndLatencyMs:         tx.GetServerTelemetry().GetEndToEndLatencyMs(),
				PartitionID:               tx.GetServerTelemetry().GetPartitionId(),
			}
		}
		var snapshot *protocol.ErrorSnapshot
		if rawSnapshot := tx.GetSnapshotJson(); rawSnapshot != "" {
			var parsed protocol.ErrorSnapshot
			if err := json.Unmarshal([]byte(rawSnapshot), &parsed); err == nil {
				snapshot = &parsed
			}
		}
		transactions = append(transactions, TransactionMetric{
			Name:             tx.GetName(),
			StartTimeUnixMs:  tx.GetStartTimeUnixMs(),
			Group:            tx.GetGroup(),
			Region:           tx.GetRegion(),
			NetworkProfile:   tx.GetNetworkProfile(),
			Hierarchy:        tx.GetHierarchy(),
			Phase:            tx.GetPhase(),
			PageName:         tx.GetPageName(),
			DurationMs:       tx.GetDurationMs(),
			WorkerDurationMs: tx.GetWorkerDurationMs(),
			DNSMs:            tx.GetDnsMs(),
			ConnectMs:        tx.GetConnectMs(),
			TLSMs:            tx.GetTlsMs(),
			TTFBMs:           tx.GetTtfbMs(),
			StatusCode:       tx.GetStatusCode(),
			Success:          tx.GetSuccess(),
			ErrorMessage:     tx.GetErrorMessage(),
			BytesSent:        tx.GetBytesSent(),
			BytesReceived:    tx.GetBytesReceived(),
			CorrelationID:    tx.GetCorrelationId(),
			DependencyType:   tx.GetDependencyType(),
			DependencyTarget: tx.GetDependencyTarget(),
			SOAPOperation:    tx.GetSoapOperation(),
			ServerTelemetry:  serverTelemetry,
			Snapshot:         snapshot,
		})
	}

	groups := make([]GroupMetric, 0, len(batch.GetGroups()))
	for _, group := range batch.GetGroups() {
		groups = append(groups, GroupMetric{
			Name:         group.GetName(),
			ActiveVUsers: group.GetActiveVusers(),
			Metrics:      metricAggregateFromProto(group.GetMetrics()),
		})
	}

	stats := generatorStatsFromProto(batch.GetStats())
	telemetry := metricsTelemetryFromProto(batch.GetTelemetry())

	return MetricsBatch{
		BrowserDocuments:           browserDocumentsFromJSON(batch.GetBrowserDocumentSummaryJson(), batch.GetBrowserDocumentSummaryVersion()),
		BrowserDocumentsVersion:    batch.GetBrowserDocumentSummaryVersion(),
		MessagingProgress:          messagingProgressFromJSON(batch.GetMessagingProgressJson(), batch.GetMessagingProgressVersion()),
		MessagingProgressVersion:   batch.GetMessagingProgressVersion(),
		TestID:                     batch.GetTestId(),
		ExecutionGeneration:        batch.GetExecutionGeneration(),
		BatchSequence:              batch.GetBatchSequence(),
		SourceEpoch:                batch.GetSourceEpoch(),
		SourceEpochStartedUnixMs:   batch.GetSourceEpochStartedUnixMs(),
		SequencePrefixStateOnly:    batch.GetSequencePrefixStateOnly(),
		GeneratorID:                batch.GetGeneratorId(),
		TimestampUnixMs:            batch.GetTimestampUnixMs(),
		WindowMs:                   batch.GetWindowMs(),
		Stats:                      stats,
		Transactions:               transactions,
		FleetMetrics:               metricAggregateFromProto(batch.GetFleetMetrics()),
		TransactionMetrics:         metricAggregatesFromProto(batch.GetTransactionMetrics()),
		DependencyMetrics:          metricAggregatesFromProto(batch.GetDependencyMetrics()),
		SOAPMetrics:                metricAggregatesFromProto(batch.GetSoapMetrics()),
		NetworkProfileMetrics:      metricAggregatesFromProto(batch.GetNetworkProfileMetrics()),
		ActionMetrics:              metricAggregatesFromProto(batch.GetActionMetrics()),
		ActionDimensionMetrics:     metricAggregatesFromProto(batch.GetActionDimensionMetrics()),
		JointMetrics:               metricAggregatesFromProto(batch.GetJointMetricsV4()),
		JointInfo:                  jointInfoFromProto(batch.GetJointMetricsInfo()),
		ActionPropertyMetrics:      metricAggregatesFromProto(batch.GetActionPropertyMetrics()),
		ActionMetricsVersion:       batch.GetActionMetricsVersion(),
		PropertyMetrics:            metricAggregatesFromProto(batch.GetPropertyMetrics()),
		RegionMetrics:              metricAggregatesFromProto(batch.GetRegionMetrics()),
		Browser:                    browserMetricsFromProto(batch.GetBrowserMetrics()),
		BrowserByDevice:            browserDeviceMetricsFromProto(batch.GetBrowserDeviceMetrics()),
		Groups:                     groups,
		LifecycleState:             batch.GetLifecycleState(),
		LifecycleError:             batch.GetLifecycleError(),
		Telemetry:                  telemetry,
		TransactionResponseTimeSLA: transactionResponseTimeSLAFromProto(batch.GetTransactionResponseTimeSla()),
		HTTPVersionMix:             httpVersionMixFromProto(batch.GetHttpVersionMix()),
	}, nil
}

// maxHTTPVersionMixKeys bounds each count map a generator sends; the keys
// are small enums, so a larger map is malformed. One more than the mix's own
// bound allows its "other" key.
const maxHTTPVersionMixKeys = protocol.MaxHTTPVersionMixKeys + 1

// httpVersionMixFromProto validates a generator's mix: negative counts,
// oversized maps or a breakdown larger than the action count discard it.
func httpVersionMixFromProto(in *pb.HTTPVersionMix) *protocol.HTTPVersionMix {
	if in == nil || in.GetActions() <= 0 {
		return nil
	}
	out := &protocol.HTTPVersionMix{
		Actions: in.GetActions(), HTTP3Policies: in.GetHttp3Policies(), UpgradeEligible: in.GetUpgradeEligible(), FellBack: in.GetFellBack(),
		QUICAttempted: in.GetQuicAttempted(), QUICFailed: in.GetQuicFailed(), Attempts: in.GetAttempts(),
	}
	for _, value := range []int64{out.HTTP3Policies, out.UpgradeEligible, out.FellBack, out.QUICAttempted, out.QUICFailed, out.Attempts} {
		if value < 0 {
			return nil
		}
	}
	if out.HTTP3Policies > out.Actions || out.UpgradeEligible > out.Actions || out.FellBack > out.UpgradeEligible || out.QUICAttempted > out.Actions || out.QUICFailed > out.QUICAttempted {
		return nil
	}
	maps := []struct {
		in  map[string]int64
		out *map[string]int64
	}{{in.GetByVersion(), &out.ByVersion}, {in.GetBySelection(), &out.BySelection}, {in.GetByFallback(), &out.ByFallback}, {in.GetByQuicError(), &out.ByQUICError}}
	for _, counts := range maps {
		if len(counts.in) > maxHTTPVersionMixKeys {
			return nil
		}
		var sum int64
		for key, value := range counts.in {
			if value < 0 || len(key) == 0 || len(key) > protocol.MaxHTTPVersionMixKeyBytes {
				return nil
			}
			sum += value
			if sum > out.Actions {
				return nil
			}
			if *counts.out == nil {
				*counts.out = make(map[string]int64, len(counts.in))
			}
			(*counts.out)[key] = value
		}
	}
	return out
}

func transactionResponseTimeSLAFromProto(rows []*pb.TransactionResponseTimeSLACompliance) []TransactionResponseTimeSLACompliance {
	out := make([]TransactionResponseTimeSLACompliance, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		out = append(out, TransactionResponseTimeSLACompliance{Group: row.GetGroup(), Transaction: row.GetTransaction(), ThresholdMs: row.GetThresholdMs(), TotalCount: row.GetTotalCount(), EligibleSuccessCount: row.GetEligibleSuccessCount(), ExcludedErrorCount: row.GetExcludedErrorCount(), MissedCount: row.GetMissedCount()})
	}
	return out
}

func toProtoAssignment(assignment Assignment) *pb.WorkAssignment {
	dataFiles := make([]*pb.DataFile, 0, len(assignment.DataFiles))
	for _, dataFile := range assignment.DataFiles {
		dataFiles = append(dataFiles, &pb.DataFile{Name: dataFile.Name, Content: dataFile.Content, Sha256: dataFile.SHA256})
	}

	environment := assignment.Environment
	if environment == nil {
		environment = map[string]string{}
	}

	targetDuration := resolvedPreciseDuration(assignment.TargetDuration, assignment.TargetDurationSeconds)
	rampUp := resolvedPreciseDuration(assignment.RampUp, int64(assignment.RampUpSeconds))
	return &pb.WorkAssignment{PrepareOnly: assignment.PrepareOnly,
		LiveTableBindingsJson:     append([]byte(nil), assignment.LiveTableBindingsJSON...),
		RuntimeResourceClaim:      CloneRuntimeResourceClaim(assignment.RuntimeResourceClaim),
		SourcePlan:                assignment.SourcePlan,
		JavaHost:                  cloneJavaHostSelection(assignment.JavaHost),
		TestId:                    assignment.TestID,
		ExecutionGeneration:       assignment.ExecutionGeneration,
		ScenarioId:                assignment.ScenarioID,
		RunCorrelationOrigins:     append([]string(nil), assignment.RunCorrelationOrigins...),
		JointEvidenceProjection:   jointProjectionToProto(assignment.JointEvidenceProjection),
		CorrelationCarriers:       append([]string(nil), assignment.CorrelationCarriers...),
		CorrelationTestName:       assignment.CorrelationTestName,
		RequireTestExecutionLease: assignment.RequireTestExecutionLease,
		AdoptionGrace:             preciseDurationProto(assignment.AdoptionGrace, 0),
		GroupName:                 assignment.GroupName,
		TargetDurationSeconds:     compatibilityDurationSeconds(targetDuration),
		TargetDuration:            preciseDurationProto(targetDuration, 0),
		TerminationMode:           assignment.TerminationMode,
		DurationOffset:            preciseDurationProto(assignment.DurationOffset, 0),
		ScriptId:                  assignment.ScriptID,
		ExpectedScriptHash:        assignment.ExpectedScriptHash,
		ScriptContent:             assignment.ScriptContent,
		VuserCount:                assignment.VUserCount,
		RampUpSeconds:             compatibilityDurationSecondsInt32(rampUp),
		RampUp:                    preciseDurationProto(rampUp, 0),
		RampDown:                  preciseDurationProto(assignment.RampDown, 0),
		InitStrategy:              assignment.InitStrategy,
		PreBaseline:               preciseDurationProto(assignment.PreBaseline, 0),
		PostBaseline:              preciseDurationProto(assignment.PostBaseline, 0),
		ShardOrdinal:              assignment.ShardOrdinal,
		ShardCount:                assignment.ShardCount,
		VuserIdOffset:             assignment.VUserIDOffset,
		ParameterPopulation:       assignment.ParameterPopulation,
		ParameterOffset:           assignment.ParameterOffset,
		StartPaused:               assignment.StartPaused,
		Environment:               environment,
		DataFiles:                 dataFiles,
		RedactionRules:            redactionRulesToProto(assignment.RedactionRules),
		SecretBundle:              cloneSecretBundle(assignment.SecretBundle),
		SecretBundleId:            assignment.SecretBundleID,
		RunSupervisionToken:       append([]byte(nil), assignment.RunSupervisionToken...),
		ArchiveSourceTarget:       assignment.ArchiveSourceTarget,
		Region:                    assignment.Region,
		StartOffset:               preciseDurationProto(assignment.StartOffset, 0),
		MessageDetailMaxItems:     int32(partitionAssignmentInt(assignment.MessageDetailMaxItems, assignment.ShardCount, assignment.ShardOrdinal)),
		// The per-item ceiling applies independently on every generator. Only the
		// run-wide item count and total-byte budget are partitioned among shards.
		MessageDetailMaxItemBytes:    assignment.MessageDetailMaxItemBytes,
		MessageDetailMaxTotalBytes:   partitionAssignmentInt64(assignment.MessageDetailMaxTotalBytes, assignment.ShardCount, assignment.ShardOrdinal),
		TransactionResponseTimeSlaMs: assignment.TransactionResponseTimeSLAMs,
	}
}

func partitionAssignmentInt(value int, shardCount, shardOrdinal uint32) int {
	return int(partitionAssignmentInt64(int64(value), shardCount, shardOrdinal))
}

func partitionAssignmentInt64(value int64, shardCount, shardOrdinal uint32) int64 {
	if value <= 0 || shardCount <= 1 {
		return value
	}
	count := int64(shardCount)
	base, remainder := value/count, value%count
	if int64(shardOrdinal) < remainder {
		base++
	}
	return base
}

func resolvedPreciseDuration(precise time.Duration, legacySeconds int64) time.Duration {
	if precise != 0 {
		return precise
	}
	if legacySeconds <= 0 || legacySeconds > maxGeneratorDurationSeconds {
		return 0
	}
	return time.Duration(legacySeconds) * time.Second
}

func preciseDurationProto(precise time.Duration, legacySeconds int64) *durationpb.Duration {
	duration := resolvedPreciseDuration(precise, legacySeconds)
	if duration <= 0 {
		return nil
	}
	return durationpb.New(duration)
}

func compatibilityDurationSeconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	seconds := int64(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	return seconds
}

func compatibilityDurationSecondsInt32(duration time.Duration) int32 {
	seconds := compatibilityDurationSeconds(duration)
	const maxInt32 = int64(^uint32(0) >> 1)
	if seconds > maxInt32 {
		return int32(maxInt32)
	}
	return int32(seconds)
}

func durationFromProtoOrSeconds(precise *durationpb.Duration, legacySeconds int64) time.Duration {
	if precise != nil && precise.CheckValid() == nil {
		return precise.AsDuration()
	}
	return resolvedPreciseDuration(0, legacySeconds)
}

func redactionRulesToProto(rules []engine.RedactionRule) []*pb.RedactionRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]*pb.RedactionRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, &pb.RedactionRule{
			Name:        rule.Name,
			Pattern:     rule.Pattern,
			Replacement: rule.Replacement,
		})
	}
	return out
}

func metricAggregatesFromProto(values []*pb.MetricAggregate) []MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	out := make([]MetricAggregate, 0, len(values))
	for _, value := range values {
		if aggregate := metricAggregateFromProto(value); aggregate != nil {
			out = append(out, *aggregate)
		}
	}
	return out
}

func metricAggregatesToProto(values []MetricAggregate) []*pb.MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	out := make([]*pb.MetricAggregate, 0, len(values))
	for index := range values {
		out = append(out, metricAggregateToProto(&values[index]))
	}
	return out
}

func metricAggregateFromProto(value *pb.MetricAggregate) *MetricAggregate {
	if value == nil {
		return nil
	}
	return &MetricAggregate{
		Name:             value.GetName(),
		DependencyType:   value.GetDependencyType(),
		DependencyTarget: value.GetDependencyTarget(),
		Count:            value.GetCount(),
		Errors:           value.GetErrors(),
		AvgMs:            value.GetAvgMs(),
		P50Ms:            value.GetP50Ms(),
		P90Ms:            value.GetP90Ms(),
		P95Ms:            value.GetP95Ms(),
		P99Ms:            value.GetP99Ms(),
		AvgDNSMs:         value.GetAvgDnsMs(),
		AvgConnectMs:     value.GetAvgConnectMs(),
		AvgTLSMs:         value.GetAvgTlsMs(),
		AvgTTFBMs:        value.GetAvgTtfbMs(),
		BytesSent:        value.GetBytesSent(),
		BytesReceived:    value.GetBytesReceived(),
		TPS:              value.GetTps(),
		DurationDigest:   tdigestSnapshotFromProto(value.GetDurationDigest()),
		Messaging:        messagingSummaryFromJSON(value.GetMessagingSummaryJson(), value.GetMessagingVersion()),
		MessagingVersion: value.GetMessagingVersion(),
		Exemplars:        traceExemplarsFromProto(value.GetExemplars()),
	}
}

// traceExemplarsFromProto keeps at most three well-formed exemplars with
// distinct reasons; anything malformed is dropped, never repaired.
func traceExemplarsFromProto(values []*pb.TraceExemplar) []engine.TraceExemplar {
	var out []engine.TraceExemplar
	seen := map[string]bool{}
	for _, value := range values {
		if len(out) == engine.MaxTraceExemplars {
			break
		}
		reason := value.GetReason()
		if seen[reason] || reason != engine.ExemplarSlowestSuccess && reason != engine.ExemplarFirstError && reason != engine.ExemplarSample {
			continue
		}
		duration := value.GetDurationMs()
		if !protocol.ValidTraceID(value.GetTraceId()) || len(value.GetSpanId()) > 16 || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 {
			continue
		}
		seen[reason] = true
		out = append(out, engine.TraceExemplar{
			TraceID: value.GetTraceId(), SpanID: value.GetSpanId(), DurationMs: duration, Success: value.GetSuccess(),
			StatusCode: int(value.GetStatusCode()), Reason: reason, Timestamp: time.UnixMilli(value.GetTimestampUnixMs()).UTC(),
		})
	}
	return out
}

func traceExemplarsToProto(values []engine.TraceExemplar) []*pb.TraceExemplar {
	out := make([]*pb.TraceExemplar, 0, len(values))
	for _, value := range values {
		out = append(out, &pb.TraceExemplar{
			TraceId: value.TraceID, SpanId: value.SpanID, DurationMs: value.DurationMs, Success: value.Success,
			StatusCode: int32(value.StatusCode), Reason: value.Reason, TimestampUnixMs: value.Timestamp.UnixMilli(),
		})
	}
	return out
}

func metricAggregateToProto(value *MetricAggregate) *pb.MetricAggregate {
	if value == nil {
		return nil
	}
	return &pb.MetricAggregate{
		Name:                 value.Name,
		DependencyType:       value.DependencyType,
		DependencyTarget:     value.DependencyTarget,
		Count:                value.Count,
		Errors:               value.Errors,
		AvgMs:                value.AvgMs,
		P50Ms:                value.P50Ms,
		P90Ms:                value.P90Ms,
		P95Ms:                value.P95Ms,
		P99Ms:                value.P99Ms,
		AvgDnsMs:             value.AvgDNSMs,
		AvgConnectMs:         value.AvgConnectMs,
		AvgTlsMs:             value.AvgTLSMs,
		AvgTtfbMs:            value.AvgTTFBMs,
		BytesSent:            value.BytesSent,
		BytesReceived:        value.BytesReceived,
		Tps:                  value.TPS,
		DurationDigest:       tdigestSnapshotToProto(value.DurationDigest),
		MessagingSummaryJson: messagingSummaryJSON(value.Messaging),
		MessagingVersion:     value.MessagingVersion,
		Exemplars:            traceExemplarsToProto(value.Exemplars),
	}
}

func tdigestSnapshotFromProto(value *pb.TDigestSnapshot) *engine.TDigestSnapshot {
	if value == nil || value.GetCount() == 0 {
		return nil
	}
	centroids := make([]engine.TDigestCentroidSnapshot, 0, len(value.GetCentroids()))
	for _, centroid := range value.GetCentroids() {
		centroids = append(centroids, engine.TDigestCentroidSnapshot{
			Mean:   centroid.GetMean(),
			Weight: centroid.GetWeight(),
		})
	}
	return &engine.TDigestSnapshot{
		Compression: value.GetCompression(),
		TotalWeight: value.GetTotalWeight(),
		Min:         value.GetMin(),
		Max:         value.GetMax(),
		Count:       value.GetCount(),
		Centroids:   centroids,
	}
}

func tdigestSnapshotToProto(value *engine.TDigestSnapshot) *pb.TDigestSnapshot {
	if value == nil || value.Count == 0 {
		return nil
	}
	centroids := make([]*pb.TDigestCentroid, 0, len(value.Centroids))
	for _, centroid := range value.Centroids {
		centroids = append(centroids, &pb.TDigestCentroid{
			Mean:   centroid.Mean,
			Weight: centroid.Weight,
		})
	}
	return &pb.TDigestSnapshot{
		Compression: value.Compression,
		TotalWeight: value.TotalWeight,
		Min:         value.Min,
		Max:         value.Max,
		Count:       value.Count,
		Centroids:   centroids,
	}
}

func browserMetricsFromProto(value *pb.BrowserMetrics) *BrowserMetrics {
	if value == nil || value.GetSamples() == 0 {
		return nil
	}
	return &BrowserMetrics{
		Samples:              value.GetSamples(),
		AvgFCPMs:             value.GetAvgFcpMs(),
		AvgLCPMs:             value.GetAvgLcpMs(),
		AvgCLSScore:          value.GetAvgClsScore(),
		AvgINPMs:             value.GetAvgInpMs(),
		AvgTTFBMs:            value.GetAvgTtfbMs(),
		AvgBlockingResources: value.GetAvgBlockingResources(),
		AvgLongTaskCount:     value.GetAvgLongTaskCount(),
		AvgJSHeapUsedMB:      value.GetAvgJsHeapUsedMb(),
		AvgBrowserProcessCPU: value.GetAvgBrowserProcessCpuPct(),
		AvgBrowserProcessMem: value.GetAvgBrowserProcessMemMb(),
	}
}

func browserMetricsToProto(value *BrowserMetrics) *pb.BrowserMetrics {
	if value == nil || value.Samples == 0 {
		return nil
	}
	return &pb.BrowserMetrics{
		Samples:                 value.Samples,
		AvgFcpMs:                value.AvgFCPMs,
		AvgLcpMs:                value.AvgLCPMs,
		AvgClsScore:             value.AvgCLSScore,
		AvgInpMs:                value.AvgINPMs,
		AvgTtfbMs:               value.AvgTTFBMs,
		AvgBlockingResources:    value.AvgBlockingResources,
		AvgLongTaskCount:        value.AvgLongTaskCount,
		AvgJsHeapUsedMb:         value.AvgJSHeapUsedMB,
		AvgBrowserProcessCpuPct: value.AvgBrowserProcessCPU,
		AvgBrowserProcessMemMb:  value.AvgBrowserProcessMem,
	}
}

func browserDeviceMetricsFromProto(values []*pb.BrowserDeviceMetrics) []BrowserDeviceMetrics {
	if len(values) == 0 {
		return nil
	}
	out := make([]BrowserDeviceMetrics, 0, len(values))
	for _, value := range values {
		metrics := browserMetricsFromProto(value.GetMetrics())
		if metrics == nil {
			continue
		}
		out = append(out, BrowserDeviceMetrics{
			DeviceProfile:  value.GetDeviceProfile(),
			DevicePlatform: value.GetDevicePlatform(),
			DeviceTier:     value.GetDeviceTier(),
			BrowserEngine:  value.GetBrowserEngine(),
			Metrics:        *metrics,
		})
	}
	return out
}

func browserDeviceMetricsToProto(values []BrowserDeviceMetrics) []*pb.BrowserDeviceMetrics {
	if len(values) == 0 {
		return nil
	}
	out := make([]*pb.BrowserDeviceMetrics, 0, len(values))
	for _, value := range values {
		metrics := browserMetricsToProto(&value.Metrics)
		if metrics == nil {
			continue
		}
		out = append(out, &pb.BrowserDeviceMetrics{
			DeviceProfile:  value.DeviceProfile,
			DevicePlatform: value.DevicePlatform,
			DeviceTier:     value.DeviceTier,
			BrowserEngine:  value.BrowserEngine,
			Metrics:        metrics,
		})
	}
	return out
}

func toProtoStats(stats GeneratorStats) *pb.GeneratorStats {
	return &pb.GeneratorStats{
		CompletedIterations:               stats.CompletedIterations,
		ActiveVusers:                      stats.ActiveVUsers,
		CpuPercent:                        stats.CPUPercent,
		MemoryBytes:                       stats.MemoryBytes,
		GoroutineCount:                    stats.GoroutineCount,
		CpuUserPct:                        stats.CPUUserPct,
		CpuSystemPct:                      stats.CPUSystemPct,
		CpuIowaitPct:                      stats.CPUIOWaitPct,
		CpuStealPct:                       stats.CPUStealPct,
		Load1:                             stats.Load1,
		Load5:                             stats.Load5,
		Load15:                            stats.Load15,
		ThreadCount:                       stats.ThreadCount,
		OpenFdCount:                       stats.OpenFDCount,
		HeapAllocMb:                       stats.HeapAllocMB,
		HeapInUseMb:                       stats.HeapInUseMB,
		GcCountDelta:                      stats.GCCountDelta,
		GcPauseMs:                         stats.GCPauseMs,
		ContainerCpuLimitCores:            stats.ContainerCPULimitCores,
		ContainerCpuUsagePct:              stats.ContainerCPUUsagePct,
		ContainerCpuThrottledPeriodsDelta: stats.ContainerCPUThrottledPeriodsDelta,
		ContainerCpuThrottledSecondsRate:  stats.ContainerCPUThrottledSecondsRate,
		ContainerMemoryWorkingSetMb:       stats.ContainerMemoryWorkingSetMB,
		ContainerMemoryLimitMb:            stats.ContainerMemoryLimitMB,
		ContainerMemoryUsagePct:           stats.ContainerMemoryUsagePct,
		ContainerMemoryFailCountDelta:     stats.ContainerMemoryFailCountDelta,
		ContainerOomKillCountDelta:        stats.ContainerOOMKillCountDelta,
		NetDropInPerSec:                   stats.NetDropInPerSec,
		NetDropOutPerSec:                  stats.NetDropOutPerSec,
		NetErrorInPerSec:                  stats.NetErrorInPerSec,
		NetErrorOutPerSec:                 stats.NetErrorOutPerSec,
		TcpRetransmitsPerSec:              stats.TCPRetransmitsPerSec,
		TcpEstablished:                    stats.TCPEstablished,
		TcpTimeWait:                       stats.TCPTimeWait,
		EphemeralPortUtilizationPct:       stats.EphemeralPortUtilizationPct,
		DiskIoTimePct:                     stats.DiskIOTimePct,
		DiskWeightedIoTimePct:             stats.DiskWeightedIOTimePct,
		DiskInProgress:                    stats.DiskInProgress,
	}
}

func generatorStatsFromProto(stats *pb.GeneratorStats) GeneratorStats {
	if stats == nil {
		return GeneratorStats{}
	}
	return GeneratorStats{
		CompletedIterations:               stats.GetCompletedIterations(),
		ActiveVUsers:                      stats.GetActiveVusers(),
		CPUPercent:                        stats.GetCpuPercent(),
		MemoryBytes:                       stats.GetMemoryBytes(),
		GoroutineCount:                    stats.GetGoroutineCount(),
		CPUUserPct:                        stats.GetCpuUserPct(),
		CPUSystemPct:                      stats.GetCpuSystemPct(),
		CPUIOWaitPct:                      stats.GetCpuIowaitPct(),
		CPUStealPct:                       stats.GetCpuStealPct(),
		Load1:                             stats.GetLoad1(),
		Load5:                             stats.GetLoad5(),
		Load15:                            stats.GetLoad15(),
		ThreadCount:                       stats.GetThreadCount(),
		OpenFDCount:                       stats.GetOpenFdCount(),
		HeapAllocMB:                       stats.GetHeapAllocMb(),
		HeapInUseMB:                       stats.GetHeapInUseMb(),
		GCCountDelta:                      stats.GetGcCountDelta(),
		GCPauseMs:                         stats.GetGcPauseMs(),
		ContainerCPULimitCores:            stats.GetContainerCpuLimitCores(),
		ContainerCPUUsagePct:              stats.GetContainerCpuUsagePct(),
		ContainerCPUThrottledPeriodsDelta: stats.GetContainerCpuThrottledPeriodsDelta(),
		ContainerCPUThrottledSecondsRate:  stats.GetContainerCpuThrottledSecondsRate(),
		ContainerMemoryWorkingSetMB:       stats.GetContainerMemoryWorkingSetMb(),
		ContainerMemoryLimitMB:            stats.GetContainerMemoryLimitMb(),
		ContainerMemoryUsagePct:           stats.GetContainerMemoryUsagePct(),
		ContainerMemoryFailCountDelta:     stats.GetContainerMemoryFailCountDelta(),
		ContainerOOMKillCountDelta:        stats.GetContainerOomKillCountDelta(),
		NetDropInPerSec:                   stats.GetNetDropInPerSec(),
		NetDropOutPerSec:                  stats.GetNetDropOutPerSec(),
		NetErrorInPerSec:                  stats.GetNetErrorInPerSec(),
		NetErrorOutPerSec:                 stats.GetNetErrorOutPerSec(),
		TCPRetransmitsPerSec:              stats.GetTcpRetransmitsPerSec(),
		TCPEstablished:                    stats.GetTcpEstablished(),
		TCPTimeWait:                       stats.GetTcpTimeWait(),
		EphemeralPortUtilizationPct:       stats.GetEphemeralPortUtilizationPct(),
		DiskIOTimePct:                     stats.GetDiskIoTimePct(),
		DiskWeightedIOTimePct:             stats.GetDiskWeightedIoTimePct(),
		DiskInProgress:                    stats.GetDiskInProgress(),
	}
}

func cloneJavaHostSelection(value *pb.JavaHostSelection) *pb.JavaHostSelection {
	if value == nil {
		return nil
	}
	return proto.Clone(value).(*pb.JavaHostSelection)
}
func cloneJavaHostPlacement(value *pb.JavaHostPlacement) *pb.JavaHostPlacement {
	if value == nil {
		return nil
	}
	return proto.Clone(value).(*pb.JavaHostPlacement)
}
func cloneJavaHostCapabilities(value *pb.JavaHostCapabilities) *pb.JavaHostCapabilities {
	if value == nil {
		return nil
	}
	return proto.Clone(value).(*pb.JavaHostCapabilities)
}

// Placement negotiation is tied to this physical connection and a recent
// heartbeat. Retained per-run clients share the same evidence. An older or
// malformed advertisement clears admission rather than falling back locally.
func (c *Client) observeJavaHosts(pong *pb.HeartbeatPong) {
	if c.connection == nil {
		return
	}
	var caps *pb.JavaHostCapabilities
	if pong != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureJavaHostBrokerV1) && ValidJavaHostCapabilities(pong.GetJavaHost()) {
		caps = cloneJavaHostCapabilities(pong.GetJavaHost())
	}
	c.connection.mu.Lock()
	c.connection.javaHostCaps = caps
	c.connection.javaHostPrepared = caps != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureJavaHostPreparedPopulationV1)
	c.connection.javaHostObserved = time.Now()
	c.connection.mu.Unlock()
}

// ValidJavaHostCapabilities validates negotiated dedicated-host advertisements.
// The limits are configured ceilings, not evidence of currently free reservations.
func ValidJavaHostCapabilities(caps *pb.JavaHostCapabilities) bool {
	if caps == nil || caps.Contract != JavaHostSelectionContract || caps.Mode != "dedicated" || caps.Containment != "execution_pod" || caps.MaxHosts < 1 || caps.MaxHosts > 10000 || caps.MaxVusers < 1 || caps.MaxVusers > 10000 || caps.MaxMemoryBytes < 1 || len(caps.Profiles) < 1 || len(caps.Profiles) > 256 {
		return false
	}
	seen := map[string]bool{}
	for _, p := range caps.Profiles {
		if p == nil || !validJavaHostProfileResources(p) || !javaHostDigest(p.ScriptSha256) || !javaHostDigest(p.PolicySha256) || !javaHostDigest(p.ConfigurationSha256) || p.ProfileId == "" || len(p.ProfileId) > 128 || p.MemoryBytes < 1 || p.PreparationMs < 1 || p.PreparationMs > 3600000 || p.MaxRunMs < 1 || p.MaxRunMs > 86400000 || p.InvocationMs < 1 || p.InvocationMs > 3600000 || seen[p.ScriptSha256] {
			return false
		}
		seen[p.ScriptSha256] = true
	}
	return true
}
func (c *Client) javaHostSelectionNegotiated(q *pb.WorkAssignment) bool {
	if c.connection == nil {
		return false
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	caps := c.connection.javaHostCaps
	if q.GetJavaHost().GetPreparedVusers() > 0 && !c.connection.javaHostPrepared {
		return false
	}
	if caps == nil || time.Since(c.connection.javaHostObserved) > 30*time.Second || javaHostPreparedVUsers(q) > caps.MaxHosts || javaHostPreparedVUsers(q) > caps.MaxVusers {
		return false
	}
	s := q.GetJavaHost()
	for _, p := range caps.Profiles {
		if p.FacadeResourcesSha256 == s.FacadeResourcesSha256 && p.ScriptSha256 == s.ScriptSha256 && p.ProfileId == s.ProfileId && p.PolicySha256 == s.PolicySha256 && p.ConfigurationSha256 == s.ConfigurationSha256 && s.PreparationMs <= p.PreparationMs && s.RunLifetimeMs <= p.MaxRunMs && int64(javaHostPreparedVUsers(q)) <= caps.MaxMemoryBytes/p.MemoryBytes {
			return true
		}
	}
	return false
}

// metricsTelemetryFromProto reads a publication's telemetry. A per-test
// subscriber-drop count is reported only when the field is present: older
// generators send none, which is not zero.
func metricsTelemetryFromProto(in *pb.MetricsBatchTelemetry) *MetricsTelemetry {
	if in == nil {
		return nil
	}
	telemetry := &MetricsTelemetry{
		DroppedResults:           in.GetDroppedResults(),
		SheddedArrivals:          in.GetSheddedArrivals(),
		CollectedAtUnixMs:        in.GetCollectedAtUnixMs(),
		PublishedAtUnixMs:        in.GetPublishedAtUnixMs(),
		SampleAgeMs:              in.GetSampleAgeMs(),
		GeneratorProcessingLagMs: in.GetGeneratorProcessingLagMs(),
		TransportLagMs:           in.GetTransportLagMs(),
		DroppedSubscriberBatches: in.GetDroppedSubscriberBatches(),
		CollectionSequence:       in.GetCollectionSequence(),
	}
	if in.TestDroppedSubscriberBatches != nil {
		telemetry.TestDroppedSubscriberBatches, telemetry.TestDroppedSubscriberBatchesReported = in.GetTestDroppedSubscriberBatches(), true
	}
	return telemetry
}

// AcknowledgeEvidence tells the generator the controller durably holds the
// named evidence artifacts, so it deletes its copies.
func (c *Client) AcknowledgeEvidence(ctx context.Context, request EvidenceAcknowledgement) (EvidenceAckResult, error) {
	message := &pb.EvidenceAcknowledgement{TestId: request.TestID, SourceIdentity: request.SourceIdentity}
	for _, artifact := range request.Artifacts {
		message.Artifacts = append(message.Artifacts, &pb.AcknowledgedEvidence{Name: artifact.Name, Sha256: artifact.SHA256})
	}
	ack, err := c.rpc.AcknowledgeEvidence(ctx, message)
	if err != nil {
		return EvidenceAckResult{}, err
	}
	return EvidenceAckResult{Deleted: ack.GetDeleted(), Retained: ack.GetRetained()}, nil
}

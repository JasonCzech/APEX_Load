package generatorrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
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
	TestID                string
	ExecutionGeneration   uint64
	GroupName             string
	TargetDuration        time.Duration
	TargetDurationSeconds int64
	DurationOffset        time.Duration
	ScriptID              string
	ExpectedScriptHash    string
	ScriptContent         []byte
	VUserCount            int32
	RampUp                time.Duration
	RampUpSeconds         int32
	RampDown              time.Duration
	InitStrategy          string
	PreBaseline           time.Duration
	PostBaseline          time.Duration
	ShardOrdinal          uint32
	ShardCount            uint32
	VUserIDOffset         uint64
	StartPaused           bool
	Environment           map[string]string
	DataFiles             []DataFile
	RedactionRules        []engine.RedactionRule
	SecretBundle          map[string]string
	SecretBundleID        string
}

type AssignmentResult struct {
	Accepted          bool
	ErrorMessage      string
	AvailableCapacity int32
}

type WorkCommitResult struct {
	Accepted     bool
	ErrorMessage string
}

type VUserAdjustment struct {
	TestID              string
	ExecutionGeneration uint64
	GroupName           string
	TargetVUsers        int32
	RampDuration        time.Duration
	RampSeconds         int32
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
}

type DurationResult struct {
	Accepted               bool
	CurrentDuration        time.Duration
	CurrentDurationSeconds int64
}

type PauseControl struct {
	TestID              string
	ExecutionGeneration uint64
	Pause               bool
	OperationID         string
	OperationSequence   uint64
}

type PauseResult struct {
	Accepted                 bool
	CurrentPaused            bool
	AppliedOperationID       string
	AppliedOperationSequence uint64
	ErrorMessage             string
}

type MetricsBatch struct {
	TestID                string
	ExecutionGeneration   uint64
	GeneratorID           string
	TimestampUnixMs       int64
	WindowMs              int64
	Stats                 GeneratorStats
	Transactions          []TransactionMetric
	FleetMetrics          *MetricAggregate
	TransactionMetrics    []MetricAggregate
	DependencyMetrics     []MetricAggregate
	SOAPMetrics           []MetricAggregate
	NetworkProfileMetrics []MetricAggregate
	Browser               *BrowserMetrics
	BrowserByDevice       []BrowserDeviceMetrics
	Groups                []GroupMetric
	LifecycleState        string
	LifecycleError        string
	Telemetry             *MetricsTelemetry
}

type MetricsTelemetry struct {
	DroppedResults             int64
	SheddedArrivals            int64
	CollectedAtUnixMs          int64
	PublishedAtUnixMs          int64
	ControllerReceivedAtUnixMs int64
	SampleAgeMs                float64
	GeneratorProcessingLagMs   float64
	TransportLagMs             float64
	DroppedSubscriberBatches   int64
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
	ServerProcessingMs     float64
	TransferMs             float64
	RequestPayloadKB       float64
	ResponsePayloadKB      float64
	ConnectionReused       bool
	RequestSizeClass       string
	ResponseSizeClass      string
	FirstEventMs           float64
	EventCount             int32
	MessageLatencyMs       float64
	MessagesSent           int32
	MessagesReceived       int32
	MessagesSentPerSec     float64
	MessagesReceivedPerSec float64
	ConsumerLagMs          float64
	SendFailureCount       int32
	RedeliveryCount        int32
	DeadLetteredCount      int32
	DuplicateCount         int32
	OutOfOrderCount        int32
	CommitCount            int32
	AbortCount             int32
	ConsumerOffsetLag      int64
	ThrottledRequests      int32
	ServerBusyCount        int32
	QuotaExceededCount     int32
	EventTooLargeCount     int32
	EndToEndLatencyMs      float64
	PartitionID            string
}

type TransactionMetric struct {
	Name             string
	StartTimeUnixMs  int64
	Group            string
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

type ArchiveArtifactRequest struct {
	TestID string
	Name   string
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
}

type Heartbeat struct {
	GeneratorID     string
	TimestampUnixMs int64
	Stats           GeneratorStats
}

type HeartbeatResponse struct {
	GeneratorID        string
	TimestampUnixMs    int64
	ActiveTestIDs      []string
	Stats              GeneratorStats
	AvailableCapacity  int32
	CPULimitCores      float64
	MemoryLimitBytes   int64
	MaxVUsers          int32
	Version            string
	SupportedProtocols []string
	CompatibleFeatures []string
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

type GeneratorStats struct {
	ActiveVUsers int32
	CPUPercent   float64
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

type Client struct {
	conn *grpc.ClientConn
	rpc  pb.GeneratorControlClient
}

type HeartbeatStream struct {
	stream pb.GeneratorControl_HeartbeatClient
}

type MetricsStream struct {
	stream    pb.GeneratorControl_StreamMetricsClient
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

func Dial(ctx context.Context, target string, opts ...grpc.DialOption) (*Client, error) {
	// Transport security (TLS/mTLS/bearer token) comes from the APEXLOAD_GRPC_*
	// env contract; plaintext without a token is the unconfigured fallback.
	dialOptions, err := DialOptionsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("generator transport config: %w", err)
	}
	dialOptions = append(dialOptions, opts...)

	conn, err := grpc.DialContext(ctx, target, dialOptions...)
	if err != nil {
		return nil, err
	}

	return NewClient(conn), nil
}

func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{
		conn: conn,
		rpc:  pb.NewGeneratorControlClient(conn),
	}
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
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
	if ttl <= 0 {
		return ExecutionEpochResult{}, fmt.Errorf("execution epoch ttl must be positive")
	}
	if err := validateExecutionLeaseOwner(generation, leaseOwnerID, true); err != nil {
		return ExecutionEpochResult{}, fmt.Errorf("execution lease owner: %w", err)
	}
	if ttl > time.Duration(^uint64(0)>>1) {
		return ExecutionEpochResult{}, fmt.Errorf("execution epoch ttl is too large")
	}
	ack, err := c.rpc.ClaimExecutionEpoch(ctx, &pb.ExecutionEpochClaim{
		ExecutionGeneration:   generation,
		LeaseTtlMilliseconds:  ttl.Milliseconds(),
		ExecutionLeaseOwnerId: strings.TrimSpace(leaseOwnerID),
	})
	if err != nil {
		return ExecutionEpochResult{}, err
	}
	return ExecutionEpochResult{
		Accepted:          ack.GetAccepted(),
		CurrentGeneration: ack.GetCurrentGeneration(),
		ExpiresAt:         time.UnixMilli(ack.GetExpiresAtUnixMs()),
		ErrorMessage:      ack.GetErrorMessage(),
	}, nil
}

func (c *Client) AssignWork(ctx context.Context, assignment Assignment) (AssignmentResult, error) {
	ack, err := c.rpc.AssignWork(ctx, toProtoAssignment(assignment))
	if err != nil {
		return AssignmentResult{}, err
	}

	return AssignmentResult{
		Accepted:          ack.GetAccepted(),
		ErrorMessage:      ack.GetErrorMessage(),
		AvailableCapacity: ack.GetAvailableCapacity(),
	}, nil
}

func (c *Client) CommitWork(ctx context.Context, testID string, generation uint64) (WorkCommitResult, error) {
	return c.CommitWorkAt(ctx, testID, generation, time.Time{})
}

func (c *Client) CommitWorkAt(ctx context.Context, testID string, generation uint64, startAt time.Time) (WorkCommitResult, error) {
	request := &pb.WorkCommitRequest{TestId: testID, ExecutionGeneration: generation}
	if !startAt.IsZero() {
		request.StartAtUnixNano = startAt.UnixNano()
	}
	ack, err := c.rpc.CommitWork(ctx, request)
	if err != nil {
		return WorkCommitResult{}, err
	}
	return WorkCommitResult{Accepted: ack.GetAccepted(), ErrorMessage: ack.GetErrorMessage()}, nil
}

func (c *Client) AdjustVUsers(ctx context.Context, adjustment VUserAdjustment) (AdjustmentResult, error) {
	ack, err := c.rpc.AdjustVUsers(ctx, &pb.VUserAdjustment{
		TestId:              adjustment.TestID,
		ExecutionGeneration: adjustment.ExecutionGeneration,
		GroupName:           adjustment.GroupName,
		TargetVusers:        adjustment.TargetVUsers,
		RampSeconds:         adjustment.RampSeconds,
		RampDuration:        preciseDurationProto(adjustment.RampDuration, int64(adjustment.RampSeconds)),
	})
	if err != nil {
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
// signal, a successful return proves the generator applied the engine state.
// OperationID makes retries after a lost response idempotent.
func (c *Client) SetPaused(ctx context.Context, control PauseControl) (PauseResult, error) {
	ack, err := c.rpc.SetPaused(ctx, &pb.PauseRequest{
		TestId:              control.TestID,
		ExecutionGeneration: control.ExecutionGeneration,
		Pause:               control.Pause,
		OperationId:         control.OperationID,
		OperationSequence:   control.OperationSequence,
	})
	if err != nil {
		return PauseResult{}, err
	}
	return PauseResult{
		Accepted:                 ack.GetAccepted(),
		CurrentPaused:            ack.GetCurrentPaused(),
		AppliedOperationID:       ack.GetAppliedOperationId(),
		AppliedOperationSequence: ack.GetAppliedOperationSequence(),
		ErrorMessage:             ack.GetErrorMessage(),
	}, nil
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

	return &MetricsStream{stream: stream, cancel: cancel}, nil
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
		return &MetricsStream{stream: result.stream, cancel: cancel}, nil
	}
}

func (c *Client) StopTest(ctx context.Context, testID string, graceful bool) (StopResult, error) {
	return c.StopTestAtGeneration(ctx, testID, graceful, 0)
}

func (c *Client) StopTestAtGeneration(ctx context.Context, testID string, graceful bool, generation uint64) (StopResult, error) {
	ack, err := c.rpc.StopTest(ctx, &pb.StopRequest{TestId: testID, Graceful: graceful, ExecutionGeneration: generation})
	if err != nil {
		return StopResult{}, err
	}

	return StopResult{
		Acknowledged:  ack.GetAcknowledged(),
		PendingVUsers: ack.GetPendingVusers(),
	}, nil
}

func (c *Client) GetArchiveArtifact(ctx context.Context, request ArchiveArtifactRequest) ([]byte, error) {
	artifact, err := c.rpc.GetArchiveArtifact(
		ctx,
		&pb.ArchiveArtifactRequest{TestId: request.TestID, Name: request.Name},
		grpc.MaxCallRecvMsgSize(grpcMessageSize(archiveArtifactUnaryReceiveMaxSize)),
	)
	if err != nil {
		return nil, err
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
	stream, err := c.rpc.StreamArchiveArtifact(ctx, &pb.ArchiveArtifactRequest{TestId: request.TestID, Name: request.Name})
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

	return &HeartbeatStream{stream: stream}, nil
}

func (s *HeartbeatStream) Send(heartbeat Heartbeat) error {
	return s.stream.Send(&pb.HeartbeatPing{
		GeneratorId:     heartbeat.GeneratorID,
		TimestampUnixMs: heartbeat.TimestampUnixMs,
		Stats:           toProtoStats(heartbeat.Stats),
	})
}

func (s *HeartbeatStream) Receive() (HeartbeatResponse, error) {
	pong, err := s.stream.Recv()
	if err != nil {
		return HeartbeatResponse{}, err
	}
	stats := generatorStatsFromProto(pong.GetStats())

	return HeartbeatResponse{
		GeneratorID:        pong.GetGeneratorId(),
		TimestampUnixMs:    pong.GetTimestampUnixMs(),
		ActiveTestIDs:      pong.GetActiveTestIds(),
		Stats:              stats,
		AvailableCapacity:  pong.GetAvailableCapacity(),
		CPULimitCores:      pong.GetCpuLimitCores(),
		MemoryLimitBytes:   pong.GetMemoryLimitBytes(),
		MaxVUsers:          pong.GetMaxVusers(),
		Version:            pong.GetVersion(),
		SupportedProtocols: append([]string(nil), pong.GetSupportedProtocols()...),
		CompatibleFeatures: append([]string(nil), pong.GetCompatibleFeatures()...),
	}, nil
}

func (s *HeartbeatStream) CloseSend() error {
	return s.stream.CloseSend()
}

func (s *MetricsStream) SendPause(control PauseControl) error {
	return s.stream.Send(&pb.ControlSignal{
		Signal: &pb.ControlSignal_Pause{
			Pause: &pb.PauseRequest{
				TestId:              control.TestID,
				ExecutionGeneration: control.ExecutionGeneration,
				Pause:               control.Pause,
				OperationId:         control.OperationID,
				OperationSequence:   control.OperationSequence,
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
				ServerProcessingMs:     tx.GetServerTelemetry().GetServerProcessingMs(),
				TransferMs:             tx.GetServerTelemetry().GetTransferMs(),
				RequestPayloadKB:       tx.GetServerTelemetry().GetRequestPayloadKb(),
				ResponsePayloadKB:      tx.GetServerTelemetry().GetResponsePayloadKb(),
				ConnectionReused:       tx.GetServerTelemetry().GetConnectionReused(),
				RequestSizeClass:       tx.GetServerTelemetry().GetRequestSizeClass(),
				ResponseSizeClass:      tx.GetServerTelemetry().GetResponseSizeClass(),
				FirstEventMs:           tx.GetServerTelemetry().GetFirstEventMs(),
				EventCount:             tx.GetServerTelemetry().GetEventCount(),
				MessageLatencyMs:       tx.GetServerTelemetry().GetMessageLatencyMs(),
				MessagesSent:           tx.GetServerTelemetry().GetMessagesSent(),
				MessagesReceived:       tx.GetServerTelemetry().GetMessagesReceived(),
				MessagesSentPerSec:     tx.GetServerTelemetry().GetMessagesSentPerSec(),
				MessagesReceivedPerSec: tx.GetServerTelemetry().GetMessagesReceivedPerSec(),
				ConsumerLagMs:          tx.GetServerTelemetry().GetConsumerLagMs(),
				SendFailureCount:       tx.GetServerTelemetry().GetSendFailureCount(),
				RedeliveryCount:        tx.GetServerTelemetry().GetRedeliveryCount(),
				DeadLetteredCount:      tx.GetServerTelemetry().GetDeadLetteredCount(),
				DuplicateCount:         tx.GetServerTelemetry().GetDuplicateCount(),
				OutOfOrderCount:        tx.GetServerTelemetry().GetOutOfOrderCount(),
				CommitCount:            tx.GetServerTelemetry().GetCommitCount(),
				AbortCount:             tx.GetServerTelemetry().GetAbortCount(),
				ConsumerOffsetLag:      tx.GetServerTelemetry().GetConsumerOffsetLag(),
				ThrottledRequests:      tx.GetServerTelemetry().GetThrottledRequests(),
				ServerBusyCount:        tx.GetServerTelemetry().GetServerBusyCount(),
				QuotaExceededCount:     tx.GetServerTelemetry().GetQuotaExceededCount(),
				EventTooLargeCount:     tx.GetServerTelemetry().GetEventTooLargeCount(),
				EndToEndLatencyMs:      tx.GetServerTelemetry().GetEndToEndLatencyMs(),
				PartitionID:            tx.GetServerTelemetry().GetPartitionId(),
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
			DurationMs:       tx.GetDurationMs(),
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
	var telemetry *MetricsTelemetry
	if batch.GetTelemetry() != nil {
		telemetry = &MetricsTelemetry{
			DroppedResults:           batch.GetTelemetry().GetDroppedResults(),
			SheddedArrivals:          batch.GetTelemetry().GetSheddedArrivals(),
			CollectedAtUnixMs:        batch.GetTelemetry().GetCollectedAtUnixMs(),
			PublishedAtUnixMs:        batch.GetTelemetry().GetPublishedAtUnixMs(),
			SampleAgeMs:              batch.GetTelemetry().GetSampleAgeMs(),
			GeneratorProcessingLagMs: batch.GetTelemetry().GetGeneratorProcessingLagMs(),
			TransportLagMs:           batch.GetTelemetry().GetTransportLagMs(),
			DroppedSubscriberBatches: batch.GetTelemetry().GetDroppedSubscriberBatches(),
		}
	}

	return MetricsBatch{
		TestID:                batch.GetTestId(),
		ExecutionGeneration:   batch.GetExecutionGeneration(),
		GeneratorID:           batch.GetGeneratorId(),
		TimestampUnixMs:       batch.GetTimestampUnixMs(),
		WindowMs:              batch.GetWindowMs(),
		Stats:                 stats,
		Transactions:          transactions,
		FleetMetrics:          metricAggregateFromProto(batch.GetFleetMetrics()),
		TransactionMetrics:    metricAggregatesFromProto(batch.GetTransactionMetrics()),
		DependencyMetrics:     metricAggregatesFromProto(batch.GetDependencyMetrics()),
		SOAPMetrics:           metricAggregatesFromProto(batch.GetSoapMetrics()),
		NetworkProfileMetrics: metricAggregatesFromProto(batch.GetNetworkProfileMetrics()),
		Browser:               browserMetricsFromProto(batch.GetBrowserMetrics()),
		BrowserByDevice:       browserDeviceMetricsFromProto(batch.GetBrowserDeviceMetrics()),
		Groups:                groups,
		LifecycleState:        batch.GetLifecycleState(),
		LifecycleError:        batch.GetLifecycleError(),
		Telemetry:             telemetry,
	}, nil
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
	return &pb.WorkAssignment{
		TestId:                assignment.TestID,
		ExecutionGeneration:   assignment.ExecutionGeneration,
		GroupName:             assignment.GroupName,
		TargetDurationSeconds: compatibilityDurationSeconds(targetDuration),
		TargetDuration:        preciseDurationProto(targetDuration, 0),
		DurationOffset:        preciseDurationProto(assignment.DurationOffset, 0),
		ScriptId:              assignment.ScriptID,
		ExpectedScriptHash:    assignment.ExpectedScriptHash,
		ScriptContent:         assignment.ScriptContent,
		VuserCount:            assignment.VUserCount,
		RampUpSeconds:         compatibilityDurationSecondsInt32(rampUp),
		RampUp:                preciseDurationProto(rampUp, 0),
		RampDown:              preciseDurationProto(assignment.RampDown, 0),
		InitStrategy:          assignment.InitStrategy,
		PreBaseline:           preciseDurationProto(assignment.PreBaseline, 0),
		PostBaseline:          preciseDurationProto(assignment.PostBaseline, 0),
		ShardOrdinal:          assignment.ShardOrdinal,
		ShardCount:            assignment.ShardCount,
		VuserIdOffset:         assignment.VUserIDOffset,
		StartPaused:           assignment.StartPaused,
		Environment:           environment,
		DataFiles:             dataFiles,
		RedactionRules:        redactionRulesToProto(assignment.RedactionRules),
		SecretBundle:          cloneSecretBundle(assignment.SecretBundle),
		SecretBundleId:        assignment.SecretBundleID,
	}
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
	}
}

func metricAggregateToProto(value *MetricAggregate) *pb.MetricAggregate {
	if value == nil {
		return nil
	}
	return &pb.MetricAggregate{
		Name:             value.Name,
		DependencyType:   value.DependencyType,
		DependencyTarget: value.DependencyTarget,
		Count:            value.Count,
		Errors:           value.Errors,
		AvgMs:            value.AvgMs,
		P50Ms:            value.P50Ms,
		P90Ms:            value.P90Ms,
		P95Ms:            value.P95Ms,
		P99Ms:            value.P99Ms,
		AvgDnsMs:         value.AvgDNSMs,
		AvgConnectMs:     value.AvgConnectMs,
		AvgTlsMs:         value.AvgTLSMs,
		AvgTtfbMs:        value.AvgTTFBMs,
		BytesSent:        value.BytesSent,
		BytesReceived:    value.BytesReceived,
		Tps:              value.TPS,
		DurationDigest:   tdigestSnapshotToProto(value.DurationDigest),
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

package generatorrpc

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
)

type DataFile struct {
	Name    string
	Content []byte
}

type Assignment struct {
	TestID                string
	GroupName             string
	TargetDurationSeconds int64
	ScriptID              string
	ScriptContent         []byte
	VUserCount            int32
	RampUpSeconds         int32
	Environment           map[string]string
	DataFiles             []DataFile
}

type AssignmentResult struct {
	Accepted          bool
	ErrorMessage      string
	AvailableCapacity int32
}

type VUserAdjustment struct {
	TestID       string
	GroupName    string
	TargetVUsers int32
	RampSeconds  int32
}

type AdjustmentResult struct {
	Accepted      bool
	CurrentVUsers int32
}

type DurationAdjustment struct {
	TestID                string
	TargetDurationSeconds int64
}

type DurationResult struct {
	Accepted               bool
	CurrentDurationSeconds int64
}

type PauseControl struct {
	TestID string
	Pause  bool
}

type MetricsBatch struct {
	TestID          string
	GeneratorID     string
	TimestampUnixMs int64
	WindowMs        int64
	Stats           GeneratorStats
	Transactions    []TransactionMetric
	Groups          []GroupMetric
	LifecycleState  string
	LifecycleError  string
	Telemetry       *MetricsTelemetry
}

type MetricsTelemetry struct {
	DroppedResults             int64
	SheddedArrivals            int64
	CollectedAtUnixMs          int64
	PublishedAtUnixMs          int64
	ControllerReceivedAtUnixMs int64
	SampleAgeMs                float64
	// WindowMs is the elapsed wall-clock duration this group's drained
	// transactions cover. It is generator-internal (merged to the batch's
	// window_ms) and is not transmitted on the telemetry proto message.
	WindowMs int64
}

type GroupMetric struct {
	Name         string
	ActiveVUsers int32
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
	ID          string
	Content     []byte
	ContentHash string
}

type ScriptResult struct {
	Cached      bool
	ContentHash string
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
	stream pb.GeneratorControl_StreamMetricsClient
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
		ScriptId:    script.ID,
		Content:     script.Content,
		ContentHash: script.ContentHash,
	})
	if err != nil {
		return ScriptResult{}, err
	}

	return ScriptResult{
		Cached:      ack.GetCached(),
		ContentHash: ack.GetContentHash(),
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

func (c *Client) AdjustVUsers(ctx context.Context, adjustment VUserAdjustment) (AdjustmentResult, error) {
	ack, err := c.rpc.AdjustVUsers(ctx, &pb.VUserAdjustment{
		TestId:       adjustment.TestID,
		GroupName:    adjustment.GroupName,
		TargetVusers: adjustment.TargetVUsers,
		RampSeconds:  adjustment.RampSeconds,
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
	ack, err := c.rpc.AdjustDuration(ctx, &pb.DurationAdjustment{
		TestId:                adjustment.TestID,
		TargetDurationSeconds: adjustment.TargetDurationSeconds,
	})
	if err != nil {
		return DurationResult{}, err
	}

	return DurationResult{
		Accepted:               ack.GetAccepted(),
		CurrentDurationSeconds: ack.GetCurrentDurationSeconds(),
	}, nil
}

func (c *Client) OpenMetricsStream(ctx context.Context) (*MetricsStream, error) {
	stream, err := c.rpc.StreamMetrics(ctx)
	if err != nil {
		return nil, err
	}

	return &MetricsStream{stream: stream}, nil
}

func (c *Client) StopTest(ctx context.Context, testID string, graceful bool) (StopResult, error) {
	ack, err := c.rpc.StopTest(ctx, &pb.StopRequest{TestId: testID, Graceful: graceful})
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
		grpc.MaxCallRecvMsgSize(64<<20),
	)
	if err != nil {
		return nil, err
	}
	return artifact.GetContent(), nil
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
				TestId: control.TestID,
				Pause:  control.Pause,
			},
		},
	})
}

func (s *MetricsStream) CloseSend() error {
	return s.stream.CloseSend()
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
		})
	}

	stats := generatorStatsFromProto(batch.GetStats())
	var telemetry *MetricsTelemetry
	if batch.GetTelemetry() != nil {
		telemetry = &MetricsTelemetry{
			DroppedResults:    batch.GetTelemetry().GetDroppedResults(),
			SheddedArrivals:   batch.GetTelemetry().GetSheddedArrivals(),
			CollectedAtUnixMs: batch.GetTelemetry().GetCollectedAtUnixMs(),
			PublishedAtUnixMs: batch.GetTelemetry().GetPublishedAtUnixMs(),
			SampleAgeMs:       batch.GetTelemetry().GetSampleAgeMs(),
		}
	}

	return MetricsBatch{
		TestID:          batch.GetTestId(),
		GeneratorID:     batch.GetGeneratorId(),
		TimestampUnixMs: batch.GetTimestampUnixMs(),
		WindowMs:        batch.GetWindowMs(),
		Stats:           stats,
		Transactions:    transactions,
		Groups:          groups,
		LifecycleState:  batch.GetLifecycleState(),
		LifecycleError:  batch.GetLifecycleError(),
		Telemetry:       telemetry,
	}, nil
}

func toProtoAssignment(assignment Assignment) *pb.WorkAssignment {
	dataFiles := make([]*pb.DataFile, 0, len(assignment.DataFiles))
	for _, dataFile := range assignment.DataFiles {
		dataFiles = append(dataFiles, &pb.DataFile{Name: dataFile.Name, Content: dataFile.Content})
	}

	environment := assignment.Environment
	if environment == nil {
		environment = map[string]string{}
	}

	return &pb.WorkAssignment{
		TestId:                assignment.TestID,
		GroupName:             assignment.GroupName,
		TargetDurationSeconds: assignment.TargetDurationSeconds,
		ScriptId:              assignment.ScriptID,
		ScriptContent:         assignment.ScriptContent,
		VuserCount:            assignment.VUserCount,
		RampUpSeconds:         assignment.RampUpSeconds,
		Environment:           environment,
		DataFiles:             dataFiles,
	}
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

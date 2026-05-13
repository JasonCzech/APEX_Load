package generatorrpc

import (
	"context"

	pb "github.com/jasonczech/stormrunner/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type DataFile struct {
	Name    string
	Content []byte
}

type Assignment struct {
	TestID        string
	GroupName     string
	ScriptID      string
	ScriptContent []byte
	VUserCount    int32
	RampUpSeconds int32
	Environment   map[string]string
	DataFiles     []DataFile
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
	Stats           GeneratorStats
	Transactions    []TransactionMetric
}

type TransactionMetric struct {
	Name          string
	DurationMs    float64
	StatusCode    int32
	Success       bool
	ErrorMessage  string
	BytesSent     int64
	BytesReceived int64
}

type StopResult struct {
	Acknowledged bool
	PendingVUsers int32
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
	TimestampUnixMs int64
	ActiveTestIDs   []string
}

type GeneratorStats struct {
	ActiveVUsers   int32
	CPUPercent     float64
	MemoryBytes    int64
	GoroutineCount int32
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
	dialOptions := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
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
		Acknowledged: ack.GetAcknowledged(),
		PendingVUsers: ack.GetPendingVusers(),
	}, nil
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

	return HeartbeatResponse{
		TimestampUnixMs: pong.GetTimestampUnixMs(),
		ActiveTestIDs:   pong.GetActiveTestIds(),
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
		transactions = append(transactions, TransactionMetric{
			Name:          tx.GetName(),
			DurationMs:    tx.GetDurationMs(),
			StatusCode:    tx.GetStatusCode(),
			Success:       tx.GetSuccess(),
			ErrorMessage:  tx.GetErrorMessage(),
			BytesSent:     tx.GetBytesSent(),
			BytesReceived: tx.GetBytesReceived(),
		})
	}

	stats := GeneratorStats{}
	if batch.GetStats() != nil {
		stats = GeneratorStats{
			ActiveVUsers:   batch.GetStats().GetActiveVusers(),
			CPUPercent:     batch.GetStats().GetCpuPercent(),
			MemoryBytes:    batch.GetStats().GetMemoryBytes(),
			GoroutineCount: batch.GetStats().GetGoroutineCount(),
		}
	}

	return MetricsBatch{
		TestID:          batch.GetTestId(),
		GeneratorID:     batch.GetGeneratorId(),
		TimestampUnixMs: batch.GetTimestampUnixMs(),
		Stats:           stats,
		Transactions:    transactions,
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
		TestId:        assignment.TestID,
		GroupName:     assignment.GroupName,
		ScriptId:      assignment.ScriptID,
		ScriptContent: assignment.ScriptContent,
		VuserCount:    assignment.VUserCount,
		RampUpSeconds: assignment.RampUpSeconds,
		Environment:   environment,
		DataFiles:     dataFiles,
	}
}

func toProtoStats(stats GeneratorStats) *pb.GeneratorStats {
	return &pb.GeneratorStats{
		ActiveVusers:   stats.ActiveVUsers,
		CpuPercent:     stats.CPUPercent,
		MemoryBytes:    stats.MemoryBytes,
		GoroutineCount: stats.GoroutineCount,
	}
}
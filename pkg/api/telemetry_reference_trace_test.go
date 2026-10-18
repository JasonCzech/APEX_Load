package api

import (
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
)

const telemetryReferenceTraceLimit = 8192

// Per-publication timings distinguish a delayed collection, a queued send,
// controller aggregation, and a terminal replay of an earlier sample. This is
// diagnostic evidence only: it neither filters nor changes the acceptance gate.
type telemetryReferenceBatchTrace struct {
	TestID             string  `json:"test_id"`
	Generator          string  `json:"generator"`
	Sequence           uint64  `json:"sequence"`
	CollectionSequence uint64  `json:"collection_sequence"`
	Lifecycle          string  `json:"lifecycle"`
	BatchAt            int64   `json:"batch_at_unix_ms"`
	CollectedAt        int64   `json:"collected_at_unix_ms"`
	PublishedAt        int64   `json:"published_at_unix_ms"`
	SendStartedAt      int64   `json:"send_started_at_unix_ms"`
	SendDurationMs     float64 `json:"send_duration_ms"`
	ProcessingMs       float64 `json:"processing_ms"`
	WindowCount        int64   `json:"window_count"`
	ActiveVUsers       int32   `json:"active_vusers"`
	SendFailed         bool    `json:"send_failed,omitempty"`
}

type telemetryReferenceTrace struct {
	mu      sync.Mutex
	batches []telemetryReferenceBatchTrace
	dropped int
}

func (trace *telemetryReferenceTrace) add(batch *pb.MetricsBatch, started time.Time, elapsed time.Duration, err error) {
	if batch == nil {
		return
	}
	evidence := telemetryReferenceBatchTrace{
		CollectionSequence: batch.GetTelemetry().GetCollectionSequence(), TestID: batch.GetTestId(), Generator: batch.GetGeneratorId(), Sequence: batch.GetBatchSequence(), Lifecycle: batch.GetLifecycleState(), BatchAt: batch.GetTimestampUnixMs(),
		CollectedAt: batch.GetTelemetry().GetCollectedAtUnixMs(), PublishedAt: batch.GetTelemetry().GetPublishedAtUnixMs(), ProcessingMs: batch.GetTelemetry().GetGeneratorProcessingLagMs(),
		SendStartedAt: started.UnixMilli(), SendDurationMs: float64(elapsed.Microseconds()) / 1000, WindowCount: batch.GetFleetMetrics().GetCount(), ActiveVUsers: batch.GetStats().GetActiveVusers(), SendFailed: err != nil,
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if len(trace.batches) >= telemetryReferenceTraceLimit {
		trace.dropped++
		return
	}
	trace.batches = append(trace.batches, evidence)
}
func (trace *telemetryReferenceTrace) forTest(id string) ([]telemetryReferenceBatchTrace, int) {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	var selected []telemetryReferenceBatchTrace
	for _, batch := range trace.batches {
		if batch.TestID == id {
			selected = append(selected, batch)
		}
	}
	return selected, trace.dropped
}

type telemetryReferenceTraceStream struct {
	grpc.ServerStream
	trace *telemetryReferenceTrace
}

func (stream *telemetryReferenceTraceStream) SendMsg(message any) error {
	started := time.Now()
	err := stream.ServerStream.SendMsg(message)
	if batch, ok := message.(*pb.MetricsBatch); ok {
		stream.trace.add(batch, started, time.Since(started), err)
	}
	return err
}
func (trace *telemetryReferenceTrace) intercept(service any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return handler(service, &telemetryReferenceTraceStream{ServerStream: stream, trace: trace})
}

type telemetryReferencePublicationTrace struct {
	Timestamp            time.Time `json:"timestamp"`
	ActiveVUsers         int64     `json:"active_vusers"`
	CollectedAt          time.Time `json:"collected_at"`
	GeneratorPublishedAt time.Time `json:"generator_published_at"`
	ReceivedAt           time.Time `json:"received_at"`
	PublishedAt          time.Time `json:"published_at"`
	SampleAgeMs          float64   `json:"sample_age_ms"`
	ProcessingMs         float64   `json:"processing_ms"`
	TransportMs          float64   `json:"transport_ms"`
	FanoutMs             float64   `json:"fanout_ms"`
}

func telemetryReferencePublicationTraces(history []*LiveMetrics) []telemetryReferencePublicationTrace {
	out := make([]telemetryReferencePublicationTrace, 0, min(len(history), telemetryReferenceTraceLimit))
	for _, live := range history {
		if live == nil || live.Telemetry == nil {
			continue
		}
		if len(out) >= telemetryReferenceTraceLimit {
			break
		}
		tele := live.Telemetry
		out = append(out, telemetryReferencePublicationTrace{Timestamp: live.Timestamp, ActiveVUsers: live.ActiveVUsers, CollectedAt: tele.CollectedAt, GeneratorPublishedAt: tele.GeneratorPublishedAt, ReceivedAt: tele.ControllerReceivedAt, PublishedAt: tele.PublishedAt, SampleAgeMs: tele.SampleAgeMs, ProcessingMs: tele.GeneratorProcessingLagMs, TransportMs: tele.TransportLagMs, FanoutMs: tele.ControllerFanoutLagMs})
	}
	return out
}

func TestTelemetryReferenceTraceKeepsTimingAndBounds(t *testing.T) {
	trace := &telemetryReferenceTrace{}
	trace.add(nil, time.Now(), 0, nil)
	started := time.UnixMilli(5000)
	for i := 0; i < telemetryReferenceTraceLimit+1; i++ {
		id := "warm"
		if i > 0 {
			id = "measured"
		}
		trace.add(&pb.MetricsBatch{TestId: id, GeneratorId: "gen-a", BatchSequence: uint64(i + 1), LifecycleState: "RUNNING", Telemetry: &pb.MetricsBatchTelemetry{CollectedAtUnixMs: 1000, PublishedAtUnixMs: 2000, CollectionSequence: 7}}, started, time.Millisecond, nil)
	}
	samples, dropped := trace.forTest("measured")
	if len(samples) != telemetryReferenceTraceLimit-1 || dropped != 1 {
		t.Fatalf("trace size=%d dropped=%d", len(samples), dropped)
	}
	if samples[0].CollectionSequence != 7 || samples[0].CollectedAt != 1000 || samples[0].PublishedAt != 2000 || samples[0].SendStartedAt != 5000 || samples[0].SendDurationMs != 1 {
		t.Fatalf("timing changed: %+v", samples[0])
	}
	samples[0].Sequence = 0
	again, _ := trace.forTest("measured")
	if again[0].Sequence == 0 {
		t.Fatal("trace returned mutable retained state")
	}
}

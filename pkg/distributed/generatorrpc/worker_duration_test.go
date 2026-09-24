package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

type oneTimingBatchStream struct {
	pb.GeneratorControl_StreamMetricsClient
	batch *pb.MetricsBatch
}

func (s oneTimingBatchStream) Recv() (*pb.MetricsBatch, error) { return s.batch, nil }

func TestSamplerAndWorkerDurationsSurviveMetricsWire(t *testing.T) {
	buffer := &transactionBuffer{}
	if err := buffer.RecordTransaction(&protocol.Result{ActionName: "batch", Duration: 5 * time.Millisecond, WorkerDuration: 17 * time.Millisecond, Success: true}); err != nil {
		t.Fatal(err)
	}
	transactions := buffer.Drain()
	service := &Service{}
	batch := service.metricsBatchForMetrics("run", 1, GeneratorStats{}, transactions, metricsAggregateSet{}, false, nil, "RUNNING", "", nil)
	payload, err := proto.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.MetricsBatch
	if err = proto.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	stream := &MetricsStream{stream: oneTimingBatchStream{batch: &decoded}}
	received, err := stream.Receive()
	if err != nil || len(received.Transactions) != 1 {
		t.Fatal(received, err)
	}
	tx := received.Transactions[0]
	if tx.DurationMs != 5 || tx.WorkerDurationMs != 17 {
		t.Fatalf("sample=%v worker=%v", tx.DurationMs, tx.WorkerDurationMs)
	}
}

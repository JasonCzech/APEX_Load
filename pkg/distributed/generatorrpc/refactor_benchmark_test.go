package generatorrpc

import (
	"context"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
)

// BenchmarkMetricsPublication intentionally exercises the Service facade so
// it can be run against both the pre-refactor implementation and the extracted
// metrics broker. Keeping the benchmark at this compatibility boundary guards
// the real publication path rather than an implementation-only helper.
func BenchmarkMetricsPublication(b *testing.B) {
	service := newIsolatedService(b, 10)
	const (
		executionGeneration = uint64(0)
		testID              = "metrics-publication-benchmark"
	)

	id, subscriber, _, err := service.registerScopedSubscriber(executionGeneration, testID)
	if err != nil {
		b.Fatalf("register metrics subscriber: %v", err)
	}
	defer service.unregisterSubscriber(id)

	batch := &pb.MetricsBatch{
		TestId:              testID,
		ExecutionGeneration: executionGeneration,
		Stats: &pb.GeneratorStats{
			ActiveVusers: 200,
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		service.broadcastMetrics(batch)
		if got := subscriber.pop(); got == nil {
			b.Fatal("published batch was not delivered")
		}
	}
}

// BenchmarkAssignWorkIdempotentControl exercises the transport validation,
// generation/capacity decision, and duplicate-stage path without starting an
// engine. It is portable across the coordinator migration and guards the
// controller retry hot path.
func BenchmarkAssignWorkIdempotentControl(b *testing.B) {
	service := newIsolatedService(b, 10)
	assignment := &pb.WorkAssignment{
		TestId:        "assign-work-control-benchmark",
		GroupName:     "default",
		ScriptId:      "script",
		ScriptContent: []byte(`{"name":"benchmark"}`),
		VuserCount:    1,
	}
	if ack, err := service.AssignWork(context.Background(), assignment); err != nil || !ack.GetAccepted() {
		b.Fatalf("initial AssignWork = (%#v, %v)", ack, err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ack, err := service.AssignWork(context.Background(), assignment)
		if err != nil || !ack.GetAccepted() {
			b.Fatalf("idempotent AssignWork = (%#v, %v)", ack, err)
		}
	}
}

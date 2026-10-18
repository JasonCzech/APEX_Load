package generatorrpc

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func awaitFinalizingTestBatch(t *testing.T, subscriber *metricsSubscriber) *pb.MetricsBatch {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if batch := subscriber.pop(); batch != nil {
			return batch
		}
		select {
		case <-subscriber.wake:
		case <-deadline.C:
			t.Fatal("metrics publication timed out")
		}
	}
}

// Archive finalization can outlive the last metric drain. Its explicit phase
// must reach current and reconnecting subscribers without completing the run
// early, including when the eventual durable archive operation fails.
func TestFinalizingMetricsRetainPhaseUntilDurableOutcome(t *testing.T) {
	for _, failArchive := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "archive-failure"}[failArchive], func(t *testing.T) {
			service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
			if ack := claimEpoch(t, service, 7, time.Minute); !ack.GetAccepted() {
				t.Fatalf("execution generation rejected: %v", ack)
			}
			service.archiveManager.setFinalizerSlotsForTest(make(chan struct{}, 1))
			release := service.archiveManager.acquireFinalizerSlot()
			var releaseOnce sync.Once
			releaseSlot := func() { releaseOnce.Do(release) }
			writerID := service.generatorID
			if failArchive {
				writerID = "foreign-namespace" // Registration must fail after finalization.
			}
			writer, err := newArchiveRecorder(writerID, "run", "Finalizing")
			if err != nil {
				releaseSlot()
				t.Fatal(err)
			}
			id, subscriber, _, err := service.registerScopedSubscriber(7, "run")
			if err != nil {
				releaseSlot()
				t.Fatal(err)
			}
			defer service.unregisterSubscriber(id)
			assignment := assignedTest{
				executionGeneration: 7, committed: true, metricsCollection: 9,
				groups:         map[string]int32{"finished-first": 2, "finished-last": 3},
				activeGroups:   map[string]int32{"finished-last": 0},
				groupTelemetry: map[string]MetricsTelemetry{"finished-last": {CollectedAtUnixMs: 1000, PublishedAtUnixMs: 1005, WindowMs: 1000}},
			}
			service.mu.Lock()
			service.markStoppingLocked("run", 7)
			service.mu.Unlock()
			done := make(chan string, 1)
			t.Cleanup(func() {
				releaseSlot()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("finalizer did not finish during cleanup")
				}
			})
			go func() {
				state, _ := service.finishStoppedTestWithState("run", assignment, writer, "COMPLETE", "")
				done <- state
			}()
			phase := awaitFinalizingTestBatch(t, subscriber)
			if phase.GetLifecycleState() != MetricsLifecycleFinalizing || phase.GetTelemetry().GetCollectionSequence() != 9 || phase.GetTelemetry().GetCollectedAtUnixMs() != 1000 || phase.GetStats().GetActiveVusers() != 0 || len(phase.Groups) != 2 {
				t.Fatalf("finalization phase changed the drain: %v", phase)
			}
			for _, group := range phase.Groups {
				if group.ActiveVusers != 0 {
					t.Fatalf("completed group still active: %v", group)
				}
			}
			if isGeneratorTerminalLifecycle(phase.GetLifecycleState()) || service.metricsBroker.hasTerminal(terminalBatchKey{testID: "run", generation: 7}) {
				t.Fatal("archive-pending phase became a terminal outcome")
			}
			if service.metricsBatchIsCurrent(&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 8, LifecycleState: MetricsLifecycleFinalizing}) || service.metricsBatchIsCurrent(&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "RUNNING"}) {
				t.Fatal("stale generation or running snapshot bypassed the stopping fence")
			}
			for attempt := 0; attempt < 2; attempt++ {
				reconnectID, _, initial, err := service.registerScopedSubscriber(7, "run")
				if err != nil {
					t.Fatal(err)
				}
				service.unregisterSubscriber(reconnectID)
				if len(initial) != 1 || initial[0].GetLifecycleState() != MetricsLifecycleFinalizing || initial[0].GetBatchSequence() != phase.GetBatchSequence() || initial[0].GetTelemetry().GetCollectedAtUnixMs() != 1000 {
					t.Fatalf("reconnect lost immutable finalization phase: %v", initial)
				}
				initial[0].Telemetry.CollectedAtUnixMs = 999 // Must not mutate retained replay.
			}
			if _, _, wrong, err := service.registerScopedSubscriber(8, "run"); status.Code(err) != codes.FailedPrecondition || len(wrong) != 0 {
				t.Fatalf("another execution generation bypassed admission: snapshots=%v error=%v", wrong, err)
			}
			releaseSlot()
			terminal := awaitFinalizingTestBatch(t, subscriber)
			wantState := "COMPLETE"
			if failArchive {
				wantState = "FAILED"
				if !strings.Contains(terminal.GetLifecycleError(), "archive finalization failed") {
					t.Fatalf("durable failure was lost: %v", terminal)
				}
			}
			if terminal.GetLifecycleState() != wantState || terminal.GetBatchSequence() <= phase.GetBatchSequence() || terminal.GetTelemetry().GetCollectionSequence() != 9 {
				t.Fatalf("terminal outcome/order changed: %v", terminal)
			}
			service.metricsBroker.mu.RLock()
			pending := len(service.metricsBroker.finalizingBatches)
			service.metricsBroker.mu.RUnlock()
			if pending != 0 || service.metricsBatchIsCurrent(phase) {
				t.Fatal("finalization phase outlived terminal ownership")
			}
		})
	}
}

func TestFinalizingMetricsRejectedWithoutExactStoppingOwner(t *testing.T) {
	service := NewService("finalizing-fence", 1)
	phase := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: MetricsLifecycleFinalizing}
	service.metricsBroker.publishFinalizing(phase, service.metricsBatchIsCurrent)
	service.mu.Lock()
	service.markStoppingLocked("run", 8)
	service.mu.Unlock()
	service.metricsBroker.publishFinalizing(phase, service.metricsBatchIsCurrent)
	service.metricsBroker.mu.RLock()
	pending := len(service.metricsBroker.finalizingBatches)
	service.metricsBroker.mu.RUnlock()
	if pending != 0 {
		t.Fatal("unowned finalization phase was retained")
	}
}

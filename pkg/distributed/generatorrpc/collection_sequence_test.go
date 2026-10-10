package generatorrpc

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func collectionSequenceExecution(t *testing.T, group string, transactions int) *groupExecution {
	t.Helper()
	results := &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)}
	for index := 0; index < transactions; index++ {
		if err := results.RecordTransaction(&protocol.Result{
			ActionName: "checkout", Group: group, Duration: 25 * time.Millisecond,
			Success: true, BusinessTransaction: true, StartTime: time.Now(),
		}); err != nil {
			t.Fatalf("record %s transaction: %v", group, err)
		}
	}
	return &groupExecution{groupName: group, results: results}
}

// A batch carries the number of the collection that produced its window, so
// the controller counts a re-sent window once. A stop-time drain is a new
// window: it replaces the windows already published, so a group that ended
// earlier does not carry its final window into the terminal batch again.
func TestTerminalBatchCarriesOnlyTheStopDrainUnderANewCollection(t *testing.T) {
	service := newIsolatedService(t, 50)
	assignment := assignedTest{
		testID: "run", committed: true, vuserCount: 2,
		groups: map[string]int32{"buyers": 1, "sellers": 1},
	}
	// sellers ended on its own: its final window was published.
	sellers := collectionSequenceExecution(t, "sellers", 3)
	metrics, ok := service.collectExecutionMetricsWithIdle(sellers, false)
	if !ok {
		t.Fatal("sellers' final window was not collected")
	}
	assignment.metricsCollection = nextMetricsCollection()
	applyExecutionMetricsToAssignment(&assignment, "sellers", metrics)
	published := service.metricsBatchForAssignment("run", assignment, "RUNNING", "")
	if published.GetFleetMetrics().GetCount() != 3 || published.GetTelemetry().GetCollectionSequence() != assignment.metricsCollection {
		t.Fatalf("sellers' final batch = %d results under collection %d, want 3 under %d", published.GetFleetMetrics().GetCount(), published.GetTelemetry().GetCollectionSequence(), assignment.metricsCollection)
	}
	stopping := service.metricsBatchForAssignment("run", assignment, "STOPPING", "")
	if stopping.GetTelemetry().GetCollectionSequence() != published.GetTelemetry().GetCollectionSequence() {
		t.Fatal("a batch that re-sends the window did not repeat its collection")
	}

	// The run is stopped: nothing is left to drain, so the terminal batch
	// re-sends the published window under the same collection.
	unchanged := assignment
	service.captureStoppedExecutionMetrics(&unchanged, []*groupExecution{collectionSequenceExecution(t, "buyers", 0)})
	if unchanged.metricsCollection != assignment.metricsCollection {
		t.Fatal("a stop with nothing to drain started a new collection")
	}

	// buyers is drained at the stop: the terminal batch is that drain alone.
	service.captureStoppedExecutionMetrics(&assignment, []*groupExecution{collectionSequenceExecution(t, "buyers", 2)})
	terminal := service.metricsBatchForAssignment("run", assignment, "ABORTED", "")
	if terminal.GetFleetMetrics().GetCount() != 2 {
		t.Fatalf("terminal batch = %d results, want the 2 drained at the stop", terminal.GetFleetMetrics().GetCount())
	}
	if got := terminal.GetTelemetry().GetCollectionSequence(); got <= published.GetTelemetry().GetCollectionSequence() {
		t.Fatalf("the stop drain's collection = %d, want one after %d", got, published.GetTelemetry().GetCollectionSequence())
	}
	if telemetry := metricsTelemetryFromProto(terminal.GetTelemetry()); telemetry.CollectionSequence != terminal.GetTelemetry().GetCollectionSequence() {
		t.Fatalf("the controller read collection %d, want %d", telemetry.CollectionSequence, terminal.GetTelemetry().GetCollectionSequence())
	}
}

// A group's own publication is a new window too: it does not carry another
// group's earlier window.
func TestGroupPublicationCarriesOnlyItsOwnWindow(t *testing.T) {
	service := newIsolatedService(t, 50)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, vuserCount: 2,
		groups: map[string]int32{"buyers": 1, "sellers": 1}, activeGroups: map[string]int32{"buyers": 1, "sellers": 1},
	}
	service.mu.Unlock()
	transactions := func(group string, count int) []TransactionMetric {
		out := make([]TransactionMetric, count)
		for index := range out {
			out[index] = TransactionMetric{Name: "checkout", Group: group, Success: true, DurationMs: 10}
		}
		return out
	}
	var collections []uint64
	for _, publication := range []struct {
		group string
		count int
	}{{"buyers", 3}, {"sellers", 2}} {
		if err := service.PublishMetrics("run", publication.group, GeneratorStats{ActiveVUsers: 1}, transactions(publication.group, publication.count), MetricsTelemetry{WindowMs: 1000}); err != nil {
			t.Fatalf("publish %s: %v", publication.group, err)
		}
		service.mu.RLock()
		batch := service.metricsBatchLocked("run")
		service.mu.RUnlock()
		if got := len(batch.GetTransactions()); got != publication.count {
			t.Fatalf("%s's publication carries %d results, want its own %d", publication.group, got, publication.count)
		}
		collections = append(collections, batch.GetTelemetry().GetCollectionSequence())
	}
	if collections[1] <= collections[0] {
		t.Fatalf("collections %v do not increase", collections)
	}
}

package generatorrpc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	parquetlocal "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/reader"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCollectionWindowsDrainBoundariesAndConcurrency(t *testing.T) {
	start := time.Now().Add(-time.Second)
	buffer := &transactionBuffer{lastDrainAt: start}
	_, _, elapsed, observed := buffer.drainObservedWindow(false)
	if elapsed != 0 || !observed.Start.IsZero() || !buffer.lastDrainAt.Equal(start) {
		t.Fatal("empty forced drain advanced observation")
	}
	var wg sync.WaitGroup
	windows := make(chan MetricsCollectionWindow, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			_, _, elapsed, observed := buffer.drainObservedWindow(true)
			if elapsed != observed.End.Sub(observed.Start) {
				t.Errorf("drain elapsed %v differs from boundaries %+v", elapsed, observed)
			}
			windows <- observed
		})
	}
	wg.Wait()
	close(windows)
	var spans []MetricsCollectionWindow
	for window := range windows {
		spans = append(spans, window)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	if !spans[0].Start.Equal(generatorPublishedClock.at(start)) || !spans[0].End.Equal(spans[1].Start) {
		t.Fatalf("concurrent drains lost or overlapped their boundary: %+v", spans)
	}
	unknown := &transactionBuffer{}
	_, _, _, window := unknown.drainObservedWindow(true)
	if !window.Start.IsZero() || !window.End.IsZero() {
		t.Fatal("unknown initial boundary was invented")
	}
}

func TestCollectionWindowsRemainIndependentOfRateWindow(t *testing.T) {
	service := &Service{publishInterval: time.Hour}
	execution := collectionSequenceExecution(t, "buyers", 1)
	metrics, ok := service.collectExecutionMetricsWithIdle(execution, false)
	if !ok || metrics.telemetry.WindowMs != time.Hour.Milliseconds() || metrics.collectionWindow.End.Sub(metrics.collectionWindow.Start) >= time.Minute {
		t.Fatalf("clamped denominator changed actual drain interval: %+v", metrics)
	}
	// With no buffer an idle collection cannot inherit the previous bounds.
	execution.results = nil
	idle, ok := service.collectExecutionMetrics(execution)
	if !ok || !idle.collectionWindow.Start.IsZero() || !idle.collectionWindow.End.IsZero() {
		t.Fatal("idle collection invented an interval from earlier evidence")
	}
	assignment := assignedTest{metricsCollection: 17, groupTelemetry: map[string]MetricsTelemetry{}, groupAggregates: map[string]metricsAggregateSet{}}
	start := time.Date(2026, 10, 19, 10, 0, 0, 123456789, time.UTC)
	for group, duration := range map[string]time.Duration{"buyers": time.Second, "sellers": 2 * time.Second} {
		applyExecutionMetricsToAssignment(&assignment, group, executionMetricsSnapshot{
			telemetry:        MetricsTelemetry{WindowMs: duration.Milliseconds()},
			aggregates:       metricsAggregateSet{Fleet: &MetricAggregate{Count: 10}},
			collectionWindow: MetricsCollectionWindow{Start: start, End: start.Add(duration)},
		})
	}
	batch := service.metricsBatchForAssignment("run", assignment, "RUNNING", "")
	windows, complete := collectionWindowsFromProto(batch.CollectionWindows, batch.CollectionWindowsComplete)
	if !complete || len(windows) != 2 || windows[0].End.Sub(windows[0].Start) != time.Second || windows[1].End.Sub(windows[1].Start) != 2*time.Second {
		t.Fatalf("multi-group intervals collapsed into a rate denominator: %+v", windows)
	}
	if batch.WindowMs != 1333 {
		t.Fatalf("fixture did not exercise effective rate weighting: %d", batch.WindowMs)
	}
}

func TestCollectionWindowsLifecycleReplayAndStopCohort(t *testing.T) {
	service := newIsolatedService(t, 50)
	assignment := assignedTest{testID: "run", executionGeneration: 7, committed: true, groups: map[string]int32{"buyers": 1, "sellers": 1}}
	metrics, ok := service.collectExecutionMetricsWithIdle(collectionSequenceExecution(t, "sellers", 3), false)
	if !ok {
		t.Fatal("missing collection")
	}
	assignment.metricsCollection = nextMetricsCollection()
	applyExecutionMetricsToAssignment(&assignment, "sellers", metrics)
	published := service.metricsBatchForAssignment("run", assignment, "RUNNING", "")
	for _, state := range []string{"PAUSED", "STOPPING", "FINALIZING", "COMPLETE"} {
		replay := service.metricsBatchForAssignment("run", assignment, state, "")
		if !reflect.DeepEqual(replay.CollectionWindows, published.CollectionWindows) || replay.Telemetry.CollectionSequence != published.Telemetry.CollectionSequence {
			t.Fatalf("%s retimed a collection", state)
		}
	}
	service.captureStoppedExecutionMetrics(&assignment, []*groupExecution{collectionSequenceExecution(t, "buyers", 2)})
	terminal := service.metricsBatchForAssignment("run", assignment, "ABORTED", "")
	if !terminal.CollectionWindowsComplete || len(terminal.CollectionWindows) != 1 || terminal.CollectionWindows[0].Group != "buyers" || terminal.Telemetry.CollectionSequence == published.Telemetry.CollectionSequence {
		t.Fatalf("stop retained stale sibling windows: %+v", terminal.CollectionWindows)
	}
}

func TestCollectionWindowsWireCompatibilityBoundsAndValidation(t *testing.T) {
	start := time.Date(2026, 10, 19, 10, 0, 0, 123456789, time.UTC)
	good := func(group string) *pb.MetricsCollectionWindow {
		return &pb.MetricsCollectionWindow{Group: group, Start: timestamppb.New(start), End: timestamppb.New(start.Add(time.Nanosecond))}
	}
	for _, kind := range []string{"valid", "legacy", "missing_start", "missing_end", "nil_entry", "oversized_group", "reversed", "invalid_timestamp", "blank_group", "duplicate", "incomplete", "overflow"} {
		t.Run(kind, func(t *testing.T) {
			batch := &pb.MetricsBatch{CollectionWindows: []*pb.MetricsCollectionWindow{good("buyers")}, CollectionWindowsComplete: true}
			switch kind {
			case "legacy":
				batch.CollectionWindows = nil
				batch.CollectionWindowsComplete = false
			case "missing_start":
				batch.CollectionWindows[0].Start = nil
			case "missing_end":
				batch.CollectionWindows[0].End = nil
			case "nil_entry":
				batch.CollectionWindows[0] = nil
			case "oversized_group":
				batch.CollectionWindows[0].Group = strings.Repeat("g", maxGeneratorGroupNameBytes+1)
			case "reversed":
				batch.CollectionWindows[0].End = timestamppb.New(start.Add(-time.Second))
			case "invalid_timestamp":
				batch.CollectionWindows[0].End = &timestamppb.Timestamp{Seconds: 253402300800}
			case "blank_group":
				batch.CollectionWindows[0].Group = " "
			case "duplicate":
				batch.CollectionWindows = append(batch.CollectionWindows, good("buyers"))
			case "incomplete":
				batch.CollectionWindowsComplete = false
			case "overflow":
				for i := 1; i <= MaxMetricsCollectionWindows; i++ {
					batch.CollectionWindows = append(batch.CollectionWindows, good(fmt.Sprint(i)))
				}
			}
			data, err := proto.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			var restored pb.MetricsBatch
			if err = proto.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			decoded, err := (&MetricsStream{stream: oneTimingBatchStream{batch: &restored}}).Receive()
			if err != nil {
				t.Fatal(err)
			}
			if decoded.CollectionWindowsComplete != (kind == "valid") || len(decoded.CollectionWindows) > MaxMetricsCollectionWindows {
				t.Fatalf("availability/bound: %+v", decoded.CollectionWindows)
			}
			if kind == "valid" && (!decoded.CollectionWindows[0].Start.Equal(start) || decoded.CollectionWindows[0].End.Sub(start) != time.Nanosecond) {
				t.Fatal("wire lost nanosecond boundaries")
			}
		})
	}
	values := map[string]MetricsCollectionWindow{}
	for i := 0; i <= MaxMetricsCollectionWindows; i++ {
		values[fmt.Sprintf("g-%04d", i)] = MetricsCollectionWindow{Start: start, End: start.Add(time.Second)}
	}
	windows, complete := collectionWindowsToProto(values)
	if complete || len(windows) != MaxMetricsCollectionWindows || windows[0].Group != "g-0000" || windows[len(windows)-1].Group != "g-0255" {
		t.Fatal("sender bound is not deterministic/incomplete")
	}
}

func TestCollectionWindowsArchiveRetainsIdentityAndPrecision(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	writer, err := newArchiveRecorder("window-generator", "window-run", "windows")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 19, 10, 0, 0, 123456789, time.UTC)
	windows, complete := collectionWindowsToProto(map[string]MetricsCollectionWindow{"buyers": {Start: start, End: start.Add(123 * time.Nanosecond)}})
	batch := &pb.MetricsBatch{TestId: "window-run", ExecutionGeneration: 7, TimestampUnixMs: start.UnixMilli(), WindowMs: 1000, Stats: &pb.GeneratorStats{}, Telemetry: &pb.MetricsBatchTelemetry{CollectionSequence: 29}, CollectionWindows: windows, CollectionWindowsComplete: complete}
	(&Service{}).recordMetricsBatch("window-run", writer, batch)
	record, err := writer.Finalize("COMPLETE", start.Add(time.Second), "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var summary archiveIntervalSummary
	if err = json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	file, err := parquetlocal.NewLocalFileReader(filepath.Join(record.ArchivePath, "interval_summaries.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	parquetReader, err := reader.NewParquetReader(file, new(parquetIntervalSummary), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer parquetReader.ReadStop()
	if parquetReader.GetNumRows() != 1 {
		t.Fatal("physical Parquet collection row count incorrect")
	}
	rows := make([]parquetIntervalSummary, 1)
	if err = parquetReader.Read(&rows); err != nil {
		t.Fatal(err)
	}
	var telemetry archiveTelemetryHealth
	if err = json.Unmarshal([]byte(rows[0].TelemetryJSON), &telemetry); err != nil {
		t.Fatal(err)
	}
	for _, got := range []*archiveCollectionWindows{summary.Telemetry.CollectionWindows, telemetry.CollectionWindows} {
		if got == nil || !got.Complete || got.SourceEpoch != processSourceEpoch.id || got.ExecutionGeneration != 7 || got.CollectionSequence != 29 || len(got.Windows) != 1 || !got.Windows[0].Start.Equal(start) || got.Windows[0].End.Sub(start) != 123*time.Nanosecond {
			t.Fatalf("archive lost source evidence: %+v", got)
		}
	}
	if archiveCollectionWindowsFromBatch(&pb.MetricsBatch{}) != nil {
		t.Fatal("legacy absence became a source collection")
	}
}

func TestCollectionWindowsCoordinatedCohortAndUnmeasuredPublication(t *testing.T) {
	service := newIsolatedService(t, 50)
	buyers, sellers := collectionSequenceExecution(t, "buyers", 1), collectionSequenceExecution(t, "sellers", 1)
	buyers.executionGeneration, sellers.executionGeneration = 7, 7
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", executionGeneration: 7, committed: true,
		groups: map[string]int32{"buyers": 1, "sellers": 1}, groupCollectionWindows: map[string]MetricsCollectionWindow{"finished": {Start: time.Now().Add(-time.Hour), End: time.Now()}}}
	service.executions["run"] = map[string]*groupExecution{"buyers": buyers, "sellers": sellers}
	service.mu.Unlock()
	service.publishTestExecutionSnapshot("run", "buyers", buyers, true)
	service.mu.RLock()
	batch := service.metricsBatchLocked("run")
	service.mu.RUnlock()
	if !batch.CollectionWindowsComplete || len(batch.CollectionWindows) != 2 || batch.CollectionWindows[0].Group != "buyers" || batch.CollectionWindows[1].Group != "sellers" {
		t.Fatalf("coordinated collection retained stale/missing windows: %+v", batch.CollectionWindows)
	}
	if err := service.PublishMetrics("run", "buyers", GeneratorStats{}, []TransactionMetric{{Name: "external", Success: true}}, MetricsTelemetry{WindowMs: 1000}); err != nil {
		t.Fatal(err)
	}
	service.mu.RLock()
	batch = service.metricsBatchLocked("run")
	service.mu.RUnlock()
	if batch.CollectionWindowsComplete || len(batch.CollectionWindows) != 0 {
		t.Fatal("unmeasured publication inherited an earlier collection's boundaries")
	}
}

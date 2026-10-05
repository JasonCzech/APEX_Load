package api

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type blockedDynatraceTraceStore struct {
	*storage.SQLiteStore
	writeStarted chan bool
	runner       *TestRunner
}

func (s blockedDynatraceTraceStore) RecordDynatraceRunTraceIDsFencedContext(ctx context.Context, _, _ string, _ time.Time, _ []string, _, _ string, _ uint64) (bool, error) {
	if s.writeStarted != nil {
		s.writeStarted <- s.runner.durableWritesInFlight.Load() == 1
	}
	<-ctx.Done()
	return false, ctx.Err()
}

func TestDynatraceTraceWorkerShutdownBoundsFinalStorageFlush(t *testing.T) {
	_, monitors, job, _ := apiDynatraceNativeFixture(t)
	store := monitors.(*storage.SQLiteStore)
	writeStarted := make(chan bool, 1)
	blockedStore := &blockedDynatraceTraceStore{SQLiteStore: store, writeStarted: writeStarted}
	runner := NewTestRunner(1, NewHub(), blockedStore)
	blockedStore.runner = runner
	artifacts := &runArtifacts{runner: runner, dynatraceTraceIDs: map[string]struct{}{fmt.Sprintf("%032x", 1): {}}}
	artifacts.nativeDynatrace.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	artifacts.startNativeDynatraceTraceWorker(ctx, job.ProjectID, job.RunID, job.StartedAt, executionLeaseTerm{})
	cancel()
	select {
	case counted := <-writeStarted:
		if !counted {
			t.Fatal("blocked trace write was invisible to the durable-write gauge")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("final trace write did not start")
	}
	done := make(chan struct{})
	go func() { artifacts.monitorWorkers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled worker remained blocked in its final storage flush")
	}
	if runner.durableWritesInFlight.Load() != 0 {
		t.Fatal("completed trace write remained counted as in flight")
	}
}

func TestDynatraceTraceWorkerCannotAdoptSuccessorExecutionTerm(t *testing.T) {
	_, monitors, job, _ := apiDynatraceNativeFixture(t)
	store := monitors.(*storage.SQLiteStore)
	leadership := newExecutionLeadership(store, "dynatrace-trace-owner")
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("initial execution ownership was not admitted")
	}
	t.Cleanup(leadership.relinquish)
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(leadership))
	oldTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	artifacts := &runArtifacts{runner: runner}
	artifacts.nativeDynatrace.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	artifacts.startNativeDynatraceTraceWorker(ctx, job.ProjectID, job.RunID, job.StartedAt, oldTerm)
	leadership.relinquish()
	leadership.tick()
	if !leadership.IsAdmitted() {
		cancel()
		t.Fatal("successor execution ownership was not admitted")
	}
	artifacts.dynatraceMu.Lock()
	artifacts.dynatraceTraceIDs = map[string]struct{}{fmt.Sprintf("%032x", 1): {}}
	artifacts.dynatraceMu.Unlock()
	cancel()
	artifacts.monitorWorkers.Wait()
	jobs, err := store.ListRunMonitorJobs(job.ProjectID, job.RunID)
	if err != nil || len(jobs) != 1 || len(jobs[0].RunTraceIDs) != 0 {
		t.Fatalf("old worker adopted successor ownership: %+v %v", jobs, err)
	}
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	artifacts.persistNativeDynatraceTraceIDs(t.Context(), job.ProjectID, job.RunID, job.StartedAt, term)
	jobs, err = store.ListRunMonitorJobs(job.ProjectID, job.RunID)
	if err != nil || len(jobs) != 1 || len(jobs[0].RunTraceIDs) != 1 {
		t.Fatalf("current ownership failed to retain proof: %+v %v", jobs, err)
	}
}

func TestDistributedDynatraceNativeMonitoringPersistsObservedTraces(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		name := "explicit source"
		if inherited {
			name = "inherited scenario"
		}
		t.Run(name, func(t *testing.T) {
			var configure []func(storage.Store, *storage.TestRecord, storage.MonitorSource)
			if inherited {
				configure = append(configure, func(db storage.Store, record *storage.TestRecord, source storage.MonitorSource) {
					now := time.Now().UTC()
					if err := db.SaveScript(&dsl.Script{ID: "native-script", Name: "Native trace fixture", ProjectID: record.ProjectID, Protocol: "http", CreatedAt: now}); err != nil {
						t.Fatal(err)
					}
					if err := db.SaveScenario(&storage.Scenario{ID: "native-scenario", Name: "Native inherited monitoring", Duration: "1m", CreatedAt: now}, []storage.ScenarioScript{{ScriptID: "native-script"}}); err != nil {
						t.Fatal(err)
					}
					if _, err := db.(storage.RunMonitorStore).SaveMonitorBindings(storage.MonitorBindings{ProjectID: record.ProjectID, ScenarioID: "native-scenario", Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}, 0); err != nil {
						t.Fatal(err)
					}
					record.ScenarioID, record.MonitorBindings = "native-scenario", nil
				})
			}
			server, store, job, _ := apiDynatraceNativeFixture(t, configure...)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			test := &ManagedTest{ID: job.RunID, ProjectID: job.ProjectID, ScenarioID: job.ScenarioID, Status: "RUNNING", StartedAt: &job.StartedAt, executionCtx: ctx,
				MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: job.Source.ID, Required: true}}}}
			if inherited {
				// The real scenario binding selected the durable job at run
				// start; inheritance leaves the in-memory override omitted.
				test.MonitorBindings = nil
				test.ScenarioID = "native-scenario"
			}
			server.runner.mu.Lock()
			server.runner.tests[test.ID] = test
			server.runner.mu.Unlock()
			artifacts := distributedMonitoringArtifacts(server.runner, test)
			if artifacts == nil {
				t.Fatal("native distributed selection did not admit trace artifacts")
			}
			t.Cleanup(artifacts.stopMonitoring)
			if distributedMonitoringArtifacts(server.runner, test) != artifacts || test.elkArtifacts != artifacts || test.dynatraceArtifacts != artifacts {
				t.Fatal("distributed publications did not reuse the run-owned trace buffer")
			}
			first, second := fmt.Sprintf("%032x", 1), fmt.Sprintf("%032x", 2)
			live := buildLiveMetricsFromDistributedBatch(test, generatorrpc.MetricsBatch{
				TestID: test.ID, TimestampUnixMs: time.Now().UnixMilli(), WindowMs: 1000,
				Transactions: []generatorrpc.TransactionMetric{
					{Name: "first", Success: true, CorrelationID: "00-" + first + "-00f067aa0ba902b7-01"},
					{Name: "second", Success: true, CorrelationID: "00-" + second + "-00f067aa0ba902b7-01"},
				},
			}, 2, nil)
			artifacts.enrichLiveMetrics(test, live)
			if !artifacts.nativeDynatrace.Load() || test.ELKCapture != nil || test.DynatraceCapture != nil || live.ELK != nil || live.Dynatrace != nil {
				t.Fatal("native distributed traces activated a legacy scalar capture")
			}
			artifacts.stopMonitoring() // The terminal path waits for this final durable flush.
			jobs, err := store.ListRunMonitorJobs(job.ProjectID, job.RunID)
			if err != nil || len(jobs) != 1 || !slices.Equal(jobs[0].RunTraceIDs, []string{first, second}) {
				t.Fatalf("distributed trace proof was not durably flushed: %+v %v", jobs, err)
			}
			if jobs[0].NextSequence != 0 || len(jobs[0].Coverage) != 0 || artifacts.elkStatus != nil || artifacts.dynatraceStatus != nil {
				t.Fatal("trace capture performed legacy scalar collection or changed query progress")
			}
		})
	}
}

func TestDistributedNativeMonitoringArtifactAdmissionPreservesBindingPrecedence(t *testing.T) {
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "native-project")
	for _, tc := range []struct {
		name       string
		project    string
		scenario   string
		bindings   *storage.MonitorBindings
		wantBuffer bool
	}{
		{name: "unselected ad hoc", project: "native-project"},
		{name: "explicit empty overrides scenario", project: "native-project", scenario: "scenario", bindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{}}},
		{name: "disabled project", project: "other-project", bindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: "source"}}}},
		{name: "explicit selection", project: "native-project", bindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: "source"}}}, wantBuffer: true},
		{name: "inherited selection candidate", project: "native-project", scenario: "scenario", wantBuffer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			test := &ManagedTest{ID: "run", ProjectID: tc.project, ScenarioID: tc.scenario, MonitorBindings: tc.bindings, Status: "RUNNING", executionCtx: t.Context()}
			runner.tests[test.ID] = test
			artifacts := distributedMonitoringArtifacts(runner, test)
			if (artifacts != nil) != tc.wantBuffer {
				t.Fatalf("trace buffer admitted = %v, want %v", artifacts != nil, tc.wantBuffer)
			}
			if artifacts != nil {
				artifacts.enrichLiveMetrics(test, &LiveMetrics{Timestamp: time.Now()})
				artifacts.stopMonitoring()
				if artifacts.nativeDynatrace.Load() || artifacts.monitorStarted {
					t.Fatal("binding candidate activated a worker without a durable Dynatrace job")
				}
			}
		})
	}
}

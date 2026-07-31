package api

import (
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCloneManagedTestForReadLockedDeeplyDetachesMutableFields(t *testing.T) {
	targetTPS := 12.5
	iterations := 3
	goalTPS := 100.0
	startedAt := time.Now().UTC()
	original := &ManagedTest{
		ID:       "snapshot-test",
		FolderID: ptrString("folder-a"),
		Groups: []TestGroup{{
			Name:       "buyers",
			Stages:     []storage.LoadStage{{Type: "hold", TargetTPS: &targetTPS}},
			Iterations: &iterations,
			RuntimeSettings: &RuntimeSettingsOverride{
				Env: map[string]string{"BASE_URL": "https://before.example"},
			},
			Assignment:    &GeneratorAssignmentSettings{SelectedTargets: []string{"generator-a"}, RegionWeights: map[string]int{"east": 1}},
			RunLogicGoals: &RunLogicGoalSnapshot{MaxTransactions: 10},
		}},
		GoalConfig:  &GoalConfig{TargetAverageTPS: &goalTPS},
		StartedAt:   &startedAt,
		LiveMetrics: populatedLiveMetricsForCloneTest(),
		History:     []*LiveMetrics{populatedLiveMetricsForCloneTest()},
		ComparisonBaseline: &RunComparisonBaseline{
			Transactions: map[string]*TxMetrics{"checkout": {Name: "checkout", CorrelationIDs: []string{"baseline"}}},
		},
		TelemetryHealth:          &TelemetryRunHealth{Conditions: []string{"healthy"}, BreachStartedAt: &startedAt},
		thresholdAlertActive:     map[string]bool{"error_pct": true},
		thresholdAlertNotifiedAt: map[string]time.Time{"error_pct": startedAt},
	}

	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	cloned := cloneManagedTestForReadLocked(original)
	if cloned.LiveMetrics == original.LiveMetrics || cloned.History[0] == original.History[0] {
		t.Fatal("live metrics or history element still aliases controller state")
	}

	cloned.FolderID = ptrString("folder-b")
	cloned.Groups[0].Stages[0].TargetTPS = ptrFloat64(99)
	*cloned.Groups[0].Iterations = 99
	cloned.Groups[0].RuntimeSettings.Env["BASE_URL"] = "https://after.example"
	cloned.Groups[0].Assignment.SelectedTargets[0] = "generator-b"
	cloned.Groups[0].Assignment.RegionWeights["east"] = 9
	cloned.Groups[0].RunLogicGoals.MaxTransactions = 99
	*cloned.GoalConfig.TargetAverageTPS = 999
	cloned.TelemetryHealth.Conditions[0] = "degraded"
	mutatePopulatedLiveMetrics(cloned.LiveMetrics)
	mutatePopulatedLiveMetrics(cloned.History[0])
	cloned.ComparisonBaseline.Transactions["checkout"].CorrelationIDs[0] = "changed"
	cloned.thresholdAlertActive["error_pct"] = false
	cloned.thresholdAlertNotifiedAt["error_pct"] = startedAt.Add(time.Hour)

	after, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("mutating read snapshot changed original JSON\nbefore: %s\nafter:  %s", before, after)
	}
	if got := original.LiveMetrics.GroupMetrics["buyers"].DurationDigest.Centroids[0].Mean; got != 1 {
		t.Fatalf("duration digest centroid changed through alias: %v", got)
	}
	if got := original.LiveMetrics.ErrorEvidence[0].Snapshot.NetworkEntries[0].RequestHeaders["x-request"]; got != "before" {
		t.Fatalf("error evidence snapshot changed through alias: %q", got)
	}
	if !original.thresholdAlertActive["error_pct"] || !original.thresholdAlertNotifiedAt["error_pct"].Equal(startedAt) {
		t.Fatal("threshold alert state changed through read-snapshot alias")
	}

	summary := cloneManagedTestSummaryForReadLocked(original)
	if summary.History != nil || summary.LiveMetrics == original.LiveMetrics {
		t.Fatalf("summary snapshot retained history or aliased live metrics: %#v", summary)
	}
	mutatePopulatedLiveMetrics(summary.LiveMetrics)
	if original.LiveMetrics.ErrorBreakdown["http"] != 1 {
		t.Fatal("summary live metrics still alias controller state")
	}
}

func populatedLiveMetricsForCloneTest() *LiveMetrics {
	goal := 100.0
	maxErrors := 5
	return &LiveMetrics{
		Timestamp:      time.Now().UTC(),
		ActiveVUsers:   5,
		ErrorBreakdown: map[string]int64{"http": 1},
		Transactions: map[string]*TxMetrics{"checkout": {
			Name:           "checkout",
			CorrelationIDs: []string{"correlation"},
			ServerTelemetry: &ServerTelemetrySummary{
				RequestPayloadHistogram: map[string]int64{"small": 1},
				PartitionEvents:         map[string]int64{"0": 1},
			},
		}},
		DependencyMap: &LiveDependencyMap{Nodes: []LiveDependencyNode{{ID: "api"}}, Edges: []LiveDependencyEdge{{Source: "client", Target: "api"}}},
		ErrorPatterns: []LiveErrorPattern{{Signature: "boom", Actions: []string{"checkout"}, AffectedGroups: []string{"buyers"}}},
		GroupMetrics: map[string]*GroupLiveMetrics{"buyers": {
			Name: "buyers",
			DurationDigest: &engine.TDigestSnapshot{
				Centroids: []engine.TDigestCentroidSnapshot{{Mean: 1, Weight: 1}},
			},
		}},
		BrowserByDevice: map[string]*BrowserDeviceLiveMetrics{"mobile": {DeviceProfile: "mobile"}},
		GoalStatus:      &GoalStatus{TargetAverageTPS: &goal, MaxErrorCount: &maxErrors},
		CapacityStatus: &LiveCapacityStatus{
			ByProtocol: map[string]*LiveCapacityBreakdown{"http": {AllocatedVUsers: 5}},
		},
		TelemetryStatus: &TelemetryRunHealth{Conditions: []string{"healthy"}},
		Resources:       &engine.SystemResources{CPUProcessPct: 10},
		GeneratorResources: map[string]*GeneratorResourceSnapshot{"generator-a": {
			ImpactReasons: []string{"none"},
			Resources:     &engine.SystemResources{CPUProcessPct: 20},
		}},
		Latency: &NetworkLatency{Targets: []LatencyTarget{{Name: "api", LatencyMs: 2}}},
		ErrorEvidence: []LiveErrorEvidence{{Snapshot: &protocol.ErrorSnapshot{
			RequestHeaders: map[string]string{"authorization": "redacted"},
			NetworkEntries: []protocol.NetworkEntry{{RequestHeaders: map[string]string{"x-request": "before"}}},
			DOMSummary:     &protocol.DOMSummary{VisibleText: []string{"before"}},
		}}},
	}
}

func mutatePopulatedLiveMetrics(metrics *LiveMetrics) {
	metrics.ErrorBreakdown["http"] = 99
	metrics.Transactions["checkout"].CorrelationIDs[0] = "changed"
	metrics.Transactions["checkout"].ServerTelemetry.RequestPayloadHistogram["small"] = 99
	metrics.Transactions["checkout"].ServerTelemetry.PartitionEvents["0"] = 99
	metrics.DependencyMap.Nodes[0].ID = "changed"
	metrics.ErrorPatterns[0].Actions[0] = "changed"
	metrics.GroupMetrics["buyers"].DurationDigest.Centroids[0].Mean = 99
	metrics.BrowserByDevice["mobile"].DeviceProfile = "changed"
	*metrics.GoalStatus.TargetAverageTPS = 999
	metrics.CapacityStatus.ByProtocol["http"].AllocatedVUsers = 99
	metrics.TelemetryStatus.Conditions[0] = "changed"
	metrics.Resources.CPUProcessPct = 99
	metrics.GeneratorResources["generator-a"].ImpactReasons[0] = "changed"
	metrics.GeneratorResources["generator-a"].Resources.CPUProcessPct = 99
	metrics.Latency.Targets[0].LatencyMs = 99
	metrics.ErrorEvidence[0].Snapshot.NetworkEntries[0].RequestHeaders["x-request"] = "changed"
}

type serializingBlockingHistoryStore struct {
	started chan *LiveMetrics
	release chan struct{}
	once    sync.Once
}

func (s *serializingBlockingHistoryStore) SaveHistory(_ string, _ time.Time, metrics any) error {
	live := metrics.(*LiveMetrics)
	s.once.Do(func() { s.started <- live })
	for {
		select {
		case <-s.release:
			return nil
		default:
			_, _ = json.Marshal(live)
			time.Sleep(time.Millisecond)
		}
	}
}

func (*serializingBlockingHistoryStore) GetHistory(string) ([]any, error) { return nil, nil }

type snapshotPausableBackend struct{}

func (*snapshotPausableBackend) Run(*TestRunner, *ManagedTest)             {}
func (*snapshotPausableBackend) PauseTest(*TestRunner, *ManagedTest) error { return nil }
func (*snapshotPausableBackend) ResumeTest(*TestRunner, *ManagedTest) error {
	return nil
}

func TestPublishLiveMetricsKeepsPersistedSnapshotImmutableDuringPause(t *testing.T) {
	history := &serializingBlockingHistoryStore{started: make(chan *LiveMetrics, 1), release: make(chan struct{})}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.historyStore = history
	test := &ManagedTest{
		ID:               "immutable-publish",
		Name:             "immutable publish",
		Status:           "RUNNING",
		ProjectID:        "project-a",
		Groups:           []TestGroup{{Name: "buyers", VUsers: 5}},
		executionBackend: &snapshotPausableBackend{},
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	input := populatedLiveMetricsForCloneTest()
	published := make(chan struct{})
	go func() {
		runner.publishLiveMetrics(test, input, nil)
		close(published)
	}()

	var persisted *LiveMetrics
	select {
	case persisted = <-history.started:
	case <-time.After(2 * time.Second):
		t.Fatal("live metrics persistence did not start")
	}
	if err := runner.PauseTest(test.ID); err != nil {
		t.Fatalf("PauseTest: %v", err)
	}
	input.ErrorBreakdown["http"] = 777

	runner.mu.RLock()
	current := runner.tests[test.ID].LiveMetrics
	latestHistory := runner.tests[test.ID].History[len(runner.tests[test.ID].History)-1]
	runner.mu.RUnlock()
	if current == persisted || latestHistory == persisted {
		t.Fatal("pause mutated the snapshot already handed to a persistence encoder")
	}
	if persisted.ActiveVUsers != 5 || persisted.ErrorBreakdown["http"] != 1 {
		t.Fatalf("persisted snapshot changed during pause/input mutation: %#v", persisted)
	}
	if current.ActiveVUsers != 0 || latestHistory.ActiveVUsers != 0 {
		t.Fatalf("copy-on-write pause was not installed: current=%d history=%d", current.ActiveVUsers, latestHistory.ActiveVUsers)
	}

	close(history.release)
	select {
	case <-published:
	case <-time.After(2 * time.Second):
		t.Fatal("metrics publication did not finish")
	}
}

type blockingArchiveCommitStore struct {
	*metricsStoreSpy
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	record  *storage.RunArchiveRecord
	test    *storage.TestRecord
}

func (s *blockingArchiveCommitStore) SaveRunArchive(record *storage.RunArchiveRecord) error {
	s.once.Do(func() { close(s.started) })
	<-s.release
	s.mu.Lock()
	s.record = cloneRunArchiveRecord(record)
	s.mu.Unlock()
	return nil
}

func (s *blockingArchiveCommitStore) GetRunArchive(string) (*storage.RunArchiveRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil {
		return nil, sql.ErrNoRows
	}
	return cloneRunArchiveRecord(s.record), nil
}

func (s *blockingArchiveCommitStore) GetTest(string) (*storage.TestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.test == nil {
		return nil, sql.ErrNoRows
	}
	cloned := *s.test
	return &cloned, nil
}

func (s *blockingArchiveCommitStore) DeleteTest(string) error {
	s.mu.Lock()
	s.test = nil
	s.mu.Unlock()
	return nil
}

func TestArchiveRetryAndDeleteAreSerializedWithoutResurrection(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		enqueue func(*TestRunner, *ManagedTest, *storage.RunArchiveRecord)
	}{
		{
			name: "controller retry",
			enqueue: func(runner *TestRunner, test *ManagedTest, record *storage.RunArchiveRecord) {
				runner.enqueueRunArchiveFinalizationRetry(test, record, false)
			},
		},
		{
			name: "local recorder retry",
			enqueue: func(runner *TestRunner, test *ManagedTest, record *storage.RunArchiveRecord) {
				(&runArtifacts{runner: runner}).enqueueArchivePersistenceRetry(test, record, false)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			archiveRoot := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
			store := &blockingArchiveCommitStore{
				metricsStoreSpy: &metricsStoreSpy{},
				started:         make(chan struct{}),
				release:         make(chan struct{}),
				test:            &storage.TestRecord{ID: "archive-delete-fence", Status: "COMPLETE"},
			}
			runner := NewTestRunner(1, NewHub(), store)
			test := &ManagedTest{ID: "archive-delete-fence", Name: "archive delete fence", Status: "COMPLETE"}
			runner.mu.Lock()
			runner.tests[test.ID] = test
			runner.mu.Unlock()
			archivePath := archiveRoot + "/" + test.ID
			if err := os.MkdirAll(archivePath, 0o755); err != nil {
				t.Fatal(err)
			}
			manifestPath := archivePath + "/" + runArchiveManifestFileName
			if err := os.WriteFile(manifestPath, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			record := &storage.RunArchiveRecord{TestID: test.ID, ArchiveBackend: archiveBackendFilesystem, ArchivePath: archivePath, ManifestPath: manifestPath}

			testCase.enqueue(runner, test, record)
			select {
			case <-store.started:
			case <-time.After(2 * time.Second):
				t.Fatal("archive retry did not reach catalog persistence")
			}

			deleteResult := make(chan struct {
				deleted bool
				err     error
			}, 1)
			go func() {
				deleted, err := runner.DeleteTestWithError(test.ID)
				deleteResult <- struct {
					deleted bool
					err     error
				}{deleted: deleted, err: err}
			}()
			select {
			case result := <-deleteResult:
				t.Fatalf("delete bypassed in-flight archive commit: %#v", result)
			case <-time.After(50 * time.Millisecond):
			}

			close(store.release)
			select {
			case result := <-deleteResult:
				if result.err != nil || !result.deleted {
					t.Fatalf("DeleteTestWithError = %v, %v", result.deleted, result.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("delete did not resume after archive commit")
			}

			runner.mu.RLock()
			_, testExists := runner.tests[test.ID]
			_, archiveExists := runner.archives[test.ID]
			runner.mu.RUnlock()
			if testExists || archiveExists {
				t.Fatalf("archive retry resurrected deleted test: test=%v archive=%v", testExists, archiveExists)
			}
		})
	}
}

func ptrFloat64(value float64) *float64 { return &value }

package api

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The runner's maps and per-run live metrics are shared by every API request
// goroutine, the metrics publishers, and the WebSocket hub. Most of the suite
// drives them from a single goroutine, so `-race` has nothing to observe. These
// tests deliberately run the real shared state from many goroutines at once so
// the race detector has something to detect.

func newConcurrencyTestRunner(t *testing.T, maxTests int) *TestRunner {
	t.Helper()
	t.Setenv("APEXLOAD_ENVIRONMENT", "local")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewTestRunner(maxTests, NewHub(), store)
}

func concurrencyTestScript(id string) *dsl.Script {
	return &dsl.Script{
		ID:       id,
		Name:     id,
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL:  "https://example.com",
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Actions: []dsl.Action{{Name: "home", Type: "request", Method: "GET", Target: "/"}},
	}
}

// TestRunnerConcurrentCatalogMutationsAndReads hammers the runner's script and
// test maps from many goroutines: writers create and delete, readers list and
// look up. It is a real multi-goroutine exercise of tr.mu-protected state.
func TestRunnerConcurrentCatalogMutationsAndReads(t *testing.T) {
	runner := newConcurrencyTestRunner(t, 32)

	const writers = 6
	const iterations = 12
	stop := make(chan struct{})
	var readerGroup, writerGroup sync.WaitGroup
	var reads atomic.Int64

	for reader := range 6 {
		readerGroup.Add(1)
		go func(reader int) {
			defer readerGroup.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = runner.ListScripts()
				_ = runner.ListTests()
				_ = runner.ListTestSummaries()
				_ = runner.TestCount()
				_, _ = runner.GetScript(fmt.Sprintf("script-%d-%d", reader%writers, reader))
				_, _ = runner.GetTest(fmt.Sprintf("missing-%d", reader))
				reads.Add(1)
			}
		}(reader)
	}

	created := make([][]string, writers)
	var contended atomic.Int64
	for writer := range writers {
		writerGroup.Add(1)
		go func(writer int) {
			defer writerGroup.Done()
			ids := make([]string, 0, iterations)
			defer func() { created[writer] = ids }()
			for iteration := range iterations {
				scriptID := fmt.Sprintf("script-%d-%d", writer, iteration)
				stored := runner.StoreScript(concurrencyTestScript(scriptID))
				if stored == nil {
					t.Errorf("StoreScript(%s) returned nil", scriptID)
					return
				}
				// Test creation is fenced against concurrent controller-content
				// changes and asks its caller to retry, which is exactly what a
				// contended fleet of API requests hits. Retry a bounded number of
				// times and treat continued contention as a tolerated outcome
				// rather than a failure — the invariant under test is that the
				// runner's maps stay consistent, not that every create wins.
				var managed *ManagedTest
				var err error
				for attempt := range 10 {
					managed, err = runner.CreateTest(CreateTestRequest{
						Name:     fmt.Sprintf("run-%d-%d-%d", writer, iteration, attempt),
						Duration: "30s",
						Groups:   []CreateGroupEntry{{Name: "g", ScriptID: scriptID, VUsers: 1}},
					})
					if err == nil {
						break
					}
					if !strings.Contains(err.Error(), "build fence is stale") {
						break
					}
				}
				if err != nil {
					if strings.Contains(err.Error(), "build fence is stale") {
						contended.Add(1)
						continue
					}
					t.Errorf("CreateTest(%s): %v", scriptID, err)
					return
				}
				ids = append(ids, managed.ID)
				if iteration%3 == 0 {
					if !runner.DeleteTest(managed.ID) {
						t.Errorf("DeleteTest(%s) reported missing", managed.ID)
						return
					}
					ids = ids[:len(ids)-1]
					if !runner.DeleteScript(scriptID) {
						t.Errorf("DeleteScript(%s) reported missing", scriptID)
						return
					}
				}
			}
		}(writer)
	}

	writerGroup.Wait()
	close(stop)
	readerGroup.Wait()

	if reads.Load() == 0 {
		t.Fatal("no concurrent reads were observed")
	}
	surviving := 0
	for _, ids := range created {
		surviving += len(ids)
		for _, id := range ids {
			if _, ok := runner.GetTest(id); !ok {
				t.Fatalf("test %q was lost by concurrent catalog mutation", id)
			}
		}
	}
	// Tolerating admission contention above must not turn the count check below
	// into `0 == 0`: an admission regression that rejects every create would
	// otherwise read as green. Require that the writers actually committed, and
	// that contention stayed the exception rather than the rule.
	const attempted = writers * iterations
	if surviving == 0 {
		t.Fatalf("no test survived the concurrent writers (%d of %d creates lost to admission contention); the catalog assertions are vacuous", contended.Load(), attempted)
	}
	if got := contended.Load(); got > attempted/2 {
		t.Fatalf("admission contention rejected %d of %d creates, want contention to stay the exception", got, attempted)
	}
	if got := runner.TestCount(); got != surviving {
		t.Fatalf("runner holds %d tests (%d creates lost to admission contention), want the %d survivors of the concurrent writers", got, contended.Load(), surviving)
	}
}

// TestRunnerConcurrentLiveMetricsPublishAndReads publishes live metrics for
// several runs at once while other goroutines read the same runs through the
// public snapshot APIs and the hub. Publishing mutates LiveMetrics, History,
// error patterns, and the run-snapshot cache under tr.mu, which is exactly the
// shared state a single-goroutine test can never exercise.
func TestRunnerConcurrentLiveMetricsPublishAndReads(t *testing.T) {
	runner := newConcurrencyTestRunner(t, 16)

	const runs = 4
	const samples = 60
	ids := make([]string, 0, runs)
	managedTests := make([]*ManagedTest, 0, runs)
	for index := range runs {
		scriptID := fmt.Sprintf("metrics-script-%d", index)
		runner.StoreScript(concurrencyTestScript(scriptID))
		managed, err := runner.CreateTest(CreateTestRequest{
			Name:     fmt.Sprintf("metrics-run-%d", index),
			Duration: "30s",
			Groups:   []CreateGroupEntry{{Name: "g", ScriptID: scriptID, VUsers: 2}},
		})
		if err != nil {
			t.Fatalf("CreateTest: %v", err)
		}
		ids = append(ids, managed.ID)
		runner.mu.RLock()
		live := runner.tests[managed.ID]
		runner.mu.RUnlock()
		if live == nil {
			t.Fatalf("test %q is not registered with the runner", managed.ID)
		}
		managedTests = append(managedTests, live)
	}

	stop := make(chan struct{})
	var readerGroup, publisherGroup sync.WaitGroup
	var reads atomic.Int64
	var published atomic.Int64

	for reader := range 6 {
		readerGroup.Add(1)
		go func(reader int) {
			defer readerGroup.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				id := ids[reader%len(ids)]
				if snapshot, ok := runner.GetTest(id); ok {
					// Touch the snapshot's metrics the way an API response does.
					if snapshot.LiveMetrics != nil {
						_ = snapshot.LiveMetrics.TotalTx
						_ = len(snapshot.History)
					}
				}
				_ = runner.ListTestSummaries()
				_, _ = runner.ListLiveErrorEvidence(id, "", "", 5)
				if runner.hub != nil {
					_ = runner.hub.Stats()
				}
				reads.Add(1)
			}
		}(reader)
	}

	for index, test := range managedTests {
		publisherGroup.Add(1)
		go func(index int, test *ManagedTest) {
			defer publisherGroup.Done()
			artifacts := &runArtifacts{runner: runner, test: test}
			base := time.Now()
			for sample := range samples {
				live := &LiveMetrics{
					Timestamp:     base.Add(time.Duration(sample) * time.Second),
					ActiveVUsers:  int64(index + 1),
					TPS:           float64(sample),
					TotalTx:       int64(sample),
					TotalErrors:   int64(sample / 10),
					AvgResponseMs: float64(50 + sample),
					GroupMetrics: map[string]*GroupLiveMetrics{
						"g": {Name: "g", VUsers: index + 1, Transactions: int64(sample), TPS: float64(sample)},
					},
				}
				if !runner.publishLiveMetrics(test, live, artifacts) {
					t.Errorf("publishLiveMetrics was refused for %s", test.ID)
					return
				}
				published.Add(1)
			}
		}(index, test)
	}

	publisherGroup.Wait()
	close(stop)
	readerGroup.Wait()

	if got := published.Load(); got != runs*samples {
		t.Fatalf("published samples = %d, want %d", got, runs*samples)
	}
	if reads.Load() == 0 {
		t.Fatal("no concurrent reads were observed")
	}
	for index, id := range ids {
		snapshot, ok := runner.GetTest(id)
		if !ok {
			t.Fatalf("test %q disappeared during concurrent publishing", id)
		}
		if snapshot.LiveMetrics == nil {
			t.Fatalf("test %q has no live metrics after %d publications", id, samples)
		}
		if snapshot.LiveMetrics.ActiveVUsers != int64(index+1) {
			t.Fatalf("test %q live active vusers = %d, want %d (a cross-run write would change this)", id, snapshot.LiveMetrics.ActiveVUsers, index+1)
		}
		if snapshot.LiveMetrics.TotalTx != int64(samples-1) {
			t.Fatalf("test %q live total transactions = %d, want the last published sample %d", id, snapshot.LiveMetrics.TotalTx, samples-1)
		}
		if len(snapshot.History) == 0 || len(snapshot.History) > maxManagedTestHistory {
			t.Fatalf("test %q history length = %d, want 1..%d", id, len(snapshot.History), maxManagedTestHistory)
		}
	}
}

// TestRunnerConcurrentPublishAndDeleteKeepsMapsConsistent races metric
// publication against deletion of the very runs being published, which is the
// ordering that actually happens when an operator deletes a finished run while
// a final metrics batch is still draining.
func TestRunnerConcurrentPublishAndDeleteKeepsMapsConsistent(t *testing.T) {
	runner := newConcurrencyTestRunner(t, 16)

	const runs = 6
	ids := make([]string, 0, runs)
	managedTests := make([]*ManagedTest, 0, runs)
	for index := range runs {
		scriptID := fmt.Sprintf("churn-script-%d", index)
		runner.StoreScript(concurrencyTestScript(scriptID))
		managed, err := runner.CreateTest(CreateTestRequest{
			Name:     fmt.Sprintf("churn-run-%d", index),
			Duration: "30s",
			Groups:   []CreateGroupEntry{{Name: "g", ScriptID: scriptID, VUsers: 1}},
		})
		if err != nil {
			t.Fatalf("CreateTest: %v", err)
		}
		ids = append(ids, managed.ID)
		runner.mu.RLock()
		live := runner.tests[managed.ID]
		runner.mu.RUnlock()
		managedTests = append(managedTests, live)
	}

	var group sync.WaitGroup
	start := make(chan struct{})
	for index, test := range managedTests {
		group.Add(1)
		go func(index int, test *ManagedTest) {
			defer group.Done()
			artifacts := &runArtifacts{runner: runner, test: test}
			<-start
			for sample := range 30 {
				runner.publishLiveMetrics(test, &LiveMetrics{
					Timestamp:    time.Now(),
					ActiveVUsers: int64(index + 1),
					TotalTx:      int64(sample),
				}, artifacts)
			}
		}(index, test)
	}
	for _, id := range ids {
		group.Add(1)
		go func(id string) {
			defer group.Done()
			<-start
			runner.DeleteTest(id)
		}(id)
	}
	group.Add(1)
	go func() {
		defer group.Done()
		<-start
		for range 200 {
			_ = runner.ListTests()
			_ = runner.TestCount()
		}
	}()
	close(start)
	group.Wait()

	if got := runner.TestCount(); got != 0 {
		t.Fatalf("runner holds %d tests after deleting every run, want 0", got)
	}
	for _, id := range ids {
		if _, ok := runner.GetTest(id); ok {
			t.Fatalf("deleted test %q is still reachable", id)
		}
	}
}

package api

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

// distributedRunTotalsBatch is one generator's metrics window: count results
// of which errors failed, in group "buyers".
func distributedRunTotalsBatch(testID string, sequence int64, count, errors int64) generatorrpc.MetricsBatch {
	return generatorrpc.MetricsBatch{
		TestID:          testID,
		LifecycleState:  "RUNNING",
		SourceEpoch:     "epoch-1",
		BatchSequence:   uint64(sequence),
		TimestampUnixMs: 1_000 * sequence,
		WindowMs:        1000,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 1},
		FleetMetrics:    &generatorrpc.MetricAggregate{Count: count, Errors: errors, TPS: float64(count)},
		Groups:          []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 1, Metrics: &generatorrpc.MetricAggregate{Count: count, Errors: errors, TPS: float64(count)}}},
	}
}

// Publications are throttled fleet-wide and carry each generator's latest
// window only, so run-to-date totals count every accepted window once where
// it is accepted: a window replaced before any publication still counts, and
// a terminal snapshot that carries the last window forward does not count it
// again.
func TestDistributedRunTotalsCountEveryAcceptedWindowOnce(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	run := &ManagedTest{
		ID:                    "distributed-run-totals",
		Name:                  "Distributed run totals",
		Status:                "RUNNING",
		Groups:                []TestGroup{{Name: "buyers", VUsers: 2}},
		remoteClientsByTarget: map[string]generatorClient{"gen-a": nil, "gen-b": nil},
	}
	runner.tests[run.ID] = run
	backend := &distributedExecutionBackend{}
	store := func(target string, batch generatorrpc.MetricsBatch) {
		t.Helper()
		if !backend.storeRemoteMetricsBatchOnly(runner, run, target, batch) {
			t.Fatalf("%s window %d was not accepted", target, batch.BatchSequence)
		}
	}
	publish := func(target string) *LiveMetrics {
		t.Helper()
		live, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, run, target)
		if !fresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
			t.Fatalf("publication after %s was not published", target)
		}
		return run.LiveMetrics
	}

	store("gen-a", distributedRunTotalsBatch(run.ID, 1, 100, 1))
	store("gen-b", distributedRunTotalsBatch(run.ID, 1, 100, 0))
	publish("gen-b")
	// gen-a's window 2 is replaced by window 3 before any publication.
	store("gen-a", distributedRunTotalsBatch(run.ID, 2, 100, 2))
	store("gen-a", distributedRunTotalsBatch(run.ID, 3, 100, 0))
	store("gen-b", distributedRunTotalsBatch(run.ID, 2, 100, 0))
	live := publish("gen-b")
	if got, errors := deref(live.RunTotalTx), deref(live.RunTotalErrors); got != 497 || errors != 3 {
		t.Fatalf("run totals after a replaced window = %d tx / %d errors, want 497 / 3", got, errors)
	}
	if group := live.GroupMetrics["buyers"]; group == nil || deref(group.RunTransactions) != 497 || deref(group.RunErrors) != 3 {
		t.Fatalf("group run totals = %+v, want 497 / 3", group)
	}

	// Each generator ends: its terminal batch carries no window of its own,
	// and the controller keeps showing its last window.
	for _, target := range []string{"gen-a", "gen-b"} {
		terminal := generatorrpc.MetricsBatch{TestID: run.ID, LifecycleState: "COMPLETE", SourceEpoch: "epoch-1", BatchSequence: 9, TimestampUnixMs: 9_000, WindowMs: 1000}
		live, _, _, fresh, publication := backend.applyRemoteMetricsBatchWithFreshness(runner, run, target, terminal, false)
		if !fresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
			t.Fatalf("%s terminal publication was not published", target)
		}
	}
	if got, errors := deref(run.LiveMetrics.RunTotalTx), deref(run.LiveMetrics.RunTotalErrors); got != 497 || errors != 3 {
		t.Fatalf("run totals after the terminal publications = %d tx / %d errors, want 497 / 3", got, errors)
	}
}

func distributedRunTotalsRun(t *testing.T, id string, groups []TestGroup, targets ...string) (*TestRunner, *ManagedTest, *distributedExecutionBackend) {
	t.Helper()
	runner := NewTestRunner(1, NewHub(), nil)
	run := &ManagedTest{ID: id, Name: id, Status: "RUNNING", Groups: groups, remoteClientsByTarget: map[string]generatorClient{}}
	for _, target := range targets {
		run.remoteClientsByTarget[target] = nil
	}
	runner.tests[run.ID] = run
	return runner, run, &distributedExecutionBackend{}
}

// collected stamps a batch as collection number collection, published at
// published (milliseconds), of its generator's source epoch.
func collected(batch generatorrpc.MetricsBatch, collection uint64, published int64) generatorrpc.MetricsBatch {
	batch.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: collection, PublishedAtUnixMs: published, CollectedAtUnixMs: published - 5}
	return batch
}

func publishTerminal(t *testing.T, runner *TestRunner, run *ManagedTest, backend *distributedExecutionBackend, target string, terminal generatorrpc.MetricsBatch) {
	t.Helper()
	live, _, _, fresh, publication := backend.applyRemoteMetricsBatchWithFreshness(runner, run, target, terminal, false)
	if !fresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
		t.Fatalf("%s terminal publication was not published", target)
	}
}

func wantRunTotals(t *testing.T, run *ManagedTest, tx, errors int64) {
	t.Helper()
	if got, gotErrors := deref(run.LiveMetrics.RunTotalTx), deref(run.LiveMetrics.RunTotalErrors); got != tx || gotErrors != errors {
		t.Fatalf("run totals = %d tx / %d errors, want %d / %d", got, gotErrors, tx, errors)
	}
}

// A batch built without a new collection (stopping, paused, terminal, a
// subscription replay) re-sends the generator's last window under that
// collection's number; it is counted once. Two collections in the same
// millisecond are both counted, and a stop that drains a new window is.
func TestDistributedRunTotalsCountEachCollectionOnce(t *testing.T) {
	runner, run, backend := distributedRunTotalsRun(t, "distributed-run-totals-collections", []TestGroup{{Name: "buyers", VUsers: 2}}, "gen-a", "gen-b")
	store := func(target string, batch generatorrpc.MetricsBatch) {
		t.Helper()
		if !backend.storeRemoteMetricsBatchOnly(runner, run, target, batch) {
			t.Fatalf("%s %s batch %d was not accepted", target, batch.LifecycleState, batch.BatchSequence)
		}
	}
	with := func(batch generatorrpc.MetricsBatch, state string) generatorrpc.MetricsBatch {
		batch.LifecycleState = state
		return batch
	}
	store("gen-a", collected(distributedRunTotalsBatch(run.ID, 1, 100, 0), 1, 1_000))
	store("gen-a", collected(distributedRunTotalsBatch(run.ID, 2, 100, 1), 2, 2_000))
	// The final flush, in the same millisecond as the last tick.
	store("gen-a", collected(distributedRunTotalsBatch(run.ID, 3, 26, 0), 3, 2_000))
	// Re-sends of the final flush.
	store("gen-a", with(collected(distributedRunTotalsBatch(run.ID, 4, 26, 0), 3, 2_000), "PAUSED"))
	store("gen-a", collected(distributedRunTotalsBatch(run.ID, 5, 26, 0), 3, 2_000))
	store("gen-a", with(collected(distributedRunTotalsBatch(run.ID, 6, 26, 0), 3, 2_000), "STOPPING"))
	terminal := with(collected(distributedRunTotalsBatch(run.ID, 7, 26, 0), 3, 2_000), "COMPLETE")
	terminal.TimestampUnixMs = 9_000
	publishTerminal(t, runner, run, backend, "gen-a", terminal)
	wantRunTotals(t, run, 225, 1)

	// gen-b is stopped: its terminal batch carries the window drained at the
	// stop under a new collection.
	store("gen-b", collected(distributedRunTotalsBatch(run.ID, 1, 50, 0), 1, 1_000))
	drained := with(collected(distributedRunTotalsBatch(run.ID, 2, 5, 1), 2, 1_400), "ABORTED")
	drained.TimestampUnixMs = 9_000
	publishTerminal(t, runner, run, backend, "gen-b", drained)
	wantRunTotals(t, run, 279, 2)
}

// An older generator numbers no collections: its re-sent window is matched by
// the publication time of the collection that produced it.
func TestDistributedRunTotalsMatchAnOlderGeneratorsWindowByPublicationTime(t *testing.T) {
	runner, run, backend := distributedRunTotalsRun(t, "distributed-run-totals-older", []TestGroup{{Name: "buyers", VUsers: 1}}, "gen-a")
	for _, batch := range []generatorrpc.MetricsBatch{
		collected(distributedRunTotalsBatch(run.ID, 1, 100, 0), 0, 1_000),
		collected(distributedRunTotalsBatch(run.ID, 2, 100, 1), 0, 2_000),
		collected(distributedRunTotalsBatch(run.ID, 3, 26, 0), 0, 2_030),
	} {
		if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", batch) {
			t.Fatalf("window %d was not accepted", batch.BatchSequence)
		}
	}
	terminal := collected(distributedRunTotalsBatch(run.ID, 4, 26, 0), 0, 2_030)
	terminal.LifecycleState, terminal.TimestampUnixMs = "COMPLETE", 9_000
	publishTerminal(t, runner, run, backend, "gen-a", terminal)
	wantRunTotals(t, run, 225, 1)
}

// A restarted generator (a new source epoch) numbers its collections afresh;
// a late batch from the old epoch is refused before it is counted.
func TestDistributedRunTotalsCountARestartedGeneratorsWindows(t *testing.T) {
	runner, run, backend := distributedRunTotalsRun(t, "distributed-run-totals-restart", []TestGroup{{Name: "buyers", VUsers: 1}}, "gen-a")
	store := func(batch generatorrpc.MetricsBatch) bool {
		return backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", batch)
	}
	first := collected(distributedRunTotalsBatch(run.ID, 1, 100, 0), 7, 1_000)
	first.SourceEpochStartedUnixMs = 500
	if !store(first) {
		t.Fatal("the first epoch's window was not accepted")
	}
	restarted := collected(distributedRunTotalsBatch(run.ID, 1, 10, 1), 1, 500)
	restarted.SourceEpoch, restarted.SourceEpochStartedUnixMs = "epoch-2", 3_000
	if !store(restarted) {
		t.Fatal("the restarted generator's first window was not accepted")
	}
	late := collected(distributedRunTotalsBatch(run.ID, 2, 100, 0), 8, 2_000)
	late.SourceEpochStartedUnixMs = 500
	if store(late) {
		t.Fatal("a late window from the old epoch was accepted")
	}
	live, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, run, "gen-a")
	if !fresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
		t.Fatal("the publication was not published")
	}
	wantRunTotals(t, run, 109, 1)
}

// A generator running several groups reports each group's window; each
// group's run totals are its own.
func TestDistributedRunTotalsCountEachGroupOfAGenerator(t *testing.T) {
	runner, run, backend := distributedRunTotalsRun(t, "distributed-run-totals-groups", []TestGroup{{Name: "buyers", VUsers: 1}, {Name: "sellers", VUsers: 1}}, "gen-a")
	batch := collected(distributedRunTotalsBatch(run.ID, 1, 100, 2), 1, 1_000)
	batch.Groups = []generatorrpc.GroupMetric{
		{Name: "buyers", ActiveVUsers: 1, Metrics: &generatorrpc.MetricAggregate{Count: 60, Errors: 2}},
		{Name: "sellers", ActiveVUsers: 1, Metrics: &generatorrpc.MetricAggregate{Count: 40}},
	}
	if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", batch) {
		t.Fatal("the window was not accepted")
	}
	live, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, run, "gen-a")
	if !fresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
		t.Fatal("the publication was not published")
	}
	wantRunTotals(t, run, 98, 2)
	for name, want := range map[string][2]int64{"buyers": {58, 2}, "sellers": {40, 0}} {
		group := run.LiveMetrics.GroupMetrics[name]
		if group == nil || deref(group.RunTransactions) != want[0] || deref(group.RunErrors) != want[1] {
			t.Fatalf("%s run totals = %+v, want %d / %d", name, group, want[0], want[1])
		}
	}
}

// A controller that takes a run over restores its history from storage, which
// keeps each window but no run totals: the totals start from the sum of those
// windows.
func TestDistributedRunTotalsSeedFromStoredHistory(t *testing.T) {
	timestamp := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveTest(&storage.TestRecord{ID: "adopted", Name: "Adopted", Status: "RUNNING", StartTime: timestamp}); err != nil {
		t.Fatalf("save test: %v", err)
	}
	for index, window := range [][2]int64{{300, 3}, {200, 0}} {
		at := timestamp.Add(time.Duration(index) * time.Second)
		if err := store.SaveHistory("adopted", at, &LiveMetrics{Timestamp: at, TotalTx: window[0], TotalErrors: window[1]}); err != nil {
			t.Fatalf("save history: %v", err)
		}
	}
	runner, run, backend := distributedRunTotalsRun(t, "adopted", []TestGroup{{Name: "buyers", VUsers: 1}}, "gen-a")
	runner.historyStore = store
	runner.loadStoredTestHistory(run)
	if len(run.History) != 2 || run.History[1].RunTotalTx != nil {
		t.Fatalf("restored history = %+v, want two windows without run totals", run.History)
	}
	if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", collected(distributedRunTotalsBatch(run.ID, 1, 100, 1), 1, 1_000)) {
		t.Fatal("the window was not accepted")
	}
	live, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, run, "gen-a")
	if !fresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
		t.Fatal("the publication was not published")
	}
	wantRunTotals(t, run, 599, 4)
	// The run's one group starts from the same sum.
	if group := run.LiveMetrics.GroupMetrics["buyers"]; group == nil || deref(group.RunTransactions) != 599 || deref(group.RunErrors) != 4 {
		t.Fatalf("buyers run totals = %+v, want 599 / 4", group)
	}
}

// distributedWindowCounts counts a window as the published sample built from
// it does, for every shape a generator sends.
func TestDistributedWindowCountsMatchPublishedSamples(t *testing.T) {
	tx := func(group string, success bool) generatorrpc.TransactionMetric {
		return generatorrpc.TransactionMetric{Name: "checkout", Group: group, Success: success, DurationMs: 10}
	}
	aggregate := func(name string, count, errors int64) *generatorrpc.MetricAggregate {
		return &generatorrpc.MetricAggregate{Name: name, Count: count, Errors: errors}
	}
	cases := map[string]generatorrpc.MetricsBatch{
		"fleet and group aggregates": {
			FleetMetrics: aggregate("fleet", 100, 4),
			Groups:       []generatorrpc.GroupMetric{{Name: "buyers", Metrics: aggregate("buyers", 70, 4)}, {Name: "sellers", Metrics: aggregate("sellers", 30, 0)}},
		},
		"more errors than attempts": {
			FleetMetrics: aggregate("fleet", 10, 25),
			Groups:       []generatorrpc.GroupMetric{{Name: "buyers", Metrics: aggregate("buyers", 10, 25)}},
		},
		"transaction aggregates only": {
			TransactionMetrics: []generatorrpc.MetricAggregate{*aggregate("checkout", 40, 1), *aggregate("search", 20, 30), *aggregate(" ", 99, 0)},
			Groups:             []generatorrpc.GroupMetric{{Name: "buyers", Metrics: aggregate("buyers", 60, 21)}},
		},
		"group listed without aggregates, raw groups": {
			TransactionMetrics: []generatorrpc.MetricAggregate{*aggregate("checkout", 4, 1)},
			Groups:             []generatorrpc.GroupMetric{{Name: "buyers"}},
			Transactions:       []generatorrpc.TransactionMetric{tx("buyers", true), tx("buyers", false), tx("sellers", true), tx("", true)},
		},
		"raw only, one listed group": {
			Groups:       []generatorrpc.GroupMetric{{Name: "buyers"}},
			Transactions: []generatorrpc.TransactionMetric{tx("", true), tx("", false), tx("buyers", true)},
		},
		"raw only, two listed groups": {
			Groups:       []generatorrpc.GroupMetric{{Name: "buyers"}, {Name: "sellers"}},
			Transactions: []generatorrpc.TransactionMetric{tx("", true), tx("buyers", false), tx("sellers", true), tx("other", true)},
		},
		"raw only, no groups": {
			Transactions: []generatorrpc.TransactionMetric{tx("", true), tx("", false), tx("", true)},
		},
		"group aggregates without a window": {
			Groups: []generatorrpc.GroupMetric{{Name: "buyers", Metrics: aggregate("buyers", 5, 0)}},
		},
		"repeated group aggregates": {
			FleetMetrics: aggregate("fleet", 50, 1),
			Groups:       []generatorrpc.GroupMetric{{Name: "buyers", Metrics: aggregate("buyers", 30, 1)}, {Name: "buyers", Metrics: aggregate("buyers", 20, 0)}},
		},
		"repeated groups without aggregates": {
			TransactionMetrics: []generatorrpc.MetricAggregate{*aggregate("checkout", 2, 0)},
			Groups:             []generatorrpc.GroupMetric{{Name: "buyers"}, {Name: "buyers"}},
			Transactions:       []generatorrpc.TransactionMetric{tx("buyers", true), tx("buyers", true)},
		},
		"empty": {},
	}
	// The raw fallback taken for a dependency or SOAP target that has no
	// aggregate of its own, beside transaction aggregates.
	for name, mark := range map[string]func(*generatorrpc.TransactionMetric){
		"dependency": func(tx *generatorrpc.TransactionMetric) { tx.DependencyTarget = "db" },
		"SOAP":       func(tx *generatorrpc.TransactionMetric) { tx.SOAPOperation = "GetQuote" },
	} {
		marked := []generatorrpc.TransactionMetric{tx("", true), tx("buyers", false), tx("sellers", true)}
		for index := range marked {
			mark(&marked[index])
		}
		cases["transaction aggregates, raw fallback for a "+name+" target"] = generatorrpc.MetricsBatch{
			TransactionMetrics: []generatorrpc.MetricAggregate{*aggregate("checkout", 3, 1)},
			Groups:             []generatorrpc.GroupMetric{{Name: "buyers"}, {Name: "sellers"}},
			Transactions:       marked,
		}
	}
	// More group names than the raw fallback keeps apart.
	overflow := generatorrpc.MetricsBatch{}
	for index := 0; index < 300; index++ {
		overflow.Transactions = append(overflow.Transactions, tx(fmt.Sprintf("group-%03d", index), index%7 != 0))
	}
	cases["raw groups beyond the series bound"] = overflow
	for _, groups := range [][]TestGroup{{{Name: "buyers"}}, {{Name: "buyers"}, {Name: "sellers"}}} {
		run := &ManagedTest{ID: "window-counts", Groups: groups}
		for name, batch := range cases {
			batch.WindowMs = 1000
			aggregated, fleetTPS, groupTPS := aggregateDistributedMetricsBatches(run.ID, map[string]generatorrpc.MetricsBatch{"gen-a": batch})
			live := buildLiveMetricsFromDistributedBatch(run, aggregated, fleetTPS, groupTPS)
			got := distributedWindowCounts(batch, groups)
			if got.tx != live.TotalTx || got.errors != live.TotalErrors {
				t.Errorf("%s, %d groups: window = %d / %d, sample = %d / %d", name, len(groups), got.tx, got.errors, live.TotalTx, live.TotalErrors)
			}
			for groupName, group := range live.GroupMetrics {
				if counts := got.groups[groupName]; counts.tx != group.Transactions || counts.errors != group.Errors {
					t.Errorf("%s, %d groups: %s = %d / %d, sample = %d / %d", name, len(groups), groupName, counts.tx, counts.errors, group.Transactions, group.Errors)
				}
			}
			for groupName, counts := range got.groups {
				if _, ok := live.GroupMetrics[groupName]; !ok && (counts.tx != 0 || counts.errors != 0) {
					t.Errorf("%s, %d groups: %s counted %d / %d, absent from the sample", name, len(groups), groupName, counts.tx, counts.errors)
				}
			}
		}
	}
}

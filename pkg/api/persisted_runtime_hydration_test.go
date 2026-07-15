package api

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPersistedTestRestoresExactCompiledRuntimeSnapshot(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	pool := &storage.SharedDataPool{
		ID:        "restart-pool",
		Name:      "Restart pool",
		ProjectID: storage.DefaultProjectID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID:        "restart-row",
		PoolID:    pool.ID,
		Data:      map[string]string{"customer_id": "customer-42"},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}

	creator := NewTestRunner(2, NewHub(), store)
	storedScript, err := creator.StoreScriptDurable(httpScript("restart-script", "Restart script"))
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	created, err := creator.CreateTest(CreateTestRequest{
		Name:      "Restart-safe run",
		ProjectID: storage.DefaultProjectID,
		Duration:  "1m",
		Groups: []CreateGroupEntry{{
			ScriptID: storedScript.ID,
			VUsers:   1,
			RuntimeSettings: &RuntimeSettingsOverride{RedactionRules: []engine.RedactionRule{{
				Name: "customer id", Pattern: `customer-[0-9]+`, Replacement: "customer-redacted",
			}}},
		}},
		DataPools: []storage.ScenarioDataPoolBinding{{
			PoolID: pool.ID, Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	restarted := NewTestRunner(2, NewHub(), store)
	restarted.mu.RLock()
	restored := restarted.tests[created.ID]
	if restored == nil {
		restarted.mu.RUnlock()
		t.Fatal("restarted runner did not load persisted test")
	}
	if restored.runtimeHydrationError != "" {
		restarted.mu.RUnlock()
		t.Fatalf("runtime hydration error = %q", restored.runtimeHydrationError)
	}
	if len(restored.groupSpecs) != 1 || restored.groupSpecs[0].Script == nil {
		restarted.mu.RUnlock()
		t.Fatalf("restored group specs = %#v, want one compiled script", restored.groupSpecs)
	}
	runtimeScript := restored.groupSpecs[0].Script
	redactionRules := append([]engine.RedactionRule(nil), restored.Groups[0].redactionRules...)
	restarted.mu.RUnlock()

	if len(runtimeScript.DataSources) != 1 || runtimeScript.DataSources[0].Name != "customers" {
		t.Fatalf("restored data sources = %#v, want customers snapshot", runtimeScript.DataSources)
	}
	if !strings.Contains(runtimeScript.DataSources[0].InlineData, "customer-42") {
		t.Fatalf("restored inline pool snapshot = %q, want persisted customer row", runtimeScript.DataSources[0].InlineData)
	}
	if len(redactionRules) != 1 || redactionRules[0].Pattern != `customer-[0-9]+` || redactionRules[0].Replacement != "customer-redacted" {
		t.Fatalf("restored redaction rules = %#v", redactionRules)
	}

	// Mutating the shared data pool after creation must not change the run's
	// already-compiled snapshot on a later controller restart.
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID:        "later-row",
		PoolID:    pool.ID,
		Data:      map[string]string{"customer_id": "customer-99"},
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveDataPoolRow(later): %v", err)
	}
	again := NewTestRunner(2, NewHub(), store)
	again.mu.RLock()
	againScript := again.tests[created.ID].groupSpecs[0].Script
	again.mu.RUnlock()
	if strings.Contains(againScript.DataSources[0].InlineData, "customer-99") {
		t.Fatalf("restart re-resolved mutable pool data instead of retaining the compiled snapshot: %q", againScript.DataSources[0].InlineData)
	}
}

func TestLegacyPersistedTestGroupsRecompileAfterRestart(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	creator := NewTestRunner(2, NewHub(), store)
	storedScript, err := creator.StoreScriptDurable(httpScript("legacy-runtime-script", "Legacy runtime"))
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	created, err := creator.CreateTest(CreateTestRequest{
		Name: "Legacy run", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: storedScript.ID, VUsers: 2}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	record, err := store.GetTest(created.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	// Simulate a row written before runtime_script was added to groups_json.
	record.Groups = created.Groups
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest(legacy): %v", err)
	}

	restarted := NewTestRunner(2, NewHub(), store)
	restarted.mu.RLock()
	restored := restarted.tests[created.ID]
	restarted.mu.RUnlock()
	if restored == nil || restored.runtimeHydrationError != "" || len(restored.groupSpecs) != 1 {
		t.Fatalf("legacy runtime was not recompiled: %#v", restored)
	}
	if restored.groupSpecs[0].Script == nil || restored.groupSpecs[0].Script.ID != storedScript.ID || restored.groupSpecs[0].VUsers != 2 {
		t.Fatalf("legacy group spec = %#v", restored.groupSpecs[0])
	}
}

func TestPersistedRuntimeHydrationFailureBlocksStart(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	if err := store.SaveTest(&storage.TestRecord{
		ID:          "missing-runtime-script",
		Name:        "Missing runtime script",
		Status:      "QUEUED",
		ProjectID:   storage.DefaultProjectID,
		Duration:    int64(time.Minute),
		StartTime:   time.Now().UTC(),
		TotalVUsers: 1,
		Groups:      []TestGroup{{Name: "Missing", ScriptID: "deleted-script", VUsers: 1}},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	restarted := NewTestRunner(2, NewHub(), store)
	err = restarted.StartTest("missing-runtime-script")
	if err == nil || !strings.Contains(err.Error(), "test runtime could not be restored") {
		t.Fatalf("StartTest error = %v, want fail-closed hydration error", err)
	}
	restarted.mu.RLock()
	status := restarted.tests["missing-runtime-script"].Status
	restarted.mu.RUnlock()
	if status != "QUEUED" {
		t.Fatalf("failed hydration changed lifecycle status to %q, want QUEUED", status)
	}
}

func TestManagedTestHistoryIsBoundedBySerializedBytes(t *testing.T) {
	points := []*LiveMetrics{
		{Timestamp: time.Unix(1, 0), ErrorBreakdown: map[string]int64{strings.Repeat("a", 256): 1}},
		{Timestamp: time.Unix(2, 0), ErrorBreakdown: map[string]int64{strings.Repeat("b", 256): 2}},
		{Timestamp: time.Unix(3, 0), ErrorBreakdown: map[string]int64{strings.Repeat("c", 256): 3}},
	}
	secondWeight := managedTestHistoryPointWeight(points[1], 1<<20)
	thirdWeight := managedTestHistoryPointWeight(points[2], 1<<20)
	testRun := &ManagedTest{}
	byteLimit := secondWeight + thirdWeight
	for _, point := range points {
		if !appendManagedTestHistoryWithLimits(testRun, point, 10, byteLimit) {
			t.Fatalf("point at %s was not retained", point.Timestamp)
		}
	}
	if len(testRun.History) != 2 || testRun.History[0] != points[1] || testRun.History[1] != points[2] {
		t.Fatalf("byte-bounded history = %#v, want newest two points", testRun.History)
	}
	if testRun.historyBytes > byteLimit || len(testRun.historyWeights) != len(testRun.History) {
		t.Fatalf("history accounting = %d bytes / %d weights, limit %d / %d points", testRun.historyBytes, len(testRun.historyWeights), byteLimit, len(testRun.History))
	}

	oversized := &LiveMetrics{ErrorBreakdown: map[string]int64{strings.Repeat("x", 1024): 1}}
	if appendManagedTestHistoryWithLimits(testRun, oversized, 10, 64) {
		t.Fatal("oversized history point was retained")
	}
	if len(testRun.History) != 0 || testRun.historyBytes != 0 {
		t.Fatalf("re-accounted history after tighter limit = %d points / %d bytes, want empty", len(testRun.History), testRun.historyBytes)
	}
}

func TestGetTestSummaryDoesNotCloneRichHistory(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	testRun := &ManagedTest{
		ID:        "summary-with-history",
		Name:      "Summary",
		Status:    "RUNNING",
		ProjectID: storage.DefaultProjectID,
		CreatedAt: time.Now().UTC(),
		History: []*LiveMetrics{{
			Timestamp: time.Now().Add(-time.Second), TPS: 1,
		}, {
			Timestamp: time.Now(), TPS: 2,
		}},
	}
	testRun.LiveMetrics = testRun.History[len(testRun.History)-1]
	runner.mu.Lock()
	runner.tests[testRun.ID] = testRun
	runner.mu.Unlock()

	summary, ok := runner.GetTestSummary(testRun.ID)
	if !ok || summary == nil {
		t.Fatal("GetTestSummary did not find test")
	}
	if len(summary.History) != 0 {
		t.Fatalf("summary cloned %d rich history points, want none", len(summary.History))
	}
	if len(summary.Sparkline) != 2 {
		t.Fatalf("summary sparkline = %#v, want two lightweight points", summary.Sparkline)
	}
	full, ok := runner.GetTest(testRun.ID)
	if !ok || len(full.History) != 2 {
		t.Fatalf("full test history = %#v, want two points", full)
	}
}

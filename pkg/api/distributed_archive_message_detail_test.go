package api

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestMessageDetailBudgetsForTestIncludesAdaptiveMode(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{
		Name: "adaptive",
		script: &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{
			Mode: "adaptive", MaxItems: 4, MaxItemBytes: 1024, MaxTotalBytes: 4096,
		}}}},
	}}}
	budget := messageDetailBudgetsForTest(test)["adaptive"]
	if budget == nil || !budget.adaptive || budget.maxItems != 4 {
		t.Fatalf("adaptive budget = %#v", budget)
	}
}

func TestControllerAdaptiveBudgetPreservesPriorityShares(t *testing.T) {
	budget := &controllerMessageDetailBudget{
		maxItems: 4, maxItemBytes: 1024, maxTotalBytes: 4096, adaptive: true,
		priorityItems: make(map[int]int64), priorityBytes: make(map[int]int64),
	}
	stats := &RunArchiveMessageDetailCapture{Dropped: make(map[string]int64)}
	newEvent := func(priority int) *TransactionEvent {
		return &TransactionEvent{Success: true, MessageDetailPriority: priority, Snapshot: &protocol.ErrorSnapshot{ResponseBody: "retained"}}
	}
	firstLow := newEvent(1)
	enforceControllerMessageDetailBudget(firstLow, budget, stats)
	secondLow := newEvent(1)
	enforceControllerMessageDetailBudget(secondLow, budget, stats)
	high := newEvent(3)
	enforceControllerMessageDetailBudget(high, budget, stats)
	if firstLow.Snapshot == nil || secondLow.Snapshot != nil || high.Snapshot == nil {
		t.Fatalf("adaptive retention low1=%t low2=%t high=%t", firstLow.Snapshot != nil, secondLow.Snapshot != nil, high.Snapshot != nil)
	}
	if stats.Dropped["controller_priority_1_item_limit"] != 1 || stats.RetainedItems != 2 {
		t.Fatalf("adaptive stats = %#v", stats)
	}
}

func TestControllerAdaptiveBudgetWithOneItemRetainsHighPriority(t *testing.T) {
	budget := &controllerMessageDetailBudget{
		maxItems: 1, maxItemBytes: 1024, maxTotalBytes: 1024, adaptive: true,
		priorityItems: make(map[int]int64), priorityBytes: make(map[int]int64),
	}
	stats := &RunArchiveMessageDetailCapture{Dropped: make(map[string]int64)}
	event := &TransactionEvent{Success: true, MessageDetailPriority: 3, Snapshot: &protocol.ErrorSnapshot{ResponseBody: "outlier"}}
	enforceControllerMessageDetailBudget(event, budget, stats)
	if event.Snapshot == nil || stats.RetainedItems != 1 {
		t.Fatalf("one-item controller capture = event %#v stats %#v", event, stats)
	}
}

func TestMergeRemoteArchiveTransactionsEnforcesFleetBudgetAndDerivesIdentity(t *testing.T) {
	root := t.TempDir()
	writeSource := func(name string, event TransactionEvent) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(event); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	base := TransactionEvent{
		Timestamp:     time.Unix(123, 0).UTC(),
		TestID:        "distributed-detail",
		Group:         "buyers",
		ActionName:    "GET /checkout",
		Success:       true,
		CorrelationID: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		Snapshot:      &protocol.ErrorSnapshot{ResponseBody: "retained"},
	}
	bundles := []remoteArchiveBundle{
		{target: "a", transactionsPath: writeSource("a.jsonl", base), manifest: RunArchiveManifest{TransactionCount: 1}},
		{target: "b", transactionsPath: writeSource("b.jsonl", base), manifest: RunArchiveManifest{TransactionCount: 1}},
	}
	manifest := RunArchiveManifest{MessageDetailCapture: &RunArchiveMessageDetailCapture{RetainedItems: 2, RetainedBytes: 32}}
	destination := filepath.Join(root, "merged.jsonl")
	budgets := map[string]*controllerMessageDetailBudget{
		"buyers": {maxItems: 1, maxItemBytes: 1024, maxTotalBytes: 1024},
	}
	if err := mergeRemoteArchiveTransactions(destination, &manifest, bundles, budgets); err != nil {
		t.Fatalf("merge transactions: %v", err)
	}

	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []TransactionEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event TransactionEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Snapshot == nil || events[1].Snapshot != nil {
		t.Fatalf("merged snapshots = %#v", events)
	}
	if events[0].EventID == "" || events[1].EventID == "" || events[0].EventID == events[1].EventID {
		t.Fatalf("event IDs are not stable and unique: %q, %q", events[0].EventID, events[1].EventID)
	}
	if events[0].TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || events[0].SpanID != "00f067aa0ba902b7" {
		t.Fatalf("trace identity = %q/%q", events[0].TraceID, events[0].SpanID)
	}
	if manifest.MessageDetailCapture.RetainedItems != 1 || manifest.MessageDetailCapture.Dropped["controller_item_limit"] != 1 {
		t.Fatalf("capture stats = %#v", manifest.MessageDetailCapture)
	}
}

func TestMergeRemoteArchiveTransactionsReplacesGeneratorRegion(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.jsonl")
	event := TransactionEvent{Timestamp: time.Now().UTC(), TestID: "test", Group: "load", Region: "forged", ActionName: "request", Success: true}
	content, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, append(content, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	destinationPath := filepath.Join(root, "merged.jsonl")
	manifest := RunArchiveManifest{}
	bundles := []remoteArchiveBundle{{
		target: "generator-a", region: "us-east", transactionsPath: sourcePath,
		manifest: RunArchiveManifest{TransactionCount: 1},
	}}
	if err := mergeRemoteArchiveTransactions(destinationPath, &manifest, bundles, nil); err != nil {
		t.Fatalf("merge: %v", err)
	}
	mergedFile, err := os.Open(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer mergedFile.Close()
	var merged TransactionEvent
	if err := json.NewDecoder(mergedFile).Decode(&merged); err != nil {
		t.Fatal(err)
	}
	if merged.Region != "us-east" {
		t.Fatalf("merged region = %q, want controller-owned us-east", merged.Region)
	}
}

func TestMergeRemoteArchiveTransactionsDropsUnbudgetedSuccessfulDetail(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.jsonl")
	source, err := os.Create(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(source).Encode(TransactionEvent{
		TestID: "run", Group: "unexpected", Success: true,
		Snapshot: &protocol.ErrorSnapshot{ResponseBody: "must not bypass the fleet budget"},
	}); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	manifest := RunArchiveManifest{MessageDetailCapture: &RunArchiveMessageDetailCapture{RetainedItems: 1}}
	destination := filepath.Join(root, "merged.jsonl")
	if err := mergeRemoteArchiveTransactions(destination, &manifest, []remoteArchiveBundle{{
		target: "a", transactionsPath: sourcePath, manifest: RunArchiveManifest{TransactionCount: 1},
	}}, map[string]*controllerMessageDetailBudget{"known": {maxItems: 1, maxItemBytes: 1024, maxTotalBytes: 1024}}); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var event TransactionEvent
	if err := json.NewDecoder(file).Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.Snapshot != nil || manifest.MessageDetailCapture.Dropped["controller_unknown_group"] != 1 {
		t.Fatalf("event/stats = %#v / %#v", event, manifest.MessageDetailCapture)
	}
}

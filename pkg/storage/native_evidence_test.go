package storage

import (
	"github.com/jasonczech/apexload/pkg/engine"
	"testing"
	"time"
)

func TestNativeEvidencePersistenceAndClone(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/native.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	digest := engine.NewTDigest(200)
	for i := 0; i < 7; i++ {
		digest.Add(float64(90 * time.Millisecond))
	}
	snapshot := digest.Snapshot()
	evidence := &NativeIntervalEvidence{Version: 2, WindowSeconds: 2, Dimensions: map[string]map[string]NativeIntervalMetric{"action": {"Checkout": {Count: 7, Errors: 1, P95: 90, DurationDigest: &snapshot}}}}
	at := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "native", Status: "RUNNING", StartTime: at}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory("run", at, map[string]any{"timestamp": at, "native_evidence": evidence}); err != nil {
		t.Fatal(err)
	}
	history, err := store.GetHistoryLimit("run", 1)
	if err != nil {
		t.Fatal(err)
	}
	record := history[0].(IntervalSummaryRecord)
	if record.NativeEvidence == nil || record.NativeEvidence.Dimensions["action"]["Checkout"].Errors != 1 || record.NativeEvidence.Dimensions["action"]["Checkout"].P95 != 90 || record.NativeEvidence.Dimensions["action"]["Checkout"].DurationDigest.Count != 7 {
		t.Fatalf("lost evidence: %+v", record)
	}
	clone := CloneNativeIntervalEvidence(evidence)
	delete(clone.Dimensions["action"], "Checkout")
	if len(evidence.Dimensions["action"]) != 1 {
		t.Fatal("clone mutated original")
	}
}
func TestNativeEvidenceQueryValidation(t *testing.T) {
	base := AnalyticsQuery{EvidenceMode: "live", Population: "action", Metric: "response_time", Aggregation: "p95", Dimensions: []string{"action"}, MaxSeries: 10, EndSeconds: 60, BucketSeconds: 10}
	for _, tc := range []struct {
		name   string
		mutate func(*AnalyticsQuery)
		valid  bool
	}{
		{"v3 hierarchy", func(q *AnalyticsQuery) { q.LiveBucketMode = "merged"; q.Dimensions = []string{"hierarchy"} }, true},
		{"hierarchy source unavailable", func(q *AnalyticsQuery) { q.Dimensions = []string{"hierarchy"} }, false},
		{"v3 phase transaction unavailable", func(q *AnalyticsQuery) {
			q.LiveBucketMode = "merged"
			q.Population = "transaction"
			q.Dimensions = []string{"phase"}
		}, false},
		{"live grouped", func(q *AnalyticsQuery) {}, true},
		{"merged intersection", func(q *AnalyticsQuery) {
			q.LiveBucketMode = "merged"
			q.PropertyName = "kind"
			q.PropertyValue = "paid"
		}, true},
		{"unknown bucket mode", func(q *AnalyticsQuery) { q.LiveBucketMode = "other" }, false},
		{"archive rejects live bucket mode", func(q *AnalyticsQuery) { q.EvidenceMode = "archive"; q.LiveBucketMode = "merged" }, false},
		{"live intersection", func(q *AnalyticsQuery) { q.PropertyName = "kind"; q.PropertyValue = "paid" }, false},
		{"archive intersection", func(q *AnalyticsQuery) { q.EvidenceMode = "archive"; q.PropertyName = "kind"; q.PropertyValue = "paid" }, true},
		{"series bound", func(q *AnalyticsQuery) { q.MaxSeries = 21 }, false},
		{"wrong live region population", func(q *AnalyticsQuery) { q.Dimensions = []string{"region"} }, false},
		{"ambiguous overlay", func(q *AnalyticsQuery) {
			q.OverlaySelection = &NativeOverlaySelection{DatasetID: "d", SourceID: "s", Metric: "cpu", Aggregation: "avg"}
		}, false},
		{"monitor overlay", func(q *AnalyticsQuery) {
			q.OverlaySelection = &NativeOverlaySelection{SourceID: "s", Metric: "cpu", Aggregation: "avg"}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			tc.mutate(&q)
			err := ValidateNativeEvidenceQuery(q)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestNativeEvidenceUpgradesPriorSchemaWithoutLosingHistory(t *testing.T) {
	path := t.TempDir() + "/upgrade.db"
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "legacy", Status: "COMPLETE", StartTime: at}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory("run", at, map[string]any{"timestamp": at, "total_transactions": 7}); err != nil {
		t.Fatal(err)
	}
	// Recreate the immediately preceding schema and its immutable ledger.
	if _, err := store.db.Exec("ALTER TABLE test_interval_summaries DROP COLUMN native_evidence_json"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM schema_migrations WHERE version >= 24"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.GetHistoryLimit("run", 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("legacy history: %v %v", history, err)
	}
	legacy := history[0].(IntervalSummaryRecord)
	if legacy.TotalTx != 7 || legacy.NativeEvidence != nil {
		t.Fatalf("changed legacy evidence: %+v", legacy)
	}
	next := at.Add(time.Second)
	evidence := &NativeIntervalEvidence{Version: 1, WindowSeconds: 1, Dimensions: map[string]map[string]NativeIntervalMetric{"action": {"Checkout": {Count: 2, P95: 50}}}}
	if err := store.SaveHistory("run", next, map[string]any{"timestamp": next, "native_evidence": evidence}); err != nil {
		t.Fatal(err)
	}
	history, err = store.GetHistoryLimit("run", 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("new history: %v %v", history, err)
	}
	if got := history[0].(IntervalSummaryRecord).NativeEvidence; got == nil || got.Dimensions["action"]["Checkout"].P95 != 50 {
		t.Fatalf("upgraded evidence: %+v", got)
	}
}

// Evidence queries execute over action and transaction observations only:
// message and browser document populations belong to archive panels, where
// the evidence executors would answer them with an empty series.
func TestNativeEvidenceQueryRefusesPopulationsItCannotRead(t *testing.T) {
	for _, q := range []AnalyticsQuery{
		{EvidenceMode: "archive", Population: "message", Metric: "message_count", Aggregation: "count", BucketSeconds: 10, EndSeconds: 60, MaxSeries: 1},
		{EvidenceMode: "archive", Population: "browser_document", Metric: "document_lcp", Aggregation: "p75", BucketSeconds: 10, EndSeconds: 60, MaxSeries: 1, Dimensions: []string{"action"}},
	} {
		if err := ValidateNativeEvidenceQuery(q); err == nil {
			t.Fatalf("evidence query over %s accepted", q.Population)
		}
	}
}

package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLoadStoredTestHistoryRetainsTransactionsAfterStoreReopen(t *testing.T) {
	timestamp := time.Date(2026, time.August, 12, 14, 30, 0, 0, time.UTC)
	databasePath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := storage.NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "running-test", Name: "Running", Status: "RUNNING", StartTime: timestamp}); err != nil {
		store.Close()
		t.Fatalf("SaveTest returned error: %v", err)
	}
	if err := store.SaveHistory("running-test", timestamp, &LiveMetrics{
		Timestamp: timestamp,
		Transactions: map[string]*TxMetrics{
			"checkout": {
				Name:   "checkout",
				TPS:    12.75,
				Count:  51,
				Errors: 2,
				AvgMs:  243.5,
			},
		},
	}); err != nil {
		store.Close()
		t.Fatalf("SaveHistory returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	reopened, err := storage.NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("reopen SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	runner := &TestRunner{historyStore: reopened}
	testRun := &ManagedTest{ID: "running-test", Status: "RUNNING"}

	runner.loadStoredTestHistory(testRun)

	if len(testRun.History) != 1 || testRun.LiveMetrics != testRun.History[0] {
		t.Fatalf("reloaded history = %#v, live = %#v; want one current point", testRun.History, testRun.LiveMetrics)
	}
	if !testRun.LiveMetrics.Timestamp.Equal(timestamp) {
		t.Fatalf("reloaded timestamp = %s, want %s", testRun.LiveMetrics.Timestamp, timestamp)
	}
	checkout := testRun.LiveMetrics.Transactions["checkout"]
	if checkout == nil || checkout.Name != "checkout" || checkout.TPS != 12.75 || checkout.Count != 51 || checkout.Errors != 2 || checkout.AvgMs != 243.5 {
		t.Fatalf("reloaded checkout transaction = %#v", checkout)
	}
}

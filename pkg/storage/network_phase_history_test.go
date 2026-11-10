package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// networkPhaseHistorySample is a live interval as the controller marshals it:
// the fleet phase averages, the server/no-response split and the phase
// observation counts. Zero averages are omitted, as LiveMetrics omits them.
func networkPhaseHistorySample(at time.Time, dns float64) map[string]any {
	sample := map[string]any{
		"timestamp":          at,
		"avg_response_ms":    120.0,
		"avg_connect_ms":     4.5,
		"avg_tls_ms":         9.25,
		"avg_ttfb_ms":        80.0,
		"avg_wait_ms":        30.0,
		"avg_server_ms":      62.0,
		"avg_no_response_ms": 6.0,
		"phase_observed":     json.RawMessage(`{"dns":3,"connect":4,"tls":4,"no_ttfb":1,"no_ttfb_setup_ms":2.5,"no_ttfb_remainder_ms":60}`),
	}
	if dns > 0 {
		sample["avg_dns_ms"] = dns
	}
	return sample
}

// exerciseNetworkPhaseHistory saves a legacy row with no phase data and two
// rows with phases, and checks the phases survive the round trip while the
// legacy row reads back as unknown.
func exerciseNetworkPhaseHistory(t *testing.T, store *SQLiteStore, now time.Time) {
	t.Helper()
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "run", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory("run", now, map[string]any{"timestamp": now, "avg_response_ms": 100.0}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory("run", now.Add(time.Second), networkPhaseHistorySample(now.Add(time.Second), 1.75)); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory("run", now.Add(2*time.Second), networkPhaseHistorySample(now.Add(2*time.Second), 0)); err != nil {
		t.Fatal(err)
	}
	history, err := store.GetHistory("run")
	if err != nil || len(history) != 3 {
		t.Fatalf("GetHistory: %+v, err=%v", history, err)
	}
	if legacy := history[0].(IntervalSummaryRecord); legacy.NetworkPhases != nil {
		t.Fatalf("a row without phases must read back as unknown, got %+v", legacy.NetworkPhases)
	}
	for index, dns := range map[int]float64{1: 1.75, 2: 0} {
		got := history[index].(IntervalSummaryRecord).NetworkPhases
		if got == nil {
			t.Fatalf("row %d lost its network phases", index)
		}
		if got.AvgDNSMs != dns || got.AvgConnectMs != 4.5 || got.AvgTLSMs != 9.25 || got.AvgTTFBMs != 80 || got.AvgWaitMs != 30 {
			t.Fatalf("row %d phase averages = %+v", index, got)
		}
		if got.AvgServerMs == nil || *got.AvgServerMs != 62 || got.AvgNoResponseMs == nil || *got.AvgNoResponseMs != 6 {
			t.Fatalf("row %d server/no-response split = %v / %v", index, got.AvgServerMs, got.AvgNoResponseMs)
		}
		var observed map[string]float64
		if err := json.Unmarshal(got.PhaseObserved, &observed); err != nil {
			t.Fatalf("row %d phase_observed %q: %v", index, got.PhaseObserved, err)
		}
		if observed["dns"] != 3 || observed["connect"] != 4 || observed["tls"] != 4 || observed["no_ttfb"] != 1 || observed["no_ttfb_setup_ms"] != 2.5 || observed["no_ttfb_remainder_ms"] != 60 {
			t.Fatalf("row %d phase_observed = %+v", index, observed)
		}
	}
	limited, err := store.GetHistoryLimit("run", 1)
	if err != nil || len(limited) != 1 || limited[0].(IntervalSummaryRecord).NetworkPhases == nil {
		t.Fatalf("GetHistoryLimit lost the phases: %+v, err=%v", limited, err)
	}
}

func TestSQLiteNetworkPhaseHistoryRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "phases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exerciseNetworkPhaseHistory(t, store, time.Now().UTC().Truncate(time.Millisecond))
}

// A database from before the column existed gains it on open, and its rows
// read back with unknown phases.
func TestSQLiteNetworkPhaseHistoryMigratesLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err = store.SaveTest(&TestRecord{ID: "old", Name: "old", Status: "COMPLETED", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveHistory("old", now, networkPhaseHistorySample(now, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec("ALTER TABLE test_interval_summaries DROP COLUMN network_phases_json"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, currentSchemaRelease().Version); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.GetHistory("old")
	if err != nil || len(history) != 1 {
		t.Fatalf("GetHistory: %+v, err=%v", history, err)
	}
	if phases := history[0].(IntervalSummaryRecord).NetworkPhases; phases != nil {
		t.Fatalf("a migrated legacy row must read back as unknown, got %+v", phases)
	}
	if err = store.SaveHistory("old", now.Add(time.Second), networkPhaseHistorySample(now.Add(time.Second), 1)); err != nil {
		t.Fatal(err)
	}
	history, err = store.GetHistory("old")
	if err != nil || len(history) != 2 || history[1].(IntervalSummaryRecord).NetworkPhases == nil {
		t.Fatalf("migrated store did not keep new phases: %+v, err=%v", history, err)
	}
}

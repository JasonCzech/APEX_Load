package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func historyMessagingFixture(t *testing.T, now time.Time) (map[string]*protocol.MessagingSummary, *protocol.MessagingProgressSnapshot) {
	t.Helper()
	var summary *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&summary, &protocol.ServerTelemetry{MessagesSent: 2, PartitionID: "0"}, "hub", "eventhubs_hub", "processors")
	p := &protocol.MessagingProgressSnapshot{Version: 1, CapturedAt: now, Lifecycle: "running", Rows: []protocol.MessagingProgressRow{{Protocol: "eventhubs", Mode: "processor", Group: "processors", Origin: "trusted", SourceID: "id", Source: "hub", Subscription: "load", SessionID: "session", Partition: "0", Generation: "generation", Revision: "1", CaptureRevision: "1", UpdatedAt: now, CapturedAt: now, ExpectedPartitions: 1, Ownership: "unverified", Delivered: 1, DeliveredPosition: protocol.MessagingProgressPosition{State: "observed", Sequence: "9007199254740993", ProviderOffset: "offset:opaque", ObservedAt: now, ReceivedAt: now}, CheckpointPosition: protocol.MessagingProgressPosition{State: "missing"}, TailPosition: protocol.MessagingProgressPosition{State: "disabled"}, Checkpoint: protocol.MessagingCheckpointProgress{Duration: protocol.MessagingDurationSummary{Metric: "checkpoint", Population: "checkpoint_operation"}}}}}
	if !p.Valid() || !summary.Valid() {
		t.Fatal("invalid history fixture")
	}
	return map[string]*protocol.MessagingSummary{"send": summary}, p
}

func assertMessagingHistory(t *testing.T, store *SQLiteStore, now time.Time) {
	t.Helper()
	messaging, progress := historyMessagingFixture(t, now)
	if err := store.SaveHistory("run", now, map[string]any{"timestamp": now, "messaging": messaging, "messaging_progress": progress}); err != nil {
		t.Fatal(err)
	}
	// No action/transaction count is required for standalone progress.
	history, err := store.GetHistoryLimit("run", 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("history %v: %v", history, err)
	}
	got := history[0].(IntervalSummaryRecord)
	if got.TotalTx != 0 || got.Messaging["send"].Dimensions[0].Sent != 2 || got.MessagingProgress == nil || got.MessagingProgress.Rows[0].DeliveredPosition.Sequence != "9007199254740993" || got.MessagingProgress.Rows[0].Origin != "trusted" {
		t.Fatalf("lost evidence: %+v", got)
	}
	got.MessagingProgress.Rows[0].Delivered = 9
	again, err := store.GetHistory("run")
	if err != nil {
		t.Fatal(err)
	}
	if again[len(again)-1].(IntervalSummaryRecord).MessagingProgress.Rows[0].Delivered != 1 {
		t.Fatal("history reload aliases prior read")
	}
}

func TestMessagingHistorySQLiteMigrationRestartAndRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err = store.SaveTest(&TestRecord{ID: "run", Name: "run", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveHistory("run", now.Add(-time.Second), map[string]any{"timestamp": now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"messaging_json", "messaging_progress_json"} {
		if _, err = store.db.Exec("ALTER TABLE test_interval_summaries DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
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
	assertMessagingHistory(t, store, now)
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.GetHistory("run")
	if err != nil || len(history) != 2 {
		t.Fatalf("history %+v: %v", history, err)
	}
	legacy := history[0].(IntervalSummaryRecord)
	current := history[1].(IntervalSummaryRecord)
	if legacy.Messaging != nil || legacy.MessagingProgress != nil {
		t.Fatal("migrated absence invented observations")
	}
	if current.MessagingProgress == nil || current.Messaging["send"] == nil {
		t.Fatal("restart erased side-channel evidence")
	}
}

func TestMessagingHistoryMalformedAndBoundedEvidence(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err = store.SaveTest(&TestRecord{ID: "run", Name: "run", Status: "RUNNING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, messaging, progress string
		absent                    bool
	}{
		{"legacy", "", "", true}, {"null", "null", "null", true},
		{"malformed", "{", "{", false},
		{"invalid", "{\"send\":{\"dimensions\":[{\"operations\":-1}]}}", `{"version":1,"rows":[]}`, false},
		{"oversized", strings.Repeat("x", maxMessagingHistoryBytes+1), strings.Repeat("x", protocol.MaxMessagingProgressBytes+1), false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at := now.Add(time.Duration(i) * time.Second)
			if err := store.SaveHistory("run", at, map[string]any{"timestamp": at}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`UPDATE test_interval_summaries SET messaging_json = ?, messaging_progress_json = ? WHERE test_id = ? AND timestamp = ?`, tc.messaging, tc.progress, "run", at); err != nil {
				t.Fatal(err)
			}
			history, err := store.GetHistoryLimit("run", 1)
			if err != nil {
				t.Fatal(err)
			}
			got := history[0].(IntervalSummaryRecord)
			if tc.absent {
				if got.Messaging != nil || got.MessagingProgress != nil {
					t.Fatal("absence became observed")
				}
				return
			}
			if len(got.Messaging) == 0 || got.MessagingProgress == nil || !got.MessagingProgress.Incomplete || len(got.MessagingProgress.Rows) > 0 {
				t.Fatal("invalid evidence did not remain unavailable")
			}
			for _, s := range got.Messaging {
				if !s.Incomplete {
					t.Fatal("invalid summary became complete")
				}
			}
		})
	}
	// Invalid input uses typed history with an unavailable marker, never the
	// legacy fallback which would bypass bounded side-channel decoding.
	if err = store.SaveHistory("run", now.Add(10*time.Second), map[string]any{"messaging_progress": map[string]any{"version": 1, "rows": nil}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM test_history WHERE test_id = ?`, "run").Scan(&count); err != nil || count != 0 {
		t.Fatalf("malformed evidence bypassed typed history: %d %v", count, err)
	}
	messaging, _ := historyMessagingFixture(t, now)
	var many = map[string]*protocol.MessagingSummary{}
	for i := 0; i < 200; i++ {
		many[strings.Repeat("a", i+1)] = messaging["send"]
	}
	raw := nullableMessagingJSON(many).(string)
	if len(raw) > maxMessagingHistoryBytes {
		t.Fatal("history codec exceeded bound")
	}
	var restored map[string]*protocol.MessagingSummary
	if err = json.Unmarshal([]byte(raw), &restored); err != nil {
		t.Fatal(err)
	}
	if restored["__other__"] == nil || !restored["__other__"].Incomplete {
		t.Fatal("overflow lost qualification")
	}
}

package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func historyJavaHTTPFixture() *protocol.JavaHTTPActions {
	var d *protocol.JavaHTTPSummary
	zero := int64(0)
	protocol.MergeJavaHTTPDiagnostic(&d, &protocol.JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "empty", RequestDecodedBytes: &zero, ResponseState: "unavailable", EnclosingMs: 1, ObjectFootprint: "unavailable_embedded_opaque_bytes"})
	var out *protocol.JavaHTTPActions
	protocol.AddJavaHTTPAction(&out, "request", 1, d)
	return out
}
func assertJavaHTTPHistory(t *testing.T, store *SQLiteStore, now time.Time) {
	t.Helper()
	if err := store.SaveHistory("run", now, map[string]any{"timestamp": now, "java_http": historyJavaHTTPFixture()}); err != nil {
		t.Fatal(err)
	}
	history, err := store.GetHistoryLimit("run", 1)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	got := history[0].(IntervalSummaryRecord)
	if got.TotalTx != 0 || got.JavaHTTP == nil || got.JavaHTTP.Incomplete || got.JavaHTTP.Actions["request"].Diagnostics.Samples != 1 || got.JavaHTTP.Actions["request"].Population != 1 {
		t.Fatal("lost action evidence", got.JavaHTTP)
	}
	got.JavaHTTP.Actions["request"].Diagnostics.Samples = 99
	history, err = store.GetHistoryLimit("run", 1)
	if err != nil || history[0].(IntervalSummaryRecord).JavaHTTP.Actions["request"].Diagnostics.Samples != 1 {
		t.Fatal("read alias", err)
	}
}
func TestJavaHTTPHistorySQLiteMigrationRestartAndRoundtrip(t *testing.T) {
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
	if _, err = store.db.Exec("ALTER TABLE test_interval_summaries DROP COLUMN java_http_json"); err != nil {
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
	assertJavaHTTPHistory(t, store, now)
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
		t.Fatal(history, err)
	}
	if history[0].(IntervalSummaryRecord).JavaHTTP != nil || history[1].(IntervalSummaryRecord).JavaHTTP == nil {
		t.Fatal("legacy absence or restart changed evidence")
	}
	for _, raw := range []string{`{"version":99}`, `{"version":1,"actions":{"a":{"population":-1}}}`, `invalid`} {
		if _, err = store.db.Exec(`UPDATE test_interval_summaries SET java_http_json = ? WHERE test_id = ?`, raw, "run"); err != nil {
			t.Fatal(err)
		}
		rows, err := store.GetHistory("run")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			v := row.(IntervalSummaryRecord).JavaHTTP
			if v == nil || !v.Incomplete || v.UnavailableReason == "" || len(v.Actions) != 0 {
				t.Fatal("malformed evidence promoted", v)
			}
		}
	}
}

package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestDiagnosticRuntimeOverridePreservesFalseAndIsolation(t *testing.T) {
	off := false
	source := &RuntimeSettingsOverride{Results: &dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled", SampleRate: 1, IncludeResources: &off}}}
	projected := projectRuntimeFields(source, []string{"results"})
	if projected == nil || runtimeSettingsOverrideEmpty(projected) {
		t.Fatal("results override dropped")
	}
	script := &dsl.Script{Protocol: "http"}
	if err := applyRuntimeOverrides(script, projected); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(script.Config.Results)
	if !strings.Contains(string(raw), `"include_resources":false`) {
		t.Fatal("explicit false lost")
	}
	*script.Config.Results.MessageDetail.IncludeResources = true
	if *source.Results.MessageDetail.IncludeResources {
		t.Fatal("override aliases source")
	}
	if projectRuntimeFields(source, []string{"http"}) != nil {
		t.Fatal("unrequested results copied")
	}
}

func TestArchivedResourceSnapshotSanitizedAndDetached(t *testing.T) {
	snapshot := &protocol.ErrorSnapshot{Resources: []protocol.ResourceSnapshot{{RequestURL: "https://example.test/?token=secret", RequestHeaders: map[string]string{"Authorization": "Bearer secret"}, ResponseHeaders: map[string]string{"Set-Cookie": "session=secret"}, ResponseBody: `{"password":"secret"}`, ResponseBodyState: "captured"}}}
	safe := sanitizeArchivedSnapshot(snapshot, scrub.Policy{})
	raw, _ := json.Marshal(safe)
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("archive leak: %s", raw)
	}
	if safe.Resources[0].ResponseBodyState != "redacted" {
		t.Fatal("redaction state lost")
	}
	clone := cloneProtocolErrorSnapshot(safe)
	clone.Resources[0].RequestHeaders["Authorization"] = "changed"
	if safe.Resources[0].RequestHeaders["Authorization"] == "changed" {
		t.Fatal("snapshot clone aliases resources")
	}
	if snapshot.Resources[0].ResponseBody != `{"password":"secret"}` {
		t.Fatal("archive projection mutated original")
	}
}

func TestResourceEvidenceArchivePersistenceRoundTrip(t *testing.T) {
	snapshot := &protocol.ErrorSnapshot{RequestURL: "https://example.test/page", Resources: []protocol.ResourceSnapshot{{RequestURL: "https://example.test/resource", ResponseStatus: 200, ResponseBody: "retained resource", ResponseBodyState: "captured", CacheHit: true}}, OmittedResources: 2}
	record := writeArchiveTransactionsForTest(t, []TransactionEvent{{ActionName: "page", Success: true, Snapshot: snapshot}})
	events, total, err := loadArchiveTransactions(record, transactionEventFilter{Action: "page"}, archivePage{Limit: 1})
	if err != nil || total != 1 || len(events) != 1 {
		t.Fatalf("archive: total=%d err=%v", total, err)
	}
	got := events[0].Snapshot
	if got == nil || len(got.Resources) != 1 || got.Resources[0].ResponseBody != "retained resource" || !got.Resources[0].CacheHit || got.OmittedResources != 2 {
		t.Fatalf("resources lost: %+v", got)
	}
}

func TestDiagnosticReasonSurvivesRunSourceProjection(t *testing.T) {
	script := &dsl.Script{Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
		"runtime_files":                map[string]any{"default.cfg": map[string]any{"WEB": map[string]any{"DisableAED": "0"}}},
		"runtime_setting_dispositions": map[string]any{"default.cfg": map[string]any{"WEB": map[string]any{"DisableAED": map[string]any{"status": "compatibility_only", "reason": "unverified_semantics", "note": "Review required"}}}},
	}}}}
	source := projectTestGroupImportedRuntimeSource(script)
	if source == nil || source.Dispositions["default.cfg"]["WEB"]["DisableAED"].Reason != "unverified_semantics" {
		t.Fatalf("reason lost: %+v", source)
	}
}

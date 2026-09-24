package api

import (
	"context"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/scrub"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestCSVExportDownloadFilenameEncoding(t *testing.T) {
	for _, name := range []string{`trace"quoted.csv`, "tracé-results.csv", "traceId-results.csv", "traceId-results.CSV"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			server := NewServer(ServerConfig{})
			run := &ManagedTest{ID: "filename-review", Name: "CSV", Status: "COMPLETE"}
			declaration := RunArchiveCSVExport{Group: "users", Export: dsl.CSVExport{ID: "trace", FileName: name, ActionName: "sample", Columns: []dsl.CSVExportColumn{{Name: "latencyMs", Metric: "latency_ms"}}}}
			writer, err := newRunArchiveWriter(run, RunArchiveProvenance{CSVExports: []RunArchiveCSVExport{declaration}})
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.RecordResult(&protocol.Result{Group: "users", ActionName: "sample", Success: true}); err != nil {
				t.Fatal(err)
			}
			record, err := writer.Finalize("COMPLETE", time.Now(), "", false, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			server.runner.tests[run.ID] = run
			server.runner.archives[run.ID] = record
			request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+run.ID+"/archive/files/"+url.PathEscape(name), nil)
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			kind, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
			if err != nil || kind != "attachment" || params["filename"] != name {
				t.Fatalf("invalid filename disposition %q: %v", response.Header().Get("Content-Disposition"), err)
			}
		})
	}
}

func TestCSVExportIdentitySurvivesRedactedNameCollision(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	target := "sample token=first"
	other := "sample token=second"
	if scrub.ValueLabel(target, scrub.Policy{}) != scrub.ValueLabel(other, scrub.Policy{}) {
		t.Fatal("test requires a redacted-name collision")
	}
	declaration := RunArchiveCSVExport{Group: "users", Export: dsl.CSVExport{ID: "trace", FileName: "trace.csv", ActionName: target, Columns: []dsl.CSVExportColumn{{Name: "traceId", Extraction: "id"}}}}
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "identity-review"}, RunArchiveProvenance{CSVExports: []RunArchiveCSVExport{declaration}})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*protocol.Result{
		{Group: "users", ActionName: target, Extractions: map[string]string{"id": "included"}},
		{Group: "users", ActionName: other, Extractions: map[string]string{"id": "excluded"}},
		{Group: "users", ActionName: "custom-redacted", CSVExportIDs: []string{"trace"}, Extractions: map[string]string{"id": "engine-bound"}},
		{Group: "users", ActionName: scrub.ValueLabel(target, scrub.Policy{}), CSVExportIDs: []string{}, Extractions: map[string]string{"id": "unbound"}},
	} {
		if err := writer.RecordResult(result); err != nil {
			t.Fatal(err)
		}
	}
	record, err := writer.Finalize("COMPLETE", time.Now(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "trace.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "traceId\nincluded\nengine-bound\n" {
		t.Fatalf("CSV=%s", data)
	}
}

func TestCSVExportSensitiveFallbackIsRedacted(t *testing.T) {
	root := t.TempDir()
	fallback := "raw-fallback-credential"
	declaration := RunArchiveCSVExport{Group: "users", Export: dsl.CSVExport{ID: "secret", ActionName: "sample", Columns: []dsl.CSVExportColumn{{Name: "value", Extraction: "client_secret", DefaultValue: &fallback}}}}
	data, _ := json.Marshal(TransactionEvent{Group: "users", ActionName: "sample"})
	if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	var csv strings.Builder
	if _, err := writeDeclaredCSVExport(context.Background(), root, &RunArchiveManifest{TransactionsFile: "transactions.jsonl", TransactionCount: 1}, declaration, &csv); err != nil {
		t.Fatal(err)
	}
	if csv.String() != "value\n"+scrub.Redacted+"\n" {
		t.Fatalf("CSV exposed fallback: %s", csv.String())
	}
	run := &ManagedTest{groupSpecs: []engine.GroupConfig{{Name: "users", Script: &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{CSVExports: []dsl.CSVExport{declaration.Export}}}}}}}
	declarations := declaredCSVExports(run)
	if got := *declarations[0].Export.Columns[0].DefaultValue; got != scrub.Redacted {
		t.Fatalf("provenance fallback=%s", got)
	}
}

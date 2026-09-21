package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDecodeELKQueryResultRedactsAndBoundsSamples(t *testing.T) {
	cfg := &ELKCaptureConfig{TimestampField: "@timestamp", LevelField: "log.level", MessageField: "message", TestIDField: "labels.test_id", TraceIDField: "trace.id",
		MetricAggregations: []storage.ELKMetricAggregation{{Name: "latency_avg", Field: "duration_ms", Operation: "avg"}}}
	payload := `{"hits":{"total":{"value":12},"hits":[{"_id":"one","_source":{"@timestamp":"2026-09-20T12:00:00Z","log":{"level":"error"},"message":"Authorization: Bearer secret-token","labels":{"test_id":"run-1"},"trace":{"id":"trace-1"}}}]},"aggregations":{"errors_total":{"doc_count":3},"metric_latency_avg":{"value":42.5}}}`
	result, err := decodeELKQueryResult([]byte(payload), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics["logs_total"] != 12 || result.Metrics["errors_total"] != 3 || result.Metrics["latency_avg"] != 42.5 {
		t.Fatalf("metrics = %#v", result.Metrics)
	}
	if len(result.Samples) != 1 || strings.Contains(result.Samples[0].Message, "secret-token") || result.Samples[0].TraceID != "trace-1" {
		t.Fatalf("samples = %#v", result.Samples)
	}
}

func TestDecodeELKRejectsIncompleteSearchResults(t *testing.T) {
	for name, payload := range map[string]string{
		"timeout":           `{"timed_out":true,"hits":{"total":1}}`,
		"shard failure":     `{"_shards":{"failed":1},"hits":{"total":1}}`,
		"early termination": `{"terminated_early":true,"hits":{"total":1}}`,
		"lower bound":       `{"hits":{"total":{"value":10000,"relation":"gte"}}}`,
		"missing hits":      `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeELKQueryResult([]byte(payload), &ELKCaptureConfig{}); err == nil {
				t.Fatal("incomplete response accepted as a healthy capture")
			}
		})
	}
}

func TestSanitizeELKSampleRedactsAndBoundsEveryString(t *testing.T) {
	sample := sanitizeELKSample(ELKLogSample{
		ID:      "Authorization: ApiKey private-key",
		Level:   strings.Repeat("error", 100),
		Message: strings.Repeat("界", 2000),
		TestID:  "password=hunter2",
		TraceID: "Authorization: Bearer private-token",
	})
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-key", "hunter2", "private-token"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sanitized sample disclosed %q: %s", secret, encoded)
		}
	}
	if len(sample.ID) > maxELKSampleIDBytes || len(sample.Level) > maxELKSampleLevelBytes || len(sample.Message) > maxELKSampleTextBytes || len(sample.TestID) > maxELKCorrelationBytes || len(sample.TraceID) > maxELKCorrelationBytes {
		t.Fatalf("sample exceeded bounds: %#v", sample)
	}
	if !utf8.ValidString(sample.Message) {
		t.Fatalf("bounded message is not valid UTF-8: %q", sample.Message)
	}
}

func TestELKStringFieldSupportsLiteralAndNestedDots(t *testing.T) {
	source := map[string]any{
		"trace.id": "literal-trace",
		"trace":    map[string]any{"id": "nested-trace"},
		"labels":   map[string]any{"test": "nested-test"},
	}
	if got := elkStringField(source, "trace.id"); got != "literal-trace" {
		t.Fatalf("literal dotted field = %q", got)
	}
	if got := elkStringField(source, "labels.test"); got != "nested-test" {
		t.Fatalf("nested dotted field = %q", got)
	}
}

func TestELKTraceEvidenceSinkDoesNotWaitForPollSerialization(t *testing.T) {
	artifacts := &runArtifacts{test: &ManagedTest{ELKCapture: &ELKCaptureConfig{ConnectionID: "elk-a"}}}
	artifacts.elkPollMu.Lock()
	defer artifacts.elkPollMu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- (elkTraceEvidenceSink{artifacts: artifacts}).RecordResult(&protocol.Result{CorrelationID: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("VUser trace evidence blocked behind ELK polling")
	}
	if _, ok := artifacts.elkTraceIDs["4bf92f3577b34da6a3ce929d0e0e4736"]; !ok {
		t.Fatalf("trace IDs = %#v", artifacts.elkTraceIDs)
	}
}

func TestBuildELKSearchBodyUsesTestThenTraceCorrelation(t *testing.T) {
	start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	cfg := &ELKCaptureConfig{TimestampField: "@timestamp", TestIDField: "labels.test", TraceIDField: "trace.id", BaseFilter: "service.name:checkout", ErrorFilter: "log.level:error"}
	withTest, _ := json.Marshal(buildELKSearchBody(cfg, "run-1", []string{"trace-1"}, start, start.Add(time.Minute)))
	if !strings.Contains(string(withTest), `"labels.test":"run-1"`) || strings.Contains(string(withTest), `"trace.id":[`) {
		t.Fatalf("test correlation body = %s", withTest)
	}
	cfg.TestIDField = ""
	withTraces, _ := json.Marshal(buildELKSearchBody(cfg, "run-1", []string{"trace-1", "trace-2"}, start, start.Add(time.Minute)))
	if !strings.Contains(string(withTraces), `"trace.id":["trace-1","trace-2"]`) || !strings.Contains(string(withTraces), `service.name:checkout`) {
		t.Fatalf("trace correlation body = %s", withTraces)
	}
}

func TestApplyELKAuthenticationUsesProjectSecretValues(t *testing.T) {
	resolver := engine.SecretResolverFunc(func(name string) (string, bool) {
		return map[string]string{"api": "encoded-key", "user": "elastic-user", "pass": "elastic-pass"}[name], true
	})
	apiKeyRequest, _ := http.NewRequest(http.MethodPost, "https://elastic.example.com/_search", nil)
	if err := applyELKAuthentication(t.Context(), apiKeyRequest, resolver, &ELKCaptureConfig{AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "api"}); err != nil {
		t.Fatal(err)
	}
	if got := apiKeyRequest.Header.Get("Authorization"); got != "ApiKey encoded-key" {
		t.Fatalf("API key authorization = %q", got)
	}
	basicRequest, _ := http.NewRequest(http.MethodPost, "https://elastic.example.com/_search", nil)
	if err := applyELKAuthentication(t.Context(), basicRequest, resolver, &ELKCaptureConfig{AuthType: storage.ELKAuthBasic, UsernameSecretName: "user", PasswordSecretName: "pass"}); err != nil {
		t.Fatal(err)
	}
	username, password, ok := basicRequest.BasicAuth()
	if !ok || username != "elastic-user" || password != "elastic-pass" {
		t.Fatalf("basic auth = %q, %q, %t", username, password, ok)
	}
}

func TestQueryELKUsesSearchAPIAuthenticationAndDecodesResults(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/_search") {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "ApiKey apexload-dev-secret" {
			t.Errorf("authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		encoded, _ := json.Marshal(body)
		if !strings.Contains(string(encoded), `"labels.test":"run-1"`) || !strings.Contains(string(encoded), start.Format(time.RFC3339Nano)) {
			t.Errorf("search body = %s", encoded)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":2},"hits":[{"_id":"one","_source":{"@timestamp":"2026-09-20T12:00:10Z","message":"request failed","trace.id":"trace-1"}}]},"aggregations":{"errors_total":{"doc_count":1},"metric_latency_avg":{"value":31.5}}}`))
	}))
	defer fixture.Close()

	cfg := &ELKCaptureConfig{Endpoint: fixture.URL, IndexPattern: "logs-*", TimestampField: "@timestamp", MessageField: "message", TestIDField: "labels.test", TraceIDField: "trace.id", ProjectID: "project-a", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "elk-api-key", MetricAggregations: []storage.ELKMetricAggregation{{Name: "latency_avg", Field: "duration_ms", Operation: "avg"}}}
	result, err := queryELKWithClient(t.Context(), NewTestRunner(1, NewHub(), nil), cfg, "run-1", nil, start, start.Add(time.Minute), fixture.Client())
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics["logs_total"] != 2 || result.Metrics["errors_total"] != 1 || result.Metrics["latency_avg"] != 31.5 {
		t.Fatalf("metrics = %#v", result.Metrics)
	}
	if len(result.Samples) != 1 || result.Samples[0].TraceID != "trace-1" {
		t.Fatalf("samples = %#v", result.Samples)
	}
}

func TestQueryELKRejectsCleartextEndpointAtRequestBoundary(t *testing.T) {
	cfg := &ELKCaptureConfig{Endpoint: "http://elastic.example.com", IndexPattern: "logs-*", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "elk-api-key"}
	_, err := queryELKWithClient(t.Context(), NewTestRunner(1, NewHub(), nil), cfg, "run-1", nil, time.Now().Add(-time.Minute), time.Now(), http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "invalid ELK endpoint") {
		t.Fatalf("cleartext endpoint error = %v", err)
	}
}

func TestDecodeELKQueryResultLeavesMissingDocumentIDEmpty(t *testing.T) {
	result, err := decodeELKQueryResult([]byte(`{"hits":{"total":1,"hits":[{"_source":{"message":"sample"}}]}}`), &ELKCaptureConfig{MessageField: "message"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Samples) != 1 || result.Samples[0].ID != "" {
		t.Fatalf("samples = %#v", result.Samples)
	}
}

func TestELKConnectionResponseNeverDisclosesSecretReferences(t *testing.T) {
	connection := &storage.ELKConnection{ID: "elk", ProjectID: "project", Name: "Elastic", APIKeySecretName: "highly-sensitive-ref", UsernameSecretName: "user-ref", PasswordSecretName: "password-ref"}
	encoded, err := json.Marshal(elkConnectionResponse(connection))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"highly-sensitive-ref", "user-ref", "password-ref"} {
		if strings.Contains(text, secret) {
			t.Fatalf("response disclosed %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, `"has_api_key":true`) {
		t.Fatalf("configured flag missing: %s", text)
	}
}

func TestRunArchiveWriterRecordsRedactedELKEvidence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	w, err := newRunArchiveWriter(&ManagedTest{ID: "elk-evidence", Name: "ELK evidence", ELKCapture: &ELKCaptureConfig{ConnectionID: "elk-a"}}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.RecordELKSamples([]ELKLogSample{{ID: "1", Timestamp: time.Now().UTC(), Level: "error", Message: "password=hunter2"}}); err != nil {
		t.Fatal(err)
	}
	record, err := w.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ELKEvidenceCount != 1 || manifest.ELKEvidenceFile == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, manifest.ELKEvidenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Fatalf("ELK evidence was not redacted: %s", data)
	}
	found := false
	for _, artifact := range manifest.Artifacts {
		if artifact.Name == manifest.ELKEvidenceFile && artifact.SHA256 != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ELK evidence missing from integrity inventory: %#v", manifest.Artifacts)
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ELKSamples) != 1 || strings.Contains(report.ELKSamples[0].Message, "hunter2") {
		t.Fatalf("archive report ELK samples = %#v", report.ELKSamples)
	}
}

func TestResolveELKCaptureRejectsCrossProjectAttachment(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/attachment.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	connection := &storage.ELKConnection{ID: "elk-a", ProjectID: "project-a", Name: "Elastic", Enabled: true, Endpoint: "https://elastic.example.com", IndexPattern: "logs-*", TimestampField: "@timestamp", MessageField: "message", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "elk-key"}
	if err := store.CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	if _, err := runner.resolveELKCaptureForTest("project-b", &ELKCaptureConfig{ConnectionID: connection.ID}); err == nil || !strings.Contains(err.Error(), "different project") {
		t.Fatalf("cross-project attachment error = %v", err)
	}
	capture, err := runner.resolveELKCaptureForTest("project-a", &ELKCaptureConfig{ConnectionID: connection.ID})
	if err != nil || capture == nil || capture.ConnectionName != "Elastic" || capture.APIKeySecretName != "elk-key" {
		t.Fatalf("same-project capture = %#v, %v", capture, err)
	}
}

func TestWriteDistributedELKEvidenceArchivesControllerCapture(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "resources.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := &runArtifacts{
		elkSamples: []ELKLogSample{{ID: "one", Message: "password=hunter2"}},
		elkStatus:  &ELKCaptureStatus{Metrics: map[string]float64{"logs_total": 7}},
	}
	test := &ManagedTest{ID: "distributed-elk", Name: "Distributed", ELKCapture: &ELKCaptureConfig{ConnectionID: "elk-a"}, elkArtifacts: artifacts}
	manifest := &RunArchiveManifest{ResourceSamplesFile: "resources.jsonl"}
	if err := writeDistributedELKEvidence(root, manifest, nil, test, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if manifest.ELKEvidenceCount != 1 || manifest.ResourceSampleCount != 1 {
		t.Fatalf("manifest counts = %#v", manifest)
	}
	evidence, err := os.ReadFile(filepath.Join(root, manifest.ELKEvidenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(evidence), "hunter2") {
		t.Fatalf("distributed evidence was not redacted: %s", evidence)
	}
	resources, err := os.ReadFile(filepath.Join(root, manifest.ResourceSamplesFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resources), `"elk.elk-a.logs_total":7`) {
		t.Fatalf("distributed ELK metrics missing: %s", resources)
	}
}

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newGrafanaDashboardAPIServer(t *testing.T) (*Server, string) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "dashboards-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "Project A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return NewServer(ServerConfig{Store: store}), "proj-a"
}

func dashboardAPIRequest(t *testing.T, server *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	return rec
}

const importFixtureDashboard = `{
	"title":"Infra","schemaVersion":39,
	"panels":[
		{"id":1,"type":"stat","title":"Up","targets":[{"expr":"apexload_up{project_id=\"$project_id\"}","refId":"A"}]},
		{"id":2,"type":"timeseries","title":"CPU","fieldConfig":{"defaults":{"unit":"percent"}},"targets":[{"expr":"cpu{project_id=\"$project_id\"}","refId":"A","legendFormat":"{{instance}}"}]},
		{"id":3,"type":"table","title":"Grid","targets":[{"expr":"up","refId":"A"}]}
	]
}`

func importGrafanaDashboard(t *testing.T, server *Server, projectID, content string) grafanaDashboardImportResponse {
	t.Helper()
	rec := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana",
		map[string]any{"project_id": projectID, "name": "Infra", "content": content})
	if rec.Code != http.StatusOK {
		t.Fatalf("import status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp grafanaDashboardImportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	return resp
}

func TestGrafanaImportTwoPhaseCommit(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)

	resp := importGrafanaDashboard(t, server, projectID, importFixtureDashboard)
	if resp.ImportSessionID == "" {
		t.Fatal("expected an import session id")
	}
	if len(resp.Dashboard.Panels) != 2 {
		t.Fatalf("imported panels = %d, want 2 (table excluded)", len(resp.Dashboard.Panels))
	}
	// The unsupported table panel is surfaced explicitly with its type, not dropped.
	var sawTableReview bool
	for _, section := range resp.ReviewSections {
		for _, item := range section.Items {
			if bytesContains(item, `"table"`) {
				sawTableReview = true
			}
		}
	}
	if !sawTableReview {
		t.Fatalf("expected the table panel surfaced as unsupported; sections=%v", resp.ReviewSections)
	}

	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID, "name": "Infra"})
	if commit.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", commit.Code, commit.Body.String())
	}
	var stored storage.GrafanaDashboard
	if err := json.Unmarshal(commit.Body.Bytes(), &stored); err != nil {
		t.Fatalf("decode commit: %v", err)
	}
	if !stored.Enabled || len(stored.Panels) != 2 {
		t.Fatalf("stored dashboard = %#v", stored)
	}
	if stored.CreatedBy == "" {
		t.Fatal("stored dashboard is missing creator provenance")
	}

	// It now appears in the project list.
	list := dashboardAPIRequest(t, server, http.MethodGet, "/api/v1/monitoring/dashboards?project_id="+projectID, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d", list.Code)
	}
	var listResp struct {
		Dashboards []storage.GrafanaDashboard `json:"dashboards"`
		Count      int                        `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 || listResp.Dashboards[0].ID != stored.ID {
		t.Fatalf("list = %#v, want the committed dashboard", listResp)
	}
}

func TestGrafanaImportCommitRejectsUnboundRequiredVariable(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	content := `{"title":"V","schemaVersion":39,
		"templating":{"list":[{"name":"env","type":"custom"}]},
		"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"up{project_id=\"$project_id\",env=\"$env\"}","refId":"A"}]}]}`
	resp := importGrafanaDashboard(t, server, projectID, content)
	if len(resp.RequiredBindings) != 1 || resp.RequiredBindings[0] != "env" {
		t.Fatalf("required bindings = %v, want [env]", resp.RequiredBindings)
	}
	// Commit without binding env -> 409 with the unbound-variables code.
	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID})
	if commit.Code != http.StatusConflict {
		t.Fatalf("commit status = %d body=%s, want 409", commit.Code, commit.Body.String())
	}
	if !bytesContains(commit.Body.String(), "grafana_import_unbound_variables") {
		t.Fatalf("commit body = %s, want unbound-variables code", commit.Body.String())
	}

	// Binding it lets the commit succeed.
	ok := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID, "bindings": map[string]string{"env": "prod"}})
	if ok.Code != http.StatusCreated {
		t.Fatalf("bound commit status = %d body=%s, want 201", ok.Code, ok.Body.String())
	}
}

func TestGrafanaImportCommitRejectsUnsafeBindingValue(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	content := `{"title":"V","schemaVersion":39,
		"templating":{"list":[{"name":"env","type":"custom"}]},
		"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"up{project_id=\"$project_id\",env=\"$env\"}","refId":"A"}]}]}`
	resp := importGrafanaDashboard(t, server, projectID, content)
	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID, "bindings": map[string]string{"env": `x"} or vector(1) #`}})
	if commit.Code != http.StatusBadRequest {
		t.Fatalf("commit status = %d, want 400 for injection binding value", commit.Code)
	}
}

func TestGrafanaImportCommitRejectsUndeclaredAndReservedBindings(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	resp := importGrafanaDashboard(t, server, projectID, importFixtureDashboard)
	for _, bindings := range []map[string]string{{"unused": "value"}, {"project_id": "other"}, {"__interval": "1h"}} {
		commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
			map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID, "bindings": bindings})
		if commit.Code != http.StatusBadRequest {
			t.Fatalf("bindings %#v status = %d body=%s, want 400", bindings, commit.Code, commit.Body.String())
		}
	}
}

func TestGrafanaImportPreservesUploadedNameAtCommit(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	review := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana",
		map[string]any{"project_id": projectID, "name": "uploaded-file-name", "content": importFixtureDashboard})
	if review.Code != http.StatusOK {
		t.Fatalf("review status = %d body=%s", review.Code, review.Body.String())
	}
	var resp grafanaDashboardImportResponse
	if err := json.Unmarshal(review.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID})
	if commit.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", commit.Code, commit.Body.String())
	}
	var stored storage.GrafanaDashboard
	if err := json.Unmarshal(commit.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Name != "uploaded-file-name" {
		t.Fatalf("stored name = %q, want uploaded-file-name", stored.Name)
	}
}

func TestGrafanaImportRequiresExplicitProjectAndPreservesExactSourceHash(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	missing := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana",
		map[string]any{"content": importFixtureDashboard})
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing project status = %d body=%s, want 400", missing.Code, missing.Body.String())
	}

	raw := " \n" + importFixtureDashboard + "\n\t"
	resp := importGrafanaDashboard(t, server, "  "+projectID+"  ", raw)
	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": "  " + projectID + "  ", "import_session_id": resp.ImportSessionID})
	if commit.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", commit.Code, commit.Body.String())
	}
	var stored storage.GrafanaDashboard
	if err := json.Unmarshal(commit.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(raw))
	wantHex := hex.EncodeToString(want[:])
	if stored.SourceSHA256 != wantHex {
		t.Fatalf("source hash = %q, want exact-upload hash %q", stored.SourceSHA256, wantHex)
	}
}

func TestGrafanaImportRejectsOversizedCommitName(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	resp := importGrafanaDashboard(t, server, projectID, importFixtureDashboard)
	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID, "name": strings.Repeat("n", storage.MaxGrafanaDashboardNameBytes+1)})
	if commit.Code != http.StatusBadRequest {
		t.Fatalf("commit status = %d body=%s, want 400", commit.Code, commit.Body.String())
	}
}

type failOnceGrafanaDashboardStore struct {
	*storage.SQLiteStore
	fail bool
}

type grafanaSnapshotReadErrorStore struct{ *storage.SQLiteStore }

func (s *grafanaSnapshotReadErrorStore) GetGrafanaDashboardSnapshot(string, string) (*storage.GrafanaDashboardSnapshot, error) {
	return nil, errors.New("injected snapshot read failure")
}

func (s *failOnceGrafanaDashboardStore) SaveGrafanaDashboard(dashboard *storage.GrafanaDashboard) error {
	if s.fail {
		s.fail = false
		return errors.New("injected save failure")
	}
	return s.SQLiteStore.SaveGrafanaDashboard(dashboard)
}

func TestGrafanaImportSaveFailureReleasesSessionForRetry(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	now := time.Now()
	if err := base.SaveProject(&storage.Project{ID: "proj-a", Name: "A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapped := &failOnceGrafanaDashboardStore{SQLiteStore: base, fail: true}
	server := NewServer(ServerConfig{Store: wrapped})
	resp := importGrafanaDashboard(t, server, "proj-a", importFixtureDashboard)
	body := map[string]any{"project_id": "proj-a", "import_session_id": resp.ImportSessionID}
	first := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit", body)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first commit = %d body=%s", first.Code, first.Body.String())
	}
	if strings.Contains(first.Body.String(), "injected save failure") {
		t.Fatalf("storage error leaked to client: %s", first.Body.String())
	}
	second := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit", body)
	if second.Code != http.StatusCreated {
		t.Fatalf("retry commit = %d body=%s", second.Code, second.Body.String())
	}
}

func TestTerminalGrafanaGraphsDoNotFallBackLiveOnSnapshotReadFailure(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "snapshot-read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	now := time.Now().UTC()
	if err := base.SaveProject(&storage.Project{ID: "proj-a", Name: "A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: &grafanaSnapshotReadErrorStore{SQLiteStore: base}})
	server.runner.mu.Lock()
	server.runner.tests["run-a"] = &ManagedTest{ID: "run-a", Name: "Run", Status: "COMPLETE", ProjectID: "proj-a", CreatedAt: now}
	server.runner.mu.Unlock()
	resp := dashboardAPIRequest(t, server, http.MethodGet, "/api/v1/monitoring/dashboards/graphs?project_id=proj-a&test_id=run-a", nil)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s, want 500", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "injected snapshot") {
		t.Fatalf("snapshot storage error leaked to client: %s", resp.Body.String())
	}
}

func TestTerminalGrafanaSnapshotIsFilteredToRequestedWindow(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	store := server.store.(*storage.SQLiteStore)
	start := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTest(&storage.TestRecord{ID: "run-filter", Name: "Run", Status: "COMPLETE", ProjectID: projectID, StartTime: start}); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests["run-filter"] = &ManagedTest{ID: "run-filter", Name: "Run", Status: "COMPLETE", ProjectID: projectID, CreatedAt: start}
	server.runner.mu.Unlock()
	points := []PrometheusPoint{
		{Timestamp: start, TimestampMs: start.UnixMilli(), Value: 1},
		{Timestamp: start.Add(time.Minute), TimestampMs: start.Add(time.Minute).UnixMilli(), Value: 2},
		{Timestamp: start.Add(2 * time.Minute), TimestampMs: start.Add(2 * time.Minute).UnixMilli(), Value: 3},
	}
	snapshotResponse := ImportedDashboardGraphsResponse{Dashboards: []DashboardPanelsResult{{
		DashboardID: "dash",
		Panels: []DashboardPanel{{
			PanelID: 1,
			Kind:    "line",
			Series:  []PrometheusSeries{{Values: points}},
		}},
	}}}
	payload, err := json.Marshal(snapshotResponse)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGrafanaDashboardSnapshot(&storage.GrafanaDashboardSnapshot{TestID: "run-filter", ProjectID: projectID, Payload: payload, CapturedAt: start.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"project_id":   {projectID},
		"test_id":      {"run-filter"},
		"start":        {start.Add(30 * time.Second).Format(time.RFC3339)},
		"end":          {start.Add(90 * time.Second).Format(time.RFC3339)},
		"step_seconds": {"15"},
	}
	resp := dashboardAPIRequest(t, server, http.MethodGet, "/api/v1/monitoring/dashboards/graphs?"+query.Encode(), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var got ImportedDashboardGraphsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	values := got.Dashboards[0].Panels[0].Series[0].Values
	if len(values) != 1 || values[0].Value != 2 {
		t.Fatalf("filtered snapshot values = %#v, want only middle point", values)
	}
}

func TestGrafanaImportFoldsIntervalVariableIntoBindings(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	// An interval variable used inside a query must resolve at run time; it is
	// auto-bound (no user input) to its selected value and folded into the stored
	// bindings so rate(x[$win]) does not fail with an unresolved token.
	content := `{"title":"V","schemaVersion":39,
		"templating":{"list":[{"name":"win","type":"interval","current":{"value":"5m"}}]},
		"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"rate(http_total{project_id=\"$project_id\"}[$win])","refId":"A"}]}]}`
	resp := importGrafanaDashboard(t, server, projectID, content)
	if len(resp.RequiredBindings) != 0 {
		t.Fatalf("interval var should not require a user binding; got %v", resp.RequiredBindings)
	}
	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID})
	if commit.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", commit.Code, commit.Body.String())
	}
	var stored storage.GrafanaDashboard
	_ = json.Unmarshal(commit.Body.Bytes(), &stored)
	if stored.Bindings["win"] != "5m" {
		t.Fatalf("stored bindings = %#v, want win=5m so $win resolves at query time", stored.Bindings)
	}
}

func TestGrafanaImportIgnoresUnusedUnsafeConstant(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	content := `{"title":"V","schemaVersion":39,
		"templating":{"list":[{"name":"unused","type":"constant","query":"x\"} or vector(1)"}]},
		"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"up{project_id=\"$project_id\"}","refId":"A"}]}]}`
	resp := importGrafanaDashboard(t, server, projectID, content)
	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID})
	if commit.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s, want unused constant ignored", commit.Code, commit.Body.String())
	}
}

func TestGrafanaImportCommitIsIdempotentPerSession(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	resp := importGrafanaDashboard(t, server, projectID, importFixtureDashboard)
	first := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID})
	if first.Code != http.StatusCreated {
		t.Fatalf("first commit = %d", first.Code)
	}
	// A second commit of the same (now-consumed) session must not mint a duplicate.
	second := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID})
	if second.Code != http.StatusConflict {
		t.Fatalf("second commit = %d, want 409", second.Code)
	}
	list := dashboardAPIRequest(t, server, http.MethodGet, "/api/v1/monitoring/dashboards?project_id="+projectID, nil)
	var listResp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 {
		t.Fatalf("dashboard count = %d, want exactly 1 (no duplicate)", listResp.Count)
	}
}

func TestGrafanaImportCommitBlocksOnBlockingDiagnostic(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	content := `{"title":"Old","schemaVersion":12,"panels":[{"id":1,"type":"timeseries","title":"P","targets":[{"expr":"up","refId":"A"}]}]}`
	resp := importGrafanaDashboard(t, server, projectID, content)
	if !resp.HasBlockers {
		t.Fatal("expected blockers for an old schema version")
	}
	commit := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit",
		map[string]any{"project_id": projectID, "import_session_id": resp.ImportSessionID})
	if commit.Code != http.StatusUnprocessableEntity {
		t.Fatalf("commit status = %d, want 422", commit.Code)
	}
}

func bytesContains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

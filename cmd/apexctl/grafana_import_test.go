package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGrafanaImportArgsRejectsMissingFlagValue(t *testing.T) {
	t.Setenv("APEXLOAD_API", "http://controller")
	if _, err := parseGrafanaImportArgs([]string{"--path"}); err == nil {
		t.Fatal("expected missing --path value error")
	}
}

func TestParseGrafanaImportArgsRejectsDuplicateBindings(t *testing.T) {
	_, err := parseGrafanaImportArgs([]string{"--api", "http://controller", "--path", "dashboard.json", "--project", "project-a", "--binding", "env=prod", "--binding", "env=stage"})
	if err == nil || !strings.Contains(err.Error(), "supplied more than once") {
		t.Fatalf("duplicate binding error = %v", err)
	}
}

func TestParseGrafanaImportArgsSupportsEqualsAndRejectsMalformedFlags(t *testing.T) {
	t.Setenv("APEXLOAD_API", "")
	args, err := parseGrafanaImportArgs([]string{
		"--api=https://controller.example", "--path=dashboard.json", "--project=project-a",
		"--binding=env=prod", "--timeout=5m", "--format=json",
	})
	if err != nil || args.api != "https://controller.example" || args.path != "dashboard.json" || args.projectID != "project-a" || args.bindings["env"] != "prod" || args.format != "json" {
		t.Fatalf("equals-form Grafana args = %#v, error %v", args, err)
	}
	for _, raw := range [][]string{
		{"--api=https://controller.example", "--path=dashboard.json", "--project=project-a", "extra"},
		{"--api=controller-without-scheme", "--path=dashboard.json", "--project=project-a"},
		{"--api=https://controller.example", "--path=dashboard.json", "--project=project-a", "--format=yaml"},
	} {
		if _, err := parseGrafanaImportArgs(raw); err == nil {
			t.Errorf("parseGrafanaImportArgs(%q) accepted invalid input", raw)
		}
	}
}

func TestRunGrafanaImportInspectOnlyDoesNotRequireBindingsOrCommit(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v1/monitoring/dashboards/import/grafana" {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"import_session_id":"session-1","required_bindings":["env"],"has_blockers":false}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "dashboard.json")
	if err := os.WriteFile(path, []byte(`{"title":"Dashboard"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, inspected, err := runGrafanaImport(t.Context(), grafanaImportArgs{path: path, projectID: "project-a", api: server.URL, timeout: defaultCLIImportTimeout, bindings: map[string]string{}, inspectOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if inspected == nil || len(inspected.RequiredBindings) != 1 || requests != 1 {
		t.Fatalf("inspection=%#v requests=%d", inspected, requests)
	}
}

func TestRunGrafanaImportRejectsOversizedFileBeforeHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxGrafanaCLIImportBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runGrafanaImport(t.Context(), grafanaImportArgs{path: path, projectID: "project-a", api: "http://127.0.0.1:1", timeout: defaultCLIImportTimeout}); err == nil {
		t.Fatal("expected oversized dashboard error")
	}
}

func TestDecodeGrafanaCLIJSONRejectsOversizedResponse(t *testing.T) {
	var destination map[string]any
	if err := decodeGrafanaCLIJSON(strings.NewReader(strings.Repeat("x", int(maxGrafanaCLIResponseBytes+1))), &destination); err == nil {
		t.Fatal("expected oversized controller response error")
	}
}

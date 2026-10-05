package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDynatraceCommandRoutesAndBoundedOperations(t *testing.T) {
	var calls []string
	var bodies []map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			bodies = append(bodies, body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"operation","status":"complete"}`))
	}))
	defer api.Close()
	for _, tc := range []struct {
		args  []string
		route string
	}{
		{[]string{"list"}, "GET /api/v1/projects/p/monitoring/dynatrace-connections"},
		{[]string{"delete", "--id", "c", "--revision", "3"}, "DELETE /api/v1/projects/p/monitoring/dynatrace-connections/c?expected_revision=3"},
		{[]string{"discover", "--id", "c", "--kind", "entities", "--selector", "type(SERVICE)", "--wait"}, "POST /api/v1/projects/p/monitoring/dynatrace-connections/c/operations"},
		{[]string{"test", "--id", "c", "--wait"}, "POST /api/v1/projects/p/monitoring/dynatrace-connections/c/operations"},
		{[]string{"preview", "--id", "c", "--source-id", "approved-source", "--wait"}, "POST /api/v1/projects/p/monitoring/dynatrace-connections/c/operations"},
		{[]string{"operation", "--id", "c", "--operation", "op"}, "GET /api/v1/projects/p/monitoring/dynatrace-connections/c/operations/op"},
	} {
		if err := executeDynatraceCommand(context.Background(), append(tc.args, "--api", api.URL, "--project", "p"), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if got := calls[len(calls)-1]; got != tc.route {
			t.Fatalf("route %s want %s", got, tc.route)
		}
	}
	if bodies[0]["kind"] != "discover" || bodies[0]["options"].(map[string]any)["selector"] != "type(SERVICE)" || bodies[1]["kind"] != "verify" {
		t.Fatalf("operations %+v", bodies)
	}
	if bodies[2]["kind"] != "preview" || bodies[2]["source_id"] != "approved-source" || bodies[2]["selection"] != nil || bodies[2]["options"] != nil {
		t.Fatalf("approved source preview %+v", bodies[2])
	}
	before := len(calls)
	for _, args := range [][]string{{"delete", "--id", "c"}, {"discover", "--id", "c", "--kind", "arbitrary"}, {"discover", "--id", "c", "--limit", "101"}, {"preview", "--id", "c"}, {"test", "--id", "c", "--wait", "--timeout", "2m"}, {"test", "--id", "c", "--source-id", "approved-source"}} {
		if err := executeDynatraceCommand(context.Background(), append(args, "--api", api.URL, "--project", "p"), &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid input accepted %+v", args)
		}
	}
	if len(calls) != before {
		t.Fatal("invalid command performed remote action")
	}
}

func TestDynatraceCommandPreviewProjectsSelectionAndReportsFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "selection.json")
	if err := os.WriteFile(file, []byte(`{"project_id":"p","problems":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["kind"] != "preview" || body["selection"].(map[string]any)["project_id"] != "p" {
			t.Fatalf("preview %+v", body)
		}
		w.Write([]byte(`{"id":"op","status":"error","message":"permission_denied"}`))
	}))
	defer api.Close()
	var out bytes.Buffer
	err := executeDynatraceCommand(context.Background(), []string{"preview", "--api", api.URL, "--project", "p", "--id", "c", "--file", file, "--wait"}, &out)
	if err == nil || !strings.Contains(out.String(), "permission_denied") {
		t.Fatalf("failure %v output %s", err, &out)
	}
	out.Reset()
	err = executeDynatraceCommand(context.Background(), []string{"preview", "--api", api.URL, "--project", "p", "--id", "c", "--file", file, "--source-id", "approved-source"}, &out)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") || out.Len() != 0 {
		t.Fatalf("ambiguous preview %v output %s", err, &out)
	}
}

func TestDynatraceCommandNativeMonitorAliases(t *testing.T) {
	var calls []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Write([]byte(`{}`))
	}))
	defer api.Close()
	for _, tc := range []struct {
		args  []string
		route string
	}{
		{[]string{"sources", "--project", "p"}, "GET /api/v1/projects/p/monitor-sources"},
		{[]string{"bindings", "--id", "scenario"}, "GET /api/v1/scenarios/scenario/monitor-bindings"},
		{[]string{"series", "--id", "run", "--source-id", "dt", "--metric", "cpu"}, "GET /api/v1/tests/run/monitor-series?limit=100&metric=cpu&source_id=dt"},
		{[]string{"evidence", "--id", "run", "--kind", "problem"}, "GET /api/v1/tests/run/monitor-evidence?kind=problem&limit=100"},
		{[]string{"evidence", "--id", "run", "--kind", "span"}, "GET /api/v1/tests/run/monitor-evidence?kind=span&limit=100"},
		{[]string{"evidence", "--id", "run", "--kind", "trace"}, "GET /api/v1/tests/run/monitor-evidence?kind=trace&limit=100"},
	} {
		if err := executeDynatraceCommand(context.Background(), append(tc.args, "--api", api.URL), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if got := calls[len(calls)-1]; got != tc.route {
			t.Fatalf("route %s want %s", got, tc.route)
		}
	}
	before := len(calls)
	if err := executeDynatraceCommand(context.Background(), []string{"evidence", "--id", "run", "--kind", "request_body", "--api", api.URL}, &bytes.Buffer{}); err == nil || len(calls) != before {
		t.Fatal("unsupported diagnostic kind performed remote action")
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPrometheusCommandRoutesAndValidation(t *testing.T) {
	var calls []string
	var bodies []map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"op","status":"complete"}`))
	}))
	defer api.Close()
	file := filepath.Join(t.TempDir(), "preview.json")
	if err := os.WriteFile(file, []byte(`{"selection":{"project_id":"p","queries":[{"id":"q","name":"cpu","expression":"up","unit":"count"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args  []string
		route string
	}{
		{[]string{"list"}, "GET /api/v1/projects/p/monitoring/prometheus-connections"},
		{[]string{"delete", "--id", "c", "--revision", "3"}, "DELETE /api/v1/projects/p/monitoring/prometheus-connections/c?revision=3"},
		{[]string{"discover", "--id", "c", "--kind", "label_values", "--selector", `{job="checkout"}`, "--label", "instance"}, "POST /api/v1/projects/p/monitoring/prometheus-connections/c/discover"},
		{[]string{"preview", "--id", "c", "--file", file, "--wait"}, "POST /api/v1/projects/p/monitoring/prometheus-connections/c/preview"},
		{[]string{"test", "--id", "c", "--wait"}, "POST /api/v1/projects/p/monitoring/prometheus-connections/c/test"},
		{[]string{"operation", "--id", "c", "--operation", "op"}, "GET /api/v1/projects/p/monitoring/prometheus-connections/c/operations/op"},
		{[]string{"adopt-dashboard", "--id", "c", "--dashboard", "saved-d"}, "POST /api/v1/projects/p/monitoring/prometheus-connections/c/adopt-dashboard"},
	} {
		if err := executePrometheusCommand(context.Background(), append(tc.args, "--api", api.URL, "--project", "p"), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if calls[len(calls)-1] != tc.route {
			t.Fatalf("got %s want %s", calls[len(calls)-1], tc.route)
		}
	}
	if bodies[2]["selector"] != `{job="checkout"}` || bodies[2]["label"] != "instance" {
		t.Fatal("discovery selector changed")
	}
	before := len(calls)
	for _, args := range [][]string{{"delete", "--id", "c"}, {"preview", "--id", "c"}, {"adopt-dashboard", "--id", "c"}, {"discover", "--id", "c", "--kind", "label_values"}, {"test", "--id", "c", "--wait", "--timeout", "2m"}} {
		if err := executePrometheusCommand(context.Background(), append(args, "--project", "p", "--api", api.URL), &bytes.Buffer{}); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	if len(calls) != before {
		t.Fatal("invalid command reached API")
	}
}
func TestPrometheusCommandInspectionFailureIsVisible(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"op","status":"failed","message":"permission denied"}`))
	}))
	defer api.Close()
	var output bytes.Buffer
	if err := executePrometheusCommand(context.Background(), []string{"test", "--project", "p", "--id", "c", "--wait", "--api", api.URL}, &output); err == nil || !bytes.Contains(output.Bytes(), []byte("permission denied")) {
		t.Fatal("inspection failure not reported")
	}
}

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

func TestElasticCommandRoutesAndRevision(t *testing.T) {
	var method, path string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"id":"op","status":"complete"}`))
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "connection.json")
	if err := os.WriteFile(file, []byte(`{"name":"Elastic","revision":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args         []string
		method, path string
	}{
		{[]string{"list"}, "GET", ""},
		{[]string{"create", "--file", file}, "POST", ""},
		{[]string{"update", "--id", "c", "--file", file}, "PUT", "/c"},
		{[]string{"inspect", "--id", "c", "--revision", "2", "--wait"}, "POST", "/c/inspections"},
		{[]string{"operation", "--id", "c", "--operation", "op"}, "GET", "/c/inspections/op"},
		{[]string{"cancel", "--id", "c", "--operation", "op"}, "DELETE", "/c/inspections/op"},
	} {
		args := append(append([]string{}, tc.args...), "--project", "p", "--api", server.URL)
		if err := executeElasticCommand(context.Background(), args, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if method != tc.method || path != "/api/v1/projects/p/monitoring/elk-connections"+tc.path {
			t.Fatalf("%s %s", method, path)
		}
		if tc.args[0] == "inspect" && (body["kind"] != "verify" || body["expected_revision"] != float64(2)) {
			t.Fatalf("inspection %+v", body)
		}
	}
}

func TestElasticCommandRejectsIncompleteRequests(t *testing.T) {
	for _, args := range [][]string{nil, {"list"}, {"get", "--project", "p"}, {"inspect", "--project", "p", "--id", "c"}, {"inspect", "--project", "p", "--id", "c", "--revision", "1", "--kind", "preview"}, {"get", "--project", "p", "--id", "c", "--wait"}, {"cancel", "--project", "p", "--id", "c"}} {
		if err := executeElasticCommand(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

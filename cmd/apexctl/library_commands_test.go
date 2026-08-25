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

	"github.com/jasonczech/apexload/pkg/api"
)

func TestParseLibraryArgsRequiresScopedContracts(t *testing.T) {
	t.Setenv("APEXLOAD_API", "")
	valid := parseLibraryArgs("export", []string{"--api", "https://controller.example", "--kind", "scenario", "--id", "scenario-1", "--output", "bundle.json"})
	if valid.err != nil || valid.kind != "scenario" || valid.id != "scenario-1" || !valid.includeDependencies {
		t.Fatalf("valid export args = %#v", valid)
	}
	for _, test := range []struct {
		name string
		cmd  string
		args []string
	}{
		{name: "missing API", cmd: "export", args: []string{"--kind", "script", "--id", "s"}},
		{name: "invalid kind", cmd: "export", args: []string{"--api", "https://controller.example", "--kind", "java", "--id", "s"}},
		{name: "missing destination", cmd: "import", args: []string{"--api", "https://controller.example", "--file", "bundle.json"}},
		{name: "missing file", cmd: "import", args: []string{"--api", "https://controller.example", "--project", "p"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if parsed := parseLibraryArgs(test.cmd, test.args); parsed.err == nil {
				t.Fatalf("expected error, got %#v", parsed)
			}
		})
	}
}

func TestDecodeBoundedCLIJSONRejectsOverflowAndTrailingValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		limit int64
	}{
		{name: "overflow", input: `{"ok":true} `, limit: int64(len(`{"ok":true}`))},
		{name: "trailing value", input: `{"ok":true}{"extra":true}`, limit: 128},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output map[string]any
			if err := decodeBoundedCLIJSON(strings.NewReader(test.input), test.limit, &output); err == nil {
				t.Fatal("expected bounded JSON decode error")
			}
		})
	}
	var output map[string]any
	if err := decodeBoundedCLIJSON(bytes.NewBufferString(`{"ok":true}`), 128, &output); err != nil || output["ok"] != true {
		t.Fatalf("valid bounded decode = %#v, %v", output, err)
	}
}

func TestWriteCLILibraryBundleTightensExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCLILibraryBundle(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("bundle permissions = %o, want 600", got)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "new" {
		t.Fatalf("bundle content = %q, %v", content, err)
	}
}

func TestLibraryClientExportAndConflictImport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("authorization = %q", got)
		}
		switch r.URL.Path {
		case "/api/v1/library/export":
			var request api.LibraryTransferRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.Kind != "script" || request.ID != "script-1" || !request.IncludeDependencies {
				t.Errorf("export request = %#v", request)
			}
			_ = json.NewEncoder(w).Encode(api.LibraryBundle{SchemaVersion: "apexload.library.v1", BundleID: "bundle-1"})
		case "/api/v1/library/import":
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.LibraryImportResponse{SchemaVersion: "apexload.library.v1", Conflicts: []api.LibraryConflict{{Kind: "script", Reason: "already exists"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewApiClientWithAuth(server.URL, "", "token")
	bundle, err := client.ExportLibraryBundle(context.Background(), api.LibraryTransferRequest{Kind: "script", ID: "script-1", IncludeDependencies: true})
	if err != nil || bundle.BundleID != "bundle-1" {
		t.Fatalf("export = %#v, %v", bundle, err)
	}
	result, conflicted, err := client.ImportLibraryBundle(context.Background(), api.LibraryImportRequest{Bundle: *bundle, ProjectID: "project-1"})
	if err != nil || !conflicted || len(result.Conflicts) != 1 {
		t.Fatalf("import = %#v, conflicted=%v, err=%v", result, conflicted, err)
	}
}

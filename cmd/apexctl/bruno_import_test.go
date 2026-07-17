package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseBrunoImportArgs(t *testing.T) {
	args, err := parseBrunoImportArgs([]string{
		"--api", "https://controller.example.test/",
		"--path", "./collection",
		"--project", "project-1",
		"--sync", "script-1",
		"--sandbox", "developer",
		"--no-migrate-secrets",
		"--secret-binding", "requirement-1=BRUNO_PASSWORD",
		"--conflict-resolution", "canonical_action=use_source",
		"--format", "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if args.api != "https://controller.example.test" || args.path != "./collection" ||
		args.projectID != "project-1" || args.syncScriptID != "script-1" ||
		args.sandbox != "developer" || args.format != "json" || args.migrateSecrets {
		t.Fatalf("parsed args = %#v", args)
	}
	if args.secretBindings["requirement-1"] != "BRUNO_PASSWORD" {
		t.Fatalf("secret bindings = %#v", args.secretBindings)
	}
	if args.conflicts["canonical_action"] != "use_source" {
		t.Fatalf("conflict resolutions = %#v", args.conflicts)
	}
}

func TestRunBrunoImportStreamsFileAndCommitsBindings(t *testing.T) {
	root := t.TempDir()
	source := []byte("info: {name: Request, type: http}\nhttp: {url: https://example.test}\n")
	sourcePath := filepath.Join(root, "request.yml")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	var inspectCalled, commitCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/scripts/import/bruno/inspect":
			inspectCalled = true
			if r.URL.Query().Get("project_id") != "project-1" || r.URL.Query().Get("filename") != "request.yml" {
				t.Fatalf("inspect query = %s", r.URL.RawQuery)
			}
			if got, _ := io.ReadAll(r.Body); !reflect.DeepEqual(got, source) {
				t.Fatalf("inspect body = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{
			  "import_session_id":"session-1",
			  "result":{
			    "collection":{"name":"Request"},
			    "secret_requirements":[{"id":"requirement-1","suggested_name":"PASSWORD"}]
			  }
			}`)
		case "/api/v1/scripts/import/bruno/commit":
			commitCalled = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			bindings, _ := body["secret_bindings"].(map[string]any)
			conflicts, _ := body["conflict_resolutions"].(map[string]any)
			if body["project_id"] != "project-1" || body["import_session_id"] != "session-1" ||
				body["migrate_secrets"] != true || bindings["requirement-1"] != "BRUNO_PASSWORD" ||
				conflicts["canonical_action"] != "use_source" {
				t.Fatalf("commit body = %#v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{
			  "created":true,
			  "script":{"id":"script-1","name":"Request","ref_key":"SCR-1"},
			  "revision":{"id":"revision-1"}
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := runBrunoImport(t.Context(), brunoImportArgs{
		path:           sourcePath,
		projectID:      "project-1",
		api:            server.URL,
		sandbox:        "safe",
		timeout:        defaultCLIImportTimeout,
		migrateSecrets: true,
		secretBindings: map[string]string{"PASSWORD": "BRUNO_PASSWORD"},
		conflicts:      map[string]string{"canonical_action": "use_source"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !inspectCalled || !commitCalled || result.Script.ID != "script-1" || result.Revision.ID != "revision-1" {
		t.Fatalf("inspect=%t commit=%t result=%#v", inspectCalled, commitCalled, result)
	}
}

func TestRunBrunoImportStreamsDirectoryInDeterministicOrder(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"b.yml": "info: {name: B, type: http}\nhttp: {url: https://b.test}\n",
		"a.yml": "info: {name: A, type: http}\nhttp: {url: https://a.test}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var uploaded []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/scripts/import/bruno/inspect":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if part.FileName() != "" {
					uploaded = append(uploaded, strings.TrimPrefix(part.FormName(), "file:"))
				}
				_, _ = io.Copy(io.Discard, part)
				_ = part.Close()
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"import_session_id":"session-1","result":{"collection":{"name":"Directory"}}}`)
		case "/api/v1/scripts/import/bruno/commit":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"script":{"id":"script-1","name":"Directory"},"revision":{"id":"revision-1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := runBrunoImport(t.Context(), brunoImportArgs{
		path:           root,
		projectID:      "project-1",
		api:            server.URL,
		sandbox:        "safe",
		timeout:        defaultCLIImportTimeout,
		migrateSecrets: true,
		secretBindings: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.yml", "b.yml"}; !reflect.DeepEqual(uploaded, want) {
		t.Fatalf("uploaded paths = %#v, want %#v", uploaded, want)
	}
}

func TestDeterministicBrunoDirectoryFilesRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "request.yml")
	if err := os.WriteFile(target, []byte("request"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked.yml")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := deterministicBrunoDirectoryFiles(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestBrunoCLIResponseErrorExplainsMissingBindings(t *testing.T) {
	response := &http.Response{
		Status:     "409 Conflict",
		StatusCode: http.StatusConflict,
		Body: io.NopCloser(strings.NewReader(`{
		  "error":"Bruno secret bindings are required before commit",
		  "requirements":[{"id":"req-1","suggested_name":"PASSWORD"}]
		}`)),
	}
	err := brunoCLIResponseError("commit Bruno import", response)
	if err == nil || !strings.Contains(err.Error(), "--secret-binding") || !strings.Contains(err.Error(), "req-1") {
		t.Fatalf("binding error = %v", err)
	}
}

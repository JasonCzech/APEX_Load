package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDynatraceDryRunChecksSavedRunProject(t *testing.T) {
	server, store := newLocalAuthTestServer(t, false)
	now := time.Now().UTC()
	if err := store.SaveDomain(&storage.Domain{ID: "foreign-domain", Name: "Foreign", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplicationRepository(&storage.ApplicationRepository{ID: "foreign-repo", DomainID: "foreign-domain", Name: "Foreign", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRepositoryCategory(&storage.RepositoryCategory{ID: "foreign-category", DomainID: "foreign-domain", RepositoryID: "foreign-repo", Name: "Foreign", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProject(&storage.Project{ID: "foreign-project", DomainID: "foreign-domain", CategoryID: "foreign-category", Name: "Foreign", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, run := range []*ManagedTest{
		{ID: "foreign-test", Name: "Confidential foreign test", ProjectID: "foreign-project", Status: "CREATED"},
		{ID: "own-test", Name: "Allowed test", ProjectID: storage.DefaultProjectID, Status: "CREATED"},
	} {
		server.runner.mu.Lock()
		server.runner.tests[run.ID] = run
		server.runner.mu.Unlock()
		if err := store.SaveTest(&storage.TestRecord{ID: run.ID, Name: run.Name, ProjectID: run.ProjectID, Status: run.Status}); err != nil {
			t.Fatal(err)
		}
	}
	login, cookie := localLogin(t, server, "user", "user")
	if login.Code != http.StatusOK || cookie == nil {
		t.Fatal("login failed")
	}
	direct := httptest.NewRequest(http.MethodGet, "/api/v1/tests/foreign-test", nil)
	direct.AddCookie(cookie)
	denied := httptest.NewRecorder()
	server.mux.ServeHTTP(denied, direct)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("foreign test must be inaccessible to the fixture: %d %s", denied.Code, denied.Body.String())
	}
	for _, tc := range []struct {
		name, body, wantName string
		wantStatus           int
	}{
		{"foreign top-level ID", `{"test_id":"foreign-test"}`, "", http.StatusForbidden},
		{"foreign event ID", `{"event":{"test_id":"foreign-test"}}`, "", http.StatusForbidden},
		{"conflicting IDs", `{"test_id":"own-test","event":{"test_id":"foreign-test"}}`, "", http.StatusBadRequest},
		{"own top-level ID", `{"test_id":"own-test"}`, "Allowed test", http.StatusOK},
		{"own event ID", `{"event":{"test_id":"own-test"}}`, "Allowed test", http.StatusOK},
		{"matching IDs", `{"test_id":" own-test ","event":{"test_id":"own-test"}}`, "Allowed test", http.StatusOK},
		{"synthetic event", `{"event":{"test_name":"Synthetic event"}}`, "Synthetic event", http.StatusOK},
		{"synthetic ID", `{"test_id":"not-a-saved-test"}`, "not-a-saved-test", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/dynatrace/dry-run", strings.NewReader(tc.body))
			req.AddCookie(cookie)
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, req)
			if response.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.wantStatus, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "Confidential foreign test") {
				t.Fatalf("foreign run disclosed: %s", response.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var result DynatraceDryRunResponse
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.CorrelationManifest.TestName != tc.wantName {
					t.Fatalf("test name %q, want %q", result.CorrelationManifest.TestName, tc.wantName)
				}
			}
		})
	}
}

func TestDynatraceArchiveIdentityRequiresAuthorizedSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, projectID, testID string
		withoutTest             bool
		wantErr                 bool
	}{
		{name: "matching archive", projectID: "project-a", testID: "test-a"},
		{name: "replacement project", projectID: "project-b", testID: "test-a", wantErr: true},
		{name: "replacement test", projectID: "project-a", testID: "test-b", wantErr: true},
		{name: "unresolved test", projectID: "project-a", testID: "test-a", withoutTest: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			manifest := RunArchiveManifest{TestID: tc.testID, Provenance: RunArchiveProvenance{ProjectID: tc.projectID}, TransactionsFile: "transactions.jsonl"}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(root, "manifest.json")
			if err := os.WriteFile(manifestPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), []byte(`{"trace_id":"trace-a","span_id":"span-a","dependency_target":"private.example"}`+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			record := &storage.RunArchiveRecord{TestID: "test-a", ArchivePath: root, ManifestPath: manifestPath}
			test := &ManagedTest{ID: "test-a", ProjectID: "project-a"}
			if tc.withoutTest {
				test = nil
			}
			traces, spans, targets, err := streamDynatraceArchiveIdentity(context.Background(), record, test, 100)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v; want error %v", err, tc.wantErr)
			}
			if tc.wantErr {
				if len(traces)+len(spans)+len(targets) != 0 {
					t.Fatalf("unauthorized archive identities: %v %v %v", traces, spans, targets)
				}
			} else if len(traces) != 1 || traces[0] != "trace-a" || len(spans) != 1 || spans[0] != "span-a" || len(targets) != 1 || targets[0] != "private.example" {
				t.Fatalf("missing authorized archive identities: %v %v %v", traces, spans, targets)
			}
		})
	}
}

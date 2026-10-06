package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func resourceHandlerFixture(t *testing.T, withProvenance bool) (*Server, *storage.SQLiteStore, *storage.TestRecord, *storage.RunArchiveRecord) {
	t.Helper()
	server, store := resourceAuthTestServer(t, authModeDev)
	now := time.Now().UTC()
	project := &storage.Project{ID: "resource-custom-project", Name: "Resource custom project", DomainID: storage.DefaultDomainID, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	name := "transactions.jsonl"
	raw := []byte(`{"timestamp":"2023-11-14T22:13:20.123456789Z","action_name":"load","network_entries":[{"url":"https://example.test/app","duration_ms":0,"status_code":0,"bytes_received":0}]}` + "\n")
	if err := os.WriteFile(filepath.Join(root, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"test_id": "resource-run", "transactions_file": name, "transaction_count": 1, "artifacts": []map[string]any{{"name": name, "format": "jsonl", "sha256": sha256BytesHex(raw), "size_bytes": len(raw), "count": 1}}}
	if withProvenance {
		manifest["provenance"] = map[string]any{"project_id": project.ID, "requested_at": now.Format(time.RFC3339Nano)}
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, runArchiveManifestFileName)
	if err := os.WriteFile(manifestPath, manifestRaw, 0600); err != nil {
		t.Fatal(err)
	}
	test := &storage.TestRecord{ID: "resource-run", RefKey: "resource-ref", Name: "Resource run", ProjectID: project.ID, Status: "COMPLETE", StartTime: now, MaterializationKey: "resource-incarnation"}
	if err := store.SaveTest(test); err != nil {
		t.Fatal(err)
	}
	archive := &storage.RunArchiveRecord{TestID: test.ID, RefKey: test.RefKey, ArchiveBackend: "filesystem", ArchivePath: root, ManifestPath: manifestPath, ManifestSHA256: sha256BytesHex(manifestRaw), CreatedAt: now, CompletedAt: now}
	if err := store.SaveRunArchive(archive); err != nil {
		t.Fatal(err)
	}
	return server, store, test, archive
}
func resourceHandlerRequest(id string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+id+"/archive/browser-resources", nil)
	request.SetPathValue("id", id)
	return request
}
func resourceHandlerReadPage(t *testing.T, server *Server, id string, options browserResourceReadOptions) *BrowserResourcePage {
	t.Helper()
	response, err := server.readBrowserResourcePage(resourceHandlerRequest(id), options)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Release()
	page, err := DecodeBrowserResourcePage(response.Raw)
	if err != nil {
		t.Fatal(err)
	}
	return page
}
func TestBrowserResourceHandlerLegacyProjectAuthorityAndSuppliedConflict(t *testing.T) {
	server, _, test, archive := resourceHandlerFixture(t, false)
	page := resourceHandlerReadPage(t, server, test.ID, browserResourceReadOptions{})
	if page.ProjectID != test.ProjectID || page.SourceIntegrity != "legacy_scan_fingerprint" || len(page.Items) != 1 || page.Items[0].DurationMs == nil || *page.Items[0].DurationMs != 0 {
		t.Fatal("legacy absent provenance changed authority, integrity or observed zero")
	}
	var manifest map[string]any
	raw, err := os.ReadFile(archive.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["provenance"] = map[string]any{"project_id": storage.DefaultProjectID}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	archive.ManifestSHA256 = sha256BytesHex(raw)
	if err := server.store.SaveRunArchive(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := server.readBrowserResourcePage(resourceHandlerRequest(test.ID), browserResourceReadOptions{}); !errors.Is(err, errBrowserResourceReadChanged) {
		t.Fatalf("supplied conflicting producer project accepted: %v", err)
	}
}
func TestBrowserResourceHandlerFinalProjectIncarnationAndAuthorizationFences(t *testing.T) {
	for _, kind := range []string{"project", "incarnation", "role", "artifact_path_replacement", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			server, store, test, archive := resourceHandlerFixture(t, false)
			request := resourceHandlerRequest(test.ID)
			var binding *storage.UserRoleBinding
			if kind == "role" {
				server.runner.settings.Auth.Mode = authModeTrustedHeaders
				user, created := resourceAuthTestUser(t, store, "project", test.ProjectID)
				binding = created
				request = resourceAuthTestRequest(t, store, user, false)
				request.SetPathValue("id", test.ID)
			}
			response, err := server.readBrowserResourcePage(request, browserResourceReadOptions{beforePublish: func() {
				switch kind {
				case "project":
					// Controlled durable invalidation bypasses SaveTest's legitimate
					// project-ownership guard, exercising the reader's final fence.
					db, err := sql.Open("sqlite", store.BackendInfo().DSN)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					result, err := db.ExecContext(context.Background(), "UPDATE tests SET project_id = ? WHERE id = ? AND project_id = ?", storage.DefaultProjectID, test.ID, test.ProjectID)
					if err != nil {
						t.Fatal(err)
					}
					if rows, err := result.RowsAffected(); err != nil || rows != 1 {
						t.Fatal("exact owned project invalidation was not applied")
					}
				case "incarnation":
					test.MaterializationKey = "new-incarnation"
					if err := store.SaveTest(test); err != nil {
						t.Fatal(err)
					}
				case "role":
					if err := store.DeleteUserRoleBinding(binding.ID); err != nil {
						t.Fatal(err)
					}
				case "artifact_path_replacement":
					original := filepath.Join(archive.ArchivePath, "transactions.jsonl")
					raw, err := os.ReadFile(original)
					if err != nil {
						t.Fatal(err)
					}
					replacement := original + ".replacement"
					if err := os.WriteFile(replacement, raw, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(replacement, original); err != nil {
						t.Fatal(err)
					}
				case "manifest":
					raw, err := os.ReadFile(archive.ManifestPath)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(archive.ManifestPath, append(raw, ' '), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}})
			if response != nil {
				response.Release()
				t.Fatal("changed or revoked source published a page")
			}
			expected := errBrowserResourceReadChanged
			if kind == "role" {
				expected = errBrowserResourceForbidden
			}
			if !errors.Is(err, expected) {
				t.Fatalf("final refusal cause changed: %v", err)
			}
		})
	}
}
func TestBrowserResourceHandlerCancellationIsNotArchiveAbsence(t *testing.T) {
	server, store, test, _ := resourceHandlerFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := resourceHandlerRequest(test.ID).WithContext(ctx)
	if _, status, _ := resolveBrowserResourceArchiveProjectID(request, store); status != http.StatusRequestTimeout {
		t.Fatalf("canceled durable lookup reported archive status%d", status)
	}
	response := httptest.NewRecorder()
	server.handleGetRunArchiveBrowserResources(response, request)
	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("canceled handler status%d", response.Code)
	}
	if browserResourceReadTimeout != 10*time.Second {
		t.Fatal("reader deadline no longer fits the approved client/server contract")
	}
}

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// A client keeps the version a write returns and sends it back with its next
// edit, as the dashboard's deployment target card does. PostgreSQL keeps
// microseconds and Go's clock has nanoseconds on Linux, so the returned
// version must be the stored one. This runs on SQLite here and on PostgreSQL
// in updated_at_precision_postgres_integration_test.go.
func TestUpdatedAtPrecisionAPI(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	exerciseUpdatedAtPrecisionAPI(t, server, store)
}

func exerciseUpdatedAtPrecisionAPI(t *testing.T, server *Server, store *storage.SQLiteStore) {
	t.Run("test_note", func(t *testing.T) {
		testID := "precision-notes-run"
		if err := store.SaveTest(&storage.TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: storage.DefaultProjectID, StartTime: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests/"+testID+"/notes", testNoteRequest{Body: "first"}, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create note: %d %s", create.Code, create.Body.String())
		}
		var note storage.TestNote
		decodeAPIResponse(t, create.Body.Bytes(), &note)
		for round := 1; round <= 2; round++ {
			edit := authoringAPIRequest(t, server, http.MethodPatch, "/api/v1/tests/"+testID+"/notes/"+note.ID, testNoteRequest{Body: fmt.Sprintf("edit %d", round), ExpectedUpdatedAt: &note.UpdatedAt}, "")
			if edit.Code != http.StatusOK {
				t.Fatalf("edit %d with the version the last write returned: %d %s", round, edit.Code, edit.Body.String())
			}
			decodeAPIResponse(t, edit.Body.Bytes(), &note)
		}
	})
	t.Run("application", func(t *testing.T) {
		create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/applications", map[string]any{"name": "Precision"}, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create application: %d %s", create.Code, create.Body.String())
		}
		var application storage.Application
		decodeAPIResponse(t, create.Body.Bytes(), &application)
		created, edited := map[string]any{}, map[string]any{}
		decodeAPIResponse(t, create.Body.Bytes(), &created)
		for round := 1; round <= 2; round++ {
			edit := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/applications/"+application.ID, map[string]any{"name": fmt.Sprintf("Precision %d", round), "updated_at": application.UpdatedAt}, "")
			if edit.Code != http.StatusOK {
				t.Fatalf("edit %d with the version the last write returned: %d %s", round, edit.Code, edit.Body.String())
			}
			decodeAPIResponse(t, edit.Body.Bytes(), &application)
			decodeAPIResponse(t, edit.Body.Bytes(), &edited)
		}
		list := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/applications", nil, "")
		var listed struct {
			Applications []map[string]any `json:"applications"`
		}
		decodeAPIResponse(t, list.Body.Bytes(), &listed)
		requireListedTimestamps(t, listed.Applications, application.ID, map[string]map[string]any{"created_at": created, "updated_at": edited})
	})
	t.Run("release", func(t *testing.T) {
		if err := store.SaveApplication(&storage.Application{ID: "precision-release-application", ProjectID: storage.DefaultProjectID, Name: "Releases"}); err != nil {
			t.Fatal(err)
		}
		create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/applications/precision-release-application/releases", map[string]any{"name": "2026.10"}, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create release: %d %s", create.Code, create.Body.String())
		}
		var release storage.Release
		decodeAPIResponse(t, create.Body.Bytes(), &release)
		for round := 1; round <= 2; round++ {
			edit := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/releases/"+release.ID, map[string]any{"name": fmt.Sprintf("2026.10.%d", round), "updated_at": release.UpdatedAt}, "")
			if edit.Code != http.StatusOK {
				t.Fatalf("edit %d with the version the last write returned: %d %s", round, edit.Code, edit.Body.String())
			}
			decodeAPIResponse(t, edit.Body.Bytes(), &release)
		}
	})
	t.Run("project", func(t *testing.T) {
		create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/projects", map[string]any{"name": "Precision project"}, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create project: %d %s", create.Code, create.Body.String())
		}
		var project storage.Project
		decodeAPIResponse(t, create.Body.Bytes(), &project)
		created := map[string]any{}
		decodeAPIResponse(t, create.Body.Bytes(), &created)
		list := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/projects", nil, "")
		var listed struct {
			Projects []map[string]any `json:"projects"`
		}
		decodeAPIResponse(t, list.Body.Bytes(), &listed)
		requireListedTimestamps(t, listed.Projects, project.ID, map[string]map[string]any{"created_at": created, "updated_at": created})
		// Recording profiles are saved against the project version a client holds.
		saveProfiles := func(expected time.Time) time.Time {
			t.Helper()
			saved := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+project.ID+"/recording-profiles", map[string]any{"profiles": []any{}, "expected_updated_at": expected}, "")
			if saved.Code != http.StatusOK {
				t.Fatalf("save recording profiles with the version the last write returned: %d %s", saved.Code, saved.Body.String())
			}
			var response recordingProfilesResponse
			decodeAPIResponse(t, saved.Body.Bytes(), &response)
			return response.UpdatedAt
		}
		saveProfiles(project.UpdatedAt)
		rename := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+project.ID, map[string]any{"name": "Precision project renamed"}, "")
		if rename.Code != http.StatusOK {
			t.Fatalf("rename project: %d %s", rename.Code, rename.Body.String())
		}
		decodeAPIResponse(t, rename.Body.Bytes(), &project)
		saveProfiles(saveProfiles(project.UpdatedAt))
	})
	t.Run("service_account", func(t *testing.T) {
		// The Access page keeps the account a create or key rotation returns and
		// compares its key rotation times with the listed account. These are
		// compared as instants, because they are UTC while PostgreSQL rows come
		// back in the local zone. The listing derives rotation_due_at again from
		// the stored key_rotated_at in that zone, which can move it by a
		// daylight saving hour, so only its precision is checked.
		requireListed := func(returned *storage.ServiceAccount) {
			t.Helper()
			list := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/auth/service-accounts", nil, "")
			var listed AuthServiceAccountListResponse
			decodeAPIResponse(t, list.Body.Bytes(), &listed)
			for _, row := range listed.ServiceAccounts {
				if row.ID != returned.ID {
					continue
				}
				requireAPIStoredTimestamp(t, "created_at", returned.CreatedAt, row.CreatedAt)
				requireAPIStoredTimestamp(t, "updated_at", returned.UpdatedAt, row.UpdatedAt)
				if returned.KeyRotatedAt == nil || row.KeyRotatedAt == nil || returned.RotationDueAt == nil || row.RotationDueAt == nil {
					t.Fatalf("key rotation times returned %v/%v, listed %v/%v", returned.KeyRotatedAt, returned.RotationDueAt, row.KeyRotatedAt, row.RotationDueAt)
				}
				requireAPIStoredTimestamp(t, "key_rotated_at", *returned.KeyRotatedAt, *row.KeyRotatedAt)
				requireAPIStoredTimestamp(t, "rotation_due_at", *returned.RotationDueAt, returned.RotationDueAt.Truncate(time.Microsecond))
				return
			}
			t.Fatalf("service account %s is not listed", returned.ID)
		}
		create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/auth/service-accounts", map[string]any{"name": "Precision reader", "scopes": []string{"results:read"}}, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create service account: %d %s", create.Code, create.Body.String())
		}
		var created AuthServiceAccountCreateResponse
		decodeAPIResponse(t, create.Body.Bytes(), &created)
		requireListed(created.ServiceAccount)
		rotate := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/auth/service-accounts/"+created.ServiceAccount.ID+"/rotate", nil, "")
		if rotate.Code != http.StatusOK {
			t.Fatalf("rotate service account key: %d %s", rotate.Code, rotate.Body.String())
		}
		var rotated AuthServiceAccountCreateResponse
		decodeAPIResponse(t, rotate.Body.Bytes(), &rotated)
		requireListed(rotated.ServiceAccount)
		edit := authoringAPIRequest(t, server, http.MethodPatch, "/api/v1/auth/service-accounts/"+created.ServiceAccount.ID, map[string]any{"description": "Reads results"}, "")
		if edit.Code != http.StatusOK {
			t.Fatalf("edit service account: %d %s", edit.Code, edit.Body.String())
		}
		var edited storage.ServiceAccount
		decodeAPIResponse(t, edit.Body.Bytes(), &edited)
		requireListed(&edited)
	})
	t.Run("domain_deployment_target", func(t *testing.T) {
		now := time.Now().UTC()
		if err := store.SaveDomain(&storage.Domain{ID: "precision-domain", Name: "Precision", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		path := "/api/v1/system/deployment-targets/precision-domain"
		saved := authoringAPIRequest(t, server, http.MethodPut, path, map[string]any{"provider": "kubernetes"}, "")
		for round := 1; ; round++ {
			if saved.Code != http.StatusOK {
				t.Fatalf("write %d: %d %s", round, saved.Code, saved.Body.String())
			}
			if round == 3 {
				return
			}
			var target DomainDeploymentTargetResponse
			decodeAPIResponse(t, saved.Body.Bytes(), &target)
			saved = authoringAPIRequest(t, server, http.MethodPut, path, map[string]any{"provider": "kubernetes", "updated_at": target.UpdatedAt}, "")
		}
	})
}

// requireListedTimestamps compares a listed row's timestamps with those a
// write returned as the JSON text the dashboard's catalog compares, field by
// field, before it accepts the listed row.
func requireListedTimestamps(t *testing.T, rows []map[string]any, id string, returned map[string]map[string]any) {
	t.Helper()
	for _, row := range rows {
		if row["id"] != id {
			continue
		}
		for field, response := range returned {
			if row[field] != response[field] {
				t.Errorf("listed %s = %v, the write returned %v", field, row[field], response[field])
			}
			if at, err := time.Parse(time.RFC3339Nano, fmt.Sprint(response[field])); err != nil || !at.Equal(at.Truncate(time.Microsecond)) {
				t.Errorf("returned %s = %v: want microseconds (%v)", field, response[field], err)
			}
		}
		return
	}
	t.Fatalf("%s is not listed", id)
}

func decodeAPIResponse(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBatchImportSavesRejectNonScriptDestinationFolders(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "batch-folder-category.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	resultsFolder := &storage.Folder{
		ID:        "batch-import-results-folder",
		ProjectID: storage.DefaultProjectID,
		Name:      "Run results",
		Category:  "results",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SaveFolder(resultsFolder); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})

	asyncWithEmbeddedFolder := asyncAPIBatchTestScript("AsyncAPI embedded", storage.DefaultProjectID, "")
	asyncWithEmbeddedFolder["folder_id"] = resultsFolder.ID
	genericWithEmbeddedFolder := genericImportBatchTestScript("Generic embedded", storage.DefaultProjectID, "")
	genericWithEmbeddedFolder["folder_id"] = resultsFolder.ID
	enterpriseWithEmbeddedFolder := enterpriseBatchTestScript("Enterprise embedded", storage.DefaultProjectID)
	enterpriseWithEmbeddedFolder["folder_id"] = resultsFolder.ID
	jmeterScript := dsl.Script{
		Name:      "JMeter embedded",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		FolderID:  &resultsFolder.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:    "jmeter",
			Preserved: map[string]any{},
		}},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}

	tests := []struct {
		name    string
		path    string
		payload map[string]any
	}{
		{
			name: "generic explicit override",
			path: "/api/v1/scripts/import/commit-batch",
			payload: map[string]any{
				"operation_id":   "generic-non-script-override",
				"source_profile": "raml",
				"folder_id":      resultsFolder.ID,
				"scripts":        []any{genericImportBatchTestScript("Generic override", storage.DefaultProjectID, "")},
			},
		},
		{
			name: "generic embedded candidate",
			path: "/api/v1/scripts/import/commit-batch",
			payload: map[string]any{
				"operation_id":   "generic-non-script-candidate",
				"source_profile": "raml",
				"scripts":        []any{genericWithEmbeddedFolder},
			},
		},
		{
			name: "AsyncAPI explicit override",
			path: "/api/v1/scripts/import/asyncapi/commit-batch",
			payload: map[string]any{
				"operation_id": "asyncapi-non-script-override",
				"folder_id":    resultsFolder.ID,
				"scripts":      []any{asyncAPIBatchTestScript("AsyncAPI override", storage.DefaultProjectID, "")},
			},
		},
		{
			name: "AsyncAPI embedded candidate",
			path: "/api/v1/scripts/import/asyncapi/commit-batch",
			payload: map[string]any{
				"operation_id": "asyncapi-non-script-candidate",
				"scripts":      []any{asyncWithEmbeddedFolder},
			},
		},
		{
			name: "enterprise explicit override",
			path: "/api/v1/scripts/import/enterprise-script-archive/save",
			payload: map[string]any{
				"operation_id": "enterprise-non-script-override",
				"folder_id":    resultsFolder.ID,
				"scripts":      []any{enterpriseBatchTestScript("Enterprise override", storage.DefaultProjectID)},
			},
		},
		{
			name: "enterprise embedded candidate",
			path: "/api/v1/scripts/import/enterprise-script-archive/save",
			payload: map[string]any{
				"operation_id": "enterprise-non-script-candidate",
				"scripts":      []any{enterpriseWithEmbeddedFolder},
			},
		},
		{
			name: "JMeter embedded destination",
			path: "/api/v1/scripts/import/jmeter/save",
			payload: map[string]any{
				"scripts": []dsl.Script{jmeterScript},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			response := postJSON(t, server, test.path, body, "")
			if response.Code != http.StatusConflict ||
				!strings.Contains(response.Body.String(), "script folder") {
				t.Fatalf("status = %d, want 409 script-folder error; body=%s", response.Code, response.Body.String())
			}
			stored, err := store.ListScripts(storage.DefaultProjectID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(stored) != 0 {
				t.Fatalf("rejected batch persisted scripts: %#v", stored)
			}
		})
	}
}

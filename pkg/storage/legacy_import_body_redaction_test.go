package storage_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRecognizedLegacyImportBodyCredentialBuildsExactRuntimeMatcher(t *testing.T) {
	const password = "durable-legacy-body-password-7319"
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-body.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	body := `{"password":"` + password + `","ordinary":"visible"}`
	script := &dsl.Script{
		ID:            "legacy-postman-body",
		Name:          "Legacy Postman Body",
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     storage.DefaultProjectID,
		GeneratedBy:   "apexload-postman-converter",
		GeneratedFrom: "postman",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
		}},
		Actions: []dsl.Action{{
			Name:   "login",
			Type:   "POST",
			Method: "POST",
			Target: "/login",
			HeaderList: dsl.HeaderList{{
				Name: "Content-Type", Value: "application/json",
			}},
			Body: body,
		}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("seed pre-policy import: %v", err)
	}

	loaded, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("load recognized legacy import: %v", err)
	}
	values, err := engine.ImportedCredentialValues(loaded)
	if err != nil {
		t.Fatalf("inspect recognized legacy body credentials: %v", err)
	}
	if !containsExact(values, password) {
		t.Fatalf("runtime credential values = %#v, want exact body password", values)
	}
	config, err := engine.WithSecretValueRedactionChecked(nil, values)
	if err != nil {
		t.Fatalf("construct exact runtime matcher: %v", err)
	}
	if got := config.ScrubText("echo=" + password); strings.Contains(got, password) {
		t.Fatalf("legacy body credential echo was not redacted: %q", got)
	}
	if loaded.Actions[0].Body != body {
		t.Fatalf("durable executable body changed to %q", loaded.Actions[0].Body)
	}
}

func containsExact(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

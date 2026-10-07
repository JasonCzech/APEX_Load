package storage

import (
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestDurableLegacyImportIsRecognizedWithoutRewritingStoredProvenance(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-import.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	legacy := &dsl.Script{
		ID:            "legacy-loadrunner",
		Name:          "Legacy LoadRunner",
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     DefaultProjectID,
		GeneratedBy:   "apexload-loadrunner-converter",
		GeneratedFrom: "loadrunner",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "loadrunner",
		}},
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type: "basic", Username: "operator", Password: "legacy-password",
			}},
		},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	if err := store.SaveScript(legacy); err != nil {
		t.Fatalf("seed legacy import: %v", err)
	}

	var rawMetadata string
	if err := store.db.QueryRow(
		`SELECT metadata_json FROM scripts WHERE id = ?`,
		legacy.ID,
	).Scan(&rawMetadata); err != nil {
		t.Fatalf("load raw legacy metadata: %v", err)
	}
	if rawMetadata == "" || containsImportValuePolicy(rawMetadata) {
		t.Fatalf("fixture unexpectedly persisted the modern policy: %s", rawMetadata)
	}

	loaded, err := store.GetScript(legacy.ID)
	if err != nil {
		t.Fatalf("load legacy import: %v", err)
	}
	assertCredentialPreservingPolicy(t, loaded)
	if loaded.Config.HTTP.Auth.Password != "legacy-password" {
		t.Fatal("legacy import credential changed while recognizing provenance")
	}
	listed, err := store.ListScripts(DefaultProjectID, nil)
	if err != nil {
		t.Fatalf("list legacy import: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed scripts = %d, want 1", len(listed))
	}
	assertCredentialPreservingPolicy(t, listed[0])

	ordinary := *legacy
	ordinary.ID = "ordinary-generated"
	ordinary.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "postman"}}
	ordinary.GeneratedFrom = "manual"
	if err := store.SaveScript(&ordinary); err != nil {
		t.Fatalf("seed mismatched provenance: %v", err)
	}
	loadedOrdinary, err := store.GetScript(ordinary.ID)
	if err != nil {
		t.Fatalf("load mismatched provenance: %v", err)
	}
	if loadedOrdinary.Metadata.Import.Preserved != nil {
		if _, exists := loadedOrdinary.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey]; exists {
			t.Fatal("mismatched client-controlled provenance established import trust")
		}
	}
}

func TestLegacyCredentialPreservingImportProvenanceCoversHistoricalMetadataShapes(t *testing.T) {
	tests := []struct {
		name          string
		generatedBy   string
		generatedFrom string
		source        string
		omitMetadata  bool
		want          bool
	}{
		{
			name: "sample import without matching source", generatedBy: "sample-import",
			generatedFrom: "raw_http", source: "", want: true,
		},
		{
			name: "HAR import without matching source", generatedBy: "apexload-har-converter",
			generatedFrom: "har", source: "sample", want: true,
		},
		{
			name: "Bruno source label differs from format", generatedBy: "apexload-bruno-importer",
			generatedFrom: "bru", source: "bruno", want: true,
		},
		{
			name: "enterprise source label identifies a lane", generatedBy: "apexload-enterprise-script-archive-java-importer",
			generatedFrom: "enterprise_script_archive", source: "java_vuser", want: true,
		},
		{
			name: "legacy HTTP recorder omitted generated-from", generatedBy: "recorder",
			generatedFrom: "", source: "", omitMetadata: true, want: true,
		},
		{
			name: "legacy browser recorder omitted generated-from", generatedBy: "browser-recorder",
			generatedFrom: "", source: "", want: true,
		},
		{
			name: "legacy recorder JSON importer omitted generated-from", generatedBy: "apexload-recorder-importer",
			generatedFrom: "", source: "recorder", want: true,
		},
		{
			name: "unbounded generated-by is rejected", generatedBy: "apexload-unknown-converter",
			generatedFrom: "har", source: "har", want: false,
		},
		{
			name: "unbounded source-less generator is rejected", generatedBy: "user-import",
			generatedFrom: "", source: "", want: false,
		},
		{
			name: "known source-less generator with wrong nonempty profile is rejected", generatedBy: "recorder",
			generatedFrom: "openapi", source: "openapi", want: false,
		},
		{
			name: "known generator with wrong source profile is rejected", generatedBy: "sample-import",
			generatedFrom: "openapi", source: "openapi", want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{
				GeneratedBy:   test.generatedBy,
				GeneratedFrom: test.generatedFrom,
			}
			if !test.omitMetadata {
				script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
					Source: test.source,
				}}
			}
			if got := LegacyCredentialPreservingImportProvenance(script); got != test.want {
				t.Fatalf("LegacyCredentialPreservingImportProvenance() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDurableSourceLessRecorderWithoutImportMetadataIsRecognized(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-recorder.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	legacy := &dsl.Script{
		ID:          "legacy-recorder",
		Name:        "Recorded flow",
		Version:     "1",
		Protocol:    "http",
		ProjectID:   DefaultProjectID,
		GeneratedBy: "recorder",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type: "basic", Username: "operator", Password: "recorded-password",
			}},
		},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	if err := store.SaveScript(legacy); err != nil {
		t.Fatalf("seed source-less recorder: %v", err)
	}
	loaded, err := store.GetScript(legacy.ID)
	if err != nil {
		t.Fatalf("load source-less recorder: %v", err)
	}
	assertCredentialPreservingPolicy(t, loaded)
	if loaded.Metadata.Import.Source != "recorder" ||
		loaded.Config.HTTP.Auth.Password != "recorded-password" {
		t.Fatalf("recognized recorder = %#v", loaded)
	}
}

func containsImportValuePolicy(metadata string) bool {
	return len(metadata) > 0 &&
		containsString(metadata, dsl.ImportPreservedCredentialPolicyKey)
}

func containsString(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

func assertCredentialPreservingPolicy(t *testing.T, script *dsl.Script) {
	t.Helper()
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatalf("script lacks import provenance: %#v", script)
	}
	policy, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey].(string)
	if policy != dsl.ImportPreservedCredentialPolicyPreserve {
		t.Fatalf("import policy = %q, want %q", policy, dsl.ImportPreservedCredentialPolicyPreserve)
	}
}

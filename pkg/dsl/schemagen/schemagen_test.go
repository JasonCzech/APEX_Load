package schemagen

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const repoRoot = "../../.."

// TestGeneratedSchemaIsFresh fails when docs/dsl.schema.json no longer
// matches the catalog — run `go run ./cmd/dslschema` to regenerate.
func TestGeneratedSchemaIsFresh(t *testing.T) {
	generated, err := GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	generated = append(generated, '\n')
	committed, err := os.ReadFile(filepath.Join(repoRoot, "docs", "dsl.schema.json"))
	if err != nil {
		t.Fatalf("read committed schema: %v (run `go run ./cmd/dslschema`)", err)
	}
	if !bytes.Equal(generated, committed) {
		t.Fatal("docs/dsl.schema.json is stale — run `go run ./cmd/dslschema`")
	}
}

// TestGeneratedReferenceIsFresh fails when docs/DSL_ACTION_REFERENCE.md no
// longer matches the catalog — run `go run ./cmd/dslschema` to regenerate.
func TestGeneratedReferenceIsFresh(t *testing.T) {
	generated := GenerateReference()
	committed, err := os.ReadFile(filepath.Join(repoRoot, "docs", "DSL_ACTION_REFERENCE.md"))
	if err != nil {
		t.Fatalf("read committed reference: %v (run `go run ./cmd/dslschema`)", err)
	}
	if !bytes.Equal(generated, committed) {
		t.Fatal("docs/DSL_ACTION_REFERENCE.md is stale — run `go run ./cmd/dslschema`")
	}
}

// TestGeneratedSchemaCompiles asserts the emitted document is a valid JSON
// Schema 2020-12.
func TestGeneratedSchemaCompiles(t *testing.T) {
	if _, err := compileGeneratedSchema(t); err != nil {
		t.Fatalf("generated schema does not compile: %v", err)
	}
}

// TestExampleScriptsPassGoValidatorAndSchema is the dual-validation drift
// guard: every example script must satisfy both the Go validator and the
// generated JSON Schema. Because the schema is generated looser-or-equal to
// the validator, a schema rejection here means the catalog and validator
// have drifted apart.
func TestExampleScriptsPassGoValidatorAndSchema(t *testing.T) {
	schema, err := compileGeneratedSchema(t)
	if err != nil {
		t.Fatal(err)
	}

	pattern := filepath.Join(repoRoot, "examples", "*.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no example scripts matched %s", pattern)
	}

	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			// Examples may wrap the script in a {"script": ...} envelope
			// (the POST /api/v1/scripts request shape).
			var envelope struct {
				Script json.RawMessage `json:"script"`
			}
			scriptJSON := raw
			if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Script) > 0 {
				scriptJSON = envelope.Script
			}

			// Some examples are test-run requests (groups, duration,
			// runtime_settings) rather than script documents — skip those.
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(scriptJSON, &probe); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if _, hasProtocol := probe["protocol"]; !hasProtocol {
				if _, hasActions := probe["actions"]; !hasActions {
					t.Skipf("%s is not a script document", filepath.Base(path))
				}
			}

			var script dsl.Script
			if err := json.Unmarshal(scriptJSON, &script); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if issues := dsl.ValidateScriptIssues(&script); len(issues) > 0 {
				t.Errorf("Go validator issues: %v", issues)
			}

			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(scriptJSON))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Errorf("schema validation: %v", err)
			}
		})
	}
}

func compileGeneratedSchema(t *testing.T) (*jsonschema.Schema, error) {
	t.Helper()
	generated, err := GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(generated))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("dsl.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	return compiler.Compile("dsl.schema.json")
}

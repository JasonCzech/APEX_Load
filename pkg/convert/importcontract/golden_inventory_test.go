package importcontract

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

type converterGoldenRegistration struct {
	Surface    string `json:"surface"`
	Converter  string `json:"converter,omitempty"`
	Valid      string `json:"valid_golden,omitempty"`
	Diagnostic string `json:"diagnostic_golden,omitempty"`
	State      string `json:"state"`
	Rationale  string `json:"rationale,omitempty"`
}

type converterGoldenInventory struct {
	SchemaVersion int                           `json:"schema_version"`
	Source        string                        `json:"source"`
	Totals        map[string]int                `json:"totals"`
	Surfaces      []converterGoldenRegistration `json:"surfaces"`
}

// TestConverterGoldenInventoryFreshness binds the M2 inventory to the product's
// backend authoring-surface registry. Adding a UI import surface without
// classifying its converter therefore fails here instead of silently lowering
// the denominator in a prose count.
func TestConverterGoldenInventoryFreshness(t *testing.T) {
	registrations := map[string]converterGoldenRegistration{
		"openapi":            coveredGolden("openapi", "openapi"),
		"bruno":              coveredGolden("bruno", "bruno"),
		"postman":            coveredGolden("postman", "postman"),
		"wsdl":               coveredGolden("wsdl", "wsdl"),
		"asyncapi":           coveredGolden("asyncapi", "asyncapi"),
		"raml":               coveredGolden("raml", "raml"),
		"traffic_har":        coveredGolden("traffic_har", "har"),
		"recorder_export":    coveredGolden("recorder_export", "recorderjson"),
		"sample":             coveredGolden("sample", "sample_curl"),
		"enterprise_archive": coveredGolden("enterprise_archive", "enterprisearchive"),
		"jmeter":             coveredGolden("jmeter", "jmeter"),
		"devweb":             coveredGolden("devweb", "devweb"),
		"k6":                 coveredGolden("k6", "k6"),
		"eventhubs_capture":  coveredGolden("eventhubs_capture", "eventhubscapture"),
		"broker_config":      coveredGolden("broker_config", "brokerconfig"),
		"cloudtest": {
			Surface:   "cloudtest",
			State:     "review_only",
			Rationale: "The converter remains fixture-gated until sanitized dependency-complete CloudTest exports establish stable wire-shape goldens.",
		},
		"oracle_ebusiness_review": {
			Surface:   "oracle_ebusiness_review",
			State:     "review_only",
			Rationale: "Capability launches the enterprise-archive review flow; it is not a distinct converter registration.",
		},
	}

	surfaceIDs := authoringSurfaceIDs(t, filepath.Join("..", "..", "api", "authoring_capabilities.go"))
	executedCases := make(map[string]bool)
	for _, goldenCase := range importerGoldenCases() {
		if executedCases[goldenCase.name] {
			t.Fatalf("golden harness registers converter %q more than once", goldenCase.name)
		}
		executedCases[goldenCase.name] = true
	}
	actual := make([]converterGoldenRegistration, 0, len(surfaceIDs))
	totals := map[string]int{"registered": len(surfaceIDs)}
	for _, surfaceID := range surfaceIDs {
		registration, ok := registrations[surfaceID]
		if !ok {
			t.Errorf("authoring import surface %q has no golden-inventory classification", surfaceID)
			continue
		}
		delete(registrations, surfaceID)
		if registration.State == "covered" {
			if !executedCases[registration.Converter] {
				t.Errorf("surface %q claims converter %q without an executed golden harness case", surfaceID, registration.Converter)
			}
			for _, golden := range []string{registration.Valid, registration.Diagnostic} {
				if _, err := os.Stat(filepath.Join("testdata", "golden", golden)); err != nil {
					t.Errorf("surface %q claims missing golden %q: %v", surfaceID, golden, err)
				}
			}
		}
		totals[registration.State]++
		actual = append(actual, registration)
	}
	for stale := range registrations {
		t.Errorf("golden-inventory classification %q is not registered by authoringImportSurfaces", stale)
	}
	for converter := range executedCases {
		covered := false
		for _, registration := range actual {
			if registration.State == "covered" && registration.Converter == converter {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("golden harness converter %q is not claimed by a covered authoring surface", converter)
		}
	}
	if t.Failed() {
		return
	}

	inventory := converterGoldenInventory{
		SchemaVersion: 1,
		Source:        "pkg/api/authoring_capabilities.go:authoringImportSurfaces",
		Totals:        totals,
		Surfaces:      actual,
	}
	encoded, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		t.Fatalf("marshal converter golden inventory: %v", err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join("..", "..", "..", "docs", "qa", "evidence", "M2", "converter-golden-inventory.json")
	if *updateImportGoldens {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("update %s: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run go test ./pkg/convert/importcontract -run 'TestImporterGoldenCorpus|TestConverterGoldenInventoryFreshness' -update)", path, err)
	}
	if string(want) != string(encoded) {
		t.Fatalf("%s is stale; regenerate with the M2 golden update command", path)
	}
}

func coveredGolden(surface, converter string) converterGoldenRegistration {
	return converterGoldenRegistration{
		Surface:    surface,
		Converter:  converter,
		Valid:      converter + ".valid.golden",
		Diagnostic: converter + ".invalid.golden",
		State:      "covered",
	}
}

func authoringSurfaceIDs(t *testing.T, path string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse authoring surface registry: %v", err)
	}
	var ids []string
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "authoringImportSurfaces" || function.Body == nil {
			continue
		}
		for _, statement := range function.Body.List {
			returned, ok := statement.(*ast.ReturnStmt)
			if !ok || len(returned.Results) != 1 {
				continue
			}
			registry, ok := returned.Results[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, element := range registry.Elts {
				surface, ok := element.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, field := range surface.Elts {
					pair, ok := field.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, keyOK := pair.Key.(*ast.Ident)
					value, valueOK := pair.Value.(*ast.BasicLit)
					if !keyOK || !valueOK || key.Name != "ID" || value.Kind != token.STRING {
						continue
					}
					var id string
					if err := json.Unmarshal([]byte(value.Value), &id); err != nil {
						t.Fatalf("decode authoring surface ID %s: %v", value.Value, err)
					}
					ids = append(ids, id)
					break
				}
			}
		}
	}
	if len(ids) == 0 {
		t.Fatal("authoringImportSurfaces returned no statically declared surface IDs")
	}
	return ids
}

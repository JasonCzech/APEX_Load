package api

import (
	"bytes"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"go/ast"
	"testing"
)

func TestBrowserEvidenceOpenAPIKeepsNestedTypedContracts(t *testing.T) {
	definitions := map[string]ast.Expr{}
	if err := addOpenAPICrossPackageTypeDefinitions(".", definitions); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"BrowserResourceEvidence", "BrowserDocumentObservation", "BrowserDocumentSummary", "BrowserDocumentEvidence", "BrowserRuntimeMetricAvailability"} {
		schema := openAPISchemaForASTTypeInPackage(&ast.SelectorExpr{X: ast.NewIdent("protocol"), Sel: ast.NewIdent(name)}, definitions, map[string]bool{}, 20, "api")
		if schema.Type != "object" || schema.Properties["version"].Type != "integer" {
			t.Fatalf("%s became opaque: %#v", name, schema)
		}
		switch name {
		case "BrowserResourceEvidence":
			if !schema.Properties["phases"].Nullable {
				t.Fatal("nil phase collection is not nullable")
			}
			if schema.Properties["phases"].Items == nil || schema.Properties["phases"].Items.Properties["start_ms"].Type != "number" || !schema.Properties["phases"].Items.Properties["start_ms"].Nullable || schema.Properties["bytes"].Properties["response_encoded_body"].Type != "integer" {
				t.Fatal("resource phases/byte presence lost")
			}
		case "BrowserDocumentObservation":
			if schema.Properties["cohort"].Properties["cache_enabled"].Type != "boolean" || !schema.Properties["cohort"].Properties["cache_enabled"].Nullable || schema.Properties["window_start"].Format != "date-time" || schema.Properties["value"].Type != "number" || !schema.Properties["value"].Nullable {
				t.Fatal("document cohort/time/value presence lost")
			}
		case "BrowserDocumentSummary":
			metrics := schema.Properties["metrics"]
			if !metrics.Nullable || !containsOpenAPIField(schema.Required, "metrics") {
				t.Fatal("summary metrics lost required presence or null shape")
			}
			if metrics.Items == nil || metrics.Items.Properties["distribution"].Properties["buckets"].Nullable || metrics.Items.Properties["eligible_final"].Type != "integer" || metrics.Items.Properties["distribution"].Properties["buckets"].Items == nil || metrics.Items.Properties["distribution"].Properties["buckets"].Items.Type != "integer" {
				t.Fatal("metric-specific populations/distribution became opaque")
			}
		case "BrowserDocumentEvidence":
			if !schema.Properties["observations"].Nullable || !containsOpenAPIField(schema.Required, "observations") {
				t.Fatal("document observations lost required nullable collection")
			}
		case "BrowserRuntimeMetricAvailability":
			if !schema.Properties["metrics"].Nullable {
				t.Fatal("nil runtime capabilities lost null shape")
			}
			if schema.Properties["metrics"].Items == nil || schema.Properties["metrics"].Items.Properties["available"].Type != "boolean" || schema.Properties["metrics"].Items.Properties["scope"].Type != "string" {
				t.Fatal("separate runtime capability lost")
			}
		}
	}

	for _, typed := range []any{protocol.BrowserDocumentSummary{Version: 2, Incomplete: true}, protocol.BrowserDocumentEvidence{Version: 2}, protocol.BrowserRuntimeMetricAvailability{Version: 1}, protocol.BrowserResourceEvidence{Version: 2}} {
		raw, err := json.Marshal(typed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte(":null")) {
			t.Fatalf("fixture no longer exercises nil wire collection: %s", raw)
		}
	}
	generic := openAPISchemaForASTTypeInPackage(&ast.ArrayType{Elt: ast.NewIdent("string")}, definitions, map[string]bool{}, 0, "api")
	if generic.Nullable {
		t.Fatal("bounded contract correction changed unrelated slice semantics")
	}
	definitions["protocol.BrowserDocumentSummary"] = &ast.StructType{Fields: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("Next")}, Type: ast.NewIdent("BrowserDocumentSummary")}}}}
	cyclic := openAPISchemaForASTTypeInPackage(ast.NewIdent("BrowserDocumentSummary"), definitions, map[string]bool{}, 20, "protocol")
	if cyclic.Properties["Next"].AdditionalProperties == nil {
		t.Fatal("bounded model bypassed cycle guard")
	}
	if completeBoundedEvidenceModel("protocol", "UserStructure") {
		t.Fatal("unbounded authored structure admitted")
	}
}

package api

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/browserresource"
	"github.com/jasonczech/apexload/pkg/librarycontract"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/runcontract"
	"github.com/jasonczech/apexload/pkg/telemetrypolicy"
)

func contractJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestClientRunContractMatchesControllerWire(t *testing.T) {
	zero := 0.0
	goal := &GoalConfig{MaxErrorPct: &zero, EarlyStop: &EarlyStopConfig{}}
	client := runcontract.CreateTestRequest{Name: "flow", Duration: "15s", ScenarioMode: "basic", GoalConfig: goal, ScriptID: "script", VUsers: 3, RampUp: "1s"}
	server := CreateTestRequest{Name: client.Name, Duration: client.Duration, ScenarioMode: client.ScenarioMode, GoalConfig: goal, ScriptID: client.ScriptID, VUsers: client.VUsers, RampUp: client.RampUp}
	if string(contractJSON(t, client)) != string(contractJSON(t, server)) {
		t.Fatalf("create projection drift: client %s server %s", contractJSON(t, client), contractJSON(t, server))
	}
	mix := protocol.HTTPVersionMix{Actions: 9007199254740993, ByVersion: map[string]int64{"3": 9007199254740993}, UpgradeEligible: 7, FellBack: 2, QUICAttempted: 9, QUICFailed: 3}
	actual := ManagedTest{ID: "run", RefKey: "alias", Name: "flow", Status: "COMPLETE", ProjectID: "project", Duration: 15 * time.Second, LiveMetrics: &LiveMetrics{TotalTx: 9007199254740993, TotalErrors: 2, TPS: 3.5, HTTPVersions: &RunArchiveHTTPVersionMix{HTTPVersionMix: mix}}}
	var projected runcontract.TestStatus
	if err := json.Unmarshal(contractJSON(t, actual), &projected); err != nil {
		t.Fatal(err)
	}
	want := runcontract.TestStatus{ID: actual.ID, RefKey: actual.RefKey, Name: actual.Name, Status: actual.Status, ProjectID: actual.ProjectID, Duration: actual.Duration, LiveMetrics: &runcontract.LiveMetrics{TotalTx: actual.LiveMetrics.TotalTx, TotalErrors: 2, TPS: 3.5, HTTPVersions: &runcontract.HTTPVersionMix{HTTPVersionMix: mix}}}
	if !reflect.DeepEqual(projected, want) {
		t.Fatalf("run projection drift: %#v", projected)
	}
}

func TestClientLibraryEnvelopePreservesControllerPayload(t *testing.T) {
	bundle := LibraryBundle{SchemaVersion: "apexload.library_bundle.v1", BundleID: "bundle", CreatedAt: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), Source: LibraryBundleSource{InstanceID: "source"}, Items: []LibraryBundleItem{{Kind: "scenario", Name: "scenario", Scenario: &LibraryScenarioPayload{ID: "scenario", Request: CreateScenarioRequest{Name: "scenario"}}}, {Kind: "result", Result: &LibraryResultPayload{Test: &ManagedTest{ID: "run", LiveMetrics: &LiveMetrics{TotalTx: 9007199254740993}}, ArchiveSizeBytes: 9007199254740993, Summary: map[string]any{"zero": float64(0), "empty": []any{}}}}}, Provenance: map[string]any{"metadata": map[string]any{"owner": "source", "nested": []any{"future"}}}}
	var client librarycontract.LibraryBundle
	if err := json.Unmarshal(contractJSON(t, bundle), &client); err != nil {
		t.Fatal(err)
	}
	forwarded := librarycontract.LibraryImportRequest{Bundle: client, ProjectID: "destination", Mode: "merge"}
	var actual LibraryImportRequest
	if err := json.Unmarshal(contractJSON(t, forwarded), &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual.Bundle, bundle) || actual.ProjectID != "destination" || actual.Mode != "merge" {
		t.Fatalf("library transfer changed payload: %s", contractJSON(t, actual))
	}
}

func TestClientPolicyAndResourceForwardsPreserveControllerSemantics(t *testing.T) {
	policy := captureTelemetryBudgetPolicy(RunArchiveExecutionSettingsSnapshot{}, false, time.Now())
	if !telemetrypolicy.Validate(policy) || telemetrypolicy.Hash(policy) != telemetryBudgetPolicyHash(policy) || telemetrypolicy.SustainedBreachWindow != telemetrySustainedBreachWindow {
		t.Fatal("captured controller policy diverged from client validation")
	}
	if maxArchiveQueryRecords != browserresource.MaxTransactionRecords {
		t.Fatal("browser page transaction bound drifted")
	}
	_, err := ParseBrowserResourcePageQuery(url.Values{"offset": {"1"}})
	if err != browserresource.ErrQuery {
		t.Fatal("query error identity changed")
	}
}

func TestClientBrowserDocumentArchiveProjectionMatchesControllerWire(t *testing.T) {
	summary := &protocol.BrowserDocumentSummary{Version: 2}
	actual := RunArchiveReport{Manifest: &RunArchiveManifest{TestID: "run", Provenance: RunArchiveProvenance{ProjectID: "project"}}, BrowserDocuments: summary, BrowserDocumentEvidenceTruncated: true}
	var projected struct {
		Manifest *struct {
			TestID     string `json:"test_id"`
			Provenance struct {
				ProjectID string `json:"project_id"`
			} `json:"provenance"`
		} `json:"manifest"`
		BrowserDocuments *protocol.BrowserDocumentSummary `json:"browser_documents"`
		Truncated        bool                             `json:"browser_document_evidence_truncated"`
	}
	if err := json.Unmarshal(contractJSON(t, actual), &projected); err != nil {
		t.Fatal(err)
	}
	if projected.Manifest == nil || projected.Manifest.TestID != "run" || projected.Manifest.Provenance.ProjectID != "project" || !projected.Truncated || !reflect.DeepEqual(projected.BrowserDocuments, summary) {
		t.Fatal("archive projection wire drift")
	}
}

func TestOpenAPIForwardedClientContractsPreserveModelDepth(t *testing.T) {
	definitions := map[string]ast.Expr{}
	files, err := parseGoSourceDirectory(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	aliases := map[string]string{}
	for _, file := range files["api"] {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typ := spec.(*ast.TypeSpec)
				definitions[typ.Name.Name] = typ.Type
				if sel, ok := typ.Type.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok {
						switch pkg.Name {
						case "runcontract", "archivecontract", "browserresource", "telemetrypolicy", "librarycontract":
							aliases[typ.Name.Name] = pkg.Name + "." + sel.Sel.Name
						}
					}
				}
			}
		}
	}
	if len(aliases) < 30 {
		t.Fatalf("expected real forwarding declarations, got %d", len(aliases))
	}
	if err := addOpenAPICrossPackageTypeDefinitions(".", definitions); err != nil {
		t.Fatal(err)
	}
	// Before the extraction these declarations appeared directly in pkg/api.
	// Simulate that layout independently, including recursion-budget boundaries.
	before := map[string]ast.Expr{}
	for name, definition := range definitions {
		before[name] = definition
	}
	for name, qualified := range aliases {
		before[name] = definitions[qualified]
	}
	for name := range aliases {
		for _, depth := range []int{0, 5, 8} {
			got := openAPISchemaForASTType(ast.NewIdent(name), definitions, map[string]bool{}, depth)
			want := openAPISchemaForASTType(ast.NewIdent(name), before, map[string]bool{}, depth)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("forwarded %s altered schema at depth %d", name, depth)
			}
		}
	}
	goal := openAPISchemaForASTType(ast.NewIdent("GoalConfig"), definitions, map[string]bool{}, 0)
	if goal.Properties["max_error_pct"].Type != "number" || goal.Properties["early_stop"].Properties["enabled"].Type != "boolean" {
		t.Fatal("goal fields became opaque")
	}

}

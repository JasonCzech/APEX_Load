package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// production_options_test.go covers the ConvertOptions shape production actually
// uses. The overwhelming majority of this package's Convert/ConvertZip call sites
// pass the zero value ConvertOptions{}, but no production caller ever does:
// pkg/api/handlers_import_enterprise.go builds
//
//	ConvertOptions{
//	    ScriptName:           r.FormValue("name"),
//	    BaseURL:              r.FormValue("base_url"),
//	    IncludeAssertions:    r.FormValue("include_assertions") == "true",
//	    PreserveTransactions: parseBoolDefault(r.FormValue("preserve_transactions"), true),
//	}
//
// so the shipped default is PreserveTransactions=TRUE, not false. Everything below
// exercises that reachable option space rather than the zero value.

// productionImportConvertOptions mirrors the enterprise-archive import handler's
// default option set (no name/base override supplied by the operator, assertions
// off, transactions preserved). Keep in sync with
// pkg/api/handlers_import_enterprise.go.
func productionImportConvertOptions() ConvertOptions {
	return ConvertOptions{PreserveTransactions: true}
}

// productionReachableOptionMatrix enumerates the option combinations the import
// form can actually produce. ScriptName/BaseURL are free-text form fields and
// IncludeAssertions/PreserveTransactions are checkboxes, so these four rows span
// the reachable space for a Web (HTTP/HTML) archive.
func productionReachableOptionMatrix() []struct {
	name string
	opts ConvertOptions
} {
	return []struct {
		name string
		opts ConvertOptions
	}{
		{"handler_default", ConvertOptions{PreserveTransactions: true}},
		{"assertions_on", ConvertOptions{PreserveTransactions: true, IncludeAssertions: true}},
		{"transactions_unchecked", ConvertOptions{PreserveTransactions: false, IncludeAssertions: true}},
		{"named_and_based", ConvertOptions{
			ScriptName:           "Operator_Named_Script",
			BaseURL:              "https://example.com",
			PreserveTransactions: true,
			IncludeAssertions:    true,
		}},
	}
}

// requestShape is the option-independent identity of an emitted request: which
// requests exist, in which order, with which method and target. Options may add
// assertions or change how a request is NAMED (preserve_transactions prefixes
// nested requests with their transaction); they must never add, drop, reorder or
// repoint a request, so Name is deliberately excluded and pinned separately by
// TestProductionPreserveTransactionsOnlyChangesNaming.
type requestShape struct {
	Path   string
	Type   string
	Method string
	Target string
}

func collectRequestShapes(t *testing.T, actions []dsl.Action, prefix string) []requestShape {
	t.Helper()
	shapes := make([]requestShape, 0, len(actions))
	for index, action := range actions {
		path := fmt.Sprintf("%s[%d]", prefix, index)
		shapes = append(shapes, requestShape{
			Path:   path,
			Type:   action.Type,
			Method: action.Method,
			Target: action.Target,
		})
		if action.Type != "http_sequence" {
			continue
		}
		for _, child := range sequenceChildActions(t, action) {
			shapes = append(shapes, requestShape{
				Path:   path + ".requests",
				Type:   child.Type,
				Method: child.Method,
				Target: child.Target,
			})
		}
	}
	return shapes
}

// sequenceChildActions decodes the nested requests of an http_sequence wrapper.
func sequenceChildActions(t *testing.T, sequence dsl.Action) []dsl.Action {
	t.Helper()
	raw, ok := sequence.Params["requests"]
	if !ok {
		return nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal sequence requests: %v", err)
	}
	var children []dsl.Action
	if err := json.Unmarshal(payload, &children); err != nil {
		t.Fatalf("unmarshal sequence requests: %v", err)
	}
	return children
}

// TestProductionOptionsPreserveRequestShape is the cross-option invariant: the set,
// order, method and target of every emitted request must be identical for every
// option combination the import form can produce. Options are presentation and
// policy knobs; a knob that silently changes which requests replay is a regression.
func TestProductionOptionsPreserveRequestShape(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	var reference []requestShape
	var referenceName string
	for _, tc := range productionReachableOptionMatrix() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := Convert([]*ParsedAction{parsed}, tc.opts)
			if err != nil {
				t.Fatalf("convert error: %v", err)
			}
			if len(result.Warnings) != 0 {
				t.Fatalf("warnings = %#v, want none for the production option set", result.Warnings)
			}
			// Production ships these scripts straight to the runner, so they must
			// validate under every reachable option combination.
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("dsl.ValidateScriptIssues = %#v, want none", issues)
			}
			shapes := collectRequestShapes(t, result.Script.Actions, "actions")
			if len(shapes) == 0 {
				t.Fatalf("no request shapes collected from %#v", result.Script.Actions)
			}
			if reference == nil {
				reference = shapes
				referenceName = tc.name
				return
			}
			if len(shapes) != len(reference) {
				t.Fatalf("option set %q emitted %d actions, %q emitted %d — options changed the executable shape\n%#v\nvs\n%#v",
					tc.name, len(shapes), referenceName, len(reference), shapes, reference)
			}
			for index := range shapes {
				if shapes[index] != reference[index] {
					t.Fatalf("option set %q action %d = %#v, %q produced %#v — options changed the executable shape",
						tc.name, index, shapes[index], referenceName, reference[index])
				}
			}
		})
	}
}

// TestProductionDefaultOptionsPreserveTransactionStructure pins what the shipped
// default (PreserveTransactions=true) actually produces, since every other test in
// this package measures the false path.
func TestProductionDefaultOptionsPreserveTransactionStructure(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, productionImportConvertOptions())
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	want := []struct {
		name       string
		actionType string
	}{
		{"Start_Login_transaction", "source_transaction"},
		{"Login", "http_sequence"},
		{"End_Login_transaction", "source_transaction"},
		{"API_GetOrders", "GET"},
		{"API_CreateOrder", "POST"},
	}
	if len(result.Script.Actions) != len(want) {
		t.Fatalf("actions = %#v, want %d transaction-bounded actions", result.Script.Actions, len(want))
	}
	for index, expected := range want {
		action := result.Script.Actions[index]
		if action.Name != expected.name || action.Type != expected.actionType {
			t.Fatalf("action[%d] = (%q,%q), want (%q,%q)", index, action.Name, action.Type, expected.name, expected.actionType)
		}
	}
	// The two requests inside the Login transaction stay inside it, so the
	// transaction timer still brackets exactly the recorded scope.
	children := sequenceChildActions(t, result.Script.Actions[1])
	childNames := make([]string, 0, len(children))
	for _, child := range children {
		childNames = append(childNames, child.Name)
		if child.Metadata["source_transaction"] != "Login" {
			t.Fatalf("sequence child %q source_transaction = %#v, want Login", child.Name, child.Metadata["source_transaction"])
		}
	}
	if strings.Join(childNames, ",") != "Login_Homepage,Login_Login_Submit" {
		t.Fatalf("sequence children = %v, want the two recorded Login requests in order", childNames)
	}
}

// TestProductionPreserveTransactionsOnlyChangesNaming pins the one thing the
// shipped-default option actually changes for this fixture: nested requests are
// prefixed with their transaction name. The transaction boundary actions, the
// sequence wrapper and every target are identical either way, so unchecking the box
// in the import form is a labelling choice — not a different script.
func TestProductionPreserveTransactionsOnlyChangesNaming(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	preserved, err := Convert([]*ParsedAction{parsed}, ConvertOptions{PreserveTransactions: true})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	flat, err := Convert([]*ParsedAction{parsed}, ConvertOptions{PreserveTransactions: false})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	names := func(result *ConvertResult) []string {
		out := make([]string, 0)
		for _, child := range sequenceChildActions(t, result.Script.Actions[1]) {
			out = append(out, child.Name)
		}
		return out
	}
	if got := strings.Join(names(preserved), ","); got != "Login_Homepage,Login_Login_Submit" {
		t.Fatalf("preserve_transactions=true nested names = %q, want transaction-prefixed names", got)
	}
	if got := strings.Join(names(flat), ","); got != "Homepage,Login_Submit" {
		t.Fatalf("preserve_transactions=false nested names = %q, want unprefixed recorded step names", got)
	}
	// The transaction boundaries themselves survive either way — unchecking the box
	// does not discard the recorded transaction structure.
	for _, result := range []*ConvertResult{preserved, flat} {
		if result.Script.Actions[0].Type != "source_transaction" || result.Script.Actions[2].Type != "source_transaction" {
			t.Fatalf("transaction boundaries = %#v, want preserved regardless of the option", result.Script.Actions)
		}
	}
}

// TestProductionIncludeAssertionsCoversEveryRequest proves the include_assertions
// checkbox reaches nested sequence children too — the empty-options tests never
// look, and an assertion that only lands on top-level actions would leave every
// transaction-wrapped request unchecked in production.
func TestProductionIncludeAssertionsCoversEveryRequest(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	off, err := Convert([]*ParsedAction{parsed}, productionImportConvertOptions())
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	on, err := Convert([]*ParsedAction{parsed}, ConvertOptions{PreserveTransactions: true, IncludeAssertions: true})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	statusAssertions := func(result *ConvertResult) map[string]int {
		counts := map[string]int{}
		visit := func(action dsl.Action) {
			for _, assertion := range action.Assertions {
				if assertion.Type == "status_code" {
					counts[action.Name]++
				}
			}
		}
		for _, action := range result.Script.Actions {
			if action.Type == "http_sequence" {
				for _, child := range sequenceChildActions(t, action) {
					visit(child)
				}
				continue
			}
			if action.Type == "source_transaction" {
				continue
			}
			visit(action)
		}
		return counts
	}

	if got := statusAssertions(off); len(got) != 0 {
		t.Fatalf("include_assertions=false produced status_code assertions on %#v, want none", got)
	}
	got := statusAssertions(on)
	wantNames := []string{"API_CreateOrder", "API_GetOrders", "Login_Homepage", "Login_Login_Submit"}
	gotNames := make([]string, 0, len(got))
	for name, count := range got {
		gotNames = append(gotNames, name)
		if count != 1 {
			t.Fatalf("action %q has %d status_code assertions, want exactly 1", name, count)
		}
	}
	sort.Strings(gotNames)
	if strings.Join(gotNames, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("include_assertions=true covered %v, want every request including the transaction-wrapped ones %v", gotNames, wantNames)
	}
}

// TestProductionScriptNameAndBaseURLOverrides pins the two free-text form fields.
//
// Note the deliberate asymmetry on BaseURL: a base that matches the recorded origin
// relativizes targets, while a base pointing somewhere else leaves the recorded
// absolute URLs untouched. The importer never silently repoints recorded traffic at
// a different host just because the operator typed one into the form.
func TestProductionScriptNameAndBaseURLOverrides(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	defaulted, err := Convert([]*ParsedAction{parsed}, productionImportConvertOptions())
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if defaulted.Script.Name != "Converted_Enterprise_Script_Archive" {
		t.Fatalf("default script name = %q", defaulted.Script.Name)
	}
	if defaulted.Script.Config.BaseURL != "https://example.com" {
		t.Fatalf("auto-detected base URL = %q, want the recorded origin", defaulted.Script.Config.BaseURL)
	}

	named, err := Convert([]*ParsedAction{parsed}, ConvertOptions{
		ScriptName:           "Operator_Named_Script",
		BaseURL:              "https://example.com",
		PreserveTransactions: true,
	})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if named.Script.Name != "Operator_Named_Script" {
		t.Fatalf("script name = %q, want the operator override", named.Script.Name)
	}
	if got := named.Script.Actions[3].Target; got != "/api/v1/orders" {
		t.Fatalf("same-origin base override target = %q, want a relative target", got)
	}

	rebased, err := Convert([]*ParsedAction{parsed}, ConvertOptions{
		BaseURL:              "https://override.test",
		PreserveTransactions: true,
	})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if rebased.Script.Config.BaseURL != "https://override.test" {
		t.Fatalf("base URL = %q, want the operator override", rebased.Script.Config.BaseURL)
	}
	if got := rebased.Script.Actions[3].Target; got != "https://example.com/api/v1/orders" {
		t.Fatalf("cross-origin base override target = %q, want the recorded absolute URL preserved (traffic must not be silently repointed)", got)
	}
}

// TestConvertZipUnderProductionOptions runs the archive entry point the handler
// actually calls with the handler's own options, so the ConvertZip-only work
// (parameter attachment, credential projection, masked-body secret references)
// is exercised with PreserveTransactions=true rather than the zero value.
func TestConvertZipUnderProductionOptions(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"Prod.usr": "[General]\nType=Web\n\n[Actions]\nAction0=Action.c\n",
		"Action.c": sampleActionC,
	})

	for _, tc := range productionReachableOptionMatrix() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertZip(archive, tc.opts)
			if err != nil {
				t.Fatalf("ConvertZip returned error: %v", err)
			}
			if result.Script == nil {
				t.Fatalf("ConvertZip produced no script: %#v", result)
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("dsl.ValidateScriptIssues = %#v, want none", issues)
			}
			if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
				t.Fatalf("source review = %#v, want mappings under production options", result.SourceReview)
			}
			// The correlation recorded before the first request must survive the
			// archive path under production options, not just ConvertOptions{}.
			children := sequenceChildActions(t, result.Script.Actions[1])
			if len(children) == 0 {
				t.Fatalf("sequence children = %#v, want the recorded Login requests", result.Script.Actions[1])
			}
			if len(children[0].Extractors) != 1 || children[0].Extractors[0].Name != "sessionid" {
				t.Fatalf("extractors = %#v, want the sessionid correlation preserved", children[0].Extractors)
			}
		})
	}
}

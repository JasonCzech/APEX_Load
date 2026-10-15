package api

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	gvpDescriptor  = "serviceComponent://ui.force.components.controllers.recordGlobalValueProvider.RecordGvpController/ACTION$getRecord"
	apexDescriptor = "aura://ApexActionController/ACTION$execute"
)

func auraBody(actions ...string) string {
	var parts []string
	for index, action := range actions {
		if strings.Contains(action, ".") && !strings.Contains(action, "://") {
			class, method, _ := strings.Cut(action, ".")
			parts = append(parts, `{"id":"`+string(rune('1'+index))+`;a","descriptor":"`+apexDescriptor+`","params":{"namespace":"","classname":"`+class+`","method":"`+method+`","params":{"term":"{{term}}"}}}`)
			continue
		}
		parts = append(parts, `{"id":"`+string(rune('1'+index))+`;a","descriptor":"`+action+`","params":{"recordDescriptor":"x"}}`)
	}
	return "message=" + url.QueryEscape(`{"actions":[`+strings.Join(parts, ",")+`]}`) + "&aura.token={{corr_token}}"
}

func auraRequest(name, body string) dsl.Action {
	return dsl.Action{Name: name, Type: "request", Method: "POST", Target: "/aura?r=12&ui-force-components-controllers.recordGlobalValueProvider.RecordGvp.getRecord=1", Body: body}
}

// Only a run with browsers freezes its HTTP groups' Aura requests: each
// /aura POST by action name, its actions named (Apex by Class.method),
// nested requests included, ambiguous names dropped.
func TestDeclaredAuraHTTPRequests(t *testing.T) {
	httpScript := &dsl.Script{Protocol: "http", Actions: []dsl.Action{
		auraRequest("Open account aura", auraBody(gvpDescriptor, "AccountSearch.find")),
		{Name: "Page", Type: "request", Method: "GET", Target: "/lightning/page/home"},
		auraRequest("Twice", auraBody(gvpDescriptor)),
		auraRequest("Twice", auraBody("AccountSearch.find")),
		{Name: "Search", Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{auraRequest("Search aura", auraBody("Orders.submit"))}}},
		{Name: "Burst", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "Burst aura", "type": "request", "method": "POST", "target": "/s/sfsites/aura", "body": auraBody(gvpDescriptor)}}}},
	}}
	browserScript := &dsl.Script{Protocol: "browser"}
	test := &ManagedTest{groupSpecs: []engine.GroupConfig{{Name: "observers", Script: browserScript}, {Name: "load", Script: httpScript}}}
	var got []string
	for _, request := range declaredAuraHTTPRequests(test) {
		got = append(got, request.Group+"|"+request.Action+"|"+strings.Join(request.Actions, "+"))
	}
	want := "load|Burst aura|" + gvpDescriptor + ",load|Open account aura|" + gvpDescriptor + "+AccountSearch.find,load|Search aura|Orders.submit"
	if strings.Join(got, ",") != want {
		t.Fatalf("declared = %v", got)
	}
	if requests := declaredAuraHTTPRequests(&ManagedTest{groupSpecs: []engine.GroupConfig{{Name: "load", Script: httpScript}}}); requests != nil {
		t.Fatalf("a run without browsers froze %v", requests)
	}
}

func browserAuraEvent(transaction string, vuser int, iteration int64, actions ...string) TransactionEvent {
	var list []any
	for _, name := range actions {
		list = append(list, map[string]any{"name": name, "batch_size": float64(len(actions)), "bytes": float64(9000)})
	}
	return TransactionEvent{Group: "observers", SourceTransaction: transaction, VUserID: vuser, Iteration: iteration, ActionName: "click", Population: "action",
		ProtocolEvidence: map[string]any{"aura_actions": map[string]any{"requests": float64(1), "actions": list}}}
}

func httpAuraEvent(transaction, action string, vuser int, iteration int64) TransactionEvent {
	return TransactionEvent{Group: "load", SourceTransaction: transaction, VUserID: vuser, Iteration: iteration, ActionName: action, Population: "action", BytesReceived: 4000}
}

// Per transaction, the report sets the browsers' median calls per instance
// beside the HTTP scripts', flags differences above one call, and lists the
// transactions both sides ran, in HTML, PDF text and CSV.
func TestArchiveReportShowsAuraParity(t *testing.T) {
	rollup := newArchiveAuraParityAccumulator([]RunArchiveAuraHTTPRequest{
		{Group: "load", Action: "Open account aura", Actions: []string{gvpDescriptor, "AccountSearch.find"}},
		{Group: "load", Action: "Search aura", Actions: []string{"Orders.submit"}},
	})
	for _, event := range []TransactionEvent{
		// Browsers: the record once, the search three times per instance.
		browserAuraEvent("Open account", 1, 1, gvpDescriptor, "AccountSearch.find"),
		browserAuraEvent("Open account", 1, 1, "AccountSearch.find", "AccountSearch.find"),
		browserAuraEvent("Open account", 2, 1, gvpDescriptor, "AccountSearch.find"),
		browserAuraEvent("Open account", 2, 1, "AccountSearch.find", "AccountSearch.find"),
		// HTTP: one request with both actions per instance, plus an action
		// the browsers never sent.
		httpAuraEvent("Open account", "Open account aura", 7, 1),
		httpAuraEvent("Open account", "Search aura", 7, 1),
		httpAuraEvent("Open account", "Search aura", 7, 1),
		httpAuraEvent("Open account", "Open account aura", 8, 1),
		httpAuraEvent("Open account", "Search aura", 8, 1),
		httpAuraEvent("Open account", "Search aura", 8, 1),
		// Only one side ran it: left out.
		browserAuraEvent("Browse", 1, 1, gvpDescriptor),
		// Not an Aura request, or not in a transaction.
		httpAuraEvent("Open account", "Page", 7, 1),
		httpAuraEvent("", "Open account aura", 7, 1),
	} {
		rollup.merge(event)
	}
	rows := rollup.result()
	if len(rows) != 1 || rows[0].Transaction != "Open account" || rows[0].BrowserInstances != 2 || rows[0].HTTPInstances != 2 ||
		rows[0].BrowserRequests != 2 || rows[0].HTTPRequests != 3 || rows[0].BrowserActionsPerRequest != 2 || rows[0].HTTPBytesPerRequest != 4000 {
		t.Fatalf("rows = %+v", rows)
	}
	var actions []string
	for _, action := range rows[0].Actions {
		actions = append(actions, action.Name+"|"+formatCSVFloat(action.Browser)+"|"+formatCSVFloat(action.HTTP)+"|"+map[bool]string{true: "flagged", false: "ok"}[action.Flagged])
	}
	if strings.Join(actions, ",") != "AccountSearch.find|3|1|flagged,Orders.submit|0|2|flagged,"+gvpDescriptor+"|1|1|ok" {
		t.Fatalf("actions = %v", actions)
	}

	report := consistencyMatrixReport()
	report.Overview.Browser = nil
	report.AuraParity = rows
	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Aura parity") || !strings.Contains(string(html), "Orders.submit") {
		t.Fatal("the HTML report does not show the Aura parity")
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), "aura_parity:Open account") || !strings.Contains(csv.String(), "browser=0;http=2;flagged=true") {
		t.Fatalf("CSV:\n%s", csv.String())
	}
	lines := []string{}
	buildReportSectionRegistry()["browser"].PDF(&runArchiveHTMLReportData{Report: report}, &lines)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "Open account: browsers 2.0 requests") || !strings.Contains(text, "differs: Orders.submit browser 0.0, HTTP 2.0") {
		t.Fatalf("PDF lines:\n%s", text)
	}
}

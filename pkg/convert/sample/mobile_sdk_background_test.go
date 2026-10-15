package sample

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// mobileAppHAR is a capture shaped like a Salesforce Mobile SDK app's: Apex
// REST calls with no page loads, a status endpoint polled every 5.5 s in two
// bursts, a long-polled permission check, and the app's logging and
// analytics calls between the user's steps.
func mobileAppHAR(t *testing.T) string {
	t.Helper()
	const host = "https://acme.my.salesforce.com"
	stamp := func(ms int) string {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(ms) * time.Millisecond).Format("2006-01-02T15:04:05.000Z")
	}
	entry := func(ms, duration int, method, path string) map[string]any {
		return recorderEntry(stamp(ms), method, host+path, 200, "application/json", `{"ok":true}`, map[string]any{"time": duration})
	}
	var entries []map[string]any
	add := func(ms, duration int, method, path string) {
		entries = append(entries, entry(ms, duration, method, path))
	}
	add(0, 200, "GET", "/services/apexrest/mobile/storeContext")
	// Search: the user's request, then the app's logging while the status
	// poll and the permission long-poll run.
	add(1000, 300, "POST", "/services/apexrest/mobile/customerSearch")
	add(1400, 150, "POST", "/services/apexrest/mobile/nebulaLogger")
	for i := range 4 {
		add(2000+i*5500, 60, "GET", "/services/apexrest/mobile/managerOverride/requests?manager=ab1")
	}
	for i := range 3 {
		add(3000+i*2100, 2000, "GET", "/services/apexrest/visibilityChecks/customPermission")
	}
	add(9000, 120, "POST", "/services/apexrest/mobile/analyticsEvent")
	// Submit, with a second burst of the status poll.
	add(30000, 400, "POST", "/services/apexrest/mobile/submit")
	add(30500, 180, "POST", "/services/data/v66.0/connect/proxy/app-analytics-logging")
	for i := range 3 {
		add(31000+i*5500, 60, "GET", "/services/apexrest/mobile/managerOverride/requests?manager=ab1")
	}
	add(48000, 200, "POST", "/services/apexrest/mobile/logout")
	return recorderHAR(t, entries...)
}

// A capture shaped like SearchAuth converts with its polls and long-poll as
// background conversations (one per burst), the app's telemetry as background
// requests, and transactions that time only the user's own requests.
func TestMobileAppCaptureModelsBackgroundTraffic(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	window := func(name string, from, to int, order int) TransactionWindow {
		return TransactionWindow{Name: name, Start: base.Add(time.Duration(from) * time.Millisecond), End: base.Add(time.Duration(to) * time.Millisecond), StartOrder: order, EndOrder: order + 1}
	}
	result, err := Convert(ImportRequest{Format: "har", Content: mobileAppHAR(t), Options: ConvertOptions{
		HighConfidenceOnly: true, OmitSourceReview: true,
		Transactions: []TransactionWindow{window("Search", 900, 20000, 0), window("Submit", 29900, 47000, 2)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var shape []string
	counts := map[string]int{}
	for _, action := range result.Script.Actions {
		switch action.Type {
		case "source_transaction":
			shape = append(shape, action.Name)
		case "http_async_start":
			pattern, _ := action.Params["pattern"].(string)
			counts[pattern]++
			request, _ := action.Params["request"].(map[string]any)
			target, _ := request["target"].(string)
			shape = append(shape, pattern+" "+target)
		case "http_async_stop":
			shape = append(shape, "stop")
		default:
			shape = append(shape, action.Method+" "+action.Target)
		}
	}
	if counts["poll"] != 2 || counts["long_poll"] != 1 || counts["once"] != 3 {
		t.Fatalf("conversations = %v, want 2 polls, 1 long-poll and 3 background requests:\n%s", counts, strings.Join(shape, "\n"))
	}
	// Transaction markers wrap only the user's requests: a background start
	// never anchors a start or an end.
	joined := strings.Join(shape, "\n")
	for _, want := range []string{
		"Start Search transaction\nPOST /services/apexrest/mobile/customerSearch",
		"Start Submit transaction\nPOST /services/apexrest/mobile/submit",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("the converted script must validate: %v\n%s", issues, joined)
	}
	warnings := strings.Join(result.Warnings, "\n")
	for _, want := range []string{`matching background request pattern "nebulaLogger"`, `matching background request pattern "analyticsEvent"`, `matching background request pattern "connect/proxy/app-analytics-logging"`} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("warnings miss %q:\n%s", want, warnings)
		}
	}
}

package sample

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func stepTransactionHAR(t *testing.T) string {
	t.Helper()
	entry := func(started, method, url, mimeType, body string) map[string]any {
		request := map[string]any{"method": method, "url": url, "headers": []any{}}
		if body != "" {
			request["postData"] = map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": body}
		}
		return map[string]any{
			"startedDateTime": started, "time": 100,
			"request":  request,
			"response": map[string]any{"status": 200, "headers": []any{map[string]any{"name": "Content-Type", "value": mimeType}}, "content": map[string]any{"mimeType": mimeType, "text": "ok"}},
		}
	}
	aura := "message=%7B%7D&aura.pageURI=%2Flightning%2Fn%2FCustomer_Search"
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		// Step 1: the login page and its redirect, no pause between them.
		entry("2026-01-01T00:00:00.000Z", "GET", "https://app.test/login", "text/html", ""),
		entry("2026-01-01T00:00:00.150Z", "GET", "https://app.test/home/home.jsp", "text/html", ""),
		entry("2026-01-01T00:00:00.300Z", "GET", "https://app.test/home/home.jsp?ajax=1", "application/json", ""),
		// A 3-second pause, then step 2: two Aura requests on the search page.
		entry("2026-01-01T00:00:03.500Z", "POST", "https://app.test/aura?r=1", "application/json", aura),
		entry("2026-01-01T00:00:03.650Z", "POST", "https://app.test/aura?r=2", "application/json", aura),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// With step transactions on, a recording without marked transactions is split
// at its recorded pauses, each step timed as a transaction named for the page
// most of its requests show; redirects stay in the step that caused them.
func TestStepTransactionsSplitARecordingAtItsPauses(t *testing.T) {
	on := true
	result, err := Convert(ImportRequest{Name: "Steps", Format: "har", Content: stepTransactionHAR(t), Options: ConvertOptions{StepTransactions: &on}})
	if err != nil {
		t.Fatal(err)
	}
	var markers []string
	tagged := map[string]int{}
	for _, action := range result.Script.Actions {
		if action.Type == "source_transaction" {
			markers = append(markers, action.Name)
			continue
		}
		name, _ := action.Metadata[harSourceTransactionMeta].(string)
		tagged[name]++
	}
	if tagged["01_home"] != 3 || tagged["02_Customer_Search"] != 2 || len(tagged) != 2 {
		t.Fatalf("tagged = %v, want 01_home (3 requests) and 02_Customer_Search (2)", tagged)
	}
	if len(markers) != 4 {
		t.Fatalf("markers = %q, want a start and an end for each step", markers)
	}

	off, err := Convert(ImportRequest{Name: "Steps", Format: "har", Content: stepTransactionHAR(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range off.Script.Actions {
		if action.Type == "source_transaction" {
			t.Fatalf("step transactions were added without the option: %#v", action)
		}
	}
}

func TestStepTransactionLabels(t *testing.T) {
	for raw, want := range map[string]string{
		"https://app.test/":                                           "root",
		"https://app.test/secur/frontdoor.jsp?x=1":                    "frontdoor",
		"/lightning/n/Customer_Search":                                "Customer_Search",
		"https://app.test/one/one.app":                                "one",
		"https://app.test/a b/%E2%9C%93":                              "a_b",
		"https://login.test/id/00D5e000000AbCdEAK/0055e000001XyZwAAC": "id",
		"https://app.test/s/detail/a0B5e00000ABCDEFGH":                "detail",
		"https://app.test/12345678":                                   "step",
	} {
		if got := stepTransactionLabel(raw); got != want {
			t.Errorf("stepTransactionLabel(%q) = %q, want %q", raw, got, want)
		}
	}
}

func stepEntry(started, method, url, mimeType, body string) map[string]any {
	request := map[string]any{"method": method, "url": url, "headers": []any{}}
	if body != "" {
		request["postData"] = map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": body}
	}
	return map[string]any{
		"startedDateTime": started, "time": 100,
		"request":  request,
		"response": map[string]any{"status": 200, "headers": []any{map[string]any{"name": "Content-Type", "value": mimeType}}, "content": map[string]any{"mimeType": mimeType, "text": "ok"}},
	}
}

func stepHAR(t *testing.T, entries ...any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": entries}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func stepMarkersAndTags(t *testing.T, content string) ([]string, map[string]int) {
	t.Helper()
	on := true
	result, err := Convert(ImportRequest{Name: "Steps", Format: "har", Content: content, Options: ConvertOptions{StepTransactions: &on}})
	if err != nil {
		t.Fatal(err)
	}
	var markers []string
	tagged := map[string]int{}
	for _, action := range result.Script.Actions {
		if action.Type == "source_transaction" {
			markers = append(markers, action.Name)
			continue
		}
		name, _ := action.Metadata[harSourceTransactionMeta].(string)
		tagged[name]++
	}
	return markers, tagged
}

// A page polling in the background while the user pauses does not split the
// step: the polls fold into a background conversation and start no step.
func TestStepTransactionsIgnoreBackgroundPolls(t *testing.T) {
	entries := []any{stepEntry("2026-01-01T00:00:00.000Z", "GET", "https://app.test/home", "text/html", "")}
	for second := 5; second <= 25; second += 5 {
		entries = append(entries, stepEntry(fmt.Sprintf("2026-01-01T00:00:%02d.000Z", second), "GET", "https://app.test/api/poll", "application/json", ""))
	}
	entries = append(entries,
		stepEntry("2026-01-01T00:00:26.000Z", "GET", "https://app.test/search", "text/html", ""),
		stepEntry("2026-01-01T00:00:26.150Z", "GET", "https://app.test/api/b", "application/json", ""))
	markers, _ := stepMarkersAndTags(t, stepHAR(t, entries...))
	want := []string{"Start 01_home transaction", "End 01_home transaction", "Start 02_search transaction", "End 02_search transaction"}
	if strings.Join(markers, "|") != strings.Join(want, "|") {
		t.Fatalf("markers = %q, want %q", markers, want)
	}
}

// A recording the recorder already tagged with the tester's transactions keeps
// them; step transactions are not added.
func TestStepTransactionsKeepRecordedTransactions(t *testing.T) {
	tagged := func(entry map[string]any, name string) map[string]any {
		entry[harTransactionKey] = name
		return entry
	}
	markers, tags := stepMarkersAndTags(t, stepHAR(t,
		tagged(stepEntry("2026-01-01T00:00:00.000Z", "GET", "https://app.test/home", "text/html", ""), "Browse"),
		tagged(stepEntry("2026-01-01T00:00:03.000Z", "GET", "https://app.test/search", "text/html", ""), "Search"),
	))
	if len(markers) != 0 || tags["Browse"] != 1 || tags["Search"] != 1 {
		t.Fatalf("markers = %q, tags = %v, want the recorded transactions untouched", markers, tags)
	}
}

// Entries a recorder appends in completion order still all belong to their
// step: the window spans the earliest to latest start.
func TestStepTransactionsSpanEntriesOutOfStartOrder(t *testing.T) {
	_, tags := stepMarkersAndTags(t, stepHAR(t,
		stepEntry("2026-01-01T00:00:00.000Z", "GET", "https://app.test/home", "text/html", ""),
		stepEntry("2026-01-01T00:00:03.100Z", "GET", "https://app.test/api/b", "application/json", ""),
		stepEntry("2026-01-01T00:00:03.000Z", "GET", "https://app.test/search", "text/html", ""),
		stepEntry("2026-01-01T00:00:03.200Z", "GET", "https://app.test/api/c", "application/json", ""),
	))
	if tags["02_search"] != 3 || tags[""] != 0 {
		t.Fatalf("tags = %v, want the three second-step requests tagged 02_search", tags)
	}
}

// A request held open for a long time (a CometD long poll, a slow download)
// does not hold its step open across later clicks: a pause is measured from
// the latest request that had finished.
func TestStepTransactionsAreNotMergedByALongHeldRequest(t *testing.T) {
	longPoll := stepEntry("2026-01-01T00:00:00.500Z", "GET", "https://app.test/cometd/connect", "application/json", "")
	longPoll["time"] = 110000
	markers, _ := stepMarkersAndTags(t, stepHAR(t,
		stepEntry("2026-01-01T00:00:00.000Z", "GET", "https://app.test/home", "text/html", ""),
		longPoll,
		stepEntry("2026-01-01T00:00:10.000Z", "GET", "https://app.test/search", "text/html", ""),
		stepEntry("2026-01-01T00:00:20.000Z", "GET", "https://app.test/detail", "text/html", ""),
	))
	if len(markers) != 6 {
		t.Fatalf("markers = %q, want three steps", markers)
	}
}

// A recorder's think time measured from a background poll does not decide a
// step boundary; the pause from the last request the step kept does.
func TestStepTransactionsIgnoreRecorderThinkTimeAfterAPoll(t *testing.T) {
	think := func(entry map[string]any, ms int) map[string]any {
		entry[harThinkTimeKey] = ms
		return entry
	}
	entries := []any{think(stepEntry("2026-01-01T00:00:00.000Z", "GET", "https://app.test/home", "text/html", ""), 0)}
	for second := 5; second <= 25; second += 5 {
		entries = append(entries, think(stepEntry(fmt.Sprintf("2026-01-01T00:00:%02d.000Z", second), "GET", "https://app.test/api/poll", "application/json", ""), 4900))
	}
	entries = append(entries, think(stepEntry("2026-01-01T00:00:25.300Z", "GET", "https://app.test/search", "text/html", ""), 200))
	markers, _ := stepMarkersAndTags(t, stepHAR(t, entries...))
	if len(markers) != 4 {
		t.Fatalf("markers = %q, want 01_home and 02_search", markers)
	}
}

// A forced boundary (the end of a sign-in) on a request the recorded shape
// folds into the background moves to the next request kept, so the step
// still ends there.
func TestStepTransactionWindowsCarryAForcedBoundaryPastAFoldedRequest(t *testing.T) {
	at := func(ms int) exchange {
		return exchange{URL: "https://app.test/x", Method: "GET", StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(ms) * time.Millisecond), HasStartedAt: true, DurationMs: 50}
	}
	exchanges := []exchange{at(0), at(100), at(150), at(200)}
	windows, member, _ := stepTransactionWindows(exchanges, 500*time.Millisecond, map[int]bool{1: true}, map[int]bool{1: true})
	if len(windows) != 2 || member[0] != 0 || member[1] != -1 || member[2] != 1 || member[3] != 1 {
		t.Fatalf("windows = %d, member = %v, want the boundary moved to the request after the folded one", len(windows), member)
	}
}

// A slow request (a 12 s report) still holds its step once it has finished:
// the request its response triggers 100 ms later stays in the same step.
func TestStepTransactionsKeepASlowRequestsFollowUp(t *testing.T) {
	report := stepEntry("2026-01-01T00:00:03.000Z", "POST", "https://app.test/api/report", "application/json", "q=1")
	report["time"] = 12000
	markers, tags := stepMarkersAndTags(t, stepHAR(t,
		stepEntry("2026-01-01T00:00:00.000Z", "GET", "https://app.test/home", "text/html", ""),
		report,
		stepEntry("2026-01-01T00:00:15.100Z", "GET", "https://app.test/api/report/1/render", "application/json", ""),
	))
	if len(markers) != 4 || len(tags) != 2 {
		t.Fatalf("markers = %q, tags = %v, want two steps with the render in the report's", markers, tags)
	}
}

// A request held across later clicks and listed last (recorders append in
// completion order) is sent last by the script, so it joins the last step
// without stretching that step back over the earlier ones: the steps stay
// disjoint.
func TestStepTransactionsStayDisjointWithALateListedLongRequest(t *testing.T) {
	long := stepEntry("2026-01-01T00:00:00.050Z", "GET", "https://app.test/api/export", "application/json", "")
	long["time"] = 30000
	on := true
	result, err := Convert(ImportRequest{Name: "Steps", Format: "har", Content: stepHAR(t,
		stepEntry("2026-01-01T00:00:00.000Z", "GET", "https://app.test/home", "text/html", ""),
		stepEntry("2026-01-01T00:00:10.000Z", "GET", "https://app.test/search", "text/html", ""),
		stepEntry("2026-01-01T00:00:20.000Z", "GET", "https://app.test/detail", "text/html", ""),
		long,
	), Options: ConvertOptions{StepTransactions: &on}})
	if err != nil {
		t.Fatal(err)
	}
	var shape []string
	for _, action := range result.Script.Actions {
		if action.Type == "source_transaction" {
			shape = append(shape, action.Name)
			continue
		}
		shape = append(shape, action.Target)
	}
	want := "Start 01_home transaction|/home|End 01_home transaction|Start 02_search transaction|/search|End 02_search transaction|Start 03_detail transaction|/detail|/api/export|End 03_detail transaction"
	if strings.Join(shape, "|") != want {
		t.Fatalf("shape = %q, want %q", shape, want)
	}
	for _, action := range result.Script.Actions {
		if action.Target == "/api/export" && action.Metadata[harSourceTransactionMeta] != "03_detail" {
			t.Fatalf("export tagged %v, want the step the script sends it in", action.Metadata[harSourceTransactionMeta])
		}
	}
}

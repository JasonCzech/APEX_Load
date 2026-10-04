package dynatrace

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// documentedResponse loads a response body shaped like the Dynatrace
// Environment API v2 reference examples. Hand-written fixtures previously
// mirrored the decoder instead of the API and hid entity-shape mismatches.
func documentedResponse(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestProblemsAndEventsDecodeDocumentedEntityStubs(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v2/events" {
			return response(200, documentedResponse(t, "events.json")), nil
		}
		return response(200, strings.Replace(documentedResponse(t, "problems.json"), `"nextPageKey": "page-two",`, "", 1)), nil
	})
	c.now = func() time.Time { return start.Add(time.Hour) }
	s := Selection{ProjectID: "p", Problems: true, Events: true}
	result, err := c.Collect(t.Context(), s, start, start.Add(10*time.Minute), "run")
	if err != nil || !result.Progress.Complete {
		t.Fatalf("collect %+v %v", result, err)
	}
	for _, key := range []string{"problems", "events"} {
		if result.Coverage[key].Status != "complete" {
			t.Fatalf("%s coverage %+v", key, result.Coverage[key])
		}
	}
	var problem, event *Diagnostic
	for i := range result.Evidence {
		switch result.Evidence[i].Kind {
		case "problem":
			problem = &result.Evidence[i]
		case "event":
			event = &result.Evidence[i]
		}
	}
	if problem == nil || problem.EntityIDs["entity_0"] != "SERVICE-0000000000000001" || problem.EntityIDs["entity_1"] != "HOST-0000000000000002" {
		t.Fatalf("problem entities %+v", problem)
	}
	if event == nil || event.EntityIDs["entity"] != "SERVICE-0000000000000001" || event.Properties["end_time"] == "" {
		t.Fatalf("event entity %+v", event)
	}
	if err := ValidateResult(s, c.cfg, start, start.Add(10*time.Minute), "run", result); err != nil {
		t.Fatal(err)
	}
}

func TestClassicTopologyRequestsAndDecodesDocumentedRelationships(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("fields") != "+fromRelationships,+toRelationships" {
			t.Fatalf("relationships must be requested as additional fields: %q", r.URL.Query().Get("fields"))
		}
		return response(200, documentedResponse(t, "entities.json")), nil
	})
	c.now = func() time.Time { return start.Add(time.Hour) }
	result, err := c.Collect(t.Context(), Selection{ProjectID: "p", TopologyMode: "classic"}, start, start.Add(10*time.Minute), "run")
	if err != nil || result.Coverage["topology"].Status != "complete" || len(result.Topology) != 1 {
		t.Fatalf("topology %+v %v", result, err)
	}
	ids := result.Topology[0].EntityIDs
	if ids["classic_id"] != "SERVICE-0000000000000001" || ids["from.calls.0"] != "SERVICE-0000000000000002" || ids["from.runsOnProcessGroupInstance.0"] != "PROCESS_GROUP_INSTANCE-0000000000000003" || ids["to.calls.0"] != "APPLICATION-0000000000000004" {
		t.Fatalf("relationships %+v", ids)
	}
	discovered, err := c.Discover(t.Context(), DiscoveryRequest{Kind: "entities"})
	if err != nil || len(discovered.Items) != 1 || discovered.Items[0].EntityIDs["to.calls.0"] != "APPLICATION-0000000000000004" {
		t.Fatalf("discovery %+v %v", discovered, err)
	}
}

// A reachable upstream returning an unexpected payload must not be reported as
// a connectivity failure; operators need to know the API contract changed.
func TestUndocumentedPayloadShapeIsReportedAsInvalidResponse(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		return response(200, `{"problems":[{"problemId":"P-1","startTime":1791028800000,"affectedEntities":[{"entityId":"SERVICE-0000000000000001"}]}]}`), nil
	})
	c.now = func() time.Time { return start.Add(time.Hour) }
	result, err := c.Collect(t.Context(), Selection{ProjectID: "p", Problems: true}, start, start.Add(time.Minute), "run")
	if err != nil || result.Coverage["problems"].Status != "invalid_response" {
		t.Fatalf("coverage %+v %v", result.Coverage, err)
	}
	if capabilityStatus(errors.Join(errors.New("decode"), ErrInvalidResponse)) != "invalid_response" {
		t.Fatal("invalid response status mapping")
	}
}

func TestMetricSelectorAllowsOnlyModelledTransformations(t *testing.T) {
	selection := func(selector string) Selection {
		return Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "m", MetricSelector: selector, Aggregation: "avg"}}}
	}
	for _, selector := range []string{
		"builtin:host.cpu.usage:avg:smooth(skipfirst)",
		"builtin:host.cpu.usage:avg:rollup(avg,1h)",
		"builtin:host.cpu.usage:avg:delta",
		"builtin:host.cpu.usage:delta:avg",
		"builtin:host.cpu.usage:fold(avg)",
		"builtin:host.cpu.usage:avg:timeshift(-1h)",
		"builtin:host.cpu.usage:avg:default(0)",
		"builtin:host.cpu.usage:last",
		"builtin:host.cpu.usage:avg:names",
		`"builtin:host.cpu.usage":avg:smooth(skipfirst)`,
	} {
		if selection(selector).Validate() == nil {
			t.Errorf("unmodelled transformation accepted: %s", selector)
		}
	}
	for _, selector := range []string{
		"builtin:host.cpu.usage",
		"builtin:host.cpu.usage:avg",
		`builtin:host.cpu.usage:filter(eq("dt.entity.host","HOST-0000000000000001")):splitBy("dt.entity.host"):avg:sort(value(avg,descending)):limit(10)`,
		`builtin:service.requestCount.total:merge("dt.entity.service"):avg:rate(1m)`,
		"builtin:host.mem.avail.bytes:avg:toUnit(Byte,MegaByte)",
		"builtin:host.cpu.usage:parents:avg:setUnit(Percent)",
		`"builtin:host.cpu.usage":avg`,
		"custom.metric.without.prefix:avg",
	} {
		if err := selection(selector).Validate(); err != nil {
			t.Errorf("supported selector rejected %s: %v", selector, err)
		}
	}
}

func TestRetryAfterIsCappedRatherThanIgnored(t *testing.T) {
	for header, want := range map[string]time.Duration{"120": 120 * time.Second, "600": 300 * time.Second, "99999999999999999999999": 300 * time.Second} {
		c := fixtureClient(t, func(*http.Request) (*http.Response, error) {
			resp := response(429, `{}`)
			resp.Header.Set("Retry-After", header)
			return resp, nil
		})
		_, err := c.request(t.Context(), false, http.MethodGet, "/api/v2/problems", nil, nil)
		var upstream *HTTPError
		if !errors.As(err, &upstream) || upstream.RetryAfter != want {
			t.Errorf("Retry-After %s: %v, want %v", header, err, want)
		}
	}
}

func TestGrailDurationsAcceptNumberAndDecimalStringEncodings(t *testing.T) {
	for _, raw := range []string{`60000000000`, `"60000000000"`, `"1m"`} {
		if seconds, err := grailInterval([]byte(raw)); err != nil || seconds != 60 {
			t.Errorf("interval %s: %d %v", raw, seconds, err)
		}
	}
	for _, raw := range []string{`"59999999999"`, `"-60000000000"`, `"sixty"`, `true`} {
		if _, err := grailInterval([]byte(raw)); err == nil {
			t.Errorf("invalid interval accepted: %s", raw)
		}
	}
	if nanos, ok := grailDurationNanos([]byte(`"2000000"`)); !ok || nanos != 2_000_000 {
		t.Fatal("decimal-string span duration rejected")
	}
}

// A query that finishes while a later one is still running must still report
// its own cost; the pass-level cost flag cannot gate the finished attempt.
func TestGrailAttemptsSettleIndependentlyAcrossPasses(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Grail: []GrailQuery{
		{Name: "a", Kind: "metric", Query: "fetch logs | summarize a = count()", ValueField: "a", Unit: "count", Statistic: "sum"},
		{Name: "b", Kind: "metric", Query: "fetch logs | summarize b = count()", ValueField: "b", Unit: "count", Statistic: "sum"},
	}}
	executes := 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "query:execute") {
			executes++
			if executes == 1 {
				return response(200, `{"state":"SUCCEEDED","result":{"records":[{"a":1}],"metadata":{"grail":{"scannedBytes":5000}}}}`), nil
			}
			return response(200, `{"state":"RUNNING","requestToken":"token-b","ttlSeconds":60}`), nil
		}
		return response(200, `{"state":"SUCCEEDED","result":{"records":[{"b":2}],"metadata":{"grail":{"scannedBytes":7000}}}}`), nil
	})
	reserved := []string{}
	c.SetScanHook(func(id string, _ int64) error { reserved = append(reserved, id); return nil })
	c.now = func() time.Time { return start.Add(time.Hour) }
	first, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "r")
	if err != nil || len(reserved) != 2 || first.Progress.QueryCostKnown {
		t.Fatalf("first %+v %v", first.Progress, err)
	}
	if len(first.Progress.ScanSettlements) != 1 || first.Progress.ScanSettlements[0] != (QueryScanSettlement{AttemptID: reserved[0], ScannedBytes: 5000}) || first.Progress.ScannedBytes != 5000 {
		t.Fatalf("finished attempt did not settle while another runs: %+v", first.Progress)
	}
	s.Cursor, s.QueryOffset = first.Progress.Cursor, first.Progress.NextQuery
	second, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "r")
	if err != nil || !second.Progress.Complete || len(reserved) != 2 {
		t.Fatalf("second %+v %v", second.Progress, err)
	}
	if len(second.Progress.ScanSettlements) != 1 || second.Progress.ScanSettlements[0] != (QueryScanSettlement{AttemptID: reserved[1], ScannedBytes: 7000}) || len(second.Progress.ScanReservationIDs) != 0 {
		t.Fatalf("continued attempt settlement %+v", second.Progress)
	}
	for _, result := range []Result{first, second} {
		if err := ValidateResult(s, c.cfg, start, start.Add(time.Minute), "r", result); err != nil {
			t.Fatal(err)
		}
	}
	forged := second.Progress
	forged.ScanSettlements = append(forged.ScanSettlements, forged.ScanSettlements[0])
	if ValidateScanSettlements(forged) == nil {
		t.Fatal("duplicate settlement accepted")
	}
	forged = second.Progress
	forged.ScannedBytes++
	if ValidateScanSettlements(forged) == nil {
		t.Fatal("pass total differing from settlements accepted")
	}
}

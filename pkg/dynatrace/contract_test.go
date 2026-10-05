package dynatrace

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUnknownGrailCostRetainsReservationAndExpiredCursorChargesNewAttempt(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	now := start.Add(time.Hour)
	s := Selection{ProjectID: "p", Grail: []GrailQuery{{Name: "n", Kind: "metric", Query: "fetch logs | summarize n=count()", ValueField: "n", Statistic: "sum", Unit: "Count"}}}
	executes, reservations := 0, []string{}
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "query:execute") {
			t.Fatal("expired cursor polled", r.URL)
		}
		executes++
		if executes == 1 {
			return response(200, `{"state":"RUNNING","requestToken":"old","ttlSeconds":1}`), nil
		}
		return response(200, `{"state":"SUCCEEDED","result":{"records":[{"n":0}]}}`), nil
	})
	c.now = func() time.Time { return now }
	c.SetScanHook(func(id string, _ int64) error { reservations = append(reservations, id); return nil })
	one, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || one.Progress.QueryCostKnown || one.Progress.Cursor == "" {
		t.Fatal(one, err)
	}
	now = now.Add(2 * time.Second)
	s.Cursor = one.Progress.Cursor
	s.QueryOffset = one.Progress.NextQuery
	two, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || !two.Progress.Complete || two.Progress.NextQuery != two.Progress.TotalQueries || two.Progress.QueryCostKnown || len(two.Points) != 1 || two.Points[0].Unit != "count" || two.Points[0].Provider.Backend != "grail" || len(reservations) != 2 || reservations[0] == reservations[1] {
		t.Fatal(two, err, reservations)
	}
}

func TestMetricAggregationIdentityAndCanonicalDeclaredUnit(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	selection := MetricSelection{Name: "calls", MetricSelector: "custom.calls", Aggregation: "value", Unit: "Count"}
	s := Selection{ProjectID: "p", Metrics: []MetricSelection{selection}}
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v2/metrics/query" {
			return response(200, `{"metricId":"custom.calls","unit":"Count"}`), nil
		}
		raw, _ := json.Marshal(map[string]any{"resolution": "1m", "result": []any{map[string]any{"metricId": "custom.calls:value", "data": []any{map[string]any{"timestamps": []int64{start.Add(time.Minute).UnixMilli()}, "values": []int{0}}}}}})
		return response(200, string(raw)), nil
	})
	result, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || len(result.Points) != 1 || result.Points[0].Unit != "count" || result.Points[0].Provider.Backend != "classic" {
		t.Fatal(result, err)
	}
	if err = ValidateResult(s, c.cfg, start, start.Add(time.Minute), "run", result); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"custom.calls:avg", "custom.calls:value:sum"} {
		changed := s
		changed.Metrics = []MetricSelection{selection}
		changed.Metrics[0].MetricSelector = selector
		if changed.Validate() == nil {
			t.Fatal("conflicting selector aggregation accepted", selector)
		}
	}
}

func TestMetricRateAndUnitTransformsPreserveProviderSemantics(t *testing.T) {
	for _, tc := range []struct{ selector, descriptor, unit, query string }{
		{"custom.calls:rate()", "Count", "count/min", "custom.calls:sum:rate()"},
		{"custom.calls:rate", "Count", "count/min", "custom.calls:sum:rate"},
		{"custom.calls:rate(5m)", "Count", "count/5min", "custom.calls:sum:rate(5m)"},
		{"custom.calls:rate(1s)", "Count", "count/s", "custom.calls:sum:rate(1s)"},
		{"custom.calls:sum:rate(1h)", "Count", "count/h", "custom.calls:sum:rate(1h)"},
		{"custom.calls:toUnit(MicroSecond,MilliSecond):setUnit(Percent)", "MicroSecond", "%", "custom.calls:sum:toUnit(MicroSecond,MilliSecond):setUnit(Percent)"},
		{"custom.calls:rate(1s):setUnit(Count)", "Count", "count", "custom.calls:sum:rate(1s):setUnit(Count)"},
		{`"custom:rate":filter(eq("label",":rate(1s)"))`, "Count", "count", `"custom:rate":filter(eq("label",":rate(1s)")):sum`},
	} {
		t.Run(tc.selector, func(t *testing.T) {
			m := MetricSelection{Name: "calls", MetricSelector: tc.selector, Aggregation: "sum", Unit: tc.unit}
			if err := validateSelectorAggregation(m); err != nil {
				t.Fatal(err)
			}
			unit, err := transformedUnit(tc.selector, tc.descriptor)
			if err != nil || unit != tc.unit || querySelector(m) != tc.query {
				t.Fatal(unit, err, querySelector(m))
			}
		})
	}
	for _, selector := range []string{"custom.calls:rate(0m)", "custom.calls:rate(1000001m)", "custom.calls:rate(1s):rate(1m)", "custom.calls:rate(1s):sum", "custom.calls:setUnit(Count):sum", "custom.calls:rate(1.5m)", "custom.calls:toUnit(Count)"} {
		if validateSelectorAggregation(MetricSelection{MetricSelector: selector, Aggregation: "sum"}) == nil {
			t.Fatal("invalid rate/unit selector accepted", selector)
		}
	}
	if _, err := transformedUnit("custom.calls:toUnit(MilliSecond,Second)", "Count"); err == nil {
		t.Fatal("unit conversion falsely changed a mismatched source unit")
	}
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "calls", MetricSelector: "custom.calls:rate(5m)", Aggregation: "sum", Unit: "count/5min"}}}
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v2/metrics/query" {
			return response(200, `{"metricId":"custom.calls","unit":"Count"}`), nil
		}
		if r.URL.Query().Get("metricSelector") != "custom.calls:sum:rate(5m)" {
			t.Fatal("aggregation was not placed before rate", r.URL)
		}
		raw, _ := json.Marshal(map[string]any{"resolution": "1m", "result": []any{map[string]any{"metricId": "custom.calls:sum:rate(5m)", "data": []any{map[string]any{"timestamps": []int64{start.Add(time.Minute).UnixMilli()}, "values": []int{7}}}}}})
		return response(200, string(raw)), nil
	})
	result, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || len(result.Points) != 1 || result.Points[0].Unit != "count/5min" || *result.Points[0].Value != 7 || result.Points[0].Provider.Statistic != "rate" {
		t.Fatal(result, err)
	}
	if err := ValidateResult(s, c.cfg, start, start.Add(time.Minute), "run", result); err != nil {
		t.Fatal(err)
	}
}

func TestRateCannotPromoteBucketPercentilesOrUnweightedAverages(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, aggregation := range []string{"p95", "p99", "avg"} {
		t.Run(aggregation, func(t *testing.T) {
			c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v2/metrics/query" {
					return response(200, `{"metricId":"custom.calls","unit":"Count"}`), nil
				}
				raw, _ := json.Marshal(map[string]any{"resolution": "1m", "result": []any{map[string]any{"metricId": "custom.calls", "data": []any{map[string]any{"timestamps": []int64{start.Add(time.Minute).UnixMilli()}, "values": []int{7}}}}}})
				return response(200, string(raw)), nil
			})
			s := Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "calls", MetricSelector: "custom.calls:rate(1s)", Aggregation: aggregation, Unit: "count/s"}}}
			result, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
			if err != nil || len(result.Points) != 1 || result.Points[0].Provider.AggregationEligible {
				t.Fatal(result, err)
			}
			if err := ValidateResult(s, c.cfg, start, start.Add(time.Minute), "run", result); err != nil {
				t.Fatal(err)
			}
			result.Points[0].Provider.AggregationEligible = true
			if ValidateResult(s, c.cfg, start, start.Add(time.Minute), "run", result) == nil {
				t.Fatal("forged transformed statistic eligibility accepted")
			}
		})
	}
}

func TestSmartscapeUsesExplicitOpaqueScopeAndRetainsClassicIdentity(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", TopologyMode: "smartscape", SmartscapeNodeType: "dt.entity.service", SmartscapeEntityIDs: []string{"service:opaque-1"}}
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		query, _ := body["query"].(string)
		if !strings.Contains(query, `smartscapeNodes "dt.entity.service"`) || !strings.Contains(query, `filter in(toString(id), {"service:opaque-1"})`) {
			t.Fatal("scope dropped", query)
		}
		return response(200, `{"state":"SUCCEEDED","result":{"records":[{"id":"service:opaque-1","id_classic":"SERVICE-0000000000000001","name":"service","type":"service"},{"id":"service:unapproved","name":"other"}],"metadata":{"grail":{"scannedBytes":12}}}}`), nil
	})
	result, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || len(result.Topology) != 1 || result.Topology[0].ID == result.Topology[0].EntityIDs["classic_id"] {
		t.Fatal(result, err)
	}
	if err = ValidateResult(s, c.cfg, start, start.Add(time.Minute), "run", result); err != nil {
		t.Fatal(err)
	}
	s.EntitySelector = `type("SERVICE")`
	if s.Validate() == nil {
		t.Fatal("classic scope silently ignored by Smartscape")
	}
}

func TestGrailEventAndSampledTraceSummaries(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	c := fixtureClient(t, func(*http.Request) (*http.Response, error) { return nil, nil })
	q := GrailQuery{Name: "traces", Kind: "trace", Query: "fetch spans", EntityIDField: "dt.entity.service", EntityIDs: []string{"SERVICE-0000000000000001"}, Fields: []string{"span.name"}}
	query, err := BuildGrailQuery(q, start, start.Add(time.Minute), "run")
	if err != nil || !strings.Contains(query, "trace.id") || !strings.Contains(query, "timestamp") || !strings.Contains(query, "dt.entity.service") {
		t.Fatal(query, err)
	}
	var data grailResponse
	json.Unmarshal([]byte(`{"result":{"records":[{"timestamp":"2026-10-03T12:00:10Z","trace.id":"00000000000000000000000000000001","span.id":"0000000000000001","span.parent_id":"0000000000000002","span.name":"call","duration":2000000,"dt.entity.service":"SERVICE-0000000000000001"},{"timestamp":"2026-10-03T12:00:20Z","trace.id":"00000000000000000000000000000001","span.name":"another","dt.entity.service":"SERVICE-0000000000000001"}]}}`), &data)
	_, evidence, _, err := c.normalizeGrail(q, data, "p", start, start.Add(time.Minute), "run", 120)
	if err != nil || len(evidence) != 1 || evidence[0].Kind != "trace" || evidence[0].Backend != "grail" || evidence[0].Properties["evidence_scope"] != "sampled_span" || evidence[0].Properties["duration_ms"] != "2" || evidence[0].Properties["parent_span_id"] == "" {
		t.Fatal(evidence, err)
	}
	data.Result.Records[0]["dt.entity.service"] = json.RawMessage(`"SERVICE-0000000000000002"`)
	if _, _, _, err = c.normalizeGrail(q, data, "p", start, start.Add(time.Minute), "run", 120); err == nil {
		t.Fatal("provider returned unapproved evidence entity")
	}
	q = GrailQuery{Name: "events", Kind: "event", Query: "fetch events"}
	if q.Validate() != nil {
		t.Fatal("Grail events unsupported")
	}
	json.Unmarshal([]byte(`{"result":{"records":[{"timestamp":"2026-10-03T12:00:10Z","event.id":"event1","event.name":"warning","event.type":"CUSTOM_ALERT","event.severity":"WARN","event.status":"OPEN"}]}}`), &data)
	_, evidence, _, err = c.normalizeGrail(q, data, "p", start, start.Add(time.Minute), "run", 120)
	if err != nil || len(evidence) != 1 || evidence[0].Kind != "event" || evidence[0].Severity != "WARN" || evidence[0].Properties["event_type"] != "CUSTOM_ALERT" {
		t.Fatal(evidence, err)
	}
}

func TestSmartscapeRelationshipsContinueWithinOneTopologyOperation(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", TopologyMode: "smartscape", SmartscapeNodeType: "SERVICE", SmartscapeEntityIDs: []string{"SERVICE-0000000000000001"}}
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		query, _ := body["query"].(string)
		if strings.HasPrefix(query, "smartscapeNodes") {
			return response(200, `{"state":"SUCCEEDED","result":{"records":[{"id":"SERVICE-0000000000000001","name":"one","type":"SERVICE"}],"metadata":{"grail":{"scannedBytes":10}}}}`), nil
		}
		if !strings.HasPrefix(query, "smartscapeEdges") || !strings.Contains(query, "source_id") || !strings.Contains(query, "target_id") {
			t.Fatal("relationship scope/projection", query)
		}
		return response(200, `{"state":"SUCCEEDED","result":{"records":[{"source_id":"SERVICE-0000000000000001","source_type":"SERVICE","type":"calls","target_id":"SERVICE-0000000000000002","target_type":"SERVICE"}],"metadata":{"grail":{"scannedBytes":20}}}}`), nil
	})
	one, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || one.Progress.Complete || one.Progress.TotalQueries != 1 || one.Progress.Cursor == "" || len(one.Topology) != 1 {
		t.Fatal(one, err)
	}
	s.Cursor, s.QueryOffset = one.Progress.Cursor, one.Progress.NextQuery
	two, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || !two.Progress.Complete || two.Progress.TotalQueries != 1 || len(two.Topology) != 1 || two.Topology[0].Type != "relationship" || two.Topology[0].EntityIDs["target_id"] != "SERVICE-0000000000000002" {
		t.Fatal(two, err)
	}
	if err = ValidateResult(s, c.cfg, start, start.Add(time.Minute), "run", two); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionVerificationKeepsClassicAndSmartscapeSeparateAndBindsIdentity(t *testing.T) {
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "query:execute") {
			return response(200, `{"state":"SUCCEEDED","result":{"records":[],"metadata":{"grail":{"scannedBytes":0}}}}`), nil
		}
		if r.URL.Path == "/api/v2/metrics/builtin:host.cpu.usage" {
			return response(200, `{"metricId":"builtin:host.cpu.usage","unit":"Percent"}`), nil
		}
		if r.URL.Path == "/api/v2/metrics/query" {
			return response(200, `{"resolution":"1m","result":[]}`), nil
		}
		return response(200, `{"problems":[],"events":[],"entities":[]}`), nil
	})
	caps, err := c.Test(t.Context())
	if err != nil || !caps.ClassicTopology || !caps.Smartscape || caps.EnvironmentID != "fixture" || caps.PlatformEnvironmentID != "fixture" || caps.TenantID != "fixture" || len(caps.VerifiedOrigins) != 2 {
		t.Fatal(caps, err)
	}
	cfg := c.cfg
	cfg.PlatformURL = "https://different.apps.dynatrace.com"
	if cfg.Validate() == nil {
		t.Fatal("different SaaS environments combined silently")
	}
}

func TestDiagnosticFiltersAreEncodedAndBounded(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Problems: true, Events: true, ProblemSelector: `severityLevel("ERROR"),text("x&y")`, EventSelector: `eventType("CUSTOM_ALERT")`}
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "problems") && r.URL.Query().Get("problemSelector") != s.ProblemSelector {
			t.Fatal(r.URL)
		}
		if strings.HasSuffix(r.URL.Path, "events") && r.URL.Query().Get("eventSelector") != s.EventSelector {
			t.Fatal(r.URL)
		}
		return response(200, `{"problems":[],"events":[]}`), nil
	})
	if _, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run"); err != nil {
		t.Fatal(err)
	}
	s.ProblemSelector = "bad\rselector"
	if s.Validate() == nil {
		t.Fatal("control characters accepted")
	}
}

func TestMetricDataPointBudgetSplitsWindowsBeforeAdvancingProgress(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "calls", MetricSelector: "custom.calls", Aggregation: "sum", Unit: "count"}}}
	large, small := 0, 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v2/metrics/query" {
			return response(200, `{"metricId":"custom.calls","unit":"Count"}`), nil
		}
		from, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("from"))
		to, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("to"))
		if to.Sub(from) > time.Minute {
			large++
			return response(400, `{"message":"data point budget exceeded"}`), nil
		}
		small++
		raw, _ := json.Marshal(map[string]any{"resolution": "1m", "result": []any{map[string]any{"metricId": "custom.calls:sum", "data": []any{map[string]any{"timestamps": []int64{to.UnixMilli()}, "values": []int{1}}}}}})
		return response(200, string(raw)), nil
	})
	points, complete := 0, false
	for pass := 0; pass < 12; pass++ {
		result, err := c.Collect(t.Context(), s, start, start.Add(4*time.Minute), "run")
		if err != nil {
			t.Fatal(err)
		}
		if err = ValidateResult(s, c.cfg, start, start.Add(4*time.Minute), "run", result); err != nil {
			t.Fatal(err)
		}
		points += len(result.Points)
		if result.Progress.Complete {
			complete = true
			break
		}
		if result.Progress.Cursor == "" || result.Progress.NextQuery != 0 {
			t.Fatal("query window advanced before smaller chunks committed", result)
		}
		s.Cursor, s.QueryOffset = result.Progress.Cursor, result.Progress.NextQuery
	}
	if !complete || points != 4 || small != 4 || large != 3 {
		t.Fatal("adaptive windows did not cover the fixed query", complete, points, large, small)
	}
}

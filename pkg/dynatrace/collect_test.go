package dynatrace

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestMetricBucketsPreserveEndTimestampNullZeroDimensionsAndUnits(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 30, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	s := Selection{ProjectID: "project", Metrics: []MetricSelection{{Name: "cpu", MetricSelector: "builtin:host.cpu.usage", Aggregation: "avg", Semantics: "gauge", Unit: "%"}}}
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v2/metrics/query" {
			return response(200, `{"metricId":"builtin:host.cpu.usage","unit":"Percent","aggregationTypes":["avg"],"metricValueType":{"type":"gauge"}}`), nil
		}
		if r.URL.Query().Get("resolution") != "1m" || r.Header.Get("Accept") != "application/json" {
			t.Fatal(r.URL)
		}
		raw, _ := json.Marshal(map[string]any{"resolution": "1m", "result": []any{map[string]any{"metricId": "builtin:host.cpu.usage:avg", "data": []any{map[string]any{"dimensionMap": map[string]string{"dt.entity.host": "HOST-0000000000000001"}, "timestamps": []int64{start.Truncate(time.Minute).Add(time.Minute).UnixMilli(), start.Truncate(time.Minute).Add(2 * time.Minute).UnixMilli(), start.Truncate(time.Minute).Add(3 * time.Minute).UnixMilli()}, "values": []any{0, nil, 2}}}}}})
		return response(200, string(raw)), nil
	})
	c.now = func() time.Time { return end.Add(5 * time.Minute) }
	result, err := c.Collect(t.Context(), s, start, end, "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) != 3 {
		t.Fatalf("points %+v", result)
	}
	first := result.Points[0]
	if first.Value == nil || *first.Value != 0 || first.Unit != "%" || !first.TimestampUTC.Equal(start.Truncate(time.Minute)) || first.Provider.Statistic != "time_average" || !first.Provider.AggregationEligible || !first.Provider.Settled {
		t.Fatalf("point %+v", first)
	}
	if result.Points[1].Value != nil || result.Points[1].Quality != analysis.QualityMissing {
		t.Fatal("null turned into zero")
	}
	if !result.Points[2].Provider.BucketEnd.After(end) {
		t.Fatal("partial boundary bucket discarded")
	}
	if err := ValidateResult(s, c.cfg, start, end, "run", result); err != nil {
		t.Fatal(err)
	}
}
func TestMetricInvalidResolutionDescriptorAndStatisticAreRejected(t *testing.T) {
	for _, tc := range []struct {
		resolution, unit string
		gauge            bool
	}{{"Inf", "Percent", true}, {"1m", "Byte", true}, {"1m", "Percent", false}} {
		t.Run(tc.resolution+tc.unit, func(t *testing.T) {
			s := Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "m", MetricSelector: "builtin:host.cpu.usage", Aggregation: "avg", Unit: "%", Semantics: "gauge"}}}
			c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v2/metrics/query" {
					kind := "counter"
					if tc.gauge {
						kind = "gauge"
					}
					return response(200, `{"metricId":"builtin:host.cpu.usage","unit":"`+tc.unit+`","metricValueType":{"type":"`+kind+`"}}`), nil
				}
				return response(200, `{"resolution":"`+tc.resolution+`","result":[]}`), nil
			})
			from := time.Now().Add(-time.Hour).UTC()
			result, err := c.Collect(t.Context(), s, from, from.Add(time.Minute), "r")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Points) > 0 || result.Coverage["metrics:m"].Status == "complete" {
				t.Fatal("invalid provider result accepted")
			}
		})
	}
}
func TestDiagnosticContinuationResolutionIDsAndTruncation(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Problems: true}
	requests := 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return response(200, documentedResponse(t, "problems.json")), nil
		}
		if len(r.URL.Query()) != 1 || r.URL.Query().Get("nextPageKey") != "page-two" {
			t.Fatal(r.URL)
		}
		return response(200, `{"problems":[{"problemId":"06F288EE2A930951-1791028800000","status":"CLOSED","startTime":1791028800000,"endTime":1791028860000,"affectedEntities":[]}]}`), nil
	})
	one, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "r")
	if err != nil || one.Progress.Complete || one.Progress.Cursor == "" || len(one.Evidence) != 1 || len(one.Evidence[0].EntityIDs) != 2 || strings.Contains(one.Evidence[0].Summary, "secret") {
		t.Fatalf("first %+v %v", one, err)
	}
	s.Cursor = one.Progress.Cursor
	s.QueryOffset = one.Progress.NextQuery
	two, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "r")
	if err != nil || !two.Progress.Complete || two.Evidence[0].Status != "CLOSED" {
		t.Fatalf("second %+v %v", two, err)
	}
	s.EntitySelector = `type("HOST")`
	if _, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "r"); err == nil {
		t.Fatal("cursor escaped frozen selection")
	}
}
func TestCapabilityRequiresEverySelectedMetric(t *testing.T) {
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "denied") {
			return response(403, ""), nil
		}
		if r.URL.Path != "/api/v2/metrics/query" {
			return response(200, `{"metricId":"good","unit":"Percent"}`), nil
		}
		return response(200, `{"resolution":"1m","result":[]}`), nil
	})
	caps, err := c.TestSelection(t.Context(), Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "good", MetricSelector: "good", Aggregation: "avg"}, {Name: "denied", MetricSelector: "denied", Aggregation: "avg"}}})
	if err != nil {
		t.Fatal(err)
	}
	if caps.Metrics || caps.Status["metrics:denied"] != "permission_denied" {
		t.Fatalf("caps %+v", caps)
	}
}

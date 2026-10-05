package dynatrace

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGovernedDQLRejectsEscapeRoutesAndProjectsSafeEvidence(t *testing.T) {
	for _, query := range []string{"fetch logs | append [fetch spans]", "fetch logs; fetch spans", "fetch logs, from:now-1d", "fetch logs | lookup [fetch events]", "fetch logs /* escape */", "fetch logs | filter x in [1,2]", "timeseries x=avg(m), filter:{fetch logs}", "fetch logs | fetch spans", "fetch logs | filter x==1 | join fetch logs"} {
		q := GrailQuery{Name: "q", Kind: "log", Query: query}
		if q.Validate() == nil {
			t.Errorf("unsafe query accepted %s", query)
		}
	}
	q := GrailQuery{Name: "logs", Kind: "log", Query: `fetch logs | filter loglevel == "ERROR"`, RunIDField: "apex.test_id"}
	query, err := BuildGrailQuery(q, time.Now().UTC(), time.Now().UTC(), "run\"| fetch spans")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, `filter apex.test_id == "run\"| fetch spans" | filter`) || !strings.Contains(query, "fields timestamp") || !strings.Contains(query, "apex.test_id | limit 101") {
		t.Fatal(query)
	}
	q.Fields = []string{"http.request.body"}
	if q.Validate() == nil {
		t.Fatal("excluded body allowed")
	}
}
func TestAsyncGrailResumesTokenChargesOnceAndPreservesMetadata(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Grail: []GrailQuery{{Name: "errors", Kind: "metric", Query: "fetch logs | summarize errors = count()", ValueField: "errors", Unit: "count", Statistic: "sum"}}}
	executes, polls, reserves := 0, 0, 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "query:execute") {
			executes++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["defaultScanLimitGbytes"] != float64(1) || body["defaultTimeframeStart"] != start.Format(time.RFC3339Nano) {
				t.Fatalf("body %+v", body)
			}
			return response(200, `{"state":"RUNNING","requestToken":"opaque-token","ttlSeconds":60}`), nil
		}
		polls++
		if r.URL.Query().Get("request-token") != "opaque-token" {
			t.Fatal(r.URL)
		}
		return response(200, `{"state":"SUCCEEDED","result":{"records":[{"errors":0}],"metadata":{"grail":{"scannedBytes":1234}}}}`), nil
	})
	c.SetScanHook(func(id string, bytes int64) error {
		reserves++
		if id == "" || bytes != 1_000_000_000 {
			t.Fatal("reservation")
		}
		return nil
	})
	c.now = func() time.Time { return start.Add(time.Hour) }
	first, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "r")
	if err != nil || first.Progress.Complete || first.Progress.QueryCostKnown || first.Progress.ScanReservationBytes != 1_000_000_000 {
		t.Fatalf("first %+v %v", first, err)
	}
	s.Cursor = first.Progress.Cursor
	s.QueryOffset = first.Progress.NextQuery
	second, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "r")
	if err != nil || !second.Progress.Complete || !second.Progress.QueryCostKnown || second.Progress.ScannedBytes != 1234 || len(second.Points) != 1 || *second.Points[0].Value != 0 || !second.Points[0].Provider.QueryWindow || reserves != 1 || executes != 1 || polls != 1 {
		t.Fatalf("second %+v %v", second, err)
	}
}
func TestGrailTimeseriesTypedBucketsAndSampleProjection(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	q := GrailQuery{Name: "cpu", Kind: "metric", Query: "timeseries value=avg(m)", ValueField: "value", Unit: "%", Statistic: "time_average", BucketSeconds: 60}
	c := fixtureClient(t, func(*http.Request) (*http.Response, error) { return nil, nil })
	c.now = func() time.Time { return start.Add(time.Hour) }
	var response grailResponse
	json.Unmarshal([]byte(`{"result":{"records":[{"timeframe":{"start":"2026-10-03T12:00:00Z","end":"2026-10-03T12:02:00Z"},"interval":60000000000,"value":[0,null]}],"metadata":{"grail":{"scannedBytes":0}}}}`), &response)
	points, _, _, err := c.normalizeGrail(q, response, "p", start, start.Add(2*time.Minute), "r", 120)
	if err != nil || len(points) != 2 || points[1].Value != nil || points[0].Provider.QueryWindow || !points[0].Provider.AggregationEligible {
		t.Fatalf("points %+v %v", points, err)
	}
	json.Unmarshal([]byte(`{"result":{"records":[{"timestamp":"2026-10-03T12:00:10Z","span.name":"password=secret","trace.id":"00000000000000000000000000000001","span.id":"0000000000000001","duration":123,"http.request.body":"excluded","apex.test_id":"r"}]}}`), &response)
	_, evidence, _, err := c.normalizeGrail(GrailQuery{Name: "spans", Kind: "span", RunIDField: "apex.test_id"}, response, "p", start, start.Add(time.Minute), "r", 120)
	if err != nil || len(evidence) != 1 || evidence[0].Properties["correlation"] != "run_header" || strings.Contains(evidence[0].Summary, "secret") || evidence[0].Properties["http.request.body"] != "" {
		t.Fatalf("evidence %+v %v", evidence, err)
	}
}

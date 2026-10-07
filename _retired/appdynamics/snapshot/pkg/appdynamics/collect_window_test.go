package appdynamics

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAppDynamicsCollectMultiDayReconciliation(t *testing.T) {
	from := time.Now().UTC().Add(-27 * time.Hour).Truncate(time.Second)
	to := from.Add(26 * time.Hour)
	controllerRequests, analyticsRequests := 0, 0
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/events/query") {
			analyticsRequests++
			if r.URL.Query().Get("start") != jsonNumber(from.UnixMilli()) || r.URL.Query().Get("end") != jsonNumber(to.UnixMilli()-1) {
				t.Fatal("Analytics changed the full reconciliation window", r.URL)
			}
			return jsonResponse(200, `[{"fields":[{"label":"value"}],"results":[[5]]}]`), nil
		}
		controllerRequests++
		if r.URL.Query().Get("start-time") != jsonNumber(from.UnixMilli()) || r.URL.Query().Get("end-time") != jsonNumber(to.UnixMilli()) || r.URL.Query().Get("rollup") != "false" {
			t.Fatal("Controller changed the full reconciliation window", r.URL)
		}
		return jsonResponse(200, `[{"metricId":7,"metricPath":"Overall Application Performance|Calls per Minute","frequency":"ONE_HOUR","metricValues":[{"startTimeInMillis":`+jsonNumber(from.UnixMilli())+`,"value":0}]}]`), nil
	})
	selection := Selection{ProjectID: "p", Applications: []string{"1"}, Metrics: []MetricSelection{{ApplicationID: "1", Name: "calls", Path: "Overall Application Performance|Calls per Minute", Unit: "count"}}, Analytics: []AnalyticsQuery{{Name: "transactions", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "metric", Query: "SELECT count(*) AS value FROM transactions"}}}
	result, err := c.Collect(context.Background(), selection, from, to, "run")
	if err != nil || len(result.Points) != 2 || !result.Progress.Complete || controllerRequests != 1 || analyticsRequests != 1 {
		t.Fatal(result, err, controllerRequests, analyticsRequests)
	}
	metric, aggregate := result.Points[0], result.Points[1]
	if metric.Value == nil || *metric.Value != 0 || metric.Provider.QueryWindow || metric.Provider.ResolutionSeconds != 3600 {
		t.Fatal("native source bucket resolution or explicit zero changed", metric)
	}
	if !aggregate.Provider.QueryWindow || aggregate.Provider.ResolutionSeconds != 26*3600 || !aggregate.Provider.BucketStart.Equal(from) || !aggregate.Provider.BucketEnd.Equal(to) || !aggregate.Provider.Settled {
		t.Fatal("whole-query aggregate lost its multi-day interval", aggregate)
	}
	for _, key := range []string{"metrics:0", "analytics:0"} {
		coverage := result.Coverage[key]
		if coverage.Status != "complete" || !coverage.From.Equal(from) || !coverage.To.Equal(to) {
			t.Fatal("multi-day coverage was narrowed", key, coverage)
		}
	}
}

func TestAppDynamicsMultiDayWindowStillAppliesPointBudget(t *testing.T) {
	from := time.Now().UTC().Add(-27 * time.Hour).Truncate(time.Minute)
	c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `[{"metricId":7,"metricPath":"calls","frequency":"ONE_MIN","metricValues":[{"startTimeInMillis":`+jsonNumber(from.UnixMilli())+`,"value":0},{"startTimeInMillis":`+jsonNumber(from.Add(time.Minute).UnixMilli())+`,"value":1}]}]`), nil
	})
	c.cfg.MaxPoints = 1
	selection := Selection{ProjectID: "p", Applications: []string{"1"}, Metrics: []MetricSelection{{ApplicationID: "1", Name: "calls", Path: "calls"}}}
	result, err := c.Collect(context.Background(), selection, from, from.Add(26*time.Hour), "run")
	coverage := result.Coverage["metrics:0"]
	if err != nil || len(result.Points) != 1 || coverage.Status != "partial" || !coverage.Truncated {
		t.Fatal("long query bypassed retention budget or hid truncation", result, err)
	}
}

func TestAppDynamicsCollectionRejectsInvalidOrUnrepresentableWindows(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, tc := range []struct {
		name     string
		from, to time.Time
	}{
		{"zero start", time.Time{}, now},
		{"inverted", now, now.Add(-time.Second)},
		{"future", now.Add(-time.Hour), now.Add(2 * time.Minute)},
		{"collapsed milliseconds", now, now.Add(time.Nanosecond)},
		{"duration overflow", time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC), now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid window produced upstream traffic")
				return nil, nil
			})
			selection := Selection{ProjectID: "p", Applications: []string{"1"}, Metrics: []MetricSelection{{ApplicationID: "1", Name: "calls", Path: "calls"}}}
			if _, err := c.Collect(context.Background(), selection, tc.from, tc.to, "run"); err == nil {
				t.Fatal("invalid collection window was accepted")
			}
		})
	}
}

func TestAppDynamicsAnalyticsRejectsDurationSaturationBeforeQuery(t *testing.T) {
	c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		t.Fatal("overflowing query produced upstream traffic")
		return nil, nil
	})
	q := AnalyticsQuery{Name: "transactions", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "metric", Query: "SELECT count(*) AS value FROM transactions"}
	from := time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, _, _, err := c.analytics(context.Background(), q, "p", "run", from, from.AddDate(400, 0, 0), time.Now().UTC(), 120, false); err == nil {
		t.Fatal("duration saturation was accepted")
	}
}

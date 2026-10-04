package appdynamics

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestAppDynamicsAnalyticsQueryValidation(t *testing.T) {
	for _, tc := range []struct {
		query string
		valid bool
	}{
		{"SELECT * FROM transactions", false}, {"SELECT eventTimestamp, * FROM transactions", false}, {"SELECT transactions.* FROM transactions", false},
		{"SELECT COUNT(*) AS value FROM transactions", true}, {"SELECT sum(responseTime) / count(*) AS value FROM transactions", true},
		{"SELECT eventTimestamp FROM transactions; DELETE", false}, {"SELECT eventTimestamp /* comment */ FROM transactions", false},
	} {
		s := Selection{ProjectID: "project", Applications: []string{"1"}, Analytics: []AnalyticsQuery{{Name: "transactions", ApplicationID: "1", ApplicationIDField: "applicationId", Query: tc.query}}}
		if got := s.Validate() == nil; got != tc.valid {
			t.Errorf("query=%q valid=%v", tc.query, got)
		}
	}
	for _, q := range []AnalyticsQuery{
		{Name: "q", ApplicationID: "2", Query: "SELECT eventTimestamp FROM transactions"},
		{Name: "q", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "analytics", Query: "SELECT eventTimestamp FROM transactions"},
		{Name: "q", ApplicationID: "1", ApplicationIDField: "applicationId", Statistic: "p99", Query: "SELECT eventTimestamp FROM transactions"},
		{Name: "q", ApplicationID: "1", ApplicationIDField: "applicationId", BucketSeconds: 86401, Query: "SELECT eventTimestamp FROM transactions"},
	} {
		if (Selection{ProjectID: "p", Applications: []string{"1"}, Analytics: []AnalyticsQuery{q}}).Validate() == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
}

func TestAppDynamicsAnalyticsNumericProvenance(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	to := from.Add(60 * time.Second)
	for _, tc := range []struct {
		name, stat, body string
		bucket           int
		quality          string
		eligible         bool
		wantErr          bool
	}{
		{"whole sum zero", "sum", `[{"fields":[{"label":"value"}],"results":[[0]]}]`, 0, analysis.QualityValid, true, false},
		{"weighted average", "average", `[{"fields":[{"label":"value"},{"label":"count"},{"label":"sum"}],"results":[[5,2,10]]}]`, 0, analysis.QualityValid, true, false},
		{"empty average", "average", `[{"fields":[{"label":"value"},{"label":"count"},{"label":"sum"}],"results":[[0,0,0]]}]`, 0, analysis.QualityMissing, false, false},
		{"average no count", "average", `[{"fields":[{"label":"value"}],"results":[[5]]}]`, 0, analysis.QualityValid, false, false},
		{"timestamp no resolution", "value", `[{"fields":[{"label":"bucket"},{"label":"value"}],"results":[[` + jsonNumber(from.UnixMilli()) + `,5]]}]`, 0, "", false, true},
		{"timestamp resolution", "value", `[{"fields":[{"label":"bucket"},{"label":"value"}],"results":[[` + jsonNumber(from.UnixMilli()) + `,5]]}]`, 30, analysis.QualityValid, true, false},
		{"partial boundary", "value", `[{"fields":[{"label":"bucket"},{"label":"value"}],"results":[[` + jsonNumber(from.Add(50*time.Second).UnixMilli()) + `,5]]}]`, 30, analysis.QualityValid, false, false},
		{"missing value alias", "value", `[{"fields":[{"label":"count"}],"results":[[5]]}]`, 0, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) { return jsonResponse(200, tc.body), nil })
			query := "SELECT count(*) AS value FROM transactions"
			if tc.stat == "average" {
				query = "SELECT avg(responseTime) AS value, count(*) AS count, sum(responseTime) AS sum FROM transactions"
			}
			if strings.Contains(tc.body, `"label":"bucket"`) {
				query = "SELECT eventTimestamp AS bucket, " + strings.TrimPrefix(query, "SELECT ")
			}
			_, points, _, err := c.analytics(context.Background(), AnalyticsQuery{Name: "q", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "metric", Statistic: tc.stat, BucketSeconds: tc.bucket, Query: query}, "p", "r", from, to, time.Now().UTC(), 120, false)
			if tc.wantErr {
				if err == nil {
					t.Fatal("missing error")
				}
				return
			}
			if err != nil || len(points) != 1 {
				t.Fatalf("points=%+v err=%v", points, err)
			}
			p := points[0]
			if p.Unit != "count" || p.Provider.EntityIDs["application"] != "1" || p.Provider.Statistic != tc.stat || p.Quality != tc.quality || p.Provider.AggregationEligible != tc.eligible {
				t.Fatalf("point=%+v provider=%+v", p, p.Provider)
			}
			if p.Validate() != nil {
				t.Fatal(p.Validate())
			}
		})
	}
}

func TestAppDynamicsAnalyticsSamplesAndWindow(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second).Add(100 * time.Millisecond)
	to := from.Add(61 * time.Second)
	expectedFrom := from.Truncate(time.Second).Add(time.Second)
	expectedTo := to.Truncate(time.Second)
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("start") != jsonNumber(expectedFrom.UnixMilli()) || r.URL.Query().Get("end") != jsonNumber(expectedTo.UnixMilli()-1) {
			t.Fatal(r.URL.Query())
		}
		return jsonResponse(200, `[{"fields":[{"label":"eventTimestamp"},{"label":"application"},{"label":"requestGUID"},{"label":"sql"}],"results":[[`+jsonNumber(expectedFrom.UnixMilli())+`,"display app","01234567-89ab-cdef-0123-456789abcdef","secret captured sql"]]}]`), nil
	})
	result, err := c.Collect(context.Background(), Selection{ProjectID: "p", Applications: []string{"1"}, Analytics: []AnalyticsQuery{{Name: "q", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "log", Query: "SELECT eventTimestamp, application, requestGUID FROM logs"}}}, from, to, "r")
	if err != nil || len(result.Evidence) != 1 {
		t.Fatal(result, err)
	}
	d := result.Evidence[0]
	if d.Kind != "log" || d.ApplicationID != "1" || d.EntityIDs["application"] != "1" || d.Properties["query_name"] != "q" || d.Properties["sql"] != "" {
		t.Fatal(d)
	}
	if !result.Coverage["analytics:0"].From.Equal(expectedFrom) || !result.Coverage["analytics:0"].To.Equal(expectedTo) {
		t.Fatal(result.Coverage)
	}
}

func TestAppDynamicsSharedConcurrentRateBudget(t *testing.T) {
	cfg := Config{Endpoint: "https://shared-rate.example", AccountName: "tenant", AuthType: AuthBasic, EventsEndpoint: "https://events-rate.example", EventsAccountName: "global"}
	credentials := Credentials{Username: "user", Password: "password", EventsAPIKey: "key-one"}
	a, err := NewClient(cfg, credentials)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewClient(cfg, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if a.controllerRate != b.controllerRate || a.analyticsRate != b.analyticsRate {
		t.Fatal("per-client rate budget")
	}
	credentials.EventsAPIKey = "key-two"
	other, err := NewClient(cfg, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if other.controllerRate != a.controllerRate || other.analyticsRate == a.analyticsRate {
		t.Fatal("credential budgets not isolated")
	}
	a.cfg.RequestsPerMinute, b.cfg.RequestsPerMinute = 6000, 6000
	var wg sync.WaitGroup
	var mu sync.Mutex
	times := []time.Time{}
	for i := range 8 {
		wg.Go(func() {
			client := a
			if i%2 != 0 {
				client = b
			}
			if err := client.waitRate(context.Background(), false); err != nil {
				t.Error(err)
			}
			mu.Lock()
			times = append(times, time.Now())
			mu.Unlock()
		})
	}
	wg.Wait()
	earliest, latest := times[0], times[0]
	for _, at := range times {
		if at.Before(earliest) {
			earliest = at
		}
		if at.After(latest) {
			latest = at
		}
	}
	if latest.Sub(earliest) < 60*time.Millisecond {
		t.Fatalf("concurrent requests exceeded rate: %s", latest.Sub(earliest))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
	defer cancel()
	if err := a.waitRate(ctx, true); err != nil {
		t.Fatalf("Controller budget blocked separate Analytics: %v", err)
	}
}

func TestAppDynamicsQueryContinuation(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `[{"metricId":7,"metricPath":"CPU","frequency":"ONE_MIN","metricValues":[{"startTimeInMillis":`+jsonNumber(from.UnixMilli())+`,"value":10}]}]`), nil
	})
	c.cfg.MaxRequestsPerPoll = 1
	s := Selection{ProjectID: "p", Applications: []string{"1"}, Metrics: []MetricSelection{{ApplicationID: "1", Name: "first", Path: "CPU", Unit: "percent"}, {ApplicationID: "1", Name: "second", Path: "CPU", Unit: "percent"}}}
	first, err := c.Collect(context.Background(), s, from, from.Add(time.Minute), "r")
	if err != nil || first.Progress.Complete || first.Progress.NextQuery != 1 || len(first.Coverage) != 1 || len(first.Points) != 1 {
		t.Fatal(first, err)
	}
	s.QueryOffset = first.Progress.NextQuery
	second, err := c.Collect(context.Background(), s, from, from.Add(time.Minute), "r")
	if err != nil || !second.Progress.Complete || second.Progress.NextQuery != 2 || len(second.Coverage) != 1 || second.Points[0].MetricName != "second" {
		t.Fatal(second, err)
	}
}

func TestAppDynamicsCapabilityProofAndStatuses(t *testing.T) {
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "analytics.example":
			data, _ := io.ReadAll(r.Body)
			body := string(data)
			if body != "SELECT COUNT(*) AS value FROM transactions" {
				t.Fatal(body)
			}
			return jsonResponse(200, `[{"fields":[{"label":"value"}],"results":[[0]]}]`), nil
		case strings.HasSuffix(r.URL.Path, "/applications"):
			return jsonResponse(200, `[{"id":1,"name":"app"}]`), nil
		case strings.HasSuffix(r.URL.Path, "/events"):
			return jsonResponse(403, `{}`), nil
		case strings.HasSuffix(r.URL.Path, "/request-snapshots"):
			return jsonResponse(404, `{}`), nil
		default:
			return jsonResponse(200, `[]`), nil
		}
	})
	caps, err := c.Test(context.Background())
	if err != nil || !caps.Analytics || caps.Events || caps.Snapshots || caps.Status["events"] != "forbidden" || caps.Status["snapshots"] != "unsupported" || caps.Status["analytics"] != "available" {
		t.Fatal(caps, err)
	}
	caps.Status["analytics"] = "evil"
	c.capMu.Lock()
	defer c.capMu.Unlock()
	if c.capabilities.Status["analytics"] != "available" {
		t.Fatal("mutable capability alias")
	}
}

func TestAppDynamicsSharedOAuthRefreshAcrossClients(t *testing.T) {
	cfg := Config{Endpoint: "https://shared-oauth.example", AccountName: "tenant", AuthType: AuthOAuth}
	credentials := Credentials{ClientID: "client", ClientSecret: "private-oauth-secret"}
	a, err := NewClient(cfg, credentials)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewClient(cfg, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if a.authState != b.authState {
		t.Fatal("OAuth cache remains per-client")
	}
	a.cfg.RequestsPerMinute, b.cfg.RequestsPerMinute = 1000000, 1000000
	var calls atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return jsonResponse(200, `{"access_token":"shared-token","expires_in":300}`), nil
	})
	a.http.Transport, b.http.Transport = transport, transport
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			c := a
			if i%2 != 0 {
				c = b
			}
			token, err := c.accessToken(context.Background(), "")
			if err != nil || token != "shared-token" {
				t.Error(token, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent NewClient token refreshes=%d", calls.Load())
	}
	a.authState.mu.Lock()
	a.authState.token = "newer-token"
	a.authState.mu.Unlock()
	token, err := b.accessToken(context.Background(), "shared-token")
	if err != nil || token != "newer-token" || calls.Load() != 1 {
		t.Fatal("stale 401 invalidated newer token", token, err, calls.Load())
	}
	credentials.ClientSecret = "rotated-secret"
	rotated, err := NewClient(cfg, credentials)
	if err != nil || rotated.authState == a.authState {
		t.Fatal("secret rotation reused old token", err)
	}
}
func TestAppDynamicsSelectedCapabilityProbeIsApplicationScoped(t *testing.T) {
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "analytics.example" {
			return jsonResponse(200, `[]`), nil
		}
		if !strings.Contains(r.URL.Path, "/applications/7") {
			t.Fatalf("unapproved application probe %s", r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/applications/7") {
			return jsonResponse(200, `[{"id":7,"name":"selected"}]`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/metric-data-v2") && r.URL.Query().Get("metric-path") != "Selected|Metric" {
			t.Fatal("unapproved metric probe")
		}
		return jsonResponse(200, `[]`), nil
	})
	caps, err := c.TestSelection(context.Background(), Selection{ProjectID: "p", Applications: []string{"7"}, Metrics: []MetricSelection{{ApplicationID: "7", Name: "metric", Path: "Selected|Metric"}}})
	if err != nil || !caps.Metrics {
		t.Fatal(caps, err)
	}
}

package appdynamics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func fixtureClient(t *testing.T, auth string, fn roundTripFunc) *Client {
	t.Helper()
	cfg := Config{Endpoint: "https://controller.example", AccountName: "tenant", AuthType: auth, EventsEndpoint: "https://analytics.example", EventsAccountName: "global-account"}
	c, err := NewClientWithHTTPClient(cfg, Credentials{ClientID: "client", ClientSecret: "private-client-secret", Username: "user", Password: "private-password", EventsAPIKey: "private-events-key"}, &http.Client{Transport: fn})
	if err != nil {
		t.Fatal(err)
	}
	c.cfg.RequestsPerMinute = 1000000
	c.cfg.AnalyticsRequestsPerMinute = 1000000
	return c
}
func jsonNumber(value int64) string { data, _ := json.Marshal(value); return string(data) }

func TestAppDynamicsOAuthRefreshAndCredentialIsolation(t *testing.T) {
	var tokens, requests atomic.Int32
	c := fixtureClient(t, AuthOAuth, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/access_token") {
			tokens.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("client_id") != "client@tenant" || r.Form.Get("client_secret") != "private-client-secret" {
				t.Fatal("missing OAuth form")
			}
			return jsonResponse(200, `{"access_token":"token-`+string(rune('0'+tokens.Load()))+`","expires_in":300}`), nil
		}
		if r.Header.Get("X-Events-API-Key") != "" {
			t.Fatal("Events key leaked to Controller")
		}
		if requests.Add(1) == 1 {
			return jsonResponse(401, `{"message":"private-password"}`), nil
		}
		return jsonResponse(200, `[{"id":1,"name":"Application"}]`), nil
	})
	entities, err := c.Discover(context.Background(), "", "applications", "")
	if err != nil || len(entities) != 1 || tokens.Load() != 2 {
		t.Fatalf("entities=%v err=%v tokens=%d", entities, err, tokens.Load())
	}
	data, _ := json.Marshal(c.credentials)
	if string(data) != "{}" {
		t.Fatalf("credentials leaked: %s", data)
	}
}
func TestAppDynamicsConcurrentTokenRefresh(t *testing.T) {
	var tokens atomic.Int32
	c := fixtureClient(t, AuthOAuth, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/access_token") {
			tokens.Add(1)
			return jsonResponse(200, `{"access_token":"fresh","expires_in":5}`), nil
		}
		return jsonResponse(200, `[]`), nil
	})
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_, err := c.Discover(context.Background(), "", "applications", "")
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if tokens.Load() != 1 {
		t.Fatalf("refresh stampede: %d", tokens.Load())
	}
}
func TestAppDynamicsNetworkPolicy(t *testing.T) {
	approved := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("fd00::/8")}
	for _, tc := range []struct {
		ip   string
		want bool
	}{{"8.8.8.8", true}, {"10.2.3.4", true}, {"fd00::12", true}, {"192.168.1.2", false}, {"127.0.0.1", false}, {"169.254.169.254", false}, {"fd00:ec2::254", false}, {"168.63.129.16", false}, {"::ffff:127.0.0.1", false}, {"2001:db8::1", false}, {"0.0.0.0", false}} {
		if got := allowedAddress(netip.MustParseAddr(tc.ip), approved); got != tc.want {
			t.Errorf("%s=%v", tc.ip, got)
		}
	}
}
func TestAppDynamicsConfigurationValidation(t *testing.T) {
	for _, origin := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/controller", "https://example.com?q=secret", "https://example.com#fragment"} {
		_, err := NewClient(Config{Endpoint: origin, AccountName: "tenant"}, Credentials{ClientID: "id", ClientSecret: "secret"})
		if err == nil {
			t.Errorf("accepted unsafe origin %s", origin)
		}
	}
	_, err := NewClient(Config{Endpoint: "https://example.com", AccountName: "tenant", CAPEM: "bad cert"}, Credentials{ClientID: "id", ClientSecret: "secret"})
	if err == nil {
		t.Fatal("accepted invalid CA")
	}
	selection := Selection{ProjectID: "p", Applications: []string{"1"}, Analytics: []AnalyticsQuery{{Name: "query", Query: "SELECT value FROM transactions; DELETE transactions"}}}
	if selection.Validate() == nil {
		t.Fatal("accepted multiple statements")
	}
	selection.Analytics = nil
	selection.Applications = []string{"1", "1"}
	if selection.Validate() == nil {
		t.Fatal("accepted duplicate applications")
	}
}
func TestAppDynamicsResponseBoundsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		body, contentType string
		max               int
	}{{"oversize", 200, strings.Repeat("a", 2048), "application/json", 1024}, {"malformed", 200, `[] []`, "application/json", 0}, {"xml", 200, `<secret>private-password</secret>`, "text/xml", 0}, {"unauthorized", 403, `{"secret":"private-password"}`, "application/json", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
				r := jsonResponse(tc.status, tc.body)
				r.Header.Set("Content-Type", tc.contentType)
				return r, nil
			})
			if tc.max > 0 {
				c.cfg.MaxResponseBytes = tc.max
			}
			_, err := c.Discover(context.Background(), "", "applications", "")
			if err == nil || strings.Contains(err.Error(), "private-password") {
				t.Fatalf("unsafe/missing error %v", err)
			}
		})
	}
}
func TestAppDynamicsNormalizeSourceStatistics(t *testing.T) {
	now := time.Now().UTC()
	zero, one, ten, minimum, maximum := 0.0, 1.0, 10.0, 2147483647.0, -2147483648.0
	for _, tc := range []struct {
		name, stat string
		value      metricValue
		quality    string
		eligible   bool
	}{{"sum zero", "sum", metricValue{Value: &zero, Count: &zero, Sum: &zero, Min: &minimum, Max: &maximum}, analysis.QualityValid, true}, {"average empty", "average", metricValue{Value: &zero, Count: &zero, Sum: &zero}, analysis.QualityMissing, false}, {"average valid", "average", metricValue{Value: &ten, Count: &one, Sum: &ten}, analysis.QualityValid, true}, {"percentile", "p95", metricValue{Value: &ten, Count: &one, Sum: &ten}, analysis.QualityValid, false}} {
		t.Run(tc.name, func(t *testing.T) {
			tc.value.Start = now.Add(-5 * time.Minute).UnixMilli()
			p, err := NormalizeMetric(MetricSelection{ApplicationID: "1", Name: "metric", Statistic: tc.stat}, metricResponse{ID: "7", Path: "metric", Frequency: "ONE_MIN"}, tc.value, "project", "run", now, 120)
			if err != nil || p.Quality != tc.quality || p.Provider.AggregationEligible != tc.eligible || p.Provider.Min != nil || p.Provider.Max != nil {
				t.Fatalf("point=%#v err=%v", p, err)
			}
			if tc.stat == "sum" && (p.Value == nil || *p.Value != 0) {
				t.Fatal("lost real zero")
			}
			if !p.Provider.Settled {
				t.Fatal("old bucket did not settle")
			}
		})
	}
}
func TestAppDynamicsCollectLegacyAndMissingCoverage(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	to := from.Add(5 * time.Minute)
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "metric-data-v2") {
			return jsonResponse(404, `{}`), nil
		}
		if r.URL.Query().Get("rollup") != "false" || r.URL.Query().Get("time-range-type") != "BETWEEN_TIMES" {
			t.Fatal("unexpected time query")
		}
		return jsonResponse(200, `[{"metricId":7,"metricPath":"Overall Application Performance|Calls per Minute","frequency":"ONE_MIN","metricValues":[{"startTimeInMillis":`+jsonNumber(from.UnixMilli())+`,"value":0,"count":0,"sum":0,"useRange":false}]}]`), nil
	})
	result, err := c.Collect(context.Background(), Selection{ProjectID: "project", Applications: []string{"1"}, Metrics: []MetricSelection{{ApplicationID: "1", Path: "Overall Application Performance|Calls per Minute", Name: "calls"}}}, from, to, "run")
	if err != nil || len(result.Points) != 1 || result.Points[0].Value == nil || *result.Points[0].Value != 0 || result.Capabilities.MetricAPI != "metric-data" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Coverage["metrics:0"].Status != "complete" {
		t.Fatal(result.Coverage)
	}
}
func TestAppDynamicsAnalyticsIsSeparateAndRedacted(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	to := from.Add(time.Minute)
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "analytics.example" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Events-API-Key") != "private-events-key" {
			t.Fatal("incorrect credential boundary")
		}
		return jsonResponse(200, `[{"fields":[{"label":"eventTimestamp"},{"label":"requestGUID"},{"label":"authorization"}],"results":[[`+jsonNumber(from.UnixMilli())+`,"private-password","Bearer private-events-key"]]}]`), nil
	})
	result, err := c.Collect(context.Background(), Selection{ProjectID: "project", Applications: []string{"1"}, Analytics: []AnalyticsQuery{{Name: "transactions", ApplicationID: "1", ApplicationIDField: "applicationId", Query: "SELECT eventTimestamp, requestGUID FROM transactions"}}}, from, to, "run")
	if err != nil || len(result.Evidence) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "private-password") || strings.Contains(string(data), "private-events-key") || strings.Contains(string(data), "authorization") {
		t.Fatalf("unredacted evidence %s", data)
	}
}
func TestAppDynamicsBudgetAndPointLimitClassification(t *testing.T) {
	c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		return jsonResponse(429, `{"message":"Metric data point limit exceeded"}`), nil
	})
	_, err := c.Discover(context.Background(), "", "applications", "")
	var status *HTTPError
	if !errors.As(err, &status) || status.Kind != "point budget exceeded" {
		t.Fatalf("error=%v", err)
	}
	ctx := context.WithValue(context.Background(), budgetKey{}, &pollBudget{remaining: 0})
	_, err = c.Discover(ctx, "", "applications", "")
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("expected budget error: %v", err)
	}
}
func TestAppDynamicsEvidenceDropsCapturedNarratives(t *testing.T) {
	from := time.Now().UTC().Add(-time.Minute)
	c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `[{"id":42,"eventTime":`+jsonNumber(from.UnixMilli())+`,"type":"APPLICATION_ERROR","summary":"SQL private-password","description":"secret customer data","severity":"ERROR","deepLinkUrl":"https://evil.example/?token=private-events-key"}]`), nil
	})
	items, _, err := c.diagnostics(context.Background(), "1", "events", from, from.Add(time.Minute), Selection{}, 0)
	if err != nil || len(items) != 1 {
		t.Fatal(err, items)
	}
	data, _ := json.Marshal(items)
	if strings.Contains(string(data), "customer") || strings.Contains(string(data), "private-") || strings.Contains(string(data), "evil.example") {
		t.Fatalf("unredacted narrative %s", data)
	}
}

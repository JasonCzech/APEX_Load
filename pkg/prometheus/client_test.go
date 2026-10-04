package prometheus

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/jasonczech/apexload/pkg/analysis"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func success(data any) *http.Response {
	b, _ := json.Marshal(map[string]any{"status": "success", "data": data})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b))}
}
func rawResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func fixture(t *testing.T, cfg Config, f roundTripFunc) *Client {
	t.Helper()
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://prometheus.example"
	}
	c, err := NewClientWithHTTPClient(cfg, Credentials{}, &http.Client{Transport: f})
	if err != nil {
		t.Fatal(err)
	}
	c.rate.skipWait = true
	return c
}
func querySelection() Selection {
	return Selection{ProjectID: "project", StepSeconds: 15, Queries: []Query{{ID: "cpu", Name: "cpu_usage", Expression: `sum by (job) (rate(cpu_seconds_total{job="sut"}[1m]))`, AllowedLabels: []string{"job"}, Semantics: "rate", Unit: "cores"}}}
}
func matrix(labels map[string]string, values ...[]any) any {
	return map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": labels, "values": values}}}
}
func TestConfigurationAndLocalParser(t *testing.T) {
	good := []string{`up{job="sut"}`, `sum(rate(requests_total{job="sut"}[1m]))`, `histogram_quantile(0.95,sum by(le)(rate(duration_bucket{job="sut"}[1m])))`, `vector(0)`, `up @ 100`, `avg_over_time(up[5m:1m] @ 100)`}
	for _, e := range good {
		if err := validateExpression(e); err != nil {
			t.Errorf("valid expression rejected %s: %v", e, err)
		}
	}
	bad := []string{"", `up[5m]`, `up @ start()`, `up @ end()`, `up @ -1`, `up @ 253402300800`, `up offset -1m`, `rate(up[25h])`, `up +`, strings.Repeat("(", 257) + "up" + strings.Repeat(")", 257)}
	for _, e := range bad {
		if err := validateExpression(e); err == nil {
			t.Errorf("unsupported expression accepted: %s", e)
		}
	}
	if err := validateTerminalExpression(`increase(requests_total[{{window.duration}}])`); err != nil {
		t.Fatal(err)
	}
	if err := validateTerminalExpression(`up{job="{{run.id}}"}`); err == nil {
		t.Fatal("unknown template accepted")
	}
	for _, cfg := range []Config{{Endpoint: "http://public.example"}, {Endpoint: "https://user:password@prom.example"}, {Endpoint: "https://prom.example/path"}, {Endpoint: "https://prom.example?token=x"}, {Endpoint: "https://prom.example", ContextPath: "/../admin"}, {Endpoint: "https://prom.example", AuthType: "oauth2"}, {Endpoint: "https://workspace.eastus.prometheus.monitor.azure.com", BackendProfile: BackendAzureManaged, AuthType: AuthAzure, AzureCredentialMode: "default"}} {
		if cfg.Validate() == nil {
			t.Fatalf("invalid config accepted: %#v", cfg)
		}
	}
	private := Config{Endpoint: "http://prom.internal", AllowPrivateHTTP: true, ApprovedCIDRs: []string{"10.0.0.0/8"}}
	if err := private.Validate(); err != nil {
		t.Fatal(err)
	}
	s := querySelection()
	s.Queries[0].AllowedLabels = []string{"password"}
	if s.Validate() == nil {
		t.Fatal("sensitive allowed label accepted")
	}
	s = querySelection()
	s.Queries = append(s.Queries, s.Queries[0])
	if s.Validate() == nil {
		t.Fatal("duplicate IDs accepted")
	}
	s = querySelection()
	s.Queries[0].Expression = `{job="sut"}`
	if (JobConfig{Config{Endpoint: "https://workspace.eastus.prometheus.monitor.azure.com", BackendProfile: BackendAzureManaged, AuthType: AuthAzure, AzureCredentialMode: "managed_identity"}, s}).Validate() == nil {
		t.Fatal("Azure implicit metric selector accepted")
	}
}
func TestNetworkApprovalAndCredentialNondisclosure(t *testing.T) {
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	for _, tc := range []struct {
		address           string
		privateOnly, want bool
	}{{"8.8.8.8", false, true}, {"8.8.8.8", true, false}, {"10.2.3.4", true, true}, {"127.0.0.1", false, false}, {"::ffff:127.0.0.1", false, false}, {"169.254.169.254", false, false}, {"168.63.129.16", false, false}, {"fd00:ec2::254", false, false}, {"192.0.2.1", false, false}, {"0.0.0.0", false, false}} {
		if got := allowedAddress(netip.MustParseAddr(tc.address), prefixes, tc.privateOnly); got != tc.want {
			t.Errorf("address %s: got %v", tc.address, got)
		}
	}
	encoded, _ := json.Marshal(Credentials{Token: "bearer", Username: "admin", Password: "password", ClientSecret: "secret", ClientCertificate: "certificate", ClientKey: "privatekey"})
	if string(encoded) != "{}" {
		t.Fatalf("credentials disclosed: %s", encoded)
	}
	c, err := NewClient(Config{Endpoint: "https://127.0.0.1", ApprovedCIDRs: []string{"0.0.0.0/0"}}, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.query(ctx, "/api/v1/query", url.Values{"query": {"up"}}); err == nil {
		t.Fatal("loopback destination was reachable")
	}
}
func TestZerosMissingNonFiniteAndNativeHistograms(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.Header.Get("Accept") != "application/json" {
			t.Fatal("query must explicitly request JSON using form POST")
		}
		return rawResponse(fmt.Sprintf(`{"status":"success","warnings":["results truncated due to limit"],"infos":["mixed samples omitted"],"data":{"resultType":"matrix","result":[{"metric":{"job":"sut"},"values":[[%d,"0"],[%d,"NaN"]],"histograms":[[%d,{"count":"1"}]]}]}}`, from.Unix(), from.Add(30*time.Second).Unix(), from.Add(45*time.Second).Unix())), nil
	})
	result, err := c.Collect(context.Background(), querySelection(), from, from.Add(45*time.Second), "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) != 4 || result.Points[0].Value == nil || *result.Points[0].Value != 0 {
		t.Fatalf("zero missing or grid incorrect: %+v", result.Points)
	}
	for _, p := range result.Points[1:] {
		if p.Value != nil || p.Quality != analysis.QualityMissing {
			t.Fatalf("missing/nonfinite/native histogram became value: %+v", p)
		}
	}
	for _, p := range result.Points {
		if p.Dimensions["password"] != "" || p.Dimensions["instance"] != "" || !p.Provider.BucketStart.IsZero() || !p.Provider.BucketEnd.IsZero() || p.Provider.Mode != "evaluation_point" {
			t.Fatalf("disclosure or fake bucket: %+v", p)
		}
	}
	cov := result.Coverage["cpu"]
	if !cov.Truncated || cov.MissingPoints != 3 || cov.ExpectedPoints != 4 || len(cov.Infos) != 1 || cov.Freshness != "unknown" {
		t.Fatalf("coverage: %+v", cov)
	}
}
func TestFreshnessMatchesLabelsAndExcludesStale(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	s := querySelection()
	s.Queries[0].FreshnessExpression = `timestamp(cpu_seconds_total{job="sut"})`
	s.Queries[0].MaxSampleAgeSeconds = 90
	c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		if strings.HasPrefix(r.Form.Get("query"), "timestamp") {
			return success(matrix(map[string]string{"job": "sut"}, []any{from.Unix(), fmt.Sprint(from.Add(-120 * time.Second).Unix())}, []any{from.Add(15 * time.Second).Unix(), fmt.Sprint(from.Unix())})), nil
		}
		return success(matrix(map[string]string{"__name__": "cpu_seconds_total", "job": "sut"}, []any{from.Unix(), "1"}, []any{from.Add(15 * time.Second).Unix(), "2"})), nil
	})
	r, err := c.Collect(context.Background(), s, from, from.Add(15*time.Second), "run")
	if err != nil {
		t.Fatal(err)
	}
	if r.Points[0].Quality != analysis.QualityStale || r.Points[0].Provider.AggregationEligible || !r.Points[0].Provider.FreshnessKnown || r.Points[1].Quality != analysis.QualityValid {
		t.Fatalf("freshness: %+v", r.Points)
	}
	if r.Coverage["cpu"].Freshness != "stale" || r.Coverage["cpu"].MissingPoints != 1 {
		t.Fatalf("coverage: %+v", r.Coverage)
	}
}
func TestGridChunkContinuationCommitsOnlyCompleteChunks(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 123000000, time.UTC)
	anchor := from
	s := querySelection()
	s.GridAnchor = &anchor
	var starts []string
	c := fixture(t, Config{MaxPoints: 2, MaxSeries: 1, MaxRequestsPerPoll: 1}, func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		starts = append(starts, r.Form.Get("start"))
		start, _ := strconvTimestamp(r.Form.Get("start"))
		return success(matrix(map[string]string{"job": "sut"}, []any{float64(start.UnixMilli()) / 1000, "1"}, []any{float64(start.Add(15*time.Second).UnixMilli()) / 1000, "2"})), nil
	})
	r, err := c.Collect(context.Background(), s, from.Add(time.Second), from.Add(time.Minute), "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Points) != 2 || r.Progress.Complete || r.Progress.NextTime == nil || !r.Progress.NextTime.Equal(from.Add(45*time.Second)) {
		t.Fatalf("continuation: %+v", r)
	}
	s.QueryTime = r.Progress.NextTime
	s.QueryOffset = r.Progress.NextQuery
	r2, err := c.Collect(context.Background(), s, from.Add(time.Second), from.Add(time.Minute), "run")
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Progress.Complete || len(r2.Points) != 2 || r2.Points[0].TimestampUTC.Equal(r.Points[1].TimestampUTC) {
		t.Fatalf("duplicate chunk grid: %+v", r2)
	}
	if starts[0] != unixSeconds(from.Add(15*time.Second)) || starts[1] != unixSeconds(from.Add(45*time.Second)) {
		t.Fatalf("grid drifted: %v", starts)
	}
}
func strconvTimestamp(s string) (time.Time, error) {
	d, err := time.ParseDuration(s + "s")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, int64(d)).UTC(), nil
}
func TestTerminalUsesExplicitWholeWindowAndStableDefinition(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 123456000, time.UTC)
	to := from.Add(90 * time.Second)
	s := querySelection()
	s.Queries[0].TerminalExpression = `increase(requests_total[{{window.duration}}])`
	c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		if r.Form.Get("query") != `increase(requests_total[90000ms])` || r.Form.Get("time") != unixSeconds(to) {
			t.Fatalf("terminal query: %v", r.Form)
		}
		return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{"job": "sut"}, "value": []any{float64(to.UnixMilli()) / 1000, "12"}}}}), nil
	})
	r, err := c.CollectTerminal(context.Background(), s, from, to, "run")
	if err != nil {
		t.Fatal(err)
	}
	p := r.Points[0]
	if !p.Provider.QueryWindow || p.Provider.Mode != "query_window" || !p.Provider.BucketStart.Equal(from) || !p.Provider.BucketEnd.Equal(to) || !p.TimestampUTC.Equal(to) || p.Provider.DefinitionHash != DefinitionHash(s.Queries[0]) {
		t.Fatalf("incorrect terminal provenance: %+v", p)
	}
}

func TestTerminalWarningResponsesKeepPartialCoverage(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	for _, warningQuery := range []string{"main", "freshness"} {
		t.Run(warningQuery, func(t *testing.T) {
			s := querySelection()
			s.Queries[0].Expression = `requests_total{job="sut"}`
			s.Queries[0].TerminalExpression = `increase(requests_total{job="sut"}[{{window.duration}}])`
			s.Queries[0].FreshnessExpression = `timestamp(requests_total{job="sut"})`
			c := fixture(t, Config{}, func(req *http.Request) (*http.Response, error) {
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				isFreshness := strings.HasPrefix(req.Form.Get("query"), "timestamp")
				value := "12"
				if isFreshness {
					value = strconv.FormatInt(to.Unix(), 10)
				}
				data := map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{"job": "sut"}, "value": []any{to.Unix(), value}}}}
				if isFreshness == (warningQuery == "freshness") {
					body, err := json.Marshal(map[string]any{"status": "success", "data": data, "warnings": []string{"remote query returned partial data"}, "infos": []string{"upstream annotation"}})
					if err != nil {
						t.Fatal(err)
					}
					return rawResponse(string(body)), nil
				}
				return success(data), nil
			})
			result, err := c.CollectTerminal(context.Background(), s, from, to, "run")
			if err != nil {
				t.Fatal(err)
			}
			coverage := result.Coverage["cpu"]
			if coverage.Status != "partial" || len(coverage.Warnings) != 1 || len(coverage.Infos) != 1 || len(result.Points) != 1 || result.Points[0].Value == nil || *result.Points[0].Value != 12 {
				t.Fatalf("warning response was lost or treated as complete: %+v", result)
			}
		})
	}
}

func TestResponseBoundsRedirectAndReadOnlyRoutes(t *testing.T) {
	calls := 0
	c := fixture(t, Config{MaxResponseBytes: 1024}, func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.example"}}, Body: io.NopCloser(strings.NewReader("secret"))}, nil
	})
	if _, err := c.query(context.Background(), "/api/v1/query", url.Values{"query": {"up"}}); err == nil || calls != 1 {
		t.Fatalf("redirect followed: %d %v", calls, err)
	}
	for _, p := range []string{"/-/reload", "/api/v1/admin/tsdb/delete_series", "/api/v1/read", "/api/v1/write", "/api/v1/status/config"} {
		if _, err := c.query(context.Background(), p, nil); err == nil {
			t.Fatalf("write/sensitive route allowed: %s", p)
		}
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(bytes.Repeat([]byte("x"), 2048))
	w.Close()
	c = fixture(t, Config{MaxResponseBytes: 1024}, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}, Body: io.NopCloser(bytes.NewReader(buf.Bytes()))}, nil
	})
	if _, err := c.query(context.Background(), "/api/v1/query", url.Values{"query": {"up"}}); err == nil || !strings.Contains(err.Error(), "decompressed") {
		t.Fatalf("gzip byte bound: %v", err)
	}
	c = fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		return rawResponse(`{"status":"success","data":{}} {}`), nil
	})
	if _, err := c.query(context.Background(), "/api/v1/query", nil); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

type fakeCredential struct {
	calls  atomic.Int32
	scopes []string
	mu     sync.Mutex
}

func (f *fakeCredential) GetToken(_ context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.scopes = o.Scopes
	f.mu.Unlock()
	return azcore.AccessToken{Token: "azure-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}
func TestAzureExplicitAudienceRefreshAndCaseIdentity(t *testing.T) {
	c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer azure-token" {
			t.Fatal("Azure authorization missing")
		}
		return success(map[string]any{"resultType": "vector", "result": []any{}}), nil
	})
	f := &fakeCredential{}
	c.cfg.AuthType = AuthAzure
	c.azure = f
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.accessToken(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.calls.Load() != 1 || len(f.scopes) != 1 || f.scopes[0] != AzureScope {
		t.Fatalf("audience/refresh: %d %v", f.calls.Load(), f.scopes)
	}
	c.authState.token.ExpiresOn = time.Now().Add(time.Second)
	if _, err := c.accessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 {
		t.Fatal("expiry did not trigger refresh")
	}
	if seriesIdentity(map[string]string{"Job": "SUT"}, BackendAzureManaged) != seriesIdentity(map[string]string{"job": "sut"}, BackendAzureManaged) {
		t.Fatal("Azure case insensitive identity not canonical")
	}
	c.cfg.BackendProfile = BackendAzureManaged
	e := envelope{Data: json.RawMessage(`{"resultType":"matrix","result":[{"metric":{"Job":"SUT"},"values":[]},{"metric":{"job":"sut"},"values":[]}]}`)}
	if _, _, err := c.observations(e, true); err == nil {
		t.Fatal("conflicting Azure identity accepted")
	}
}
func TestSafeErrorsAndDiagnostics(t *testing.T) {
	c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 422, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"private SQL password=hunter2 exceeds sample limit"}`))}, nil
	})
	_, err := c.query(context.Background(), "/api/v1/query", nil)
	var e *HTTPError
	if !errors.As(err, &e) || e.Kind != "sample budget exceeded" || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("unsafe/misclassified error: %v", err)
	}
	c.credentials.Token = "explicit-secret"
	if strings.Contains(c.safeText("warning explicit-secret password=other", 256), "explicit-secret") || strings.Contains(c.safeText("warning explicit-secret password=other", 256), "other") {
		t.Fatal("upstream diagnostics disclose secrets")
	}
	data := json.RawMessage(`{"activeTargets":[{"labels":{"job":"sut","instance":"server:9090","password":"hidden"},"health":"down","scrapeUrl":"https://user:password@private/path?token=hidden","lastError":"SQL and secret","lastScrape":"2025-01-01T00:00:00Z"}]}`)
	records, _, err := c.targetEvidence(envelope{Data: data}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(records)
	if strings.Contains(string(encoded), "hidden") || strings.Contains(string(encoded), "SQL") || strings.Contains(string(encoded), "scrapeUrl") {
		t.Fatalf("unsafe projection: %s", encoded)
	}
}
func TestDiscoveryExplicitTruncationAndAzureRestrictions(t *testing.T) {
	c := fixture(t, Config{MaxSeries: 2}, func(r *http.Request) (*http.Response, error) { return success([]string{"c", "a", "b"}), nil })
	entities, err := c.Discover(context.Background(), "", "metrics", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 3 || entities[2].Type != "truncation" || entities[0].Name != "a" {
		t.Fatalf("discovery limit: %+v", entities)
	}
	c.cfg.BackendProfile = BackendAzureManaged
	if _, err := c.Discover(context.Background(), `up{job=~".*"}`, "series", ""); err == nil {
		t.Fatal("Azure series regex accepted")
	}
	if _, err := c.Discover(context.Background(), "", "targets", ""); err == nil {
		t.Fatal("unsupported Azure targets accepted")
	}
}

func TestTargetDiscoveryAppliesSelectorBeforeRetention(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selected  int
		wantRows  int
		truncated bool
	}{{"two_jobs", 1, 1, false}, {"bounded_scope", 101, 100, true}} {
		t.Run(tc.name, func(t *testing.T) {
			targets := []any{map[string]any{"labels": map[string]string{"job": "generator", "instance": "generator:9090"}, "health": "up"}}
			for i := 0; i < tc.selected; i++ {
				targets = append(targets, map[string]any{"labels": map[string]string{"job": "sut", "namespace": "prod", "instance": fmt.Sprintf("sut-%d:9090", i)}, "health": "up"})
			}
			c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v1/targets" {
					t.Fatalf("unexpected discovery endpoint %s", r.URL.Path)
				}
				// Prometheus targets ignores match[]; filtering is a client duty.
				return success(map[string]any{"activeTargets": targets}), nil
			})
			entities, err := c.Discover(context.Background(), `{job="sut",namespace="prod"}`, "targets", "")
			if err != nil {
				t.Fatal(err)
			}
			wantCount := tc.wantRows
			if tc.truncated {
				wantCount++
			}
			if len(entities) != wantCount {
				t.Fatalf("discovery count: %+v", entities)
			}
			for _, entity := range entities[:tc.wantRows] {
				if entity.Type != "target" || entity.EntityIDs["job"] != "sut" || entity.EntityIDs["namespace"] != "prod" {
					t.Fatalf("unrelated target or missing approved selector label: %+v", entity)
				}
			}
			if tc.truncated && entities[len(entities)-1].Type != "truncation" {
				t.Fatal("bounded target catalog omitted truncation notice")
			}
		})
	}
}

func TestResultLabelsCannotBeSilentlyDiscarded(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, labels := range []map[string]string{{"job": "sut", "instance": "unapproved"}, {"job": "sut", "password": "hidden"}, {"job": "sut", "le": "1"}} {
		c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
			return success(matrix(labels, []any{from.Unix(), "1"})), nil
		})
		result, err := c.Collect(context.Background(), querySelection(), from, from.Add(time.Minute), "run")
		if err == nil || len(result.Points) != 0 || strings.Contains(err.Error(), "hidden") {
			t.Fatalf("unapproved identity accepted: %+v %v", result, err)
		}
	}
}

func TestApprovedOutputLabelNameBounds(t *testing.T) {
	c := fixture(t, Config{}, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	for _, length := range []int{128, 129} {
		name := strings.Repeat("a", length)
		s := querySelection()
		s.Queries[0].AllowedLabels = []string{name}
		definitionError := s.Validate()
		resultError := c.validateResultLabels(map[string]string{name: "sut"}, []string{name})
		if length == 128 && (definitionError != nil || resultError != nil) {
			t.Fatalf("128-byte approved label rejected: %v %v", definitionError, resultError)
		}
		if length == 129 && (definitionError == nil || resultError == nil) {
			t.Fatalf("129-byte label passed definition/readiness contract: %v %v", definitionError, resultError)
		}
	}
}

func TestSelectedReadinessChecksAllQueriesAndCompanions(t *testing.T) {
	for _, mode := range []string{"missing", "histogram", "unapproved", "freshness"} {
		c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/v1/query" {
				return success([]any{}), nil
			}
			r.ParseForm()
			switch mode {
			case "missing":
				return success(map[string]any{"resultType": "vector", "result": []any{}}), nil
			case "histogram":
				return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{"job": "sut"}, "histogram": []any{time.Now().Unix(), map[string]string{"count": "1"}}}}}), nil
			case "unapproved":
				return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{"job": "sut", "instance": "server"}, "value": []any{time.Now().Unix(), "1"}}}}), nil
			case "freshness":
				if strings.HasPrefix(r.Form.Get("query"), "timestamp") {
					return success(map[string]any{"resultType": "vector", "result": []any{}}), nil
				}
			}
			return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{"job": "sut"}, "value": []any{time.Now().Unix(), "1"}}}}), nil
		})
		s := querySelection()
		if mode == "freshness" {
			s.Queries[0].FreshnessExpression = `timestamp(cpu_seconds_total{job="sut"})`
		}
		caps, _ := c.TestSelection(context.Background(), s)
		if caps.Metrics {
			t.Fatalf("readiness passed %s: %+v", mode, caps)
		}
	}
}

func TestDiagnosticScopeFiltersBeforeRetentionAndUsesObservedTime(t *testing.T) {
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	c := fixture(t, Config{}, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	data := json.RawMessage(`{"activeTargets":[{"labels":{"job":"generator","namespace":"test"},"health":"up"},{"labels":{"job":"sut","namespace":"prod"},"health":"down","lastScrape":"2024-12-01T00:00:00Z"}]}`)
	rows, truncated, err := c.targetEvidence(envelope{Data: data}, now, `{namespace="prod",job="sut"}`)
	if err != nil || truncated || len(rows) != 1 || rows[0].EntityIDs["namespace"] != "prod" || !rows[0].TimestampUTC.Equal(now) || rows[0].Properties["last_scrape_utc"] != "2024-12-01T00:00:00Z" {
		t.Fatalf("scope/observation: %+v %v", rows, err)
	}
	if !MatchesEvidence(`{namespace="prod",job="sut"}`, rows[0].EntityIDs) || MatchesEvidence(`{job="generator"}`, rows[0].EntityIDs) {
		t.Fatal("ingestion scope verifier disagrees")
	}
	alerts := json.RawMessage(`{"alerts":[{"labels":{"alertname":"GeneratorCPU","job":"generator"},"state":"firing"},{"labels":{"alertname":"SUTError","job":"sut"},"state":"firing","activeAt":"2024-12-01T00:00:00Z"}]}`)
	rows, _, err = c.alertEvidence(envelope{Data: alerts}, now, `{job="sut"}`)
	if err != nil || len(rows) != 1 || rows[0].Summary != "SUTError" || rows[0].Properties["active_at"] != "2024-12-01T00:00:00Z" || !rows[0].TimestampUTC.Equal(now) {
		t.Fatalf("alert scope: %+v %v", rows, err)
	}
	s := querySelection()
	s.Targets = true
	if s.Validate() == nil {
		t.Fatal("unscoped diagnostic source accepted")
	}
	s.EvidenceSelector = `{job="sut"}`
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"up", `sum(up{job="sut"})`, `{password="secret"}`} {
		s.EvidenceSelector = selector
		if s.Validate() == nil {
			t.Fatalf("unsafe scope accepted %s", selector)
		}
	}
}

func TestPrivateHTTPRejectsCredentialMaterialAndWorkloadIdentityIsPinned(t *testing.T) {
	cfg := Config{Endpoint: "http://prom.internal", AllowPrivateHTTP: true, ApprovedCIDRs: []string{"10.0.0.0/8"}}
	for _, auth := range []string{AuthBasic, AuthBearer, AuthAzure} {
		candidate := cfg
		candidate.AuthType = auth
		if candidate.Validate() == nil {
			t.Fatalf("HTTP permitted auth %s", auth)
		}
	}
	candidate := cfg
	candidate.MTLS = true
	if candidate.Validate() == nil {
		t.Fatal("HTTP permitted mTLS")
	}
	for _, creds := range []Credentials{{Token: "token"}, {Username: "username"}, {Password: "password"}, {ClientSecret: "secret"}, {ClientCertificate: "cert"}, {ClientKey: "key"}} {
		if validateCredentials(cfg.Defaults(), creds) == nil {
			t.Fatal("HTTP accepted unused credential material")
		}
	}
	azure := Config{Endpoint: "https://workspace.eastus.prometheus.monitor.azure.com", BackendProfile: BackendAzureManaged, AuthType: AuthAzure, AzureCredentialMode: "workload_identity"}
	if azure.Validate() == nil {
		t.Fatal("ambient workload identity accepted")
	}
	azure.ClientID = "client"
	azure.AzureTenantID = "tenant"
	if err := azure.Validate(); err != nil {
		t.Fatal(err)
	}
	other := azure
	other.AzureTenantID = "other"
	if QuotaKey(azure, Credentials{}) == QuotaKey(other, Credentials{}) {
		t.Fatal("Azure tenant omitted from quota identity")
	}
}
func TestTerminalRejectsRollingOrInstantDependencies(t *testing.T) {
	for _, expr := range []string{`counter_total`, `rate(counter_total[1m])`, `sum(increase(counter_total[{{window.duration}}]))+sum(other_total)`, `sum(increase(counter_total[{{window.duration}}] offset 1m))`, `increase(counter_total[1234567ms])+0*increase(other_total[{{window.duration}}])`, `increase(counter_total[20m34.567s])+0*increase(other_total[{{window.duration}}])`, `sum_over_time(rate(counter_total[1m])[{{window.duration}}:15s])`, `sum_over_time(rate(counter_total[{{window.duration}}])[{{window.duration}}:15s])`, `avg_over_time(avg_over_time(up[{{window.duration}}:15s])[{{window.duration}}:15s])`} {
		if validateTerminalExpression(expr) == nil {
			t.Fatalf("not an exact window: %s", expr)
		}
	}
	for _, expr := range []string{`sum(increase(counter_total[{{window.duration}}]))`, `histogram_quantile(0.95,sum by(le)(rate(duration_bucket[{{window.duration}}])))`, `1+sum(increase(counter_total[{{window.duration}}]))`, `avg_over_time(up{job="sut"}[{{window.duration}}:15s])`, `sum(increase(counter_total[{{window.duration}}]))+sum(increase(other_total[{{window.duration}}]))`} {
		if err := validateTerminalExpression(expr); err != nil {
			t.Fatalf("whole window rejected %s: %v", expr, err)
		}
	}
	if QueryStatistic(Query{Semantics: "count"}, true) != "total" || QueryStatistic(Query{Semantics: "count"}, false) != "count" {
		t.Fatal("rolling counts promoted to interval totals")
	}
}

func TestDiagnosticCacheSurvivesClientRecreationWithoutReplayingEvidence(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	s := querySelection()
	s.Targets = true
	s.EvidenceSelector = `{job="sut"}`
	s.CollectionID = "cache-job-one"
	var calls atomic.Int32
	transport := func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/targets" {
			calls.Add(1)
			return rawResponse(`{"status":"success","data":{"activeTargets":[{"labels":{"job":"sut","instance":"fixture"},"health":"up"}]}}`), nil
		}
		r.ParseForm()
		start, _ := strconvTimestamp(r.Form.Get("start"))
		return success(matrix(map[string]string{"job": "sut"}, []any{float64(start.UnixMilli()) / 1000, "1"})), nil
	}
	first := fixture(t, Config{}, transport)
	r, err := first.Collect(context.Background(), s, from, from.Add(time.Second), "diagnostic-cache-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Evidence) != 1 || r.Coverage["targets"].ObservedAt == nil {
		t.Fatalf("first observation missing: %+v", r)
	}
	second := fixture(t, Config{}, transport)
	cached, err := second.Collect(context.Background(), s, from, from.Add(time.Second), "diagnostic-cache-run")
	if err != nil {
		t.Fatal(err)
	}
	cov := cached.Coverage["targets"]
	if calls.Load() != 1 || len(cached.Evidence) != 0 || !cov.Cached || !cov.ObservedAt.Equal(*r.Coverage["targets"].ObservedAt) {
		t.Fatalf("cached evidence replayed: %+v calls=%d", cached, calls.Load())
	}
	s.CollectionID = "cache-job-two"
	if _, err := second.Collect(context.Background(), s, from, from.Add(time.Second), "diagnostic-cache-run"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("new source job lost its first diagnostic observation")
	}
	if _, err := second.Collect(context.Background(), s, from, from.Add(time.Second), "another-diagnostic-run"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("new run lost its first diagnostic observation")
	}
}

func TestDefaultGridCeilsNanosecondWindowToMillisecond(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 123456789, time.UTC)
	expected := from.Truncate(time.Millisecond).Add(time.Millisecond)
	c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		start, err := strconvTimestamp(r.Form.Get("start"))
		if err != nil {
			t.Fatal(err)
		}
		if !start.Equal(expected) {
			t.Fatalf("query grid was not ceiled: %s", start)
		}
		return success(matrix(map[string]string{"job": "sut"}, []any{float64(start.UnixMilli()) / 1000, "1"})), nil
	})
	result, err := c.Collect(context.Background(), querySelection(), from, from.Add(time.Second), "nanosecond-run")
	if err != nil || len(result.Points) != 1 || !result.Points[0].TimestampUTC.Equal(expected) {
		t.Fatalf("standalone nanosecond grid: %+v %v", result, err)
	}
}

func TestQuotaIdentityCanonicalizesOrigins(t *testing.T) {
	a := Config{Endpoint: "https://PROM.example:443/"}
	b := Config{Endpoint: "https://prom.example"}
	if QuotaKey(a, Credentials{}) != QuotaKey(b, Credentials{}) {
		t.Fatal("equivalent origins bypass shared quota")
	}
	b.Endpoint = "https://prom.example:444"
	if QuotaKey(a, Credentials{}) == QuotaKey(b, Credentials{}) {
		t.Fatal("distinct origin quotas merged")
	}
	_ = QuotaKey(Config{Endpoint: "%invalid"}, Credentials{})
}

func TestTokenStateIsSharedOnlyAcrossSameOriginAndIdentity(t *testing.T) {
	cfg := Config{Endpoint: "https://workspace.eastus.prometheus.monitor.azure.com", BackendProfile: BackendAzureManaged, AuthType: AuthAzure, AzureCredentialMode: "service_principal", AzureTenantID: "token-state-tenant", ClientID: "token-state-client"}
	cred := Credentials{ClientSecret: "token-state-secret"}
	first := sharedTokenState(cfg, cred)
	second := sharedTokenState(cfg, cred)
	if first != second {
		t.Fatal("SDK recreation lost token cache")
	}
	different := cfg
	different.Endpoint = "https://other.eastus.prometheus.monitor.azure.com"
	if first == sharedTokenState(different, cred) {
		t.Fatal("token shared across origins")
	}
	different = cfg
	different.ClientID = "another-client"
	if first == sharedTokenState(different, cred) {
		t.Fatal("token shared across principals")
	}
	if first == sharedTokenState(cfg, Credentials{ClientSecret: "rotated-secret"}) {
		t.Fatal("token shared across secret versions")
	}
}

func TestBoundedSharedAzureTokenCacheAndExpiryAcrossClients(t *testing.T) {
	tokens.Lock()
	saved := tokens.entries
	tokens.entries = map[[32]byte]*tokenState{}
	tokens.Unlock()
	defer func() { tokens.Lock(); tokens.entries = saved; tokens.Unlock() }()
	cfg := Config{Endpoint: "https://workspace.eastus.prometheus.monitor.azure.com", AuthType: AuthAzure, BackendProfile: BackendAzureManaged, AzureCredentialMode: "managed_identity"}
	clients := []*Client{{cfg: cfg, azure: &fakeCredential{}, authState: sharedTokenState(cfg, Credentials{})}, {cfg: cfg, azure: &fakeCredential{}, authState: sharedTokenState(cfg, Credentials{})}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := clients[i%2].accessToken(context.Background()); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	calls := clients[0].azure.(*fakeCredential).calls.Load() + clients[1].azure.(*fakeCredential).calls.Load()
	if calls != 1 {
		t.Fatalf("client recreation caused%d concurrent renewals", calls)
	}
	clients[0].authState.mu.Lock()
	clients[0].authState.token.ExpiresOn = time.Now().Add(time.Second)
	clients[0].authState.mu.Unlock()
	if _, err := clients[1].accessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if clients[0].azure.(*fakeCredential).calls.Load()+clients[1].azure.(*fakeCredential).calls.Load() != 2 {
		t.Fatal("shared expiry did not renew")
	}
	for i := 0; i < 300; i++ {
		other := cfg
		other.ClientID = fmt.Sprintf("bounded-client-%d", i)
		sharedTokenState(other, Credentials{})
	}
	tokens.Lock()
	size := len(tokens.entries)
	tokens.Unlock()
	if size != 256 {
		t.Fatalf("token cache grew to%d", size)
	}
	overflow := cfg
	overflow.ClientID = "overflow-client"
	if sharedTokenState(overflow, Credentials{}) == sharedTokenState(overflow, Credentials{}) {
		t.Fatal("overflow cache must remain isolated")
	}
}

func TestAzureUnauthorizedInvalidatesOnlyTheRejectedToken(t *testing.T) {
	for _, newer := range []bool{false, true} {
		t.Run(fmt.Sprint(newer), func(t *testing.T) {
			var c *Client
			c = fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
				if newer {
					c.authState.mu.Lock()
					c.authState.token = azcore.AccessToken{Token: "newer-token", ExpiresOn: time.Now().Add(time.Hour)}
					c.authState.mu.Unlock()
				}
				return &http.Response{StatusCode: 401, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			c.cfg.AuthType = AuthAzure
			c.azure = &fakeCredential{}
			_, err := c.query(context.Background(), "/api/v1/query", nil)
			if err == nil {
				t.Fatal("401 accepted")
			}
			c.authState.mu.Lock()
			token := c.authState.token.Token
			c.authState.mu.Unlock()
			if newer && token != "newer-token" || !newer && token != "" {
				t.Fatalf("unexpected cached token after401: %q", token)
			}
		})
	}
}

func TestSelectionReadinessChecksCumulativeMainSeriesOnly(t *testing.T) {
	c := fixture(t, Config{MaxSeries: 1}, func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		stamp, _ := strconv.ParseFloat(r.Form.Get("time"), 64)
		if r.URL.Path == "/api/v1/query" {
			value := "1"
			if strings.HasPrefix(r.Form.Get("query"), "timestamp") {
				value = r.Form.Get("time")
			}
			return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{stamp, value}}}}), nil
		}
		return success(map[string]any{}), nil
	})
	s := Selection{ProjectID: "p", Queries: []Query{{ID: "one", Name: "one", Expression: "up", FreshnessExpression: "timestamp(up)"}}}
	if caps, err := c.TestSelection(context.Background(), s); err != nil || !caps.Metrics {
		t.Fatalf("freshness counted twice: %+v %v", caps, err)
	}
	s.Queries = append(s.Queries, Query{ID: "two", Name: "two", Expression: "up"})
	caps, err := c.TestSelection(context.Background(), s)
	if err == nil || caps.Metrics || caps.Status["metrics"] != "partial" {
		t.Fatalf("oversized source verified: %+v %v", caps, err)
	}
}

func TestAzureWarningRedactsTheRequestTokenAfterConcurrentRefresh(t *testing.T) {
	var c *Client
	c = fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		c.authState.mu.Lock()
		c.authState.token = azcore.AccessToken{Token: "new-token", ExpiresOn: time.Now().Add(time.Hour)}
		c.authState.mu.Unlock()
		return rawResponse(`{"status":"success","data":{"resultType":"vector","result":[]},"warnings":["upstream echoed azure-token"],"infos":["azure-token"]}`), nil
	})
	c.cfg.AuthType = AuthAzure
	c.azure = &fakeCredential{}
	response, err := c.query(context.Background(), "/api/v1/query", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range append(response.Warnings, response.Infos...) {
		if strings.Contains(value, "azure-token") {
			t.Fatal("inflight old token escaped redaction")
		}
	}
}

func TestAzureDisplayCasingChangesWithoutChangingCanonicalIdentity(t *testing.T) {
	from := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	upper := true
	c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		at, _ := strconv.ParseFloat(r.Form.Get("start"), 64)
		labels := map[string]string{"Job": "SUT"}
		if !upper {
			labels = map[string]string{"job": "sut"}
		}
		return success(matrix(labels, []any{at, "1"})), nil
	})
	c.cfg.BackendProfile = BackendAzureManaged
	c.cfg.AuthType = AuthAzure
	c.cfg.Endpoint = "https://workspace.eastus.prometheus.monitor.azure.com"
	c.cfg.AzureCredentialMode = "managed_identity"
	c.azure = &fakeCredential{}
	s := Selection{ProjectID: "p", StepSeconds: 30, SettleSeconds: 1, Queries: []Query{{ID: "up", Name: "up", Expression: "up", AllowedLabels: []string{"job"}}}}
	first, err := c.Collect(context.Background(), s, from, from.Add(time.Second), "run")
	if err != nil || len(first.Points) != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	upper = false
	second, err := c.Collect(context.Background(), s, from, from.Add(time.Second), "run")
	if err != nil || len(second.Points) != 1 {
		t.Fatalf("second: %+v %v", second, err)
	}
	a, b := first.Points[0], second.Points[0]
	if a.Dimensions["job"] != "sut" || b.Dimensions["job"] != "sut" || a.Provider.EntityIDs["series"] != b.Provider.EntityIDs["series"] || a.Provider.DisplayLabels["Job"] != "SUT" || b.Provider.DisplayLabels["job"] != "sut" {
		t.Fatalf("display versus identity mismatch: %+v %+v", a, b)
	}
}

func TestQuotedUTF8MetricAndUnsupportedOutputLabelIdentity(t *testing.T) {
	from := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s := querySelection()
	s.Queries[0].Expression = `{"処理時間_秒",job="sut"}`
	s.Queries[0].Semantics = "value"
	if err := s.Validate(); err != nil {
		t.Fatalf("Prometheus 3 quoted UTF-8 metric was rejected: %v", err)
	}
	c := fixture(t, Config{}, func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil || r.Form.Get("query") != s.Queries[0].Expression {
			t.Fatalf("UTF-8 expression changed in form encoding: %v", err)
		}
		return success(matrix(map[string]string{"__name__": "処理時間_秒", "job": "sut"}, []any{from.Unix(), "0.75"})), nil
	})
	result, err := c.Collect(context.Background(), s, from, from.Add(time.Second), "run")
	if err != nil || len(result.Points) != 1 || result.Points[0].Value == nil || *result.Points[0].Value != 0.75 || result.Points[0].Dimensions["job"] != "sut" {
		t.Fatalf("UTF-8 metric numeric normalization failed: %+v, %v", result, err)
	}
	c = fixture(t, Config{}, func(*http.Request) (*http.Response, error) {
		return success(matrix(map[string]string{"__name__": "処理時間_秒", "job": "sut", "地域": "east"}, []any{from.Unix(), "0.75"})), nil
	})
	result, err = c.Collect(context.Background(), s, from, from.Add(time.Second), "run")
	if err == nil || len(result.Points) != 0 || !strings.Contains(err.Error(), "label") {
		t.Fatalf("unsupported output identity silently merged: %+v, %v", result, err)
	}
}

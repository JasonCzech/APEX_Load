package elastic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureTransport struct {
	handler func(*http.Request, map[string]any) (int, any)
}

func (f fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := map[string]any{}
	if req.Body != nil {
		decoder := json.NewDecoder(req.Body)
		decoder.UseNumber()
		_ = decoder.Decode(&body)
	}
	status, value := f.handler(req, body)
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(encoded)), Request: req}, nil
}
func fixtureClient(t *testing.T, product string, handler func(*http.Request, map[string]any) (int, any)) *Client {
	t.Helper()
	original := handler
	handler = func(req *http.Request, body map[string]any) (int, any) {
		if strings.Contains(req.URL.Path, "/_resolve/index/") {
			return 200, map[string]any{"indices": []any{map[string]any{"name": "logs-a"}, map[string]any{"name": "logs-b"}, map[string]any{"name": "logs-1"}, map[string]any{"name": "logs-2"}, map[string]any{"name": "logs-2026"}}}
		}
		return original(req, body)
	}
	config := Config{Product: product, Endpoint: "https://example.com/search", AuthType: AuthBasic, RequestsPerMinute: 600, MaxRequestsPerPoll: 100, MaxEvidence: 10000, MaxPollSeconds: 60}
	c, err := NewClientWithHTTPClient(config, Credentials{Username: "operator", Password: "fixture-password"}, &http.Client{Transport: fixtureTransport{handler: handler}})
	if err != nil {
		t.Fatal(err)
	}
	// Unit fixtures need no real server delay. The shared limiter is exercised
	// independently below with real timing and concurrent client instances.
	c.rate = &rateState{inflight: make(chan struct{}, 2)}
	c.componentHTTP["kibana"] = c.http
	c.componentHTTP["logstash"] = c.http
	c.cfg.RequestsPerMinute = 1_000_000_000
	if err = c.UseVerifiedCapabilities(Capabilities{Product: product, Version: "9.1.0", DSL: true, PIT: true, ShardDoc: true, Metrics: true, Logs: true, Traces: true, ESQL: product == ProductElasticsearch, AsyncESQL: product == ProductElasticsearch}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
func testWindow() (time.Time, time.Time) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return from, from.Add(time.Minute)
}
func testSelection() Selection {
	return Selection{ProjectID: "project", Indices: []string{"logs-*"}, Logs: true}.Defaults()
}
func responseHit(index, id string, at time.Time, tie any) map[string]any {
	return map[string]any{"_index": index, "_id": id, "_source": map[string]any{"@timestamp": at.Format(time.RFC3339Nano), "message": "hello", "service.name": "checkout"}, "sort": []any{at.Format(time.RFC3339Nano), tie}}
}

func TestDefaultsAndSelectionBounds(t *testing.T) {
	s := testSelection()
	if s.PageSize != 100 || s.SettleSeconds != 60 || s.OverlapSeconds != 120 {
		t.Fatalf("defaults %+v", s)
	}
	if (Config{}).Defaults().MaxRequestsPerPoll != 20 {
		t.Fatal("request default")
	}
	for _, mutate := range []func(*Selection){func(s *Selection) { s.Indices = []string{"*"} }, func(s *Selection) { s.Indices = []string{"remote:logs-*"} }, func(s *Selection) { s.PageSize = 101 }, func(s *Selection) { s.MaxRetainedRecords = 10001 }, func(s *Selection) { s.MaxRetainedBytes = 8<<20 + 1 }, func(s *Selection) { s.ProjectedFields = make([]string, 17) }, func(s *Selection) {
		for i := 0; i < 6; i++ {
			s.Queries = append(s.Queries, Query{Name: fmt.Sprintf("q%d", i), Language: "dsl", Kind: "log"})
		}
	}} {
		copy := testSelection()
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatalf("unsafe selection accepted: %+v", copy)
		}
	}
	health := Selection{ProjectID: "project", KibanaHealth: true}.Defaults()
	if err := health.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSelectionFields(health, nil); err != nil {
		t.Fatal(err)
	}
}
func TestEndpointsAndIndependentTrust(t *testing.T) {
	for _, endpoint := range []string{"https://example.com", "https://example.com/elastic", "https://example.com/elastic/"} {
		if err := (Config{Endpoint: endpoint}).Validate(); err != nil {
			t.Fatalf("valid endpoint %s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/../search", "https://example.com/%2fsearch", "https://example.com/search?dest=other", "https://example.com//search"} {
		if (Config{Endpoint: endpoint}).Validate() == nil {
			t.Fatalf("unsafe endpoint %s", endpoint)
		}
	}
	private := netip.MustParseAddr("10.0.0.12")
	if allowedAddress(private, nil) {
		t.Fatal("private network implicitly approved")
	}
	if !allowedAddress(private, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}) {
		t.Fatal("approved private destination rejected")
	}
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "168.63.129.16", "::1", "fd00:ec2::254"} {
		if allowedAddress(netip.MustParseAddr(ip), []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}) {
			t.Fatalf("metadata or loopback admitted: %s", ip)
		}
	}
	c, err := NewClient(Config{Endpoint: "https://example.com", AuthType: AuthBasic, KibanaEndpoint: "https://kibana.example.com"}, Credentials{Username: "user", Password: "pass"})
	if err != nil {
		t.Fatalf("optional missing component auth blocked primary: %v", err)
	}
	defer c.Close()
	if c.componentHTTP["kibana"] != nil {
		t.Fatal("component inherited primary credential")
	}
}
func TestDSLRestrictions(t *testing.T) {
	for _, raw := range []string{`{"match_all":{}}`, `{"bool":{"filter":[{"term":{"service.name":"shop"}},{"range":{"status":{"gte":400,"lt":500}}}]}}`, `{"exists":{"field":"trace.id"}}`} {
		if err := ValidateDSL(json.RawMessage(raw)); err != nil {
			t.Fatalf("valid clause %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"query":{"match_all":{}}}`, `{"global":{}}`, `{"post_filter":{"term":{"service":"x"}}}`, `{"script":{"script":"true"}}`, `{"terms":{"id":{"index":"other","id":"all","path":"ids"}}}`, `{"term":{"field":{"value":"secret"}}}`, `{"match_all":{}} false`, `{"match_all":{}} broken`} {
		if ValidateDSL(json.RawMessage(raw)) == nil {
			t.Fatalf("scope escape accepted %s", raw)
		}
	}
}
func TestESQLGrammar(t *testing.T) {
	valid := Query{Name: "requests", Language: "esql", Kind: "metric", ESQL: "FROM logs-* | WHERE status >= ? | STATS result = COUNT(*) | LIMIT 10000", Params: []any{400}, ValueColumn: "result", ExactWindow: true, Statistic: "count", Unit: "count"}
	if err := ValidateESQL(valid, []string{"logs-*"}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"FROM other-* | STATS n=COUNT(*) | LIMIT 10", "FROM logs-* | LIMIT 10 | STATS n=COUNT(*)", "FROM logs-* | LOOKUP JOIN secrets ON id | LIMIT 10", "FROM logs-* | STATS n=COUNT(*) | LIMIT 10001", "FROM logs-* | STATS n=COUNT(*) | LIMIT 10; ROW secret=1", "FROM logs-* | FORK (STATS n=COUNT(*)) | LIMIT 10", "FROM logs-* | EVAL f=?? | STATS n=COUNT(*) | LIMIT 10"} {
		copy := valid
		copy.ESQL = query
		copy.Params = nil
		if ValidateESQL(copy, []string{"logs-*"}) == nil {
			t.Fatalf("unsafe pipeline accepted %s", query)
		}
	}
	s := testSelection()
	s.Logs = false
	s.Queries = []Query{valid}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Queries[0].Kind = "log"
	if s.Validate() == nil {
		t.Fatal("ESQL allowed exhaustive document claims")
	}
	valid.ESQL = "FROM logs-* | WHERE status>=?minimum OR retries>=?minimum | STATS result=COUNT(*) | LIMIT 10000"
	valid.Params = []any{map[string]any{"minimum": 400}}
	if err := ValidateESQL(valid, []string{"logs-*"}); err != nil {
		t.Fatal(err)
	}
	valid.Params = []any{map[string]any{"wrong": 400}}
	if ValidateESQL(valid, []string{"logs-*"}) == nil {
		t.Fatal("unbound named parameter accepted")
	}
}
func TestMandatorySearchScopeAndProjection(t *testing.T) {
	from, to := testWindow()
	s := testSelection()
	s.RunIDField = "apex.run_id"
	s.ServiceValues = []string{"checkout"}
	calls, closed := 0, false
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		if req.Header.Get("Authorization") == "" {
			t.Error("authentication missing")
		}
		switch req.Method + " " + req.URL.Path {
		case "POST /search/logs-*/_pit":
			return 200, map[string]any{"id": "owned"}
		case "DELETE /search/_pit":
			closed = true
			return 200, map[string]any{"succeeded": true}
		case "POST /search/_search":
			calls++
			if req.URL.Query().Get("allow_partial_search_results") != "false" {
				t.Error("partial search allowed")
			}
			if _, ok := body["_source"].([]any); !ok {
				t.Error("unrestricted projection")
			}
			filters := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
			text, _ := json.Marshal(filters)
			for _, required := range []string{"gte", "lt", from.Format(time.RFC3339), to.Format(time.RFC3339), "apex.run_id", "run-value", "checkout"} {
				if !strings.Contains(string(text), required) {
					t.Errorf("mandatory filter %s missing %s", required, text)
				}
			}
			return 200, map[string]any{"pit_id": "latest-owned", "hits": map[string]any{"hits": []any{}}}
		default:
			t.Errorf("arbitrary endpoint %s", req.URL.Path)
			return 404, nil
		}
	})
	r, err := c.Collect(context.Background(), s, from, to, "run-value")
	if err != nil || !r.Progress.Complete || calls != 1 || !closed {
		t.Fatalf("collect=%+v error=%v calls=%d cleanup=%v", r, err, calls, closed)
	}
}
func TestExhaustivePaginationAboveTenThousandAndPrivateContinuation(t *testing.T) {
	from, to := testWindow()
	opened, closed, requests := 0, 0, 0
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		switch req.Method + " " + req.URL.Path {
		case "POST /search/logs-*/_pit":
			opened++
			return 200, map[string]any{"id": "owned"}
		case "DELETE /search/_pit":
			closed++
			if body["id"] != "latest-owned" {
				t.Errorf("latest PIT not closed: %+v", body)
			}
			return 200, map[string]any{"succeeded": true}
		case "POST /search/_search":
			requests++
			start := 0
			if after, ok := body["search_after"].([]any); ok {
				n, _ := number(after[1])
				start = int(n) + 1
			}
			size, _ := number(body["size"])
			hits := []any{}
			for i := start; i < min(10001, start+int(size)); i++ {
				index := "logs-a"
				if i%2 == 0 {
					index = "logs-b"
				}
				hits = append(hits, responseHit(index, fmt.Sprintf("document-%d", i/2), from.Add(time.Second), i))
			}
			return 200, map[string]any{"pit_id": "latest-owned", "hits": map[string]any{"hits": hits}}
		default:
			return 404, nil
		}
	})
	s := testSelection()
	retained := map[string]bool{}
	polls := 0
	for {
		polls++
		r, err := c.Collect(context.Background(), s, from, to, "run")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Evidence) > c.cfg.MaxEvidence {
			t.Fatal("collection evidence cap exceeded")
		}
		for _, e := range r.Evidence {
			if retained[e.ID] {
				t.Fatalf("record collapsed or repeated %s", e.ID)
			}
			retained[e.ID] = true
		}
		if r.Progress.Complete {
			break
		}
		if r.Continuation == nil || r.Progress.Continuation == nil {
			t.Fatal("unaccepted suffix lacks continuation")
		}
		if len(r.Continuation.SearchAfter) != 2 {
			t.Fatal("sort vector lost")
		}
		s.Continuation = r.Continuation
		s.QueryOffset = r.Progress.NextQuery
		if polls > 5 {
			t.Fatal("pagination failed to terminate")
		}
	}
	if len(retained) != 10001 || opened != 1 || closed != 1 || requests < 101 {
		t.Fatalf("records=%d opened=%d closed=%d requests=%d", len(retained), opened, closed, requests)
	}
	public, _ := json.Marshal(s)
	if strings.Contains(string(public), "latest-owned") || strings.Contains(string(public), "search_after") {
		t.Fatal("private continuation serialized in source")
	}
}
func TestPITExpiryRestartsSameWindowAndDeduplicates(t *testing.T) {
	from, to := testWindow()
	opened, searches, closed := 0, 0, 0
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		switch req.Method + " " + req.URL.Path {
		case "POST /search/logs-*/_pit":
			opened++
			return 200, map[string]any{"id": fmt.Sprintf("pit-%d", opened)}
		case "DELETE /search/_pit":
			closed++
			return 200, map[string]any{"succeeded": true}
		case "POST /search/_search":
			searches++
			if searches == 2 {
				return 404, map[string]any{"error": "expired"}
			}
			hits := []any{}
			if body["search_after"] == nil {
				hits = append(hits, responseHit("logs-a", "one", from.Add(time.Second), 0))
			}
			return 200, map[string]any{"hits": map[string]any{"hits": hits}}
		default:
			return 404, nil
		}
	})
	s := testSelection()
	s.PageSize = 1
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !r.Progress.Complete || len(r.Evidence) != 1 || opened != 2 || closed != 2 {
		t.Fatalf("result %+v err=%v opened=%d closed=%d", r, err, opened, closed)
	}
}
func TestOpenSearchSeparateEndpointsAndStableTies(t *testing.T) {
	from, to := testWindow()
	closed := false
	c := fixtureClient(t, ProductOpenSearch, func(req *http.Request, body map[string]any) (int, any) {
		switch req.Method + " " + req.URL.Path {
		case "POST /search/logs-*/_search/point_in_time":
			if req.URL.Query().Get("allow_partial_pit_creation") != "false" {
				t.Error("partial PIT admitted")
			}
			return 200, map[string]any{"pit_id": "os-owned"}
		case "DELETE /search/_search/point_in_time":
			closed = true
			if _, ok := body["pit_id"].([]any); !ok {
				t.Error("OpenSearch PIT body incorrect")
			}
			return 200, map[string]any{"pits": []any{map[string]any{"successful": true}}}
		case "POST /search/_search":
			sort := body["sort"].([]any)
			if len(sort) != 3 || sort[1].(map[string]any)["_index"] != "asc" {
				t.Error("older unique tie sort missing")
			}
			return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
		default:
			t.Errorf("wrong product endpoint %s", req.URL.Path)
			return 404, nil
		}
	})
	c.capabilities.ShardDoc = false
	c.capabilities.Version = "2.19.0"
	s := testSelection()
	r, _ := c.Collect(context.Background(), s, from, to, "run")
	if r.Coverage[selectionTasks(s)[0].id].Status == "complete" {
		t.Fatal("unsafe older pagination readiness")
	}
	s.UniqueSortField = "sortable_id"
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !r.Progress.Complete || !closed {
		t.Fatalf("OpenSearch collection=%+v err=%v", r, err)
	}
}
func TestRecordProjectionScrubTimeAndTraceIdentity(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) { return 200, nil })
	s := testSelection()
	s.Traces = true
	s.ProjectedFields = []string{"user.email", "authorization", "big.integer"}
	s.RunIDField = "apex.run_id"
	row := map[string]any{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "event.ingested": from.Add(time.Minute).Format(time.RFC3339Nano), "apex.run_id": "run", "trace.id": "0123456789abcdef0123456789abcdef", "span.id": "0123456789abcdef", "parent.id": "", "event.duration": json.Number("2500000"), "span.name": "database", "service.name": "shop", "user.email": "operator@example.com", "authorization": "Bearer fixture-password", "big.integer": json.Number("9007199254740993")}
	task := task{id: "query", kind: "span"}
	e, err := c.projectRecord(s, task, row, ".ds-logs-apex-000001", "one", from, to, "run")
	if err != nil {
		t.Fatal(err)
	}
	if e.Span.DurationMS != 2.5 || e.Correlation != "run_id" || e.IngestedAt == nil || e.ReceivedAt.IsZero() {
		t.Fatalf("semantics %+v", e)
	}
	if e.Span.Attributes["authorization"].StringValue == row["authorization"] || e.Span.Attributes["user.email"].StringValue == row["user.email"] {
		t.Fatal("projected sensitive data retained")
	}
	if e.Span.Attributes["big.integer"].Kind != "integer" || e.Span.Attributes["big.integer"].StringValue != "9007199254740993" {
		t.Fatal("integer projection lost precision")
	}
	second, err := c.projectRecord(s, task, row, "logs-second", "one", from, to, "run")
	if err != nil || second.ID == e.ID {
		t.Fatal("same document ID across indices collapsed")
	}
	row["apex.run_id"] = "wrong"
	if _, err = c.projectRecord(s, task, row, "logs-a", "one", from, to, "run"); err == nil {
		t.Fatal("wrong run retained")
	}
	row["apex.run_id"] = "run"
	row["@timestamp"] = to.Format(time.RFC3339Nano)
	if _, err = c.projectRecord(s, task, row, "logs-a", "one", from, to, "run"); err == nil {
		t.Fatal("upper endpoint included")
	}
}
func TestScalarProjectionRejectsNestedAndUnsafeNumericMetrics(t *testing.T) {
	for _, value := range []any{map[string]any{"raw": "document"}, []any{"nested"}, json.Number("18446744073709551615"), math.NaN()} {
		if _, err := scalar(value); err == nil {
			t.Fatalf("unbounded projection %v", value)
		}
	}
	if _, err := mappedNumber(json.Number("9007199254740993")); err == nil {
		t.Fatal("unsafe integer rounded silently")
	}
	if value, err := mappedNumber(nil); err != nil || value != nil {
		t.Fatal("missing metric invented zero")
	}
}

func TestWeightedMetricBucketsPreserveLeadingBoundaries(t *testing.T) {
	from, to := testWindow()
	from = from.Add(500 * time.Millisecond)
	closed := false
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		switch req.Method + " " + req.URL.Path {
		case "POST /search/logs-*/_pit":
			return 200, map[string]any{"id": "pit"}
		case "DELETE /search/_pit":
			closed = true
			return 200, map[string]any{"succeeded": true}
		case "POST /search/_search":
			return 200, map[string]any{"aggregations": map[string]any{"series": map[string]any{"buckets": []any{map[string]any{"key": map[string]any{"timestamp": from.Truncate(10 * time.Second).UnixMilli()}, "doc_count": 20, "sum": map[string]any{"value": 6000}, "count": map[string]any{"value": 3}}}}}}
		default:
			return 404, nil
		}
	})
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "latency", Statistic: "average", SumField: "latency.sum", CountField: "latency.count", Unit: "us", BucketSeconds: 10}}}.Defaults()
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(r.Points) != 1 || !closed {
		t.Fatalf("result %+v error %v", r, err)
	}
	p := r.Points[0]
	if *p.Value != 2 || *p.Provider.Count != 3 || *p.Provider.Sum != 6 || p.Unit != "ms" || p.Provider.AggregationEligible || !p.TimestampUTC.Equal(from.Truncate(10*time.Second)) {
		t.Fatalf("bucket fabricated or weights lost %+v", p)
	}
}

func TestExactCountEstablishesZeroAndPercentilesAreApproximate(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		if req.Method != "POST" || req.URL.Path != "/search/logs-*/_search" {
			t.Error("exact query used arbitrary endpoint")
		}
		return 200, map[string]any{"hits": map[string]any{"total": map[string]any{"value": 0, "relation": "eq"}}, "aggregations": map[string]any{"percentile": map[string]any{"values": map[string]any{"95.0": 1200}}, "count": map[string]any{"value": 2}, "sum": map[string]any{"value": 1800}, "min": map[string]any{"value": 600}, "max": map[string]any{"value": 1200}}}
	})
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "requests", Statistic: "count", ExactWindow: true}, {Name: "latency", Field: "duration", Statistic: "p95", Unit: "us", ExactWindow: true}}}.Defaults()
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(r.Points) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if *r.Points[0].Value != 0 || r.Points[0].Provider.WindowKind != "query_window" {
		t.Fatal("complete zero lost")
	}
	p := r.Points[1]
	if !p.Provider.Approximate || p.Provider.AggregationEligible || *p.Value != 1.2 {
		t.Fatalf("approximate percentile semantics %+v", p)
	}
}

func TestESQLMappedWindowAndAsyncOwnership(t *testing.T) {
	from, to := testWindow()
	polls, deleted := 0, false
	query := `FROM logs-* | WHERE service.name == ? | STATS total = SUM(duration), observations = COUNT(*), latency = AVG(duration) | LIMIT 100`
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		switch req.Method + " " + req.URL.Path {
		case "POST /search/_query/async":
			if body["query"] != query || body["filter"] == nil {
				t.Error("pipeline rewritten or filter omitted")
			}
			if body["params"].([]any)[0] != "shop\" | LIMIT 1" {
				t.Error("parameter concatenated")
			}
			return 200, map[string]any{"id": "owned-async", "is_running": true}
		case "GET /search/_query/async/owned-async":
			polls++
			return 200, map[string]any{"is_running": false, "is_partial": false, "columns": []any{map[string]any{"name": "total", "type": "double"}, map[string]any{"name": "observations", "type": "long"}, map[string]any{"name": "latency", "type": "double"}}, "values": []any{[]any{6000, 3, 999}}}
		case "DELETE /search/_query/async/owned-async":
			deleted = true
			return 200, map[string]any{"acknowledged": true}
		default:
			t.Errorf("arbitrary endpoint %s", req.URL.Path)
			return 404, nil
		}
	})
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Queries: []Query{{Name: "latency", Language: "esql", Kind: "metric", ESQL: query, Params: []any{"shop\" | LIMIT 1"}, ValueColumn: "latency", SumColumn: "total", CountColumn: "observations", Statistic: "average", Unit: "us", ExactWindow: true, Async: true}}}.Defaults()
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || r.Continuation == nil || r.Continuation.AsyncID != "owned-async" || deleted {
		t.Fatalf("initial %+v err=%v", r, err)
	}
	s.Continuation = r.Continuation
	r, err = c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(r.Points) != 1 || polls != 1 || !deleted || *r.Points[0].Value != 2 || !r.Points[0].Provider.QueryWindow {
		t.Fatalf("async final %+v err=%v polls=%d deleted=%v", r, err, polls, deleted)
	}
}
func TestESQLRowLimitIsTruncatedAndOpenSearchRejectsESQL(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
		return 200, map[string]any{"columns": []any{map[string]any{"name": "value", "type": "double"}, map[string]any{"name": "service", "type": "keyword"}}, "values": []any{[]any{42, "shop"}}}
	})
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Queries: []Query{{Name: "value", Language: "esql", Kind: "metric", ESQL: "FROM logs-* | STATS value = COUNT(*) BY service | LIMIT 1", ValueColumn: "value", DimensionColumns: []string{"service"}, ExactWindow: true, Statistic: "count", Unit: "count"}}}.Defaults()
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(r.Points) != 1 || !r.Progress.Complete {
		t.Fatalf("%+v %v", r, err)
	}
	for _, coverage := range r.Coverage {
		if !coverage.Truncated || coverage.Status != "truncated" {
			t.Fatal("row cap claimed exhaustive coverage")
		}
	}
	other := fixtureClient(t, ProductOpenSearch, func(*http.Request, map[string]any) (int, any) {
		t.Error("OpenSearch attempted ESQL request")
		return 500, nil
	})
	r, _ = other.Collect(context.Background(), s, from, to, "run")
	for _, coverage := range r.Coverage {
		if coverage.Status == "complete" {
			t.Fatal("OpenSearch silently translated ES|QL")
		}
	}
}
func TestPreviewClosesTruncatedCursorAndDisabledSignalsRemainIncomplete(t *testing.T) {
	from, to := testWindow()
	closed := false
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		switch req.Method {
		case "DELETE":
			closed = true
			return 200, map[string]any{"succeeded": true}
		case "POST":
			if strings.HasSuffix(req.URL.Path, "/_pit") {
				return 200, map[string]any{"id": "preview-owned"}
			}
			hits := []any{}
			for i := 0; i < 100; i++ {
				hits = append(hits, responseHit("logs-a", fmt.Sprint(i), from.Add(time.Second), i))
			}
			return 200, map[string]any{"hits": map[string]any{"hits": hits}}
		}
		return 404, nil
	})
	s := testSelection()
	r, err := c.Preview(context.Background(), s, from, to, "run")
	if err != nil || len(r.Evidence) != 100 || !closed || r.Continuation != nil || r.Progress.Continuation != nil || r.Progress.Complete {
		t.Fatalf("preview %+v err=%v closed=%v", r, err, closed)
	}
	s.DisabledQueries = map[string]bool{selectionTasks(s)[0].id: true}
	r, err = c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !r.Progress.Complete || len(r.Evidence) != 0 {
		t.Fatalf("disabled %+v %v", r, err)
	}
	for _, coverage := range r.Coverage {
		if !coverage.Truncated {
			t.Fatal("disabled query claimed complete")
		}
	}
}
func TestHealthIsInstantaneousAndComponentAuthIndependent(t *testing.T) {
	from, to := testWindow()
	var auth []string
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		auth = append(auth, req.Header.Get("Authorization"))
		switch req.URL.Path {
		case "/kibana/api/status":
			return 200, map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}}
		case "/logstash/_node/stats/jvm,process,events,pipelines":
			return 200, map[string]any{"jvm": map[string]any{"mem": map[string]any{"heap_used_percent": 25}}, "events": map[string]any{"in": 100, "out": 80}, "pipelines": map[string]any{"orders": map[string]any{"queue": map[string]any{"events_count": 20}}}}
		default:
			t.Error("component destination wrong")
			return 500, nil
		}
	})
	c.cfg.KibanaEndpoint = "https://kibana.example.com/kibana"
	c.cfg.LogstashEndpoint = "https://logstash.example.com/logstash"
	c.credentials.Kibana = ComponentCredentials{AuthType: AuthBearer, BearerToken: "kibana-token"}
	c.credentials.Logstash = ComponentCredentials{AuthType: AuthBasic, Username: "logstash", Password: "separate"}
	s := Selection{ProjectID: "project", KibanaHealth: true, LogstashHealth: true}.Defaults()
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(r.Points) < 4 {
		t.Fatalf("health %+v %v", r, err)
	}
	for _, p := range r.Points {
		if p.Provider.WindowKind != "instant" || p.Provider.ResolutionSeconds != 0 || p.Provider.AggregationEligible || !p.Provider.BucketStart.Equal(p.Provider.BucketEnd) {
			t.Fatal("instant snapshot fabricated a bucket")
		}
	}
	if !strings.HasPrefix(auth[0], "Bearer ") || !strings.HasPrefix(auth[1], "Basic ") {
		t.Fatalf("component credentials inherited %v", auth)
	}
	s.HealthCoverage = r.Coverage
	s.LastHealthAt = time.Now().UTC()
	r, err = c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(r.Points) != 0 || len(auth) != 2 || len(r.Coverage) != 2 || !r.Progress.Complete {
		t.Fatalf("health cadence lost coverage: %+v err=%v requests=%d", r, err, len(auth))
	}
	for id, coverage := range r.Coverage {
		if coverage.Status != "complete" || coverage.Points != 0 || coverage.Evidence != 0 || coverage.AsOf == nil || !coverage.AsOf.Equal(*s.HealthCoverage[id].AsOf) {
			t.Fatalf("cadence coverage changed observation: %+v", coverage)
		}
	}
}
func TestFieldCapabilitiesRejectConflictsMissingAndNonnumeric(t *testing.T) {
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "latency", Field: "duration", Statistic: "average", Unit: "ms"}}}.Defaults()
	fields := []Field{{Name: "@timestamp", Types: []string{"date"}, Searchable: true}, {Name: "duration", Types: []string{"double"}, Searchable: true, Aggregatable: true}}
	if err := ValidateSelectionFields(s, fields); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Field{{Name: "duration", Types: []string{"double", "keyword"}, Searchable: true, Aggregatable: true, Conflict: true}, {Name: "duration", Types: []string{"keyword"}, Searchable: true, Aggregatable: true}, {Name: "duration", Types: []string{"double"}, Searchable: true, Aggregatable: false}} {
		if ValidateSelectionFields(s, []Field{fields[0], bad}) == nil {
			t.Fatalf("bad field mapping %+v", bad)
		}
	}
}
func TestSharedLimiterConcurrencyAndCancellation(t *testing.T) {
	config := Config{Endpoint: "https://rate-fixture.example.com", AuthType: AuthBasic, RequestsPerMinute: 600}.Defaults()
	creds := Credentials{Username: "user", Password: "same"}
	a, b := newClient(config, creds, &http.Client{}), newClient(config, creds, &http.Client{})
	if a.rate != b.rate || cap(a.rate.inflight) != 2 {
		t.Fatal("per-client limiter evaded connection budget")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := a.wait(ctx); err == nil {
		release()
		t.Fatal("cancelled work acquired request slot")
	}
	var wg sync.WaitGroup
	times := make(chan time.Time, 2)
	for _, client := range []*Client{a, b} {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			release, err := c.wait(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			times <- time.Now()
			release()
		}(client)
	}
	wg.Wait()
	close(times)
	var values []time.Time
	for at := range times {
		values = append(values, at)
	}
	delta := values[1].Sub(values[0])
	if delta < 80*time.Millisecond {
		t.Fatalf("shared request budget not enforced %v", delta)
	}
}
func TestHTTPResponseBoundsAndSanitizedErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		response *http.Response
	}{{"vendor", &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"password":"vendor-secret"}`))}}, {"large", &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 1025)))}}, {"multiple", &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{} {}`))}}, {"type", &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(`<html>private</html>`))}}} {
		t.Run(test.name, func(t *testing.T) {
			c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) { return 200, nil })
			c.cfg.MaxResponseBytes = 1024
			c.http = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return test.response, nil })}
			var out any
			err := c.request(context.Background(), "GET", "/", nil, &out)
			if err == nil || strings.Contains(err.Error(), "vendor-secret") || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe response %v", err)
			}
		})
	}
	if !errors.Is(ErrBudget, ErrBudget) {
		t.Fatal("budget sentinel lost")
	}
}

func TestCapabilitiesAreProbedAndOptionalMonitorPermissionsAreIndependent(t *testing.T) {
	for _, product := range []string{ProductElasticsearch, ProductOpenSearch} {
		t.Run(product, func(t *testing.T) {
			closed := 0
			c := fixtureClient(t, product, func(req *http.Request, body map[string]any) (int, any) {
				switch req.Method + " " + req.URL.Path {
				case "GET /search/":
					version := map[string]any{"number": "8.11.0", "build_flavor": "serverless"}
					if product == ProductOpenSearch {
						version = map[string]any{"number": "2.19.0", "distribution": "opensearch"}
					}
					return 200, map[string]any{"version": version}
				case "POST /search/logs-*/_search":
					return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
				case "POST /search/_query":
					return 200, map[string]any{"columns": []any{map[string]any{"name": "probe", "type": "integer"}}, "values": []any{[]any{1}}}
				case "POST /search/_query/async":
					return 200, map[string]any{"id": "verify-async", "is_running": true}
				case "DELETE /search/_query/async/verify-async":
					return 200, map[string]any{"acknowledged": true}
				case "POST /search/logs-*/_pit":
					return 200, map[string]any{"id": "probe-pit"}
				case "POST /search/logs-*/_search/point_in_time":
					return 200, map[string]any{"pit_id": "probe-pit"}
				case "POST /search/_search":
					return 400, map[string]any{"error": "_shard_doc unavailable"}
				case "DELETE /search/_pit":
					closed++
					return 200, map[string]any{"succeeded": true}
				case "DELETE /search/_search/point_in_time":
					closed++
					return 200, map[string]any{"pits": []any{map[string]any{"successful": true}}}
				case "POST /search/logs-*/_field_caps":
					return 200, map[string]any{"fields": map[string]any{"@timestamp": map[string]any{"date": map[string]any{"searchable": true, "aggregatable": true}}}}
				case "GET /search/_cluster/health":
					return 403, map[string]any{"error": "private vendor security details"}
				default:
					t.Errorf("unsupported verification request %s", req.URL.Path)
					return 404, nil
				}
			})
			cap, err := c.Verify(context.Background(), []string{"logs-*"})
			if err != nil {
				t.Fatal(err)
			}
			if !cap.DSL || !cap.PIT || cap.Health || closed != 1 {
				t.Fatalf("capability result %+v cleanup=%d", cap, closed)
			}
			if product == ProductElasticsearch {
				if !cap.Serverless || !cap.ESQL || !cap.AsyncESQL {
					t.Fatal("serverless compatibility version incorrectly gated features")
				}
			} else {
				if cap.ESQL || cap.AsyncESQL || cap.ShardDoc {
					t.Fatal("OpenSearch capabilities inferred instead of probed")
				}
			}
		})
	}
}

func TestTerminalMetricWindowsPreserveQueryIdentityAndBounds(t *testing.T) {
	from, to := testWindow()
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "latency", Field: "duration", Statistic: "p95", Unit: "ms", BucketSeconds: 10}}}.Defaults()
	base := QueryMetricDefinitions(s)[0].QueryID
	s.TerminalWindows = []TerminalWindow{{Metric: "latency", FromUTC: from.Add(10 * time.Second), UntilUTC: to.Add(-10 * time.Second)}}
	tasks := selectionTasks(s)
	if len(tasks) != 2 || tasks[1].id != base || tasks[1].cursorID == base || !tasks[1].metric.ExactWindow {
		t.Fatalf("terminal tasks %+v", tasks)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		t.Fatal("disabled base and terminal queries should not execute")
		return 500, nil
	})
	s.DisabledQueries = map[string]bool{base: true}
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !r.Progress.Complete || len(r.Coverage) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestIntegerExponentAndFractionalTimestampBounds(t *testing.T) {
	for _, value := range []json.Number{"9007199254740993", "9007199254740993.0", "9.007199254740993e15", "1e1000000000", "1e-1000000000"} {
		if _, ok := number(value); ok {
			t.Fatalf("unsafe number accepted %s", value)
		}
	}
	if _, ok := timestamp(json.Number("1234.5")); ok {
		t.Fatal("fractional epoch timestamp silently truncated")
	}
	if value, ok := number(json.Number("9007199254740992")); !ok || value != 9007199254740992 {
		t.Fatal("safe IEEE-754 boundary rejected")
	}
	var aggregate aggregateNumber
	if json.Unmarshal([]byte(`{"value":9007199254740993}`), &aggregate) == nil {
		t.Fatal("aggregate decoder rounded an unsafe integer before validation")
	}
}

func TestObservedTraceCorrelationIsPrivateAndRunIdentityWins(t *testing.T) {
	from, to := testWindow()
	s := testSelection()
	base := QuerySignalDefinitions(s)[0].QueryID
	s.ObservedTraceIDs = []string{"0123456789abcdef0123456789abcdef"}
	s.CorrelationLimitReached = true
	if QuerySignalDefinitions(s)[0].QueryID != base {
		t.Fatal("execution trace set changed source query identity")
	}
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) { return 200, nil })
	row := map[string]any{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "message": "transaction failed", "service.name": "checkout", "trace.id": s.ObservedTraceIDs[0]}
	e, err := c.projectRecord(s, task{id: base, kind: "error"}, row, "logs-a", "document", from, to, "run")
	if err != nil || e.Correlation != "trace_id" || e.EntityIDs["trace_id"] != s.ObservedTraceIDs[0] {
		t.Fatalf("evidence %+v err=%v", e, err)
	}
	s.RunIDField = "apex.run_id"
	row["apex.run_id"] = "run"
	e, err = c.projectRecord(s, task{id: base, kind: "error"}, row, "logs-a", "document", from, to, "run")
	if err != nil || e.Correlation != "run_id" {
		t.Fatal("run-ID correlation did not win")
	}
	data, _ := json.Marshal(s)
	if strings.Contains(string(data), s.ObservedTraceIDs[0]) || strings.Contains(string(data), "correlation_limit_reached") {
		t.Fatal("runtime trace set disclosed through source configuration")
	}
}

func TestProviderSeriesCeilingAndIndependentQueryFailures(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		switch req.URL.Path {
		case "/search/logs-*/_pit":
			return 403, map[string]any{"error": "private"}
		case "/kibana/api/status":
			return 200, map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}}
		default:
			return 500, nil
		}
	})
	c.cfg.KibanaEndpoint = "https://example.com/kibana"
	c.credentials.Kibana = ComponentCredentials{AuthType: AuthBasic, Username: "k", Password: "pass"}
	s := testSelection()
	s.KibanaHealth = true
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !r.Progress.Complete || len(r.Points) != 1 {
		t.Fatalf("optional query failure blocked independent component %+v %v", r, err)
	}
	if r.Coverage[QuerySignalDefinitions(s)[0].QueryID].Status != "forbidden" {
		t.Fatal("permission failure lost")
	}
	c.cfg.MaxSeries = 1
	result := Result{}
	v := float64(1)
	point := r.Points[0]
	point.Value = &v
	if err := c.appendPoint(&result, point); err != nil {
		t.Fatal(err)
	}
	point.MetricName = "another.series"
	if !errors.Is(c.appendPoint(&result, point), ErrTruncated) {
		t.Fatal("source series budget not enforced")
	}
}

func TestElasticAPMErrorPresetProjectsExceptionMessage(t *testing.T) {
	from, to := testWindow()
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Preset: "elastic_apm", Errors: true}.Defaults()
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) { return 200, nil })
	row := map[string]any{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "processor.event": "error", "error": map[string]any{"exception": []any{map[string]any{"message": "password=must-be-redacted", "stacktrace": []any{"never retained"}}}}}
	e, err := c.projectRecord(s, task{id: "error", kind: "error"}, row, "logs-a", "document", from, to, "run")
	if err != nil || e.Log == nil || e.Summary == "" || strings.Contains(e.Summary, "must-be-redacted") {
		t.Fatalf("APM error projection %+v error=%v", e, err)
	}
	data, _ := json.Marshal(e)
	if strings.Contains(string(data), "stacktrace") || strings.Contains(string(data), "never retained") {
		t.Fatal("raw exception document retained")
	}
}

func TestCountAndCardinalitySupportKeywordFieldsWithoutNumericAggregations(t *testing.T) {
	for _, stat := range []string{"count", "cardinality"} {
		m := MetricSelection{Name: "services", Field: "service.name", Statistic: stat}
		aggs := metricAggs(m)
		if len(aggs) != 1 || aggs["sum"] != nil {
			t.Fatalf("keyword metric used numeric aggregations %+v", aggs)
		}
		s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{m}}.Defaults()
		fields := []Field{{Name: "@timestamp", Types: []string{"date"}, Searchable: true}, {Name: "service.name", Types: []string{"keyword"}, Searchable: true, Aggregatable: true}}
		if err := ValidateSelectionFields(s, fields); err != nil {
			t.Fatal(err)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDisabledQueryClosesCursorAndEmptyCoverage(t *testing.T) {
	closed := false
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		switch {
		case req.Method == "DELETE" && strings.HasSuffix(req.URL.Path, "/_pit"):
			closed = body["id"] == "owned-private-cursor"
			return 200, map[string]any{"succeeded": true}
		case strings.HasSuffix(req.URL.Path, "/_pit"):
			return 200, map[string]any{"id": "next-pit"}
		default:
			return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
		}
	})
	from, to := testWindow()
	s := testSelection()
	id := selectionTasks(s)[0].id
	s.DisabledQueries = map[string]bool{id: true}
	s.Continuation = &Continuation{QueryID: id, WindowFrom: from, WindowUntil: to, PITID: "owned-private-cursor"}
	r, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !closed || r.Continuation != nil || !r.Progress.Complete || r.Coverage[id].Status != "truncated" {
		t.Fatalf("disabled query cleanup closed=%t result=%+v err=%v", closed, r.Progress, err)
	}
	s.DisabledQueries, s.Continuation = nil, nil
	r, err = c.Collect(context.Background(), s, from, to, "run")
	if err != nil || r.Coverage[id].Status != "empty" {
		t.Fatalf("empty collection result=%+v err=%v", r.Coverage, err)
	}
}

func TestIngestionCursorKeepsEventScopeAndQueryIdentity(t *testing.T) {
	from, to := testWindow()
	s := testSelection()
	s.IngestedField = "event.ingested"
	baseID := selectionTasks(s)[0].id
	s.IngestionFrom = from.Add(-time.Minute)
	if err := s.Validate(); err != nil || selectionTasks(s)[0].id != baseID {
		t.Fatalf("transient ingestion cursor altered approved identity: %v", err)
	}
	body, _ := json.Marshal(searchFilter(s, selectionTasks(s)[0], from, to, "run"))
	if !strings.Contains(string(body), "event.ingested") || !strings.Contains(string(body), "must_not") || !strings.Contains(string(body), from.Format(time.RFC3339Nano)) || !strings.Contains(string(body), to.Format(time.RFC3339Nano)) {
		t.Fatalf("ingestion/event scopes missing: %s", body)
	}
	s.IngestionFrom = s.IngestionFrom.In(time.FixedZone("offset", 60))
	if s.Validate() == nil {
		t.Fatal("non-UTC private cursor accepted")
	}
}

func TestPartialShardAndProviderProseScrubbing(t *testing.T) {
	for _, shards := range []responseShards{{Total: 2, Successful: 1}, {Total: 1, Successful: 1, Failed: 1}} {
		if checkSearch(searchResponse{Shards: shards}) == nil {
			t.Fatalf("partial shards accepted: %+v", shards)
		}
	}
	if checkSearch(searchResponse{Shards: responseShards{Total: 2, Successful: 2, Skipped: 1}}) != nil {
		t.Fatal("successful/skipped shard count rejected")
	}
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) { return 200, nil })
	for _, value := range []string{"Authorization: Bearer short-private-token", "Authorization=Basic short-private-token", "operator@example.com"} {
		if strings.Contains(c.redact(value), "short-private-token") || strings.Contains(c.redactIdentity(value), "short-private-token") || strings.Contains(c.redact(value), "operator@example.com") {
			t.Fatalf("unscrubbed provider prose %q", value)
		}
	}
}

func TestDashboardsURLIsValidatedAndNeverRequested(t *testing.T) {
	for _, navigation := range []string{"https://dashboards.example.com", "https://dashboards.example.com/observability/app/discover"} {
		if err := (Config{Endpoint: "https://search.example.com", AuthType: AuthBasic, DashboardsURL: navigation}).Validate(); err != nil {
			t.Fatalf("valid approved navigation rejected: %v", err)
		}
	}
	for _, navigation := range []string{"http://dashboards.example.com", "https://user:password@dashboards.example.com", "https://dashboards.example.com/app?query=value", "https://dashboards.example.com/app#fragment", "https://dashboards.example.com/path/../admin", "https://dashboards.example.com/%2e%2e/admin", "javascript:alert(1)"} {
		if (Config{Endpoint: "https://search.example.com", AuthType: AuthBasic, DashboardsURL: navigation}).Validate() == nil {
			t.Fatalf("unsafe dashboard navigation accepted %q", navigation)
		}
	}
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		if req.URL.Host == "dashboards.example.com" {
			t.Fatal("read provider requested a browser navigation destination")
		}
		if strings.HasSuffix(req.URL.Path, "/_pit") {
			if req.Method == "DELETE" {
				return 200, map[string]any{"succeeded": true}
			}
			return 200, map[string]any{"id": "private-pit"}
		}
		return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
	})
	c.cfg.DashboardsURL = "https://dashboards.example.com/observability"
	from, to := testWindow()
	if _, err := c.Collect(context.Background(), testSelection(), from, to, "run"); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalComponentTrustFailureDoesNotBlockPrimary(t *testing.T) {
	client, err := NewClient(Config{Endpoint: "https://example.com", AuthType: AuthBasic, KibanaEndpoint: "https://kibana.example.com", KibanaCAPEM: "invalid certificate"}, Credentials{Username: "primary", Password: "fixture", Kibana: ComponentCredentials{AuthType: AuthBasic, Username: "component", Password: "fixture"}})
	if err != nil {
		t.Fatalf("optional component trust blocked primary connection: %v", err)
	}
	defer client.Close()
	if client.componentHTTP["kibana"] != nil {
		t.Fatal("invalid optional trust installed a transport")
	}
}

func TestMultiIndexAPMOptionalMappingsAndRequiredScope(t *testing.T) {
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		return 200, map[string]any{"fields": map[string]any{
			"@timestamp":              map[string]any{"date": map[string]any{"searchable": true, "aggregatable": true}},
			"processor.event":         map[string]any{"keyword": map[string]any{"searchable": true, "aggregatable": true}},
			"labels.run_id":           map[string]any{"keyword": map[string]any{"searchable": true, "aggregatable": true}},
			"service.name":            map[string]any{"keyword": map[string]any{"searchable": true, "aggregatable": true}},
			"span.id":                 map[string]any{"keyword": map[string]any{"searchable": true, "aggregatable": true}, "unmapped": map[string]any{}},
			"trace.id":                map[string]any{"keyword": map[string]any{"searchable": true, "aggregatable": true}, "unmapped": map[string]any{}},
			"transaction.id":          map[string]any{"keyword": map[string]any{"searchable": true, "aggregatable": true}, "unmapped": map[string]any{}},
			"span.duration.us":        map[string]any{"long": map[string]any{"searchable": true, "aggregatable": true}, "unmapped": map[string]any{}},
			"transaction.duration.us": map[string]any{"long": map[string]any{"searchable": true, "aggregatable": true}, "unmapped": map[string]any{}},
		}}
	})
	s := Selection{ProjectID: "project", Indices: []string{"traces-apm-span-*", "traces-apm-transaction-*"}, Preset: "elastic_apm", Traces: true, Transactions: true, RunIDField: "labels.run_id", ServiceValues: []string{"checkout"}, ProjectedFields: []string{"span.duration.us"}}.Defaults()
	fields, err := c.DiscoverFields(context.Background(), s.Indices)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSelectionFields(s, fields); err != nil {
		t.Fatalf("optional APM fields mapped in separate index families rejected: %v", err)
	}
	for _, name := range []string{"@timestamp", "labels.run_id", "service.name"} {
		copy := append([]Field{}, fields...)
		for i := range copy {
			if copy[i].Name == name {
				copy[i].PartiallyMapped = true
			}
		}
		if ValidateSelectionFields(s, copy) == nil {
			t.Fatalf("partially mapped mandatory scope accepted: %s", name)
		}
	}
	copy := append([]Field{}, fields...)
	for i := range copy {
		if copy[i].Name == "span.duration.us" {
			copy[i].Types = []string{"long", "keyword"}
			copy[i].Conflict = true
		}
	}
	if ValidateSelectionFields(s, copy) == nil {
		t.Fatal("conflicting optional duration types accepted")
	}
}

func TestEmptyNumericSumRemainsMissing(t *testing.T) {
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		return 200, map[string]any{"hits": map[string]any{"total": map[string]any{"value": 0, "relation": "eq"}}, "aggregations": map[string]any{"sum": map[string]any{"value": 0}, "count": map[string]any{"value": 0}}}
	})
	s := Selection{ProjectID: "project", Indices: []string{"metrics-*"}, Metrics: []MetricSelection{{Name: "sum", Field: "amount", Statistic: "sum", Unit: "count", ExactWindow: true}}}.Defaults()
	from, to := testWindow()
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(result.Points) != 1 || result.Points[0].Value != nil || result.Points[0].Quality != "missing" {
		t.Fatalf("empty sum fabricated a numeric zero: %+v %v", result.Points, err)
	}
}

func TestResolvedAliasAndDataStreamScopeSurvivesContinuation(t *testing.T) {
	from, to := testWindow()
	resolved := 0
	config := Config{Endpoint: "https://example.com", AuthType: AuthBasic, MaxEvidence: 1, MaxRequestsPerPoll: 20}.Defaults()
	c, err := NewClientWithHTTPClient(config, Credentials{Username: "fixture", Password: "fixture"}, &http.Client{Transport: fixtureTransport{handler: func(req *http.Request, body map[string]any) (int, any) {
		switch {
		case strings.Contains(req.URL.Path, "/_resolve/index/"):
			resolved++
			return 200, map[string]any{"aliases": []any{map[string]any{"name": "approved-alias", "indices": []string{"concrete-2026"}}}, "data_streams": []any{map[string]any{"name": "logs-stream", "backing_indices": []string{".ds-logs-stream-2026-000001"}}}, "indices": []any{map[string]any{"name": "unrequested-resource"}}}
		case strings.HasSuffix(req.URL.Path, "/_pit") && req.Method == "POST":
			if req.URL.Path != "/approved-alias,logs-stream/_pit" {
				t.Fatalf("alias filter target replaced by raw concrete indices: %s", req.URL.Path)
			}
			return 200, map[string]any{"id": "owned"}
		case strings.HasSuffix(req.URL.Path, "/_pit") && req.Method == "DELETE":
			return 200, map[string]any{"succeeded": true}
		default:
			if _, ok := body["search_after"]; ok {
				return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
			}
			return 200, map[string]any{"hits": map[string]any{"hits": []any{responseHit("concrete-2026", "document", from.Add(time.Second), 1)}}}
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.cfg.RequestsPerMinute = 1_000_000_000
	if err := c.UseVerifiedCapabilities(Capabilities{Product: ProductElasticsearch, Version: "9.1.0", PIT: true, ShardDoc: true}); err != nil {
		t.Fatal(err)
	}
	s := Selection{ProjectID: "project", Indices: []string{"approved-alias", "logs-stream"}, Logs: true}.Defaults()
	first, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(first.Evidence) != 1 || first.Continuation == nil || len(first.ResolvedIndices) != 2 || len(first.Continuation.ResolvedIndices) != 2 || first.ResolvedIndices[0] != ".ds-logs-stream-2026-000001" {
		t.Fatalf("resolved capture %+v %v", first, err)
	}
	s.Continuation = first.Continuation
	s.QueryOffset = first.Progress.NextQuery
	second, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !second.Progress.Complete || resolved != 1 || !cursorEqual(first.ResolvedIndices, second.ResolvedIndices) {
		t.Fatalf("fixed scope lost across pages %+v resolves=%d err=%v", second, resolved, err)
	}
	if _, err := c.projectRecord(Selection{TimestampField: "@timestamp", ResolvedIndices: first.ResolvedIndices}, task{id: "scope", kind: "log"}, map[string]any{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "message": "no scope expansion"}, "outside-index", "document", from, to, "run"); err == nil {
		t.Fatal("out-of-scope concrete record accepted")
	}
}

func TestResolveConcreteIndexBoundsAndSafety(t *testing.T) {
	for _, invalid := range [][]string{{".security"}, {"remote:index"}, {"../escape"}, make([]string, 201)} {
		if len(invalid) > 200 {
			for i := range invalid {
				invalid[i] = fmt.Sprintf("logs-%d", i)
			}
		}
		client, err := NewClientWithHTTPClient(Config{Endpoint: "https://example.com", AuthType: AuthBasic}, Credentials{Username: "fixture", Password: "fixture"}, &http.Client{Transport: fixtureTransport{handler: func(*http.Request, map[string]any) (int, any) {
			return 200, map[string]any{"aliases": []any{map[string]any{"name": "approved-alias", "indices": invalid}}}
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.ResolveConcreteIndices(context.Background(), []string{"approved-alias"}); err == nil {
			t.Fatalf("unsafe resolved alias scope accepted %+v", invalid)
		}
		client.Close()
	}
}

func TestOpenSearchNativeTieIncludesConcreteIndex(t *testing.T) {
	c := fixtureClient(t, ProductOpenSearch, func(req *http.Request, body map[string]any) (int, any) {
		if strings.HasSuffix(req.URL.Path, "/point_in_time") {
			if req.Method == "DELETE" {
				return 200, map[string]any{"pits": []any{map[string]any{"successful": true}}}
			}
			return 200, map[string]any{"pit_id": "owned"}
		}
		sort := body["sort"].([]any)
		if len(sort) != 3 || sort[1].(map[string]any)["_index"] != "asc" || sort[2].(map[string]any)["_shard_doc"] != "asc" {
			t.Fatalf("OpenSearch native tiebreaker cannot distinguish indices: %+v", sort)
		}
		return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
	})
	c.capabilities.Version = "3.3.0"
	from, to := testWindow()
	if _, err := c.Collect(context.Background(), testSelection(), from, to, "run"); err != nil {
		t.Fatal(err)
	}
}

package elastic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAPMSignalsUseEventKindInsteadOfCorrelationIDs(t *testing.T) {
	from, to := testWindow()
	rows := []map[string]any{
		{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "processor.event": "span", "transaction.id": "transaction", "span.id": "span", "trace.id": "trace", "span.name": "SQL", "span.duration.us": 3000},
		{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "processor.event": "transaction", "transaction.id": "transaction", "trace.id": "trace", "transaction.name": "checkout", "transaction.duration.us": 9000},
		{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "processor.event": "error", "transaction.id": "transaction", "trace.id": "trace", "error.id": "error", "error.message": "failed"},
	}
	selected := map[string]int{}
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		if req.Method == "DELETE" {
			return 200, map[string]any{"succeeded": true}
		}
		if strings.HasSuffix(req.URL.Path, "/_pit") {
			return 200, map[string]any{"id": "apm-owned"}
		}
		filters := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
		kind := ""
		for _, filter := range filters {
			if term, ok := filter.(map[string]any)["term"].(map[string]any); ok {
				kind, _ = term["processor.event"].(string)
				if kind != "" {
					break
				}
			}
		}
		selected[kind]++
		hits := []any{}
		for i, row := range rows {
			if row["processor.event"] == kind {
				hits = append(hits, map[string]any{"_index": "logs-a", "_id": fmt.Sprint(i), "_source": row, "sort": []any{from.Add(time.Second).UnixMilli(), i}})
			}
		}
		return 200, map[string]any{"hits": map[string]any{"hits": hits}}
	})
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Preset: "elastic_apm", Traces: true, Transactions: true, Errors: true}.Defaults()
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !result.Progress.Complete || len(result.Evidence) != 3 || selected["span"] != 1 || selected["transaction"] != 1 || selected["error"] != 1 || selected[""] != 0 {
		t.Fatalf("APM tasks confused linked signals: evidence=%+v selected=%v err=%v", result.Evidence, selected, err)
	}
	for _, evidence := range result.Evidence {
		switch evidence.Kind {
		case "span":
			if evidence.Span == nil || evidence.Span.DurationMS != 3 {
				t.Fatalf("span duration mapping changed: %+v", evidence)
			}
		case "transaction":
			if evidence.Transaction == nil || evidence.Transaction.Name != "checkout" || evidence.Transaction.DurationMS != 9 {
				t.Fatalf("linked span became a transaction: %+v", evidence)
			}
		}
	}
	if _, err := c.projectRecord(s, task{id: "transaction", kind: "transaction"}, rows[0], "logs-a", "span", from, to, "run"); err == nil {
		t.Fatal("a response ignoring the APM event filter was accepted")
	}
	custom := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Preset: "custom", Transactions: true}.Defaults()
	row := map[string]any{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "transaction.id": "custom", "event.duration": 2_000_000, "name": "custom transaction"}
	if _, err := c.projectRecord(custom, task{id: "transaction", kind: "transaction"}, row, "logs-a", "custom", from, to, "run"); err != nil {
		t.Fatalf("APM-specific fields were imposed on custom records: %v", err)
	}
}

func TestESQLCountUnitsDoNotScaleCardinality(t *testing.T) {
	from, to := testWindow()
	for _, stat := range []string{"count", "cardinality"} {
		for _, exact := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exact=%t", stat, exact), func(t *testing.T) {
				c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
					return 200, map[string]any{"columns": []any{map[string]any{"name": "n", "type": "long"}, map[string]any{"name": "bucket", "type": "date"}}, "values": []any{[]any{3, from.Format(time.RFC3339Nano)}}}
				})
				s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Queries: []Query{{Name: "events", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS n=COUNT(*) | LIMIT 100", ValueColumn: "n", TimestampColumn: "bucket", Statistic: stat, Unit: "us", ExactWindow: exact}}}.Defaults()
				result, err := c.Collect(context.Background(), s, from, to, "run")
				if err != nil || len(result.Points) != 1 || result.Points[0].Unit != "count" || result.Points[0].Value == nil || *result.Points[0].Value != 3 {
					t.Fatalf("ES|QL %s was scaled as a duration: %+v err=%v", stat, result.Points, err)
				}
			})
		}
	}
}

func TestGroupedDimensionsRetainDistinctScrubbedIdentities(t *testing.T) {
	from, to := testWindow()
	for _, values := range [][2]any{{nil, ""}, {"first@example.com", "second@example.com"}} {
		t.Run(fmt.Sprint(values), func(t *testing.T) {
			page := 0
			c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
				if req.Method == "DELETE" {
					return 200, map[string]any{"succeeded": true}
				}
				if strings.HasSuffix(req.URL.Path, "/_pit") {
					return 200, map[string]any{"id": "grouped-owned"}
				}
				page++
				buckets := []any{}
				series := map[string]any{"buckets": buckets}
				if page <= 2 {
					key := map[string]any{"timestamp": from.UnixMilli(), "d0": values[page-1]}
					series["buckets"] = []any{map[string]any{"key": key, "doc_count": 1, "count": map[string]any{"value": 1}, "sum": map[string]any{"value": page}}}
					series["after_key"] = key
				}
				return 200, map[string]any{"aggregations": map[string]any{"series": series}}
			})
			s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "latency", Field: "duration", Statistic: "average", Unit: "ms", GroupBy: []string{"service.name"}}}}.Defaults()
			result, err := c.Collect(context.Background(), s, from, to, "run")
			if err != nil || len(result.Points) != 2 || metricPointKey(result.Points[0]) == metricPointKey(result.Points[1]) {
				t.Fatalf("distinct grouped buckets merged: %+v err=%v", result.Points, err)
			}
			encoded, _ := json.Marshal(result.Points)
			if strings.Contains(string(encoded), "first@example.com") || strings.Contains(string(encoded), "second@example.com") {
				t.Fatal("raw scrubbed dimension leaked")
			}
		})
	}
}

func TestPartialPITCreationReleasesOwnedHandle(t *testing.T) {
	closed := false
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		if req.Method == "DELETE" {
			closed = body["id"] == "rejected-owned"
			return 200, map[string]any{"succeeded": true}
		}
		return 200, map[string]any{"id": "rejected-owned", "_shards": map[string]any{"total": 2, "successful": 1, "failed": 1}}
	})
	if _, err := c.openPIT(context.Background(), []string{"logs-*"}); err == nil || !closed {
		t.Fatalf("rejected PIT leaked its owned handle: closed=%t err=%v", closed, err)
	}
}

func TestRequestBudgetCheckpointsUnstartedMetric(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
		return 200, map[string]any{"hits": map[string]any{"total": map[string]any{"value": 3, "relation": "eq"}}}
	})
	c.cfg.MaxRequestsPerPoll = 1
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "first", Statistic: "count", ExactWindow: true}, {Name: "second", Statistic: "count", ExactWindow: true}}}.Defaults()
	first, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || first.Progress.Complete || first.Progress.NextQuery != 1 || first.Continuation == nil || len(first.Points) != 1 {
		t.Fatalf("budget skipped pending query: %+v err=%v", first, err)
	}
	s.QueryOffset, s.Continuation = first.Progress.NextQuery, first.Continuation
	second, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !second.Progress.Complete || len(second.Points) != 1 || second.Points[0].MetricName != "second" {
		t.Fatalf("pending metric starved: %+v err=%v", second, err)
	}
}

func TestTerminalAsyncContinuationRetainsOuterWindow(t *testing.T) {
	from, to := testWindow()
	requests := 0
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
		if req.Method == "DELETE" {
			return 200, map[string]any{}
		}
		requests++
		if requests == 1 {
			return 200, map[string]any{"id": "terminal-owned", "is_running": true}
		}
		return 200, map[string]any{"id": "terminal-owned", "columns": []any{map[string]any{"name": "n", "type": "long"}}, "values": []any{[]any{3}}}
	})
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Queries: []Query{{Name: "events", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS n=COUNT(*) | LIMIT 1", ValueColumn: "n", Statistic: "count", Unit: "count", ExactWindow: true, Async: true}}, TerminalWindows: []TerminalWindow{{Metric: "events", FromUTC: from.Add(10 * time.Second), UntilUTC: to.Add(-10 * time.Second)}}, QueryOffset: 1}.Defaults()
	first, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || first.Continuation == nil || !first.Continuation.WindowFrom.Equal(from) || !first.Continuation.WindowUntil.Equal(to) {
		t.Fatalf("terminal task replaced fixed cursor window: %+v err=%v", first, err)
	}
	s.Continuation = first.Continuation
	second, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !second.Progress.Complete || len(second.Points) != 1 || !second.Points[0].Provider.BucketStart.Equal(s.TerminalWindows[0].FromUTC) || !second.Points[0].Provider.BucketEnd.Equal(s.TerminalWindows[0].UntilUTC) {
		t.Fatalf("terminal async task could not resume approved interval: %+v err=%v", second, err)
	}
}

func TestVerificationRespectsPrivateLocalPolicy(t *testing.T) {
	for _, policy := range []VerificationPolicy{{}, {ESQL: true}, {Health: true}, {ESQL: true, Health: true}} {
		t.Run(fmt.Sprint(policy), func(t *testing.T) {
			esqlRequests, healthRequests := 0, 0
			c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
				switch req.URL.Path {
				case "/search/":
					return 200, map[string]any{"version": map[string]any{"number": "9.1.0"}}
				case "/search/_query", "/search/_query/async":
					esqlRequests++
					return 200, map[string]any{"columns": []any{map[string]any{"name": "probe", "type": "long"}}, "values": []any{[]any{1}}}
				case "/search/_cluster/health", "/kibana/api/status", "/logstash/_node/stats/jvm,process,events,pipelines":
					healthRequests++
					return 200, map[string]any{"status": "green"}
				default:
					t.Fatalf("unexpected verification request: %s", req.URL.Path)
					return 500, nil
				}
			})
			c.cfg.KibanaEndpoint = "https://kibana.example.com/kibana"
			c.cfg.LogstashEndpoint = "https://logstash.example.com/logstash"
			c.SetVerificationPolicy(policy)
			cap, err := c.Verify(context.Background(), nil)
			if err != nil || cap.ESQL != policy.ESQL || cap.AsyncESQL != policy.ESQL || cap.Health != policy.Health || cap.KibanaHealth != policy.Health || cap.LogstashHealth != policy.Health || !policy.ESQL && esqlRequests != 0 || !policy.Health && healthRequests != 0 {
				t.Fatalf("local policy expanded: %+v err=%v query_requests=%d health_requests=%d", cap, err, esqlRequests, healthRequests)
			}
			if !policy.ESQL && cap.Status["esql"] != "locally_disabled" || !policy.Health && cap.Status["health"] != "locally_disabled" {
				t.Fatalf("local policy lost safe statuses: %+v", cap.Status)
			}
			cap.Status["caller_owned"] = "modified"
			if c.capabilitiesSnapshot().Status["caller_owned"] != "" {
				t.Fatal("returned capability map aliases client state")
			}
			if err := c.UseVerifiedCapabilities(Capabilities{Product: ProductElasticsearch, Version: "9.1.0", ESQL: true, AsyncESQL: true, Health: true, KibanaHealth: true, LogstashHealth: true}); err != nil {
				t.Fatal(err)
			}
			frozen := c.capabilitiesSnapshot()
			if frozen.ESQL != policy.ESQL || frozen.Health != policy.Health {
				t.Fatal("frozen controller capabilities expanded local policy")
			}
		})
	}
}

func TestForbiddenHealthPreviewCannotExpandCollectorPolicy(t *testing.T) {
	from, to := testWindow()
	requests := 0
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
		requests++
		return 200, map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}}
	})
	c.cfg.KibanaEndpoint = "https://kibana.example.com/kibana"
	c.SetVerificationPolicy(VerificationPolicy{})
	s := Selection{ProjectID: "project", KibanaHealth: true}.Defaults()
	result, err := c.Preview(context.Background(), s, from, to, "run")
	if err != nil || requests != 0 || len(result.Points) != 0 || result.Coverage[QueryDefinitions(s)[0].QueryID].Status != "forbidden" {
		t.Fatalf("preview expanded health approval: %+v err=%v requests=%d", result, err, requests)
	}
}

func TestReservedAndCredentialDimensionMappingsAreRejected(t *testing.T) {
	for _, field := range []string{"query_id", "statistic", dimensionIdentity, "user.password", "api_key"} {
		t.Run(field, func(t *testing.T) {
			s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "events", Statistic: "count", GroupBy: []string{field}}}}.Defaults()
			if s.Validate() == nil {
				t.Fatal("unsafe grouped metric metadata accepted")
			}
			s.Metrics = nil
			s.Queries = []Query{{Name: "events", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS n=COUNT(*) BY service.name | LIMIT 100", Statistic: "count", Unit: "count", ValueColumn: "n", DimensionColumns: []string{field}, ExactWindow: true}}
			if s.Validate() == nil {
				t.Fatal("unsafe grouped ES|QL metadata accepted")
			}
		})
	}
}

func TestAdvancedRecordQueriesRequireExecutableSignalMappings(t *testing.T) {
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Preset: "custom", Queries: []Query{{Name: "custom_spans", Kind: "span", Language: "dsl", DSL: json.RawMessage(`{"match_all":{}}`)}}}.Defaults()
	date := Field{Name: "@timestamp", Types: []string{"date"}, Searchable: true}
	if ValidateSelectionFields(s, []Field{date}) == nil {
		t.Fatal("advanced spans without trace/span/duration mappings passed readiness")
	}
	fields := []Field{date, {Name: "trace.id", Types: []string{"keyword"}, Searchable: true}, {Name: "span.id", Types: []string{"keyword"}, Searchable: true}, {Name: "event.duration", Types: []string{"long"}, Searchable: true}}
	if err := ValidateSelectionFields(s, fields); err != nil {
		t.Fatalf("valid custom query mappings rejected: %v", err)
	}
	s.Queries[0].Kind = "transaction"
	if ValidateSelectionFields(s, []Field{date}) == nil {
		t.Fatal("advanced transaction without duration mapping passed readiness")
	}
}

func TestSumCountMappingsCannotInventExtrema(t *testing.T) {
	for _, statistic := range []string{"min", "max", "p95", "cardinality"} {
		t.Run(statistic, func(t *testing.T) {
			s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "metric", Statistic: statistic, Field: "duration", SumField: "duration.sum", CountField: "duration.count", Unit: "ms"}}}.Defaults()
			if s.Validate() == nil {
				t.Fatal("sum/count mapping silently removed required native statistic")
			}
			s.Metrics = nil
			s.Queries = []Query{{Name: "metric", Kind: "metric", Language: "dsl", Statistic: statistic, ValueColumn: "duration", SumColumn: "duration.sum", CountColumn: "duration.count", Unit: "ms"}}
			if s.Validate() == nil {
				t.Fatal("advanced sum/count mapping silently removed native statistic")
			}
		})
	}
}

func TestAdvancedDSLWeightedAverageNeedsOnlySumAndCount(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
		return 200, map[string]any{"aggregations": map[string]any{"sum": map[string]any{"value": 30000}, "count": map[string]any{"value": 3}}}
	})
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Queries: []Query{{Name: "weighted_duration", Kind: "metric", Language: "dsl", Statistic: "average", SumColumn: "duration.sum", CountColumn: "duration.count", Unit: "us", ExactWindow: true}}}.Defaults()
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(result.Points) != 1 || result.Points[0].Value == nil || *result.Points[0].Value != 10 || result.Points[0].Unit != "ms" || result.Points[0].Provider.Count == nil || *result.Points[0].Provider.Count != 3 {
		t.Fatalf("weighted average required an unrelated raw field: %+v err=%v", result.Points, err)
	}
}

func TestControllerPollDeadlineCheckpointsPendingQuery(t *testing.T) {
	from, to := testWindow()
	requests := 0
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
		requests++
		if requests == 1 {
			<-req.Context().Done()
		}
		return 200, map[string]any{"hits": map[string]any{"total": map[string]any{"value": 3, "relation": "eq"}}}
	})
	selection := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Metrics: []MetricSelection{{Name: "first", Statistic: "count", ExactWindow: true}, {Name: "pending", Statistic: "count", ExactWindow: true}}}.Defaults()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	first, err := c.Collect(ctx, selection, from, to, "run")
	if err != nil || first.Progress.Complete || first.Progress.NextQuery != 1 || first.Continuation == nil || len(first.Points) != 1 {
		t.Fatalf("controller work deadline skipped pending task: %+v err=%v", first, err)
	}
	selection.QueryOffset, selection.Continuation = first.Progress.NextQuery, first.Continuation
	second, err := c.Collect(context.Background(), selection, from, to, "run")
	if err != nil || !second.Progress.Complete || len(second.Points) != 1 || second.Points[0].MetricName != "pending" {
		t.Fatalf("pending deadline task did not resume: %+v err=%v", second, err)
	}
}

func TestReadinessRequiresExactStringScopeAndScalarDurations(t *testing.T) {
	selection := Selection{ProjectID: "project", Indices: []string{"logs-*"}, RunIDField: "apex.run_id", ServiceValues: []string{"checkout"}, Traces: true}.Defaults()
	fields := []Field{{Name: "@timestamp", Types: []string{"date"}, Searchable: true}, {Name: "apex.run_id", Types: []string{"keyword"}, Searchable: true}, {Name: "service.name", Types: []string{"keyword"}, Searchable: true}, {Name: "trace.id", Types: []string{"keyword"}, Searchable: true}, {Name: "span.id", Types: []string{"keyword"}, Searchable: true}, {Name: "event.duration", Types: []string{"long"}, Searchable: true}}
	if err := ValidateSelectionFields(selection, fields); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, kind string }{{"apex.run_id", "text"}, {"apex.run_id", "long"}, {"service.name", "text"}, {"event.duration", "histogram"}, {"event.duration", "aggregate_metric_double"}} {
		t.Run(tc.name+tc.kind, func(t *testing.T) {
			copy := append([]Field{}, fields...)
			for i := range copy {
				if copy[i].Name == tc.name {
					copy[i].Types = []string{tc.kind}
				}
			}
			if ValidateSelectionFields(selection, copy) == nil {
				t.Fatal("non-executable scope or record duration passed readiness")
			}
		})
	}
}

func TestCompletedQueriesRetainFailedCleanupOwnership(t *testing.T) {
	from, to := testWindow()
	for _, kind := range []string{"records", "bucket", "async", "partial_pit", "failed_search"} {
		t.Run(kind, func(t *testing.T) {
			c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
				if req.Method == "DELETE" {
					return 503, map[string]any{}
				}
				if strings.HasSuffix(req.URL.Path, "/_pit") {
					r := map[string]any{"id": "owned-private-pit"}
					if kind == "partial_pit" {
						r["_shards"] = map[string]any{"total": 2, "successful": 1, "failed": 1}
					}
					return 200, r
				}
				if kind == "failed_search" {
					return 503, map[string]any{}
				}
				if kind == "async" {
					return 200, map[string]any{"id": "owned-private-async", "columns": []any{map[string]any{"name": "n", "type": "long"}}, "values": []any{[]any{3}}}
				}
				if kind == "bucket" {
					return 200, map[string]any{"aggregations": map[string]any{"series": map[string]any{"buckets": []any{}}}}
				}
				return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
			})
			s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Logs: true}.Defaults()
			if kind == "bucket" {
				s.Logs = false
				s.Metrics = []MetricSelection{{Name: "events", Statistic: "count", BucketSeconds: 10, Kind: "gauge"}}
			}
			if kind == "async" {
				s.Logs = false
				s.Queries = []Query{{Name: "events", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS n=COUNT(*) | LIMIT 1", ValueColumn: "n", Statistic: "count", Unit: "count", ExactWindow: true, Async: true, BucketSeconds: 10}}
			}
			result, err := c.Collect(context.Background(), s, from, to, "run")
			if err != nil || !result.Progress.Complete || result.Continuation != nil || len(result.CleanupContinuations) != 1 {
				t.Fatalf("lost cleanup ownership: %+v %v", result, err)
			}
			owned := result.CleanupContinuations[0]
			if owned.Validate() != nil || !owned.WindowFrom.Equal(from) || !owned.WindowUntil.Equal(to) || owned.KeepAliveUntil.IsZero() {
				t.Fatal("invalid owned cursor", owned)
			}
			status := result.Coverage[QueryDefinitions(s)[0].QueryID].Status
			if kind == "partial_pit" || kind == "failed_search" {
				if status != "unavailable" {
					t.Fatal(status)
				}
			} else if status != "complete" && status != "empty" {
				t.Fatal("cleanup changed coverage", status)
			}
			public, _ := json.Marshal(result)
			if strings.Contains(string(public), "owned-private") || strings.Contains(string(public), "cleanup_continuations") {
				t.Fatal("private ownership disclosed")
			}
		})
	}
}

func TestCleanupOwnershipKnownGoneAndExpiry(t *testing.T) {
	from, to := testWindow()
	for _, status := range []int{200, 404, 503} {
		c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) { return status, map[string]any{"succeeded": true} })
		cursor := &Continuation{QueryID: "owned", PITID: "pit", WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(-time.Second)}
		var result Result
		c.retainFailedCleanup(context.Background(), cursor, &result)
		if len(result.CleanupContinuations) != 0 {
			t.Fatal("known expiry retained")
		}
		cursor.KeepAliveUntil = time.Time{}
		c.retainFailedCleanup(context.Background(), cursor, &result)
		if status == 503 && len(result.CleanupContinuations) != 1 || status != 503 && len(result.CleanupContinuations) != 0 {
			t.Fatal("unknown expiry discarded", status, result)
		}
	}
}

func TestVerificationRetainsFailedProbeCleanupPrivately(t *testing.T) {
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, body map[string]any) (int, any) {
		if req.Method == "DELETE" {
			return 503, map[string]any{}
		}
		if req.URL.Path == "/search/" {
			return 200, map[string]any{"version": map[string]any{"number": "9.1.0"}}
		}
		if strings.HasSuffix(req.URL.Path, "/_pit") {
			return 200, map[string]any{"id": "private-probe-pit"}
		}
		if strings.Contains(req.URL.Path, "_field_caps") {
			return 200, map[string]any{"fields": map[string]any{}}
		}
		if strings.Contains(req.URL.Path, "/_query/async") {
			return 200, map[string]any{"id": "private-probe-async", "values": []any{[]any{1}}}
		}
		if strings.Contains(req.URL.Path, "/_query") {
			return 200, map[string]any{"values": []any{[]any{1}}}
		}
		if strings.Contains(req.URL.Path, "/_cluster/health") {
			return 403, map[string]any{}
		}
		return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
	})
	caps, err := c.Verify(context.Background(), []string{"logs-*"})
	if err != nil || !caps.PIT || !caps.AsyncESQL {
		t.Fatal(caps, err)
	}
	owned := c.PendingCleanupContinuations()
	if len(owned) != 2 {
		t.Fatal("probe ownership lost", owned)
	}
	owned[0].PITID = "mutated"
	if c.PendingCleanupContinuations()[0].PITID == "mutated" {
		t.Fatal("private cursor alias escaped")
	}
	public, _ := json.Marshal(caps)
	if strings.Contains(string(public), "private-probe") {
		t.Fatal("probe identity disclosed")
	}
}

func TestAsyncPollingPreservesActualCreationExpiry(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
		return 200, map[string]any{"id": "owned-async", "is_running": true}
	})
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Queries: []Query{{Name: "events", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS n=COUNT(*) | LIMIT 1", ValueColumn: "n", Statistic: "count", Unit: "count", ExactWindow: true, Async: true}}}.Defaults()
	expiry := time.Now().UTC().Add(time.Minute)
	s.Continuation = &Continuation{QueryID: QueryDefinitions(s)[0].QueryID, AsyncID: "owned-async", WindowFrom: from, WindowUntil: to, KeepAliveUntil: expiry}
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || result.Continuation == nil || !result.Continuation.KeepAliveUntil.Equal(expiry) {
		t.Fatal("GET fabricated renewed keep-alive", result, err)
	}
}

func TestMalformedCreationResponseRetainsKnownOwnedHandle(t *testing.T) {
	from, to := testWindow()
	for _, kind := range []string{"pit", "async"} {
		t.Run(kind, func(t *testing.T) {
			c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
				if req.Method == "DELETE" {
					return 503, map[string]any{}
				}
				if kind == "pit" {
					return 200, map[string]any{"id": "known-malformed-pit", "_shards": map[string]any{"total": "invalid"}}
				}
				return 200, map[string]any{"id": "known-malformed-async", "is_running": "invalid"}
			})
			s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Logs: true}.Defaults()
			if kind == "async" {
				s.Logs = false
				s.Queries = []Query{{Name: "events", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS n=COUNT(*) | LIMIT 1", ValueColumn: "n", Statistic: "count", Unit: "count", ExactWindow: true, Async: true}}
			}
			result, err := c.Collect(context.Background(), s, from, to, "run")
			if err != nil || len(result.CleanupContinuations) != 1 || result.Coverage[QueryDefinitions(s)[0].QueryID].Status != "unavailable" {
				t.Fatal("malformed creation lost known owner", result, err)
			}
		})
	}
}

func TestFailedCleanupCannotExtendPollWorkDeadline(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
		if req.Method == "DELETE" {
			<-req.Context().Done()
			return 503, map[string]any{}
		}
		return 200, map[string]any{"id": "owned-slow-close", "columns": []any{map[string]any{"name": "n", "type": "long"}}, "values": []any{[]any{3}}}
	})
	c.cfg.MaxPollSeconds = 1
	q := Query{Name: "first", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS n=COUNT(*) | LIMIT 1", ValueColumn: "n", Statistic: "count", Unit: "count", ExactWindow: true, Async: true}
	second := q
	second.Name = "second"
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Queries: []Query{q, second}}.Defaults()
	started := time.Now()
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if time.Since(started) > 3*time.Second || err != nil || len(result.Points) != 1 || len(result.CleanupContinuations) != 1 || result.Continuation == nil || result.Progress.NextQuery != 1 {
		t.Fatal("cleanup escaped deadline or lost work", result, err)
	}
	if result.Coverage[QueryDefinitions(s)[0].QueryID].Status != "complete" {
		t.Fatal("cleanup invalidated successful metric")
	}
}

func TestDisabledQueryRetainsFailedCleanupOwnership(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) { return 503, map[string]any{} })
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Logs: true}.Defaults()
	id := QueryDefinitions(s)[0].QueryID
	s.DisabledQueries = map[string]bool{id: true}
	s.Continuation = &Continuation{QueryID: id, PITID: "quota-stopped-owned-pit", WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(time.Minute)}
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || !result.Progress.Complete || len(result.CleanupContinuations) != 1 || result.Coverage[id].Status != "truncated" {
		t.Fatal("quota stop lost cleanup", result, err)
	}
}

func TestCollectionCleanupBoundIncludesPendingProbeOwnership(t *testing.T) {
	from, to := testWindow()
	opened := 0
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
		if req.Method == "DELETE" {
			return 503, map[string]any{}
		}
		if strings.HasSuffix(req.URL.Path, "/_pit") {
			opened++
			return 200, map[string]any{"id": fmt.Sprintf("new-owned-pit-%d", opened)}
		}
		return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
	})
	for i := range 31 {
		c.pendingCleanup = append(c.pendingCleanup, &Continuation{QueryID: "inspection-pit", PITID: fmt.Sprintf("pending-owned-pit-%d", i), WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(time.Minute)})
	}
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Logs: true, Errors: true}.Defaults()
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(result.CleanupContinuations) != 32 || result.Progress.Complete || result.Progress.NextQuery != 1 || result.Continuation == nil || opened != 1 {
		t.Fatalf("cleanup escaped bound: handles=%d progress=%+v opened=%d err=%v", len(result.CleanupContinuations), result.Progress, opened, err)
	}
}

func TestCleanupByteBudgetReservesWorstCaseEscapedHandles(t *testing.T) {
	from, to := testWindow()
	opened := 0
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
		if req.Method == "DELETE" {
			return 503, map[string]any{}
		}
		if strings.HasSuffix(req.URL.Path, "/_pit") {
			opened++
			return 200, map[string]any{"id": strings.Repeat("<", 16384)}
		}
		return 200, map[string]any{"hits": map[string]any{"hits": []any{}}}
	})
	for i := range 2 {
		c.pendingCleanup = append(c.pendingCleanup, &Continuation{QueryID: "inspection-pit", PITID: strings.Repeat("<", 16383) + fmt.Sprint(i), WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(time.Minute)})
	}
	s := Selection{ProjectID: "project", Indices: []string{"logs-*"}, Logs: true}.Defaults()
	result, err := c.Collect(context.Background(), s, from, to, "run")
	body, _ := json.Marshal(result.CleanupContinuations)
	if err != nil || opened != 0 || result.Progress.Complete || result.Continuation == nil || len(result.CleanupContinuations) != 2 || len(body) > maxPrivateCleanupBytes {
		t.Fatal("collection exceeded private cleanup byte bound", len(body), opened, result, err)
	}
	preview, previewErr := c.Preview(context.Background(), s, from, to, "run")
	previewBody, _ := json.Marshal(preview.CleanupContinuations)
	if previewErr != nil || opened != 0 || preview.Progress.Complete || len(preview.CleanupContinuations) != 2 || len(previewBody) > maxPrivateCleanupBytes {
		t.Fatal("preview lost pending ownership reservation", len(previewBody), opened, preview, previewErr)
	}
	// Two new capability probes must fit before verification starts either one.
	if c.cleanupCapacity(nil, 2) {
		t.Fatal("verification did not reserve both maximum handles")
	}
	c.capabilities.Version = ""
	if _, err = c.Verify(context.Background(), []string{"logs-*"}); err == nil || opened != 0 {
		t.Fatal("verification opened unretainable contexts", opened, err)
	}
}

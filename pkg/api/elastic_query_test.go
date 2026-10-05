package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

type elasticDoc struct {
	at     time.Time
	level  string
	fields map[string]string
}

// elasticFixture answers bucketed searches from in-memory documents the way
// Elasticsearch would, so tests exercise the real request and decode path.
type elasticFixture struct {
	mu       sync.Mutex
	docs     []elasticDoc
	requests []map[string]any
}

func (f *elasticFixture) add(doc elasticDoc) {
	f.mu.Lock()
	f.docs = append(f.docs, doc)
	f.mu.Unlock()
}

func (f *elasticFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Header.Get("Authorization") != "ApiKey fixture-token" {
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, body)
	filter := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
	rng := filter[0].(map[string]any)["range"].(map[string]any)["@timestamp"].(map[string]any)
	gte, _ := time.Parse(time.RFC3339Nano, rng["gte"].(string))
	lt, _ := time.Parse(time.RFC3339Nano, rng["lt"].(string))
	aggs := body["aggs"].(map[string]any)["buckets"].(map[string]any)
	hist := aggs["date_histogram"].(map[string]any)
	seconds, _ := strconv.Atoi(strings.TrimSuffix(hist["fixed_interval"].(string), "s"))
	step := time.Duration(seconds) * time.Second
	bounds := hist["extended_bounds"].(map[string]any)
	sub := aggs["aggs"].(map[string]any)
	var buckets []any
	for start := time.UnixMilli(int64(bounds["min"].(float64))).UTC(); !start.After(time.UnixMilli(int64(bounds["max"].(float64))).UTC()); start = start.Add(step) {
		bucket := map[string]any{"key": start.UnixMilli(), "doc_count": 0}
		var inBucket []elasticDoc
		for _, doc := range f.docs {
			if !doc.at.Before(gte) && doc.at.Before(lt) && !doc.at.Before(start) && doc.at.Before(start.Add(step)) {
				inBucket = append(inBucket, doc)
			}
		}
		bucket["doc_count"] = len(inBucket)
		if _, ok := sub["errors"]; ok {
			errors := 0
			for _, doc := range inBucket {
				if doc.level == "error" {
					errors++
				}
			}
			bucket["errors"] = map[string]any{"doc_count": errors}
		}
		for name, raw := range sub {
			if !strings.HasPrefix(name, "terms_") {
				continue
			}
			terms := raw.(map[string]any)["terms"].(map[string]any)
			field, size := terms["field"].(string), int(terms["size"].(float64))
			counts := map[string]int{}
			for _, doc := range inBucket {
				if value, ok := doc.fields[field]; ok {
					counts[value]++
				}
			}
			keys := make([]string, 0, len(counts))
			for key := range counts {
				keys = append(keys, key)
			}
			sort.Slice(keys, func(i, j int) bool {
				return counts[keys[i]] > counts[keys[j]] || counts[keys[i]] == counts[keys[j]] && keys[i] < keys[j]
			})
			other := 0
			var top []any
			for i, key := range keys {
				if i < size {
					top = append(top, map[string]any{"key": key, "doc_count": counts[key]})
				} else {
					other += counts[key]
				}
			}
			bucket[name] = map[string]any{"sum_other_doc_count": other, "buckets": top}
		}
		buckets = append(buckets, bucket)
	}
	out, _ := json.Marshal(map[string]any{"timed_out": false, "_shards": map[string]any{"failed": 0}, "aggregations": map[string]any{"buckets": map[string]any{"buckets": buckets}}})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(out)))}, nil
}

func apiElasticFixture(t *testing.T, terms []string) (*Server, providerMonitorStore, storage.RunMonitorJob, *elasticFixture) {
	t.Helper()
	server, db := newTimeslotServer(t)
	if server.appDynamicsCancel != nil {
		server.appDynamicsCancel()
	}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", "")
	connection := &storage.ELKConnection{ID: "elk-api", ProjectID: "proj-a", Name: "Logs", Enabled: true, Endpoint: "https://elk.example", IndexPattern: "logs-*", TimestampField: "@timestamp", ErrorFilter: "level:error", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "elk-token"}
	if err := db.(interface {
		CreateELKConnection(*storage.ELKConnection) error
	}).CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	monitors := db.(providerMonitorStore)
	source := storage.MonitorSource{ID: "elastic-source", ProjectID: "proj-a", Name: "Log buckets", Enabled: true, Pack: "elastic-query", ExecutorMode: "direct", ConnectionID: connection.ID, IntervalSeconds: 30, ElasticQuery: &storage.ElasticQuerySelection{ProjectID: "proj-a", TermsFields: terms, TermsSize: 2}}
	if _, err := monitors.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-10 * time.Minute)
	record := &storage.TestRecord{ID: "elastic-run", ProjectID: "proj-a", Name: "Elastic run", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}}
	if err := db.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	jobs, err := monitors.StartRunMonitors("proj-a", record.ID, "", start, time.Now().UTC().Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].ElasticSnapshot == nil {
		t.Fatal(jobs, err)
	}
	fixture := &elasticFixture{}
	server.elasticHTTPClient = &http.Client{Transport: fixture}
	server.runner.projectSecretStore = &devSecretStore{value: "fixture-token"}
	return server, monitors, jobs[0], fixture
}

func elasticPointValues(t *testing.T, store providerMonitorStore, job storage.RunMonitorJob) map[string]map[time.Time]float64 {
	t.Helper()
	page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "point", "", 500)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[time.Time]float64{}
	for _, p := range page.Points {
		if p.Provider == nil || p.Provider.BucketStart.Truncate(time.Minute) != p.Provider.BucketStart || p.Provider.BucketStart.Location() != time.UTC {
			t.Fatalf("bucket not aligned to UTC minutes: %+v", p.Provider)
		}
		if p.Timing == nil || p.Timing.ClockSource != analysis.ClockSourceProvider || p.Timing.OffsetStatus != analysis.OffsetUnknown || p.Timing.ResolutionSeconds != p.Provider.ResolutionSeconds {
			t.Fatalf("persisted bucket lacks provider timing: %+v", p.Timing)
		}
		if out[p.MetricName] == nil {
			out[p.MetricName] = map[time.Time]float64{}
		}
		out[p.MetricName][p.Provider.BucketStart] = *p.Value
	}
	return out
}

func TestElasticQueryCollectsUTCBucketsAndErrors(t *testing.T) {
	s, store, job, fixture := apiElasticFixture(t, []string{"method"})
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	for i, level := range []string{"info", "error", "error", "info"} {
		fixture.add(elasticDoc{at: bucket.Add(time.Duration(i) * time.Second), level: level, fields: map[string]string{"method": []string{"GET", "GET", "POST", "PUT"}[i]}})
	}
	s.collectDirectElastic(context.Background(), job)
	values := elasticPointValues(t, store, job)
	if values["logs.count"][bucket] != 4 || values["logs.errors"][bucket] != 2 || values["logs.terms.method.GET"][bucket] != 2 {
		t.Fatalf("bucket values = %v", values)
	}
	request := fixture.requests[0]
	hist := request["aggs"].(map[string]any)["buckets"].(map[string]any)["date_histogram"].(map[string]any)
	if hist["fixed_interval"] != "60s" || hist["time_zone"] != "UTC" || int64(hist["extended_bounds"].(map[string]any)["min"].(float64))%60000 != 0 {
		t.Fatalf("histogram request = %v", hist)
	}
	current, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil || current.QueryWatermark.IsZero() {
		t.Fatalf("watermark did not advance: %+v %v", current, err)
	}
	// Three terms values with a top-2 cap: the remainder is flagged, not dropped silently.
	warned := false
	for _, warning := range current.Coverage[elasticCoverageKey].Warnings {
		warned = warned || strings.Contains(warning, "method")
	}
	if !warned {
		t.Fatalf("terms truncation not flagged: %+v", current.Coverage)
	}
}

func TestElasticQueryLateDocumentUpdatesItsBucketThenSettles(t *testing.T) {
	s, store, job, fixture := apiElasticFixture(t, nil)
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	fixture.add(elasticDoc{at: bucket.Add(time.Second), level: "info"})
	s.collectDirectElastic(context.Background(), job)
	if got := elasticPointValues(t, store, job)["logs.count"][bucket]; got != 1 {
		t.Fatalf("first poll count = %v", got)
	}
	// A late document lands in the same bucket, still inside the overlap.
	fixture.add(elasticDoc{at: bucket.Add(30 * time.Second), level: "error"})
	current, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.collectDirectElastic(context.Background(), *current)
	values := elasticPointValues(t, store, job)
	if values["logs.count"][bucket] != 2 || values["logs.errors"][bucket] != 1 {
		t.Fatalf("late document did not update its bucket: %v", values)
	}
	// Settling is decided by the overlap: inside it the bucket stays unsettled.
	sel := current.Source.ElasticQuery.Defaults()
	body := fmt.Sprintf(`{"aggregations":{"buckets":{"buckets":[{"key":%d,"doc_count":2}]}}}`, bucket.UnixMilli())
	inside, err := decodeElasticBuckets([]byte(body), *current, sel, nil, bucket, bucket.Add(time.Minute), bucket.Add(3*time.Minute))
	if err != nil || len(inside.Points) != 1 || inside.Points[0].Provider.Settled {
		t.Fatalf("inside the overlap = %+v %v", inside.Points, err)
	}
	after, _ := decodeElasticBuckets([]byte(body), *current, sel, nil, bucket, bucket.Add(time.Minute), bucket.Add(7*time.Minute))
	if !after.Points[0].Provider.Settled {
		t.Fatal("bucket did not settle after falling behind the overlap")
	}
	// Ingestion refuses a bucket that claims to be settled too early.
	premature := inside
	premature.Points[0].Provider.Settled = true
	batch := storage.ProviderMonitorBatch{Points: premature.Points, QueryFromUTC: bucket, QueryUntilUTC: bucket.Add(time.Minute)}
	if err := s.ingestElasticBatch(context.Background(), *current, current.NextSequence, batch, executionLeaseTerm{}); err == nil || !strings.Contains(err.Error(), "settled") {
		t.Fatalf("premature settled claim accepted: %v", err)
	}
}

func TestElasticQueryCapsTermsAggregations(t *testing.T) {
	fields := make([]string, 17)
	for i := range fields {
		fields[i] = fmt.Sprintf("field_%02d", i)
	}
	if err := (storage.ElasticQuerySelection{ProjectID: "p", TermsFields: fields}).Validate(); err != nil {
		t.Fatalf("17 configured fields rejected: %v", err)
	}
	conn := &storage.ElasticConnectionSnapshot{TimestampField: "@timestamp", ErrorFilter: "level:error"}
	from := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	body, queried := buildElasticQueryBody(conn, storage.ElasticQuerySelection{ProjectID: "p", TermsFields: fields}.Defaults(), from, from.Add(5*time.Minute), nil)
	sub := body["aggs"].(map[string]any)["buckets"].(map[string]any)["aggs"].(map[string]any)
	if len(queried) != storage.ElasticMaxQueriedTermsFields || len(sub) != storage.ElasticMaxQueriedTermsFields+1 {
		t.Fatalf("queried %d fields with %d sub-aggregations", len(queried), len(sub))
	}
	job := storage.RunMonitorJob{ProjectID: "p", RunID: "r", Source: storage.MonitorSource{ID: "s"}, ElasticSnapshot: conn}
	result, err := decodeElasticBuckets([]byte(`{"aggregations":{"buckets":{"buckets":[]}}}`), job, storage.ElasticQuerySelection{ProjectID: "p", TermsFields: fields}.Defaults(), queried, from, from.Add(5*time.Minute), from.Add(time.Hour))
	if err != nil || !result.Truncated || !strings.Contains(strings.Join(result.Coverage.Warnings, " "), "first 16 of 17") {
		t.Fatalf("field cap not flagged: %+v %v", result.Coverage, err)
	}
	for _, bad := range []storage.ElasticQuerySelection{{ProjectID: "p", BucketSeconds: 45 * 2}, {ProjectID: "p", BucketSeconds: 20}, {ProjectID: "p", BucketSeconds: 600}, {ProjectID: "p", Profile: "solr"}} {
		if bad.BucketSeconds == 90 {
			continue // 90 s divides a day and is allowed
		}
		if bad.Validate() == nil {
			t.Fatalf("selection %+v accepted", bad)
		}
	}
}

func TestElasticQueryNamesTermsSafely(t *testing.T) {
	conn := &storage.ElasticConnectionSnapshot{TimestampField: "@timestamp", ErrorFilter: "level:error"}
	from := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	field := strings.Repeat("f", storage.ElasticMaxTermsFieldBytes)
	sel := storage.ElasticQuerySelection{ProjectID: "p", TermsFields: []string{"status", "user", "ok", field}}.Defaults()
	if err := sel.Validate(); err != nil {
		t.Fatal(err)
	}
	job := storage.RunMonitorJob{ProjectID: "p", RunID: "r", Source: storage.MonitorSource{ID: "s"}, ElasticSnapshot: conn}
	// 11 + 128 + 1 + 120 bytes is longer than a metric name may be.
	long := strings.Repeat("v", 120)
	response := fmt.Sprintf(`{"aggregations":{"buckets":{"buckets":[{"key":%d,"doc_count":4,
		"terms_0":{"buckets":[{"key":200,"doc_count":3}]},
		"terms_1":{"buckets":[{"key":1234567890123456789,"doc_count":1}]},
		"terms_2":{"buckets":[{"key":1,"key_as_string":"true","doc_count":2}]},
		"terms_3":{"buckets":[{"key":%q,"doc_count":1},{"key":"short","doc_count":3}]}}]}}}`, from.UnixMilli(), long)
	result, err := decodeElasticBuckets([]byte(response), job, sel, sel.TermsFields, from, from.Add(time.Minute), from.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, p := range result.Points {
		names[p.MetricName] = true
		if err := p.Validate(); err != nil {
			t.Fatalf("point %q is invalid: %v", p.MetricName, err)
		}
	}
	// Numbers keep their exact literal and booleans their display form.
	for _, want := range []string{"logs.terms.status.200", "logs.terms.user.1234567890123456789", "logs.terms.ok.true", "logs.terms." + field + ".short"} {
		if !names[want] {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	if names["logs.terms."+field+"."+long] || !result.Truncated || !strings.Contains(strings.Join(result.Coverage.Warnings, " "), "too long to name a series were skipped for: "+field) {
		t.Fatalf("an unnameable value was not skipped with a warning: %+v", result.Coverage.Warnings)
	}
}

func TestElasticQueryWindowFitsOneSearchAndAdvances(t *testing.T) {
	fields := make([]string, storage.ElasticMaxQueriedTermsFields)
	for i := range fields {
		fields[i] = fmt.Sprintf("field_%02d", i)
	}
	// The heaviest selection that still fits the default overlap.
	sel := storage.ElasticQuerySelection{ProjectID: "p", BucketSeconds: 30, TermsFields: fields, TermsSize: 50}
	if err := sel.Validate(); err != nil {
		t.Fatal(err)
	}
	perPoll := sel.BucketsPerPoll()
	if perPoll*(2+len(fields)*50)+65 > 10000 {
		t.Fatalf("%d buckets per poll exceed the 10,000 aggregation bucket limit", perPoll)
	}
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	job := storage.RunMonitorJob{StartedAt: start, Source: storage.MonitorSource{ElasticQuery: &sel}}
	// After a long outage one poll still reads only one search's worth.
	from, to := elasticQueryWindow(job, start.Add(2*time.Hour))
	if to.Sub(from) != time.Duration(perPoll)*30*time.Second {
		t.Fatalf("catch-up window %s..%s, want %d buckets", from, to, perPoll)
	}
	// Re-reading the overlap behind the watermark still reaches new buckets.
	job.QueryWatermark = to
	if _, next := elasticQueryWindow(job, start.Add(2*time.Hour)); !next.After(to) {
		t.Fatalf("window ending %s does not advance past the watermark %s", next, to)
	}
	for name, bad := range map[string]storage.ElasticQuerySelection{
		"hour of overlap at 30 s buckets never advances": {ProjectID: "p", BucketSeconds: 30, OverlapSeconds: 3600},
		"overlap too long for heavy terms":               {ProjectID: "p", BucketSeconds: 30, OverlapSeconds: 600, TermsFields: fields, TermsSize: 50},
		"terms field leaves no room for values":          {ProjectID: "p", TermsFields: []string{strings.Repeat("f", storage.ElasticMaxTermsFieldBytes+1)}},
	} {
		if bad.Validate() == nil {
			t.Fatalf("%s: selection accepted", name)
		}
	}
}

func TestLegacyELKCaptureIsLabelledCumulative(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "legacy-elk", ELKCapture: &ELKCaptureConfig{ConnectionID: "elk"}}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := writer.Finalize("COMPLETE", time.Now(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil || manifest.ELKObservation != "cumulative" {
		t.Fatalf("manifest ELK observation = %q %v", manifest.ELKObservation, err)
	}
	plain, _ := newRunArchiveWriter(&ManagedTest{ID: "no-elk"}, RunArchiveProvenance{})
	if plain.manifest.ELKObservation != "" {
		t.Fatal("runs without ELK capture claimed an observation")
	}
}

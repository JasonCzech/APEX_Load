package elastic

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestVendorQualification seeds only a disposable, explicitly opted-in vendor
// fixture. The integration under test receives a verified-TLS client through the
// trusted embedding seam; production still rejects loopback destinations.
func TestVendorQualification(t *testing.T) {
	if os.Getenv("APEXLOAD_ELASTIC_INTEGRATION") != "1" {
		t.Skip("set APEXLOAD_ELASTIC_INTEGRATION=1 for a disposable vendor fixture")
	}
	endpoint := os.Getenv("APEXLOAD_ELASTIC_INTEGRATION_URL")
	product := os.Getenv("APEXLOAD_ELASTIC_INTEGRATION_PRODUCT")
	if product == "" {
		product = ProductElasticsearch
	}
	username := os.Getenv("APEXLOAD_ELASTIC_INTEGRATION_USERNAME")
	password := os.Getenv("APEXLOAD_ELASTIC_INTEGRATION_PASSWORD")
	ca, err := os.ReadFile(os.Getenv("APEXLOAD_ELASTIC_INTEGRATION_CA"))
	if err != nil {
		t.Fatal(err)
	}
	trust := x509.NewCertPool()
	if !trust.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA has no certificates")
	}
	h := &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: trust, ServerName: os.Getenv("APEXLOAD_ELASTIC_INTEGRATION_SERVER_NAME")}, Proxy: nil}}
	defer h.CloseIdleConnections()
	admin := func(method, path, contentType string, body []byte) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, endpoint+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth(username, password)
		req.Header.Set("Content-Type", contentType)
		resp, err := h.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// No raw vendor error bodies or authentication identity are logged.
			t.Fatalf("fixture setup %s %s returned HTTP %d", method, path, resp.StatusCode)
		}
		var out map[string]any
		if len(data) > 0 && json.Unmarshal(data, &out) != nil {
			t.Fatal("fixture setup returned malformed JSON")
		}
		return out
	}
	encode := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	prefix := "apex-qualification-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	indices := []string{prefix + "-a", prefix + "-b"}
	mapping := map[string]any{"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0}, "mappings": map[string]any{"properties": map[string]any{"@timestamp": map[string]any{"type": "date"}, "event.ingested": map[string]any{"type": "date"}, "service.name": map[string]any{"type": "keyword"}, "apex.run_id": map[string]any{"type": "keyword"}, "message": map[string]any{"type": "text"}, "sort_key": map[string]any{"type": "long"}, "duration": map[string]any{"type": "double"}, "latency.sum": map[string]any{"type": "double"}, "latency.count": map[string]any{"type": "double"}}}}
	for _, index := range indices {
		admin("PUT", "/"+index, "application/json", encode(mapping))
		t.Cleanup(func() { admin("DELETE", "/"+index, "application/json", nil) })
	}
	t.Log("created disposable mapped fixture indices")
	from := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	to := from.Add(time.Minute)
	runID := "fixture-run"
	const total = 10005
	var bulk bytes.Buffer
	for i := 0; i < total; i++ {
		// Adjacent documents deliberately share timestamps and IDs across the
		// two concrete indices, while the sortable source key remains unique.
		index := indices[i%2]
		bulk.Write(encode(map[string]any{"index": map[string]any{"_index": index, "_id": strconv.Itoa(i / 2)}}))
		bulk.WriteByte('\n')
		bulk.Write(encode(map[string]any{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "event.ingested": from.Add(2 * time.Second).Format(time.RFC3339Nano), "service.name": "checkout", "apex.run_id": runID, "message": "fixture Authorization: Bearer hidden-test-token operator@example.com", "sort_key": i, "duration": 5000, "latency.sum": 10000, "latency.count": 2}))
		bulk.WriteByte('\n')
	}
	for i, excluded := range []map[string]any{
		{"@timestamp": from.Add(-time.Second).Format(time.RFC3339Nano), "apex.run_id": runID, "service.name": "checkout"},
		{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "apex.run_id": "other-run", "service.name": "checkout"},
		{"@timestamp": from.Add(time.Second).Format(time.RFC3339Nano), "apex.run_id": runID, "service.name": "other-service"},
	} {
		bulk.Write(encode(map[string]any{"index": map[string]any{"_index": indices[0], "_id": "excluded-" + strconv.Itoa(i)}}))
		bulk.WriteByte('\n')
		excluded["message"], excluded["sort_key"], excluded["duration"] = "outside approved scope", total+i, 900000
		bulk.Write(encode(excluded))
		bulk.WriteByte('\n')
	}
	// wait_for is used only by fixture setup. Provider requests never refresh,
	// change settings, provision pipelines or write telemetry.
	if r := admin("POST", "/_bulk?refresh=wait_for", "application/x-ndjson", bulk.Bytes()); r["errors"] != false {
		t.Fatal("fixture bulk indexing failed")
	}
	t.Log("seeded 10,005 fixture documents with equal timestamps and repeated IDs across indices")
	alias := prefix + "-approved"
	actions := []any{}
	for _, index := range indices {
		actions = append(actions, map[string]any{"add": map[string]any{"index": index, "alias": alias, "filter": map[string]any{"term": map[string]any{"apex.run_id": runID}}}})
	}
	admin("POST", "/_aliases", "application/json", encode(map[string]any{"actions": actions}))
	credentials := Credentials{Username: username, Password: password}
	if product == ProductElasticsearch {
		// Permission for product discovery is narrower than cluster monitor.
		// In particular cluster health remains forbidden to this read principal.
		role, user := prefix+"-role", prefix+"-reader"
		admin("PUT", "/_security/role/"+role, "application/json", encode(map[string]any{"cluster": []string{"cluster:monitor/main"}, "indices": []any{map[string]any{"names": []string{prefix + "-*"}, "privileges": []string{"read", "view_index_metadata"}}}}))
		admin("PUT", "/_security/user/"+user, "application/json", encode(map[string]any{"password": "ApexQualificationReadOnly1!", "roles": []string{role}}))
		t.Cleanup(func() { admin("DELETE", "/_security/role/"+role, "application/json", nil) })
		t.Cleanup(func() { admin("DELETE", "/_security/user/"+user, "application/json", nil) })
		credentials = Credentials{Username: user, Password: "ApexQualificationReadOnly1!"}
	} else {
		role, user := prefix+"-role", prefix+"-reader"
		admin("PUT", "/_plugins/_security/api/roles/"+role, "application/json", encode(map[string]any{
			"cluster_permissions": []string{"cluster:monitor/main"},
			"index_permissions":   []any{map[string]any{"index_patterns": []string{prefix + "-*"}, "allowed_actions": []string{"read", "indices:admin/resolve/index", "indices:data/read/point_in_time/create", "indices:data/read/point_in_time/delete"}}},
		}))
		admin("PUT", "/_plugins/_security/api/internalusers/"+user, "application/json", encode(map[string]any{"password": "ApexQualificationReadOnly1!", "opendistro_security_roles": []string{role}}))
		t.Cleanup(func() { admin("DELETE", "/_plugins/_security/api/roles/"+role, "application/json", nil) })
		t.Cleanup(func() { admin("DELETE", "/_plugins/_security/api/internalusers/"+user, "application/json", nil) })
		credentials = Credentials{Username: user, Password: "ApexQualificationReadOnly1!"}
	}
	cfg := Config{Product: product, Endpoint: endpoint, AuthType: AuthBasic, TimeoutSeconds: 30, RequestsPerMinute: 600, MaxRequestsPerPoll: 100, MaxEvidence: 10000, MaxPollSeconds: 60}
	client, err := NewClientWithHTTPClient(cfg, credentials, h)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	capabilities, err := client.Verify(ctx, []string{alias})
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.DSL || !capabilities.PIT || capabilities.Product != product {
		t.Fatalf("missing essential read capabilities: %+v", capabilities)
	}
	if capabilities.Health {
		t.Fatal("read/search verification unexpectedly obtained cluster-monitor permission")
	}
	if expected := os.Getenv("APEXLOAD_ELASTIC_INTEGRATION_VERSION"); expected != "" && capabilities.Version != expected {
		t.Fatalf("version %s, expected %s", capabilities.Version, expected)
	}
	t.Logf("verified vendor version=%s product=%s esql=%t async_esql=%t native_tiebreaker=%t health=%t", capabilities.Version, capabilities.Product, capabilities.ESQL, capabilities.AsyncESQL, capabilities.ShardDoc, capabilities.Health)
	if _, err := client.DiscoverIndices(ctx, []string{alias}); err != nil {
		t.Fatal(err)
	}
	fields, err := client.DiscoverFields(ctx, []string{alias})
	if err != nil {
		t.Fatal(err)
	}
	selection := Selection{ProjectID: "qualification", Indices: []string{alias}, RunIDField: "apex.run_id", IngestedField: "event.ingested", ServiceValues: []string{"checkout"}, UniqueSortField: "sort_key", Metrics: []MetricSelection{
		{Name: "requests", Statistic: "count", ExactWindow: true},
		{Name: "latency", Statistic: "average", SumField: "latency.sum", CountField: "latency.count", Unit: "us", ExactWindow: true},
		{Name: "latency_bucket", Statistic: "average", Field: "duration", Unit: "us", BucketSeconds: 10, GroupBy: []string{"service.name"}},
		{Name: "latency_p95", Statistic: "p95", Field: "duration", Unit: "us", ExactWindow: true},
		{Name: "services", Statistic: "cardinality", Field: "service.name", ExactWindow: true},
	}, Logs: true}.Defaults()
	if err := ValidateSelectionFields(selection, fields); err != nil {
		t.Fatal(err)
	}
	identities := map[string]bool{}
	metricValues := map[string]float64{}
	pages := 0
	for {
		result, err := client.Collect(ctx, selection, from, to, runID)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(result.Evidence) > 0 && (len(result.ResolvedIndices) != 2 || !cursorEqual(result.ResolvedIndices, indices)) {
			t.Fatal("filtered alias lost its approved concrete-index provenance")
		}
		for _, coverage := range result.Coverage {
			if coverage.Status != "complete" && coverage.Status != "partial" && coverage.Status != "empty" {
				for _, task := range selectionTasks(selection) {
					if task.id == coverage.QueryID {
						probe := Result{Capabilities: capabilities}
						var detail error
						var cursor *Continuation
						if task.metric != nil {
							cursor, detail = client.collectMetric(ctx, selection, task, from, to, runID, &probe)
						} else {
							cursor, detail = client.collectRecords(ctx, selection, task, from, to, runID, &probe)
						}
						client.cleanup(cursor)
						t.Fatalf("query %s failed: %v; coverage=%+v", task.name, detail, coverage)
					}
				}
				t.Fatalf("unexpected query coverage: %+v", coverage)
			}
		}
		for _, point := range result.Points {
			if point.Value != nil {
				metricValues[point.MetricName] = *point.Value
			}
		}
		for _, record := range result.Evidence {
			if identities[record.ID] {
				t.Fatal("document duplicated across accepted collection continuations")
			}
			identities[record.ID] = true
			if record.IngestedAt == nil || strings.Contains(record.Summary, "hidden-test-token") || strings.Contains(record.Summary, "operator@example.com") {
				t.Fatal("ingestion time or scrubbing projection failed")
			}
		}
		if result.Progress.Complete {
			break
		}
		if result.Continuation == nil || pages > 10 {
			t.Fatal("bounded collection failed to provide advancing private continuation")
		}
		selection.QueryOffset = result.Progress.NextQuery
		selection.Continuation = result.Continuation
	}
	if len(identities) != total || metricValues["requests"] != total || metricValues["latency"] != 5 || metricValues["latency_bucket"] != 5 || metricValues["latency_p95"] != 5 || metricValues["services"] != 1 || pages < 2 {
		t.Fatalf("normalized capture documents=%d metrics=%v collections=%d", len(identities), metricValues, pages)
	}
	t.Logf("verified PIT document_count=%d collections=%d equal_timestamp_and_cross_index_identity=true weighted_average_ms=5", len(identities), pages)
	if product == ProductElasticsearch {
		if !capabilities.ESQL {
			t.Fatal("qualified Elasticsearch is missing ES|QL")
		}
		query := Query{Name: "esql_count", Language: "esql", Kind: "metric", ESQL: "FROM " + alias + " | STATS observations = COUNT(*) | LIMIT 100", ValueColumn: "observations", Statistic: "count", Unit: "count", ExactWindow: true}
		selection.Metrics, selection.Logs, selection.QueryOffset, selection.Continuation = nil, false, 0, nil
		selection.Queries = []Query{query}
		result, err := client.Collect(ctx, selection, from, to, runID)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Points) != 1 || result.Points[0].Value == nil || *result.Points[0].Value != total || !result.Progress.Complete {
			t.Fatalf("ES|QL exact count failed, coverage=%+v points=%d", result.Coverage, len(result.Points))
		}
		if capabilities.AsyncESQL {
			selection.Queries[0].Async = true
			for i := 0; i < 10; i++ {
				result, err = client.Collect(ctx, selection, from, to, runID)
				if err != nil {
					t.Fatal(err)
				}
				if result.Progress.Complete {
					break
				}
				selection.QueryOffset, selection.Continuation = result.Progress.NextQuery, result.Continuation
			}
			if len(result.Points) != 1 || result.Points[0].Value == nil || *result.Points[0].Value != total {
				t.Fatalf("async ES|QL exact count failed, coverage=%+v points=%d", result.Coverage, len(result.Points))
			}
		}
		t.Log("verified synchronous ES|QL exact-window count and capability-dependent owned asynchronous execution")
	} else {
		selection.Queries = []Query{{Name: "unsupported", Language: "esql", Kind: "metric", ESQL: "FROM " + alias + " | STATS value = COUNT(*) | LIMIT 100", ValueColumn: "value", Statistic: "count", Unit: "count", ExactWindow: true}}
		selection.Metrics, selection.Logs, selection.QueryOffset, selection.Continuation = nil, false, 0, nil
		result, err := client.Collect(ctx, selection, from, to, runID)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Points) != 0 {
			t.Fatal("OpenSearch returned unsupported ES|QL results")
		}
		for _, coverage := range result.Coverage {
			if coverage.Status != "unavailable" {
				t.Fatal("OpenSearch ES|QL rejection was not disclosed")
			}
		}
	}
}

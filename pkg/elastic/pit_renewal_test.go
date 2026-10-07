package elastic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestRejectedPITPagesRetainRenewedOwnership(t *testing.T) {
	from, to := testWindow()
	for _, product := range []string{ProductElasticsearch, ProductOpenSearch} {
		for _, kind := range []string{"records", "metrics"} {
			for _, failure := range []string{"malformed_type", "partial_page", "failed_http", "failed_http_without_id", "invalid_handle", "oversized_handle"} {
				t.Run(product+"/"+kind+"/"+failure, func(t *testing.T) {
					s := testSelection()
					if kind == "metrics" {
						s.Logs = false
						s.Metrics = []MetricSelection{{Name: "events", Statistic: "count", BucketSeconds: 10, Kind: "gauge"}}
					}
					oldExpiry := time.Now().UTC().Add(5 * time.Millisecond)
					oldAfter := []analysis.ScalarValue{{Kind: "string", StringValue: from.Format(time.RFC3339Nano)}, {Kind: "integer", StringValue: "1"}}
					oldComposite := map[string]analysis.ScalarValue{"timestamp": {Kind: "integer", StringValue: "1767225600000"}}
					s.Continuation = &Continuation{QueryID: QueryDefinitions(s)[0].QueryID, PITID: "prior-owned-pit", WindowFrom: from, WindowUntil: to, KeepAliveUntil: oldExpiry, ResolvedIndices: []string{"logs-1"}}
					if kind == "records" {
						s.Continuation.SearchAfter = oldAfter
					} else {
						s.Continuation.CompositeAfter = oldComposite
					}
					priorCursor := *s.Continuation
					closedID, searched := "", 0
					c := fixtureClient(t, product, func(req *http.Request, body map[string]any) (int, any) {
						if req.Method == "DELETE" {
							if product == ProductOpenSearch {
								closedID = body["pit_id"].([]any)[0].(string)
							} else {
								closedID, _ = body["id"].(string)
							}
							return 503, map[string]any{}
						}
						searched++
						pit := body["pit"].(map[string]any)
						if pit["id"] != "prior-owned-pit" || pit["keep_alive"] != pitKeepAlive {
							t.Fatal("search changed immutable owner or renewal policy", body)
						}
						// Renewal may outlive a nearly expired durable cursor, even
						// when the page cannot be accepted or cleanup is unavailable.
						time.Sleep(10 * time.Millisecond)
						page := map[string]any{"pit_id": "renewed-owned-pit"}
						switch failure {
						case "malformed_type":
							page["timed_out"] = "invalid"
						case "partial_page":
							page["timed_out"] = true
						case "failed_http":
							return 503, page
						case "failed_http_without_id":
							return 503, map[string]any{}
						case "invalid_handle":
							page["pit_id"] = "unsafe\npit"
						case "oversized_handle":
							page["pit_id"] = strings.Repeat("x", 16385)
						}
						return 200, page
					})
					c.cfg.MaxRetries = 0
					started := time.Now().UTC()
					result, err := c.Collect(context.Background(), s, from, to, "run")
					if err != nil || searched != 1 || len(result.CleanupContinuations) != 1 || result.Continuation != nil || len(result.Points) != 0 || len(result.Evidence) != 0 {
						t.Fatal("rejected page lost ownership or published data", result, err, searched)
					}
					owner := result.CleanupContinuations[0]
					wantID := "renewed-owned-pit"
					if failure == "failed_http_without_id" || failure == "invalid_handle" || failure == "oversized_handle" {
						wantID = "prior-owned-pit"
					}
					if owner.PITID != wantID || closedID != wantID || owner.Validate() != nil || owner.KeepAliveUntil.Before(started.Add(2*time.Minute)) || time.Now().UTC().Before(oldExpiry) {
						t.Fatal("retained stale or invalid renewal ownership", owner, closedID)
					}
					if result.Coverage[QueryDefinitions(s)[0].QueryID].Status != "unavailable" || !reflect.DeepEqual(s.Continuation, &priorCursor) {
						t.Fatal("rejected renewal advanced coverage or durable pagination", result, s.Continuation)
					}
				})
			}
		}
	}
}

func TestPITRestartRetainsPreviousOwnerBeforeReplacement(t *testing.T) {
	from, to := testWindow()
	for _, product := range []string{ProductElasticsearch, ProductOpenSearch} {
		for _, kind := range []string{"records", "metrics"} {
			for _, reopen := range []string{"success", "failure"} {
				t.Run(product+"/"+kind+"/"+reopen, func(t *testing.T) {
					s := testSelection()
					if kind == "metrics" {
						s.Logs = false
						s.Metrics = []MetricSelection{{Name: "events", Statistic: "count", BucketSeconds: 10, Kind: "gauge"}}
					}
					s.Continuation = &Continuation{QueryID: QueryDefinitions(s)[0].QueryID, PITID: "prior-owned-pit", WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(5 * time.Millisecond), ResolvedIndices: []string{"logs-1"}}
					searched, opened := 0, 0
					closed := map[string]bool{}
					c := fixtureClient(t, product, func(req *http.Request, body map[string]any) (int, any) {
						if req.Method == "DELETE" {
							if product == ProductOpenSearch {
								closed[body["pit_id"].([]any)[0].(string)] = true
							} else {
								closed[body["id"].(string)] = true
							}
							return 503, map[string]any{}
						}
						if !strings.HasSuffix(req.URL.Path, "/_search") {
							opened++
							if !closed["rotated-before-404"] {
								t.Fatal("replacement opened before previous owner cleanup")
							}
							if reopen == "failure" {
								return 503, map[string]any{}
							}
							if product == ProductOpenSearch {
								return 200, map[string]any{"pit_id": "replacement-owned-pit"}
							}
							return 200, map[string]any{"id": "replacement-owned-pit"}
						}
						searched++
						if searched == 1 {
							time.Sleep(10 * time.Millisecond)
							return 404, map[string]any{"pit_id": "rotated-before-404"}
						}
						if body["search_after"] != nil || body["pit"].(map[string]any)["id"] != "replacement-owned-pit" {
							t.Fatal("restart did not reset private pagination", body)
						}
						return 200, map[string]any{"hits": map[string]any{"hits": []any{}}, "aggregations": map[string]any{"series": map[string]any{"buckets": []any{}}}}
					})
					c.cfg.MaxRetries = 0
					started := time.Now().UTC()
					result, err := c.Collect(context.Background(), s, from, to, "run")
					wantOwners := 1
					if reopen == "success" {
						wantOwners = 2
					}
					if err != nil || opened != 1 || len(result.CleanupContinuations) != wantOwners || result.CleanupContinuations[0].PITID != "rotated-before-404" || result.CleanupContinuations[0].KeepAliveUntil.Before(started.Add(2*time.Minute)) {
						t.Fatal("restart lost previous renewed ownership", result, err, opened)
					}
					status := result.Coverage[QueryDefinitions(s)[0].QueryID].Status
					if reopen == "failure" && status != "unavailable" || reopen == "success" && status != "empty" {
						t.Fatal("cleanup changed independent query coverage", status)
					}
				})
			}
		}
	}
}

func TestPITRestartReservesCleanupCapacity(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
		if req.Method != "DELETE" {
			t.Fatal("restart opened context beyond cleanup budget")
		}
		return 503, map[string]any{}
	})
	var result Result
	for _, suffix := range []string{"1", "2"} {
		result.CleanupContinuations = append(result.CleanupContinuations, &Continuation{QueryID: "prior", PITID: strings.Repeat("<", 16383) + suffix, WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(time.Minute)})
	}
	cursor := &Continuation{QueryID: "restart", PITID: "rotated-before-404", WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(2 * time.Minute)}
	if err := c.restartPIT(context.Background(), []string{"logs-*"}, cursor, &result); !errors.Is(err, ErrBudget) || cursor.PITID != "" || len(result.CleanupContinuations) != 3 {
		t.Fatal("restart lost owner or exceeded capacity", cursor, result, err)
	}
}

type pitBodyReadError struct{ io.Reader }

func (body pitBodyReadError) Read(p []byte) (int, error) {
	n, err := body.Reader.Read(p)
	if err == io.EOF {
		err = errors.New("fixture body read failed after complete JSON")
	}
	return n, err
}

type pitRawResponseTransport struct {
	data      string
	readError bool
	closedID  string
}

func (transport *pitRawResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == "DELETE" {
		var body struct {
			ID    string   `json:"id"`
			PITID []string `json:"pit_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		transport.closedID = body.ID
		if len(body.PITID) == 1 {
			transport.closedID = body.PITID[0]
		}
		return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	}
	var body io.Reader = strings.NewReader(transport.data)
	if transport.readError {
		body = pitBodyReadError{body}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(body), Request: req}, nil
}

func TestPITReadFailureKeepsBoundedCompleteResponseIdentity(t *testing.T) {
	from, to := testWindow()
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) { return 200, nil })
	c.http.Transport = &pitRawResponseTransport{data: `{"pit_id":"renewed-before-read-error"}`, readError: true}
	cursor := &Continuation{QueryID: "read-error", PITID: "prior-owned-pit", WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(time.Millisecond)}
	started := time.Now().UTC()
	var response searchResponse
	if err := c.searchPIT(context.Background(), cursor, map[string]any{"pit": map[string]any{"id": cursor.PITID, "keep_alive": pitKeepAlive}}, &response); err == nil || cursor.PITID != "renewed-before-read-error" || cursor.KeepAliveUntil.Before(started.Add(2*time.Minute)) {
		t.Fatal("read failure lost latest bounded owner", cursor, err)
	}
}

func TestRejectedSyntaxPITPagesKeepOnlyTopLevelOwnership(t *testing.T) {
	from, to := testWindow()
	for _, product := range []string{ProductElasticsearch, ProductOpenSearch} {
		for _, kind := range []string{"records", "metrics"} {
			for _, tc := range []struct{ name, body, owner string }{
				{"truncated", `{"pit_id":"renewed-top-level", "hits":`, "renewed-top-level"},
				{"malformed", `{"pit_id":"renewed-top-level", "hits":!}`, "renewed-top-level"},
				{"nested_only", `{"hits":{"hits":[{"_source":{"pit_id":"nested-source-value"}}]}, "timed_out":!}`, "prior-owned-pit"},
				{"nested_before_top_level", `{"hits":{"hits":[{"_source":{"pit_id":"nested-source-value"}}]}, "pit_id":"renewed-top-level", "timed_out":!}`, "renewed-top-level"},
				{"unmapped_large_number", `{"hits":{"hits":[{"_source":{"unmapped":1e400}}]}, "pit_id":"renewed-top-level", "timed_out":!}`, "renewed-top-level"},
				{"duplicate", `{"pit_id":"first-owner", "pit_id":"second-owner", "hits":!}`, "prior-owned-pit"},
			} {
				t.Run(product+"/"+kind+"/"+tc.name, func(t *testing.T) {
					s := testSelection()
					if kind == "metrics" {
						s.Logs = false
						s.Metrics = []MetricSelection{{Name: "events", Statistic: "count", BucketSeconds: 10, Kind: "gauge"}}
					}
					s.Continuation = &Continuation{QueryID: QueryDefinitions(s)[0].QueryID, PITID: "prior-owned-pit", WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(-time.Millisecond), ResolvedIndices: []string{"logs-1"}}
					c := fixtureClient(t, product, func(*http.Request, map[string]any) (int, any) { return 200, nil })
					transport := &pitRawResponseTransport{data: tc.body}
					c.http.Transport = transport
					started := time.Now().UTC()
					result, err := c.Collect(context.Background(), s, from, to, "run")
					if err != nil || len(result.CleanupContinuations) != 1 || result.CleanupContinuations[0].PITID != tc.owner || transport.closedID != tc.owner || result.CleanupContinuations[0].KeepAliveUntil.Before(started.Add(2*time.Minute)) || len(result.Points) != 0 || len(result.Evidence) != 0 || result.Continuation != nil || result.Coverage[QueryDefinitions(s)[0].QueryID].Status != "unavailable" {
						t.Fatal("rejected syntax lost or misidentified ownership", result, transport.closedID, err)
					}
				})
			}
		}
	}
}

func TestVerificationRejectedNativePITProbeRetainsLatestOwner(t *testing.T) {
	c := fixtureClient(t, ProductOpenSearch, func(req *http.Request, body map[string]any) (int, any) {
		if req.Method == "DELETE" {
			return 503, map[string]any{}
		}
		if req.Method == "GET" && req.URL.Path == "/search/" {
			return 200, map[string]any{"version": map[string]any{"number": "3.3.0", "distribution": "opensearch"}}
		}
		if strings.HasSuffix(req.URL.Path, "/point_in_time") {
			return 200, map[string]any{"pit_id": "probe-prior-owner"}
		}
		if body["pit"] != nil {
			return 200, map[string]any{"pit_id": "probe-latest-owner", "timed_out": "invalid"}
		}
		return 200, map[string]any{}
	})
	started := time.Now().UTC()
	caps, err := c.Verify(context.Background(), []string{"logs-*"})
	owned := c.PendingCleanupContinuations()
	if err != nil || !caps.PIT || caps.ShardDoc || len(owned) != 1 || owned[0].PITID != "probe-latest-owner" || owned[0].KeepAliveUntil.Before(started.Add(2*time.Minute)) {
		t.Fatal("rejected verification probe lost ownership or became available", caps, owned, err)
	}
}

func TestPITSearchStoppedBeforeDispatchPreservesExpiry(t *testing.T) {
	from, to := testWindow()
	for _, stopped := range []string{"budget", "cancelled"} {
		t.Run(stopped, func(t *testing.T) {
			c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
				t.Fatal("stopped PIT search was dispatched")
				return 200, nil
			})
			ctx := context.Background()
			wantErr := ErrBudget
			if stopped == "budget" {
				ctx = context.WithValue(ctx, budgetKey{}, &budget{})
			} else {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx, wantErr = cancelled, context.Canceled
			}
			expiry := time.Now().UTC().Add(time.Second)
			cursor := &Continuation{QueryID: "stopped", PITID: "prior-owned-pit", WindowFrom: from, WindowUntil: to, KeepAliveUntil: expiry}
			var response searchResponse
			if err := c.searchPIT(ctx, cursor, map[string]any{"pit": map[string]any{"id": cursor.PITID, "keep_alive": pitKeepAlive}}, &response); !errors.Is(err, wantErr) || !cursor.KeepAliveUntil.Equal(expiry) || cursor.PITID != "prior-owned-pit" {
				t.Fatal("unissued search fabricated renewal", cursor, err)
			}
		})
	}
}

func TestCleanupBackpressurePreservesOwnedIncomingContinuation(t *testing.T) {
	from, to := testWindow()
	for _, kind := range []string{"records", "metrics", "async"} {
		t.Run(kind, func(t *testing.T) {
			c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
				t.Fatal("cleanup backpressure dispatched upstream work")
				return 200, nil
			})
			for _, suffix := range []string{"1", "2"} {
				c.pendingCleanup = append(c.pendingCleanup, &Continuation{QueryID: "prior", PITID: strings.Repeat("<", 16383) + suffix, WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(time.Minute)})
			}
			s := testSelection()
			if kind == "metrics" {
				s.Logs = false
				s.Metrics = []MetricSelection{{Name: "events", Statistic: "count", BucketSeconds: 10, Kind: "gauge"}}
			} else if kind == "async" {
				s.Logs = false
				s.Queries = []Query{{Name: "events", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS n=COUNT(*) | LIMIT 1", ValueColumn: "n", Statistic: "count", Unit: "count", ExactWindow: true, Async: true}}
			}
			cursor := &Continuation{QueryID: QueryDefinitions(s)[0].QueryID, WindowFrom: from, WindowUntil: to, KeepAliveUntil: time.Now().UTC().Add(time.Minute), ResolvedIndices: []string{"logs-1"}}
			if kind == "async" {
				cursor.AsyncID = "incoming-owned-async"
			} else {
				cursor.PITID = "incoming-owned-pit"
				if kind == "records" {
					cursor.SearchAfter = []analysis.ScalarValue{{Kind: "string", StringValue: from.Format(time.RFC3339Nano)}, {Kind: "integer", StringValue: "1"}}
				} else {
					cursor.CompositeAfter = map[string]analysis.ScalarValue{"timestamp": {Kind: "integer", StringValue: "1767225600000"}}
				}
			}
			s.Continuation = cursor
			result, err := c.Collect(context.Background(), s, from, to, "run")
			if err != nil || result.Continuation == cursor || !reflect.DeepEqual(result.Continuation, cursor) || !reflect.DeepEqual(result.Progress.Continuation, cursor) || result.Progress.Complete || len(result.Coverage) != 0 || len(result.Points) != 0 || len(result.Evidence) != 0 {
				t.Fatal("pre-dispatch backpressure lost incoming owner or advanced data", result, err)
			}
		})
	}
}

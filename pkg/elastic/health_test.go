package elastic

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHealthOnlyFinalSnapshotFreezes(t *testing.T) {
	from, to := testWindow()
	requests := 0
	c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
		requests++
		return 200, map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}}
	})
	c.cfg.KibanaEndpoint = "https://kibana.example.com/kibana"
	c.credentials.Kibana = ComponentCredentials{AuthType: AuthBearer, BearerToken: "fixture-token"}
	s := Selection{ProjectID: "project", KibanaHealth: true, FinalHealthAfter: time.Now().UTC().Add(-2 * time.Minute)}.Defaults()
	definitions := QueryDefinitions(s)
	first, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(first.Points) != 1 || !first.Progress.Complete {
		t.Fatalf("first health snapshot=%+v err=%v", first, err)
	}
	id := definitions[0].QueryID
	initial := first.Coverage[id]
	if initial.Status != "complete" || initial.AsOf == nil || initial.AsOf.Before(s.FinalHealthAfter) {
		t.Fatalf("first health snapshot was not settled: %+v", initial)
	}
	// An observation older than the normal polling interval still freezes when
	// it is a successful snapshot after the final settlement cutoff.
	observed := time.Now().UTC().Add(-time.Minute)
	initial.AsOf = &observed
	initial.Message = "Authorization: Bearer private-token operator@example.com"
	s.HealthCoverage = map[string]Coverage{id: initial}
	s.LastHealthAt = time.Time{}
	newUntil := to.Add(time.Minute)
	for i := 0; i < 2; i++ {
		result, err := c.Collect(context.Background(), s, from, newUntil, "run")
		coverage := result.Coverage[id]
		if err != nil || requests != 1 || len(result.Points) != 0 || len(result.Evidence) != 0 || !result.Progress.Complete || len(result.Coverage) != 1 {
			t.Fatalf("final pass created a new snapshot: %+v err=%v requests=%d", result, err, requests)
		}
		if coverage.Status != "complete" || coverage.Points != 0 || coverage.Evidence != 0 || coverage.AsOf == nil || !coverage.AsOf.Equal(observed) || !coverage.From.Equal(from) || !coverage.To.Equal(newUntil) {
			t.Fatalf("final coverage changed identity/window/counts: %+v", coverage)
		}
		if strings.Contains(coverage.Message, "private-token") || strings.Contains(coverage.Message, "operator@example.com") {
			t.Fatalf("prior coverage was not scrubbed: %q", coverage.Message)
		}
		if QueryDefinitions(s)[0].QueryID != id {
			t.Fatal("private health lifecycle changed the query identity")
		}
	}
	if s.HealthCoverage[id].Points != 1 || !s.HealthCoverage[id].AsOf.Equal(observed) {
		t.Fatal("reused coverage mutated the caller's durable snapshot")
	}
}

func TestFinalHealthRequiresPostSettlementSnapshot(t *testing.T) {
	from, to := testWindow()
	requests := 0
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
		requests++
		return 200, map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}}
	})
	c.cfg.KibanaEndpoint = "https://kibana.example.com/kibana"
	c.credentials.Kibana = ComponentCredentials{AuthType: AuthBearer, BearerToken: "fixture-token"}
	s := Selection{ProjectID: "project", KibanaHealth: true}.Defaults()
	id := QueryDefinitions(s)[0].QueryID
	observed := time.Now().UTC().Add(-2 * time.Second)
	s.HealthCoverage = map[string]Coverage{id: {QueryID: id, Status: "complete", From: from, To: to, AsOf: &observed, Points: 1}}
	s.LastHealthAt = observed
	s.FinalHealthAfter = observed.Add(time.Second)
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || requests != 1 || len(result.Points) != 1 || result.Coverage[id].AsOf.Before(s.FinalHealthAfter) {
		t.Fatalf("recent pre-settlement snapshot froze final coverage: %+v err=%v requests=%d", result, err, requests)
	}
	s.HealthCoverage = result.Coverage
	result, err = c.Collect(context.Background(), s, from, to, "run")
	if err != nil || requests != 1 || len(result.Points) != 0 || result.Coverage[id].Status != "complete" {
		t.Fatalf("post-settlement snapshot did not freeze: %+v err=%v requests=%d", result, err, requests)
	}
}

func TestHealthRequestCrossingSettlementCannotFreeze(t *testing.T) {
	from, to := testWindow()
	requests := 0
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
		requests++
		if requests == 1 {
			time.Sleep(250 * time.Millisecond)
		}
		return 200, map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}}
	})
	c.cfg.KibanaEndpoint = "https://kibana.example.com/kibana"
	c.credentials.Kibana = ComponentCredentials{AuthType: AuthBearer, BearerToken: "fixture-token"}
	s := Selection{ProjectID: "project", KibanaHealth: true, FinalHealthAfter: time.Now().UTC().Add(200 * time.Millisecond)}.Defaults()
	id := QueryDefinitions(s)[0].QueryID
	first, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || len(first.Points) != 1 || !first.Coverage[id].AsOf.Before(s.FinalHealthAfter) || time.Now().UTC().Before(s.FinalHealthAfter) {
		t.Fatalf("cutoff-crossing fixture invalid: %+v err=%v", first, err)
	}
	s.HealthCoverage = first.Coverage
	second, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || requests != 2 || len(second.Points) != 1 || second.Coverage[id].AsOf.Before(s.FinalHealthAfter) {
		t.Fatalf("pre-cutoff request froze final coverage: %+v err=%v requests=%d", second, err, requests)
	}
}

func TestHealthOnlySuccessfulSettledCoverageCanFreeze(t *testing.T) {
	from, to := testWindow()
	s := Selection{ProjectID: "project", KibanaHealth: true}.Defaults()
	task := selectionTasks(s)[0]
	now := time.Now().UTC()
	observed := now.Add(-time.Minute)
	s.FinalHealthAfter = now.Add(-2 * time.Minute)
	c := &Client{}
	for _, status := range []string{"complete", "empty", "partial", "unavailable", "forbidden", "unsupported", "stale", "truncated"} {
		t.Run(status, func(t *testing.T) {
			s.HealthCoverage = map[string]Coverage{task.id: {QueryID: task.id, Status: status, From: from, To: to, AsOf: &observed}}
			_, reuse := c.reuseHealthCoverage(s, task, from, to, now)
			if reuse != (status == "complete" || status == "empty") {
				t.Fatalf("status %s incorrectly froze=%t", status, reuse)
			}
		})
	}
	s.HealthCoverage = map[string]Coverage{task.id: {QueryID: task.id, Status: "complete", Truncated: true, From: from, To: to, AsOf: &observed}}
	if _, reuse := c.reuseHealthCoverage(s, task, from, to, now); reuse {
		t.Fatal("a truncated snapshot established final coverage")
	}
	// Accepted public coverage allows bounded collector clock skew, but a
	// timestamp ahead of this executor's clock cannot freeze or throttle it.
	observed = now.Add(20 * time.Second)
	s.FinalHealthAfter = now.Add(10 * time.Second)
	s.HealthCoverage = map[string]Coverage{task.id: {QueryID: task.id, Status: "complete", From: from, To: to, AsOf: &observed}}
	if _, reuse := c.reuseHealthCoverage(s, task, from, to, now); reuse {
		t.Fatal("a future observation froze before settlement")
	}
}

func TestHealthFailuresRetryAtCadenceAndNeverFreeze(t *testing.T) {
	for _, failure := range []string{"unavailable", "partial"} {
		t.Run(failure, func(t *testing.T) {
			from, to := testWindow()
			requests, recovered := 0, false
			c := fixtureClient(t, ProductElasticsearch, func(req *http.Request, _ map[string]any) (int, any) {
				requests++
				switch req.URL.Path {
				case "/search/_cluster/health":
					if !recovered && failure == "unavailable" {
						return 503, map[string]any{"error": "fixture unavailable"}
					}
					return 200, map[string]any{"status": "green", "number_of_nodes": 1}
				case "/search/_nodes/stats/jvm,process,indices":
					if !recovered {
						return 503, map[string]any{"error": "fixture unavailable"}
					}
					return 200, map[string]any{"_nodes": map[string]any{"total": 1, "successful": 1, "failed": 0}, "nodes": map[string]any{"node": map[string]any{"jvm": map[string]any{"mem": map[string]any{"heap_used_percent": 25}}}}}
				default:
					t.Fatalf("unexpected health request: %s", req.URL.Path)
					return 500, nil
				}
			})
			s := Selection{ProjectID: "project", ClusterHealth: true, FinalHealthAfter: time.Now().UTC().Add(-2 * time.Minute)}.Defaults()
			id := QueryDefinitions(s)[0].QueryID
			failed, err := c.Collect(context.Background(), s, from, to, "run")
			if err != nil || failed.Coverage[id].Status != failure || failed.Coverage[id].AsOf == nil {
				t.Fatalf("failed snapshot=%+v err=%v", failed, err)
			}
			s.HealthCoverage = failed.Coverage
			previousRequests := requests
			skipped, err := c.Collect(context.Background(), s, from, to, "run")
			if err != nil || requests != previousRequests || skipped.Coverage[id].Status != failure || len(skipped.Points) != 0 || skipped.Coverage[id].Points != 0 || !skipped.Coverage[id].AsOf.Equal(*failed.Coverage[id].AsOf) {
				t.Fatalf("failure cadence lost coverage: %+v err=%v requests=%d", skipped, err, requests)
			}
			// Reconciliation must retry a failed post-cutoff observation once its
			// bounded cadence expires instead of mistaking it for a frozen read.
			prior := failed.Coverage[id]
			stale := time.Now().UTC().Add(-31 * time.Second)
			prior.AsOf = &stale
			s.HealthCoverage = map[string]Coverage{id: prior}
			recovered = true
			result, err := c.Collect(context.Background(), s, from, to, "run")
			if err != nil || requests <= previousRequests || len(result.Points) == 0 || result.Coverage[id].Status != "complete" {
				t.Fatalf("failed snapshot froze instead of retrying: %+v err=%v requests=%d", result, err, requests)
			}
		})
	}
}

func TestHealthCadenceUsesPerQueryObservation(t *testing.T) {
	from, to := testWindow()
	requests := 0
	c := fixtureClient(t, ProductElasticsearch, func(*http.Request, map[string]any) (int, any) {
		requests++
		return 200, map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}}
	})
	c.cfg.KibanaEndpoint = "https://kibana.example.com/kibana"
	c.credentials.Kibana = ComponentCredentials{AuthType: AuthBearer, BearerToken: "fixture-token"}
	s := Selection{ProjectID: "project", KibanaHealth: true, LastHealthAt: time.Now().UTC()}.Defaults()
	// A global cadence watermark without this query's coverage cannot establish
	// that this component was ever observed.
	result, err := c.Collect(context.Background(), s, from, to, "run")
	if err != nil || requests != 1 || len(result.Points) != 1 {
		t.Fatalf("unknown component was silently skipped: %+v err=%v requests=%d", result, err, requests)
	}
	id := QueryDefinitions(s)[0].QueryID
	s.HealthCoverage = result.Coverage
	s.FinalHealthAfter = time.Now().UTC().Add(time.Minute)
	result, err = c.Collect(context.Background(), s, from, to.Add(time.Second), "run")
	if err != nil || requests != 1 || len(result.Points) != 0 || result.Coverage[id].Status != "complete" || !result.Coverage[id].To.Equal(to.Add(time.Second)) {
		t.Fatalf("pre-settlement cadence lost known coverage: %+v err=%v requests=%d", result, err, requests)
	}
	prior := s.HealthCoverage[id]
	stale := time.Now().UTC().Add(-31 * time.Second)
	prior.AsOf = &stale
	s.HealthCoverage = map[string]Coverage{id: prior}
	result, err = c.Collect(context.Background(), s, from, to, "run")
	if err != nil || requests != 2 || len(result.Points) != 1 {
		t.Fatalf("normal cadence did not refresh: %+v err=%v requests=%d", result, err, requests)
	}
}

func TestHealthCoveragePrivateContractValidation(t *testing.T) {
	from, to := testWindow()
	s := Selection{ProjectID: "project", KibanaHealth: true}.Defaults()
	id := QueryDefinitions(s)[0].QueryID
	observed := time.Now().UTC()
	s.HealthCoverage = map[string]Coverage{id: {QueryID: id, Status: "complete", From: from, To: to, AsOf: &observed, Points: 1}}
	s.FinalHealthAfter = observed.Add(time.Minute)
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(s)
	if err != nil || strings.Contains(string(public), "health_coverage") || strings.Contains(string(public), "final_health_after") {
		t.Fatalf("private health lifecycle leaked into source: %s err=%v", public, err)
	}
	job := JobConfig{Config: Config{Endpoint: "https://example.com", AuthType: AuthBasic}, Selection: Selection{ProjectID: "project", KibanaHealth: true}.Defaults(), HealthCoverage: s.HealthCoverage, FinalHealthAfter: s.FinalHealthAfter}
	private, err := json.Marshal(job)
	if err != nil || !strings.Contains(string(private), "health_coverage") || !strings.Contains(string(private), "final_health_after") || job.Validate() != nil {
		t.Fatalf("signed job lost health lifecycle: %s err=%v validation=%v", private, err, job.Validate())
	}
	invalid := []struct {
		name   string
		change func(*Selection)
	}{
		{"foreign_query", func(v *Selection) { v.HealthCoverage = map[string]Coverage{"foreign": s.HealthCoverage[id]} }},
		{"wrong_query", func(v *Selection) {
			prior := v.HealthCoverage[id]
			prior.QueryID = "foreign"
			v.HealthCoverage = map[string]Coverage{id: prior}
		}},
		{"unknown_status", func(v *Selection) {
			prior := v.HealthCoverage[id]
			prior.Status = "unknown"
			v.HealthCoverage = map[string]Coverage{id: prior}
		}},
		{"evidence", func(v *Selection) {
			prior := v.HealthCoverage[id]
			prior.Evidence = 1
			v.HealthCoverage = map[string]Coverage{id: prior}
		}},
		{"future_observation", func(v *Selection) {
			prior := v.HealthCoverage[id]
			at := observed.Add(2 * time.Minute)
			prior.AsOf = &at
			v.HealthCoverage = map[string]Coverage{id: prior}
		}},
		{"non_utc_cutoff", func(v *Selection) { v.FinalHealthAfter = observed.In(time.FixedZone("other", 3600)) }},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			copy := s
			tc.change(&copy)
			if copy.Validate() == nil {
				t.Fatal("invalid private health lifecycle accepted")
			}
			job.HealthCoverage, job.FinalHealthAfter = copy.HealthCoverage, copy.FinalHealthAfter
			if job.Validate() == nil {
				t.Fatal("invalid signed health lifecycle accepted")
			}
		})
	}
}

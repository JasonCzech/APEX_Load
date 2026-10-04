package collectoragent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

func prometheusJobFixture() RunJob {
	now := time.Now().UTC().Truncate(time.Millisecond)
	anchor := now.Add(-time.Minute)
	return RunJob{ID: "job", ProjectID: "project", RunID: "run", CollectorID: "collector", SourceID: "source", SourceRevision: 1, Pack: "prometheus-query", Destination: "https://prom.example.com", IntervalSeconds: 60, StartedAt: anchor, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), MaxBatches: 32, Prometheus: &prometheus.JobConfig{Config: prometheus.Config{Endpoint: "https://prom.example.com"}, Selection: prometheus.Selection{ProjectID: "project", GridAnchor: &anchor, Queries: []prometheus.Query{{ID: "cpu", Name: "cpu", Expression: "cpu_usage", Unit: "percent"}}}}}
}
func TestPrometheusCollectorRequiresLocalQueryApprovalAndAuthenticationIdentity(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	cfg := prometheus.Config{Endpoint: "https://prom.example.com"}
	agent, err := New(Config{ControllerURL: "http://localhost", CollectorID: "collector", Token: "token", Packs: []string{"prometheus-query"}, ControllerPublicKey: pub, AdapterTargets: []AdapterTarget{{Pack: "prometheus-query", Destination: cfg.Endpoint, Prometheus: &cfg}}})
	if err != nil || !agent.hasPrometheusQueryTarget() {
		t.Fatal(agent, err)
	}
	job := prometheusJobFixture()
	job.Prometheus.Config.ApprovedCIDRs = []string{"10.0.0.0/8"}
	local, err := agent.localPrometheusConfig(job)
	if err != nil || len(local.ApprovedCIDRs) != 0 {
		t.Fatal("grant broadened local network policy", local, err)
	}
	job.Prometheus.Config.AuthType = "bearer"
	if _, err = agent.localPrometheusConfig(job); err == nil {
		t.Fatal("grant changed local authentication identity")
	}
	cfg.AuthType = "bearer"
	if _, err = New(Config{ControllerURL: "http://localhost", CollectorID: "collector", Token: "token", Packs: []string{"prometheus-query"}, ControllerPublicKey: pub, AdapterTargets: []AdapterTarget{{Pack: "prometheus-query", Destination: cfg.Endpoint, Prometheus: &cfg}}}); err == nil {
		t.Fatal("secured target without enrolled key")
	}
	if agent.cfg.AdapterTargets[0].Prometheus.AuthType != "" {
		t.Fatal("caller mutated owned collector trust configuration")
	}
}

func TestPrometheusExporterFlushRetriesOnlyCapturedBatch(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	var scrapes atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { scrapes.Add(1); w.Write([]byte("cpu 1\n")) }))
	defer target.Close()
	now := time.Now().UTC()
	end := now.Add(-time.Second)
	deadline := end.Add(30 * time.Second)
	job := RunJob{ID: "job", ProjectID: "p", RunID: "r", CollectorID: "c", SourceID: "s", SourceRevision: 1, Pack: "prometheus", Destination: target.URL, IntervalSeconds: 10, StartedAt: end.Add(-time.Minute), ExpiresAt: deadline, LeaseExpiresAt: deadline, FlushOnly: true, LoadEndedAt: &end, DrainDeadline: &deadline}
	envelope, _ := SignRunJob(job, key)
	var received []RunSampleBatch
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			var readiness RunJobReadiness
			if json.Unmarshal([]byte(r.Header.Get("X-APEX-Monitor-Readiness")), &readiness) != nil || !slicesContains(readiness.Contracts, PrometheusScrapeContract) {
				t.Error("flush contract not advertised")
			}
			json.NewEncoder(w).Encode([]SignedRunJob{envelope})
		case strings.HasSuffix(r.URL.Path, "/samples"):
			var batch RunSampleBatch
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Error(err)
			}
			received = append(received, batch)
			if !batch.Final {
				job.NextSequence++
				envelope, _ = SignRunJob(job, key)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer controller.Close()
	agent, err := New(Config{ControllerURL: controller.URL, CollectorID: "c", Token: "token", ControllerPublicKey: pub, Packs: []string{"prometheus"}, PrometheusURL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	v := float64(0)
	original := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", RunID: "r", Source: "s", TimestampUTC: end.Add(-time.Second), MetricName: "cpu", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &v}
	agent.pendingBatches[job.ID] = &RunSampleBatch{Job: envelope, Sequence: 0, Points: []analysis.MetricPoint{original}}
	if err = agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scrapes.Load() != 0 || len(received) != 2 || received[0].Final || !received[1].Final || len(received[1].Points) != 0 || received[1].Sequence != 1 || !received[0].Points[0].TimestampUTC.Equal(original.TimestampUTC) {
		t.Fatalf("post-load capture changed scrapes=%d batches=%+v", scrapes.Load(), received)
	}
	bad := job
	bad.Pack = "go-runtime"
	signed, _ := SignRunJob(bad, key)
	if _, err = VerifyRunJob(signed, pub, "c", now); err == nil {
		t.Fatal("flush authorization accepted for unrelated pack")
	}
}

func slicesContains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

type prometheusFixtureTransport func(*http.Request) (*http.Response, error)

func (f prometheusFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPrometheusCollectorExecutesSignedScopedQueryAndPreservesEmptyOrFailedCoverage(t *testing.T) {
	for _, mode := range []string{"numeric", "empty", "forbidden", "credential_failure"} {
		t.Run(mode, func(t *testing.T) {
			pub, key, _ := ed25519.GenerateKey(rand.Reader)
			job := prometheusJobFixture()
			job.QueryFromUTC = job.StartedAt
			job.QueryUntilUTC = job.StartedAt.Add(45 * time.Second)
			envelope, err := SignRunJob(job, key)
			if err != nil {
				t.Fatal(err)
			}
			var received []RunSampleBatch
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var batch RunSampleBatch
				if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
					t.Error(err)
				}
				if _, err := VerifyRunJob(batch.Job, pub, "collector", time.Now().UTC()); err != nil {
					t.Error(err)
				}
				received = append(received, batch)
			}))
			defer controller.Close()
			cfg := job.Prometheus.Config
			agent, err := New(Config{ControllerURL: controller.URL, CollectorID: "collector", Token: "token", ControllerPublicKey: pub, Packs: []string{"prometheus-query"}, AdapterTargets: []AdapterTarget{{Pack: "prometheus-query", Destination: cfg.Endpoint, Prometheus: &cfg}}})
			if err != nil {
				t.Fatal(err)
			}
			var queries int
			agent.prometheusClient = func(config prometheus.Config, credentials prometheus.Credentials) (*prometheus.Client, error) {
				if mode == "credential_failure" {
					return nil, errors.New("credential resolution denied")
				}
				return prometheus.NewClientWithHTTPClient(config, credentials, &http.Client{Transport: prometheusFixtureTransport(func(r *http.Request) (*http.Response, error) {
					queries++
					if r.URL.Path != "/api/v1/query_range" {
						t.Fatalf("unexpected provider endpoint %s", r.URL.Path)
					}
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if r.Form.Get("query") != job.Prometheus.Selection.Queries[0].Expression {
						t.Fatal("selected query changed")
					}
					if mode == "forbidden" {
						return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("denied"))}, nil
					}
					from, _ := strconv.ParseFloat(r.Form.Get("start"), 64)
					to, _ := strconv.ParseFloat(r.Form.Get("end"), 64)
					step, _ := strconv.ParseFloat(r.Form.Get("step"), 64)
					values := []any{}
					for stamp := from; stamp <= to; stamp += step {
						values = append(values, []any{stamp, "0"})
					}
					rows := []any{}
					if mode == "numeric" {
						rows = append(rows, map[string]any{"metric": map[string]string{}, "values": values})
					}
					body, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": rows}})
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})})
			}
			if err = agent.collectPrometheusJob(context.Background(), job, envelope); err != nil {
				t.Fatal(err)
			}
			if len(received) != 1 || received[0].Progress == nil || received[0].Progress.TotalQueries != 1 {
				t.Fatalf("query progress omitted %+v", received)
			}
			batch := received[0]
			if mode == "numeric" {
				if len(batch.Points) != 4 || batch.QueryWindowEnd == nil || !batch.Progress.Complete {
					t.Fatalf("numeric query not committed %+v", batch)
				}
				for _, point := range batch.Points {
					if point.ProjectID != job.ProjectID || point.RunID != job.RunID || point.Source != job.SourceID || point.Value == nil || *point.Value != 0 || point.Provider == nil || point.Provider.Name != "prometheus" {
						t.Fatal("zero or immutable attribution lost", point)
					}
				}
			} else if len(batch.Points) != 0 {
				t.Fatal("missing upstream observations fabricated numeric samples")
			}
			if mode == "empty" && (batch.Coverage["cpu"].Status != "no_data" || !batch.Progress.Complete || batch.QueryWindowEnd == nil) {
				t.Fatal("successful empty query conflated with failed collection", batch)
			}
			if mode == "forbidden" && (batch.Coverage["cpu"].Status != "permission_denied" || batch.Progress.Complete || batch.QueryWindowEnd != nil) {
				t.Fatal("permission failure advanced query", batch)
			}
			if mode == "credential_failure" && (queries != 0 || batch.Coverage["query"].Status != "unavailable" || batch.Progress.Complete || batch.QueryWindowEnd != nil) {
				t.Fatal("pre-request failure fabricated query success", batch)
			}
		})
	}
}
func TestPrometheusCollectorFailureReceiptSurvivesRestartAndAllowsRecovery(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	job := prometheusJobFixture()
	job.QueryFromUTC = job.StartedAt
	job.QueryUntilUTC = job.StartedAt.Add(45 * time.Second)
	envelope, err := SignRunJob(job, key)
	if err != nil {
		t.Fatal(err)
	}
	var received []RunSampleBatch
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch RunSampleBatch
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := VerifyRunJob(batch.Job, pub, job.CollectorID, time.Now().UTC()); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// The actual submission contract allows approved query IDs and the
		// pre-request query/unavailable capability, never a generic collection key.
		for name, coverage := range batch.Coverage {
			if name != "cpu" && (name != "query" || coverage.Status != "unavailable" || coverage.Points != 0 || coverage.Evidence != 0) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		received = append(received, batch)
		if len(received) == 1 {
			// Lose the first acknowledgment after receiving the failure evidence.
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer controller.Close()
	cfg := Config{ControllerURL: controller.URL, CollectorID: job.CollectorID, Token: "token", ControllerPublicKey: pub, Packs: []string{"prometheus-query"}, SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: "prometheus-query", Destination: job.Destination, Prometheus: &job.Prometheus.Config}}}
	agent, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	agent.prometheusClient = func(prometheus.Config, prometheus.Credentials) (*prometheus.Client, error) {
		attempts++
		return nil, errors.New("credential lease unavailable")
	}
	if err = agent.collectPrometheusJob(context.Background(), job, envelope); err == nil {
		t.Fatal("lost failure acknowledgment did not retain a retry")
	}
	if attempts != 1 || len(received) != 1 || received[0].Coverage["query"].Status != "unavailable" || received[0].Progress == nil || received[0].Progress.Complete || received[0].QueryWindowEnd != nil {
		t.Fatal("failed provider read was not submitted as bounded failure evidence", attempts, received)
	}

	// Reload an on-disk retry in a new collector. Retry must upload the saved
	// signed failure evidence before attempting another provider query.
	agent, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = agent.loadSpool(); err != nil {
		t.Fatal(err)
	}
	queries := 0
	agent.prometheusClient = func(config prometheus.Config, credentials prometheus.Credentials) (*prometheus.Client, error) {
		return prometheus.NewClientWithHTTPClient(config, credentials, &http.Client{Transport: prometheusFixtureTransport(func(r *http.Request) (*http.Response, error) {
			queries++
			if err := r.ParseForm(); err != nil {
				return nil, err
			}
			at, err := strconv.ParseFloat(r.Form.Get("start"), 64)
			if err != nil {
				return nil, err
			}
			to, err := strconv.ParseFloat(r.Form.Get("end"), 64)
			if err != nil {
				return nil, err
			}
			step, err := strconv.ParseFloat(r.Form.Get("step"), 64)
			if err != nil || step <= 0 || to < at || (to-at)/step > 1000 {
				return nil, errors.New("invalid fixture evaluation grid")
			}
			values := []any{}
			for stamp := at; stamp <= to; stamp += step {
				values = append(values, []any{stamp, "0"})
			}
			body, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{}, "values": values}}}})
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		})})
	}
	if err = agent.collectPrometheusJob(context.Background(), job, envelope); err != nil {
		t.Fatal("failure receipt retry did not clear the durable spool", err)
	}
	if queries != 0 || len(received) != 2 || agent.pendingBatches[job.ID] != nil {
		t.Fatal("retry re-executed the provider or did not clear its spool", queries, received)
	}
	first, _ := json.Marshal(received[0])
	retry, _ := json.Marshal(received[1])
	if string(first) != string(retry) {
		t.Fatal("failure retry changed its durable content")
	}

	job.NextSequence++
	envelope, err = SignRunJob(job, key)
	if err != nil {
		t.Fatal(err)
	}
	agent.nextCollection[job.ID] = time.Time{} // Advance to the next scheduled poll without sleeping.
	if err = agent.collectPrometheusJob(context.Background(), job, envelope); err != nil {
		t.Fatal("collector did not recover after committing its failure receipt", err)
	}
	if queries != 1 || len(received) != 3 || received[2].Sequence != 1 || len(received[2].Points) != 4 || received[2].Progress == nil || !received[2].Progress.Complete || received[2].QueryWindowEnd == nil || agent.pendingBatches[job.ID] != nil {
		t.Fatal("successful read remained blocked by the failed poll", queries, received)
	}
}

func TestPrometheusCollectorResumesSignedTerminalCursorWithoutRepeatingNumericQuery(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(strconv.FormatBool(unavailable), func(t *testing.T) {
			pub, key, _ := ed25519.GenerateKey(rand.Reader)
			job := prometheusJobFixture()
			job.Reconciling = true
			job.QueryFromUTC = job.StartedAt
			job.QueryUntilUTC = job.StartedAt.Add(45 * time.Second)
			job.LoadEndedAt = &job.QueryUntilUTC
			job.Prometheus.Selection.QueryOffset = 1
			job.Prometheus.Selection.Queries[0].TerminalExpression = `avg_over_time(cpu_usage[{{window.duration}}])`
			job.TerminalWindows = []monitorprovider.EvaluationWindow{{QueryID: "cpu", From: job.StartedAt, To: job.QueryUntilUTC}}
			envelope, err := SignRunJob(job, key)
			if err != nil {
				t.Fatal(err)
			}
			var received []RunSampleBatch
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var batch RunSampleBatch
				if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
					t.Error(err)
				}
				if _, err := VerifyRunJob(batch.Job, pub, "collector", time.Now().UTC()); err != nil {
					t.Error(err)
				}
				received = append(received, batch)
			}))
			defer controller.Close()
			cfg := job.Prometheus.Config
			agent, err := New(Config{ControllerURL: controller.URL, CollectorID: "collector", Token: "token", ControllerPublicKey: pub, Packs: []string{"prometheus-query"}, AdapterTargets: []AdapterTarget{{Pack: "prometheus-query", Destination: cfg.Endpoint, Prometheus: &cfg}}})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			agent.prometheusClient = func(config prometheus.Config, credentials prometheus.Credentials) (*prometheus.Client, error) {
				return prometheus.NewClientWithHTTPClient(config, credentials, &http.Client{Transport: prometheusFixtureTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.Path != "/api/v1/query" {
						t.Fatalf("committed numeric cursor was queried again: %s", r.URL.Path)
					}
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(r.Form.Get("query"), "45000ms") && !strings.Contains(r.Form.Get("query"), "45s") {
						t.Fatal("terminal query lost the frozen window duration")
					}
					if unavailable {
						return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("denied"))}, nil
					}
					at, _ := strconv.ParseFloat(r.Form.Get("time"), 64)
					body, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{at, "0"}}}}})
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})})
			}
			if err = agent.collectPrometheusJob(context.Background(), job, envelope); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(received) != 1 || received[0].Progress == nil || received[0].Progress.TotalQueries != 2 {
				t.Fatalf("terminal operation was not part of durable progress calls=%d batches=%+v", calls, received)
			}
			batch := received[0]
			if unavailable {
				if batch.Progress.Complete || batch.Progress.NextQuery != 1 || batch.QueryWindowEnd != nil || len(batch.Points) != 0 || batch.Coverage["terminal:0:cpu"].Status != "permission_denied" {
					t.Fatal("failed terminal operation advanced the cursor", batch)
				}
			} else if !batch.Progress.Complete || batch.Progress.NextQuery != 2 || batch.QueryWindowEnd == nil || len(batch.Points) != 1 || !batch.Points[0].Provider.QueryWindow || !batch.Points[0].Provider.BucketStart.Equal(job.StartedAt) || !batch.Points[0].Provider.BucketEnd.Equal(job.QueryUntilUTC) || batch.Points[0].Value == nil || *batch.Points[0].Value != 0 {
				t.Fatal("terminal observation lost exact-window attribution or zero", batch)
			}
		})
	}
}

func TestPrometheusCollectorPreservesCapturedDiagnosticsThroughDrainRestartAndLostAcknowledgment(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	old := prometheusJobFixture()
	old.Prometheus.Selection.Targets, old.Prometheus.Selection.Alerts = true, true
	old.Prometheus.Selection.EvidenceSelector = `{job="sut"}`
	old.QueryFromUTC, old.QueryUntilUTC = old.StartedAt, old.StartedAt.Add(time.Minute)
	oldEnvelope, err := SignRunJob(old, key)
	if err != nil {
		t.Fatal(err)
	}
	job := old
	job.Prometheus = &prometheus.JobConfig{Config: old.Prometheus.Config, Selection: old.Prometheus.Selection}
	job.Prometheus.Selection.Targets, job.Prometheus.Selection.Alerts = false, false
	job.Reconciling = true
	job.QueryUntilUTC = old.StartedAt.Add(45 * time.Second)
	job.LoadEndedAt = &job.QueryUntilUTC
	envelope, _ := SignRunJob(job, key)
	observed := old.StartedAt.Add(10 * time.Second)
	alertAt := old.StartedAt.Add(20 * time.Second)
	after := job.QueryUntilUTC.Add(time.Second)
	value := float64(0)
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: job.ProjectID, RunID: job.RunID, Source: job.SourceID, Host: job.CollectorID, TimestampUTC: job.StartedAt, MetricName: "cpu", Kind: analysis.KindGauge, Unit: "percent", Value: &value, Quality: analysis.QualityValid, Provider: &analysis.ProviderMeasurement{Name: "prometheus", MetricID: "cpu", QueryID: "cpu", DefinitionHash: prometheus.DefinitionHash(job.Prometheus.Selection.Queries[0]), BackendProfile: "prometheus", Mode: "evaluation_point", Statistic: "gauge", EvaluationStepSeconds: 15, EntityIDs: map[string]string{"series": strings.Repeat("a", 64)}}}
	late := point
	late.TimestampUTC = after
	first := &RunSampleBatch{Job: oldEnvelope, Sequence: 0, QueryFromUTC: old.QueryFromUTC, QueryUntilUTC: old.QueryUntilUTC, Points: []analysis.MetricPoint{point, late}, Evidence: []monitorprovider.Diagnostic{{ID: "target", Kind: "target", TimestampUTC: observed}, {ID: "after", Kind: "target", TimestampUTC: after}}, Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: old.QueryFromUTC, To: old.QueryUntilUTC}, "targets": {Status: "complete", From: old.QueryFromUTC, To: old.QueryUntilUTC, ObservedAt: &observed}}, Progress: &monitorprovider.QueryProgress{TotalQueries: 3, NextQuery: 3, Complete: true}, QueryWindowEnd: &old.QueryUntilUTC, Final: true}
	backlog := &RunSampleBatch{Job: oldEnvelope, Sequence: 1, QueryFromUTC: old.QueryFromUTC, QueryUntilUTC: old.QueryUntilUTC, Evidence: []monitorprovider.Diagnostic{{ID: "alert", Kind: "alert", TimestampUTC: alertAt}}, Coverage: map[string]monitorprovider.Coverage{"alerts": {Status: "no_data", From: old.QueryFromUTC, To: old.QueryUntilUTC, ObservedAt: &alertAt}}}
	var received []RunSampleBatch
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch RunSampleBatch
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
		}
		if _, err := VerifyRunJob(batch.Job, pub, job.CollectorID, time.Now().UTC()); err != nil {
			t.Error(err)
		}
		received = append(received, batch)
		if len(received) == 1 {
			// The controller commits the content, but the collector loses the ack.
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer controller.Close()
	cfg := Config{ControllerURL: controller.URL, CollectorID: job.CollectorID, Token: "token", ControllerPublicKey: pub, Packs: []string{"prometheus-query"}, SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: "prometheus-query", Destination: job.Destination, Prometheus: &job.Prometheus.Config}}}
	agent, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	agent.pendingBatches[job.ID] = first
	agent.providerBacklog[job.ID] = []*RunSampleBatch{backlog}
	queries := 0
	collect := func(context.Context, time.Time, time.Time) (providerResult, error) {
		queries++
		return providerResult{Points: []analysis.MetricPoint{point}, Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: job.QueryFromUTC, To: job.QueryUntilUTC}}, Progress: monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: true}}, nil
	}
	if err = agent.collectProviderJob(context.Background(), job, envelope, collect); err == nil {
		t.Fatal("lost acknowledgment was not retained for retry")
	}
	// Reload the durable spool and accept the controller's advanced sequence.
	agent, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = agent.loadSpool(); err != nil {
		t.Fatal(err)
	}
	job.NextSequence = 1
	envelope, _ = SignRunJob(job, key)
	if err = agent.collectProviderJob(context.Background(), job, envelope, collect); err != nil {
		t.Fatal(err)
	}
	if queries != 0 || len(received) != 2 {
		t.Fatal("historical queries replaced unacknowledged captured state", queries, received)
	}
	for i, batch := range received {
		if batch.Sequence != int64(i) || batch.Progress != nil || batch.QueryWindowEnd != nil || batch.Final || !batch.QueryFromUTC.Equal(job.StartedAt) || !batch.QueryUntilUTC.Equal(*job.LoadEndedAt) || batch.Coverage["cpu"].Status != "" {
			t.Fatal("captured batch advanced reconciliation or retained old numeric coverage", batch)
		}
		for _, record := range batch.Evidence {
			if record.TimestampUTC.After(*job.LoadEndedAt) {
				t.Fatal("post-load current state entered the archive", record)
			}
		}
	}
	if len(received[0].Points) != 1 || len(received[0].Evidence) != 1 || len(received[1].Evidence) != 1 || received[0].Evidence[0].ID != "target" || received[1].Evidence[0].ID != "alert" {
		t.Fatal("captured evidence was lost during drain", received)
	}
	job.NextSequence = 2
	envelope, _ = SignRunJob(job, key)
	if err = agent.collectProviderJob(context.Background(), job, envelope, collect); err != nil {
		t.Fatal(err)
	}
	if queries != 1 || len(received) != 3 || received[2].Progress == nil || !received[2].Progress.Complete || received[2].QueryWindowEnd == nil || len(received[2].Evidence) != 0 {
		t.Fatal("fresh final query did not own reconciliation progress", queries, received)
	}
}

func TestPrometheusSignedGrantAndCredentialBundleBindDestination(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
	job := prometheusJobFixture()
	sealed, err := SealCredentials(job, decrypt.PublicKey().Bytes(), PrometheusCredentialBundle(prometheus.Credentials{Token: "private-token", ClientKey: "private-key"}))
	if err != nil {
		t.Fatal(err)
	}
	job.Credentials = sealed
	opened, err := OpenCredentials(job, decrypt.Bytes(), time.Now().UTC())
	if err != nil || opened.Prometheus.Runtime().Token != "private-token" {
		t.Fatal(opened, err)
	}
	signed, err := SignRunJob(job, key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(signed.Payload, "private-token") || strings.Contains(signed.Payload, "private-key") {
		t.Fatal("credential disclosed in grant")
	}
	if _, err = VerifyRunJob(signed, pub, "collector", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	changed := job
	changed.Destination = "https://other.example.com"
	if _, err = OpenCredentials(changed, decrypt.Bytes(), time.Now().UTC()); err == nil {
		t.Fatal("credentials followed changed destination")
	}
	changed = job
	changed.Pack = "go-runtime"
	bad, _ := SignRunJob(changed, key)
	if _, err = VerifyRunJob(bad, pub, "collector", time.Now().UTC()); err == nil {
		t.Fatal("query grant used wrong pack")
	}
}
func TestPrometheusInspectionSignatureBoundsSelectionAndPreviewWindow(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	run := prometheusJobFixture()
	now := time.Now().UTC()
	op := InspectionJob{Provider: "prometheus", Prometheus: run.Prometheus, ID: "op", ProjectID: "project", ConnectionID: "connection", ConnectionRevision: 1, CollectorID: "collector", OwnerToken: "owner", LeaseExpiresAt: now.Add(time.Minute), Kind: "preview", FromUTC: now.Add(-5 * time.Minute), ToUTC: now}
	signed, err := SignInspectionJob(op, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyInspectionJob(signed, pub, "collector", now); err != nil {
		t.Fatal(err)
	}
	op.FromUTC = now.Add(-11 * time.Minute)
	signed, _ = SignInspectionJob(op, key)
	if _, err = VerifyInspectionJob(signed, pub, "collector", now); err == nil {
		t.Fatal("unbounded preview accepted")
	}
}
func TestPrometheusCollectorTransportLimitRetainsApprovedPartialCoverage(t *testing.T) {
	job := prometheusJobFixture()
	job.StartedAt = time.Now().UTC().Add(-18 * time.Hour).Truncate(time.Millisecond)
	job.QueryFromUTC = job.StartedAt
	job.QueryUntilUTC = job.StartedAt.Add(4095 * 15 * time.Second)
	job.Reconciling = true
	job.LoadEndedAt = &job.QueryUntilUTC
	job.Prometheus.Config = prometheus.Config{Endpoint: "https://workspace.eastus.prometheus.monitor.azure.com", BackendProfile: prometheus.BackendAzureManaged, AuthType: prometheus.AuthAzure, AzureCredentialMode: "managed_identity"}
	job.Destination = job.Prometheus.Config.Endpoint
	job.Prometheus.Selection.GridAnchor = &job.StartedAt
	labels := map[string]string{}
	for i := range 16 {
		name := fmt.Sprintf("label%02d_", i) + strings.Repeat("x", 120)
		labels[name] = strings.Repeat(`\`, 128)
		job.Prometheus.Selection.Queries[0].AllowedLabels = append(job.Prometheus.Selection.Queries[0].AllowedLabels, name)
	}
	query := job.Prometheus.Selection.Queries[0]
	value := float64(0)
	result := providerResult{Progress: monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: job.QueryFromUTC, To: job.QueryUntilUTC, Points: 4096, ExpectedPoints: 4096}}}
	for i := range 4096 {
		result.Points = append(result.Points, analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: job.ProjectID, Source: job.SourceID, RunID: job.RunID, TimestampUTC: job.StartedAt.Add(time.Duration(i) * 15 * time.Second), MetricName: query.Name, Unit: query.Unit, Kind: analysis.KindGauge, Value: &value, Quality: analysis.QualityValid, Dimensions: labels, Provider: &analysis.ProviderMeasurement{Name: "prometheus", BackendProfile: prometheus.BackendAzureManaged, QueryID: query.ID, DefinitionHash: prometheus.DefinitionHash(query), MetricID: query.ID, Mode: "evaluation_point", Statistic: "gauge", EvaluationStepSeconds: 15, DisplayLabels: labels, EntityIDs: map[string]string{"series": strings.Repeat("a", 64)}, Settled: true}})
	}
	batches, err := providerBatches(job, SignedRunJob{}, result, job.QueryUntilUTC)
	if err != nil || len(batches) != 32 {
		t.Fatalf("expected bounded transport prefix, batches=%d err=%v", len(batches), err)
	}
	retained := 0
	for _, batch := range batches {
		body, err := json.Marshal(batch)
		if err != nil || len(body) > 768<<10 {
			t.Fatal("complete transport batch exceeds its budget", len(body), err)
		}
		retained += len(batch.Points)
	}
	last := batches[len(batches)-1]
	coverage := last.Coverage[query.ID]
	if retained <= 0 || retained >= len(result.Points) || coverage.Status != "partial" || !coverage.Truncated || last.Progress == nil || !last.Progress.Complete || last.QueryWindowEnd == nil || !last.Final {
		t.Fatal("transport loss did not retain approved partial coverage and final progress", retained, coverage, last.Progress)
	}
	if _, unsupported := last.Coverage["retention"]; unsupported || len(last.Coverage) != 1 {
		t.Fatal("transport truncation inserted a capability outside the signed query scope", last.Coverage)
	}
	if result.Coverage[query.ID].Truncated || result.Coverage[query.ID].Status != "complete" {
		t.Fatal("transport assembly mutated the provider query result")
	}
}

func TestPrometheusSharedProviderBatchRetainsProgressAndCoverage(t *testing.T) {
	job := prometheusJobFixture()
	result := providerResult{Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: job.StartedAt, To: time.Now().UTC()}}, Progress: monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, PrometheusCapabilities: &prometheus.Capabilities{Metrics: true}}
	for i := 0; i < 700; i++ {
		value := float64(i)
		result.Points = append(result.Points, analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "provider", Source: "provider", TimestampUTC: job.StartedAt.Add(time.Duration(i) * time.Millisecond), MetricName: "cpu", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Unit: "percent", Value: &value})
	}
	batches, err := providerBatches(job, SignedRunJob{}, result, time.Now().UTC())
	if err != nil || len(batches) != 2 {
		t.Fatal(len(batches), err)
	}
	if len(batches[0].Points) != 512 || len(batches[1].Points) != 188 || batches[0].Progress != nil || batches[1].Progress == nil || !batches[1].Progress.Complete || batches[1].PrometheusCapabilities == nil || batches[1].QueryWindowEnd == nil {
		t.Fatal("batch continuation lost")
	}
	if *batches[0].Points[0].Value != 0 || batches[0].Points[0].Source != "source" || batches[0].Points[0].Host != "collector" {
		t.Fatal("zero or attribution changed")
	}
	if _, err = json.Marshal(batches); err != nil {
		t.Fatal(err)
	}
	// The same shared worker entry point refuses destinations before any I/O.
	agent := &Agent{}
	if err = agent.collectPrometheusJob(context.Background(), job, SignedRunJob{}); err == nil {
		t.Fatal("unapproved query executed")
	}
}

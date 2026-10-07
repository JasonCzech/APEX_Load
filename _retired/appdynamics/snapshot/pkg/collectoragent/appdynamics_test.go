package collectoragent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
)

func TestAppDynamicsInspectionSignatureAndCredentialIdentity(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := InspectionJob{ID: "inspect", ProjectID: "p", ConnectionID: "connection", ConnectionRevision: 3, CollectorID: "collector", Kind: "test", OwnerToken: "owner", LeaseExpiresAt: now.Add(time.Minute), Config: appdynamics.Config{Endpoint: "https://example.appdynamics.com", AccountName: "customer"}}
	var err error
	job.Credentials, err = SealInspectionCredentials(job, decrypt.PublicKey().Bytes(), AppDynamicsCredentialBundle(appdynamics.Credentials{ClientID: "client", ClientSecret: "private-secret"}))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignInspectionJob(job, private)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(signed.Payload, "private-secret") || strings.Contains(signed.Payload, "run_id") {
		t.Fatal("inspection exposed credentials or invented a load run")
	}
	checked, err := VerifyInspectionJob(signed, public, "collector", now)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := OpenCredentials(inspectionCredentialJob(checked), decrypt.Bytes(), now)
	if err != nil || creds.AppDynamics.Runtime().ClientSecret != "private-secret" {
		t.Fatalf("sealed credentials %v", err)
	}
	checked.Config.Endpoint = "https://other.appdynamics.com"
	if _, err = OpenCredentials(inspectionCredentialJob(checked), decrypt.Bytes(), now); err == nil {
		t.Fatal("credentials redirected to another identity")
	}
	for _, at := range []time.Time{now.Add(time.Minute), now.Add(2 * time.Minute)} {
		if _, err = VerifyInspectionJob(signed, public, "collector", at); err == nil {
			t.Fatal("expired inspection accepted")
		}
	}
	if _, err = VerifyInspectionJob(signed, public, "other", now); err == nil {
		t.Fatal("wrong collector accepted")
	}
	tampered := signed
	tampered.Payload = strings.Replace(tampered.Payload, "connection", "changed", 1)
	if _, err = VerifyInspectionJob(tampered, public, "collector", now); err == nil {
		t.Fatal("tampered inspection accepted")
	}
}

type fixtureAppDynamicsInspectionClient struct {
	mode         string
	applications []string
	selection    appdynamics.Selection
}

func (c *fixtureAppDynamicsInspectionClient) Test(context.Context) (appdynamics.Capabilities, error) {
	c.mode = "connection"
	return appdynamics.Capabilities{Metrics: true, Events: true, Analytics: true}, nil
}
func (c *fixtureAppDynamicsInspectionClient) TestApplications(_ context.Context, applications []string) (appdynamics.Capabilities, error) {
	c.mode, c.applications = "approved connection", append([]string(nil), applications...)
	return appdynamics.Capabilities{Metrics: true, Events: true, Analytics: true}, nil
}
func (c *fixtureAppDynamicsInspectionClient) TestSelection(_ context.Context, selection appdynamics.Selection) (appdynamics.Capabilities, error) {
	c.mode, c.selection = "selected source", selection
	return appdynamics.Capabilities{Events: selection.Events}, nil
}

func TestAppDynamicsInspectionPreservesConnectionAndSourceScope(t *testing.T) {
	approved := []string{"7", "8"}
	client := &fixtureAppDynamicsInspectionClient{}
	caps, err := inspectAppDynamicsCapabilities(context.Background(), client, nil, approved)
	if err != nil || !caps.Metrics || !caps.Events || !caps.Analytics || client.mode != "approved connection" || !reflect.DeepEqual(client.applications, approved) {
		t.Fatal("restricted connection test dropped capabilities or approval", caps, err, client)
	}
	selection := &appdynamics.Selection{ProjectID: "p", Applications: []string{"8"}, Events: true}
	client = &fixtureAppDynamicsInspectionClient{}
	caps, err = inspectAppDynamicsCapabilities(context.Background(), client, selection, approved)
	if err != nil || !caps.Events || caps.Metrics || client.mode != "selected source" || !reflect.DeepEqual(client.selection, *selection) {
		t.Fatal("source preparation broadened its selected capabilities", caps, err, client)
	}
	selection.Applications = []string{"9"}
	client = &fixtureAppDynamicsInspectionClient{}
	if _, err := inspectAppDynamicsCapabilities(context.Background(), client, selection, approved); err == nil || client.mode != "" {
		t.Fatal("unapproved application reached the provider", err, client)
	}
	client = &fixtureAppDynamicsInspectionClient{}
	if _, err := inspectAppDynamicsCapabilities(context.Background(), client, nil, nil); err != nil || client.mode != "connection" {
		t.Fatal("unrestricted connection probe changed", err, client)
	}
}

func TestAppDynamicsBatchesPreservePointsAndFinalProgress(t *testing.T) {
	at := time.Now().UTC()
	job := RunJob{ProjectID: "p", RunID: "run", SourceID: "source", CollectorID: "collector", StartedAt: at.Add(-time.Hour)}
	result := appdynamics.Result{Progress: appdynamics.QueryProgress{Complete: true, NextQuery: 1, TotalQueries: 1}, Coverage: map[string]appdynamics.Coverage{"metrics": {Status: "complete", From: job.StartedAt, To: at}}}
	for i := 0; i < 1300; i++ {
		value := float64(i)
		result.Points = append(result.Points, analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "provider", Source: "provider", MetricName: "latency", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &value, Unit: "ms", TimestampUTC: at.Add(time.Duration(i) * time.Millisecond)})
	}
	for i := 0; i < 230; i++ {
		result.Evidence = append(result.Evidence, appdynamics.Diagnostic{ID: strings.Repeat("x", 10), Kind: "event", TimestampUTC: at})
	}
	batches, err := appDynamicsBatches(job, SignedRunJob{}, result, at)
	if err != nil {
		t.Fatal(err)
	}
	points, evidence := 0, 0
	for i, b := range batches {
		points += len(b.Points)
		evidence += len(b.Evidence)
		encoded, _ := json.Marshal(b)
		if len(encoded) > 1<<20 || len(b.Points) > 512 || len(b.Evidence) > 100 {
			t.Fatal("unbounded batch")
		}
		if i < len(batches)-1 && b.QueryWindowEnd != nil {
			t.Fatal("progress committed before last chunk")
		}
		for _, p := range b.Points {
			if p.Source != "source" || p.Host != "collector" || p.RunID != "run" {
				t.Fatal("source attribution not canonical")
			}
		}
	}
	if points != 1300 || evidence != 230 || batches[len(batches)-1].QueryWindowEnd == nil {
		t.Fatal("records dropped or window not committed")
	}
}
func TestAppDynamicsCoverageOnlyBatchAndSpoolBacklog(t *testing.T) {
	at := time.Now().UTC()
	result := appdynamics.Result{Coverage: map[string]appdynamics.Coverage{"metrics": {Status: "unavailable", From: at.Add(-time.Minute), To: at}}}
	batches, err := appDynamicsBatches(RunJob{}, SignedRunJob{}, result, at)
	if err != nil || len(batches) != 1 {
		t.Fatalf("coverage-only %v", err)
	}
	cfg := Config{ControllerURL: "http://localhost:8080", CollectorID: "collector", Token: "token", SpoolDirectory: t.TempDir()}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.pendingBatches["job"] = batches[0]
	a.providerBacklog["job"] = batches
	if err = a.saveSpool(); err != nil {
		t.Fatal(err)
	}
	loaded, _ := New(cfg)
	if err = loaded.loadSpool(); err != nil || len(loaded.providerBacklog["job"]) != 1 || len(loaded.pendingBatches["job"].Coverage) != 1 {
		t.Fatalf("restart dropped provider backlog %v", err)
	}
}

func TestAppDynamicsStreamsDurableChunkQueueAndRecoversLostUploads(t *testing.T) {
	for _, failMiddle := range []bool{false, true} {
		t.Run(map[bool]string{false: "stream", true: "retry"}[failMiddle], func(t *testing.T) {
			public, private, _ := ed25519.GenerateKey(rand.Reader)
			decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
			now := time.Now().UTC()
			job := RunJob{ID: "job", ProjectID: "p", RunID: "run", CollectorID: "collector", SourceID: "source", SourceRevision: 1, Pack: "appdynamics", Destination: "https://controller.example.com", IntervalSeconds: 60, StartedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), MaxBatches: 32, QueryFromUTC: now.Add(-time.Hour), QueryUntilUTC: now, AppDynamics: &appdynamics.JobConfig{Config: appdynamics.Config{Endpoint: "https://controller.example.com", AccountName: "account"}, Selection: appdynamics.Selection{ProjectID: "p", Applications: []string{"1"}}}}
			envelope, err := SignRunJob(job, private)
			if err != nil {
				t.Fatal(err)
			}
			result := appdynamics.Result{Progress: appdynamics.QueryProgress{Complete: true, NextQuery: 1, TotalQueries: 1}, Coverage: map[string]appdynamics.Coverage{"metrics": {Status: "complete", From: job.QueryFromUTC, To: now}}}
			for i := 0; i < 1100; i++ {
				value := float64(i)
				result.Points = append(result.Points, analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", Source: "source", TimestampUTC: now, MetricName: "metric", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &value})
			}
			chunks, err := appDynamicsBatches(job, envelope, result, now)
			if err != nil || len(chunks) != 3 {
				t.Fatalf("chunks %d %v", len(chunks), err)
			}
			seen := []int64{}
			failed := false
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var batch RunSampleBatch
				if json.NewDecoder(r.Body).Decode(&batch) != nil {
					t.Error("invalid batch")
					w.WriteHeader(400)
					return
				}
				if failMiddle && batch.Sequence == 1 && !failed {
					failed = true
					w.WriteHeader(503)
					return
				}
				seen = append(seen, batch.Sequence)
				if !batch.QueryUntilUTC.Equal(now) || !batch.QueryFromUTC.Equal(job.QueryFromUTC) {
					t.Error("chunk query window changed")
				}
				if batch.Sequence < 2 && batch.Progress != nil {
					t.Error("early query progress")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"accepted":true}`))
			}))
			defer controller.Close()
			cfg := Config{ControllerURL: controller.URL, CollectorID: "collector", Token: "token", Packs: []string{"appdynamics"}, ControllerPublicKey: public, CredentialPrivateKey: decrypt.Bytes(), AdapterTargets: []AdapterTarget{{Pack: "appdynamics", Destination: job.Destination}}, SpoolDirectory: t.TempDir()}
			agent, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			agent.pendingBatches[job.ID] = chunks[0]
			agent.providerBacklog[job.ID] = chunks[1:]
			err = agent.collectAppDynamicsJob(context.Background(), job, envelope)
			if failMiddle {
				if err == nil || len(seen) != 1 || agent.pendingBatches[job.ID].Sequence != 1 {
					t.Fatalf("retry state %v %+v", err, seen)
				}
				agent, err = New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err = agent.loadSpool(); err != nil {
					t.Fatal(err)
				}
				job.NextSequence = 1
				envelope, err = SignRunJob(job, private)
				if err != nil {
					t.Fatal(err)
				}
				err = agent.collectAppDynamicsJob(context.Background(), job, envelope)
			}
			if err != nil || len(seen) != 3 || seen[0] != 0 || seen[1] != 1 || seen[2] != 2 || agent.pendingBatches[job.ID] != nil || len(agent.providerBacklog[job.ID]) != 0 {
				t.Fatalf("stream %+v %v", seen, err)
			}
			if agent.nextCollection[job.ID].IsZero() {
				t.Fatal("cadence not set after complete query")
			}
		})
	}
}

func TestAppDynamicsBatchLimitRetainsPrefixAndCoverage(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Minute)
	from := at.Add(-time.Hour)
	job := RunJob{ProjectID: "p", RunID: "run", SourceID: "source", CollectorID: "collector", StartedAt: from, QueryFromUTC: from, QueryUntilUTC: at, Reconciling: true}
	result := appdynamics.Result{Progress: appdynamics.QueryProgress{Complete: true, NextQuery: 1, TotalQueries: 1}, Coverage: map[string]appdynamics.Coverage{"metrics:0": {Status: "complete", From: from, To: at}}}
	value := 1.0
	for i := 0; i < 10000; i++ {
		result.Points = append(result.Points, analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "provider", Source: "provider", MetricName: "latency", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &value, Unit: "ms", TimestampUTC: from, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "1", CatalogPath: strings.Repeat("x", 4096), BucketStart: from, BucketEnd: from.Add(time.Minute), ResolutionSeconds: 60, Statistic: "value"}})
	}
	batches, err := appDynamicsBatches(job, SignedRunJob{}, result, at)
	if err != nil || len(batches) != 32 {
		t.Fatalf("bounded retained batches %d %v", len(batches), err)
	}
	retained := 0
	for _, batch := range batches {
		body, err := json.Marshal(batch)
		if err != nil || len(body) > 768<<10 || !batch.QueryFromUTC.Equal(from) || !batch.QueryUntilUTC.Equal(at) {
			t.Fatal("chunk omitted immutable bounds or exceeded complete payload limit")
		}
		retained += len(batch.Points)
	}
	last := batches[len(batches)-1]
	coverage := last.Coverage["retention"]
	if retained == 0 || retained >= len(result.Points) || coverage.Status != "partial" || !coverage.Truncated || coverage.Points != retained || !coverage.From.Equal(from) || !coverage.To.Equal(at) || last.Coverage["metrics:0"].Status == "complete" {
		t.Fatalf("silent loss or false coverage: retained=%d coverage=%+v", retained, last.Coverage)
	}
	if result.Coverage["metrics:0"].Status != "complete" {
		t.Fatal("assembler mutated the provider result")
	}
}

func TestAppDynamicsLastBatchIncludesMetadataInSizeBudget(t *testing.T) {
	at := time.Now().UTC()
	from := at.Add(-time.Hour)
	job := RunJob{ProjectID: "p", RunID: "run", SourceID: "source", CollectorID: "collector", StartedAt: from, QueryFromUTC: from, QueryUntilUTC: at}
	result := appdynamics.Result{Progress: appdynamics.QueryProgress{Complete: true, NextQuery: 1, TotalQueries: 1}, Coverage: map[string]appdynamics.Coverage{"topology": {Status: "complete", From: from, To: at}}}
	for i := 0; i < 200; i++ {
		result.Topology = append(result.Topology, appdynamics.Entity{ID: "1", Name: strings.Repeat("x", 512), Path: strings.Repeat("p", 4096), Type: "node", EntityIDs: map[string]string{"application": "1"}})
	}
	batches, err := appDynamicsBatches(job, SignedRunJob{}, result, at)
	if err != nil || len(batches) != 1 {
		t.Fatalf("metadata batch %d %v", len(batches), err)
	}
	body, _ := json.Marshal(batches[0])
	if len(body) > 768<<10 || len(batches[0].Topology) == 0 || len(batches[0].Topology) >= len(result.Topology) || !batches[0].Coverage["topology"].Truncated || !batches[0].Coverage["retention"].Truncated {
		t.Fatal("oversized final topology was retained without bounded coverage")
	}
}

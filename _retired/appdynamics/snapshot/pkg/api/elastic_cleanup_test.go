package api

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

type elasticCleanupFixture struct {
	server       *Server
	store        providerMonitorStore
	cleanup      elasticCleanupStore
	job          storage.RunMonitorJob
	cursor       *analysis.ProviderContinuation
	signer       ed25519.PrivateKey
	collectorKey *ecdh.PrivateKey
}

func newElasticCleanupFixture(t *testing.T, reconcile ...bool) elasticCleanupFixture {
	t.Helper()
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(signer.Seed()))
	server, durable := newTimeslotServer(t)
	if server.appDynamicsCancel != nil {
		server.appDynamicsCancel()
	}
	server.runner.projectSecretStore = &devSecretStore{value: "cleanup-sensitive-api-key"}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = durable.SaveProject(&storage.Project{ID: "proj-b", Name: "Other project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []storage.MonitoringCollector{
		{ID: "cleanup-collector", ProjectID: "proj-a", TokenHash: hashAPIKey("cleanup-token")},
		{ID: "other-collector", ProjectID: "proj-a", TokenHash: hashAPIKey("other-token")},
		{ID: "foreign-collector", ProjectID: "proj-b", TokenHash: hashAPIKey("foreign-token")},
	} {
		c.Name, c.Health, c.LastSeenAt = c.ID, "healthy", &now
		c.AllowedMetricPacks = []string{"elastic"}
		if err = durable.SaveMonitoringCollector(&c); err != nil {
			t.Fatal(err)
		}
	}
	store := durable.(providerMonitorStore)
	connection := &storage.ELKConnection{ID: "cleanup-connection", ProjectID: "proj-a", Name: "Cleanup connection", Enabled: true, CollectionMode: "collector", CollectorAgentID: "cleanup-collector", Config: elastic.Config{Endpoint: "https://cleanup.example.com", Product: "elasticsearch", AuthType: "api_key"}, APIKeySecretName: "CLEANUP_API_KEY"}
	connections := durable.(storage.ElasticConnectionStore)
	if err = connections.CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	if ok, err := connections.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", elastic.Capabilities{Product: "elasticsearch", Version: "8.14.0", DSL: true, PIT: true, Logs: true}, now); err != nil || !ok {
		t.Fatal("connection verification", ok, err)
	}
	if err = store.SaveCollectorRunReadiness("proj-a", "cleanup-collector", []string{"elastic", collectoragent.ElasticBatchContract}, []string{connection.Config.Endpoint + "#" + base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())}, now); err != nil {
		t.Fatal(err)
	}
	source, err := store.SaveMonitorSource(storage.MonitorSource{ID: "cleanup-source", ProjectID: "proj-a", Name: "Cleanup logs", Pack: "elastic", Enabled: true, ExecutorMode: "collector", CollectorID: "cleanup-collector", ConnectionID: connection.ID, Elastic: &elastic.Selection{ProjectID: "proj-a", Indices: []string{"logs-*"}, Logs: true}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	started := now.Add(-10 * time.Second)
	run := &storage.TestRecord{ID: "cleanup-run", ProjectID: "proj-a", Name: "Cleanup run", Status: "RUNNING", StartTime: started, StartedAt: &started, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}}
	if err = durable.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors(run.ProjectID, run.ID, "", started, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].State != "collecting" {
		t.Fatalf("assigned job %+v %v", jobs, err)
	}
	until := now.Add(-5 * time.Second)
	cursor := &analysis.ProviderContinuation{QueryID: elastic.QuerySignalDefinitions(*source.Elastic)[0].QueryID, PITID: "owned-private-pit", WindowFrom: started, WindowUntil: until, KeepAliveUntil: now.Add(time.Minute)}
	batch := storage.ProviderMonitorBatch{QueryFromUTC: started, QueryUntilUTC: until, Progress: &analysis.ProviderProgress{TotalQueries: 1, Continuation: cursor}}
	if err = server.ingestElasticBatch(context.Background(), jobs[0], 0, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	if len(reconcile) > 0 && reconcile[0] {
		loadEnd := time.Now().UTC().Add(-time.Second)
		run.Status, run.EndTime = "COMPLETE", &loadEnd
		atomic := durable.(interface {
			SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
		})
		if _, err = atomic.SaveTestWithRunMonitors(run, "", "", true); err != nil {
			t.Fatal(err)
		}
	} else if err = store.StopRunMonitors(run.ProjectID, run.ID, until); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetRunMonitorJob(run.ProjectID, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return elasticCleanupFixture{server, store, durable.(elasticCleanupStore), *job, cursor, signer, key}
}

func (f elasticCleanupFixture) poll(t *testing.T) collectoragent.SignedRunJob {
	t.Helper()
	response := collectorRequest(t, f.server, http.MethodGet, "/api/v1/monitoring/collectors/cleanup-collector/elastic-cleanups", "cleanup-token", nil)
	var envelopes []collectoragent.SignedRunJob
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelopes) != nil || len(envelopes) != 1 {
		t.Fatalf("poll cleanup: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "cleanup-sensitive-api-key") || strings.Contains(response.Body.String(), "CLEANUP_API_KEY") {
		t.Fatal("cleanup poll disclosed credentials")
	}
	return envelopes[0]
}

func (f elasticCleanupFixture) receipt(t *testing.T, envelope collectoragent.SignedRunJob, collector, token string) (int, bool) {
	t.Helper()
	response := collectorRequest(t, f.server, http.MethodPost, "/api/v1/monitoring/collectors/"+collector+"/jobs/"+f.job.ID+"/cleanup", token, ElasticCleanupReceipt{Job: envelope})
	var result struct {
		Accepted bool `json:"accepted"`
	}
	if response.Code == http.StatusOK && json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("invalid cleanup receipt response %s", response.Body.String())
	}
	return response.Code, result.Accepted
}

func TestElasticCleanupAssignedSignedGrantAndDisabledFeature(t *testing.T) {
	f := newElasticCleanupFixture(t)
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	if response := collectorRequest(t, f.server, http.MethodGet, "/api/v1/monitoring/collectors/cleanup-collector/jobs", "cleanup-token", nil); response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("stopped run received collection: %d %s", response.Code, response.Body.String())
	}
	envelope := f.poll(t)
	grant, err := collectoragent.VerifyRunJob(envelope, f.signer.Public().(ed25519.PublicKey), "cleanup-collector", time.Now().UTC())
	if err != nil || !grant.Cleanup || grant.SourceID != f.job.Source.ID || grant.Elastic == nil || grant.Elastic.Continuation.PITID != f.cursor.PITID {
		t.Fatalf("signed cleanup grant %+v %v", grant, err)
	}
	credentials, err := collectoragent.OpenCredentials(grant, f.collectorKey.Bytes(), time.Now().UTC())
	if err != nil || credentials.Elastic == nil || credentials.Elastic.APIKey != "cleanup-sensitive-api-key" {
		t.Fatal("cleanup credentials were not sealed for assigned collector", err)
	}
	if code, accepted := f.receipt(t, envelope, "cleanup-collector", "cleanup-token"); code != http.StatusOK || !accepted {
		t.Fatal("assigned cleanup rejected", code, accepted)
	}
	if code, accepted := f.receipt(t, envelope, "cleanup-collector", "cleanup-token"); code != http.StatusOK || accepted {
		t.Fatal("cleanup retry cleared a second cursor", code, accepted)
	}
	if jobs, _, err := f.cleanup.ListElasticContinuationCleanupJobs(f.job.ProjectID, f.job.RunID, "", 32); err != nil || len(jobs) != 0 {
		t.Fatalf("acknowledged cursor still queued %+v %v", jobs, err)
	}
}

func TestElasticCleanupRejectsCollectorProjectAndSourceSubstitution(t *testing.T) {
	f := newElasticCleanupFixture(t)
	envelope := f.poll(t)
	for _, wrong := range []struct{ collector, token string }{{"other-collector", "other-token"}, {"foreign-collector", "foreign-token"}, {"cleanup-collector", "incorrect-token"}} {
		code, accepted := f.receipt(t, envelope, wrong.collector, wrong.token)
		if code != http.StatusForbidden && code != http.StatusUnauthorized || accepted {
			t.Fatal("different collector authorized cleanup", wrong.collector, code, accepted)
		}
		poll := collectorRequest(t, f.server, http.MethodGet, "/api/v1/monitoring/collectors/"+wrong.collector+"/elastic-cleanups", wrong.token, nil)
		if wrong.token != "incorrect-token" && (poll.Code != http.StatusOK || strings.TrimSpace(poll.Body.String()) != "[]") {
			t.Fatalf("cleanup leaked to another collector/project %d %s", poll.Code, poll.Body.String())
		}
	}
	for name, mutate := range map[string]func(*collectoragent.RunJob){
		"source":   func(g *collectoragent.RunJob) { g.SourceID = "other-source" },
		"revision": func(g *collectoragent.RunJob) { g.SourceRevision++ },
		"project":  func(g *collectoragent.RunJob) { g.ProjectID = "proj-b" },
		"run":      func(g *collectoragent.RunJob) { g.RunID = "other-run" },
		"endpoint": func(g *collectoragent.RunJob) { g.Elastic.Config.Endpoint = "https://different.example.com" },
		"expired":  func(g *collectoragent.RunJob) { g.LeaseExpiresAt = time.Now().UTC().Add(-time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			var grant collectoragent.RunJob
			if err := json.Unmarshal([]byte(envelope.Payload), &grant); err != nil {
				t.Fatal(err)
			}
			mutate(&grant)
			changed, err := collectoragent.SignRunJob(grant, f.signer)
			if err != nil {
				t.Fatal(err)
			}
			if code, accepted := f.receipt(t, changed, "cleanup-collector", "cleanup-token"); code != http.StatusForbidden || accepted {
				t.Fatal("substituted cleanup grant accepted", code, accepted)
			}
		})
	}
	job, err := f.store.GetRunMonitorJob(f.job.ProjectID, f.job.ID)
	if err != nil || job.ElasticContinuation == nil || job.ElasticContinuation.PITID != f.cursor.PITID {
		t.Fatal("rejected grant altered durable checkpoint", err)
	}
}

func TestElasticCleanupStaleHandleAndSortVectorCannotClearCheckpoint(t *testing.T) {
	f := newElasticCleanupFixture(t)
	envelope := f.poll(t)
	for _, change := range []func(*analysis.ProviderContinuation){
		func(c *analysis.ProviderContinuation) { c.PITID = "replacement-private-pit" },
		func(c *analysis.ProviderContinuation) {
			c.SearchAfter = []analysis.ScalarValue{{Kind: "integer", StringValue: "123"}}
		},
	} {
		var grant collectoragent.RunJob
		if err := json.Unmarshal([]byte(envelope.Payload), &grant); err != nil {
			t.Fatal(err)
		}
		change(grant.Elastic.Continuation)
		changed, err := collectoragent.SignRunJob(grant, f.signer)
		if err != nil {
			t.Fatal(err)
		}
		if code, accepted := f.receipt(t, changed, "cleanup-collector", "cleanup-token"); code != http.StatusOK || accepted {
			t.Fatal("different private checkpoint cleared", code, accepted)
		}
	}
	if job, err := f.store.GetRunMonitorJob(f.job.ProjectID, f.job.ID); err != nil || job.ElasticContinuation == nil || job.ElasticContinuation.PITID != f.cursor.PITID || len(job.ElasticContinuation.SearchAfter) != 0 {
		t.Fatal("stale cursor acknowledgement changed canonical checkpoint", err)
	}
	if code, accepted := f.receipt(t, envelope, "cleanup-collector", "cleanup-token"); code != http.StatusOK || !accepted {
		t.Fatal("matching cursor failed after stale receipt", code, accepted)
	}
}

func TestElasticCleanupRetiredGrantCannotClearChangedCurrentCursor(t *testing.T) {
	f := newElasticCleanupFixture(t, true)
	envelope := f.poll(t)
	current := *f.cursor
	current.PITID = "new-current-reconciliation-pit"
	current.WindowUntil = *f.job.LoadEndedAt
	batch := storage.ProviderMonitorBatch{QueryFromUTC: f.job.StartedAt, QueryUntilUTC: current.WindowUntil, Progress: &analysis.ProviderProgress{TotalQueries: 1, Continuation: &current}}
	if err := f.server.ingestElasticBatch(context.Background(), f.job, 1, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	if code, accepted := f.receipt(t, envelope, "cleanup-collector", "cleanup-token"); code != http.StatusOK || !accepted {
		t.Fatal("retired handle could not be acknowledged", code, accepted)
	}
	if code, accepted := f.receipt(t, envelope, "cleanup-collector", "cleanup-token"); code != http.StatusOK || accepted {
		t.Fatal("retired grant replay cleared changed current handle", code, accepted)
	}
	var grant collectoragent.RunJob
	if err := json.Unmarshal([]byte(envelope.Payload), &grant); err != nil {
		t.Fatal(err)
	}
	grant.Elastic.Continuation = &current
	changed, err := collectoragent.SignRunJob(grant, f.signer)
	if err != nil {
		t.Fatal(err)
	}
	if code, accepted := f.receipt(t, changed, "cleanup-collector", "cleanup-token"); code != http.StatusOK || accepted {
		t.Fatal("active current cursor was cleanup eligible", code, accepted)
	}
	job, err := f.store.GetRunMonitorJob(f.job.ProjectID, f.job.ID)
	if err != nil || job.ElasticContinuation == nil || job.ElasticContinuation.PITID != current.PITID || len(job.ElasticRetiredContinuations) != 0 {
		t.Fatal("retired cleanup altered current pagination", err)
	}
}

func TestElasticDirectRetiredCleanupPreservesCurrentReconciliationCursor(t *testing.T) {
	server, store, job, _, evidence := apiElasticMonitorFixture(t)
	cleanup := server.store.(elasticCleanupStore)
	until := job.StartedAt.Add(time.Minute)
	old := &analysis.ProviderContinuation{QueryID: evidence.QueryID, PITID: "retired-direct-pit", WindowFrom: job.StartedAt, WindowUntil: until}
	batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: until, Progress: &analysis.ProviderProgress{TotalQueries: 2, NextQuery: 1, Continuation: old}}
	if err := server.ingestElasticBatch(context.Background(), job, 0, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	run, err := server.store.GetTest(job.RunID)
	if err != nil {
		t.Fatal(err)
	}
	loadEnd := time.Now().UTC().Add(-2 * time.Second)
	run.Status, run.EndTime = "COMPLETE", &loadEnd
	atomic := server.store.(interface {
		SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
	})
	if _, err = atomic.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	reconciling, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := *old
	current.PITID = "current-reconciliation-pit"
	current.WindowUntil = loadEnd
	batch.QueryUntilUTC = loadEnd
	batch.Progress.Continuation = &current
	if err = server.ingestElasticBatch(context.Background(), *reconciling, 1, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	listed, _, err := cleanup.ListElasticContinuationCleanupJobs(job.ProjectID, job.RunID, "", 32)
	if err != nil || len(listed) != 1 || listed[0].Source.ExecutorMode != "direct" || listed[0].ElasticContinuation.PITID != old.PITID {
		t.Fatalf("direct retired cleanup not discoverable %+v %v", listed, err)
	}
	if cleared, err := cleanup.ClearElasticContinuation(job.ProjectID, job.ID, old, "", "", 0); err != nil || !cleared {
		t.Fatal("retired direct cursor not cleared", cleared, err)
	}
	retained, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil || retained.ElasticContinuation == nil || retained.ElasticContinuation.PITID != current.PITID || len(retained.ElasticRetiredContinuations) != 0 {
		t.Fatal("direct cleanup removed newer reconciliation cursor", err)
	}
	if cleared, err := cleanup.ClearElasticContinuation(job.ProjectID, job.ID, &current, "", "", 0); err != nil || cleared {
		t.Fatal("active current cursor was eligible for cleanup", cleared, err)
	}
}

func TestElasticDirectCleanupFailureRetainsUnknownExpiry(t *testing.T) {
	server, store, job, _, evidence := apiElasticMonitorFixture(t)
	server.runner.projectSecretStore = &devSecretStore{value: "cleanup-key"}
	cleanup := server.store.(elasticCleanupStore)
	until := job.StartedAt.Add(time.Minute)
	cursor := &analysis.ProviderContinuation{QueryID: evidence.QueryID, PITID: "owned-unknown-expiry", WindowFrom: job.StartedAt, WindowUntil: until}
	batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: until, Progress: &analysis.ProviderProgress{TotalQueries: 2, NextQuery: 1, Continuation: cursor}}
	if err := server.ingestElasticBatch(context.Background(), job, 0, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	if err := store.StopRunMonitors(job.ProjectID, job.RunID, until); err != nil {
		t.Fatal(err)
	}
	listed, _, err := cleanup.ListElasticContinuationCleanupJobs(job.ProjectID, job.RunID, "", 32)
	if err != nil || len(listed) != 1 {
		t.Fatalf("cleanup queue %+v %v", listed, err)
	}
	// The fixture endpoint is unreachable. A failed bounded close must retain
	// ownership when the vendor handle's expiry is unknown.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	server.closeDirectElasticContinuation(ctx, cleanup, listed[0], true)
	retained, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil || retained.ElasticContinuation == nil || retained.ElasticContinuation.PITID != cursor.PITID {
		t.Fatal("failed close erased an unknown-expiry owned handle", err)
	}
	queued, _, err := cleanup.ListElasticContinuationCleanupJobs(job.ProjectID, job.RunID, "", 32)
	if err != nil || len(queued) != 1 {
		t.Fatal("failed cleanup lost its durable retry", err)
	}
}

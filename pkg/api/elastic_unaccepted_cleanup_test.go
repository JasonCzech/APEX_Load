package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestUnacceptedDirectElasticResultRetainsFailedCleanupPrivately(t *testing.T) {
	for _, reason := range []string{"ingestion_failure", "execution_term_lost", "malformed_selection"} {
		t.Run(reason, func(t *testing.T) {
			server, provider, job, point, evidence := apiElasticMonitorFixture(t)
			inspections := server.store.(storage.ElasticInspectionStore)
			before, err := provider.GetRunMonitorJob(job.ProjectID, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			until := job.StartedAt.Add(time.Minute)
			owned := &analysis.ProviderContinuation{QueryID: point.Provider.QueryID, PITID: "unaccepted-private-pit", WindowFrom: job.StartedAt, WindowUntil: until, KeepAliveUntil: time.Now().UTC().Add(2 * time.Minute)}
			async := *owned
			async.PITID, async.AsyncID = "", "unaccepted-private-async"
			result := elastic.Result{Points: []analysis.MetricPoint{point}, Evidence: []analysis.ProviderEvidence{evidence}, Continuation: owned, CleanupContinuations: []*analysis.ProviderContinuation{&async}}
			if reason == "ingestion_failure" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: until, Points: result.Points, Evidence: result.Evidence}
				if err = server.ingestExpandedElasticBatch(ctx, job, job.NextSequence, batch, executionLeaseTerm{}); err == nil {
					t.Fatal("failed handoff accepted")
				}
			} else if reason == "execution_term_lost" {
				server.runner.durableExecutionFence = true
				if server.runner.executionLeaseTermCurrent(executionLeaseTerm{ownerID: "replaced-owner", generation: 1}) {
					t.Fatal("fixture term remained current")
				}
			}
			if reason == "malformed_selection" {
				job.Source.Elastic.Queries = []elastic.Query{{DSL: json.RawMessage(`{`)}}
			}
			var durableBeforeClose atomic.Int32
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					t.Error("unaccepted result performed a read")
				}
				pending, e := inspections.ListElasticInspectionCleanup(job.ProjectID, "", 2)
				if e == nil && len(pending) == 1 && len(pending[0].CleanupContinuations) == 2 {
					durableBeforeClose.Add(1)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer upstream.Close()
			config := job.ElasticConnectionSnapshot.Config
			config.Endpoint = upstream.URL
			client, err := elastic.NewClientWithHTTPClient(config, elastic.Credentials{APIKey: "fixture-cleanup-key"}, upstream.Client())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			server.finishUnacceptedDirectElasticResult(client, job, result)
			if durableBeforeClose.Load() != 2 {
				t.Fatal("cleanup not durable before close")
			}
			pending, err := inspections.ListElasticInspectionCleanup(job.ProjectID, "", 2)
			if err != nil || len(pending) != 1 || len(pending[0].CleanupContinuations) != 2 || pending[0].State != "complete" || pending[0].ResultJSON != "{}" {
				t.Fatal("cleanup not recoverable", pending, err)
			}
			var immutable elasticInspectionWork
			if json.Unmarshal([]byte(pending[0].PayloadJSON), &immutable) != nil || immutable.Connection.Config.Endpoint != job.ElasticConnectionSnapshot.Config.Endpoint || immutable.Connection.APIKeySecretName != "protected-key-reference" || immutable.Connection.Revision != job.ElasticConnectionSnapshot.Revision {
				t.Fatal("immutable references lost")
			}
			visible, _ := json.Marshal(elasticInspectionResponse(pending[0]))
			for _, private := range []string{owned.PITID, async.AsyncID, "protected-key-reference", "coverage", "points"} {
				if strings.Contains(string(visible), private) {
					t.Fatal("private or unaccepted data published", private)
				}
			}
			after, err := provider.GetRunMonitorJob(job.ProjectID, job.ID)
			beforeJSON, _ := json.Marshal(before)
			afterJSON, _ := json.Marshal(after)
			if err != nil || string(beforeJSON) != string(afterJSON) {
				t.Fatal("run checkpoint advanced", err)
			}
			if receipts, err := provider.ListMonitorBatchReceipts(job.ProjectID, job.RunID, "", -1, 10); err != nil || len(receipts) != 0 {
				t.Fatal("data receipt published", receipts, err)
			}
		})
	}
}

func TestUnacceptedDirectElasticCleanupAcknowledgementClearsOwnership(t *testing.T) {
	server, _, job, point, _ := apiElasticMonitorFixture(t)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"succeeded":true}`))
	}))
	defer upstream.Close()
	config := job.ElasticConnectionSnapshot.Config
	config.Endpoint = upstream.URL
	client, err := elastic.NewClientWithHTTPClient(config, elastic.Credentials{APIKey: "acknowledged-fixture-key"}, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	owned := &analysis.ProviderContinuation{QueryID: point.Provider.QueryID, PITID: "closed-unaccepted-pit", WindowFrom: job.StartedAt, WindowUntil: job.StartedAt.Add(time.Minute), KeepAliveUntil: time.Now().UTC().Add(time.Minute)}
	server.finishUnacceptedDirectElasticResult(client, job, elastic.Result{Continuation: owned, CleanupContinuations: []*analysis.ProviderContinuation{owned}})
	if pending, err := server.store.(storage.ElasticInspectionStore).ListElasticInspectionCleanup(job.ProjectID, "", 2); err != nil || len(pending) != 0 {
		t.Fatal("acknowledged ownership remains", pending, err)
	}
}

type failingElasticCleanupStore struct {
	*storage.SQLiteStore
	stage   string
	fail    atomic.Bool
	creates atomic.Int32
}

func (s *failingElasticCleanupStore) CreateElasticInspection(op *storage.ElasticInspection) error {
	s.creates.Add(1)
	if s.stage == "create" && s.fail.Load() {
		return errors.New("temporary private storage outage")
	}
	return s.SQLiteStore.CreateElasticInspection(op)
}

func (s *failingElasticCleanupStore) RetainElasticInspectionCleanup(id, owner string, owned []*analysis.ProviderContinuation, now time.Time) (bool, error) {
	if s.stage == "retain" && s.fail.Load() {
		return false, errors.New("temporary private storage outage")
	}
	return s.SQLiteStore.RetainElasticInspectionCleanup(id, owner, owned, now)
}

func unacceptedElasticOutageClient(t *testing.T, job storage.RunMonitorJob) *elastic.Client {
	t.Helper()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Error("cleanup performed new upstream collection")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)
	config := job.ElasticConnectionSnapshot.Config
	config.Endpoint = upstream.URL
	client, err := elastic.NewClientWithHTTPClient(config, elastic.Credentials{APIKey: "private-outage-fixture-key"}, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestUnacceptedElasticCleanupSurvivesStorageAndDeleteOutage(t *testing.T) {
	for _, tc := range []struct{ path, stage string }{{"run", "create"}, {"run", "retain"}, {"inspection", "retain"}, {"preparation", "retain"}} {
		t.Run(tc.path+"_"+tc.stage, func(t *testing.T) {
			server, provider, job, point, _ := apiElasticMonitorFixture(t)
			store := &failingElasticCleanupStore{SQLiteStore: server.store.(*storage.SQLiteStore), stage: tc.stage}
			server.store = store
			client := unacceptedElasticOutageClient(t, job)
			owned := &analysis.ProviderContinuation{QueryID: point.Provider.QueryID, PITID: "outage-owned-private-pit", WindowFrom: job.StartedAt, WindowUntil: job.StartedAt.Add(time.Minute), KeepAliveUntil: time.Now().UTC().Add(time.Minute)}
			key, scope := job.ID, elasticCleanupRun
			var op *storage.ElasticInspection
			var owner string
			if tc.path != "run" {
				var err error
				op, owner, err = beginPreparedElasticInspection(context.Background(), store, job.Source, job.ElasticConnectionSnapshot)
				if err != nil {
					t.Fatal(err)
				}
				key, scope = op.ID, elasticCleanupInspection
			}
			store.fail.Store(true)
			if tc.path == "run" {
				server.finishUnacceptedDirectElasticResult(client, job, elastic.Result{Continuation: owned})
			} else {
				if tc.path == "preparation" {
					if err := completePreparedElasticInspection(store, op, owner, collectoragent.ElasticInspectionData{CleanupContinuations: []*analysis.ProviderContinuation{owned}}, nil, time.Now().UTC()); err == nil {
						t.Fatal("failed private receipt published verification")
					}
				}
				server.finishUnacceptedElasticInspectionCleanup(client, store, op, owner, []*analysis.ProviderContinuation{owned})
			}
			if !server.hasUnacceptedElasticCleanup(scope, key) {
				t.Fatal("simultaneous outage dropped private ownership")
			}
			if tc.path == "run" {
				// Per-job backpressure prevents any second upstream context and
				// keeps the existing owner's bounded queue and run receipt intact.
				server.collectDirectExpandedElastic(context.Background(), job)
				if receipts, err := provider.ListMonitorBatchReceipts(job.ProjectID, job.RunID, "", -1, 10); err != nil || len(receipts) != 0 {
					t.Fatal("collection bypassed pending cleanup ownership", receipts, err)
				}
			}
			store.fail.Store(false)
			deadline := time.Now().Add(3 * time.Second)
			var pending []*storage.ElasticInspection
			for time.Now().Before(deadline) {
				pending, _ = store.ListElasticInspectionCleanup(job.ProjectID, "", 2)
				if len(pending) == 1 && !server.hasUnacceptedElasticCleanup(scope, key) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if len(pending) != 1 || server.hasUnacceptedElasticCleanup(scope, key) || len(pending[0].CleanupContinuations) != 1 || pending[0].CleanupContinuations[0].PITID != owned.PITID {
				t.Fatal("restored storage did not recover exact owned context", pending)
			}
			var work elasticInspectionWork
			if json.Unmarshal([]byte(pending[0].PayloadJSON), &work) != nil || work.Connection.Config.Endpoint != job.ElasticConnectionSnapshot.Config.Endpoint || work.Connection.APIKeySecretName != "protected-key-reference" {
				t.Fatal("retry changed immutable private configuration")
			}
			public, _ := json.Marshal(elasticInspectionResponse(pending[0]))
			if strings.Contains(string(public), owned.PITID) || strings.Contains(string(public), "protected-key-reference") {
				t.Fatal("retry disclosed private ownership")
			}
			if tc.stage == "retain" && store.creates.Load() != 1 {
				t.Fatal("retry created another operation for the same owner", store.creates.Load())
			}
			if current, err := provider.GetRunMonitorJob(job.ProjectID, job.ID); err != nil || current.NextSequence != job.NextSequence {
				t.Fatal("unaccepted retry advanced run checkpoint", current, err)
			}
		})
	}
}

func TestUnacceptedElasticCleanupHonorsKnownExpiryAndShutdown(t *testing.T) {
	for _, mode := range []string{"expiry", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			server, _, job, point, _ := apiElasticMonitorFixture(t)
			store := &failingElasticCleanupStore{SQLiteStore: server.store.(*storage.SQLiteStore), stage: "retain"}
			store.fail.Store(true)
			server.store = store
			client := unacceptedElasticOutageClient(t, job)
			expiry := time.Now().UTC().Add(time.Second)
			if mode == "expiry" {
				expiry = time.Now().UTC().Add(100 * time.Millisecond)
			}
			owned := &analysis.ProviderContinuation{QueryID: point.Provider.QueryID, PITID: "bounded-outage-private-pit", WindowFrom: job.StartedAt, WindowUntil: job.StartedAt.Add(time.Minute), KeepAliveUntil: expiry}
			server.finishUnacceptedDirectElasticResult(client, job, elastic.Result{Continuation: owned})
			if mode == "shutdown" {
				server.cancelServerStartup()
				value, present := elasticUnacceptedCleanupPending.Load(elasticUnacceptedCleanupKey{server: server, scope: elasticCleanupRun, jobID: job.ID})
				if !present {
					t.Fatal("cleanup owner disappeared before shutdown")
				}
				task := value.(*elasticUnacceptedCleanupTask)
				deadline := time.Now().Add(500 * time.Millisecond)
				for task.running.Load() && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if task.running.Load() || !server.hasUnacceptedElasticCleanup(elasticCleanupRun, job.ID) {
					t.Fatal("shutdown delayed retry worker or discarded live ownership")
				}
			}
			deadline := time.Now().Add(2 * time.Second)
			for server.hasUnacceptedElasticCleanup(elasticCleanupRun, job.ID) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if server.hasUnacceptedElasticCleanup(elasticCleanupRun, job.ID) || time.Now().UTC().Before(expiry) {
				t.Fatal("cleanup released before known expiry or failed to stop after it")
			}
		})
	}
}

func TestUnacceptedElasticCleanupKeepsOverlappingInspectionOwners(t *testing.T) {
	server, _, job, _, _ := apiElasticMonitorFixture(t)
	store := &failingElasticCleanupStore{SQLiteStore: server.store.(*storage.SQLiteStore), stage: "create"}
	store.fail.Store(true)
	server.store = store
	privateJob := storage.RunMonitorJob{ID: "overlapping-inspection", ProjectID: job.ProjectID, Source: storage.MonitorSource{ConnectionID: job.Source.ConnectionID}, ElasticConnectionSnapshot: job.ElasticConnectionSnapshot}
	key := elasticUnacceptedCleanupKey{server: server, scope: elasticCleanupInspection, jobID: privateJob.ID}
	now := time.Now().UTC()
	for _, handle := range []string{"first-owned-pit", "second-owned-pit"} {
		cursor := &analysis.ProviderContinuation{QueryID: "inspection-pit", PITID: handle, WindowFrom: now, WindowUntil: now.Add(time.Nanosecond), KeepAliveUntil: now.Add(time.Minute)}
		client := unacceptedElasticOutageClient(t, job)
		server.finishOwnedElasticCleanup(key, &elasticUnacceptedCleanupTask{store: store, job: privateJob, client: client, owned: []*analysis.ProviderContinuation{cursor}})
	}
	if !server.hasUnacceptedElasticCleanup(elasticCleanupInspection, privateJob.ID) {
		t.Fatal("overlapping private owners did not block reassignment")
	}
	owners := map[string]bool{"first-owned-pit": true, "second-owned-pit": true}
	taskCount := 0
	elasticUnacceptedCleanupPending.Range(func(entry, _ any) bool {
		candidate := entry.(elasticUnacceptedCleanupKey)
		if candidate.server == server && candidate.scope == elasticCleanupInspection && candidate.jobID == privateJob.ID {
			taskCount++
		}
		return true
	})
	if taskCount != 2 {
		t.Fatal("one overlapping private owner was discarded", taskCount)
	}
	store.fail.Store(false)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := store.ListElasticInspectionCleanup(job.ProjectID, "", 10)
		if err == nil && len(pending) == 2 && !server.hasUnacceptedElasticCleanup(elasticCleanupInspection, privateJob.ID) {
			for _, op := range pending {
				for _, cursor := range op.CleanupContinuations {
					delete(owners, cursor.PITID)
				}
			}
			if len(owners) != 0 {
				t.Fatal("an overlapping private owner was not retained", owners)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("overlapping private cleanup was not durably retained")
}

func TestStaleInspectionFallbackDoesNotCancelReplacementClaim(t *testing.T) {
	server, _, job, _, _ := apiElasticMonitorFixture(t)
	store := server.store.(storage.ElasticInspectionStore)
	now := time.Now().UTC()
	work, err := json.Marshal(elasticInspectionWork{Connection: job.ElasticConnectionSnapshot, Request: ElasticInspectionRequest{Kind: "verify"}})
	if err != nil {
		t.Fatal(err)
	}
	op := &storage.ElasticInspection{ID: "replacement-claim", ProjectID: job.ProjectID, ConnectionID: job.Source.ConnectionID, ConnectionRevision: job.ElasticConnectionSnapshot.Revision, Kind: "verify", PayloadJSON: string(work), CreatedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(10 * time.Minute)}
	if err = store.CreateElasticInspection(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimElasticInspection(op.ID, "old-owner", now.Add(-90*time.Second), now.Add(-time.Minute)); err != nil || !ok {
		t.Fatal("first claim", ok, err)
	}
	if ok, err := store.ClaimElasticInspection(op.ID, "replacement-owner", now, now.Add(time.Minute)); err != nil || !ok {
		t.Fatal("replacement claim", ok, err)
	}
	client := unacceptedElasticOutageClient(t, job)
	owned := &analysis.ProviderContinuation{QueryID: "inspection-pit", PITID: "old-owner-private-pit", WindowFrom: now, WindowUntil: now.Add(time.Nanosecond), KeepAliveUntil: now.Add(time.Minute)}
	server.finishUnacceptedElasticInspectionCleanup(client, store, op, "old-owner", []*analysis.ProviderContinuation{owned})
	current, err := store.GetElasticInspection(op.ID)
	if err != nil || current.State != "running" || current.OwnerToken != "replacement-owner" {
		t.Fatal("stale fallback cancelled replacement", current, err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := store.ListElasticInspectionCleanup(job.ProjectID, "", 10)
		if err == nil && len(pending) == 1 && pending[0].ID != op.ID && len(pending[0].CleanupContinuations) == 1 && pending[0].CleanupContinuations[0].PITID == owned.PITID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stale executor did not retain its own private handle")
}

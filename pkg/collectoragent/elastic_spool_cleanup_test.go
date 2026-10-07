package collectoragent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
)

type elasticSpoolCleanupFixture struct {
	agent   *Agent
	job     RunJob
	cursor  *analysis.ProviderContinuation
	signer  ed25519.PrivateKey
	decrypt *ecdh.PrivateKey
	status  int
	closed  []string
}

func TestElasticCleanupOnlyHandlesSurviveSpoolRestartAndReconciliation(t *testing.T) {
	f := newElasticSpoolCleanupFixture(t)
	second := *f.cursor
	second.PITID = "second-completed-pit"
	last := f.agent.providerBacklog[f.job.ID][0]
	last.Progress = &analysis.ProviderProgress{TotalQueries: 1, NextQuery: 1, Complete: true}
	last.Coverage["logs"] = analysis.ProviderCoverage{Status: "complete"}
	last.CleanupContinuations = []*analysis.ProviderContinuation{f.cursor, &second}
	if err := f.agent.saveSpool(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(f.agent.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.loadSpool(); err != nil {
		t.Fatal(err)
	}
	f.installClient(t, restarted)
	if !restarted.elasticSpoolNeedsCleanup(f.job.ID, time.Now().UTC()) {
		t.Fatal("completed query coverage hid outstanding upstream ownership")
	}
	job, handles, owned := restarted.elasticSpoolContinuations(f.job.ID)
	if !owned || len(handles) != 2 || job.ID != f.job.ID {
		t.Fatalf("private cleanup spool lost a handle: %+v %v", handles, owned)
	}
	current := f.job
	current.Reconciling = true
	current.QueryUntilUTC = current.QueryUntilUTC.Add(time.Second)
	if err := restarted.retireObsoleteElasticSpool(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	if len(f.closed) != 2 || f.closed[0] != f.cursor.PITID || f.closed[1] != second.PITID || restarted.latestElasticSpoolBatch(f.job.ID) != nil {
		t.Fatalf("reconciliation discarded a handle before cleanup: closed=%v pending=%v", f.closed, restarted.latestElasticSpoolBatch(f.job.ID))
	}
}

func newElasticSpoolCleanupFixture(t *testing.T) *elasticSpoolCleanupFixture {
	t.Helper()
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
	now := time.Now().UTC()
	f := &elasticSpoolCleanupFixture{signer: private, decrypt: decrypt, status: http.StatusOK}
	f.job = RunJob{ID: "job", ProjectID: "project", RunID: "run", SourceID: "source", SourceRevision: 1, CollectorID: "collector", Pack: "elastic", Destination: "https://search.example.com", IntervalSeconds: 10, StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), MaxBatches: 1, QueryFromUTC: now.Add(-time.Minute), QueryUntilUTC: now.Add(-10 * time.Second), Elastic: &elastic.JobConfig{Config: elastic.Config{Endpoint: "https://search.example.com", Product: "elasticsearch", AuthType: "basic"}, Selection: elastic.Selection{ProjectID: "project", Indices: []string{"logs-*"}, Logs: true}.Defaults()}}
	f.cursor = &analysis.ProviderContinuation{QueryID: elastic.QueryDefinitions(f.job.Elastic.Selection)[0].QueryID, PITID: "latest-unaccepted-pit", WindowFrom: f.job.QueryFromUTC, WindowUntil: f.job.QueryUntilUTC}
	controller := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || !strings.Contains(req.URL.Path, "/jobs") {
			t.Errorf("unexpected cancelled job controller request %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`[]`)), Request: req}, nil
	})}
	var err error
	f.agent, err = New(Config{ControllerURL: "http://localhost:8080", CollectorID: f.job.CollectorID, Token: "token", HTTPClient: controller, Packs: []string{"elastic"}, ControllerPublicKey: public, CredentialPrivateKey: decrypt.Bytes(), SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: "elastic", Destination: f.job.Destination, Indices: []string{"logs-*"}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.installClient(t, f.agent)
	f.queue(t)
	return f
}

func (f *elasticSpoolCleanupFixture) installClient(t *testing.T, agent *Agent) {
	t.Helper()
	agent.elasticClient = func(cfg elastic.Config, credentials elastic.Credentials) (*elastic.Client, error) {
		client := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodDelete || req.URL.Path != "/_pit" {
				t.Errorf("cleanup expanded the read destination: %s %s", req.Method, req.URL.Path)
			}
			var body struct {
				ID string `json:"id"`
			}
			if json.NewDecoder(req.Body).Decode(&body) != nil {
				t.Error("invalid owned handle close")
			}
			f.closed = append(f.closed, body.ID)
			return &http.Response{StatusCode: f.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"succeeded":true}`)), Request: req}, nil
		})}
		return elastic.NewClientWithHTTPClient(cfg, credentials, client)
	}
}

func (f *elasticSpoolCleanupFixture) queue(t *testing.T) {
	t.Helper()
	var err error
	f.job.Credentials, err = SealCredentials(f.job, f.decrypt.PublicKey().Bytes(), ElasticCredentialBundle(elastic.Credentials{Username: "operator", Password: "private-password"}))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := SignRunJob(f.job, f.signer)
	if err != nil {
		t.Fatal(err)
	}
	first := &RunSampleBatch{Job: envelope, QueryFromUTC: f.job.QueryFromUTC, QueryUntilUTC: f.job.QueryUntilUTC, Coverage: map[string]analysis.ProviderCoverage{"logs": {Status: "partial"}}}
	last := &RunSampleBatch{Job: envelope, Sequence: 1, QueryFromUTC: f.job.QueryFromUTC, QueryUntilUTC: f.job.QueryUntilUTC, Coverage: map[string]analysis.ProviderCoverage{"logs": {Status: "partial"}}, Progress: &analysis.ProviderProgress{TotalQueries: 1, Continuation: f.cursor}}
	if err = f.agent.admitMonitorRetry(f.job.ID, first, []*RunSampleBatch{last}); err != nil {
		t.Fatal(err)
	}
}

func (f *elasticSpoolCleanupFixture) restart(t *testing.T) *Agent {
	t.Helper()
	agent, err := New(f.agent.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = agent.loadSpool(); err != nil {
		t.Fatal(err)
	}
	f.installClient(t, agent)
	return agent
}

func TestElasticObsoleteSpoolRetainsLatestHandleUntilDurableCleanup(t *testing.T) {
	f := newElasticSpoolCleanupFixture(t)
	current := f.job
	current.Reconciling = true
	current.QueryUntilUTC = time.Now().UTC()
	f.status = http.StatusServiceUnavailable
	if err := f.agent.retireObsoleteElasticSpool(context.Background(), current); err == nil {
		t.Fatal("unknown-expiry close failure discarded the obsolete suffix")
	}
	restarted := f.restart(t)
	if len(restarted.pendingBatches) != 1 || len(restarted.providerBacklog[f.job.ID]) != 1 || len(f.closed) != 1 || f.closed[0] != f.cursor.PITID {
		t.Fatal("failed cleanup lost the latest privately owned cursor or spool")
	}
	f.status = http.StatusOK
	if err := restarted.retireObsoleteElasticSpool(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	if len(restarted.pendingBatches) != 0 || len(restarted.providerBacklog) != 0 || len(f.restart(t).pendingBatches) != 0 || len(f.closed) != 2 {
		t.Fatal("successful cleanup did not durably retire the old fixed window")
	}
}

func TestElasticObsoleteSpoolUsesOnlyMatchingRenewedCredentialLease(t *testing.T) {
	f := newElasticSpoolCleanupFixture(t)
	f.job.LeaseExpiresAt = time.Now().UTC().Add(-time.Second)
	f.queue(t)
	current := f.job
	current.Reconciling = true
	current.QueryUntilUTC = time.Now().UTC()
	current.LeaseExpiresAt = time.Now().UTC().Add(time.Minute)
	var err error
	current.Credentials, err = SealCredentials(current, f.decrypt.PublicKey().Bytes(), ElasticCredentialBundle(elastic.Credentials{Username: "operator", Password: "renewed-password"}))
	if err != nil {
		t.Fatal(err)
	}
	foreign := current
	foreign.SourceRevision++
	if err = f.agent.retireObsoleteElasticSpool(context.Background(), foreign); err == nil || len(f.closed) != 0 {
		t.Fatal("changed source identity authorized old handle cleanup")
	}
	if err = f.agent.retireObsoleteElasticSpool(context.Background(), current); err != nil || len(f.closed) != 1 || len(f.agent.pendingBatches) != 0 {
		t.Fatalf("same-source renewed cleanup failed: %v closed=%v", err, f.closed)
	}
}

func TestElasticCancelledSpoolRetainsFailureAcrossShutdownAndRestart(t *testing.T) {
	f := newElasticSpoolCleanupFixture(t)
	f.status = http.StatusServiceUnavailable
	if err := f.agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.agent.providerWorkers.Wait()
	if len(f.agent.pendingBatches) != 1 || len(f.closed) != 1 || len(f.restart(t).providerBacklog[f.job.ID]) != 1 {
		t.Fatal("cancelled unaccepted owned work was silently discarded")
	}
	// Shutdown closes the same latest owned handle without erasing retry data.
	f.status = http.StatusOK
	f.agent.closeElasticSpoolHandles()
	if len(f.closed) != 2 || f.closed[1] != f.cursor.PITID {
		t.Fatal("shutdown did not release the latest spooled PIT")
	}
	// A restart recognizes the already closed handle's 404 and removes the
	// obsolete evidence only after the bounded cleanup worker acknowledges it.
	restarted := f.restart(t)
	f.status = http.StatusNotFound
	if err := restarted.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted.providerWorkers.Wait()
	if len(restarted.pendingBatches) != 0 || len(restarted.providerBacklog) != 0 || len(f.restart(t).pendingBatches) != 0 || len(f.closed) != 3 {
		t.Fatal("closed cancelled spool remained after durable cleanup")
	}
}

func TestElasticExpiredLeasePreservesUnknownOwnedHandleUntilKnownExpiry(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "unexpired"}[known], func(t *testing.T) {
			f := newElasticSpoolCleanupFixture(t)
			now := time.Now().UTC()
			f.job.ExpiresAt = now.Add(-time.Second)
			f.job.LeaseExpiresAt = now.Add(-2 * time.Second)
			if known {
				f.cursor.KeepAliveUntil = now.Add(time.Minute)
			}
			f.queue(t)
			if err := f.agent.runJobTick(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.agent.providerWorkers.Wait()
			if len(f.agent.pendingBatches) != 1 || len(f.closed) != 0 || len(f.restart(t).pendingBatches) != 1 {
				t.Fatal("expired signed lease discarded unknown/unexpired work or decrypted credentials")
			}
			// Once the provider's known expiry elapses, no credentials or
			// remote request are required to release the bounded local spool.
			f.cursor.KeepAliveUntil = now.Add(-time.Second)
			f.queue(t)
			if err := f.agent.runJobTick(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(f.agent.pendingBatches) != 0 || len(f.closed) != 0 || len(f.restart(t).pendingBatches) != 0 {
				t.Fatal("known expired cleanup retained stale local ownership")
			}
		})
	}
}

func TestElasticProviderShutdownStopsNewAssignments(t *testing.T) {
	f := newElasticSpoolCleanupFixture(t)
	envelope, err := SignRunJob(f.job, f.signer)
	if err != nil {
		t.Fatal(err)
	}
	f.agent.stateMu.Lock()
	f.agent.providerClosing = true
	f.agent.dispatchElasticJob(context.Background(), f.job, envelope)
	f.agent.dispatchCancelledElasticSpoolCleanup(context.Background(), f.job.ID)
	f.agent.stateMu.Unlock()
	f.agent.providerWorkers.Wait()
	if len(f.agent.providerBusy) != 0 || len(f.agent.providerSlots) != 0 || len(f.closed) != 0 {
		t.Fatal("shutdown admitted new provider worker")
	}
	if f.agent.latestElasticSpoolBatch(f.job.ID) == nil {
		t.Fatal("shutdown lost owned handle cleanup state")
	}
}

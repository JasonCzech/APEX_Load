package api

import (
	"context"
	"crypto/ed25519"
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

func TestElasticInspectionReplacementRetainsCurrentCancel(t *testing.T) {
	id := t.Name()
	oldCtx, oldCancel := context.WithCancel(context.Background())
	defer oldCancel()
	newCtx, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	formerDone := registerElasticInspectionCancel(id, oldCancel)
	currentDone := registerElasticInspectionCancel(id, newCancel)
	formerDone()
	value, found := elasticInspectionCancels.Load(id)
	if !found {
		t.Fatal("former executor removed replacement cancel handle")
	}
	value.(*elasticInspectionCancelHandle).cancel()
	if newCtx.Err() != context.Canceled || oldCtx.Err() != nil {
		t.Fatal("replacement cancellation targeted the wrong executor")
	}
	currentDone()
	if _, found := elasticInspectionCancels.Load(id); found {
		t.Fatal("completed executor left a cancel handle")
	}
}

func TestElasticInspectionReceiptsRetainCleanupAndRetryWithoutDisclosure(t *testing.T) {
	f := newElasticCleanupFixture(t)
	store := f.server.store.(storage.ElasticInspectionStore)
	now := time.Now().UTC()
	work := elasticInspectionWork{Connection: f.job.ElasticConnectionSnapshot, CollectorPublicKey: base64.StdEncoding.EncodeToString(f.collectorKey.PublicKey().Bytes()), Request: ElasticInspectionRequest{Kind: "verify"}}
	payload, _ := json.Marshal(work)
	op := &storage.ElasticInspection{ID: "cleanup-inspection", ProjectID: f.job.ProjectID, ConnectionID: f.job.Source.ConnectionID, ConnectionRevision: f.job.ElasticConnectionSnapshot.Revision, CollectorID: "cleanup-collector", Kind: "verify", PayloadJSON: string(payload), CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err := store.CreateElasticInspection(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimElasticInspection(op.ID, "inspection-owner", now, now.Add(time.Minute)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	grant := collectoragent.ElasticInspectionJob{ID: op.ID, ProjectID: op.ProjectID, ConnectionID: op.ConnectionID, ConnectionRevision: op.ConnectionRevision, CollectorID: op.CollectorID, Kind: op.Kind, Config: elasticPolicyConfig(work.Connection.Config), OwnerToken: "inspection-owner", LeaseExpiresAt: now.Add(time.Minute)}
	envelope, err := collectoragent.SignElasticInspectionJob(grant, f.signer)
	if err != nil {
		t.Fatal(err)
	}
	owned := &analysis.ProviderContinuation{QueryID: "inspection-pit", PITID: "private-inspection-handle", WindowFrom: now, WindowUntil: now.Add(time.Nanosecond), KeepAliveUntil: now.Add(2 * time.Minute)}
	result := collectoragent.ElasticInspectionResult{Job: envelope, Data: collectoragent.ElasticInspectionData{Capabilities: elastic.Capabilities{Product: "elasticsearch"}}, CleanupContinuations: []*analysis.ProviderContinuation{owned}}
	path := "/api/v1/monitoring/collectors/cleanup-collector/elastic-inspections/" + op.ID + "/result"
	for range 2 {
		response := collectorRequest(t, f.server, http.MethodPost, path, "cleanup-token", result)
		if response.Code != http.StatusOK {
			t.Fatalf("inspection receipt: %d %s", response.Code, response.Body.String())
		}
	}
	current, err := store.GetElasticInspection(op.ID)
	if err != nil || current.State != "complete" || len(current.CleanupContinuations) != 1 {
		t.Fatalf("ownership lost: %+v %v", current, err)
	}
	visible, _ := json.Marshal(elasticInspectionResponse(current))
	if strings.Contains(string(visible), owned.PITID) || strings.Contains(string(visible), "inspection-owner") || strings.Contains(string(visible), "CLEANUP_API_KEY") {
		t.Fatal("inspection response disclosed private ownership")
	}
	result.Data.Indices = []elastic.Index{{Name: "conflicting-index"}}
	if response := collectorRequest(t, f.server, http.MethodPost, path, "cleanup-token", result); response.Code != http.StatusConflict {
		t.Fatalf("conflicting retry accepted: %d", response.Code)
	}
	poll := collectorRequest(t, f.server, http.MethodGet, "/api/v1/monitoring/collectors/cleanup-collector/elastic-inspections", "cleanup-token", nil)
	var jobs []collectoragent.SignedElasticInspectionJob
	if poll.Code != http.StatusOK || json.Unmarshal(poll.Body.Bytes(), &jobs) != nil || len(jobs) != 1 {
		t.Fatalf("cleanup assignment: %d %s", poll.Code, poll.Body.String())
	}
	cleanup, err := collectoragent.VerifyElasticInspectionJob(jobs[0], f.signer.Public().(ed25519.PublicKey), op.CollectorID, time.Now().UTC())
	if err != nil || cleanup.Kind != "cleanup" || len(cleanup.CleanupContinuations) != 1 {
		t.Fatal("cleanup grant", cleanup, err)
	}
	for range 2 {
		response := collectorRequest(t, f.server, http.MethodPost, path, "cleanup-token", collectoragent.ElasticInspectionResult{Job: jobs[0]})
		if response.Code != http.StatusOK {
			t.Fatalf("cleanup acknowledgement: %d %s", response.Code, response.Body.String())
		}
	}
	current, _ = store.GetElasticInspection(op.ID)
	if len(current.CleanupContinuations) != 0 || current.State != "complete" {
		t.Fatal("cleanup changed independent operation result")
	}
}

func TestElasticInspectionHistoricalReceiptKeepsSignatureAndTimeFence(t *testing.T) {
	f := newElasticCleanupFixture(t)
	now := time.Now().UTC()
	grant := collectoragent.ElasticInspectionJob{ID: "old-operation", ProjectID: f.job.ProjectID, ConnectionID: f.job.Source.ConnectionID, ConnectionRevision: f.job.ElasticConnectionSnapshot.Revision, CollectorID: "cleanup-collector", Kind: "verify", Config: elasticPolicyConfig(f.job.ElasticConnectionSnapshot.Config), OwnerToken: "old-owner", LeaseExpiresAt: now.Add(-time.Minute)}
	envelope, err := collectoragent.SignElasticInspectionJob(grant, f.signer)
	if err != nil {
		t.Fatal(err)
	}
	key := f.signer.Public().(ed25519.PublicKey)
	if _, err := verifyElasticInspectionReceipt(envelope, key, grant.CollectorID, now); err != nil {
		t.Fatal("historical retry rejected", err)
	}
	if _, err := verifyElasticInspectionReceipt(envelope, key, grant.CollectorID, now.Add(16*time.Minute)); err == nil {
		t.Fatal("unbounded historical retry accepted")
	}
	envelope.Payload = strings.Replace(envelope.Payload, "old-owner", "other-owner", 1)
	if _, err := verifyElasticInspectionReceipt(envelope, key, grant.CollectorID, now); err == nil {
		t.Fatal("modified receipt signature accepted")
	}
}

func TestElasticInspectionExpiredClaimAssignsCleanupBeforeRecovery(t *testing.T) {
	f := newElasticCleanupFixture(t)
	store := f.server.store.(storage.ElasticInspectionStore)
	now := time.Now().UTC()
	work := elasticInspectionWork{Connection: f.job.ElasticConnectionSnapshot, CollectorPublicKey: base64.StdEncoding.EncodeToString(f.collectorKey.PublicKey().Bytes()), Request: ElasticInspectionRequest{Kind: "verify"}}
	payload, _ := json.Marshal(work)
	op := &storage.ElasticInspection{ID: "expired-claim-cleanup", ProjectID: f.job.ProjectID, ConnectionID: f.job.Source.ConnectionID, ConnectionRevision: f.job.ElasticConnectionSnapshot.Revision, CollectorID: "cleanup-collector", Kind: "verify", PayloadJSON: string(payload), CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(10 * time.Minute)}
	if err := store.CreateElasticInspection(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimElasticInspection(op.ID, "original-owner", now.Add(-30*time.Second), now.Add(-time.Second)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	owned := &analysis.ProviderContinuation{QueryID: "inspection-pit", PITID: "private-expired-claim-handle", WindowFrom: now, WindowUntil: now.Add(time.Nanosecond), KeepAliveUntil: now.Add(time.Minute)}
	if ok, err := store.RetainElasticInspectionCleanup(op.ID, "original-owner", []*analysis.ProviderContinuation{owned}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	poll := collectorRequest(t, f.server, http.MethodGet, "/api/v1/monitoring/collectors/cleanup-collector/elastic-inspections", "cleanup-token", nil)
	var jobs []collectoragent.SignedElasticInspectionJob
	if poll.Code != http.StatusOK || json.Unmarshal(poll.Body.Bytes(), &jobs) != nil || len(jobs) != 1 {
		t.Fatalf("expired claim cleanup: %d %s", poll.Code, poll.Body.String())
	}
	grant, err := collectoragent.VerifyElasticInspectionJob(jobs[0], f.signer.Public().(ed25519.PublicKey), op.CollectorID, time.Now().UTC())
	if err != nil || grant.Kind != "cleanup" || grant.OwnerToken != "original-owner" || len(grant.CleanupContinuations) != 1 {
		t.Fatalf("inspection re-executed before cleanup: %+v %v", grant, err)
	}
	path := "/api/v1/monitoring/collectors/cleanup-collector/elastic-inspections/" + op.ID + "/result"
	if receipt := collectorRequest(t, f.server, http.MethodPost, path, "cleanup-token", collectoragent.ElasticInspectionResult{Job: jobs[0]}); receipt.Code != http.StatusOK {
		t.Fatalf("cleanup acknowledgement: %d %s", receipt.Code, receipt.Body.String())
	}
	current, err := store.GetElasticInspection(op.ID)
	if err != nil || current.State != "running" || current.OwnerToken != "original-owner" || len(current.CleanupContinuations) != 0 {
		t.Fatalf("cleanup changed inspection result or owner: %+v %v", current, err)
	}
	poll = collectorRequest(t, f.server, http.MethodGet, "/api/v1/monitoring/collectors/cleanup-collector/elastic-inspections", "cleanup-token", nil)
	jobs = nil
	if poll.Code != http.StatusOK || json.Unmarshal(poll.Body.Bytes(), &jobs) != nil || len(jobs) != 1 {
		t.Fatalf("claim did not resume after cleanup: %d %s", poll.Code, poll.Body.String())
	}
	grant, err = collectoragent.VerifyElasticInspectionJob(jobs[0], f.signer.Public().(ed25519.PublicKey), op.CollectorID, time.Now().UTC())
	if err != nil || grant.Kind != "verify" || grant.OwnerToken == "original-owner" {
		t.Fatalf("claim recovery was not fenced: %+v %v", grant, err)
	}
}

package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
)

func TestElasticInspectionPreviewFallbackRetainsFailedClosePrivately(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		expiry time.Duration
		retain bool
	}{
		{name: "released", status: http.StatusOK},
		{name: "already_gone", status: http.StatusNotFound},
		{name: "unknown_expiry_failure", status: http.StatusServiceUnavailable, retain: true},
		{name: "live_failure", status: http.StatusServiceUnavailable, expiry: time.Minute, retain: true},
		{name: "expired_failure", status: http.StatusServiceUnavailable, expiry: -time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newElasticSpoolCleanupFixture(t)
			f.status = tc.status
			client, err := f.agent.newElasticClient(f.job.Elastic.Config, elastic.Credentials{Username: "operator", Password: "private-password"})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			cursor := *f.cursor
			cursor.ResolvedIndices = []string{"logs-private"}
			cursor.SearchAfter = []analysis.ScalarValue{{Kind: "string", StringValue: "private-sort"}}
			if tc.expiry != 0 {
				cursor.KeepAliveUntil = time.Now().UTC().Add(tc.expiry)
			}
			owned := closeElasticInspectionPreviewCursor(client, &cursor)
			if (owned != nil) != tc.retain {
				t.Fatalf("retained=%v, want %v", owned != nil, tc.retain)
			}
			if owned == nil {
				return
			}
			if owned == &cursor || owned.PITID != cursor.PITID || owned.QueryID != cursor.QueryID || owned.KeepAliveUntil != cursor.KeepAliveUntil || !owned.WindowFrom.Equal(cursor.WindowFrom) || !owned.WindowUntil.Equal(cursor.WindowUntil) || len(owned.ResolvedIndices) != 0 || len(owned.SearchAfter) != 0 {
				t.Fatal("cleanup ownership lost its identity/window or retained pagination state")
			}
			data := ElasticInspectionData{Truncated: true, CleanupContinuations: []*analysis.ProviderContinuation{owned}}
			if err := ValidateElasticInspectionData(data); err != nil {
				t.Fatal(err)
			}
			public, err := json.Marshal(data)
			if err != nil || strings.Contains(string(public), cursor.PITID) || strings.Contains(string(public), "private-sort") || !strings.Contains(string(public), `"truncated":true`) {
				t.Fatalf("preview cleanup/truncation disclosure: %s %v", public, err)
			}
		})
	}
}

func inspectionSpoolGrant(t *testing.T, f *elasticSpoolCleanupFixture, kind string) (ElasticInspectionJob, SignedElasticInspectionJob) {
	t.Helper()
	job := ElasticInspectionJob{ID: "inspection", ProjectID: f.job.ProjectID, ConnectionID: "connection", ConnectionRevision: 1, CollectorID: f.job.CollectorID, Kind: kind, Config: f.job.Elastic.Config, Indices: f.job.Elastic.Selection.Indices, FromUTC: f.job.QueryFromUTC, ToUTC: f.job.QueryUntilUTC, OwnerToken: "owner", LeaseExpiresAt: time.Now().UTC().Add(time.Minute)}
	if kind == "cleanup" {
		job.CleanupContinuations = []*analysis.ProviderContinuation{f.cursor}
	}
	var err error
	job.Credentials, err = SealElasticInspectionCredentials(job, f.decrypt.PublicKey().Bytes(), ElasticCredentialBundle(elastic.Credentials{Username: "operator", Password: "private-password"}))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := SignElasticInspectionJob(job, f.signer)
	if err != nil {
		t.Fatal(err)
	}
	return job, envelope
}

func TestElasticInspectionResultDurablyReplaysAfterRestart(t *testing.T) {
	f := newElasticSpoolCleanupFixture(t)
	job, envelope := inspectionSpoolGrant(t, f, "preview")
	status := http.StatusServiceUnavailable
	var accepted []ElasticInspectionResult
	controller := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/elastic-inspections/inspection/result") {
			t.Errorf("unexpected replay destination %s %s", req.Method, req.URL.Path)
		}
		var result ElasticInspectionResult
		if err := json.NewDecoder(req.Body).Decode(&result); err != nil {
			t.Error(err)
		}
		accepted = append(accepted, result)
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	})}
	f.agent.client = controller
	result := &ElasticInspectionResult{Job: envelope, CleanupContinuations: []*analysis.ProviderContinuation{f.cursor}, Data: ElasticInspectionData{Preview: &elastic.Result{Evidence: []analysis.ProviderEvidence{{ID: "one", Provider: "elasticsearch", Kind: "log", QueryID: "logs", TimestampUTC: f.job.QueryFromUTC, Log: &analysis.ProviderLog{Message: "retained preview"}}}}}}
	if err := f.agent.persistElasticInspectionResult(job.ID, result); err != nil {
		t.Fatal(err)
	}
	if err := f.agent.uploadElasticInspectionResult(context.Background(), job.ID, result); err == nil {
		t.Fatal("failed controller receipt discarded inspection")
	}
	cfg := f.agent.cfg
	cfg.HTTPClient = controller
	restarted, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.loadSpool(); err != nil {
		t.Fatal(err)
	}
	if pending := restarted.elasticInspectionPending[job.ID]; pending == nil || len(pending.CleanupContinuations) != 1 || pending.CleanupContinuations[0].PITID != f.cursor.PITID {
		t.Fatal("restart lost inspection cleanup ownership")
	}
	status = http.StatusOK
	if replayed, err := restarted.replayElasticInspectionResult(context.Background(), job, envelope); err != nil || !replayed {
		t.Fatal(replayed, err)
	}
	if len(accepted) != 2 || len(restarted.elasticInspectionPending) != 0 {
		t.Fatal("inspection replay did not acknowledge exactly one retained result")
	}
	first, _ := json.Marshal(accepted[0])
	second, _ := json.Marshal(accepted[1])
	if string(first) != string(second) {
		t.Fatal("retry changed normalized inspection result or owned cleanup")
	}
	newer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = newer.loadSpool(); err != nil {
		t.Fatal(err)
	}
	if len(newer.elasticInspectionPending) != 0 {
		t.Fatal("acknowledged inspection revived after restart")
	}
}

func TestElasticInspectionCleanupAcknowledgesOnlyReleasedHandles(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newElasticSpoolCleanupFixture(t)
			f.status = status
			job, envelope := inspectionSpoolGrant(t, f, "cleanup")
			acknowledgements := 0
			f.agent.client = &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				acknowledgements++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
			})}
			err := f.agent.executeElasticInspection(context.Background(), job, envelope)
			if status == http.StatusServiceUnavailable {
				if err == nil || acknowledgements != 0 {
					t.Fatal("failed owned DELETE was acknowledged", err, acknowledgements)
				}
			} else if err != nil || acknowledgements != 1 {
				t.Fatal("successful or expired DELETE was not acknowledged", err, acknowledgements)
			}
			if len(f.closed) != 1 || f.closed[0] != f.cursor.PITID {
				t.Fatalf("cleanup used other than its signed handle: %v", f.closed)
			}
		})
	}
}

func TestElasticInspectionCleanupSpoolPreservesPendingPreview(t *testing.T) {
	f := newElasticSpoolCleanupFixture(t)
	previewJob, previewGrant := inspectionSpoolGrant(t, f, "preview")
	cleanupJob, cleanupGrant := inspectionSpoolGrant(t, f, "cleanup")
	preview := &ElasticInspectionResult{Job: previewGrant, Data: ElasticInspectionData{Truncated: true}, CleanupContinuations: []*analysis.ProviderContinuation{f.cursor}}
	if err := f.agent.persistElasticInspectionResult(previewJob.ID, preview); err != nil {
		t.Fatal(err)
	}
	status := http.StatusServiceUnavailable
	controller := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/elastic-inspections/inspection/result") {
			t.Errorf("private spool key leaked into controller path %s", req.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	})}
	f.agent.client = controller
	if err := f.agent.executeElasticInspection(context.Background(), cleanupJob, cleanupGrant); err == nil {
		t.Fatal("controller rejected cleanup receipt without retaining it")
	}
	if len(f.agent.elasticInspectionPending) != 2 || f.agent.elasticInspectionPending[previewJob.ID] != preview {
		t.Fatal("cleanup acknowledgement overwrote pending preview")
	}
	cfg := f.agent.cfg
	cfg.HTTPClient = controller
	restarted, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.loadSpool(); err != nil {
		t.Fatal(err)
	}
	key := elasticInspectionSpoolKey(cleanupJob)
	cleanup := restarted.elasticInspectionPending[key]
	if cleanup == nil || len(restarted.elasticInspectionPending) != 2 {
		t.Fatal("restart lost separately owned result and cleanup acknowledgement")
	}
	status = http.StatusOK
	if err := restarted.uploadElasticInspectionResult(context.Background(), key, cleanup); err != nil {
		t.Fatal(err)
	}
	if len(restarted.elasticInspectionPending) != 1 || restarted.elasticInspectionPending[previewJob.ID] == nil {
		t.Fatal("cleanup receipt discarded pending preview")
	}
	if replayed, err := restarted.replayElasticInspectionResult(context.Background(), previewJob, previewGrant); err != nil || !replayed {
		t.Fatal("preview did not independently replay", replayed, err)
	}
}

func TestElasticInspectionExpiredLeaseTransfersCleanupWithinReceiptHorizon(t *testing.T) {
	for _, tc := range []struct {
		name     string
		leaseAgo time.Duration
		expired  bool
		posts    int
		retained bool
	}{
		{name: "historical-transfer", leaseAgo: time.Minute, posts: 1},
		{name: "past-receipt-horizon", leaseAgo: 16 * time.Minute, retained: true},
		{name: "known-upstream-expiry", leaseAgo: time.Minute, expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newElasticSpoolCleanupFixture(t)
			job, _ := inspectionSpoolGrant(t, f, "preview")
			job.LeaseExpiresAt = time.Now().UTC().Add(-tc.leaseAgo)
			grant, err := SignElasticInspectionJob(job, f.signer)
			if err != nil {
				t.Fatal(err)
			}
			cursor := *f.cursor
			cursor.KeepAliveUntil = time.Now().UTC().Add(time.Hour)
			if tc.expired {
				cursor.KeepAliveUntil = time.Now().UTC().Add(-time.Second)
			}
			result := &ElasticInspectionResult{Job: grant, CleanupContinuations: []*analysis.ProviderContinuation{&cursor}}
			if err = f.agent.persistElasticInspectionResult(job.ID, result); err != nil {
				t.Fatal(err)
			}
			posts := 0
			f.agent.client = &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				if err := req.Context().Err(); err != nil {
					t.Error("historical ownership transfer reused the expired query deadline", err)
				}
				posts++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"completed":false}`)), Request: req}, nil
			})}
			f.agent.retryElasticInspectionSpool(context.Background())
			f.agent.providerWorkers.Wait()
			if posts != tc.posts || (f.agent.elasticInspectionPending[job.ID] != nil) != tc.retained || len(f.closed) != 0 {
				t.Fatalf("expired receipt retry: posts=%d retained=%v upstream closes=%v", posts, f.agent.elasticInspectionPending[job.ID] != nil, f.closed)
			}
		})
	}
}

func TestElasticInspectionFullSpoolPreventsUpstreamWork(t *testing.T) {
	for _, kind := range []string{"verify", "cleanup"} {
		t.Run(kind, func(t *testing.T) {
			f := newElasticSpoolCleanupFixture(t)
			job, grant := inspectionSpoolGrant(t, f, kind)
			// Model an existing shared spool without allocating a disposable
			// 32 MiB document. Fresh admission reserves the complete batch bound.
			f.agent.spoolBudget.mu.Lock()
			f.agent.spoolBudget.fileBytes["occupied"] = maxCollectorSpoolBytes - (512 << 10)
			f.agent.spoolBudget.mu.Unlock()
			called := false
			f.agent.elasticClient = func(elastic.Config, elastic.Credentials) (*elastic.Client, error) {
				called = true
				return nil, errors.New("unexpected upstream client creation")
			}
			if err := f.agent.executeElasticInspection(context.Background(), job, grant); err == nil || called || len(f.closed) != 0 || len(f.agent.elasticInspectionPending) != 0 {
				t.Fatal("full shared spool allowed fresh inspection I/O", err, called, f.closed)
			}
		})
	}
}

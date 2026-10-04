package collectoragent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunJobSignatureAndLease(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := RunJob{ID: "job", ProjectID: "p", RunID: "r", CollectorID: "c", SourceID: "s", SourceRevision: 1, IntervalSeconds: 1, Pack: "go-runtime", StartedAt: now, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute)}
	signed, err := SignRunJob(job, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRunJob(signed, pub, "c", now); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*SignedRunJob){"payload": func(j *SignedRunJob) { j.Payload = strings.Replace(j.Payload, `"p"`, `"other"`, 1) }, "signature": func(j *SignedRunJob) { j.Signature = "invalid" }} {
		t.Run(name, func(t *testing.T) {
			bad := signed
			change(&bad)
			if _, err := VerifyRunJob(bad, pub, "c", now); err == nil {
				t.Fatal("accepted tampered job")
			}
		})
	}
	if _, err := VerifyRunJob(signed, pub, "other", now); err == nil {
		t.Fatal("wrong collector")
	}
	if _, err := VerifyRunJob(signed, pub, "c", now.Add(time.Minute)); err == nil {
		t.Fatal("expired lease")
	}
}

func TestSignedCollectorDoesNotScrapeWithoutRunAndRetriesEvidence(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("# TYPE rpc_seconds gauge\nrpc_seconds{method=\"Lookup\"} 0.003\n"))
	}))
	defer target.Close()
	var jobs []SignedRunJob
	var received []RunSampleBatch
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "heartbeat"):
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			json.NewEncoder(w).Encode(jobs)
		case strings.HasSuffix(r.URL.Path, "/samples"):
			var b RunSampleBatch
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				t.Error(err)
			}
			received = append(received, b)
			if len(received) == 1 {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(200)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer controller.Close()
	a, err := New(Config{ControllerURL: controller.URL, CollectorID: "c", Token: "token", ControllerPublicKey: pub, Packs: []string{"prometheus"}, PrometheusURL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	a.tick(context.Background())
	if requests.Load() != 0 {
		t.Fatal("idle heartbeat contacted target")
	}
	now := time.Now().UTC()
	job := RunJob{ID: "job", ProjectID: "p", RunID: "r", CollectorID: "c", SourceID: "s", SourceRevision: 1, IntervalSeconds: 1, Pack: "prometheus", Destination: target.URL, StartedAt: now, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute)}
	signed, _ := SignRunJob(job, key)
	jobs = []SignedRunJob{signed}
	a.tick(context.Background())
	a.tick(context.Background())
	if requests.Load() != 1 || len(received) != 2 {
		t.Fatalf("requests=%d batches=%d", requests.Load(), len(received))
	}
	if point := received[0].Points[0]; point.MetricName != "rpc_seconds" || point.Unit != "ms" || point.Value == nil || *point.Value != 3 || point.Dimensions["method"] != "Lookup" {
		t.Fatalf("typed evidence: %+v", point)
	}
	first, _ := json.Marshal(received[0].Points)
	second, _ := json.Marshal(received[1].Points)
	if string(first) != string(second) {
		t.Fatal("retry changed observations")
	}
	jobs = nil
	a.tick(context.Background())
	if requests.Load() != 1 {
		t.Fatal("cancelled job contacted target")
	}
}

func TestSignedCollectorRejectsUnapprovedDestination(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	signed, _ := SignRunJob(RunJob{ID: "job", ProjectID: "p", RunID: "r", CollectorID: "c", SourceID: "s", SourceRevision: 1, IntervalSeconds: 1, Pack: "prometheus", Destination: "http://127.0.0.1:1", StartedAt: now, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute)}, key)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode([]SignedRunJob{signed}) }))
	defer controller.Close()
	a, err := New(Config{ControllerURL: controller.URL, CollectorID: "c", Token: "token", ControllerPublicKey: pub, Packs: []string{"prometheus"}, PrometheusURL: controller.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.runJobTick(context.Background()); err == nil {
		t.Fatal("unapproved destination accepted")
	}
}

func TestCloudEmptyWindowRetainsCollectionInterval(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := RunJob{ID: "job", ProjectID: "p", RunID: "r", CollectorID: "c", SourceID: "s", SourceRevision: 1, IntervalSeconds: 60, Pack: "cloudwatch", Destination: "cloudwatch://ec2", StartedAt: now, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute)}
	signed, _ := SignRunJob(job, key)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode([]SignedRunJob{signed}) }))
	defer controller.Close()
	target := AdapterTarget{Pack: job.Pack, Destination: job.Destination, Cloud: &CloudQuery{Region: "us-east-1", AccountID: "123456789012", Namespace: "AWS/EC2", Metrics: []CloudMetric{{Name: "CPU", Unit: "Percent", Statistic: "Average"}}}}
	a, err := New(Config{ControllerURL: controller.URL, CollectorID: "c", Token: "token", ControllerPublicKey: pub, Packs: []string{"cloudwatch"}, AdapterTargets: []AdapterTarget{target}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	until := a.nextCollection[job.ID]
	if until.Before(now.Add(time.Minute)) {
		t.Fatal("empty cloud window lost collection interval")
	}
	if err = a.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !a.nextCollection[job.ID].Equal(until) {
		t.Fatal("heartbeat triggered another cloud collection")
	}
	job.IntervalSeconds = 1
	signed, _ = SignRunJob(job, key)
	if _, err = VerifyRunJob(signed, pub, "c", now); err == nil {
		t.Fatal("cloud interval below provider bound accepted")
	}
}

func TestCollectorJobPagesRetainRetriesUntilCompleteScan(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := RunJob{ID: "a", ProjectID: "p", RunID: "r-a", CollectorID: "c", SourceID: "s", SourceRevision: 1, IntervalSeconds: 60, Pack: "go-runtime", StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute)}
	first, _ := SignRunJob(job, key)
	job.ID, job.RunID = "b", "r-b"
	second, _ := SignRunJob(job, key)
	cancelFirst := false
	var posted []RunSampleBatch
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			if r.Header.Get("X-APEX-Monitor-Readiness") == "" || r.URL.Query().Get("page_size") != "32" {
				t.Error("page omitted readiness or bounded size")
			}
			w.Header().Set("X-APEX-Monitor-Page", "1")
			if r.URL.Query().Get("after_key") == "" {
				if cancelFirst {
					w.Header().Set("X-APEX-Monitor-Next-Key", "b")
					json.NewEncoder(w).Encode([]SignedRunJob{second})
				} else {
					w.Header().Set("X-APEX-Monitor-Next-Key", "a")
					json.NewEncoder(w).Encode([]SignedRunJob{first})
				}
			} else if !cancelFirst {
				json.NewEncoder(w).Encode([]SignedRunJob{second})
			} else {
				json.NewEncoder(w).Encode([]SignedRunJob{})
			}
			return
		}
		var batch RunSampleBatch
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
		}
		posted = append(posted, batch)
		w.WriteHeader(503)
	}))
	defer controller.Close()
	agent, err := New(Config{ControllerURL: controller.URL, CollectorID: "c", Token: "token", ControllerPublicKey: pub, Packs: []string{"go-runtime"}, SpoolDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = agent.runJobTick(context.Background()); err == nil {
			t.Fatal("upload failure not surfaced")
		}
	}
	if len(agent.pendingBatches) != 2 || agent.pendingBatches["a"] == nil || agent.jobPollAfterKey != "" {
		t.Fatal("second page discarded the first page's durable retry")
	}
	original, _ := json.Marshal(agent.pendingBatches["a"].Points)
	if err = agent.runJobTick(context.Background()); err == nil {
		t.Fatal("retry failure not surfaced")
	}
	retried, _ := json.Marshal(posted[len(posted)-1].Points)
	if string(original) != string(retried) {
		t.Fatal("page continuation regenerated unacknowledged observations")
	}
	// Finish this scan, then verify a stopped job is removed only when the
	// following complete scan proves it absent.
	_ = agent.runJobTick(context.Background())
	cancelFirst = true
	_ = agent.runJobTick(context.Background())
	if agent.pendingBatches["a"] == nil {
		t.Fatal("partial scan treated an unvisited job as cancelled")
	}
	_ = agent.runJobTick(context.Background())
	if agent.pendingBatches["a"] != nil || agent.pendingBatches["b"] == nil {
		t.Fatal("completed scan failed to prune only the stopped job")
	}
}

func TestCollectorExpiredSpoolIsNotRenewedByPagination(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := RunJob{ID: "expired", ProjectID: "p", RunID: "r", CollectorID: "c", SourceID: "s", SourceRevision: 1, IntervalSeconds: 1, Pack: "go-runtime", StartedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second), LeaseExpiresAt: now.Add(-2 * time.Second)}
	expired, _ := SignRunJob(job, key)
	agent, err := New(Config{ControllerURL: "http://localhost:8080", CollectorID: "c", Token: "token", ControllerPublicKey: pub, Packs: []string{"go-runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	agent.pendingBatches[job.ID] = &RunSampleBatch{Job: expired}
	agent.providerBacklog[job.ID] = []*RunSampleBatch{{Job: expired}}
	agent.discardExpiredMonitorSpool(now)
	if len(agent.pendingBatches) != 0 || len(agent.providerBacklog) != 0 {
		t.Fatal("expired signed lifetime retained observations for resurrection")
	}
	job.ID, job.ExpiresAt = "lease-ended", now.Add(time.Hour)
	leaseEnded, _ := SignRunJob(job, key)
	agent.pendingBatches[job.ID] = &RunSampleBatch{Job: leaseEnded}
	agent.discardExpiredMonitorSpool(now)
	if agent.pendingBatches[job.ID] == nil {
		t.Fatal("renewable collection lease erased unacknowledged observations")
	}
}

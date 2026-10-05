package collectoragent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestCollectorPublicationMergeManagementOwnershipWaitsForCompletePageScan(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	target := managementTestTarget()
	scope, _ := target.Scope.Hash()
	identity, _ := target.Identity.Fingerprint()
	now := time.Now().UTC()
	job := RunJob{ID: "a", ProjectID: "p", RunID: "run-a", CollectorID: "c", SourceID: "source", SourceRevision: 1, Pack: "kafka-offsets", Destination: "management://orders", IntervalSeconds: 10, StartedAt: now, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), Management: &ManagementJob{Version: 1, ScopeHash: scope, IdentityHash: identity}}
	first, err := SignRunJob(job, key)
	if err != nil {
		t.Fatal(err)
	}
	job.ID, job.RunID = "b", "run-b"
	second, err := SignRunJob(job, key)
	if err != nil {
		t.Fatal(err)
	}
	cancelFirst := false
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-APEX-Monitor-Page", "1")
		var jobs []SignedRunJob
		if r.URL.Query().Get("after_key") == "" {
			if cancelFirst {
				w.Header().Set("X-APEX-Monitor-Next-Key", "b")
				jobs = []SignedRunJob{second}
			} else {
				w.Header().Set("X-APEX-Monitor-Next-Key", "a")
				jobs = []SignedRunJob{first}
			}
		} else if !cancelFirst {
			jobs = []SignedRunJob{second}
		}
		if jobs == nil {
			jobs = []SignedRunJob{}
		}
		if err := json.NewEncoder(w).Encode(jobs); err != nil {
			t.Error(err)
		}
	}))
	defer controller.Close()
	agent, err := New(Config{ControllerURL: controller.URL, CollectorID: "c", Token: "token", ControllerPublicKey: pub, Packs: []string{"go-runtime"}, SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: "kafka-offsets", Destination: "management://orders", Management: &target}}})
	if err != nil {
		t.Fatal(err)
	}
	agent.cfg.Packs = append(agent.cfg.Packs, "kafka-offsets")
	agent.management.observers["kafka-offsets"] = managementObserverFunc(func(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		t.Error("publication scan executed observer")
		return nil, nil
	})
	// A cancelled scheduler lifetime prevents provider execution while its real
	// candidate/cohort admission still exposes whether a partial page replaced it.
	root, cancel := context.WithCancel(context.Background())
	cancel()
	agent.management.root = root
	if err = agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(agent.management.jobs) != 0 || len(agent.jobScanManagement) != 1 {
		t.Fatal("partial page replaced management population")
	}
	if err = agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	cohort := agent.management.jobs[managementSourceKey("p", "source")]
	if len(cohort) != 1 || len(cohort[0].cohort) != 2 {
		t.Fatalf("complete scan omitted another-page run: %#v", cohort)
	}
	cancelFirst = true
	if err = agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(agent.management.jobs[managementSourceKey("p", "source")][0].cohort) != 2 {
		t.Fatal("partial cancellation scan removed unvisited owner")
	}
	if err = agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	cohort = agent.management.jobs[managementSourceKey("p", "source")]
	if len(cohort) != 1 || len(cohort[0].cohort) != 1 || cohort[0].cohort[0] != "b" {
		t.Fatalf("complete cancellation scan retained stopped owner: %#v", cohort)
	}
}

func TestCollectorPublicationMergeProviderBacklogSharesDurableSpoolBudget(t *testing.T) {
	agent, err := New(Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", SpoolDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	value := 1.0
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", Source: "s", MetricName: "cpu", Kind: analysis.KindGauge, Value: &value, Quality: analysis.QualityValid, TimestampUTC: time.Now().UTC()}
	for i := 0; i < maxCollectorSpoolJobs; i++ {
		agent.providerBacklog[fmt.Sprintf("provider-%d", i)] = []*RunSampleBatch{{Sequence: 0, Points: []analysis.MetricPoint{point}}}
	}
	if err = agent.saveSpool(); err != nil {
		t.Fatal(err)
	}
	if err = agent.spoolBudget.reserve("management", []string{"new-job"}); err == nil {
		t.Fatal("provider-only durable queues escaped combined job admission")
	}
	if err = agent.admitMonitorRetry("extra", &RunSampleBatch{Sequence: 0, Points: []analysis.MetricPoint{point}}, nil); err == nil {
		t.Fatal("point retry exceeded combined provider job budget")
	}
	if len(agent.pendingBatches) != 0 || len(agent.providerBacklog) != maxCollectorSpoolJobs {
		t.Fatal("refused admission discarded existing queues")
	}
	restarted, err := New(agent.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.loadSpool(); err != nil {
		t.Fatal(err)
	}
	if len(restarted.providerBacklog) != maxCollectorSpoolJobs {
		t.Fatal("provider backlog disappeared after restart")
	}
	if err = restarted.spoolBudget.reserve("management", []string{"new-job"}); err == nil {
		t.Fatal("restored provider queues escaped combined job admission")
	}
}

func TestCollectorPublicationMergeManagementTargetRefusesProviderConfiguration(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	target := managementTestTarget()
	cfg := Config{ControllerURL: "http://localhost", CollectorID: "c", Token: "t", ControllerPublicKey: pub, Packs: []string{"go-runtime"}, SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: "kafka-offsets", Destination: "management://orders", Management: &target}}}
	if _, err := New(cfg); err != nil {
		t.Fatal("ordinary dormant management target lost compatibility", err)
	}
	cfg.AdapterTargets[0].Applications = []string{"unrelated-provider-application"}
	if _, err := New(cfg); err == nil {
		t.Fatal("management target admitted provider application selection")
	}
}

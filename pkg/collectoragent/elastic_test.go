package collectoragent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
)

func TestElasticInspectionSignatureAndCredentialIdentity(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := ElasticInspectionJob{ID: "inspection", ProjectID: "p", ConnectionID: "c", ConnectionRevision: 3, CollectorID: "collector", Kind: "verify", OwnerToken: "owner", LeaseExpiresAt: now.Add(time.Minute), Config: elastic.Config{Product: "elasticsearch", Endpoint: "https://search.example.com", AuthType: "api_key"}}
	var err error
	job.Credentials, err = SealElasticInspectionCredentials(job, decrypt.PublicKey().Bytes(), ElasticCredentialBundle(elastic.Credentials{APIKey: "private-sensitive-key"}))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignElasticInspectionJob(job, key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(signed.Payload, "private-sensitive-key") {
		t.Fatal("credential disclosed")
	}
	checked, err := VerifyElasticInspectionJob(signed, public, "collector", now)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := OpenCredentials(elasticInspectionCredentialJob(checked), decrypt.Bytes(), now)
	if err != nil || creds.Elastic.Runtime().APIKey != "private-sensitive-key" {
		t.Fatalf("credential %v", err)
	}
	checked.Config.Endpoint = "https://other.example.com"
	if _, err = OpenCredentials(elasticInspectionCredentialJob(checked), decrypt.Bytes(), now); err == nil {
		t.Fatal("redirected credential accepted")
	}
	if _, err = VerifyElasticInspectionJob(signed, public, "other", now); err == nil {
		t.Fatal("different collector accepted")
	}
	if _, err = VerifyElasticInspectionJob(signed, public, "collector", now.Add(time.Minute)); err == nil {
		t.Fatal("expired grant accepted")
	}
}

func TestElasticBatchesRetainEveryBoundedRecordAndCheckpointOnlyLast(t *testing.T) {
	now := time.Now().UTC()
	job := RunJob{ID: "job", ProjectID: "p", RunID: "r", SourceID: "s", CollectorID: "c", SourceRevision: 1, Pack: "elastic", StartedAt: now, QueryFromUTC: now, QueryUntilUTC: now.Add(time.Minute)}
	cursor := &analysis.ProviderContinuation{QueryID: "q", PITID: "private-pit", WindowFrom: now, WindowUntil: now.Add(time.Minute)}
	result := elastic.Result{ResolvedIndices: []string{"logs-1"}, Continuation: cursor, Progress: analysis.ProviderProgress{NextQuery: 1, TotalQueries: 2}}
	for i := range 251 {
		result.Evidence = append(result.Evidence, analysis.ProviderEvidence{ID: fmt.Sprint(i), Kind: "log", QueryID: "q", Index: "logs-1", DocumentID: fmt.Sprint(i), TimestampUTC: now, Log: &analysis.ProviderLog{Message: "Authorization: Bearer private-token"}})
	}
	batches, err := elasticBatches(job, SignedRunJob{}, result, job.QueryUntilUTC)
	if err != nil || len(batches) != 3 {
		t.Fatalf("batches=%d err=%v", len(batches), err)
	}
	count := 0
	for i, batch := range batches {
		count += len(batch.Evidence)
		if len(batch.Evidence) > 100 || batch.Sequence != int64(i) {
			t.Fatal("batch limits or sequence")
		}
		if len(batch.ResolvedIndices) != 1 || batch.ResolvedIndices[0] != "logs-1" {
			t.Fatal("fixed-window concrete scope lost")
		}
		body, _ := json.Marshal(batch)
		if len(body) > 768<<10 || strings.Contains(string(body), "private-token") {
			t.Fatal("unsafe durable batch")
		}
		if (batch.Progress != nil) != (i == len(batches)-1) {
			t.Fatal("checkpoint advanced before prefix acceptance")
		}
	}
	if count != 251 || batches[2].Progress.Continuation != cursor {
		t.Fatal("suffix or continuation lost")
	}
	result.Evidence = make([]analysis.ProviderEvidence, 3201)
	if _, err = elasticBatches(job, SignedRunJob{}, result, job.QueryUntilUTC); err == nil {
		t.Fatal("over-budget suffix silently dropped")
	}
}

func TestElasticCleanupGrantRequiresAssignedOwnedCursor(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := RunJob{Cleanup: true, ID: "j", ProjectID: "p", RunID: "r", CollectorID: "c", SourceID: "s", SourceRevision: 1, Pack: "elastic", IntervalSeconds: 10, StartedAt: now, ExpiresAt: now.Add(time.Minute), LeaseExpiresAt: now.Add(time.Minute), Elastic: &elastic.JobConfig{Continuation: &analysis.ProviderContinuation{QueryID: "q", PITID: "owned", WindowFrom: now, WindowUntil: now.Add(time.Minute)}}}
	signed, _ := SignRunJob(job, key)
	if _, err := VerifyRunJob(signed, public, "c", now); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRunJob(signed, public, "other", now); err == nil {
		t.Fatal("cleanup crossed collector assignment")
	}
	job.Elastic.Continuation = nil
	signed, _ = SignRunJob(job, key)
	if _, err := VerifyRunJob(signed, public, "c", now); err == nil {
		t.Fatal("unowned cleanup grant accepted")
	}
}

func TestElasticCollectorLocalIndexApproval(t *testing.T) {
	for _, tc := range []struct {
		indices []string
		want    bool
	}{{[]string{"logs-*"}, true}, {[]string{"logs-2026"}, true}, {[]string{"*"}, false}, {[]string{"logs-*,secret-*"}, false}, {[]string{"secret-1"}, false}, {nil, false}} {
		if got := elasticIndicesApproved(tc.indices, []string{"logs-*"}); got != tc.want {
			t.Fatalf("%v=%v", tc.indices, got)
		}
	}
}

func TestElasticCollectorHealthOnlyAndDocumentLocalApproval(t *testing.T) {
	for _, tc := range []struct {
		name    string
		indices []string
		logs    bool
		health  bool
		fresh   bool
		want    bool
	}{
		{"indexless_health", nil, false, true, true, true},
		{"health_disabled", nil, false, false, false, false},
		{"mixed_unapproved", []string{"secret-*"}, true, true, false, false},
		{"documents_unapproved", []string{"secret-*"}, true, true, false, false},
		{"health_index_stats_unapproved", []string{"secret-*"}, false, true, false, false},
		{"mixed_approved", []string{"logs-*"}, true, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			public, private, _ := ed25519.GenerateKey(rand.Reader)
			decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
			now := time.Now().UTC()
			selection := elastic.Selection{ProjectID: "p", Indices: tc.indices, Logs: tc.logs, ClusterHealth: true}.Defaults()
			job := RunJob{ID: "job", ProjectID: "p", RunID: "run", SourceID: "source", SourceRevision: 1, CollectorID: "collector", Pack: "elastic", Destination: "https://search.example.com", IntervalSeconds: 30, StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), MaxBatches: 1, QueryFromUTC: now.Add(-time.Minute), QueryUntilUTC: now, Elastic: &elastic.JobConfig{Config: elastic.Config{Endpoint: "https://search.example.com", Product: "elasticsearch", AuthType: "basic", KibanaEndpoint: "https://kibana.example.com", LogstashEndpoint: "https://logstash.example.com"}, Selection: selection}}
			if tc.fresh {
				query := elastic.QueryDefinitions(selection)[0].QueryID
				observed := now.Add(-time.Second)
				job.Elastic.Capabilities = &elastic.Capabilities{Product: "elasticsearch", Version: "9.1.0", Health: true}
				job.Elastic.FinalHealthAfter = now.Add(-time.Minute)
				job.Elastic.HealthCoverage = map[string]analysis.ProviderCoverage{query: {QueryID: query, Status: "complete", From: job.QueryFromUTC, To: now, Points: 1, AsOf: &observed}}
				var err error
				job.Credentials, err = SealCredentials(job, decrypt.PublicKey().Bytes(), ElasticCredentialBundle(elastic.Credentials{Username: "operator", Password: "password"}))
				if err != nil {
					t.Fatal(err)
				}
			}
			envelope, err := SignRunJob(job, private)
			if err != nil {
				t.Fatal(err)
			}
			checked, err := VerifyRunJob(envelope, public, job.CollectorID, now)
			if err != nil {
				t.Fatal(err)
			}
			uploads := 0
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				uploads++
				var batch RunSampleBatch
				if r.URL.Path != "/api/v1/monitoring/collectors/collector/jobs/job/samples" || json.NewDecoder(r.Body).Decode(&batch) != nil || batch.Progress == nil || !batch.Progress.Complete || len(batch.Coverage) != 1 {
					t.Error("health snapshot or durable progress was not uploaded")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"accepted":true}`))
			}))
			defer controller.Close()
			approvedIndices := []string{"logs-*"}
			if tc.fresh {
				approvedIndices = nil
			}
			a, err := New(Config{ControllerURL: controller.URL, CollectorID: job.CollectorID, Token: "token", Packs: []string{"elastic"}, ControllerPublicKey: public, CredentialPrivateKey: decrypt.Bytes(), SpoolDirectory: t.TempDir(), AdapterTargets: []AdapterTarget{{Pack: "elastic", Destination: job.Destination, Indices: approvedIndices, ElasticHealth: tc.health}}})
			if err != nil {
				t.Fatal(err)
			}
			query := "health"
			result := elastic.Result{Coverage: map[string]analysis.ProviderCoverage{query: {QueryID: query, Status: "complete", From: job.QueryFromUTC, To: now}}, Progress: analysis.ProviderProgress{Complete: true, NextQuery: 1, TotalQueries: 1}}
			batches, err := elasticBatches(job, envelope, result, now)
			if err != nil {
				t.Fatal(err)
			}
			// A durably spooled health observation follows the same approval path
			// as a fresh read, without replacing the provider's governed transport.
			if !tc.fresh {
				if err = a.admitMonitorRetry(job.ID, batches[0], nil); err != nil {
					t.Fatal(err)
				}
			}
			err = a.collectElasticJob(context.Background(), checked, envelope)
			if (err == nil) != tc.want || (uploads == 1) != tc.want {
				t.Fatalf("collection err=%v uploads=%d want success=%t", err, uploads, tc.want)
			}
			if tc.want && len(a.pendingBatches) != 0 {
				t.Fatal("acknowledged health observation remains pending")
			}
		})
	}
}

func TestElasticComponentsRequireOnlySelectedLocalApprovals(t *testing.T) {
	config := elastic.Config{Endpoint: "https://search.example.com", KibanaEndpoint: "https://kibana.example.com", LogstashEndpoint: "https://logstash.example.com", CAPEM: "controller trust", ApprovedCIDRs: []string{"10.0.0.0/8"}, KibanaCAPEM: "controller kibana trust", LogstashCAPEM: "controller logstash trust"}
	target := AdapterTarget{Destination: config.Endpoint, CAPEM: "local trust", ApprovedCIDRs: []string{"192.168.0.0/16"}, KibanaEndpoint: config.KibanaEndpoint, KibanaCAPEM: "local kibana trust", KibanaApprovedCIDRs: []string{"172.16.0.0/12"}}
	for _, tc := range []struct {
		name             string
		kibana, logstash bool
		want             bool
	}{
		{"logs_only", false, false, true},
		{"approved_kibana", true, false, true},
		{"unapproved_logstash", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := elasticLocallyApprovedConfig(config, target, tc.kibana, tc.logstash)
			if (err == nil) != tc.want {
				t.Fatalf("component policy error=%v want=%t", err, tc.want)
			}
			if !tc.want {
				return
			}
			if cfg.CAPEM != target.CAPEM || len(cfg.ApprovedCIDRs) != 1 || cfg.ApprovedCIDRs[0] != target.ApprovedCIDRs[0] {
				t.Fatal("controller trust expanded local network policy")
			}
			if tc.kibana {
				if cfg.KibanaEndpoint != target.KibanaEndpoint || cfg.KibanaCAPEM != target.KibanaCAPEM || len(cfg.KibanaApprovedCIDRs) != 1 {
					t.Fatal("selected component lost independent destination/trust")
				}
			} else if cfg.KibanaEndpoint != "" || cfg.KibanaCAPEM != "" || len(cfg.KibanaApprovedCIDRs) != 0 {
				t.Fatal("unselected Kibana target survived")
			}
			if cfg.LogstashEndpoint != "" || cfg.LogstashCAPEM != "" || len(cfg.LogstashApprovedCIDRs) != 0 {
				t.Fatal("unselected Logstash target survived")
			}
			cfg.ApprovedCIDRs[0] = "changed"
			if target.ApprovedCIDRs[0] == "changed" {
				t.Fatal("client configuration mutated local approval")
			}
		})
	}
}

func TestElasticCleanupFailureDoesNotAcknowledgeUnknownExpiry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expiry time.Duration
		known  bool
		want   bool
	}{
		{"unknown_expiry", 0, false, false},
		{"future_expiry", time.Minute, true, false},
		{"known_expired", -time.Minute, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			public, private, _ := ed25519.GenerateKey(rand.Reader)
			decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
			now := time.Now().UTC()
			cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "owned", WindowFrom: now.Add(-time.Minute), WindowUntil: now}
			if tc.known {
				cursor.KeepAliveUntil = now.Add(tc.expiry)
			}
			// Loopback upstream destinations are rejected by the real governed
			// transport, giving a deterministic failed close without remote I/O.
			job := RunJob{Cleanup: true, ID: "job", ProjectID: "p", RunID: "run", SourceID: "source", SourceRevision: 1, CollectorID: "collector", Pack: "elastic", Destination: "https://127.0.0.1:9200", IntervalSeconds: 10, StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), LeaseExpiresAt: now.Add(time.Minute), Elastic: &elastic.JobConfig{Config: elastic.Config{Endpoint: "https://127.0.0.1:9200", Product: "elasticsearch", AuthType: "basic"}, Selection: elastic.Selection{ProjectID: "p", Indices: []string{"logs-*"}, Logs: true}.Defaults(), Continuation: cursor}}
			var err error
			job.Credentials, err = SealCredentials(job, decrypt.PublicKey().Bytes(), ElasticCredentialBundle(elastic.Credentials{Username: "operator", Password: "password"}))
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := SignRunJob(job, private)
			if err != nil {
				t.Fatal(err)
			}
			acks := 0
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				acks++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"accepted":true}`))
			}))
			defer controller.Close()
			a, err := New(Config{ControllerURL: controller.URL, CollectorID: job.CollectorID, Token: "token", Packs: []string{"elastic"}, ControllerPublicKey: public, CredentialPrivateKey: decrypt.Bytes(), AdapterTargets: []AdapterTarget{{Pack: "elastic", Destination: job.Destination, Indices: []string{"logs-*"}}}})
			if err != nil {
				t.Fatal(err)
			}
			err = a.executeElasticCleanup(context.Background(), job, envelope)
			if (err == nil) != tc.want || (acks == 1) != tc.want {
				t.Fatalf("cleanup err=%v acknowledgements=%d want=%t", err, acks, tc.want)
			}
		})
	}
}

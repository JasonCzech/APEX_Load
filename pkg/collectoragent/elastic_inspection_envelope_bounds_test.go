package collectoragent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
)

func TestElasticInspectionEnvelopeBoundsDependOnKind(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := ElasticInspectionJob{ID: "operation", ProjectID: "project", ConnectionID: "connection", ConnectionRevision: 1, CollectorID: "collector", Kind: "cleanup", Config: elastic.Config{Endpoint: "https://search.example.com", Product: elastic.ProductElasticsearch, AuthType: elastic.AuthAPIKey}, OwnerToken: "owned", LeaseExpiresAt: now.Add(time.Minute)}
	for i := range 2 {
		job.CleanupContinuations = append(job.CleanupContinuations, &analysis.ProviderContinuation{QueryID: "inspection-pit", PITID: strings.Repeat("<", 16383) + string(rune('0'+i)), WindowFrom: now, WindowUntil: now.Add(time.Nanosecond), KeepAliveUntil: now.Add(2 * time.Minute)})
	}
	body, _ := json.Marshal(job.CleanupContinuations)
	if len(body) > 256<<10 {
		t.Fatal("fixture exceeds storage cleanup ceiling")
	}
	envelope, err := SignElasticInspectionJob(job, key)
	if err != nil || len(envelope.Payload) <= normalElasticInspectionJobBytes {
		t.Fatal("large valid cleanup envelope rejected", len(envelope.Payload), err)
	}
	if restored, err := VerifyElasticInspectionJob(envelope, public, job.CollectorID, now); err != nil || len(restored.CleanupContinuations) != 2 {
		t.Fatal("large signed cleanup did not verify", restored, err)
	}

	// Normal inspections retain their existing budget even when correctly signed.
	normal := job
	normal.Kind = "verify"
	normal.CleanupContinuations = nil
	normal.RunID = strings.Repeat("x", normalElasticInspectionJobBytes)
	if _, err := SignElasticInspectionJob(normal, key); err == nil {
		t.Fatal("normal signer accepted enlarged budget")
	}
	payload, _ := json.Marshal(normal)
	forged := SignedElasticInspectionJob{Payload: string(payload), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, payload))}
	if _, err := VerifyElasticInspectionJob(forged, public, job.CollectorID, now); err == nil {
		t.Fatal("normal verifier accepted enlarged budget")
	}

	job.RunID = strings.Repeat("x", cleanupElasticInspectionJobBytes)
	if _, err := SignElasticInspectionJob(job, key); err == nil {
		t.Fatal("unbounded cleanup signer")
	}
	payload, _ = json.Marshal(job)
	forged = SignedElasticInspectionJob{Payload: string(payload), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, payload))}
	if _, err := VerifyElasticInspectionJob(forged, public, job.CollectorID, now); err == nil {
		t.Fatal("unbounded cleanup verifier")
	}
}

func TestElasticInspectionCleanupMergeDeduplicatesOwnedContexts(t *testing.T) {
	from := time.Now().UTC()
	first := &analysis.ProviderContinuation{QueryID: "inspection-pit", PITID: "owned-pit", WindowFrom: from, WindowUntil: from.Add(time.Nanosecond)}
	copy := *first
	copy.WindowUntil = from.Add(time.Second)
	async := &analysis.ProviderContinuation{QueryID: "inspection-async", AsyncID: "owned-async", WindowFrom: from, WindowUntil: from.Add(time.Nanosecond)}
	merged := mergeElasticInspectionCleanup([]*analysis.ProviderContinuation{first, async}, []*analysis.ProviderContinuation{&copy})
	if len(merged) != 2 || merged[0] != first || merged[1] != async {
		t.Fatal("private preview and parent ownership duplicated", merged)
	}
}

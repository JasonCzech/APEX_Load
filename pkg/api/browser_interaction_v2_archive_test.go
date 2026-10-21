package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserInteractionV2ArchivePreservesMixedRecordsAndReceipts(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a")
	client := &ackingArchiveClient{archiveStreamingFakeClient: run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)}
	run.remoteClientsByTarget["a"] = client
	current := v3Interaction(1, 0)
	legacy := e4Tap(2, time.Second, 100, 100)
	records := []*protocol.BrowserInteractionEvidence{
		{Version: 2, SourceGeneration: e4Generation, Interactions: []protocol.BrowserInteraction{current}},
		{Version: 1, SourceGeneration: e4Generation, Interactions: []protocol.BrowserInteraction{legacy}},
	}
	var body []byte
	for _, record := range records {
		if record.Clone() == nil {
			t.Fatal("invalid fixture")
		}
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		body = append(append(body, raw...), '\n')
	}
	path := filepath.Join(t.TempDir(), browserInteractionsFileName)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	client.mu.Lock()
	client.artifacts[browserInteractionsFileName] = path
	manifestPath := client.artifacts[runArchiveManifestFileName]
	client.mu.Unlock()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["browser_interaction_record_count"] = 2
	manifest["browser_interactions_artifact"] = map[string]any{"name": browserInteractionsFileName, "kind": "interaction_evidence", "fidelity": "native", "source_generation": "interactions", "sequence": 1, "sha256": hex.EncodeToString(sum[:]), "bytes": len(body)}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "", ended, true)
	if err != nil {
		t.Fatal(err)
	}
	m, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	var got []*protocol.BrowserInteractionEvidence
	count, err := forEachBrowserInteractionRecord(filepath.Join(record.ArchivePath, m.BrowserInteractionsFile), func(e *protocol.BrowserInteractionEvidence) error { got = append(got, e.Clone()); return nil })
	if err != nil || count != 2 || !reflect.DeepEqual(got, records) {
		t.Fatalf("round trip count=%d err=%v records=%+v", count, err, got)
	}
	receipts, err := store.ListRunEvidenceReceipts(run.ID)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipts=%+v err=%v", receipts, err)
	}
	if _, err := store.UpdateTestStatusIfActive(run.ID, "COMPLETE", &ended, ""); err != nil {
		t.Fatal(err)
	}
	if err := tr.finalizeRunArchiveDurably(context.Background(), run, record, true); err != nil {
		t.Fatal(err)
	}
	// Durable readers preserve raw versions; derived analysis only qualifies v2.
	gotAnalysis := analyzeV3(got[0].Interactions[0], got[1].Interactions[0])
	if gotAnalysis.Coverage.PatternEligible != 1 || gotAnalysis.Coverage.PatternExcluded["legacy_identity_unavailable"] != 1 {
		t.Fatalf("analysis=%+v", gotAnalysis)
	}
}

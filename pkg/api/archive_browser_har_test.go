package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// ackingArchiveClient is a fake generator that serves a HAR-bearing run
// archive and records acknowledgements.
type ackingArchiveClient struct {
	*archiveStreamingFakeClient
	ackMu sync.Mutex
	acks  []generatorrpc.EvidenceAcknowledgement
}

func (c *ackingArchiveClient) AcknowledgeEvidence(_ context.Context, request generatorrpc.EvidenceAcknowledgement) (generatorrpc.EvidenceAckResult, error) {
	c.ackMu.Lock()
	defer c.ackMu.Unlock()
	c.acks = append(c.acks, request)
	result := generatorrpc.EvidenceAckResult{}
	for _, artifact := range request.Artifacts {
		result.Deleted = append(result.Deleted, artifact.Name)
	}
	return result, nil
}

// declareFakeHAR adds a HAR file to a fake generator archive and declares it
// in the generator manifest; declaredSHA overrides the real hash.
func declareFakeHAR(t *testing.T, client *archiveStreamingFakeClient, generation, content string, sequence int64, declaredSHA string) {
	t.Helper()
	name := "browser_har_" + generation + ".har"
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	if declaredSHA == "" {
		declaredSHA = hex.EncodeToString(sum[:])
	}
	client.mu.Lock()
	client.artifacts[name] = path
	manifestPath := client.artifacts[runArchiveManifestFileName]
	client.mu.Unlock()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	list, _ := manifest["browser_har_artifacts"].([]any)
	manifest["browser_har_artifacts"] = append(list, map[string]any{"name": name, "kind": "har_native", "fidelity": "native", "source_generation": generation, "sequence": sequence, "sha256": declaredSHA, "bytes": len(content)})
	if data, err = json.Marshal(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDistributedArchiveTransfersHARsWithReceipts(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a", "b")
	a := &ackingArchiveClient{archiveStreamingFakeClient: run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)}
	run.remoteClientsByTarget["a"] = a
	b := run.remoteClientsByTarget["b"].(*archiveStreamingFakeClient)
	first, second := strings.Repeat("1", 32), strings.Repeat("2", 32)
	declareFakeHAR(t, a.archiveStreamingFakeClient, first, `{"log":{"entries":[1]}}`, 1, "")
	declareFakeHAR(t, a.archiveStreamingFakeClient, second, `{"log":{"entries":[2]}}`, 2, "")
	// A HAR whose bytes differ from its declaration is never archived.
	declareFakeHAR(t, b, strings.Repeat("3", 32), `{"log":{"entries":[3]}}`, 1, strings.Repeat("f", 64))
	ended := time.Now().UTC()
	record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "", ended, true)
	if err != nil || record == nil {
		t.Fatalf("stage: %v", err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.BrowserHARArtifacts) != 2 || !manifest.BrowserHARIncomplete || manifest.BrowserHAROmitted[harOmittedTransfer] != 1 {
		t.Fatalf("archive HARs %+v omitted %+v", manifest.BrowserHARArtifacts, manifest.BrowserHAROmitted)
	}
	identity := generatorrpc.ArchiveSourceIdentity(run.ID, bytesRepeat(1), "a")
	described := 0
	for _, har := range manifest.BrowserHARArtifacts {
		if har.SourceIdentity != identity || har.Fidelity != "native" {
			t.Fatalf("HAR source %+v", har)
		}
		if err := verifyHARFile(filepath.Join(record.ArchivePath, har.Name), har); err != nil {
			t.Fatal(err)
		}
		for _, artifact := range manifest.Artifacts {
			if artifact.Name == har.Name && artifact.RecordType == runArchiveHARRecordType && artifact.SHA256 == har.SHA256 {
				described++
			}
		}
	}
	if described != 2 {
		t.Fatal("HARs missing from the archive inventory")
	}
	receipts, err := store.ListRunEvidenceReceipts(run.ID)
	if err != nil || len(receipts) != 2 || receipts[0].State != storage.EvidenceReceiptAccepted || receipts[0].Target != "a" {
		t.Fatalf("receipts %+v %v", receipts, err)
	}

	// The controller restarts before publishing: the staged archive never
	// became the published one. A fresh collection fetches the same HARs
	// again; their receipts stay single and accepted.
	if err := os.RemoveAll(record.ArchivePath); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewTestRunner(1, nil, nil)
	restarted.testStore, restarted.archiveStore = store, store
	restored := managedTestFromRecord(row)
	restored.remoteClientsByTarget = map[string]generatorClient{"a": a, "b": b}
	restarted.tests[restored.ID] = restored
	record, err = backend.stageRemoteArchive(restarted, restored, "COMPLETE", "", ended, true)
	if err != nil || record == nil {
		t.Fatalf("restaged: %v", err)
	}
	receipts, _ = store.ListRunEvidenceReceipts(run.ID)
	if len(receipts) != 2 || receipts[1].State != storage.EvidenceReceiptAccepted {
		t.Fatalf("restarted transfer duplicated receipts: %+v", receipts)
	}
	if len(a.acks) != 0 {
		t.Fatal("acknowledged before publication")
	}

	// Publication commits through the durable finalizer: receipts are
	// published and the generator told.
	if _, err := store.UpdateTestStatusIfActive(run.ID, "COMPLETE", &ended, ""); err != nil {
		t.Fatal(err)
	}
	if err := restarted.finalizeRunArchiveDurably(context.Background(), restored, record, true); err != nil {
		t.Fatal(err)
	}
	receipts, _ = store.ListRunEvidenceReceipts(run.ID)
	for _, receipt := range receipts {
		if receipt.State != storage.EvidenceReceiptPublished {
			t.Fatalf("receipt not published: %+v", receipt)
		}
	}
	if len(a.acks) != 1 || a.acks[0].SourceIdentity != identity || len(a.acks[0].Artifacts) != 2 {
		t.Fatalf("acknowledgements %+v", a.acks)
	}

	// An upload after publication is tombstoned and its copy removed.
	staged := filepath.Join(t.TempDir(), "browser_har_"+strings.Repeat("4", 32)+".har")
	late := `{"log":{"entries":[4]}}`
	if err := os.WriteFile(staged, []byte(late), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(late))
	entry := RunArchiveHARArtifact{Name: filepath.Base(staged), Kind: storage.EvidenceKindHARNative, Fidelity: "native", SourceIdentity: identity, SourceGeneration: strings.Repeat("4", 32), Sequence: 3, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(late))}
	working := t.TempDir()
	var lateManifest RunArchiveManifest
	bundles := []remoteArchiveBundle{{target: "a", hars: []stagedRemoteHAR{{entry: entry, path: staged}}}}
	if err := mergeRemoteHARArtifacts(working, &lateManifest, bundles, restarted.acceptRemoteHARReceipt(restored, map[string]string{identity: "a"})); err != nil {
		t.Fatal(err)
	}
	if len(lateManifest.BrowserHARArtifacts) != 0 || lateManifest.BrowserHAROmitted[harOmittedReceipt] != 1 {
		t.Fatalf("late HAR archived: %+v", lateManifest)
	}
	if _, err := os.Stat(filepath.Join(working, entry.Name)); !os.IsNotExist(err) {
		t.Fatal("tombstoned HAR left in the archive", err)
	}
	receipts, _ = store.ListRunEvidenceReceipts(run.ID)
	if len(receipts) != 3 || receipts[2].State != storage.EvidenceReceiptTombstoned {
		t.Fatalf("late receipt %+v", receipts)
	}
}

func TestCachedArchiveSourceLeavesHARsUnavailable(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a", "lost")
	lost := run.remoteClientsByTarget["lost"].(*archiveStreamingFakeClient)
	declareFakeHAR(t, lost, strings.Repeat("5", 32), `{"log":{"entries":[5]}}`, 1, "")
	declareFakeHAR(t, lost, strings.Repeat("6", 32), `{"log":{"entries":[6]}}`, 2, "")
	// The first attempt caches source "lost", then fails on a missing
	// required source; the retry serves "lost" from the cache only.
	missing := run.remoteClientsByTarget["a"]
	delete(run.remoteClientsByTarget, "a")
	if _, err := backend.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
		t.Fatal("a missing required source was accepted")
	}
	row, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewTestRunner(1, nil, nil)
	restarted.testStore, restarted.archiveStore = store, store
	restored := managedTestFromRecord(row)
	restored.remoteClientsByTarget = map[string]generatorClient{"a": missing}
	record, err := backend.stageRemoteArchive(restarted, restored, "FAILED", "", time.Now(), true)
	if err != nil || record == nil {
		t.Fatalf("cached retry: %v", err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil || len(manifest.BrowserHARArtifacts) != 0 || manifest.BrowserHAROmitted[harOmittedSourceUnavailable] != 2 {
		t.Fatalf("cached-only HARs %+v %v", manifest.BrowserHAROmitted, err)
	}
}

func bytesRepeat(b byte) []byte { return []byte(strings.Repeat(string([]byte{b}), 32)) }

func TestLocalRunArchiveRetainsBrowserHARs(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	for _, tamper := range []bool{false, true} {
		run := &ManagedTest{ID: "local-har-" + map[bool]string{false: "kept", true: "tampered"}[tamper], Name: "local"}
		writer, err := newRunArchiveWriter(run, RunArchiveProvenance{})
		if err != nil {
			t.Fatal(err)
		}
		harDir := t.TempDir()
		generation := strings.Repeat("7", 32)
		path := filepath.Join(harDir, "apexload_browser_"+generation+"_scrubbed.har")
		content := `{"log":{"entries":[7]}}`
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writer.RecordBrowserHARArtifact(protocolHAR(path, generation)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("the HAR directory kept a moved artifact", err)
		}
		name := "browser_har_" + generation + ".har"
		if tamper {
			if err := os.WriteFile(filepath.Join(writer.rootDir, name), []byte(`{"forged":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
		if tamper {
			if err == nil {
				t.Fatal("a HAR that differs from its record was archived")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil || len(manifest.BrowserHARArtifacts) != 1 || manifest.BrowserHARArtifacts[0].Name != name || manifest.BrowserHARArtifacts[0].SourceIdentity != "" {
			t.Fatalf("local HARs %+v %v", manifest.BrowserHARArtifacts, err)
		}
		listed := false
		for _, artifact := range manifest.Artifacts {
			listed = listed || artifact.Name == name && artifact.Format == "har"
		}
		if !listed {
			t.Fatal("local HAR missing from the inventory")
		}
		// Safe exports carry no HAR until HARs have their own export contract.
		plan, err := prepareSanitizedRunArchivePlan(context.Background(), record, manifest)
		if err != nil {
			t.Fatal(err)
		}
		if _, omitted := plan.omit[name]; !omitted || len(plan.manifest.BrowserHARArtifacts) != 0 {
			t.Fatal("a safe export includes the HAR")
		}
		for _, artifact := range plan.manifest.Artifacts {
			if artifact.Name == name {
				t.Fatal("a safe export lists the HAR")
			}
		}
	}
}

func protocolHAR(path, generation string) protocol.BrowserHARArtifact {
	return protocol.BrowserHARArtifact{Path: path, Generation: generation}
}

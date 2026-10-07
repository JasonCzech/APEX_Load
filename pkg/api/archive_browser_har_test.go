package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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
	// became the published one. A fresh collection fetches the HARs again;
	// one now fails verification, so the archive omits it and its earlier
	// receipt claim is forgotten rather than published and acknowledged.
	if err := os.RemoveAll(record.ArchivePath); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	secondPath := a.artifacts["browser_har_"+second+".har"]
	a.mu.Unlock()
	if err := os.WriteFile(secondPath, []byte(`{"log":{"entries":["changed"]}}`), 0o600); err != nil {
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
	if len(receipts) != 1 || receipts[0].State != storage.EvidenceReceiptAccepted || receipts[0].SourceGeneration != first {
		t.Fatalf("restarted transfer kept a stale receipt: %+v", receipts)
	}
	if len(a.acks) != 0 {
		t.Fatal("acknowledged before publication")
	}

	// Publication commits through the durable finalizer in the real order:
	// the run's generator clients are already closed, so the controller
	// dials the generator to tell it.
	if _, err := store.UpdateTestStatusIfActive(run.ID, "COMPLETE", &ended, ""); err != nil {
		t.Fatal(err)
	}
	restored.remoteClientsByTarget, restored.remoteArchiveClientsByTarget = nil, nil
	backend.dial = func(_ context.Context, target string) (generatorClient, error) {
		if target == "a" {
			return a, nil
		}
		return b, nil
	}
	restarted.setExecutionBackend(backend)
	if err := restarted.finalizeRunArchiveDurably(context.Background(), restored, record, true); err != nil {
		t.Fatal(err)
	}
	restarted.waitEvidenceAcknowledgements()
	receipts, _ = store.ListRunEvidenceReceipts(run.ID)
	for _, receipt := range receipts {
		if receipt.State != storage.EvidenceReceiptPublished {
			t.Fatalf("receipt not published: %+v", receipt)
		}
	}
	if len(a.acks) != 1 || a.acks[0].SourceIdentity != identity || len(a.acks[0].Artifacts) != 1 || a.acks[0].Artifacts[0].Name != "browser_har_"+first+".har" {
		t.Fatalf("acknowledgements %+v", a.acks)
	}

	// An upload after publication is tombstoned and its copy removed.
	staged := filepath.Join(t.TempDir(), "browser_har_"+strings.Repeat("4", 32)+".har")
	late := `{"log":{"entries":[4]}}`
	if err := os.WriteFile(staged, []byte(late), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(late))
	entry := RunArchiveEvidenceArtifact{Name: filepath.Base(staged), Kind: storage.EvidenceKindHARNative, Fidelity: "native", SourceIdentity: identity, SourceGeneration: strings.Repeat("4", 32), Sequence: 3, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(late))}
	working := t.TempDir()
	var lateManifest RunArchiveManifest
	bundles := []remoteArchiveBundle{{target: "a", hars: []stagedRemoteHAR{{entry: entry, path: staged}}}}
	if err := mergeRemoteHARArtifacts(working, &lateManifest, bundles, restarted.acceptRemoteHARReceipt(restored, map[string]string{identity: "a"}), nil); err != nil {
		t.Fatal(err)
	}
	if len(lateManifest.BrowserHARArtifacts) != 0 || lateManifest.BrowserHAROmitted[harOmittedReceipt] != 1 {
		t.Fatalf("late HAR archived: %+v", lateManifest)
	}
	if _, err := os.Stat(filepath.Join(working, entry.Name)); !os.IsNotExist(err) {
		t.Fatal("tombstoned HAR left in the archive", err)
	}
	receipts, _ = store.ListRunEvidenceReceipts(run.ID)
	if len(receipts) != 2 || receipts[1].State != storage.EvidenceReceiptTombstoned {
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
		// The HAR directory keeps its copy until the archive is final.
		if _, err := os.Stat(path); err != nil {
			t.Fatal("the HAR directory lost an artifact before its archive was final", err)
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
			if _, err := os.Stat(path); err != nil {
				t.Fatal("a failed archive retired its HAR's other copy", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("the HAR directory kept a finalized archive's artifact", err)
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

func fakeInteractionEvidence(gen string, interactions int) *protocol.BrowserInteractionEvidence {
	evidence := &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen}
	for i := 0; i < interactions; i++ {
		evidence.Interactions = append(evidence.Interactions, protocol.BrowserInteraction{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen, InteractionID: gen + "-i" + strings.Repeat("1", i+1), FrameScope: "main_frame", Kind: "click", Provenance: protocol.InteractionProvenanceScripted, CapturedAt: time.Now().UTC(), Outcome: protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared}})
	}
	return evidence
}

// declareFakeInteractions adds an interactions sidecar to a fake generator
// archive; declaredRecords overrides the real record count when non-zero.
func declareFakeInteractions(t *testing.T, client *archiveStreamingFakeClient, gen string, records int, declaredRecords int) {
	t.Helper()
	var body []byte
	for i := 0; i < records; i++ {
		data, err := json.Marshal(fakeInteractionEvidence(gen, i+1))
		if err != nil {
			t.Fatal(err)
		}
		body = append(append(body, data...), '\n')
	}
	path := filepath.Join(t.TempDir(), browserInteractionsFileName)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	client.mu.Lock()
	client.artifacts[browserInteractionsFileName] = path
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
	if declaredRecords == 0 {
		declaredRecords = records
	}
	manifest["browser_interaction_record_count"] = declaredRecords
	manifest["browser_interactions_artifact"] = map[string]any{"name": browserInteractionsFileName, "kind": "interaction_evidence", "fidelity": "native", "source_generation": "interactions", "sequence": 1, "sha256": hex.EncodeToString(sum[:]), "bytes": len(body)}
	if data, err = json.Marshal(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDistributedArchiveMergesInteractionEvidenceWithReceipts(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a", "b", "c")
	a := &ackingArchiveClient{archiveStreamingFakeClient: run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)}
	run.remoteClientsByTarget["a"] = a
	declareFakeInteractions(t, a.archiveStreamingFakeClient, strings.Repeat("a", 32), 2, 0)
	declareFakeInteractions(t, run.remoteClientsByTarget["b"].(*archiveStreamingFakeClient), strings.Repeat("b", 32), 1, 0)
	// A sidecar whose records differ from its declaration is not archived.
	declareFakeInteractions(t, run.remoteClientsByTarget["c"].(*archiveStreamingFakeClient), strings.Repeat("c", 32), 2, 5)
	ended := time.Now().UTC()
	record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "", ended, true)
	if err != nil || record == nil {
		t.Fatalf("stage: %v", err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BrowserInteractionsFile != browserInteractionsFileName || manifest.BrowserInteractionRecordCount != 3 || !manifest.BrowserInteractionsIncomplete || manifest.BrowserInteractionsArtifact != nil {
		t.Fatalf("archive interactions %+v", manifest)
	}
	interactions := 0
	count, err := forEachBrowserInteractionRecord(filepath.Join(record.ArchivePath, manifest.BrowserInteractionsFile), func(e *protocol.BrowserInteractionEvidence) error {
		interactions += len(e.Interactions)
		return nil
	})
	if err != nil || count != 3 || interactions != 4 {
		t.Fatalf("merged %d records, %d interactions: %v", count, interactions, err)
	}
	inventoried := false
	for _, artifact := range manifest.Artifacts {
		inventoried = inventoried || artifact.Name == browserInteractionsFileName && artifact.RecordType == browserInteractionsRecordType && artifact.Count == 3
	}
	if !inventoried {
		t.Fatal("interactions missing from the archive inventory")
	}
	receipts, err := store.ListRunEvidenceReceipts(run.ID)
	if err != nil || len(receipts) != 2 || receipts[0].Kind != storage.EvidenceKindInteraction {
		t.Fatalf("receipts %+v %v", receipts, err)
	}
	if _, err := store.UpdateTestStatusIfActive(run.ID, "COMPLETE", &ended, ""); err != nil {
		t.Fatal(err)
	}
	if err := tr.finalizeRunArchiveDurably(context.Background(), run, record, true); err != nil {
		t.Fatal(err)
	}
	tr.waitEvidenceAcknowledgements()
	if len(a.acks) != 1 || len(a.acks[0].Artifacts) != 1 || a.acks[0].Artifacts[0].Name != browserInteractionsFileName {
		t.Fatalf("acknowledgements %+v", a.acks)
	}
	// After publication a late interactions upload is tombstoned, not merged.
	late := filepath.Join(t.TempDir(), browserInteractionsFileName)
	data, _ := json.Marshal(fakeInteractionEvidence(strings.Repeat("f", 32), 1))
	if err := os.WriteFile(late, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append(data, '\n'))
	identity := generatorrpc.ArchiveSourceIdentity(run.ID, bytesRepeat(1), "b")
	entry := RunArchiveEvidenceArtifact{Name: browserInteractionsFileName, Kind: storage.EvidenceKindInteraction, Fidelity: "native", SourceIdentity: identity, SourceGeneration: "late-interactions", Sequence: 1, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data) + 1)}
	var lateManifest RunArchiveManifest
	working := t.TempDir()
	if err := mergeRemoteInteractions(working, &lateManifest, []remoteArchiveBundle{{target: "b", interactions: &stagedRemoteHAR{entry: entry, path: late}}}, tr.acceptRemoteHARReceipt(run, map[string]string{identity: "b"}), nil); err != nil {
		t.Fatal(err)
	}
	if lateManifest.BrowserInteractionsFile != "" || !lateManifest.BrowserInteractionsIncomplete {
		t.Fatalf("late interactions merged: %+v", lateManifest)
	}
	if _, err := os.Stat(filepath.Join(working, browserInteractionsFileName)); !os.IsNotExist(err) {
		t.Fatal("tombstoned interactions written", err)
	}
}

func TestLocalRunArchiveWritesInteractionEvidence(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "local-interactions", Name: "local"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	gen := strings.Repeat("9", 32)
	// The engine hands evidence to the run's combined sink, which must pass
	// it to the archive writer.
	sink := combinedResultSink{liveErrorEvidenceSink{}, writer}
	for i := 0; i < 2; i++ {
		if err := sink.RecordBrowserInteractionEvidence(fakeInteractionEvidence(gen, 3)); err != nil {
			t.Fatal(err)
		}
	}
	record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil || manifest.BrowserInteractionsFile != browserInteractionsFileName || manifest.BrowserInteractionRecordCount != 2 {
		t.Fatalf("local interactions %+v %v", manifest, err)
	}
	if count, err := forEachBrowserInteractionRecord(filepath.Join(record.ArchivePath, browserInteractionsFileName), func(*protocol.BrowserInteractionEvidence) error { return nil }); err != nil || count != 2 {
		t.Fatalf("records %d: %v", count, err)
	}
}

// A staged HAR on another filesystem is copied only within the archive merge
// budget; a refusal omits it as archive_quota without latching the budget.
func TestStagedHARCrossFilesystemCopyStaysWithinTheMergeBudget(t *testing.T) {
	staging, working := t.TempDir(), t.TempDir()
	content := []byte(`{"log":{"version":"1.2","entries":[]}}`)
	path := filepath.Join(staging, "staged.har")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	name := "browser_har_" + strings.Repeat("e", 32) + ".har"
	staged := stagedRemoteHAR{entry: RunArchiveEvidenceArtifact{Name: name, Bytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}, path: path}
	original := renameStagedHAR
	t.Cleanup(func() { renameStagedHAR = original })
	renameStagedHAR = func(string, string) error { return &os.LinkError{Op: "rename", Err: os.ErrInvalid} }

	tight := &archiveOutputBudget{root: working, remaining: 4}
	if err := copyStagedHAR(staged, working, tight); err == nil || err != errHARArchiveQuota {
		t.Fatalf("a HAR past the merge budget: %v", err)
	}
	if _, err := os.Stat(filepath.Join(working, name)); !os.IsNotExist(err) {
		t.Fatal("a refused HAR reached the archive", err)
	}
	if tight.failure() != nil {
		t.Fatal("an optional refusal latched the merge budget")
	}
	roomy := &archiveOutputBudget{root: working, remaining: 1 << 20}
	if err := copyStagedHAR(staged, working, roomy); err != nil {
		t.Fatal(err)
	}
	if copied, err := os.ReadFile(filepath.Join(working, name)); err != nil || string(copied) != string(content) || roomy.remaining != 1<<20-int64(len(content)) {
		t.Fatalf("copied %q remaining %d %v", copied, roomy.remaining, err)
	}
}

// partialArchiveClient streams declared artifacts, breaking some part-way.
type partialArchiveClient struct {
	content   map[string][]byte
	failAfter map[string]int
}

func (c *partialArchiveClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, os.ErrInvalid
}

func (c *partialArchiveClient) StreamArchiveArtifact(_ context.Context, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, _ int64) (int64, error) {
	data := c.content[request.Name]
	if n, broken := c.failAfter[request.Name]; broken {
		written, _ := destination.Write(data[:n])
		return int64(written), io.ErrUnexpectedEOF
	}
	written, err := destination.Write(data)
	return int64(written), err
}

// A source whose optional evidence breaks part-way returns the allowance it
// reserved, so a later source that fits is still fetched.
func TestOptionalEvidenceAllowanceIsRefundedOnAFailedFetch(t *testing.T) {
	declare := func(generation string, content []byte) RunArchiveEvidenceArtifact {
		sum := sha256.Sum256(content)
		return RunArchiveEvidenceArtifact{Name: "browser_har_" + generation + ".har", Kind: storage.EvidenceKindHARNative, Fidelity: "native", SourceGeneration: generation, Sequence: 1, Bytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	}
	broken, whole := []byte(strings.Repeat("a", 80)), []byte(strings.Repeat("b", 60))
	first, second := declare(strings.Repeat("1", 32), broken), declare(strings.Repeat("2", 32), whole)
	client := &partialArchiveClient{content: map[string][]byte{first.Name: broken, second.Name: whole}, failAfter: map[string]int{first.Name: 70}}
	// Both the transfer allowance and the run's HAR budget (bytes and
	// artifact count) are shared by every source.
	allowance := &remoteArchiveTransferBudget{remaining: 100}
	harBudget := &remoteHARBudget{remaining: 100, artifacts: 1}
	staged, omitted := fetchRemoteHARArtifacts(context.Background(), client, "run", "source-a", t.TempDir(), []RunArchiveEvidenceArtifact{first}, harBudget, allowance)
	if len(staged) != 0 || omitted[harOmittedTransfer] != 1 {
		t.Fatalf("broken fetch staged %+v omitted %v", staged, omitted)
	}
	staged, omitted = fetchRemoteHARArtifacts(context.Background(), client, "run", "source-b", t.TempDir(), []RunArchiveEvidenceArtifact{second}, harBudget, allowance)
	if len(staged) != 1 || len(omitted) != 0 || allowance.remaining != 40 || harBudget.remaining != 40 || harBudget.artifacts != 0 {
		t.Fatalf("a fitting source was refused after another failed: staged %+v omitted %v remaining %d/%d artifacts %d", staged, omitted, allowance.remaining, harBudget.remaining, harBudget.artifacts)
	}
}

// All optional HAR copies of one merge share one allowance, fixed at the
// first admission: a quarter of what the merge budget then had left.
func TestStagedHARCopiesShareOneOptionalAllowance(t *testing.T) {
	staging, working := t.TempDir(), t.TempDir()
	original := renameStagedHAR
	t.Cleanup(func() { renameStagedHAR = original })
	renameStagedHAR = func(string, string) error { return &os.LinkError{Op: "rename", Err: os.ErrInvalid} }
	var hars []stagedRemoteHAR
	for _, generation := range []string{strings.Repeat("5", 32), strings.Repeat("6", 32)} {
		content := []byte(strings.Repeat("h", 60))
		path := filepath.Join(staging, generation+".har")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		hars = append(hars, stagedRemoteHAR{entry: RunArchiveEvidenceArtifact{Name: "browser_har_" + generation + ".har", Kind: storage.EvidenceKindHARNative, Fidelity: "native", SourceIdentity: "source-a", SourceGeneration: generation, Sequence: 1, Bytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}, path: path})
	}
	accept := func(RunArchiveEvidenceArtifact) (string, error) { return storage.EvidenceReceiptAccepted, nil }
	budget := &archiveOutputBudget{root: working, remaining: 400}
	var manifest RunArchiveManifest
	if err := mergeRemoteHARArtifacts(working, &manifest, []remoteArchiveBundle{{target: "a", hars: hars}}, accept, budget); err != nil {
		t.Fatal(err)
	}
	if len(manifest.BrowserHARArtifacts) != 1 || manifest.BrowserHAROmitted[harOmittedArchiveQuota] != 1 || !manifest.BrowserHARIncomplete || budget.remaining != 340 {
		t.Fatalf("shared optional allowance: artifacts %+v omitted %v remaining %d", manifest.BrowserHARArtifacts, manifest.BrowserHAROmitted, budget.remaining)
	}
}

// An interactions sidecar that breaks part-way returns its reservation of the
// run-wide allowance, so a later source that fits is still fetched; a source
// the merge's optional allowance cannot hold is dropped before it is
// receipted.
func TestInteractionsAllowanceRefundAndMergeAdmission(t *testing.T) {
	record := func(gen string) []byte {
		data, err := json.Marshal(fakeInteractionEvidence(gen, 1))
		if err != nil {
			t.Fatal(err)
		}
		return append(data, '\n')
	}
	declare := func(content []byte) *RunArchiveEvidenceArtifact {
		sum := sha256.Sum256(content)
		return &RunArchiveEvidenceArtifact{Name: browserInteractionsFileName, Kind: storage.EvidenceKindInteraction, Fidelity: "native", SourceGeneration: interactionsEvidenceSource, Sequence: 1, Bytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	}
	a, b := record(strings.Repeat("a", 32)), record(strings.Repeat("b", 32))
	broken := &partialArchiveClient{content: map[string][]byte{browserInteractionsFileName: a}, failAfter: map[string]int{browserInteractionsFileName: len(a) / 2}}
	whole := &partialArchiveClient{content: map[string][]byte{browserInteractionsFileName: b}}
	allowance := &remoteArchiveTransferBudget{remaining: int64(len(a) + len(b) - 1)}
	if staged, reason := fetchRemoteInteractions(context.Background(), broken, "run", "source-a", t.TempDir(), declare(a), 1, allowance); staged != nil || reason != interactionsUnavailableTransfer {
		t.Fatalf("broken sidecar staged %+v reason %q", staged, reason)
	}
	staged, reason := fetchRemoteInteractions(context.Background(), whole, "run", "source-b", t.TempDir(), declare(b), 1, allowance)
	if staged == nil || reason != "" || allowance.remaining != int64(len(a)-1) {
		t.Fatalf("a fitting sidecar was refused after another failed: %q remaining %d", reason, allowance.remaining)
	}

	working := t.TempDir()
	accepted := 0
	accept := func(RunArchiveEvidenceArtifact) (string, error) {
		accepted++
		return storage.EvidenceReceiptAccepted, nil
	}
	var manifest RunArchiveManifest
	tight := &archiveOutputBudget{root: working, remaining: 4}
	if err := mergeRemoteInteractions(working, &manifest, []remoteArchiveBundle{{target: "b", interactions: staged}}, accept, tight); err != nil {
		t.Fatal(err)
	}
	if accepted != 0 || !manifest.BrowserInteractionsIncomplete || manifest.BrowserInteractionsDropped != 1 || manifest.BrowserInteractionsFile != "" {
		t.Fatalf("unadmitted sidecar: accepted %d manifest %+v", accepted, manifest)
	}
	if _, err := os.Stat(filepath.Join(working, browserInteractionsFileName)); !os.IsNotExist(err) {
		t.Fatal("an unadmitted sidecar reached the archive", err)
	}
}

package generatorrpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// publishedHAR writes a scrubbed HAR artifact into a HAR directory, as a
// browser session publishes one.
func publishedHAR(t *testing.T, dir, generation, content string) protocol.BrowserHARArtifact {
	t.Helper()
	path := filepath.Join(dir, "apexload_browser_"+generation+"_scrubbed.har")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return protocol.BrowserHARArtifact{Path: path, Generation: generation}
}

func harTestClient(t *testing.T, service *Service) *Client {
	t.Helper()
	listener := bufconn.Listen(2 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(conn)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// A run archive that is discarded (a failed recording) leaves its HARs in
// their HAR directory under its retention rather than deleting them with
// the archive.
func TestArchiveRecorderDiscardKeepsHARsInTheirDirectory(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newArchiveRecorder("har-generator", "har-discard", "har")
	if err != nil {
		t.Fatal(err)
	}
	artifact := publishedHAR(t, t.TempDir(), strings.Repeat("c", 32), `{"log":{"version":"1.2","entries":[]}}`)
	if err := writer.RecordBrowserHARArtifact(artifact); err != nil {
		t.Fatal(err)
	}
	if err := writer.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifact.Path); err != nil {
		t.Fatal("a discarded archive took its HAR with it", err)
	}
}

func TestArchiveRecorderMovesHARsAndAcknowledgementDeletesThem(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("har-generator", 10)
	writer, err := newArchiveRecorder("har-generator", "har-run", "har")
	if err != nil {
		t.Fatal(err)
	}
	harDir := t.TempDir()
	content := `{"log":{"version":"1.2","entries":[]}}`
	first := publishedHAR(t, harDir, strings.Repeat("a", 32), content)
	if err := writer.RecordBrowserHARArtifact(first); err != nil {
		t.Fatal(err)
	}
	// The HAR directory keeps its copy until the archive is finalized.
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatal("the HAR directory lost an artifact before its archive was final", err)
	}
	// A repeated generation is refused and counted, and the original stays.
	again := publishedHAR(t, harDir, strings.Repeat("a", 32), content)
	if err := writer.RecordBrowserHARArtifact(again); err == nil {
		t.Fatal("a repeated generation was moved")
	}
	if _, err := os.Stat(again.Path); err != nil {
		t.Fatal("a refused artifact left its HAR directory", err)
	}
	if err := writer.RecordBrowserHARArtifact(protocol.BrowserHARArtifact{Path: filepath.Join(harDir, "missing.har"), Generation: strings.Repeat("b", 32)}); err == nil {
		t.Fatal("an unreadable artifact was recorded")
	}
	if err := service.finalizeArchiveWriter(writer, "COMPLETE", time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatal("a finalized archive's HAR stayed in its HAR directory", err)
	}
	manifest := writer.manifest
	sum := sha256.Sum256([]byte(content))
	name := archiveHARName(strings.Repeat("a", 32))
	if len(manifest.BrowserHARArtifacts) != 1 || manifest.BrowserHARArtifacts[0].Name != name || manifest.BrowserHARArtifacts[0].SHA256 != hex.EncodeToString(sum[:]) || manifest.BrowserHARArtifacts[0].Kind != archiveHARKindNative || manifest.BrowserHARArtifacts[0].Fidelity != "native" {
		t.Fatalf("manifest HARs %+v", manifest.BrowserHARArtifacts)
	}
	if !manifest.BrowserHARIncomplete || manifest.BrowserHAROmitted[harOmittedDuplicate] != 1 || manifest.BrowserHAROmitted[harOmittedTransfer] != 1 {
		t.Fatalf("omissions %+v", manifest.BrowserHAROmitted)
	}
	found := false
	for _, artifact := range writer.ArtifactNames() {
		found = found || artifact == name
	}
	if !found {
		t.Fatal("the HAR is not a served artifact")
	}
	client := harTestClient(t, service)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var served bytes.Buffer
	if _, err := client.StreamArchiveArtifact(ctx, ArchiveArtifactRequest{TestID: "har-run", Name: name}, &served, 1<<20); err != nil || served.String() != content {
		t.Fatalf("served %q: %v", served.String(), err)
	}
	// A changed or unknown artifact is retained; the right hash deletes it,
	// and repeating the acknowledgement is harmless.
	result, err := client.AcknowledgeEvidence(ctx, EvidenceAcknowledgement{TestID: "har-run", Artifacts: []AcknowledgedEvidence{{Name: name, SHA256: strings.Repeat("0", 64)}}})
	if err != nil || len(result.Retained) != 1 || len(result.Deleted) != 0 {
		t.Fatalf("wrong hash: %+v %v", result, err)
	}
	for i := 0; i < 2; i++ {
		result, err = client.AcknowledgeEvidence(ctx, EvidenceAcknowledgement{TestID: "har-run", Artifacts: []AcknowledgedEvidence{{Name: name, SHA256: hex.EncodeToString(sum[:])}}})
		if err != nil || len(result.Deleted) != 1 || len(result.Retained) != 0 {
			t.Fatalf("acknowledgement %d: %+v %v", i, result, err)
		}
	}
	if _, err := client.StreamArchiveArtifact(ctx, ArchiveArtifactRequest{TestID: "har-run", Name: name}, &served, 1<<20); err == nil {
		t.Fatal("an acknowledged HAR is still served")
	}
	for _, bad := range []AcknowledgedEvidence{{Name: "transactions.jsonl", SHA256: hex.EncodeToString(sum[:])}, {Name: name, SHA256: "nothex"}} {
		if _, err := client.AcknowledgeEvidence(ctx, EvidenceAcknowledgement{TestID: "har-run", Artifacts: []AcknowledgedEvidence{bad}}); err == nil {
			t.Fatalf("acknowledged %+v", bad)
		}
	}
}

func TestArchiveRecorderHAROmissionsNeverFailTheRun(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newArchiveRecorder("har-limit", "har-limit-run", "har")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Discard()
	writer.mu.Lock()
	// The HAR fits the run limit but not the room left after reserving a
	// quarter of it for required evidence.
	writer.byteBudget.maxBytes = 12
	writer.mu.Unlock()
	artifact := publishedHAR(t, t.TempDir(), strings.Repeat("c", 32), "0123456789")
	if err := writer.RecordBrowserHARArtifact(artifact); err == nil {
		t.Fatal("a HAR over the optional budget was recorded")
	}
	if _, err := os.Stat(artifact.Path); err != nil {
		t.Fatal("a refused HAR left its directory", err)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.recordErr != nil || writer.manifest.BrowserHAROmitted[harOmittedArchiveQuota] != 1 || len(writer.manifest.BrowserHARArtifacts) != 0 {
		t.Fatalf("omission %+v err %v", writer.manifest.BrowserHAROmitted, writer.recordErr)
	}
}

func interactionEvidenceFixture(gen string, count int) *protocol.BrowserInteractionEvidence {
	evidence := &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen}
	for i := 0; i < count; i++ {
		evidence.Interactions = append(evidence.Interactions, protocol.BrowserInteraction{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen, InteractionID: gen + "-i" + strings.Repeat("1", i+1), FrameScope: "main_frame", Kind: "click", Provenance: protocol.InteractionProvenanceScripted, CapturedAt: time.Now().UTC(), Outcome: protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared}})
	}
	return evidence
}

func TestArchiveRecorderDeclaresInteractionsAndAcknowledgementDeletesThem(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("ix-generator", 10)
	writer, err := newArchiveRecorder("ix-generator", "ix-run", "ix")
	if err != nil {
		t.Fatal(err)
	}
	gen := strings.Repeat("d", 32)
	for i := 0; i < 2; i++ {
		if err := writer.RecordBrowserInteractionEvidence(interactionEvidenceFixture(gen, 2)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.RecordBrowserInteractionEvidence(&protocol.BrowserInteractionEvidence{Version: 9}); err == nil {
		t.Fatal("malformed interaction evidence recorded")
	}
	if err := service.finalizeArchiveWriter(writer, "COMPLETE", time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	artifact := writer.manifest.BrowserInteractionsArtifact
	if writer.manifest.BrowserInteractionRecordCount != 2 || artifact == nil || artifact.Name != archiveInteractionsName || artifact.Kind != archiveInteractionsKind || artifact.Bytes == 0 {
		t.Fatalf("declared %+v (%d records)", artifact, writer.manifest.BrowserInteractionRecordCount)
	}
	client := harTestClient(t, service)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var served bytes.Buffer
	if _, err := client.StreamArchiveArtifact(ctx, ArchiveArtifactRequest{TestID: "ix-run", Name: archiveInteractionsName}, &served, 1<<20); err != nil || int64(served.Len()) != artifact.Bytes {
		t.Fatalf("served %d bytes: %v", served.Len(), err)
	}
	result, err := client.AcknowledgeEvidence(ctx, EvidenceAcknowledgement{TestID: "ix-run", Artifacts: []AcknowledgedEvidence{{Name: archiveInteractionsName, SHA256: artifact.SHA256}}})
	if err != nil || len(result.Deleted) != 1 {
		t.Fatalf("acknowledgement %+v %v", result, err)
	}
}

func TestArchiveRecorderInteractionBudgetCountsDrops(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newArchiveRecorder("ix-budget", "ix-budget-run", "ix")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Discard()
	writer.mu.Lock()
	writer.interactionBytes = maxArchiveInteractionsRunBytes
	writer.mu.Unlock()
	if err := writer.RecordBrowserInteractionEvidence(interactionEvidenceFixture(strings.Repeat("e", 32), 3)); err == nil {
		t.Fatal("evidence over the sidecar budget was recorded")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.recordErr != nil || !writer.manifest.BrowserInteractionsIncomplete || writer.manifest.BrowserInteractionsDropped != 3 {
		t.Fatalf("drop accounting %+v err %v", writer.manifest, writer.recordErr)
	}
}

// Optional evidence never takes more than a quarter of the run's byte limit,
// whenever it arrives, and an admitted length that was not written is
// returned.
func TestOptionalEvidenceKeepsThreeQuartersForRequiredEvidence(t *testing.T) {
	budget := &generatorArchiveByteBudget{maxBytes: 100}
	if !budget.admitOptional(20) || budget.admitOptional(6) {
		t.Fatal("optional evidence passed a quarter of the limit")
	}
	budget.releaseOptional(20)
	if !budget.admitOptional(25) || budget.admitOptional(1) {
		t.Fatal("released optional length was not returned")
	}
	full := &generatorArchiveByteBudget{maxBytes: 100, written: 90}
	if full.admitOptional(11) {
		t.Fatal("optional evidence admitted past the limit")
	}
}

// A finalized archive whose registration fails is deleted, so its HARs'
// HAR-directory copies must stay: they are retired only once the archive is
// registered.
func TestArchiveHARsStayWhenRegistrationFails(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("har-generator", 10)
	writer, err := newArchiveRecorder("har-generator", "har-unregistered", "har")
	if err != nil {
		t.Fatal(err)
	}
	artifact := publishedHAR(t, t.TempDir(), strings.Repeat("d", 32), `{"log":{"version":"1.2","entries":[]}}`)
	if err := writer.RecordBrowserHARArtifact(artifact); err != nil {
		t.Fatal(err)
	}
	// The run directory registration expects is not the one finalized.
	other, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writer.runIdentity = other
	if err := service.finalizeArchiveWriter(writer, "COMPLETE", time.Now().UTC(), ""); err == nil {
		t.Fatal("a replaced run directory was registered")
	}
	if _, err := os.Stat(artifact.Path); err != nil {
		t.Fatal("an unregistered archive retired its HAR's only other copy", err)
	}
}

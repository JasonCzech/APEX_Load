package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// writeArchiveArtifactFile writes content under root and returns its SHA-256 so a
// manifest can record the "trusted" hash captured at Finalize time.
func writeArchiveArtifactFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write artifact %s: %v", name, err)
	}
	sum, err := sha256File(path)
	if err != nil {
		t.Fatalf("hash artifact %s: %v", name, err)
	}
	return sum
}

func TestVerifyRunArchiveIntegrityCleanArchive(t *testing.T) {
	root := t.TempDir()
	manifest := &RunArchiveManifest{
		TestID: "test-clean",
		Artifacts: []RunArchiveArtifact{
			{Name: "transactions.jsonl", SHA256: writeArchiveArtifactFile(t, root, "transactions.jsonl", `{"a":1}`)},
			{Name: "resources.jsonl", SHA256: writeArchiveArtifactFile(t, root, "resources.jsonl", `{"b":2}`)},
			{Name: "interval_summaries.jsonl", SHA256: writeArchiveArtifactFile(t, root, "interval_summaries.jsonl", `{"c":3}`)},
		},
	}
	record := &storage.RunArchiveRecord{TestID: "test-clean", ArchivePath: root}

	report := verifyRunArchiveIntegrity(record, manifest)

	if !report.Verified {
		t.Fatalf("expected verified archive, got status=%q summary=%q", report.Status, report.Summary)
	}
	if report.Status != integrityStatusVerified {
		t.Fatalf("status = %q, want %q", report.Status, integrityStatusVerified)
	}
	if report.Matched != 3 || report.Mismatched != 0 || report.Missing != 0 {
		t.Fatalf("counts matched=%d mismatched=%d missing=%d, want 3/0/0", report.Matched, report.Mismatched, report.Missing)
	}
	if report.TestID != "test-clean" {
		t.Fatalf("test_id = %q, want test-clean", report.TestID)
	}
	for _, artifact := range report.Artifacts {
		if artifact.Status != integrityArtifactMatch {
			t.Fatalf("artifact %s status = %q, want match", artifact.Name, artifact.Status)
		}
		if artifact.ActualSHA256 != artifact.ExpectedSHA256 {
			t.Fatalf("artifact %s actual %q != expected %q", artifact.Name, artifact.ActualSHA256, artifact.ExpectedSHA256)
		}
	}
}

func TestVerifyRunArchiveIntegrityTamperedArtifact(t *testing.T) {
	root := t.TempDir()
	manifest := &RunArchiveManifest{
		TestID: "test-tampered",
		Artifacts: []RunArchiveArtifact{
			{Name: "transactions.jsonl", SHA256: writeArchiveArtifactFile(t, root, "transactions.jsonl", `{"a":1}`)},
			{Name: "resources.jsonl", SHA256: writeArchiveArtifactFile(t, root, "resources.jsonl", `{"b":2}`)},
		},
	}
	record := &storage.RunArchiveRecord{TestID: "test-tampered", ArchivePath: root}

	// Tamper with one artifact after its hash was recorded in the manifest.
	if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), []byte(`{"a":999}`), 0o644); err != nil {
		t.Fatalf("tamper artifact: %v", err)
	}

	report := verifyRunArchiveIntegrity(record, manifest)

	if report.Verified {
		t.Fatal("expected tampered archive to fail verification")
	}
	if report.Status != integrityStatusTampered {
		t.Fatalf("status = %q, want %q", report.Status, integrityStatusTampered)
	}
	if report.Mismatched != 1 || report.Matched != 1 {
		t.Fatalf("counts matched=%d mismatched=%d, want 1/1", report.Matched, report.Mismatched)
	}
	var tampered *RunArchiveIntegrityArtifact
	for i := range report.Artifacts {
		if report.Artifacts[i].Name == "transactions.jsonl" {
			tampered = &report.Artifacts[i]
		}
	}
	if tampered == nil {
		t.Fatal("transactions.jsonl missing from integrity report")
	}
	if tampered.Status != integrityArtifactMismatch {
		t.Fatalf("tampered artifact status = %q, want mismatch", tampered.Status)
	}
	if tampered.ActualSHA256 == tampered.ExpectedSHA256 || tampered.ActualSHA256 == "" {
		t.Fatalf("expected recomputed hash to differ from recorded, got actual=%q expected=%q", tampered.ActualSHA256, tampered.ExpectedSHA256)
	}
}

func TestVerifyRunArchiveIntegrityMissingAndUnverifiable(t *testing.T) {
	root := t.TempDir()
	manifest := &RunArchiveManifest{
		TestID: "test-missing",
		Artifacts: []RunArchiveArtifact{
			{Name: "transactions.jsonl", SHA256: writeArchiveArtifactFile(t, root, "transactions.jsonl", `{"a":1}`)},
			// Recorded with a hash but the file never gets written → missing (corrupt).
			{Name: "resources.jsonl", SHA256: "deadbeef"},
			// Recorded without a hash → unverifiable, never a false tamper.
			{Name: "interval_summaries.jsonl", SHA256: ""},
		},
	}
	record := &storage.RunArchiveRecord{TestID: "test-missing", ArchivePath: root}

	report := verifyRunArchiveIntegrity(record, manifest)

	if report.Verified {
		t.Fatal("expected corrupt archive to fail verification")
	}
	if report.Status != integrityStatusCorrupt {
		t.Fatalf("status = %q, want %q", report.Status, integrityStatusCorrupt)
	}
	if report.Missing != 1 || report.Matched != 1 || report.Unverifiable != 1 {
		t.Fatalf("counts matched=%d missing=%d unverifiable=%d, want 1/1/1", report.Matched, report.Missing, report.Unverifiable)
	}
	statuses := map[string]string{}
	for _, artifact := range report.Artifacts {
		statuses[artifact.Name] = artifact.Status
	}
	if statuses["resources.jsonl"] != integrityArtifactMissing {
		t.Fatalf("resources.jsonl status = %q, want missing", statuses["resources.jsonl"])
	}
	if statuses["interval_summaries.jsonl"] != integrityArtifactUnverifiable {
		t.Fatalf("interval_summaries.jsonl status = %q, want unverifiable", statuses["interval_summaries.jsonl"])
	}
}

func TestVerifyRunArchiveIntegrityVerifiedPartial(t *testing.T) {
	root := t.TempDir()
	manifest := &RunArchiveManifest{
		TestID: "test-partial",
		Artifacts: []RunArchiveArtifact{
			{Name: "transactions.jsonl", SHA256: writeArchiveArtifactFile(t, root, "transactions.jsonl", `{"a":1}`)},
			{Name: "resources.jsonl", SHA256: writeArchiveArtifactFile(t, root, "resources.jsonl", `{"b":2}`)},
			// Recorded without a hash (e.g. a legacy/external manifest) → cannot be
			// checked, so the overall verdict must not claim a fully-clean pass.
			{Name: "interval_summaries.jsonl", SHA256: ""},
		},
	}
	record := &storage.RunArchiveRecord{TestID: "test-partial", ArchivePath: root}

	report := verifyRunArchiveIntegrity(record, manifest)

	if report.Status != integrityStatusVerifiedPartial {
		t.Fatalf("status = %q, want %q", report.Status, integrityStatusVerifiedPartial)
	}
	if report.Verified {
		t.Fatal("partial verification must not report Verified=true (tamper-evidence overstatement)")
	}
	if report.Matched != 2 || report.Unverifiable != 1 || report.Mismatched != 0 || report.Missing != 0 {
		t.Fatalf("counts matched=%d unverifiable=%d mismatched=%d missing=%d, want 2/1/0/0",
			report.Matched, report.Unverifiable, report.Mismatched, report.Missing)
	}
	if !strings.Contains(report.Summary, "had no recorded hash") {
		t.Fatalf("summary = %q, want it to disclose the unchecked artifact", report.Summary)
	}
}

func TestVerifyRunArchiveIntegrityRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	manifest := &RunArchiveManifest{
		TestID: "test-traversal",
		Artifacts: []RunArchiveArtifact{
			{Name: "transactions.jsonl", SHA256: writeArchiveArtifactFile(t, root, "transactions.jsonl", `{"a":1}`)},
			// A manifest artifact name that escapes the archive root must never be
			// hashed off-root; it is reported missing, not silently matched.
			{Name: "../outside.jsonl", SHA256: "deadbeef"},
		},
	}
	record := &storage.RunArchiveRecord{TestID: "test-traversal", ArchivePath: root}

	report := verifyRunArchiveIntegrity(record, manifest)

	if report.Status != integrityStatusCorrupt {
		t.Fatalf("status = %q, want %q", report.Status, integrityStatusCorrupt)
	}
	var traversal *RunArchiveIntegrityArtifact
	for i := range report.Artifacts {
		if report.Artifacts[i].Name == "../outside.jsonl" {
			traversal = &report.Artifacts[i]
		}
	}
	if traversal == nil {
		t.Fatal("traversal artifact missing from report")
	}
	if traversal.Status != integrityArtifactMissing {
		t.Fatalf("traversal artifact status = %q, want missing", traversal.Status)
	}
	if traversal.ActualSHA256 != "" {
		t.Fatalf("traversal artifact must not be hashed, got actual=%q", traversal.ActualSHA256)
	}
	if !strings.Contains(traversal.Detail, "path is invalid") {
		t.Fatalf("traversal detail = %q, want path-invalid explanation", traversal.Detail)
	}
}

func TestVerifyRunArchiveNilManifest(t *testing.T) {
	report := verifyRunArchiveIntegrity(nil, nil)
	if report.Verified || report.Status != integrityStatusUnverifiable {
		t.Fatalf("nil manifest: verified=%v status=%q, want false/unverifiable", report.Verified, report.Status)
	}
}

// TestArchiveVerifyEndpoint drives the full endpoint against a real archive
// produced by running a test to completion (so the manifest carries genuine
// SHA-256 hashes recorded at Finalize), then re-verifies it clean and tampered.
func TestArchiveVerifyEndpoint(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store})
	protocolName := "archive-verify-stub-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol { return &archiveStubProtocol{} })

	server.runner.StoreScript(&dsl.Script{
		ID:       "script-archive-verify",
		Name:     "Archive Verify Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Archive Verify Run",
		Duration: "2200ms",
		ScriptID: "script-archive-verify",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := server.runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}
	completed := waitForTestStatus(t, server.runner, testRun.ID, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("expected archive metadata after completion")
	}

	verify := func(t *testing.T) RunArchiveIntegrityReport {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/verify", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("verify status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var report RunArchiveIntegrityReport
		if err := json.Unmarshal(resp.Body.Bytes(), &report); err != nil {
			t.Fatalf("unmarshal integrity report: %v", err)
		}
		return report
	}

	t.Run("clean archive verifies", func(t *testing.T) {
		report := verify(t)
		if !report.Verified || report.Status != integrityStatusVerified {
			t.Fatalf("clean verify = %q (verified=%v) summary=%q, want verified", report.Status, report.Verified, report.Summary)
		}
		if report.Matched == 0 || report.Mismatched != 0 || report.Missing != 0 {
			t.Fatalf("clean counts matched=%d mismatched=%d missing=%d", report.Matched, report.Mismatched, report.Missing)
		}
	})

	t.Run("tampered artifact is detected", func(t *testing.T) {
		// Append bytes to a recorded artifact, mutating its content after the
		// manifest captured its hash at Finalize.
		target := filepath.Join(completed.Archive.ArchivePath, "transactions.jsonl")
		file, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open archived artifact for tamper: %v", err)
		}
		if _, err := file.WriteString("\n{\"tampered\":true}\n"); err != nil {
			file.Close()
			t.Fatalf("tamper artifact: %v", err)
		}
		file.Close()

		report := verify(t)
		if report.Verified || report.Status != integrityStatusTampered {
			t.Fatalf("tampered verify = %q (verified=%v), want tampered", report.Status, report.Verified)
		}
		if report.Mismatched < 1 {
			t.Fatalf("expected at least one mismatch, got %d", report.Mismatched)
		}
		var found bool
		for _, artifact := range report.Artifacts {
			if artifact.Name == "transactions.jsonl" {
				found = true
				if artifact.Status != integrityArtifactMismatch {
					t.Fatalf("transactions.jsonl status = %q, want mismatch", artifact.Status)
				}
			}
		}
		if !found {
			t.Fatal("transactions.jsonl not present in integrity report")
		}
	})
}

func TestArchiveVerifyEndpointMissingArchive(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	// Unknown test → 404 (test not found).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/does-not-exist/archive/verify", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("unknown test verify status = %d, want %d", resp.Code, http.StatusNotFound)
	}
	if !strings.Contains(resp.Body.String(), "test not found") {
		t.Fatalf("unknown test body = %q, want test-not-found", resp.Body.String())
	}

	// Known test with no archive (created, never run) → 404 (archive not found).
	server.runner.mu.Lock()
	server.runner.tests["no-archive"] = &ManagedTest{ID: "no-archive", Name: "No Archive", Status: "COMPLETE"}
	server.runner.mu.Unlock()

	req = httptest.NewRequest(http.MethodGet, "/api/v1/tests/no-archive/archive/verify", nil)
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("no-archive verify status = %d, want %d", resp.Code, http.StatusNotFound)
	}
	if !strings.Contains(resp.Body.String(), "archive not found") {
		t.Fatalf("no-archive body = %q, want archive-not-found", resp.Body.String())
	}
}

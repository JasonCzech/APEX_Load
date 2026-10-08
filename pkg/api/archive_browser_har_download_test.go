package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/storage"
)

const harDownloadNativeBody = "nativeResponseBodyQxZr"

// harDownloadFixture archives a run with one native HAR (its context's
// entries are left out of the synthetic HAR) and the synthetic fixture's
// transactions, behind trusted-header authentication.
func harDownloadFixture(t *testing.T) (*Server, string, *storage.RunArchiveRecord) {
	t.Helper()
	server, store := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	id := "har-download-run"
	root := t.TempDir()
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	var transactions bytes.Buffer
	for _, row := range syntheticArchiveRows(t) {
		transactions.Write(bytes.ReplaceAll(row, []byte(`"run-1"`), []byte(`"`+id+`"`)))
		transactions.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), transactions.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	generation := strings.Repeat("a", 32)
	harName := "browser_har_" + generation + ".har"
	native := []byte(`{"log":{"version":"1.2","creator":{"name":"Playwright","version":"1"},"entries":[{"response":{"content":{"text":"` + harDownloadNativeBody + `"}}}]}}`)
	if err := os.WriteFile(filepath.Join(root, harName), native, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(native)
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: id, Status: "COMPLETE", CreatedAt: start, CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &start}, TransactionsFile: "transactions.jsonl", TransactionCount: 5,
		BrowserHARArtifacts: []RunArchiveEvidenceArtifact{{Name: harName, Kind: storage.EvidenceKindHARNative, Fidelity: "native", SourceGeneration: generation, Sequence: 1, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(native)), EvidenceGeneration: syntheticCoveredGeneration}},
		Artifacts: []RunArchiveArtifact{{Name: "transactions.jsonl", Kind: "dataset", Format: "jsonl", RecordType: "transactions", Count: 5}, {Name: harName, Kind: "evidence", Format: "har", RecordType: runArchiveHARRecordType, SizeBytes: int64(len(native)), SHA256: hex.EncodeToString(sum[:])}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", Groups: []any{}, StartTime: start}); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), CreatedAt: start, CompletedAt: end, TransactionCount: 5}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	server.runner.archives[id] = record
	return server, id, record
}

func harDownloadRequest(t *testing.T, server *Server, path, user, projectRoles string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Auth-Request-User", user)
	req.Header.Set("X-APEXLoad-Project-Roles", projectRoles)
	reply := httptest.NewRecorder()
	server.mux.ServeHTTP(reply, req)
	return reply
}

var (
	harViewer       = storage.DefaultProjectID + "=" + RoleViewer
	harEditor       = storage.DefaultProjectID + "=" + RoleEditor
	harOtherEditor  = "another-project=" + RoleEditor
	harNativeName   = "browser_har_" + strings.Repeat("a", 32) + ".har"
	harDownloadBase = "/api/v1/tests/har-download-run/archive/browser/har"
)

func TestRunArchiveHARListShowsNativeAndSyntheticToViewers(t *testing.T) {
	server, _, _ := harDownloadFixture(t)
	for _, tc := range []struct {
		roles       string
		canDownload bool
	}{{harViewer, false}, {harEditor, true}} {
		reply := harDownloadRequest(t, server, harDownloadBase, "har-user", tc.roles)
		if reply.Code != http.StatusOK {
			t.Fatalf("%s list: %d %s", tc.roles, reply.Code, reply.Body.String())
		}
		var list BrowserHARArchiveList
		if err := json.Unmarshal(reply.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Artifacts) != 2 || list.CanDownload != tc.canDownload || list.DownloadRole != RoleEditor {
			t.Fatalf("%s list %+v", tc.roles, list)
		}
		native, synthetic := list.Artifacts[0], list.Artifacts[1]
		if native.Name != harNativeName || native.Fidelity != "native" || native.EvidenceGeneration != syntheticCoveredGeneration || native.Bytes == nil || native.SHA256 == "" {
			t.Fatalf("native %+v", native)
		}
		if synthetic.Name != RunArchiveSyntheticHARName || synthetic.Fidelity != harsynth.Fidelity || !synthetic.Available || !synthetic.Derived || synthetic.CaptureMode != harsynth.CaptureRetainedNetworkEntries {
			t.Fatalf("synthetic %+v", synthetic)
		}
	}
	if reply := harDownloadRequest(t, server, harDownloadBase, "har-user", harOtherEditor); reply.Code != http.StatusForbidden {
		t.Fatalf("another project's editor listed HARs: %d", reply.Code)
	}
}

func TestRunArchiveHARDownloadsNeedEditorAccess(t *testing.T) {
	server, _, record := harDownloadFixture(t)
	nativePath := harDownloadBase + "/" + harNativeName
	syntheticPath := harDownloadBase + "/" + RunArchiveSyntheticHARName
	for _, path := range []string{nativePath, syntheticPath} {
		if reply := harDownloadRequest(t, server, path, "har-viewer", harViewer); reply.Code != http.StatusForbidden {
			t.Fatalf("a viewer downloaded %s: %d", path, reply.Code)
		}
		if reply := harDownloadRequest(t, server, path, "har-other", harOtherEditor); reply.Code != http.StatusForbidden {
			t.Fatalf("another project's editor downloaded %s: %d", path, reply.Code)
		}
	}
	reply := harDownloadRequest(t, server, nativePath, "har-editor", harEditor)
	if reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" || !strings.Contains(reply.Body.String(), harDownloadNativeBody) || !strings.HasPrefix(reply.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("native download %d %v %s", reply.Code, reply.Header(), reply.Body.String())
	}
	reply = harDownloadRequest(t, server, syntheticPath, "har-editor", harEditor)
	if reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != harsynth.Fidelity || reply.Header().Get("X-APEXLoad-HAR-Completeness") != harsynth.CompletenessPartial {
		t.Fatalf("synthetic download %d %v", reply.Code, reply.Header())
	}
	syntheticHARSchemaValidate(t, reply.Body.Bytes())
	for _, secret := range []string{"urlTokenQxZrTm", "headerTokenQxZrTmWp", "secretBodyQxZr", "privateDetailQxZr"} {
		if strings.Contains(reply.Body.String(), secret) {
			t.Fatalf("synthetic download retains %q", secret)
		}
	}
	var doc syntheticHARDoc
	if err := json.Unmarshal(reply.Body.Bytes(), &doc); err != nil || len(doc.Log.Entries) != 4 {
		t.Fatalf("synthetic entries %d %v", len(doc.Log.Entries), err)
	}
	// Only archived HARs are served: no working or published HAR-directory
	// name, other archive file or path escape.
	for _, name := range []string{"apexload_browser_" + strings.Repeat("a", 32) + "_scrubbed.har", "manifest.json", "transactions.jsonl", "..%2Fmanifest.json", "browser_har_" + strings.Repeat("b", 32) + ".har"} {
		if reply := harDownloadRequest(t, server, harDownloadBase+"/"+name, "har-editor", harEditor); reply.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", name, reply.Code)
		}
	}
	// A native HAR that differs from its manifest is refused.
	if err := os.WriteFile(filepath.Join(record.ArchivePath, harNativeName), []byte(`{"log":{"entries":["changed"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if reply := harDownloadRequest(t, server, nativePath, "har-editor", harEditor); reply.Code != http.StatusConflict {
		t.Fatalf("a changed native HAR was served: %d", reply.Code)
	}
}

func harZipEntries(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[file.Name] = data
	}
	return out
}

func TestRunArchiveZipIncludesHARsOnlyForEditors(t *testing.T) {
	server, id, _ := harDownloadFixture(t)
	path := "/api/v1/tests/" + id + "/archive"
	manifestOf := func(entries map[string][]byte) RunArchiveManifest {
		t.Helper()
		var manifest RunArchiveManifest
		for name, data := range entries {
			if strings.HasSuffix(name, "manifest.json") && !strings.Contains(name, "test") {
				if err := json.Unmarshal(data, &manifest); err != nil {
					t.Fatal(err)
				}
			}
		}
		return manifest
	}
	containsHAR := func(entries map[string][]byte) bool {
		for name, data := range entries {
			if strings.HasSuffix(name, harNativeName) {
				return bytes.Contains(data, []byte(harDownloadNativeBody))
			}
		}
		return false
	}
	reply := harDownloadRequest(t, server, path, "har-viewer", harViewer)
	if reply.Code != http.StatusOK {
		t.Fatalf("viewer export %d %s", reply.Code, reply.Body.String())
	}
	entries := harZipEntries(t, reply.Body.Bytes())
	if manifest := manifestOf(entries); containsHAR(entries) || len(manifest.BrowserHARArtifacts) != 0 || manifest.BrowserHAROmitted["safe_export"] != 1 {
		t.Fatalf("the default export carries HARs: %+v", manifest.BrowserHAROmitted)
	}
	if reply := harDownloadRequest(t, server, path+"?include=har", "har-viewer", harViewer); reply.Code != http.StatusForbidden {
		t.Fatalf("a viewer exported HARs: %d", reply.Code)
	}
	if reply := harDownloadRequest(t, server, path+"?include=everything", "har-editor", harEditor); reply.Code != http.StatusBadRequest {
		t.Fatalf("an unknown include: %d", reply.Code)
	}
	reply = harDownloadRequest(t, server, path+"?include=har", "har-editor", harEditor)
	if reply.Code != http.StatusOK {
		t.Fatalf("editor HAR export %d %s", reply.Code, reply.Body.String())
	}
	entries = harZipEntries(t, reply.Body.Bytes())
	manifest := manifestOf(entries)
	if !containsHAR(entries) || len(manifest.BrowserHARArtifacts) != 1 || manifest.BrowserHAROmitted["safe_export"] != 0 || manifest.BrowserHARIncomplete {
		t.Fatalf("the HAR export lacks its HAR: %+v %+v", manifest.BrowserHARArtifacts, manifest.BrowserHAROmitted)
	}
	listed := false
	for _, artifact := range manifest.Artifacts {
		listed = listed || artifact.Name == harNativeName && artifact.RecordType == runArchiveHARRecordType
	}
	if !listed {
		t.Fatal("the HAR export's inventory does not list its HAR")
	}
}

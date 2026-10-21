package api

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestLocalArchiveHARCaptureOmissionsSurviveFinalization(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "capture-omissions", Name: "capture"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (combinedResultSink{}).RecordBrowserHARCaptureOmissions(map[string]int64{protocol.BrowserHAROmittedCaptureFailed: 1}); err == nil {
		t.Fatal("missing combined sink accepted losses")
	}
	counts := map[string]int64{protocol.BrowserHAROmittedCaptureFailed: 2}
	if err := (combinedResultSink{resultOnlySink{}, writer}).RecordBrowserHARCaptureOmissions(counts); err != nil {
		t.Fatal(err)
	}
	counts[protocol.BrowserHAROmittedCaptureFailed] = 90
	if err := writer.RecordBrowserHARCaptureOmissions(map[string]int64{"invalid": 1}); err == nil {
		t.Fatal("accepted arbitrary reason")
	}
	record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.BrowserHARIncomplete || manifest.BrowserHAROmitted[protocol.BrowserHAROmittedCaptureFailed] != 2 || len(manifest.BrowserHARArtifacts) != 0 {
		t.Fatalf("omissions=%v incomplete=%t", manifest.BrowserHAROmitted, manifest.BrowserHARIncomplete)
	}
	if err := writer.RecordBrowserHARCaptureOmissions(counts); err == nil {
		t.Fatal("closed archive accepted a mutation")
	}
}
func TestRemoteArchiveHARCaptureOmissionsSurviveTransfer(t *testing.T) {
	tr, _, run, backend := collectionCacheFixture(t, "a")
	client := run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)
	client.mu.Lock()
	path := client.artifacts[runArchiveManifestFileName]
	client.mu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["browser_har_incomplete"] = true
	manifest["browser_har_omitted"] = map[string]int64{protocol.BrowserHAROmittedCaptureFailed: 3}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "", time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if !result.BrowserHARIncomplete || result.BrowserHAROmitted[protocol.BrowserHAROmittedCaptureFailed] != 3 || len(result.BrowserHARArtifacts) != 0 {
		t.Fatal("capture loss was omitted", result.BrowserHAROmitted)
	}
}

package generatorrpc

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestGeneratorArchiveHARCaptureOmissionsSurviveFinalization(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	if err := (&combinedSink{}).RecordBrowserHARCaptureOmissions(map[string]int64{protocol.BrowserHAROmittedCaptureFailed: 1}); err == nil {
		t.Fatal("missing combined sink accepted losses")
	}
	writer, err := newArchiveRecorder("generator", "capture-loss", "capture")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&combinedSink{sinks: []engine.ResultSink{writer}}).RecordBrowserHARCaptureOmissions(map[string]int64{protocol.BrowserHAROmittedOwnerUnretired: 1}); err != nil {
		t.Fatal(err)
	}
	if err := writer.RecordBrowserHARCaptureOmissions(map[string]int64{"unknown": 1}); err == nil {
		t.Fatal("arbitrary reason accepted")
	}
	if _, err := writer.Finalize("COMPLETE", time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	if !writer.manifest.BrowserHARIncomplete || writer.manifest.BrowserHAROmitted[protocol.BrowserHAROmittedOwnerUnretired] != 1 || len(writer.manifest.BrowserHARArtifacts) != 0 {
		t.Fatal(writer.manifest.BrowserHAROmitted)
	}
	if err := writer.RecordBrowserHARCaptureOmissions(map[string]int64{protocol.BrowserHAROmittedCaptureFailed: 1}); err == nil {
		t.Fatal("closed archive mutated")
	}
}

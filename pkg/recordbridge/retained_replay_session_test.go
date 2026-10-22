package recordbridge

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestRestoreRetainedReplaySession(t *testing.T) {
	now := time.Now().UTC()
	fixture := func() *BrowserReplaySession {
		return &BrowserReplaySession{ID: "replay", ProjectID: "project", Status: BrowserReplayCompleted, CompletedAt: &now, harCopy: "/private/foreign.har", FinalReport: &engine.ReplayReport{ReplaySessionID: "replay", Success: true, FinalVariables: map[string]string{"count": "3"}}}
	}
	for _, status := range []string{BrowserReplayCompleted, BrowserReplayFailed, BrowserReplayStopped} {
		session := fixture()
		session.Status = status
		got, err := RestoreRetainedReplaySession(session, "replay", "project")
		if err != nil {
			t.Fatal(err)
		}
		if !got.WorkerSettled() || got.ArtifactDurability() != "durable" || got.harCopy != "" || got.reservation != nil || got.cancel != nil {
			t.Fatal("restored execution or file capability")
		}
		got.FinalReport.FinalVariables["count"] = "changed"
		if session.FinalReport.FinalVariables["count"] != "3" {
			t.Fatal("report aliases input")
		}
	}
	for _, kind := range []string{"nil", "id", "project", "running", "timestamp", "zero-timestamp", "report", "report-id"} {
		t.Run(kind, func(t *testing.T) {
			session := fixture()
			switch kind {
			case "nil":
				session = nil
			case "id":
				session.ID = "another"
			case "project":
				session.ProjectID = "another"
			case "running":
				session.Status = BrowserReplayRunning
			case "timestamp":
				session.CompletedAt = nil
			case "zero-timestamp":
				session.CompletedAt = &time.Time{}
			case "report":
				session.FinalReport = nil
			case "report-id":
				session.FinalReport.ReplaySessionID = "another"
			}
			got, err := RestoreRetainedReplaySession(session, "replay", "project")
			if got != nil || !errors.Is(err, ErrRetainedReplayUnavailable) {
				t.Fatalf("accepted invalid session: %+v %v", got, err)
			}
		})
	}
}

func TestRetainedNativeReplayCannotBecomeSynthetic(t *testing.T) {
	now := time.Now().UTC()
	retained, err := RestoreRetainedReplaySession(&BrowserReplaySession{ID: "replay", ProjectID: "project", Status: BrowserReplayCompleted, CompletedAt: &now, FinalReport: &engine.ReplayReport{ReplaySessionID: "replay"}}, "replay", "project")
	if err != nil {
		t.Fatal(err)
	}
	manager := &BrowserReplayManager{}
	har, ok, err := manager.ReplayHARFor(retained)
	har.Close()
	if ok || !errors.Is(err, ErrRetainedReplayUnavailable) {
		t.Fatalf("durable native replay synthesized: %v %v", ok, err)
	}
	// Ordinary local-only bridges retain their disclosed synthetic behavior.
	retained.artifactDurability = "local_only"
	har, ok, err = manager.ReplayHARFor(retained)
	defer har.Close()
	if err != nil || !ok || har.Fidelity == "native" {
		t.Fatalf("local synthetic contract changed: %v %v", ok, err)
	}
}

func TestRestoreRetainedReplayPreservesHistoricalEvidence(t *testing.T) {
	now := time.Now().UTC()
	// A historical retained value may exceed today's per-field policy. Recovery
	// preserves the verified bounded envelope instead of reapplying that policy.
	value := strings.Repeat("evidence", 10000)
	session := &BrowserReplaySession{ID: "replay", ProjectID: "project", Status: BrowserReplayCompleted, CompletedAt: &now, FinalReport: &engine.ReplayReport{ReplaySessionID: "replay", Events: []engine.ReplayActionEvent{{ProtocolEvidence: map[string]any{"historical": value}}}}}
	restored, err := RestoreRetainedReplaySession(session, "replay", "project")
	if err != nil {
		t.Fatal(err)
	}
	if restored.FinalReport.Events[0].ProtocolEvidence["historical"] != value {
		t.Fatal("historical evidence silently truncated")
	}
}

func TestReplayCaptureOmissionOwnershipAndRecovery(t *testing.T) {
	now := time.Now().UTC()
	source := &engine.ReplayReport{ReplaySessionID: "replay", NativeHARCaptureOmissions: map[string]int64{"native_capture_failed": 2}}
	copy := CloneBrowserReplayReport(source)
	copy.NativeHARCaptureOmissions["native_capture_failed"] = 8
	if source.NativeHARCaptureOmissions["native_capture_failed"] != 2 {
		t.Fatal("clone aliases capture omissions")
	}
	restored, err := RestoreRetainedReplaySession(&BrowserReplaySession{ID: "replay", ProjectID: "project", Status: BrowserReplayCompleted, CompletedAt: &now, FinalReport: source}, "replay", "project")
	if err != nil {
		t.Fatal(err)
	}
	if restored.FinalReport.NativeHARCaptureOmissions["native_capture_failed"] != 2 {
		t.Fatal("recovery dropped omissions")
	}
	restored.FinalReport.NativeHARCaptureOmissions["native_capture_failed"] = 9
	if source.NativeHARCaptureOmissions["native_capture_failed"] != 2 {
		t.Fatal("recovery aliases capture omissions")
	}
}

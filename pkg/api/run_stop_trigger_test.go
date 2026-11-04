package api

import (
	"testing"
	"time"
)

func TestRecordAutoStopTriggerKeepsTheFirstStopOfARunningTest(t *testing.T) {
	tr := &TestRunner{}
	test := &ManagedTest{ID: "run", Status: "QUEUED"}
	tr.recordAutoStopTrigger(test, runStopRuleGoal, "ignored before RUNNING")
	if got := tr.autoStopTriggerOf(test); got != nil {
		t.Fatalf("trigger recorded for a test that was not running: %+v", got)
	}
	test.Status = "RUNNING"
	tr.recordAutoStopTrigger(test, runStopRuleSustainedP99, "  sustained p99 latency threshold breached ")
	tr.recordAutoStopTrigger(test, runStopRuleErrorRate, "later")
	got := tr.autoStopTriggerOf(test)
	if got == nil || got.Rule != runStopRuleSustainedP99 || got.Reason != "sustained p99 latency threshold breached" || got.At.IsZero() {
		t.Fatalf("trigger = %+v", got)
	}
	got.Rule = "mutated"
	if tr.autoStopTriggerOf(test).Rule != runStopRuleSustainedP99 {
		t.Fatal("autoStopTriggerOf returned the run's own trigger, not a copy")
	}
}

func TestLocalArchiveManifestKeepsTheStopTrigger(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	w, err := newRunArchiveWriter(&ManagedTest{ID: "stop-trigger"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	w.SetStopTrigger(&RunStopTrigger{Rule: runStopRuleGoal, Reason: "p95 latency SLA breached", At: at})
	record, err := w.Finalize("COMPLETE", at.Add(time.Minute), "", true, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.StopTrigger == nil || manifest.StopTrigger.Rule != runStopRuleGoal || !manifest.StopTrigger.At.Equal(at) {
		t.Fatalf("stop trigger = %+v", manifest.StopTrigger)
	}
}

func TestDistributedArchiveStopTriggerComesFromTheController(t *testing.T) {
	tr := &TestRunner{}
	test := &ManagedTest{ID: "run", Status: "RUNNING", CreatedAt: time.Unix(1, 0)}
	// A generator bundle can never supply the trigger: only the controller's
	// run does.
	forged := RunArchiveManifest{TestID: "run", StopTrigger: &RunStopTrigger{Rule: "forged"}}
	bundles := []remoteArchiveBundle{{target: "gen-0:9090", manifest: forged}}
	if manifest := mergeRemoteArchiveManifest(tr, test, bundles, "COMPLETE", "", test.CreatedAt); manifest.StopTrigger != nil {
		t.Fatalf("generator stop trigger was merged: %+v", manifest.StopTrigger)
	}
	tr.recordAutoStopTrigger(test, runStopRuleErrorRate, "error rate threshold breached")
	manifest := mergeRemoteArchiveManifest(tr, test, bundles, "COMPLETE", "", test.CreatedAt)
	if manifest.StopTrigger == nil || manifest.StopTrigger.Rule != runStopRuleErrorRate {
		t.Fatalf("stop trigger = %+v", manifest.StopTrigger)
	}
}

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetrypolicy"
)

// runningTelemetrySegmentFixture is a running, admitted run: its admission
// receipt is the system settings' at a one-second local cadence.
func runningTelemetrySegmentFixture(t *testing.T, name string) (*TestRunner, *storage.SQLiteStore, *ManagedTest) {
	t.Helper()
	runner, store, test := newPassiveTransitionTestRunner(t, name)
	settings := cloneSystemSettings(runner.settings)
	snapshot := RunArchiveExecutionSettingsSnapshot{General: settings.General, Thresholds: settings.Thresholds}
	snapshot.General.MetricsIntervalSeconds = 1
	receipt := captureTelemetryBudgetPolicy(snapshot, false, time.Now().Add(-time.Minute))
	if !validTelemetryBudgetPolicy(receipt) {
		t.Fatal("admission receipt is invalid")
	}
	runner.mu.Lock()
	started := time.Now().Add(-time.Minute).UTC()
	test.Status, test.StartedAt = "RUNNING", &started
	test.initialArchiveProvenance = &RunArchiveProvenance{TelemetryPolicy: receipt}
	runner.mu.Unlock()
	return runner, store, test
}

func segmentRequest(id, policy string, expected int64) TelemetryPolicySegmentRequest {
	return TelemetryPolicySegmentRequest{RequestID: id, TelemetryBudgetPolicyID: policy, ExpectedSequence: expected}
}

// TEL-B: a running test's telemetry budget policy changes by appending a
// segment: the chosen policy's receipt (or the threshold settings') with an
// in-run capture boundary, stored and installed, the admission receipt kept
// as segment 1. A retry returns the segment it created; a change against a
// stale sequence, another request reusing the ID, an unusable policy, a
// distributed cadence change and a run that is not running are refused.
func TestTelemetryPolicySegmentsAppendOnlyChanges(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-append")
	project := normalizeProjectID(test.ProjectID)
	seedPolicy(t, store, project, "tight-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	seedPolicy(t, store, project, "slow-window", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sustained_window_seconds":60}`)
	actor := testCreationActor{Subject: "user:editor", DisplayName: "Editor"}

	for _, tc := range []struct {
		name string
		req  TelemetryPolicySegmentRequest
		want error
	}{
		{"no request id", segmentRequest("", "tight-budgets", 1), errTelemetrySegmentRequest},
		{"no policy", segmentRequest("r", "", 1), errTelemetrySegmentRequest},
		{"no sequence", segmentRequest("r", "tight-budgets", 0), errTelemetrySegmentRequest},
		{"missing policy", segmentRequest("r", "missing-budgets", 1), errTelemetrySegmentRequest},
		{"other window", segmentRequest("r", "slow-window", 1), errRunPolicyConfiguration},
		{"stale sequence", segmentRequest("r", "tight-budgets", 2), errTelemetrySegmentState},
	} {
		if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, tc.req, actor); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}

	segment, replayed, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("first", " tight-budgets ", 1), actor)
	if err != nil || replayed || segment.Sequence != 2 || segment.Source != telemetrySegmentSourceRunSelection || segment.PolicyID != "tight-budgets" || segment.RequestedBy != "Editor" {
		t.Fatalf("append: %+v %v %v", segment, replayed, err)
	}
	policy := segment.Policy
	if policy.SchemaVersion != telemetrypolicy.VersionProjectPolicy || policy.CaptureBoundary != telemetrypolicy.CaptureBoundarySegment || policy.Budgets[1].LimitMs != 250 || policy.ExpectedControllerPublicationIntervalMs != 1000 || !validTelemetryBudgetPolicy(policy) {
		t.Fatalf("segment receipt %+v", policy)
	}
	retried, replayed, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("first", "tight-budgets", 1), actor)
	if err != nil || !replayed || retried.Sequence != 2 || !retried.EffectiveAt.Equal(segment.EffectiveAt) {
		t.Fatalf("retry: %+v %v %v", retried, replayed, err)
	}
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("first", runPolicyProjectDefault, 2), actor); !errors.Is(err, errTelemetrySegmentRequestReused) {
		t.Fatalf("a reused request ID: %v", err)
	}
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("late", runPolicyProjectDefault, 1), actor); !errors.Is(err, errTelemetrySegmentState) {
		t.Fatalf("a change against the admission receipt after a change: %v", err)
	}
	// Back to the threshold settings, at a slower local cadence.
	settingsSegment, _, err := runner.AppendTelemetryPolicySegment(test.ID, TelemetryPolicySegmentRequest{RequestID: "second", TelemetryBudgetPolicyID: runPolicyProjectDefault, ExpectedSequence: 2, ControllerPublicationIntervalSeconds: 5}, actor)
	if err != nil || settingsSegment.Sequence != 3 || settingsSegment.Source != telemetrySegmentSourceSystemSettings || settingsSegment.Policy.SchemaVersion != telemetrypolicy.Version || settingsSegment.Policy.ExpectedControllerPublicationIntervalMs != 5000 {
		t.Fatalf("settings segment: %+v %v", settingsSegment, err)
	}

	segments, ok, _ := runner.TelemetryPolicySegments(test.ID)
	if !ok || len(segments) != 3 || segments[0].Source != telemetrySegmentSourceAdmission || segments[0].Sequence != 1 || segments[1].Sequence != 2 || segments[2].Sequence != 3 {
		t.Fatalf("segments %+v", segments)
	}
	if read, _ := runner.GetTest(test.ID); read == nil || len(read.TelemetryPolicySegments) != 3 {
		t.Fatalf("the test's read does not list its segments: %+v", read)
	}
	stored, err := store.ListTelemetryPolicySegments(test.ID)
	if err != nil || len(stored) != 2 || stored[0].RequestedBySubject != "user:editor" {
		t.Fatalf("stored segments %+v %v", stored, err)
	}
	evidence, limits := runner.runTelemetryPolicy(test)
	if evidence.Policy == nil || evidence.Policy.Hash != settingsSegment.Policy.Hash || *limits.MaxTelemetryGeneratorLagMs != 500 {
		t.Fatalf("the policy in force: %+v", evidence.Policy)
	}

	// A distributed run's cadence cannot change; a stopped run cannot change.
	runner.mu.Lock()
	admission := cloneTelemetryBudgetPolicy(test.initialArchiveProvenance.TelemetryPolicy)
	admission.CadenceScope, admission.ExpectedControllerPublicationIntervalMs = "distributed-controller-coalescing-gate", 1000
	admission.Hash = telemetryBudgetPolicyHash(admission)
	test.initialArchiveProvenance.TelemetryPolicy = admission
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, TelemetryPolicySegmentRequest{RequestID: "cadence", TelemetryBudgetPolicyID: runPolicyProjectDefault, ExpectedSequence: 1, ControllerPublicationIntervalSeconds: 2}, actor); !errors.Is(err, errTelemetrySegmentRequest) {
		t.Fatalf("a distributed cadence change: %v", err)
	}
	runner.mu.Lock()
	test.Status = "COMPLETE"
	runner.mu.Unlock()
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("after", runPolicyProjectDefault, 1), actor); !errors.Is(err, errTelemetrySegmentState) {
		t.Fatalf("a change after the run ended: %v", err)
	}
	// A retry is still answered after the run ended.
	if _, replayed, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("first", "tight-budgets", 1), actor); err != nil || !replayed {
		t.Fatalf("a retry after the run ended: %v %v", replayed, err)
	}
}

// Live classification follows the segments on the controller's clock: an
// interval wholly before a change is classified by the old segment, one a
// change falls inside by none (unscored, and a breach spell ends with it),
// one wholly after by the new segment, with its budgets; an interval ending
// while a change is being persisted is unscored too.
func TestTelemetryPolicySegmentsClassifyLiveIntervals(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-classify")
	seedPolicy(t, store, normalizeProjectID(test.ProjectID), "tight-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	publish := func() *TelemetryRunHealth {
		t.Helper()
		// 300 ms of generator processing: within the admission budget (500),
		// over the chosen policy's (250).
		point := &LiveMetrics{Timestamp: time.Now(), ActiveVUsers: 1, Telemetry: &TelemetryHealth{GeneratorProcessingLagMs: 300}}
		if !runner.publishLiveMetrics(test, point, nil) {
			t.Fatal("publication rejected")
		}
		return point.TelemetryStatus
	}
	if health := publish(); health == nil || health.Status != "healthy" || health.PolicySegment != 1 {
		t.Fatalf("before the change: %+v", health)
	}
	time.Sleep(5 * time.Millisecond)
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("tighten", "tight-budgets", 1), testCreationActor{}); err != nil {
		t.Fatal(err)
	}
	if health := publish(); health == nil || health.Status != "unscored" || health.PolicySegment != 0 || len(health.Unscored) != 1 || health.Unscored[0] != telemetryPolicyBoundaryUnscored {
		t.Fatalf("the interval the change fell inside: %+v", health)
	}
	if health := publish(); health == nil || health.Status != "degraded" || health.PolicySegment != 2 {
		t.Fatalf("after the change: %+v", health)
	}
	// While a change is being persisted, an interval ending after its
	// boundary is not classified by the old segment.
	runner.mu.Lock()
	pending := time.Now()
	test.telemetrySegmentPendingAt = &pending
	runner.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	if health := publish(); health == nil || health.Status != "unscored" {
		t.Fatalf("an interval ending while a change is pending: %+v", health)
	}
	runner.mu.Lock()
	test.telemetrySegmentPendingAt = nil
	runner.mu.Unlock()

	// An interval whose segments cannot be read is unscored as unavailable,
	// not as a boundary; what no policy decides (a collector loss) is still
	// judged and alerted.
	runner.mu.Lock()
	test.telemetrySegmentsUnavailable = true
	test.telemetrySegmentsRetryAt = time.Now().Add(time.Hour)
	runner.mu.Unlock()
	point := &LiveMetrics{Timestamp: time.Now(), ActiveVUsers: 1, Telemetry: &TelemetryHealth{GeneratorProcessingLagMs: 300}}
	if !runner.publishLiveMetrics(test, point, nil) {
		t.Fatal("publication rejected")
	}
	if health := point.TelemetryStatus; health == nil || health.Status != "unscored" || len(health.Unscored) != 1 || health.Unscored[0] != telemetryPolicySegmentsUnavailableUnscored {
		t.Fatalf("an interval whose segments cannot be read: %+v", health)
	}
	// 900 ms of generator processing would breach every budget: unjudged.
	diagnostic, _ := buildTelemetryDiagnostic(test, &LiveMetrics{Telemetry: &TelemetryHealth{DroppedResults: 3, GeneratorProcessingLagMs: 900}}, ThresholdSettings{}, telemetryPolicySegmentsUnavailableUnscored)
	if diagnostic == nil || diagnostic.Severity != "danger" || len(diagnostic.Conditions) != 1 || diagnostic.Conditions[0] != "collector_drops" || len(diagnostic.Unscored) != 1 {
		t.Fatalf("a loss in an unclassified interval: %+v", diagnostic)
	}
	runner.mu.Lock()
	test.telemetrySegmentsUnavailable = false
	runner.mu.Unlock()
}

// A change whose write fails (the execution lease was lost) leaves the
// current segment in force and nothing pending.
func TestTelemetryPolicySegmentFailedWriteKeepsTheCurrentSegment(t *testing.T) {
	runner, _, test := runningTelemetrySegmentFixture(t, "segments-lease")
	runner.mu.Lock()
	runner.durableExecutionFence = true
	test.executionFence = "not-the-lease-holder"
	runner.mu.Unlock()
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("lost", runPolicyProjectDefault, 1), testCreationActor{}); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("a change without the lease: %v", err)
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if test.telemetrySegmentPendingAt != nil || len(test.telemetryPolicySegments) != 0 {
		t.Fatalf("a failed change left state behind: pending %v, segments %d", test.telemetrySegmentPendingAt, len(test.telemetryPolicySegments))
	}
}

type unavailableSegmentStore struct{ *storage.SQLiteStore }

func (unavailableSegmentStore) ListTelemetryPolicySegments(string) ([]storage.TelemetryPolicySegment, error) {
	return nil, errors.New("segments unavailable")
}

// A run whose installed segments may be incomplete lists them from the
// store: one marked for a re-read (a write's outcome unknown) even after it
// ended, merged with what is installed; a failed read is an error.
func TestTelemetryPolicySegmentsListedFromTheStoreWhenStale(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-stale-list")
	for i, id := range []string{"second", "third"} {
		if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest(id, runPolicyProjectDefault, int64(i+1)), testCreationActor{}); err != nil {
			t.Fatal(err)
		}
	}
	runner.mu.Lock()
	test.telemetryPolicySegments = test.telemetryPolicySegments[:1]
	test.telemetrySegmentsUnavailable = true
	test.Status = "COMPLETE"
	runner.mu.Unlock()
	if listed, _, err := runner.TelemetryPolicySegments(test.ID); err != nil || len(listed) != 3 || listed[2].Sequence != 3 {
		t.Fatalf("a stale ended run's segments: %+v (%v)", listed, err)
	}
	runner.mu.Lock()
	test.Status = "RUNNING"
	runner.testStore = unavailableSegmentStore{store}
	runner.mu.Unlock()
	if _, _, err := runner.TelemetryPolicySegments(test.ID); err == nil {
		t.Fatal("a running run whose segments cannot be read listed its admission segment alone")
	}
}

// A run named by its reference key lists and changes its segments, and an
// HA follower, which never installs a run's segments, reads them from the
// store.
func TestTelemetryPolicySegmentsByRefKeyAndOnAFollower(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-refkey")
	key := refkey.Run("dev", time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), 7)
	runner.mu.Lock()
	if runner.refKeyIndex == nil {
		runner.refKeyIndex = map[string]string{}
	}
	runner.refKeyIndex[normalizeRefKeyCandidate(key)] = test.ID
	runner.mu.Unlock()
	if _, _, err := runner.AppendTelemetryPolicySegment(key, segmentRequest("by-key", runPolicyProjectDefault, 1), testCreationActor{}); err != nil {
		t.Fatalf("a change by reference key: %v", err)
	}
	if listed, ok, err := runner.TelemetryPolicySegments(key); !ok || err != nil || len(listed) != 2 {
		t.Fatalf("segments by reference key: %+v %v %v", listed, ok, err)
	}
	// A leader still adopting its runs (not yet installed) reads the store.
	runner.mu.Lock()
	reconciling := newExecutionLeadership(store, "segments-leader")
	runner.mu.Unlock()
	reconciling.tick()
	reconciling.reconciling.Store(true)
	if !reconciling.IsLeader() {
		t.Fatal("the reconciling controller holds no lease")
	}
	runner.mu.Lock()
	runner.leadership = reconciling
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	if listed, ok, err := runner.TelemetryPolicySegments(test.ID); !ok || err != nil || len(listed) != 2 {
		t.Fatalf("a reconciling leader's segments: %+v %v %v", listed, ok, err)
	}
	// A follower reloads the run from the store, with its admission receipt.
	if saved, err := store.SaveTestInitialProvenance(test.ID, normalizeProjectID(test.ProjectID), test.initialArchiveProvenance); err != nil || !saved {
		t.Fatalf("save provenance: %v %v", saved, err)
	}
	runner.mu.Lock()
	runner.leadership = newExecutionLeadership(store, "follower")
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	if listed, ok, err := runner.TelemetryPolicySegments(test.ID); !ok || err != nil || len(listed) != 2 {
		t.Fatalf("a follower's segments: %+v %v %v", listed, ok, err)
	}
}

// An adopted or reloaded run installs its stored segments; one whose
// segments cannot be read is classified by none and takes no change.
func TestTelemetryPolicySegmentsRestoredOnAdoption(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-restore")
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("stored", runPolicyProjectDefault, 1), testCreationActor{}); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	runner.restoreTelemetryPolicySegments(test)
	if segments, _, _ := runner.TelemetryPolicySegments(test.ID); len(segments) != 2 || segments[1].Source != telemetrySegmentSourceSystemSettings {
		t.Fatalf("restored segments %+v", segments)
	}
	runner.mu.Lock()
	runner.testStore = unavailableSegmentStore{store}
	runner.mu.Unlock()
	runner.restoreTelemetryPolicySegments(test)
	runner.mu.RLock()
	interval := telemetryIntervalPolicyLocked(test, time.Now().Add(-time.Second), time.Now())
	runner.mu.RUnlock()
	if !interval.spanning || !interval.unavailable || interval.boundary {
		t.Fatalf("a run whose segments could not be read: %+v", interval)
	}
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("blind", runPolicyProjectDefault, 2), testCreationActor{}); !errors.Is(err, errRunPolicyStoreUnavailable) {
		t.Fatalf("a change to a run whose segments could not be read: %v", err)
	}
	// Once the store answers again, a due retry restores them.
	runner.mu.Lock()
	runner.testStore = store
	runner.mu.Unlock()
	runner.retryTelemetryPolicySegments(test, time.Now())
	runner.mu.RLock()
	stillUnavailable := test.telemetrySegmentsUnavailable
	runner.mu.RUnlock()
	if !stillUnavailable {
		t.Fatal("a retry ran before it was due")
	}
	runner.retryTelemetryPolicySegments(test, time.Now().Add(telemetrySegmentsRetryInterval))
	if segments, _, _ := runner.TelemetryPolicySegments(test.ID); len(segments) != 2 {
		t.Fatalf("segments after the store recovered: %+v", segments)
	}
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("seen", runPolicyProjectDefault, 2), testCreationActor{}); err != nil {
		t.Fatalf("a change once the segments are read: %v", err)
	}
}

// Coverage counts each segment's classified time from the intervals it
// classified; an interval a change fell inside is unknown.
func TestTelemetryPolicySegmentsCoverage(t *testing.T) {
	start := time.Date(2026, time.October, 23, 12, 0, 0, 0, time.UTC)
	segments := []TelemetryPolicySegment{{Sequence: 1, Start: start, End: start.Add(3 * time.Second)}, {Sequence: 2, Start: start.Add(3 * time.Second), End: start.Add(6 * time.Second)}}
	row := func(second int, status string, segment int64) IntervalSummaryRecord {
		return IntervalSummaryRecord{Timestamp: start.Add(time.Duration(second) * time.Second), SampleWindowSeconds: 1, TelemetryStatus: &TelemetryRunHealth{Status: status, PolicySegment: segment}}
	}
	rows := []IntervalSummaryRecord{row(1, "healthy", 1), row(2, "healthy", 1), row(3, "unscored", 0), row(4, "degraded", 2), row(5, "breached", 2), row(6, "degraded", 2)}
	coverage := TelemetryCoverage{WindowStart: start, WindowEnd: start.Add(6 * time.Second), WindowMs: 6000}
	all := &coverageSpans{}
	all.add(start.UnixMilli(), start.Add(6*time.Second).UnixMilli())
	record := telemetryCoverageRecord(coverage, all, rows, segments)
	if record.HealthyMs != 2000 || record.DegradedMs != 2000 || record.BreachedMs != 1000 || record.UnknownMs != 1000 {
		t.Fatalf("run totals %+v", record)
	}
	first, second := record.Segments[0], record.Segments[1]
	if first.HealthyMs != 2000 || first.DegradedMs != 0 || second.DegradedMs != 2000 || second.BreachedMs != 1000 || second.HealthyMs != 0 {
		t.Fatalf("segment totals %+v %+v", first, second)
	}
}

// A run whose policy changed while it ran is never compared like-for-like
// with another run's whole-run health; its report lists the segments.
func TestTelemetryPolicySegmentsInComparisonAndReport(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-report")
	seedPolicy(t, store, normalizeProjectID(test.ProjectID), "tight-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("tighten", "tight-budgets", 1), testCreationActor{}); err != nil {
		t.Fatal(err)
	}
	segments, _, _ := runner.TelemetryPolicySegments(test.ID)
	runner.mu.RLock()
	admission := cloneTelemetryBudgetPolicy(test.initialArchiveProvenance.TelemetryPolicy)
	runner.mu.RUnlock()
	changed := &RunArchiveManifest{Provenance: RunArchiveProvenance{TelemetryPolicy: admission}, TelemetryPolicySegments: segments,
		TelemetryCoverage: &TelemetryCoverageRecord{Algorithm: telemetryCoverageAlgorithm, Segments: []TelemetryPolicySegment{{Sequence: 1, HealthyMs: 3000}, {Sequence: 2, DegradedMs: 2000}}}}
	steady := &RunArchiveManifest{Provenance: RunArchiveProvenance{TelemetryPolicy: admission}, TelemetryCoverage: &TelemetryCoverageRecord{Algorithm: telemetryCoverageAlgorithm}}
	comparison := decideBaselineTelemetry(&baselineComparisonRun{manifest: changed}, &baselineComparisonRun{manifest: steady}, rca.WorkloadQualification{Status: rca.BaselineQualified})
	if comparison.Comparable || !strings.Contains(strings.Join(comparison.Reasons, ","), telemetryReasonPolicyChanged) || comparison.Current.PolicySegments != 2 {
		t.Fatalf("comparison %+v", comparison)
	}
	view := buildTelemetryAcceptanceReportView(changed, nil)
	if len(view.Segments) != 2 || view.Segments[1].Policy != "tight-budgets revision 1" || view.Segments[1].Degraded != "2,000" || view.Segments[0].Source != telemetrySegmentSourceAdmission {
		t.Fatalf("report segments %+v", view.Segments)
	}
	// A segment's receipt is never a run's admission evidence.
	if evidence := telemetryPolicyEvidenceFromProvenance(&RunArchiveProvenance{TelemetryPolicy: segments[1].Policy}); evidence.Policy != nil || evidence.Reason != "immutable-policy-invalid" {
		t.Fatalf("a segment receipt as admission evidence: %+v", evidence)
	}
	if view.SegmentsNote != telemetryAcceptanceSegmentsNote {
		t.Fatalf("report segments note %q", view.SegmentsNote)
	}
	// The PDF and HTML report render the note and the segments.
	var lines []string
	buildReportSectionRegistry()["telemetry_acceptance"].PDF(&runArchiveHTMLReportData{Report: &RunArchiveReport{Manifest: changed}}, &lines)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, telemetryAcceptanceSegmentsNote) || !strings.Contains(text, "Policy segment 2") {
		t.Fatalf("PDF lines:\n%s", text)
	}
	report := consistencyMatrixReport()
	report.Manifest = changed
	rendered, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), html.EscapeString(telemetryAcceptanceSegmentsNote)) || !strings.Contains(string(rendered), "tight-budgets revision 1") {
		t.Fatal("the HTML report lacks the segments note or table")
	}
	report.Manifest = steady
	if rendered, err = renderRunArchiveHTMLReportWithProfile(report, "engineer"); err != nil || strings.Contains(string(rendered), html.EscapeString(telemetryAcceptanceSegmentsNote)) {
		t.Fatalf("a steady run's HTML report: %v", err)
	}
	lines = nil
	buildReportSectionRegistry()["telemetry_acceptance"].PDF(&runArchiveHTMLReportData{Report: &RunArchiveReport{Manifest: steady}}, &lines)
	if text := strings.Join(lines, "\n"); strings.Contains(text, telemetryAcceptanceSegmentsNote) || strings.Contains(text, "Policy segment") {
		t.Fatalf("a steady run's PDF lines:\n%s", text)
	}
	if steadyView := buildTelemetryAcceptanceReportView(steady, nil); len(steadyView.Segments) != 0 || steadyView.SegmentsNote != "" {
		t.Fatal("a run whose policy never changed lists segments")
	}
	scrubbed := scrubTelemetryText(changed, newTelemetryTextScrubber(changed))
	if len(scrubbed.TelemetryPolicySegments) != 2 || scrubbed.TelemetryPolicySegments[1].Policy.Hash != segments[1].Policy.Hash {
		t.Fatalf("the scrubbed projection lost the segments: %+v", scrubbed.TelemetryPolicySegments)
	}
}

// Re-evaluation can select one segment of a run: only the intervals it
// classified, against its receipt.
func TestTelemetryReevaluationSelectsASegment(t *testing.T) {
	intervals := []IntervalSummaryRecord{
		{TelemetryStatus: &TelemetryRunHealth{Status: "healthy", PolicySegment: 1}},
		{TelemetryStatus: &TelemetryRunHealth{Status: "unscored"}},
		{TelemetryStatus: &TelemetryRunHealth{Status: "degraded", PolicySegment: 2}},
	}
	if kept, _ := telemetryIntervalsOfSegment(intervals, 2, false, nil); len(kept) != 1 || kept[0].TelemetryStatus.Status != "degraded" {
		t.Fatalf("segment 2 intervals %+v", kept)
	}
	// A run recorded as never changing keeps the intervals naming its one
	// segment or none, never another segment's.
	if kept, _ := telemetryIntervalsOfSegment(intervals, 1, true, nil); len(kept) != 2 {
		t.Fatalf("a run whose policy never changed: %+v", kept)
	}
	// An interval naming no segment (archived by an older controller) in a
	// changed run is the segment's whose span holds it, as coverage counts it.
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	legacy := []IntervalSummaryRecord{
		{Timestamp: start.Add(2 * time.Second), SampleWindowSeconds: 1, TelemetryStatus: &TelemetryRunHealth{Status: "healthy"}},
		{Timestamp: start.Add(10500 * time.Millisecond), SampleWindowSeconds: 1, TelemetryStatus: &TelemetryRunHealth{Status: "healthy"}},
		// Its sample window begins before the run's: clipped to it, as
		// coverage counts it, the row is segment 1's.
		{Timestamp: start.Add(500 * time.Millisecond), SampleWindowSeconds: 1, TelemetryStatus: &TelemetryRunHealth{Status: "healthy"}},
	}
	coverage := &TelemetryCoverageRecord{Coverage: TelemetryCoverage{WindowStart: start, WindowEnd: start.Add(time.Minute)}, Segments: []TelemetryPolicySegment{{Sequence: 1, Start: start, End: start.Add(10 * time.Second)}}}
	if kept, _ := telemetryIntervalsOfSegment(legacy, 1, false, coverage); len(kept) != 2 || !kept[0].Timestamp.Equal(start.Add(500*time.Millisecond)) || !kept[1].Timestamp.Equal(start.Add(2*time.Second)) {
		t.Fatalf("legacy intervals of segment 1: %+v", kept)
	}
	// An interval this controller left without a segment on purpose (a
	// change fell inside it) is never a segment's, whatever its span.
	unclassified := IntervalSummaryRecord{Timestamp: start.Add(5 * time.Second), SampleWindowSeconds: 1, TelemetryStatus: &TelemetryRunHealth{Status: "breached", Unscored: []string{telemetryPolicyBoundaryUnscored}}}
	if kept, _ := telemetryIntervalsOfSegment(append(append([]IntervalSummaryRecord(nil), legacy...), unclassified), 1, false, coverage); len(kept) != 2 {
		t.Fatalf("an unclassified interval was re-evaluated with segment 1: %+v", kept)
	}
	if index := telemetryRowSegmentIndex([]TelemetryPolicySegment{{Sequence: 1, Start: start, End: start.Add(10 * time.Second)}, {Sequence: 2, Start: start.Add(10 * time.Second), End: start.Add(time.Minute)}}, unclassified.TelemetryStatus, start.Add(4*time.Second).UnixMilli(), start.Add(5*time.Second).UnixMilli()); index != -1 {
		t.Fatalf("an unclassified interval was attributed to segment index %d", index)
	}
	// A left-out interval ends a breach spell in the segment's
	// re-evaluation, as it did in the run: the condition after it starts a
	// new spell rather than joining the one before.
	fanout := 100.0
	slow := &TelemetryHealth{ControllerFanoutLagMs: 250}
	spell := []IntervalSummaryRecord{
		{Timestamp: start, Telemetry: slow, TelemetryStatus: &TelemetryRunHealth{Status: "degraded", PolicySegment: 2}},
		{Timestamp: start.Add(time.Second), Telemetry: slow, TelemetryStatus: &TelemetryRunHealth{Status: "unscored", Unscored: []string{telemetryPolicySegmentsUnavailableUnscored}}},
		{Timestamp: start.Add(2 * time.Second), Telemetry: slow, TelemetryStatus: &TelemetryRunHealth{Status: "degraded", PolicySegment: 2}},
	}
	kept, breaks := telemetryIntervalsOfSegment(spell, 2, false, nil)
	if len(kept) != 2 || len(breaks) != 1 {
		t.Fatalf("kept %d, breaks %v", len(kept), breaks)
	}
	var rescored TelemetryReevaluationRun
	reevaluateTelemetryIntervals(&rescored, kept, reevaluationPolicy(2*time.Second, &fanout, nil), false, breaks...)
	if rescored.Reevaluated == nil || rescored.Reevaluated.Breached != 0 || rescored.Reevaluated.Degraded != 2 {
		t.Fatalf("a left-out interval did not end the spell: %+v", rescored.Reevaluated)
	}
	admission := &TelemetryBudgetPolicy{Hash: "admission"}
	manifest := &RunArchiveManifest{TelemetryPolicySegments: []TelemetryPolicySegmentReceipt{{Sequence: 1, Policy: admission}, {Sequence: 2, Policy: &TelemetryBudgetPolicy{Hash: "second"}}}}
	if hash, ok := telemetryManifestSegmentHash(manifest, 2); !ok || hash != "second" {
		t.Fatalf("segment 2 hash %q %v", hash, ok)
	}
	if _, ok := telemetryManifestSegmentHash(manifest, 3); ok {
		t.Fatal("an unknown segment had a hash")
	}
}

// Through the routes: 201 for a change and 200 for its retry, with an audit
// event once; 400, 409 and 404 refusals; the list for a viewer; and a CI run
// changes only where its project allows telemetry policy selection.
func TestTelemetryPolicySegmentRoutes(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	runner := server.runner
	storeBudgetScenarioScript(t, server, "segments-route-script")
	created, err := runner.CreateTest(CreateTestRequest{Name: "Route", ScriptID: "segments-route-script", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	settings := cloneSystemSettings(runner.settings)
	receipt := captureTelemetryBudgetPolicy(RunArchiveExecutionSettingsSnapshot{General: settings.General, Thresholds: settings.Thresholds}, false, time.Now())
	runner.mu.Lock()
	test := runner.tests[created.ID]
	started := time.Now().UTC()
	test.Status, test.StartedAt = "RUNNING", &started
	test.initialArchiveProvenance = &RunArchiveProvenance{TelemetryPolicy: receipt}
	runner.mu.Unlock()
	seedPolicy(t, store, storage.DefaultProjectID, "route-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	post := func(id string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+id+"/telemetry-policy-segments", bytes.NewReader(raw)))
		return recorder
	}
	if response := post(created.ID, segmentRequest("", "route-budgets", 1)); response.Code != http.StatusBadRequest {
		t.Fatalf("no request ID: %d %s", response.Code, response.Body.String())
	}
	if response := post(created.ID, segmentRequest("r1", "missing-budgets", 1)); response.Code != http.StatusBadRequest {
		t.Fatalf("a missing policy: %d %s", response.Code, response.Body.String())
	}
	if response := post(created.ID, segmentRequest("r1", "route-budgets", 3)); response.Code != http.StatusConflict {
		t.Fatalf("a stale sequence: %d %s", response.Code, response.Body.String())
	}
	if response := post("missing-run", segmentRequest("r1", "route-budgets", 1)); response.Code != http.StatusNotFound {
		t.Fatalf("a missing run: %d %s", response.Code, response.Body.String())
	}
	first := post(created.ID, segmentRequest("r1", "route-budgets", 1))
	if first.Code != http.StatusCreated {
		t.Fatalf("a change: %d %s", first.Code, first.Body.String())
	}
	if retry := post(created.ID, segmentRequest("r1", "route-budgets", 1)); retry.Code != http.StatusOK {
		t.Fatalf("a retry: %d %s", retry.Code, retry.Body.String())
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "test.telemetry_policy.change"})
	if err != nil || len(events) != 1 {
		t.Fatalf("audit events %d %v", len(events), err)
	}
	listed := httptest.NewRecorder()
	server.mux.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+created.ID+"/telemetry-policy-segments", nil))
	var list struct {
		Segments []TelemetryPolicySegmentReceipt `json:"segments"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || listed.Code != http.StatusOK || len(list.Segments) != 2 {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}
	// A run whose segments must be read from the store, which cannot: 503,
	// not the admission segment alone.
	runner.mu.Lock()
	previousStore := runner.testStore
	runner.testStore = unavailableSegmentStore{store}
	test.telemetrySegmentsUnavailable = true
	runner.mu.Unlock()
	unreadable := httptest.NewRecorder()
	server.mux.ServeHTTP(unreadable, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+created.ID+"/telemetry-policy-segments", nil))
	if unreadable.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable list: %d %s", unreadable.Code, unreadable.Body.String())
	}
	runner.mu.Lock()
	runner.testStore = previousStore
	test.telemetrySegmentsUnavailable = false
	runner.mu.Unlock()

	// A CI run: refused without the project's permission.
	metadata, err := encodeCIRunMetadata(ciRunMetadata{SchemaVersion: ciRunSchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.MaterializationKey = metadata
	runner.mu.Unlock()
	if response := post(created.ID, segmentRequest("ci", runPolicyProjectDefault, 2)); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "allow_telemetry_policy_selection") {
		t.Fatalf("a CI run's change without the permission: %d %s", response.Code, response.Body.String())
	}
}

// Re-evaluation validates a segment selection, includes it in the input
// fingerprint, and re-scores a selected segment against its own receipt; an
// unknown segment is unanswerable rather than silently the whole run.
func TestTelemetryReevaluationSegmentSelection(t *testing.T) {
	server, store := baselineTestServer(t)
	telemetryReevaluationArchive(t, server, store, "reeval-seg", []float64{10, 300})
	saveTelemetryPolicy(t, store, "common", 100)
	for name, request := range map[string]string{
		"zero segment": `{"runs":["reeval-seg"],"policy":{"id":"common","revision":1},"segments":{"reeval-seg":0}}`,
		"unnamed run":  `{"runs":["reeval-seg"],"policy":{"id":"common","revision":1},"segments":{"other":1}}`,
	} {
		if _, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, json.RawMessage(request)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	whole, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, json.RawMessage(`{"runs":["reeval-seg"],"policy":{"id":"common","revision":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, json.RawMessage(`{"runs":["reeval-seg"],"policy":{"id":"common","revision":1},"segments":{"reeval-seg":1}}`))
	if err != nil || selected.fingerprint == whole.fingerprint {
		t.Fatalf("the fingerprint ignored the segment: %v", err)
	}
	first, err := reevaluateRunArchive(context.Background(), selected.runs[0], selected.policy)
	if err != nil || first.Segment != 1 || first.OriginalPolicyHash == "" || first.Original.Intervals != 2 {
		t.Fatalf("segment 1 of a run whose policy never changed: %+v %v", first, err)
	}
	unknown := selected.runs[0]
	unknown.segment = 2
	missing, err := reevaluateRunArchive(context.Background(), unknown, selected.policy)
	if err != nil || missing.Answerable || len(missing.Unanswerable) != 1 || missing.Unanswerable[0] != "segment_unknown" {
		t.Fatalf("an unknown segment: %+v %v", missing, err)
	}
}

// An ended run this controller has not loaded segments for (it was reloaded
// after a restart) lists them from the store; a running one never reads it.
func TestTelemetryPolicySegmentsOfAnEndedRunAreReadFromTheStore(t *testing.T) {
	runner, _, test := runningTelemetrySegmentFixture(t, "segments-ended")
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("stored", runPolicyProjectDefault, 1), testCreationActor{}); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	if segments, _, _ := runner.TelemetryPolicySegments(test.ID); len(segments) != 1 {
		t.Fatalf("a running run read the store: %+v", segments)
	}
	runner.mu.Lock()
	test.Status = "COMPLETE"
	runner.mu.Unlock()
	if segments, _, _ := runner.TelemetryPolicySegments(test.ID); len(segments) != 2 || segments[1].Source != telemetrySegmentSourceSystemSettings {
		t.Fatalf("an ended run's segments: %+v", segments)
	}
	masked := maskRunArchiveManifest(&RunArchiveManifest{TelemetryPolicySegments: []TelemetryPolicySegmentReceipt{{Sequence: 1}, {Sequence: 2}}})
	if len(masked.TelemetryPolicySegments) != 2 {
		t.Fatal("the masked manifest a report carries lost the segments")
	}
}

// writeSegmentArchive writes an archive whose intervals name segments, and
// returns its record and manifest (unrecorded coverage).
func writeSegmentArchive(t *testing.T, id string, start time.Time, rows []IntervalSummaryRecord, manifest RunArchiveManifest) (*storage.RunArchiveRecord, *RunArchiveManifest) {
	t.Helper()
	root := t.TempDir()
	var buffer bytes.Buffer
	for _, row := range rows {
		if err := json.NewEncoder(&buffer).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "interval_summaries.jsonl"), buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest.SchemaVersion, manifest.TestID, manifest.TestName, manifest.Status = archiveSchemaVersion, id, id, "COMPLETE"
	manifest.CreatedAt, manifest.IntervalSummariesFile, manifest.IntervalSummaryCount = start, "interval_summaries.jsonl", int64(len(rows))
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	record := &storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), ManifestSHA256: hex.EncodeToString(sum[:]), CreatedAt: start, CompletedAt: manifest.CompletedAt, IntervalSummaryCount: int64(len(rows))}
	return record, &manifest
}

func unclassifiedSegmentRow(at time.Time, status, reason string) IntervalSummaryRecord {
	row := segmentRow(at, status, 0, 10)
	row.TelemetryStatus.Unscored = []string{reason}
	return row
}

func segmentRow(at time.Time, status string, segment int64, fanout float64) IntervalSummaryRecord {
	return IntervalSummaryRecord{Timestamp: at, SampleWindowSeconds: 1, Telemetry: &TelemetryHealth{ControllerFanoutLagMs: fanout}, TelemetryStatus: &TelemetryRunHealth{Status: status, PolicySegment: segment}}
}

// At finalize, a run's stored segments are read into the finalization
// snapshot (whose source never loaded them), recorded in the manifest, and
// each one's classified time is counted from the intervals it classified; a
// run whose segments cannot be read fails the step (retried) rather than
// being recorded as classified by one policy.
func TestTelemetryPolicySegmentsRecordedAtFinalize(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-finalize")
	segment, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("finalize", runPolicyProjectDefault, 1), testCreationActor{})
	if err != nil {
		t.Fatal(err)
	}
	boundary := segment.EffectiveAt
	runner.mu.Lock()
	start, end := *test.StartedAt, boundary.Add(10*time.Second)
	test.Status, test.EndedAt, test.telemetryPolicySegments = "COMPLETE", &end, nil
	runner.mu.Unlock()
	rows := []IntervalSummaryRecord{
		segmentRow(boundary.Add(-3*time.Second), "healthy", 1, 10),
		segmentRow(boundary.Add(-2*time.Second), "healthy", 1, 10),
		segmentRow(boundary.Add(500*time.Millisecond), "unscored", 0, 10),
		segmentRow(boundary.Add(2*time.Second), "degraded", 2, 900),
		// Left without a segment on purpose (a loss judged in it), though
		// its span lies within segment 2's: the run's, not the segment's.
		unclassifiedSegmentRow(boundary.Add(5*time.Second), "breached", telemetryPolicyBoundaryUnscored),
	}
	record, manifest := writeSegmentArchive(t, test.ID, start, rows, RunArchiveManifest{CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &start}})
	// Finalization works on a snapshot, never the live run.
	runner.mu.RLock()
	snapshot := cloneManagedTestForReadLocked(test)
	runner.mu.RUnlock()
	if err := runner.persistTelemetryCoverage(context.Background(), snapshot, record, manifest); err != nil {
		t.Fatal(err)
	}
	coverage := manifest.TelemetryCoverage
	if len(manifest.TelemetryPolicySegments) != 2 || coverage == nil || len(coverage.Segments) != 2 {
		t.Fatalf("finalized segments %+v, coverage %+v", manifest.TelemetryPolicySegments, coverage)
	}
	if first, second := coverage.Segments[0], coverage.Segments[1]; first.HealthyMs != 2000 || second.DegradedMs != 1000 || second.BreachedMs != 0 || coverage.BreachedMs != 1000 || !second.Start.Equal(boundary) || first.PolicyHash == second.PolicyHash {
		t.Fatalf("segment coverage %+v %+v (run breached %d)", first, second, coverage.BreachedMs)
	}

	// The store cannot be read: no coverage, and an error that fails the
	// finalization step so the job retries it.
	runner.mu.Lock()
	runner.testStore = unavailableSegmentStore{store}
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	record, manifest = writeSegmentArchive(t, test.ID, start, rows, RunArchiveManifest{CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &start}})
	runner.mu.RLock()
	snapshot = cloneManagedTestForReadLocked(test)
	runner.mu.RUnlock()
	if err := runner.persistTelemetryCoverage(context.Background(), snapshot, record, manifest); !errors.Is(err, errTelemetrySegmentsUnreadable) || manifest.TelemetryCoverage != nil || len(manifest.TelemetryPolicySegments) != 0 {
		t.Fatalf("finalize with unreadable segments: %v, coverage %+v", err, manifest.TelemetryCoverage)
	}
}

// Re-evaluating one segment of a run whose policy changed re-scores only the
// intervals that segment classified, and names its receipt as the original.
func TestTelemetryReevaluationOfOneSegmentOfAChangedRun(t *testing.T) {
	server, store := baselineTestServer(t)
	saveTelemetryPolicy(t, store, "common", 100)
	start := time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	admission := telemetryReceipt(t, 1, 250)
	changed := cloneTelemetryBudgetPolicy(admission)
	changed.CaptureBoundary = telemetrypolicy.CaptureBoundarySegment
	changed.Budgets[3].LimitMs, changed.Budgets[3].Enabled = 1000, true
	changed.Hash = telemetryBudgetPolicyHash(changed)
	segments := []TelemetryPolicySegmentReceipt{{Sequence: 1, EffectiveAt: start, Source: telemetrySegmentSourceAdmission, Policy: admission}, {Sequence: 2, EffectiveAt: start.Add(10 * time.Second), Source: telemetrySegmentSourceSystemSettings, Policy: changed}}
	rows := []IntervalSummaryRecord{
		segmentRow(start.Add(5*time.Second), "healthy", 1, 10),
		segmentRow(start.Add(10500*time.Millisecond), "unscored", 0, 10),
		segmentRow(start.Add(12*time.Second), "healthy", 2, 500),
		segmentRow(start.Add(13*time.Second), "healthy", 2, 500),
	}
	provenance := RunArchiveProvenance{ProjectID: storage.DefaultProjectID, StartedAt: &start, TelemetryPolicy: admission}
	record, _ := writeSegmentArchive(t, "reeval-changed", start, rows, RunArchiveManifest{CompletedAt: end, Provenance: provenance, InitialProvenance: &provenance, TelemetryPolicySegments: segments})
	if err := store.SaveTest(&storage.TestRecord{ID: "reeval-changed", Name: "reeval-changed", ProjectID: storage.DefaultProjectID, ScenarioID: "scenario", Status: "COMPLETE", Groups: []any{}, StartTime: start}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests["reeval-changed"] = &ManagedTest{ID: "reeval-changed", Name: "reeval-changed", ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartedAt: &start, EndedAt: &end}
	server.runner.archives["reeval-changed"] = record
	server.runner.mu.Unlock()
	inputs, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, json.RawMessage(`{"runs":["reeval-changed"],"policy":{"id":"common","revision":1},"segments":{" reeval-changed ":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := reevaluateRunArchive(context.Background(), inputs.runs[0], inputs.policy)
	if err != nil || result.Segment != 2 || result.OriginalPolicyHash != changed.Hash || result.Original.Intervals != 2 || result.Reevaluated == nil || result.Reevaluated.Degraded+result.Reevaluated.Breached != 2 {
		t.Fatalf("segment 2: %+v %v (reevaluated %+v)", result, err, result.Reevaluated)
	}
	// The whole run names no single original receipt: it spans two.
	whole, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, json.RawMessage(`{"runs":["reeval-changed"],"policy":{"id":"common","revision":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reevaluateRunArchive(context.Background(), whole.runs[0], whole.policy); err != nil || result.OriginalPolicyHash != "" || result.PolicySegments != 2 {
		t.Fatalf("the whole changed run: %+v %v", result, err)
	}
	if _, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, json.RawMessage(`{"runs":["reeval-changed"],"policy":{"id":"common","revision":1},"segments":{"reeval-changed":2," reeval-changed":1}}`)); err == nil {
		t.Fatal("a run selected twice by keys that trim alike was accepted")
	}
}

// The core review fixes: a publication whose sample was collected before a
// change is not classified by the new segment even when the previous
// publication came after the change; an unplaceable distributed sample after
// a change is unscored; boundaries carry microseconds; a replayed change is
// installed; a restore keeps a change made meanwhile.
func TestTelemetryPolicySegmentPlacementAndRecovery(t *testing.T) {
	runner, _, test := runningTelemetrySegmentFixture(t, "segments-placement")
	segment, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("place", runPolicyProjectDefault, 1), testCreationActor{})
	if err != nil {
		t.Fatal(err)
	}
	if segment.EffectiveAt.Nanosecond()%1000 != 0 {
		t.Fatalf("boundary %s is finer than a microsecond", segment.EffectiveAt)
	}
	boundary := segment.EffectiveAt
	runner.mu.Lock()
	test.telemetryLastClassifiedAt = boundary.Add(100 * time.Millisecond)
	runner.mu.Unlock()
	now := boundary.Add(time.Second)
	if _, _, interval := runner.runTelemetryIntervalPolicy(test, now, telemetryMeasurement{collectedAt: boundary.Add(-100 * time.Millisecond)}); !interval.spanning || !interval.boundary {
		t.Fatalf("a local sample collected before the change: %+v", interval)
	}
	if _, _, interval := runner.runTelemetryIntervalPolicy(test, now, telemetryMeasurement{collectedAt: boundary.Add(200 * time.Millisecond)}); interval.spanning || interval.segment.Sequence != 2 {
		t.Fatalf("a local sample collected after the change: %+v", interval)
	}
	unqualified := telemetryMeasurement{collectedAt: now, quality: &generatorrpc.ClockQuality{Reason: "uncalibrated"}}
	if _, known := unqualified.start(true); known {
		t.Fatal("an unqualified distributed sample was placed")
	}
	qualified := telemetryMeasurement{collectedAt: boundary.Add(time.Second), offsetMs: 400, quality: &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 700}}
	if start, known := qualified.start(true); !known || !start.Equal(boundary.Add(-100*time.Millisecond)) {
		t.Fatalf("a qualified distributed sample starts at %s", start)
	}

	// A replayed change whose answer was lost is installed.
	runner.mu.Lock()
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	if _, replayed, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("place", runPolicyProjectDefault, 1), testCreationActor{}); err != nil || !replayed {
		t.Fatalf("replay: %v %v", replayed, err)
	}
	if segments, _, _ := runner.TelemetryPolicySegments(test.ID); len(segments) != 2 {
		t.Fatalf("the replayed change was not installed: %+v", segments)
	}
	// A restore keeps a segment installed meanwhile that its read predates.
	runner.mu.Lock()
	extra := TelemetryPolicySegmentReceipt{Sequence: 3, EffectiveAt: boundary.Add(time.Minute), Source: telemetrySegmentSourceSystemSettings, Policy: cloneTelemetryBudgetPolicy(segment.Policy)}
	installTelemetryPolicySegmentLocked(test, extra)
	runner.mu.Unlock()
	runner.restoreTelemetryPolicySegments(test)
	if segments, _, _ := runner.TelemetryPolicySegments(test.ID); len(segments) != 3 {
		t.Fatalf("a restore dropped a segment installed meanwhile: %+v", segments)
	}
}

// A distributed run whose samples' clock is unqualified stays classified by
// its admission receipt while its policy never changes; after a change, its
// unplaceable measurements are unscored.
func TestTelemetryPolicySegmentUnqualifiedDistributedRun(t *testing.T) {
	runner, _, test := runningTelemetrySegmentFixture(t, "segments-unqualified")
	runner.mu.Lock()
	test.remoteClientsByTarget = map[string]generatorClient{"gen-a": nil}
	runner.mu.Unlock()
	unqualified := telemetryMeasurement{collectedAt: time.Now(), quality: &generatorrpc.ClockQuality{Reason: "uncalibrated"}}
	if _, _, interval := runner.runTelemetryIntervalPolicy(test, time.Now(), unqualified); !interval.ok || interval.spanning || interval.segment.Sequence != 1 {
		t.Fatalf("an unqualified run without a change: %+v", interval)
	}
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("unqualified", runPolicyProjectDefault, 1), testCreationActor{}); err != nil {
		t.Fatal(err)
	}
	if _, _, interval := runner.runTelemetryIntervalPolicy(test, time.Now().Add(time.Minute), unqualified); !interval.spanning {
		t.Fatalf("an unqualified run after a change: %+v", interval)
	}
}

// A write that fails with a conflict (another change was stored) marks the
// run's segments for an immediate re-read; the next publication installs
// what the store holds.
func TestTelemetryPolicySegmentConflictRereadsTheStore(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-conflict")
	// Another controller stored segment 2, which this one never installed.
	receipt := captureTelemetryBudgetPolicy(RunArchiveExecutionSettingsSnapshot{General: runner.settings.General, Thresholds: runner.settings.Thresholds}, false, time.Now())
	receipt.CaptureBoundary = telemetrypolicy.CaptureBoundarySegment
	receipt.Hash = telemetryBudgetPolicyHash(receipt)
	encoded, _ := json.Marshal(receipt)
	if _, _, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(storage.TelemetryPolicySegment{TestID: test.ID, EffectiveAt: time.Now(), Receipt: encoded, Source: telemetrySegmentSourceSystemSettings, RequestID: "elsewhere"}, 1, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("here", runPolicyProjectDefault, 1), testCreationActor{}); !errors.Is(err, errTelemetrySegmentState) {
		t.Fatalf("a change against a stale memory: %v", err)
	}
	runner.mu.RLock()
	unavailable := test.telemetrySegmentsUnavailable
	runner.mu.RUnlock()
	if !unavailable {
		t.Fatal("a conflict did not mark the segments for a re-read")
	}
	runner.retryTelemetryPolicySegments(test, time.Now())
	if segments, _, _ := runner.TelemetryPolicySegments(test.ID); len(segments) != 2 {
		t.Fatalf("segments after the re-read: %+v", segments)
	}
}

// invalidReceiptSegmentStore returns the stored segments with one receipt
// replaced by one that does not validate.
type invalidReceiptSegmentStore struct{ *storage.SQLiteStore }

func (s invalidReceiptSegmentStore) ListTelemetryPolicySegments(testID string) ([]storage.TelemetryPolicySegment, error) {
	segments, err := s.SQLiteStore.ListTelemetryPolicySegments(testID)
	if err == nil && len(segments) > 0 {
		segments[0].Receipt = json.RawMessage(`{"schema_version":"apexload.telemetry_policy.v9"}`)
	}
	return segments, err
}

// A stored segment whose receipt does not validate keeps the run unscored
// live (no segment can classify it), and at finalize is recorded as a
// segment without a receipt rather than retried forever or dropped.
func TestTelemetryPolicySegmentWithAnInvalidStoredReceipt(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-invalid-receipt")
	segment, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest("invalid", runPolicyProjectDefault, 1), testCreationActor{})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.testStore = invalidReceiptSegmentStore{store}
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	runner.restoreTelemetryPolicySegments(test)
	runner.mu.RLock()
	unavailable, invalidSegments := test.telemetrySegmentsUnavailable, len(test.telemetrySegmentsInvalid)
	runner.mu.RUnlock()
	if !unavailable || invalidSegments != 1 {
		t.Fatalf("a run with an invalid stored receipt: unavailable %v, invalid %d", unavailable, invalidSegments)
	}
	// The running run lists it without a receipt, claims no receipt live,
	// and marks its intervals unscored as unavailable, not as a boundary.
	if listed, _, _ := runner.TelemetryPolicySegments(test.ID); len(listed) != 2 || listed[1].Sequence != 2 || listed[1].Policy != nil {
		t.Fatalf("live segments %+v", listed)
	}
	evidence, _, interval := runner.runTelemetryIntervalPolicy(test, time.Now(), telemetryMeasurement{})
	if !interval.unavailable || evidence == nil || evidence.Policy != nil || evidence.Reason != telemetryPolicySegmentsUnavailableReason {
		t.Fatalf("live evidence %+v, interval %+v", evidence, interval)
	}
	// No re-read can repair it, so none is made (the counting store serves
	// the stored rows as they are).
	reads := 0
	runner.mu.Lock()
	runner.testStore = markingSegmentStore{SQLiteStore: store, mark: func() { reads++ }}
	runner.mu.Unlock()
	runner.retryTelemetryPolicySegments(test, time.Now().Add(time.Hour))
	if _, _, err := runner.TelemetryPolicySegments(test.ID); err != nil || reads != 0 {
		t.Fatalf("an invalid receipt was re-read %d times (%v)", reads, err)
	}
	runner.mu.Lock()
	runner.testStore = invalidReceiptSegmentStore{store}
	runner.mu.Unlock()
	// The policy cannot change, and a retry of the change that stored it is
	// refused rather than failed.
	for _, request := range []TelemetryPolicySegmentRequest{segmentRequest("after-invalid", runPolicyProjectDefault, 1), segmentRequest("invalid", runPolicyProjectDefault, 1)} {
		if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, request, testCreationActor{}); !errors.Is(err, errTelemetrySegmentState) || !strings.Contains(err.Error(), "invalid receipt") {
			t.Fatalf("change %s after an invalid receipt: %v", request.RequestID, err)
		}
	}
	boundary := segment.EffectiveAt
	runner.mu.Lock()
	start, end := *test.StartedAt, boundary.Add(10*time.Second)
	test.Status, test.EndedAt = "COMPLETE", &end
	snapshot := cloneManagedTestForReadLocked(test)
	runner.mu.Unlock()
	rows := []IntervalSummaryRecord{segmentRow(boundary.Add(-2*time.Second), "healthy", 1, 10), segmentRow(boundary.Add(2*time.Second), "healthy", 2, 10)}
	record, manifest := writeSegmentArchive(t, test.ID, start, rows, RunArchiveManifest{CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &start}})
	if err := runner.persistTelemetryCoverage(context.Background(), snapshot, record, manifest); err != nil {
		t.Fatalf("finalize with an invalid stored receipt: %v", err)
	}
	if len(manifest.TelemetryPolicySegments) != 2 || manifest.TelemetryPolicySegments[1].Policy != nil || manifest.TelemetryCoverage == nil || manifest.TelemetryCoverage.Segments[1].Unavailable != "immutable-policy-invalid" {
		t.Fatalf("manifest segments %+v, coverage %+v", manifest.TelemetryPolicySegments, manifest.TelemetryCoverage)
	}
	if view := buildTelemetryAcceptanceReportView(manifest, nil); len(view.Segments) != 2 || view.Segments[1].Policy != "receipt invalid" {
		t.Fatalf("report segments %+v", view.Segments)
	}
	// An ended run this controller never loaded reads its list from the
	// store, as the manifest records it; a failed read is an error, not a
	// run that never changed.
	runner.mu.Lock()
	test.telemetryPolicySegments, test.telemetrySegmentsInvalid = nil, nil
	runner.mu.Unlock()
	if listed, ok, err := runner.TelemetryPolicySegments(test.ID); !ok || err != nil || len(listed) != 2 || listed[1].Policy != nil || listed[1].Sequence != 2 {
		t.Fatalf("listed segments %+v (%v)", listed, err)
	}
	runner.mu.Lock()
	runner.testStore = unavailableSegmentStore{store}
	runner.mu.Unlock()
	if _, _, err := runner.TelemetryPolicySegments(test.ID); err == nil {
		t.Fatal("an unreadable ended run's segments were listed")
	}
	// A clean re-read (the row repaired) clears the invalid mark.
	runner.mu.Lock()
	runner.testStore = store
	test.telemetrySegmentsUnavailable, test.telemetrySegmentsInvalid = true, []TelemetryPolicySegmentReceipt{{Sequence: 2}}
	runner.mu.Unlock()
	runner.restoreTelemetryPolicySegments(test)
	runner.mu.RLock()
	unavailable, invalidSegments = test.telemetrySegmentsUnavailable, len(test.telemetrySegmentsInvalid)
	runner.mu.RUnlock()
	if unavailable || invalidSegments != 0 {
		t.Fatalf("after a clean re-read: unavailable %v, invalid %d", unavailable, invalidSegments)
	}
	// An invalid stored row never duplicates a sequence already held.
	merged := telemetryPolicySegmentsWithInvalid([]TelemetryPolicySegmentReceipt{{Sequence: 1}, {Sequence: 2, Policy: &TelemetryBudgetPolicy{}}}, []TelemetryPolicySegmentReceipt{{Sequence: 2}, {Sequence: 3}})
	if len(merged) != 3 || merged[1].Policy == nil || merged[2].Sequence != 3 {
		t.Fatalf("merged %+v", merged)
	}
}

// A valid segment stored after an invalid one is installed and listed,
// but never classifies the run: the segment in force is unknown.
func TestTelemetryPolicySegmentAfterAnInvalidOne(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-after-invalid")
	for i, id := range []string{"second", "third"} {
		if _, _, err := runner.AppendTelemetryPolicySegment(test.ID, segmentRequest(id, runPolicyProjectDefault, int64(i+1)), testCreationActor{}); err != nil {
			t.Fatal(err)
		}
	}
	runner.mu.Lock()
	runner.testStore = invalidReceiptSegmentStore{store}
	test.telemetryPolicySegments = nil
	runner.mu.Unlock()
	runner.restoreTelemetryPolicySegments(test)
	if listed, _, err := runner.TelemetryPolicySegments(test.ID); err != nil || len(listed) != 3 || listed[1].Policy != nil || listed[2].Policy == nil {
		t.Fatalf("segments after an invalid one: %+v (%v)", listed, err)
	}
	runner.mu.RLock()
	installed := len(test.telemetryPolicySegments)
	interval := telemetryIntervalPolicyLocked(test, time.Now().Add(time.Second), time.Now().Add(2*time.Second))
	runner.mu.RUnlock()
	if installed != 1 || !interval.unavailable || interval.ok {
		t.Fatalf("installed %d, interval %+v", installed, interval)
	}
}

// Finalization reads the segments only once no change is being written, so
// a change admitted before the run ended is in what it records.
func TestTelemetryPolicySegmentsFinalizeWaitsForAChangeBeingWritten(t *testing.T) {
	runner, _, test := runningTelemetrySegmentFixture(t, "segments-finalize-wait")
	runner.mu.Lock()
	start, end := *test.StartedAt, time.Now()
	test.Status, test.EndedAt = "COMPLETE", &end
	snapshot := cloneManagedTestForReadLocked(test)
	runner.mu.Unlock()
	record, manifest := writeSegmentArchive(t, test.ID, start, []IntervalSummaryRecord{segmentRow(end.Add(-time.Second), "healthy", 1, 10)}, RunArchiveManifest{CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &start}})
	runner.telemetrySegmentMu.Lock()
	done := make(chan error, 1)
	go func() { done <- runner.persistTelemetryCoverage(context.Background(), snapshot, record, manifest) }()
	select {
	case err := <-done:
		runner.telemetrySegmentMu.Unlock()
		t.Fatalf("finalize read the segments while a change was being written: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	runner.telemetrySegmentMu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// markingSegmentStore records a write failure while a re-read is reading.
type markingSegmentStore struct {
	*storage.SQLiteStore
	mark func()
}

func (s markingSegmentStore) ListTelemetryPolicySegments(testID string) ([]storage.TelemetryPolicySegment, error) {
	s.mark()
	return s.SQLiteStore.ListTelemetryPolicySegments(testID)
}

// A re-read clears the failure mark only if no failure was marked after it
// began: a later failure's segment may be missing from what it read.
func TestTelemetryPolicySegmentRereadKeepsALaterMark(t *testing.T) {
	runner, store, test := runningTelemetrySegmentFixture(t, "segments-mark")
	runner.mu.Lock()
	test.telemetrySegmentsUnavailable = true
	runner.testStore = markingSegmentStore{SQLiteStore: store, mark: func() {
		runner.mu.Lock()
		test.telemetrySegmentsMark++
		runner.mu.Unlock()
	}}
	runner.mu.Unlock()
	runner.restoreTelemetryPolicySegments(test)
	runner.mu.RLock()
	unavailable := test.telemetrySegmentsUnavailable
	runner.mu.RUnlock()
	if !unavailable {
		t.Fatal("a re-read cleared a failure marked while it read")
	}
	runner.mu.Lock()
	runner.testStore = store
	runner.mu.Unlock()
	runner.restoreTelemetryPolicySegments(test)
	runner.mu.RLock()
	unavailable = test.telemetrySegmentsUnavailable
	runner.mu.RUnlock()
	if unavailable {
		t.Fatal("a clean re-read kept the mark")
	}
}

// The finalization step that writes the manifest's adjuncts fails while the
// run's segments cannot be read, so the job retries it, and succeeds once
// they can.
func TestTelemetryPolicySegmentsUnreadableFailTheFinalizationStep(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv(envArchiveAdjunctMinFreeBytes, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, record := createArchiveFinalizationTest(t, store, "segments-finalize-step", root)
	runner := NewTestRunner(1, nil, unavailableSegmentStore{store})
	if err := runner.persistTestManifestArtifact(run, record); !errors.Is(err, errTelemetrySegmentsUnreadable) {
		t.Fatalf("finalize with unreadable segments: %v", err)
	}
	reads := 0
	runner.mu.Lock()
	runner.testStore = markingSegmentStore{SQLiteStore: store, mark: func() { reads++ }}
	runner.mu.Unlock()
	if err := runner.persistTestManifestArtifact(run, record); err != nil || reads != 1 {
		t.Fatalf("retry: %v (segment reads %d)", err, reads)
	}
}

package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetrypolicy"
)

// TelemetryPolicySegmentReceipt is one segment of a run's telemetry budget
// policy (TEL-B): from EffectiveAt, the run's live telemetry is classified by
// Policy. Sequence 1 is the receipt captured at admission; each later one is
// an explicitly requested, append-only change made while the run ran. A
// receipt is never edited: a change appends the next segment.
type TelemetryPolicySegmentReceipt struct {
	Sequence    int64     `json:"sequence"`
	EffectiveAt time.Time `json:"effective_at"`
	// Source is "admission" for sequence 1, otherwise "system_settings" (the
	// controller's threshold settings when the change was made) or
	// "run_selection" (a project telemetry budget policy).
	Source      string                 `json:"source"`
	PolicyID    string                 `json:"policy_id,omitempty"`
	Policy      *TelemetryBudgetPolicy `json:"policy"`
	RequestedBy string                 `json:"requested_by,omitempty"`
}

// TelemetryPolicySegmentRequest asks to change a running test's telemetry
// budget policy from now on.
type TelemetryPolicySegmentRequest struct {
	// RequestID identifies the change: a retry with the same ID returns the
	// segment it created instead of appending another.
	RequestID string `json:"request_id"`
	// TelemetryBudgetPolicyID names one of the run's project's telemetry
	// budget policies, or project_default for the controller's threshold
	// settings.
	TelemetryBudgetPolicyID string `json:"telemetry_budget_policy_id"`
	// ControllerPublicationIntervalSeconds changes a local run's publication
	// cadence (whole seconds); omitted keeps the current one. A distributed
	// run's cadence is fixed at one second.
	ControllerPublicationIntervalSeconds int `json:"controller_publication_interval_seconds,omitempty"`
	// ExpectedSequence is the run's latest segment as the requester saw it
	// (1 before any change): a change made meanwhile refuses this one.
	ExpectedSequence int64 `json:"expected_sequence"`
}

const (
	telemetrySegmentSourceAdmission      = "admission"
	telemetrySegmentSourceSystemSettings = "system_settings"
	telemetrySegmentSourceRunSelection   = "run_selection"
	maxTelemetrySegmentRequestIDBytes    = 128
	maxTelemetrySegmentIntervalSeconds   = 3600
)

var (
	// errTelemetrySegmentRequest is a malformed change request (400).
	errTelemetrySegmentRequest = errors.New("invalid telemetry policy change")
	// errTelemetrySegmentState refuses a change the run cannot take now:
	// it is not running, it has no admission receipt, or a change was made
	// meanwhile (409).
	errTelemetrySegmentState = errors.New("the run's telemetry policy cannot change now")
	// errTelemetrySegmentRequestReused is a request ID already used for a
	// different change (409).
	errTelemetrySegmentRequestReused = errors.New("request_id was used for a different telemetry policy change")
)

// telemetryPolicySegmentsLocked is the run's segments, the admission receipt
// first; nil for a run admitted without a valid receipt. The caller holds
// tr.mu.
func telemetryPolicySegmentsLocked(test *ManagedTest) []TelemetryPolicySegmentReceipt {
	if test == nil || test.initialArchiveProvenance == nil || !telemetrypolicy.ValidateAdmission(test.initialArchiveProvenance.TelemetryPolicy) {
		return nil
	}
	admission := test.initialArchiveProvenance.TelemetryPolicy
	start := admission.CapturedAt
	if test.StartedAt != nil {
		start = *test.StartedAt
	}
	out := make([]TelemetryPolicySegmentReceipt, 0, 1+len(test.telemetryPolicySegments))
	out = append(out, TelemetryPolicySegmentReceipt{Sequence: 1, EffectiveAt: start.UTC(), Source: telemetrySegmentSourceAdmission, PolicyID: admission.PolicyID, Policy: cloneTelemetryBudgetPolicy(admission)})
	for _, segment := range test.telemetryPolicySegments {
		segment.Policy = cloneTelemetryBudgetPolicy(segment.Policy)
		out = append(out, segment)
	}
	return out
}

// telemetryPolicySegmentsListedLocked is the run's segments as readers see
// them: those stored with a receipt that does not validate are listed
// without one.
func telemetryPolicySegmentsListedLocked(test *ManagedTest) []TelemetryPolicySegmentReceipt {
	return telemetryPolicySegmentsWithInvalid(telemetryPolicySegmentsLocked(test), test.telemetrySegmentsInvalid)
}

// telemetryPolicySegmentAt is the segment in force at instant: the latest
// whose effective time is not after it.
func telemetryPolicySegmentAt(segments []TelemetryPolicySegmentReceipt, instant time.Time) (TelemetryPolicySegmentReceipt, bool) {
	var found TelemetryPolicySegmentReceipt
	ok := false
	for _, segment := range segments {
		if segment.Sequence == 1 || !segment.EffectiveAt.After(instant) {
			found, ok = segment, true
		}
	}
	return found, ok
}

// telemetryIntervalPolicy is the segment that classifies the live interval
// (from, to] on the controller's clock. An interval that a segment boundary
// falls inside, or that ends after a change still being persisted began, is
// classified by none (spanning): its measurements were taken partly under
// each policy, and they are never split between them.
type telemetryIntervalPolicy struct {
	segment  TelemetryPolicySegmentReceipt
	ok       bool
	spanning bool
	// boundary: a stored segment boundary falls inside the interval (not
	// merely a change still being persisted, or segments that could not be
	// read), so a breach spell under the previous segment ends.
	boundary bool
	// unavailable: the run's segments could not be read (or one's receipt
	// does not validate), so no segment can classify the interval.
	unavailable bool
}

// telemetryMeasurement is when a live publication's measurements began: its
// oldest sample's collection time, on the controller's clock for a local run
// and on its generator's for a distributed one, with that generator's clock
// offset and quality.
type telemetryMeasurement struct {
	collectedAt time.Time
	offsetMs    float64
	quality     *generatorrpc.ClockQuality
}

func telemetryMeasurementOf(live *LiveMetrics) telemetryMeasurement {
	if live == nil || live.Telemetry == nil || live.Telemetry.SampleAgeUnavailable {
		return telemetryMeasurement{}
	}
	return telemetryMeasurement{collectedAt: live.Telemetry.CollectedAt, offsetMs: live.Telemetry.collectedAtOffsetMs, quality: live.Telemetry.collectedAtQuality}
}

// start is the earliest controller-clock instant the measurements may have
// begun: a local sample's collection time; a distributed sample's, mapped
// by its qualified clock offset less the uncertainty. A run with no sample
// measures nothing earlier than the interval (zero, known). A distributed
// sample on an unqualified clock cannot be placed (known false).
func (m telemetryMeasurement) start(distributed bool) (time.Time, bool) {
	if m.collectedAt.IsZero() {
		return time.Time{}, true
	}
	if !distributed {
		return m.collectedAt, true
	}
	if m.quality == nil || !m.quality.Qualified {
		return time.Time{}, false
	}
	corrected := float64(m.collectedAt.UnixMicro())/1000 - m.offsetMs - m.quality.UncertaintyMs
	return time.UnixMicro(int64(corrected * 1000)), true
}

func telemetryIntervalPolicyLocked(test *ManagedTest, from, to time.Time) telemetryIntervalPolicy {
	segments := telemetryPolicySegmentsLocked(test)
	if len(segments) == 0 {
		return telemetryIntervalPolicy{}
	}
	if test.telemetrySegmentsUnavailable {
		return telemetryIntervalPolicy{spanning: true, unavailable: true}
	}
	if test.telemetrySegmentPendingAt != nil && to.After(*test.telemetrySegmentPendingAt) {
		return telemetryIntervalPolicy{spanning: true}
	}
	for _, segment := range segments[1:] {
		if segment.EffectiveAt.After(from) && segment.EffectiveAt.Before(to) {
			return telemetryIntervalPolicy{spanning: true, boundary: true}
		}
	}
	segment, ok := telemetryPolicySegmentAt(segments, from)
	return telemetryIntervalPolicy{segment: segment, ok: ok}
}

// TelemetryPolicySegments returns the run's telemetry policy segments, the
// admission receipt first. A run whose installed segments may be incomplete
// reads them from the store: one marked for a re-read (its segments could
// not be read, or a write's outcome is unknown), or one that has ended whose
// segments this controller has not loaded (it was reloaded after a restart).
// An HA follower never installs a run's segments, so it always reads them.
// err is that read's failure (the run may have changed).
func (tr *TestRunner) TelemetryPolicySegments(testID string) ([]TelemetryPolicySegmentReceipt, bool, error) {
	tr.refreshTestsForHARead()
	// Not yet a leader that has adopted its runs (whose segments it installs
	// on adoption) reads them from the store too.
	follower := !tr.isExecutionLeader()
	tr.mu.RLock()
	testID = tr.resolveRefKeyLocked(testID)
	test, ok := tr.tests[testID]
	if !ok {
		tr.mu.RUnlock()
		return nil, false, nil
	}
	segments := telemetryPolicySegmentsListedLocked(test)
	// A run with an invalid stored receipt already lists every stored
	// segment (re-reading cannot repair it).
	stale := follower || test.telemetrySegmentsUnavailable && len(test.telemetrySegmentsInvalid) == 0 || len(segments) == 1 && isTerminalTestStatus(test.Status)
	tr.mu.RUnlock()
	store, stores := tr.testStore.(storage.TelemetryPolicySegmentStore)
	if len(segments) == 0 || !stale || !stores || store == nil {
		return segments, true, nil
	}
	receipts, invalid, _, err := tr.readTelemetryPolicySegments(testID)
	if err != nil {
		return nil, true, err
	}
	// Merged with what is installed, and listed as the manifest records
	// them: a segment whose receipt does not validate appears without one.
	held := make(map[int64]bool, len(segments))
	for _, segment := range segments {
		held[segment.Sequence] = true
	}
	for _, receipt := range receipts {
		if !held[receipt.Sequence] {
			held[receipt.Sequence] = true
			segments = append(segments, receipt)
		}
	}
	sort.SliceStable(segments, func(i, j int) bool { return segments[i].Sequence < segments[j].Sequence })
	return telemetryPolicySegmentsWithInvalid(segments, invalid), true, nil
}

// AppendTelemetryPolicySegment changes a running test's telemetry budget
// policy from now on: it captures the chosen policy's receipt, stores it as
// the run's next segment (fenced by the execution lease on an HA controller)
// and installs it. The boundary is the controller time taken before the
// write; until the write commits, intervals ending after it are not
// classified, and a failed write leaves the current segment in force.
func (tr *TestRunner) AppendTelemetryPolicySegment(testID string, req TelemetryPolicySegmentRequest, actor testCreationActor) (*TelemetryPolicySegmentReceipt, bool, error) {
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.TelemetryBudgetPolicyID = strings.TrimSpace(req.TelemetryBudgetPolicyID)
	switch {
	case req.RequestID == "" || len(req.RequestID) > maxTelemetrySegmentRequestIDBytes:
		return nil, false, fmt.Errorf("%w: request_id must be 1 to %d bytes", errTelemetrySegmentRequest, maxTelemetrySegmentRequestIDBytes)
	case req.TelemetryBudgetPolicyID == "":
		return nil, false, fmt.Errorf("%w: telemetry_budget_policy_id is required (a policy ID, or %s for the threshold settings)", errTelemetrySegmentRequest, runPolicyProjectDefault)
	case req.ExpectedSequence < 1:
		return nil, false, fmt.Errorf("%w: expected_sequence must be at least 1", errTelemetrySegmentRequest)
	case req.ControllerPublicationIntervalSeconds < 0 || req.ControllerPublicationIntervalSeconds > maxTelemetrySegmentIntervalSeconds:
		return nil, false, fmt.Errorf("%w: controller_publication_interval_seconds must be 1 to %d", errTelemetrySegmentRequest, maxTelemetrySegmentIntervalSeconds)
	}
	tr.telemetrySegmentMu.Lock()
	defer tr.telemetrySegmentMu.Unlock()

	tr.mu.RLock()
	testID = tr.resolveRefKeyLocked(testID)
	test, ok := tr.tests[testID]
	unavailable := ok && test.telemetrySegmentsUnavailable && len(test.telemetrySegmentsInvalid) == 0
	tr.mu.RUnlock()
	if unavailable {
		tr.restoreTelemetryPolicySegments(test)
	}
	tr.mu.RLock()
	test, ok = tr.tests[testID]
	if !ok {
		tr.mu.RUnlock()
		return nil, false, sql.ErrNoRows
	}
	projectID := normalizeProjectID(test.ProjectID)
	status := test.Status
	segmentsUnavailable := test.telemetrySegmentsUnavailable
	var invalidSegment int64
	if len(test.telemetrySegmentsInvalid) > 0 {
		invalidSegment = test.telemetrySegmentsInvalid[0].Sequence
	}
	segments := telemetryPolicySegmentsLocked(test)
	executionFence := test.executionFence
	durableFence := tr.durableExecutionFence
	settings := cloneSystemSettings(tr.settings)
	tr.mu.RUnlock()

	store, ok := tr.testStore.(storage.TelemetryPolicySegmentStore)
	if !ok || store == nil {
		return nil, false, fmt.Errorf("%w: this controller's storage keeps no telemetry policy segments", errTelemetrySegmentState)
	}
	// A retry is answered from the store, whatever the run's state now.
	if existing, found, err := telemetryPolicySegmentByRequest(store, testID, req.RequestID); err != nil {
		return nil, false, fmt.Errorf("%w: %v", errRunPolicyStoreUnavailable, err)
	} else if found {
		receipt, err := telemetryPolicySegmentReceiptFromStorage(existing)
		if err != nil {
			// The change was stored, but its receipt does not validate here
			// (another controller version wrote it): refused, not retried.
			return nil, false, fmt.Errorf("%w: the stored telemetry policy segment %d has an invalid receipt", errTelemetrySegmentState, existing.Sequence)
		}
		if !telemetryPolicySegmentMatchesRequest(receipt, req) {
			return nil, false, errTelemetrySegmentRequestReused
		}
		// A change whose answer was lost is in force: install it if this
		// controller has not.
		tr.mu.Lock()
		if tr.tests[testID] == test {
			installTelemetryPolicySegmentLocked(test, *receipt)
		}
		tr.mu.Unlock()
		return receipt, true, nil
	}
	if status != "RUNNING" && status != "PAUSED" {
		return nil, false, fmt.Errorf("%w: the run is %s; only a running or paused run's telemetry policy can change", errTelemetrySegmentState, strings.ToLower(status))
	}
	if len(segments) == 0 {
		return nil, false, fmt.Errorf("%w: the run was admitted without a telemetry policy receipt", errTelemetrySegmentState)
	}
	if invalidSegment > 0 {
		return nil, false, fmt.Errorf("%w: the run's stored telemetry policy segment %d has an invalid receipt", errTelemetrySegmentState, invalidSegment)
	}
	if segmentsUnavailable {
		return nil, false, fmt.Errorf("%w: the run's stored telemetry policy segments could not be read", errRunPolicyStoreUnavailable)
	}
	latest := segments[len(segments)-1]
	if req.ExpectedSequence != latest.Sequence {
		return nil, false, fmt.Errorf("%w: its latest segment is %d, not %d", errTelemetrySegmentState, latest.Sequence, req.ExpectedSequence)
	}
	current := latest.Policy
	distributed := current.CadenceScope == "distributed-controller-coalescing-gate"
	intervalSeconds := int(current.ExpectedControllerPublicationIntervalMs / 1000)
	if req.ControllerPublicationIntervalSeconds > 0 {
		if distributed && req.ControllerPublicationIntervalSeconds != 1 {
			return nil, false, fmt.Errorf("%w: a distributed run publishes every second; its cadence cannot change", errTelemetrySegmentRequest)
		}
		intervalSeconds = req.ControllerPublicationIntervalSeconds
	}

	// The receipt: the controller fields are the run's (cadence as asked),
	// the budgets the chosen policy's or the current threshold settings'.
	now := time.Now().UTC()
	snapshot := RunArchiveExecutionSettingsSnapshot{General: settings.General, Thresholds: settings.Thresholds, CapacityPlanning: settings.CapacityPlanning}
	snapshot.General.MetricsIntervalSeconds = intervalSeconds
	base := captureTelemetryBudgetPolicy(snapshot, distributed, now)
	if base == nil {
		return nil, false, fmt.Errorf("%w: the threshold settings cannot form a telemetry policy receipt", errRunPolicyConfiguration)
	}
	source, policyID := telemetrySegmentSourceSystemSettings, ""
	receipt := base
	if req.TelemetryBudgetPolicyID != runPolicyProjectDefault {
		chosen, err := tr.validateRunTelemetryBudgetPolicy(projectID, req.TelemetryBudgetPolicyID)
		if err != nil && !errors.Is(err, errRunPolicyConfiguration) && !errors.Is(err, errRunPolicyStoreUnavailable) {
			// The project lacks the policy, or the ID is malformed.
			return nil, false, fmt.Errorf("%w: %v", errTelemetrySegmentRequest, err)
		}
		if err != nil {
			return nil, false, err
		}
		definitions, _ := tr.testStore.(storage.AnalysisDefinitionStore)
		definition, err := projectPolicyByID(definitions, projectID, storage.AnalysisDefinitionKindTelemetryPolicy, chosen)
		if err != nil && !errors.Is(err, errRunPolicyConfiguration) {
			return nil, false, fmt.Errorf("%w: %v", errRunPolicyStoreUnavailable, err)
		}
		if err != nil {
			return nil, false, err
		}
		if definition == nil {
			return nil, false, fmt.Errorf("%w: telemetry budget policy %q was not found in the project", errTelemetrySegmentRequest, chosen)
		}
		if receipt, err = projectTelemetryBudgetPolicy(base, definition); err != nil {
			return nil, false, err
		}
		source, policyID = telemetrySegmentSourceRunSelection, chosen
	}
	receipt = cloneTelemetryBudgetPolicy(receipt)
	receipt.CaptureBoundary = telemetrypolicy.CaptureBoundarySegment
	receipt.CapturedAt = now
	receipt.Hash = telemetryBudgetPolicyHash(receipt)
	if !validTelemetryBudgetPolicy(receipt) {
		return nil, false, fmt.Errorf("%w: the change cannot form a valid telemetry policy receipt", errRunPolicyConfiguration)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, false, err
	}

	leaseName, leaseOwner := "", ""
	if durableFence {
		if executionFence == "" {
			return nil, false, fmt.Errorf("%w: test %s has no claimed execution epoch", errNotExecutionLeader, testID)
		}
		leaseName, leaseOwner = executionLeaseName, executionFence
	}
	// The boundary is taken under the runner lock, so no interval is
	// classified by the old segment past it; it is pending until the write
	// returns.
	tr.mu.Lock()
	if tr.tests[testID] != test || (test.Status != "RUNNING" && test.Status != "PAUSED") {
		tr.mu.Unlock()
		return nil, false, fmt.Errorf("%w: the run is no longer running", errTelemetrySegmentState)
	}
	// Stored with microsecond precision (PostgreSQL's): every copy uses it.
	effectiveAt := atMicrosecond(time.Now()).UTC()
	test.telemetrySegmentPendingAt = &effectiveAt
	tr.mu.Unlock()

	var stored storage.TelemetryPolicySegment
	var replayed bool
	err = tr.trackDurableWrite(func() error {
		var writeErr error
		stored, replayed, writeErr = store.AppendTelemetryPolicySegmentIfLeaseHeld(storage.TelemetryPolicySegment{
			TestID: testID, EffectiveAt: effectiveAt, Receipt: encoded, Source: source, PolicyID: policyID,
			RequestID: req.RequestID, RequestedBySubject: actor.Subject, RequestedByDisplayName: actor.DisplayName,
		}, req.ExpectedSequence, leaseName, leaseOwner)
		return writeErr
	})

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.telemetrySegmentPendingAt != nil && test.telemetrySegmentPendingAt.Equal(effectiveAt) {
		test.telemetrySegmentPendingAt = nil
	}
	if err != nil && !errors.Is(err, storage.ErrTelemetryPolicySegmentLease) && tr.tests[testID] == test {
		// The store may hold a segment this controller has not installed: a
		// conflict means another change was stored, and a failed reply may
		// follow a committed write. Until the stored segments are read again
		// (by the next publication), no segment classifies the run.
		test.telemetrySegmentsUnavailable = true
		test.telemetrySegmentsRetryAt = time.Now()
		test.telemetrySegmentsMark++
	}
	switch {
	case errors.Is(err, storage.ErrTelemetryPolicySegmentLease):
		return nil, false, fmt.Errorf("%w: test %s belongs to an expired execution epoch", errNotExecutionLeader, testID)
	case errors.Is(err, storage.ErrTelemetryPolicySegmentConflict):
		return nil, false, fmt.Errorf("%w: a change was made meanwhile; read the run's segments and try again", errTelemetrySegmentState)
	case err != nil:
		return nil, false, err
	}
	appended, err := telemetryPolicySegmentReceiptFromStorage(stored)
	if err != nil {
		return nil, false, err
	}
	if replayed && !telemetryPolicySegmentMatchesRequest(appended, req) {
		// A concurrent request with this ID won the write for another change.
		return nil, false, errTelemetrySegmentRequestReused
	}
	if tr.tests[testID] == test {
		installTelemetryPolicySegmentLocked(test, *appended)
	}
	return appended, replayed, nil
}

// installTelemetryPolicySegmentLocked adds a stored segment to the live test
// in sequence order, once. The caller holds tr.mu.
func installTelemetryPolicySegmentLocked(test *ManagedTest, segment TelemetryPolicySegmentReceipt) {
	for _, existing := range test.telemetryPolicySegments {
		if existing.Sequence == segment.Sequence {
			return
		}
	}
	// A sustained breach is a breach under one policy: the new segment
	// starts its own spell.
	test.telemetryDiagnostic.BreachStartedAt = nil
	test.telemetryPolicySegments = append(test.telemetryPolicySegments, segment)
	for i := len(test.telemetryPolicySegments) - 1; i > 0 && test.telemetryPolicySegments[i].Sequence < test.telemetryPolicySegments[i-1].Sequence; i-- {
		test.telemetryPolicySegments[i], test.telemetryPolicySegments[i-1] = test.telemetryPolicySegments[i-1], test.telemetryPolicySegments[i]
	}
}

func telemetryPolicySegmentByRequest(store storage.TelemetryPolicySegmentStore, testID, requestID string) (storage.TelemetryPolicySegment, bool, error) {
	segments, err := store.ListTelemetryPolicySegments(testID)
	if err != nil {
		return storage.TelemetryPolicySegment{}, false, err
	}
	for _, segment := range segments {
		if segment.RequestID == requestID {
			return segment, true, nil
		}
	}
	return storage.TelemetryPolicySegment{}, false, nil
}

// telemetryPolicySegmentMatchesRequest reports whether a stored segment is
// the change req asks for: the same policy (or the threshold settings) and,
// when req names one, the same cadence.
func telemetryPolicySegmentMatchesRequest(segment *TelemetryPolicySegmentReceipt, req TelemetryPolicySegmentRequest) bool {
	if segment == nil || segment.Policy == nil {
		return false
	}
	if req.TelemetryBudgetPolicyID == runPolicyProjectDefault {
		if segment.Source != telemetrySegmentSourceSystemSettings {
			return false
		}
	} else if segment.Source != telemetrySegmentSourceRunSelection || segment.PolicyID != req.TelemetryBudgetPolicyID {
		return false
	}
	return req.ControllerPublicationIntervalSeconds == 0 || segment.Policy.ExpectedControllerPublicationIntervalMs == int64(req.ControllerPublicationIntervalSeconds)*1000
}

// telemetryPolicySegmentReceiptFromStorage decodes a stored segment; a
// receipt that fails validation is refused, never presented.
func telemetryPolicySegmentReceiptFromStorage(stored storage.TelemetryPolicySegment) (*TelemetryPolicySegmentReceipt, error) {
	var policy TelemetryBudgetPolicy
	if err := json.Unmarshal(stored.Receipt, &policy); err != nil || !validTelemetryBudgetPolicy(&policy) || policy.CaptureBoundary != telemetrypolicy.CaptureBoundarySegment {
		return nil, fmt.Errorf("telemetry policy segment %d of test %s has an invalid receipt", stored.Sequence, stored.TestID)
	}
	return &TelemetryPolicySegmentReceipt{
		Sequence: stored.Sequence, EffectiveAt: stored.EffectiveAt.UTC(), Source: stored.Source, PolicyID: stored.PolicyID,
		Policy: &policy, RequestedBy: firstNonEmpty(stored.RequestedByDisplayName, stored.RequestedBySubject),
	}, nil
}

// restoreTelemetryPolicySegments installs an adopted or reloaded run's
// stored segments. A run whose segments cannot be read is classified by none
// of them: its live intervals stay unscored, rather than judged by a receipt
// a stored change superseded.
func (tr *TestRunner) restoreTelemetryPolicySegments(test *ManagedTest) {
	if test == nil {
		return
	}
	tr.mu.RLock()
	mark := test.telemetrySegmentsMark
	tr.mu.RUnlock()
	receipts, invalid, supported, err := tr.readTelemetryPolicySegments(test.ID)
	if !supported {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test {
		return
	}
	if err == nil && len(invalid) > 0 {
		// No segment can classify the run past a change whose receipt does
		// not validate, and no re-read repairs it: the run stays unscored
		// and its policy cannot change. Its segments are still listed, that
		// one without a receipt.
		slog.Warn("a stored telemetry policy segment has an invalid receipt; live telemetry stays unscored", "test_id", test.ID, "sequence", invalid[0].Sequence)
		test.telemetrySegmentsUnavailable = true
		test.telemetrySegmentsInvalid = invalid
		for _, receipt := range receipts {
			installTelemetryPolicySegmentLocked(test, receipt)
		}
		return
	}
	if err != nil {
		slog.Warn("telemetry policy segments unavailable; live telemetry stays unscored", "test_id", test.ID, "error", err)
		test.telemetrySegmentsUnavailable = true
		test.telemetrySegmentsRetryAt = time.Now().Add(telemetrySegmentsRetryInterval)
		return
	}
	// Merged with what is installed: a change this controller made meanwhile
	// is kept (segments are append-only). A failure marked since the read
	// began needs another read.
	test.telemetrySegmentsInvalid = nil
	if test.telemetrySegmentsMark == mark {
		test.telemetrySegmentsUnavailable = false
	}
	for _, receipt := range receipts {
		installTelemetryPolicySegmentLocked(test, receipt)
	}
}

// telemetryPolicyBoundaryUnscored marks a live interval a telemetry policy
// segment boundary fell inside.
const telemetryPolicyBoundaryUnscored = "policy:segment_boundary"

// telemetryPolicySegmentsUnavailableUnscored marks a live interval no segment
// could classify because the run's segments could not be read.
const telemetryPolicySegmentsUnavailableUnscored = "policy:segments_unavailable"

// telemetryPolicyUnscoredSummary describes an interval no segment classifies.
func telemetryPolicyUnscoredSummary(reason string) string {
	if reason == telemetryPolicySegmentsUnavailableUnscored {
		return "telemetry policy segments unavailable"
	}
	return "telemetry policy changed during this interval"
}

// telemetryPolicySegmentsUnavailableReason is the live evidence's reason
// while no receipt is claimed because the run's segments could not be read.
const telemetryPolicySegmentsUnavailableReason = "policy-segments-unavailable"

// telemetrySegmentsRetryInterval spaces the reads that retry a run's
// unreadable segments.
const telemetrySegmentsRetryInterval = 5 * time.Second

// retryTelemetryPolicySegments retries reading a run's segments when they
// could not be read and the retry is due.
func (tr *TestRunner) retryTelemetryPolicySegments(test *ManagedTest, now time.Time) {
	tr.mu.RLock()
	due := test != nil && test.telemetrySegmentsUnavailable && len(test.telemetrySegmentsInvalid) == 0 && !now.Before(test.telemetrySegmentsRetryAt)
	tr.mu.RUnlock()
	if due {
		tr.restoreTelemetryPolicySegments(test)
	}
}

// errTelemetrySegmentsUnreadable: a run's stored telemetry policy segments
// could not be read where they decide what is recorded (finalization).
var errTelemetrySegmentsUnreadable = errors.New("the run's telemetry policy segments could not be read")

// readTelemetryPolicySegments reads a run's stored segments (sequence 2 on):
// those whose receipts decode, and those whose do not (without a policy).
// supported is false for a store that keeps none; err is a failed read.
func (tr *TestRunner) readTelemetryPolicySegments(testID string) ([]TelemetryPolicySegmentReceipt, []TelemetryPolicySegmentReceipt, bool, error) {
	store, ok := tr.testStore.(storage.TelemetryPolicySegmentStore)
	if !ok || store == nil {
		return nil, nil, false, nil
	}
	stored, err := store.ListTelemetryPolicySegments(testID)
	if err != nil {
		return nil, nil, true, err
	}
	var valid, invalid []TelemetryPolicySegmentReceipt
	for _, segment := range stored {
		receipt, err := telemetryPolicySegmentReceiptFromStorage(segment)
		if err != nil {
			slog.Warn("a stored telemetry policy segment has an invalid receipt", "test_id", testID, "sequence", segment.Sequence, "error", err)
			invalid = append(invalid, TelemetryPolicySegmentReceipt{Sequence: segment.Sequence, EffectiveAt: segment.EffectiveAt.UTC(), Source: segment.Source, PolicyID: segment.PolicyID, RequestedBy: firstNonEmpty(segment.RequestedByDisplayName, segment.RequestedBySubject)})
			continue
		}
		valid = append(valid, *receipt)
	}
	return valid, invalid, true, nil
}

// telemetryPolicySegmentsWithInvalid adds stored segments without a valid
// receipt to a run's segments, in sequence order; a sequence already held
// with a receipt keeps it.
func telemetryPolicySegmentsWithInvalid(segments, invalid []TelemetryPolicySegmentReceipt) []TelemetryPolicySegmentReceipt {
	if len(invalid) == 0 || len(segments) == 0 {
		return segments
	}
	out := append([]TelemetryPolicySegmentReceipt(nil), segments...)
	held := make(map[int64]bool, len(segments))
	for _, segment := range segments {
		held[segment.Sequence] = true
	}
	for _, segment := range invalid {
		if !held[segment.Sequence] {
			held[segment.Sequence] = true
			out = append(out, segment)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

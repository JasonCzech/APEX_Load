package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultLiveBrowserIntelligenceInterval = 30 * time.Second
	maxLiveBrowserInteractionRecords       = 2048
	maxLiveBrowserInteractionTotalCount    = 100_000
	maxLiveBrowserJourneyEvents            = 100_000
)

// liveBrowserIntelligenceTickInterval is the controller evaluation cadence for
// provisional browser intelligence. Tests may shorten it while preserving the
// same ticker lifecycle and stop-and-join barrier.
var liveBrowserIntelligenceTickInterval = defaultLiveBrowserIntelligenceInterval

// ProvisionalBrowserIntelligenceSnapshot holds the latest non-verdict live
// browser intelligence evaluation for an active or finalizing run. It is
// cached in RunLiveStateSnapshot so HA followers serve identical provisional
// views before the archive is finalized.
type ProvisionalBrowserIntelligenceSnapshot struct {
	UpdatedAt        time.Time                   `json:"updated_at"`
	LiveTruncated    bool                        `json:"live_truncated,omitempty"`
	TruncatedReasons []string                    `json:"truncated_reasons,omitempty"`
	Findings         *RunBrowserFindingsResponse `json:"findings,omitempty"`
	Interactions     *BrowserInteractionAnalysis `json:"interactions,omitempty"`
	Journeys         *JourneyRiskReport          `json:"journeys,omitempty"`
}

func cloneProvisionalBrowserIntelligenceSnapshot(src *ProvisionalBrowserIntelligenceSnapshot) *ProvisionalBrowserIntelligenceSnapshot {
	if src == nil {
		return nil
	}
	raw, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var dst ProvisionalBrowserIntelligenceSnapshot
	if err := json.Unmarshal(raw, &dst); err != nil {
		return nil
	}
	return &dst
}

type liveBrowserIntelligenceState struct {
	mu                       sync.Mutex
	testID                   string
	projectID                string
	distributed              bool
	documentCaptureRequested bool
	startedAt                time.Time
	policyPins               []RunPolicyPin

	expectedTargets  map[string]bool
	observedTargets  map[string]bool
	completedTargets map[string]bool
	seenCollections  map[string]bool

	documentAccumulator archiveBrowserDocumentAccumulator
	generationCohorts   browserGenerationCohorts
	documentRecords     int
	droppedDocuments    int64

	interactionsBySource map[string][]*protocol.BrowserInteractionEvidence
	interactionRecords   int
	interactionCount     int64
	droppedInteractions  int64

	journeyEventsBySource map[string][]TransactionEvent
	journeyEventCount     int
	droppedJourneyEvents  int64

	liveTruncated    bool
	truncatedReasons []string
	dirty            bool
	firstEvaluated   bool
	stopped          bool
	stopOnce         sync.Once
	stopCh           chan struct{}
	doneCh           chan struct{}
	notifyCh         chan struct{}
}

func newLiveBrowserIntelligenceState(test *ManagedTest, expectedTargets []string) *liveBrowserIntelligenceState {
	startedAt := time.Now().UTC()
	var pins []RunPolicyPin
	captureRequested := false
	testID, projectID := "", storage.DefaultProjectID
	if test != nil {
		testID = test.ID
		projectID = normalizeProjectID(test.ProjectID)
		if test.StartedAt != nil && !test.StartedAt.IsZero() {
			startedAt = test.StartedAt.UTC()
		} else if !test.CreatedAt.IsZero() {
			startedAt = test.CreatedAt.UTC()
		}
		if test.initialArchiveProvenance != nil {
			pins = cloneRunPolicyPins(test.initialArchiveProvenance.PolicyPins)
		}
		captureRequested = groupsRequestBrowserDocumentCapture(test.Groups)
	}
	expected := make(map[string]bool, len(expectedTargets))
	for _, target := range expectedTargets {
		if trimmed := strings.TrimSpace(target); trimmed != "" {
			expected[trimmed] = true
		}
	}
	return &liveBrowserIntelligenceState{
		testID:                   testID,
		projectID:                projectID,
		distributed:              len(expected) > 0,
		documentCaptureRequested: captureRequested,
		startedAt:                startedAt,
		policyPins:               pins,
		expectedTargets:          expected,
		observedTargets:          make(map[string]bool),
		completedTargets:         make(map[string]bool),
		seenCollections:          make(map[string]bool),
		generationCohorts: browserGenerationCohorts{
			byGeneration: make(map[string]browserGenerationCohort),
			bound:        maxInteractionGenerationCohorts,
			joinBound:    maxInteractionDocumentJoins,
		},
		interactionsBySource:  make(map[string][]*protocol.BrowserInteractionEvidence),
		journeyEventsBySource: make(map[string][]TransactionEvent),
		stopCh:                make(chan struct{}),
		doneCh:                make(chan struct{}),
		notifyCh:              make(chan struct{}, 1),
	}
}

func (s *liveBrowserIntelligenceState) markTruncatedLocked(reason string) {
	s.liveTruncated = true
	if reason != "" && !slices.Contains(s.truncatedReasons, reason) {
		s.truncatedReasons = append(s.truncatedReasons, reason)
	}
}

func (s *liveBrowserIntelligenceState) notifyDirtyLocked() {
	s.dirty = true
	if !s.firstEvaluated {
		select {
		case s.notifyCh <- struct{}{}:
		default:
		}
	}
}

func (s *liveBrowserIntelligenceState) recordDocumentEvidence(evidence *protocol.BrowserDocumentEvidence) {
	if s == nil || evidence == nil {
		return
	}
	cloned := evidence.Clone()
	s.mu.Lock()
	defer s.mu.Unlock()
	if cloned == nil {
		s.droppedDocuments += max(int64(len(evidence.Observations)), 1)
		s.markTruncatedLocked("malformed_document_evidence")
		s.notifyDirtyLocked()
		return
	}
	s.documentRecords++
	s.documentAccumulator.add(cloned)
	s.generationCohorts.add(cloned)
	if s.documentAccumulator.incomplete || s.generationCohorts.truncated || s.generationCohorts.joinsTruncated {
		s.markTruncatedLocked("document_evidence_truncated")
	}
	s.notifyDirtyLocked()
}

func (s *liveBrowserIntelligenceState) recordInteractionEvidence(source string, evidence *protocol.BrowserInteractionEvidence) {
	if s == nil || evidence == nil {
		return
	}
	cloned := evidence.Clone()
	s.mu.Lock()
	defer s.mu.Unlock()
	if cloned == nil {
		s.droppedInteractions += max(int64(len(evidence.Interactions)), 1)
		s.markTruncatedLocked("malformed_interaction_evidence")
		s.notifyDirtyLocked()
		return
	}
	if s.interactionRecords >= maxLiveBrowserInteractionRecords || s.interactionCount+int64(len(cloned.Interactions)) > maxLiveBrowserInteractionTotalCount {
		s.droppedInteractions += max(int64(len(cloned.Interactions)), 1) + max(cloned.Dropped, 0)
		s.markTruncatedLocked("interactions_truncated")
		s.notifyDirtyLocked()
		return
	}
	s.interactionsBySource[source] = append(s.interactionsBySource[source], cloned)
	s.interactionRecords++
	s.interactionCount += int64(len(cloned.Interactions))
	s.notifyDirtyLocked()
}

func (s *liveBrowserIntelligenceState) recordJourneyEvent(source string, event TransactionEvent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journeyEventCount >= maxLiveBrowserJourneyEvents {
		s.droppedJourneyEvents++
		s.markTruncatedLocked("journey_events_truncated")
		s.notifyDirtyLocked()
		return
	}
	if event.AuthoredStep != nil {
		event.AuthoredStep = event.AuthoredStep.Clone()
	}
	s.journeyEventsBySource[source] = append(s.journeyEventsBySource[source], event)
	s.journeyEventCount++
	s.notifyDirtyLocked()
}

func (s *liveBrowserIntelligenceState) observeGeneratorBatch(target string, own generatorrpc.MetricsBatch, terminal bool) {
	if s == nil {
		return
	}
	target = strings.TrimSpace(target)
	s.mu.Lock()
	if target != "" {
		s.expectedTargets[target] = true
		s.observedTargets[target] = true
		if terminal && !s.completedTargets[target] {
			s.completedTargets[target] = true
			s.notifyDirtyLocked()
		}
	}
	if repeatedFinalCollection(own) {
		s.mu.Unlock()
		return
	}
	if own.Telemetry != nil && own.Telemetry.CollectionSequence > 0 {
		key := target + "\x00" + own.SourceEpoch + "\x00" + strconv.FormatUint(own.Telemetry.CollectionSequence, 10)
		if s.seenCollections[key] {
			s.mu.Unlock()
			return
		}
		s.seenCollections[key] = true
	}
	if own.LiveBrowserTruncation != nil {
		if own.LiveBrowserTruncation.DroppedDocuments > 0 {
			s.droppedDocuments += own.LiveBrowserTruncation.DroppedDocuments
			s.markTruncatedLocked("generator_documents_truncated")
			s.notifyDirtyLocked()
		}
		if own.LiveBrowserTruncation.DroppedInteractions > 0 {
			s.droppedInteractions += own.LiveBrowserTruncation.DroppedInteractions
			s.markTruncatedLocked("generator_interactions_truncated")
			s.notifyDirtyLocked()
		}
		if own.LiveBrowserTruncation.DroppedJourneyEvents > 0 {
			s.droppedJourneyEvents += own.LiveBrowserTruncation.DroppedJourneyEvents
			s.markTruncatedLocked("generator_journey_events_truncated")
			s.notifyDirtyLocked()
		}
	}
	s.mu.Unlock()

	for _, rawDoc := range own.LiveBrowserDocumentsJSON {
		if strings.TrimSpace(rawDoc) == "" {
			continue
		}
		decoded, err := protocol.DecodeBrowserDocumentEvidence([]byte(rawDoc))
		if err != nil {
			s.mu.Lock()
			s.droppedDocuments++
			s.markTruncatedLocked("malformed_document_evidence")
			s.notifyDirtyLocked()
			s.mu.Unlock()
			continue
		}
		s.recordDocumentEvidence(decoded)
	}
	for _, rawInteraction := range own.LiveBrowserInteractionsJSON {
		if strings.TrimSpace(rawInteraction) == "" {
			continue
		}
		var evidence protocol.BrowserInteractionEvidence
		if err := json.Unmarshal([]byte(rawInteraction), &evidence); err != nil {
			s.mu.Lock()
			s.droppedInteractions++
			s.markTruncatedLocked("malformed_interaction_evidence")
			s.notifyDirtyLocked()
			s.mu.Unlock()
			continue
		}
		s.recordInteractionEvidence(target, &evidence)
	}
	for _, item := range own.LiveJourneyEvents {
		var ts time.Time
		if item.TimestampUnixNano > 0 {
			ts = time.Unix(0, item.TimestampUnixNano).UTC()
		}
		s.recordJourneyEvent(target, TransactionEvent{
			Group:               item.Group,
			VUserID:             item.VUserID,
			VUserLifetime:       item.VUserLifetime,
			Iteration:           item.Iteration,
			Phase:               item.Phase,
			ActionName:          item.ActionName,
			Timestamp:           ts,
			DurationMs:          item.DurationMs,
			ElapsedMs:           item.ElapsedMs,
			BusinessTransaction: item.BusinessTransaction,
			Success:             item.Success,
			Interrupted:         item.Interrupted,
			EndsIteration:       item.EndsIteration,
			AuthoredStep:        item.AuthoredStep.Clone(),
		})
	}
}

func (s *liveBrowserIntelligenceState) sourcesCompleteLocked(finalEvaluation bool) (allObserved bool, allCompleted bool) {
	if !s.distributed || len(s.expectedTargets) == 0 {
		return true, finalEvaluation
	}
	for target := range s.expectedTargets {
		if !s.observedTargets[target] {
			return false, false
		}
	}
	if !finalEvaluation {
		return true, false
	}
	for target := range s.expectedTargets {
		if !s.completedTargets[target] {
			return true, false
		}
	}
	return true, true
}

func (s *liveBrowserIntelligenceState) evaluateSnapshot(tr *TestRunner, now time.Time, finalEvaluation bool) *ProvisionalBrowserIntelligenceSnapshot {
	if s == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	var defStore storage.AnalysisDefinitionStore
	if tr != nil && tr.testStore != nil {
		defStore, _ = tr.testStore.(storage.AnalysisDefinitionStore)
	}

	s.mu.Lock()
	s.dirty = false
	s.firstEvaluated = true
	liveTruncated := s.liveTruncated
	truncatedReasons := slices.Clone(s.truncatedReasons)
	allObserved, allCompleted := s.sourcesCompleteLocked(finalEvaluation)

	findings := s.evaluateFindingsLocked(tr, defStore, now, allObserved)
	interactions := s.evaluateInteractionsLocked(allObserved)
	journeys := s.evaluateJourneysLocked(defStore, "", "", allCompleted)
	s.mu.Unlock()

	return &ProvisionalBrowserIntelligenceSnapshot{
		UpdatedAt:        now,
		LiveTruncated:    liveTruncated,
		TruncatedReasons: truncatedReasons,
		Findings:         findings,
		Interactions:     interactions,
		Journeys:         journeys,
	}
}

func (s *liveBrowserIntelligenceState) evaluateFindingsLocked(tr *TestRunner, store storage.AnalysisDefinitionStore, now time.Time, allSourcesObserved bool) *RunBrowserFindingsResponse {
	resp := &RunBrowserFindingsResponse{
		RunID:         s.testID,
		Status:        "not_evaluated",
		Provisional:   true,
		LiveTruncated: s.liveTruncated,
	}
	var pin *RunPolicyPin
	for i := range s.policyPins {
		if s.policyPins[i].Kind == storage.AnalysisDefinitionKindBrowserPolicy {
			pin = &s.policyPins[i]
			break
		}
	}
	if pin == nil || store == nil {
		return resp
	}
	definition, err := store.GetAnalysisDefinition(s.projectID, pin.ID, pin.Revision)
	if err != nil || definition == nil || definition.Kind != storage.AnalysisDefinitionKindBrowserPolicy {
		resp.Status = "failed"
		resp.Failure = "pinned browser_policy revision is unavailable"
		return resp
	}
	digest := sha256.Sum256(definition.Definition)
	sum := hex.EncodeToString(digest[:])
	if pin.SHA256 != "" && pin.SHA256 != sum {
		resp.Status = "failed"
		resp.Failure = "the pinned browser policy revision no longer matches its pin"
		return resp
	}
	var policy BrowserPolicyDefinition
	if _, err := decodeAnalysisPolicy(definition.Kind, definition.Definition); err != nil || json.Unmarshal(definition.Definition, &policy) != nil {
		resp.Status = "failed"
		resp.Failure = "pinned browser_policy revision is invalid"
		return resp
	}
	ref := BrowserPolicyRef{
		ID:       definition.ID,
		Name:     definition.Name,
		Revision: definition.Revision,
		SHA256:   sum,
		Source:   "pinned",
	}
	var policyPins []RunPolicyPin
	for _, candidate := range s.policyPins {
		if candidate.Kind == storage.AnalysisDefinitionKindBrowserPolicy || candidate.Kind == storage.AnalysisDefinitionKindJourneyPolicy {
			policyPins = append(policyPins, candidate)
		}
	}
	baselineRunID := ""
	baselineArchiveSHA := ""
	if policy.Baseline.Mode == "pinned_run" && strings.TrimSpace(policy.Baseline.RunID) != "" {
		baselineRunID = strings.TrimSpace(policy.Baseline.RunID)
		if tr != nil && tr.archiveStore != nil {
			if rec, recErr := tr.archiveStore.GetRunArchive(baselineRunID); recErr == nil && rec != nil {
				baselineArchiveSHA = strings.TrimSpace(rec.ManifestSHA256)
			}
		}
	}
	inputs := &browserIntelligenceInputs{
		projectID:              s.projectID,
		runID:                  s.testID,
		policy:                 policy,
		ref:                    ref,
		policyPins:             policyPins,
		baselineRunID:          baselineRunID,
		baselineArchiveSHA256:  baselineArchiveSHA,
		baselineMismatchReason: "not_evaluated_live",
		provisional:            true,
		liveTruncated:          s.liveTruncated,
	}
	inputs.fingerprint = computeBrowserIntelligenceFingerprint(
		s.projectID, s.testID, "provisional", ref, policyPins,
		"", 0, baselineRunID, baselineArchiveSHA, "", "not_evaluated_live",
	)
	var observations []protocol.BrowserDocumentObservation
	if len(s.documentAccumulator.measurements) > 0 {
		keys := make([]string, 0, len(s.documentAccumulator.measurements))
		for key := range s.documentAccumulator.measurements {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		observations = make([]protocol.BrowserDocumentObservation, 0, len(keys))
		for _, key := range keys {
			observations = append(observations, s.documentAccumulator.measurements[key].observation)
		}
	}
	incomplete := s.documentAccumulator.incomplete || s.liveTruncated || !allSourcesObserved
	dropped := s.documentAccumulator.dropped + s.droppedDocuments
	window := BrowserFindingWindow{Start: s.startedAt, End: now}
	if window.End.Before(window.Start) {
		window.End = window.Start
	}
	result := evaluateBrowserFindings(inputs, window, observations, s.documentCaptureRequested || s.documentRecords > 0, incomplete, dropped)
	result.Provisional = true
	result.LiveTruncated = s.liveTruncated
	completedAt := now
	resp.Status = "ready"
	resp.CompletedAt = &completedAt
	resp.Result = result
	return resp
}

func (s *liveBrowserIntelligenceState) evaluateInteractionsLocked(allSourcesObserved bool) *BrowserInteractionAnalysis {
	if s.interactionRecords == 0 {
		analysis := newInteractionAnalyzer(s.testID, nil).finish()
		analysis.Status = "not_captured"
		if s.droppedInteractions > 0 || s.liveTruncated {
			analysis.Status = "unavailable"
			analysis.Coverage.Dropped = s.droppedInteractions
			analysis.Coverage.Incomplete = true
		}
		analysis.Provisional = true
		analysis.LiveTruncated = s.liveTruncated
		return &analysis
	}
	cohortsCopy := maps.Clone(s.generationCohorts.byGeneration)
	joinsCopy := maps.Clone(s.generationCohorts.joins)
	unknownReason, documentLimitation := unknownCohortNoDocuments, ""
	if s.generationCohorts.truncated {
		unknownReason = fmt.Sprintf("the run kept no consistent engine/profile document evidence for these gestures, or their browser context is past the first %d whose document evidence the analysis keeps", maxInteractionGenerationCohorts)
		documentLimitation = fmt.Sprintf("document_evidence_truncated: engines and device profiles are kept for the first %d browser contexts", maxInteractionGenerationCohorts)
	}
	analyzer := newInteractionAnalyzer(s.testID, cohortsCopy)
	analyzer.unknownReason = unknownReason
	analyzer.documentJoins = joinsCopy
	if s.generationCohorts.joinsTruncated {
		analyzer.analysis.Limitations = append(analyzer.analysis.Limitations, fmt.Sprintf("document_joins_truncated: retained at most %d source/document/lifecycle identities for qualified v3 patterns", maxInteractionDocumentJoins))
	}
	if documentLimitation != "" {
		analyzer.analysis.Limitations = append(analyzer.analysis.Limitations, documentLimitation)
	}
	sources := slices.Sorted(maps.Keys(s.interactionsBySource))
	for _, source := range sources {
		for _, evidence := range s.interactionsBySource[source] {
			analyzer.add(evidence)
		}
	}
	analysis := analyzer.finish()
	analysis.Coverage.Dropped = max(analysis.Coverage.Dropped, s.droppedInteractions)
	analysis.Coverage.Incomplete = analysis.Coverage.Incomplete || s.liveTruncated || !allSourcesObserved
	analysis.Provisional = true
	analysis.LiveTruncated = s.liveTruncated
	return &analysis
}

func (s *liveBrowserIntelligenceState) evaluateJourneysLocked(store storage.AnalysisDefinitionStore, requestedID, requestedRevision string, allSourcesCompleted bool) *JourneyRiskReport {
	syntheticManifest := &RunArchiveManifest{
		TestID: s.testID,
		Provenance: RunArchiveProvenance{
			ProjectID:  s.projectID,
			PolicyPins: cloneRunPolicyPins(s.policyPins),
		},
	}
	if store == nil {
		return &JourneyRiskReport{
			SchemaVersion: journeyRiskSchemaVersion,
			RunID:         s.testID,
			Availability:  "unavailable",
			Reason:        "no_journey_policy",
			Provisional:   true,
			LiveTruncated: s.liveTruncated,
			Journeys:      []JourneyAccounting{},
		}
	}
	policy, ref, found, _, err := resolveJourneyPolicy(store, s.projectID, syntheticManifest, requestedID, requestedRevision)
	if err != nil || !found {
		return &JourneyRiskReport{
			SchemaVersion: journeyRiskSchemaVersion,
			RunID:         s.testID,
			Availability:  "unavailable",
			Reason:        "no_journey_policy",
			Provisional:   true,
			LiveTruncated: s.liveTruncated,
			Journeys:      []JourneyAccounting{},
			Notes:         []string{"The run pinned no journey_policy; name one with ?policy=ID."},
		}
	}
	report := &JourneyRiskReport{
		SchemaVersion:   journeyRiskSchemaVersion,
		RunID:           s.testID,
		Policy:          ref,
		Availability:    "available",
		Provisional:     true,
		LiveTruncated:   s.liveTruncated,
		StepRiskEnabled: policy.StepRiskEnabled,
		Journeys:        []JourneyAccounting{},
		Notes: []string{
			"Weighted test-journey risk from the run's own transactions; never revenue or conversion impact.",
			"An attempt still open when its VUser's last iteration ended is censored and excluded from the index, unless the engine recorded that iteration ending on its own.",
		},
	}
	acc := newJourneyAccumulator(policy, report)
	sources := slices.Sorted(maps.Keys(s.journeyEventsBySource))
	for _, source := range sources {
		events := s.journeyEventsBySource[source]
		for i := range events {
			event := events[i]
			acc.add(&event)
		}
	}
	if s.liveTruncated || s.droppedJourneyEvents > 0 {
		acc.incomplete("live_truncated")
	}
	if s.distributed && !allSourcesCompleted {
		acc.incomplete("sources_not_collected")
	}
	if !acc.identity {
		report.Availability, report.Journeys, report.Reason = "unavailable", []JourneyAccounting{}, acc.unavailableReason()
		return report
	}
	acc.finish()
	return report
}

func (tr *TestRunner) startLiveBrowserIntelligence(test *ManagedTest, expectedTargets []string) {
	if tr == nil || test == nil {
		return
	}
	tr.mu.Lock()
	if tr.tests[test.ID] != test {
		tr.mu.Unlock()
		return
	}
	if existing := test.liveBrowserIntelligence; existing != nil {
		tr.mu.Unlock()
		existing.stopAndJoin()
		tr.mu.Lock()
		if tr.tests[test.ID] != test {
			tr.mu.Unlock()
			return
		}
	}
	state := newLiveBrowserIntelligenceState(test, expectedTargets)
	test.liveBrowserIntelligence = state
	tr.mu.Unlock()

	tr.publishLiveBrowserIntelligenceSnapshot(test, state, time.Now().UTC(), false)

	interval := liveBrowserIntelligenceTickInterval
	if interval <= 0 {
		interval = defaultLiveBrowserIntelligenceInterval
	}
	go func() {
		defer close(state.doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-state.stopCh:
				return
			case <-state.notifyCh:
				tr.publishLiveBrowserIntelligenceSnapshot(test, state, time.Now().UTC(), false)
			case now := <-ticker.C:
				state.mu.Lock()
				dirty := state.dirty
				state.mu.Unlock()
				if dirty {
					tr.publishLiveBrowserIntelligenceSnapshot(test, state, now.UTC(), false)
				}
			}
		}
	}()
}

func (s *liveBrowserIntelligenceState) stopAndJoin() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()
		close(s.stopCh)
	})
	<-s.doneCh
}

func (tr *TestRunner) publishLiveBrowserIntelligenceSnapshot(test *ManagedTest, state *liveBrowserIntelligenceState, now time.Time, finalEvaluation bool) {
	if tr == nil || test == nil || state == nil {
		return
	}
	snapshot := state.evaluateSnapshot(tr, now, finalEvaluation)
	if snapshot == nil {
		return
	}
	tr.mu.Lock()
	if tr.tests[test.ID] != test || test.liveBrowserIntelligence != state {
		tr.mu.Unlock()
		return
	}
	test.provisionalBrowserIntelligence = snapshot
	runSnapshot := buildRunLiveStateSnapshotLocked(test, now)
	testID := test.ID
	projectID := normalizeProjectID(test.ProjectID)
	testName := test.Name
	tr.mu.Unlock()

	tr.cacheRunSnapshot(runSnapshot)
	if tr.hub != nil {
		tr.hub.BroadcastScopedJSON(testID, projectID, map[string]any{
			"type":       "browser_intelligence_provisional",
			"test_id":    testID,
			"project_id": projectID,
			"test_name":  testName,
			"timestamp":  snapshot.UpdatedAt,
			"data":       snapshot,
		})
	}
}

// stopAndJoinLiveBrowserIntelligence cancels and waits for the 30 s ticker
// goroutine before archive finalization begins, performing one synchronous
// final provisional evaluation over all collected run evidence.
func (tr *TestRunner) stopAndJoinLiveBrowserIntelligence(test *ManagedTest, completedAt time.Time) {
	if tr == nil || test == nil {
		return
	}
	tr.mu.RLock()
	state := test.liveBrowserIntelligence
	tr.mu.RUnlock()
	if state == nil {
		return
	}
	state.stopAndJoin()
	tr.publishLiveBrowserIntelligenceSnapshot(test, state, completedAt, true)
}

func (tr *TestRunner) stopAndJoinLiveBrowserIntelligenceByID(testID string) {
	if tr == nil || strings.TrimSpace(testID) == "" {
		return
	}
	tr.mu.RLock()
	test := tr.tests[testID]
	tr.mu.RUnlock()
	if test == nil {
		return
	}
	tr.stopAndJoinLiveBrowserIntelligence(test, time.Now().UTC())
}

func releaseLiveBrowserIntelligenceLocked(test *ManagedTest) {
	if test == nil {
		return
	}
	test.liveBrowserIntelligence = nil
	test.provisionalBrowserIntelligence = nil
}

type liveBrowserIntelligenceSink struct {
	runner *TestRunner
	test   *ManagedTest
}

func (sink liveBrowserIntelligenceSink) state() *liveBrowserIntelligenceState {
	if sink.runner == nil || sink.test == nil {
		return nil
	}
	sink.runner.mu.RLock()
	defer sink.runner.mu.RUnlock()
	current := sink.runner.tests[sink.test.ID]
	if current != sink.test {
		return nil
	}
	return current.liveBrowserIntelligence
}

func (sink liveBrowserIntelligenceSink) RecordResult(result *protocol.Result) error {
	if result == nil || result.SampleKind == "browser_document_evidence" {
		return nil
	}
	state := sink.state()
	if state == nil {
		return nil
	}
	state.recordJourneyEvent("", TransactionEvent{
		Group:               result.Group,
		VUserID:             result.VUserID,
		VUserLifetime:       result.VUserLifetime,
		Iteration:           result.Iteration,
		Phase:               result.Phase,
		ActionName:          result.ActionName,
		Timestamp:           result.StartTime,
		DurationMs:          durationMillis(result.Duration),
		ElapsedMs:           float64(result.WallElapsed) / float64(time.Millisecond),
		BusinessTransaction: result.BusinessTransaction,
		Success:             result.Success,
		Interrupted:         result.Interrupted,
		EndsIteration:       result.EndsIteration,
		AuthoredStep:        result.AuthoredStep.Clone(),
	})
	return nil
}

func (sink liveBrowserIntelligenceSink) RecordTransaction(result *protocol.Result) error {
	return sink.RecordResult(result)
}

func (sink liveBrowserIntelligenceSink) RecordLiveBrowserDocumentEvidence(evidence *protocol.BrowserDocumentEvidence) error {
	state := sink.state()
	if state == nil {
		return nil
	}
	state.recordDocumentEvidence(evidence)
	return nil
}

func (sink liveBrowserIntelligenceSink) RecordLiveBrowserInteractionEvidence(evidence *protocol.BrowserInteractionEvidence) error {
	state := sink.state()
	if state == nil {
		return nil
	}
	state.recordInteractionEvidence("", evidence)
	return nil
}

func (s *Server) liveBrowserIntelligenceForTest(ctx context.Context, testID string) (*liveBrowserIntelligenceState, *ProvisionalBrowserIntelligenceSnapshot) {
	if s == nil || strings.TrimSpace(testID) == "" {
		return nil, nil
	}
	if s.runner != nil {
		s.runner.mu.RLock()
		current := s.runner.tests[testID]
		var state *liveBrowserIntelligenceState
		var snap *ProvisionalBrowserIntelligenceSnapshot
		if current != nil {
			state = current.liveBrowserIntelligence
			snap = cloneProvisionalBrowserIntelligenceSnapshot(current.provisionalBrowserIntelligence)
		}
		store := s.runner.liveState
		s.runner.mu.RUnlock()
		if state != nil {
			state.mu.Lock()
			dirty := state.dirty
			state.mu.Unlock()
			if snap == nil || dirty {
				s.runner.publishLiveBrowserIntelligenceSnapshot(current, state, time.Now().UTC(), isTerminalTestStatus(current.Status))
				s.runner.mu.RLock()
				if s.runner.tests[testID] == current {
					snap = cloneProvisionalBrowserIntelligenceSnapshot(current.provisionalBrowserIntelligence)
				}
				s.runner.mu.RUnlock()
			}
		}
		if snap != nil {
			return state, snap
		}
		if store != nil {
			if liveSnap, err := store.GetRunSnapshot(ctx, testID); err == nil && liveSnap != nil && liveSnap.ProvisionalBrowserIntelligence != nil {
				return state, cloneProvisionalBrowserIntelligenceSnapshot(liveSnap.ProvisionalBrowserIntelligence)
			}
		}
	}
	if s.cache != nil {
		if liveSnap, err := s.cache.GetRunSnapshot(ctx, testID); err == nil && liveSnap != nil && liveSnap.ProvisionalBrowserIntelligence != nil {
			return nil, cloneProvisionalBrowserIntelligenceSnapshot(liveSnap.ProvisionalBrowserIntelligence)
		}
	}
	return nil, nil
}

func (s *Server) provisionalRunBrowserFindings(ctx context.Context, test *ManagedTest) (*RunBrowserFindingsResponse, bool) {
	if s == nil || test == nil {
		return nil, false
	}
	_, snap := s.liveBrowserIntelligenceForTest(ctx, test.ID)
	if snap == nil || snap.Findings == nil {
		return nil, false
	}
	out := *snap.Findings
	out.Provisional = true
	out.LiveTruncated = snap.LiveTruncated
	if out.Result != nil {
		resCopy := *out.Result
		resCopy.Provisional = true
		resCopy.LiveTruncated = snap.LiveTruncated
		out.Result = &resCopy
	}
	return &out, true
}

func (s *Server) provisionalRunBrowserInteractions(ctx context.Context, testID string) (*BrowserInteractionAnalysis, bool) {
	if s == nil || strings.TrimSpace(testID) == "" {
		return nil, false
	}
	_, snap := s.liveBrowserIntelligenceForTest(ctx, testID)
	if snap == nil || snap.Interactions == nil {
		return nil, false
	}
	out := *snap.Interactions
	out.Provisional = true
	out.LiveTruncated = snap.LiveTruncated
	return &out, true
}

func (s *Server) provisionalRunJourneys(ctx context.Context, test *ManagedTest, requestedID, requestedRevision string) (*JourneyRiskReport, int, error, bool) {
	if s == nil || test == nil {
		return nil, 0, nil, false
	}
	state, snap := s.liveBrowserIntelligenceForTest(ctx, test.ID)
	if state == nil && snap == nil {
		return nil, 0, nil, false
	}
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		return nil, http.StatusServiceUnavailable, errors.New("analysis policies unavailable"), true
	}
	if strings.TrimSpace(requestedID) != "" || strings.TrimSpace(requestedRevision) != "" {
		var pins []RunPolicyPin
		if state != nil {
			state.mu.Lock()
			pins = cloneRunPolicyPins(state.policyPins)
			state.mu.Unlock()
		}
		syntheticManifest := &RunArchiveManifest{
			TestID: test.ID,
			Provenance: RunArchiveProvenance{
				ProjectID:  normalizeProjectID(test.ProjectID),
				PolicyPins: pins,
			},
		}
		_, ref, _, status, err := resolveJourneyPolicy(store, normalizeProjectID(test.ProjectID), syntheticManifest, requestedID, requestedRevision)
		if err != nil {
			return nil, status, err, true
		}
		if state != nil {
			state.mu.Lock()
			_, allCompleted := state.sourcesCompleteLocked(isTerminalTestStatus(test.Status))
			report := state.evaluateJourneysLocked(store, requestedID, requestedRevision, allCompleted)
			state.mu.Unlock()
			return report, http.StatusOK, nil, true
		}
		if snap != nil && snap.Journeys != nil &&
			snap.Journeys.Policy.ID == ref.ID && snap.Journeys.Policy.Revision == ref.Revision {
			out := *snap.Journeys
			out.Provisional = true
			out.LiveTruncated = snap.LiveTruncated
			return &out, http.StatusOK, nil, true
		}
		return nil, 0, nil, false
	}
	if snap != nil && snap.Journeys != nil {
		out := *snap.Journeys
		out.Provisional = true
		out.LiveTruncated = snap.LiveTruncated
		return &out, http.StatusOK, nil, true
	}
	if state != nil {
		state.mu.Lock()
		_, allCompleted := state.sourcesCompleteLocked(isTerminalTestStatus(test.Status))
		report := state.evaluateJourneysLocked(store, requestedID, requestedRevision, allCompleted)
		state.mu.Unlock()
		return report, http.StatusOK, nil, true
	}
	return nil, 0, nil, false
}

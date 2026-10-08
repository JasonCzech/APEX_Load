package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Journey accounting (E5). A journey_policy names journeys by an explicit
// start and success boundary (an authored transaction or action) and a
// weight. After a run, its archived transactions are read per VUser
// iteration: each attempt of a journey is completed (its success boundary
// succeeded), failed (the boundary failed, or the iteration ended after a
// failed step), aborted (the iteration ended without the outcome and without
// a failed step), or censored (still open when the run ended, so its outcome
// is unknown); an iteration of a group that runs the journey but never
// started it skipped it. Step failures are counted separately: one failed
// journey counts once however many of its steps failed. The weighted failure
// index is sum(w * failed) / sum(w * eligible), eligible being completed,
// failed and aborted attempts; a zero denominator is unscored. It is weighted
// test-journey risk, never revenue or conversion impact. A failure the engine
// marked interrupted (recorded once the VUser was stopping, or refused by the
// generator's own resource limit) is not the target's, so it censors the
// attempts it cut rather than failing them, and an iteration that may have
// been cut short never counts a journey as skipped.

const (
	journeyRiskSchemaVersion = "apexload.journey_risk.v1"
	// maxJourneyTrackedVUsers bounds the per-VUser state held while reading
	// the archive; past it, further VUsers are not accounted and the result
	// is incomplete.
	maxJourneyTrackedVUsers = 200_000
	// maxJourneyIterationEvents bounds one open iteration's buffered rows and
	// maxJourneyBufferedEvents all open iterations' together; past either,
	// rows are dropped and the accounting is incomplete (and unscored).
	maxJourneyIterationEvents = 10_000
	maxJourneyBufferedEvents  = 1_000_000
	maxJourneyExamples        = 5
)

// JourneyAttemptRef identifies one attempt, for drill-down.
type JourneyAttemptRef struct {
	Group      string    `json:"group"`
	VUserID    int       `json:"vuser_id"`
	Iteration  int64     `json:"iteration"`
	StartedAt  time.Time `json:"started_at"`
	Outcome    string    `json:"outcome"`
	FailedStep string    `json:"failed_step,omitempty"`
}

// JourneyAccounting is one journey's attempts.
type JourneyAccounting struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Weight    float64         `json:"weight"`
	Start     JourneyBoundary `json:"start"`
	Success   JourneyBoundary `json:"success"`
	Completed int64           `json:"completed"`
	Failed    int64           `json:"failed"`
	Aborted   int64           `json:"aborted"`
	Skipped   int64           `json:"skipped"`
	Censored  int64           `json:"censored"`
	Eligible  int64           `json:"eligible"`
	// Steps and StepFailures count the action samples inside eligible
	// attempts and the failed ones.
	Steps        int64               `json:"steps"`
	StepFailures int64               `json:"step_failures"`
	FailureRate  *float64            `json:"failure_rate,omitempty"`
	Examples     []JourneyAttemptRef `json:"examples,omitempty"`
}

// JourneyPolicyRef names the evaluated journey_policy revision.
type JourneyPolicyRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	SHA256   string `json:"sha256"`
	// Source is "pinned" (the run's own policy pin) or "requested".
	Source string `json:"source"`
}

// JourneyRiskReport is a run's journey accounting under one policy.
type JourneyRiskReport struct {
	SchemaVersion string           `json:"schema_version"`
	RunID         string           `json:"run_id"`
	Policy        JourneyPolicyRef `json:"policy"`
	// Availability is "available", or "unavailable" with Reason (an archive
	// without VUser iteration identity, for example).
	Availability         string              `json:"availability"`
	Reason               string              `json:"reason,omitempty"`
	Journeys             []JourneyAccounting `json:"journeys"`
	WeightedFailureIndex *float64            `json:"weighted_failure_index,omitempty"`
	// IndexUnscored says why the index is absent.
	IndexUnscored    string   `json:"index_unscored,omitempty"`
	StepRiskEnabled  bool     `json:"step_risk_enabled"`
	StepRiskIndex    *float64 `json:"step_risk_index,omitempty"`
	StepRiskUnscored string   `json:"step_risk_unscored,omitempty"`
	// Incomplete: the accounting does not cover the whole run;
	// IncompleteReasons says why (tracking bounds, the archive scan budget,
	// uncollected generator sources, rows without VUser identity).
	Incomplete        bool     `json:"incomplete,omitempty"`
	IncompleteReasons []string `json:"incomplete_reasons,omitempty"`
	// RowsOutsideIterations counts init, teardown and identity-less rows,
	// which belong to no journey attempt.
	RowsOutsideIterations int64    `json:"rows_outside_iterations"`
	Notes                 []string `json:"notes"`
}

func (b JourneyBoundary) matches(event *TransactionEvent) bool {
	if b.Transaction != "" {
		return event.BusinessTransaction && event.ActionName == b.Transaction
	}
	return !event.BusinessTransaction && b.Action != "" && event.ActionName == b.Action
}

// journeyEvent is the part of an archived row journey accounting keeps while
// its iteration is open.
type journeyEvent struct {
	ordinal int64
	begin   int64 // start, Unix ns
	end     int64 // begin + duration
	step    bool  // an action sample (not a transaction row)
	success bool
	// interrupted: a failure recorded once the VUser was stopping, or one the
	// generator's own resource limit caused.
	interrupted bool
	name        string
	starts      uint64 // journeys this row is the start boundary of
	finishes    uint64 // journeys this row is the success boundary of
}

// A row contributes a begin point; a success boundary also a finish point at
// its end, and an interrupted row an interrupt point at its end, when the
// stop cut whatever was open.
const (
	journeyPointBegin = iota
	journeyPointFinish
	journeyPointInterrupt
)

type journeyPoint struct {
	at    int64
	kind  int
	event *journeyEvent
}

type journeyAttempt struct {
	startedAt    time.Time
	steps        int64
	stepFailures int64
	failedStep   string
	// interrupted: a stop or a generator limit cut the attempt, so its
	// outcome is unknown.
	interrupted bool
}

type journeyVUserState struct {
	group     string
	vuser     int
	lifetime  uint64
	iteration int64
	events    []journeyEvent
	// ended: the engine recorded that the current iteration ended on its
	// own (an ends_iteration row that is not interrupted), so it was not cut
	// by a stop.
	ended bool
}

type journeyAccumulator struct {
	policy  JourneyPolicyDefinition
	results []JourneyAccounting
	vusers  map[journeyVUserKey]*journeyVUserState
	// started marks, per journey, the groups that ever started it; skipped
	// counts iterations without a start per group, kept only for those.
	started  []map[string]bool
	skipped  []map[string]int64
	buffered int
	ordinal  int64
	report   *JourneyRiskReport
	identity bool
	// lifetimes says any row carries a VUser lifetime; rowsWithoutLifetime
	// counts the VUsers' action rows that do not (an older generator's).
	// Lifecycle rows of a group's once-only init/teardown VUser carry none.
	lifetimes           bool
	rowsWithoutLifetime int64
}

type journeyVUserKey struct {
	group string
	vuser int
}

func newJourneyAccumulator(policy JourneyPolicyDefinition, report *JourneyRiskReport) *journeyAccumulator {
	acc := &journeyAccumulator{policy: policy, vusers: map[journeyVUserKey]*journeyVUserState{}, report: report}
	for _, journey := range policy.Journeys {
		acc.results = append(acc.results, JourneyAccounting{ID: journey.ID, Name: journey.Name, Weight: journey.Weight, Start: journey.Start, Success: journey.Success})
		acc.started = append(acc.started, map[string]bool{})
		acc.skipped = append(acc.skipped, map[string]int64{})
	}
	return acc
}

func (a *journeyAccumulator) example(j int, state *journeyVUserState, attempt *journeyAttempt, outcome string) {
	if len(a.results[j].Examples) >= maxJourneyExamples {
		return
	}
	a.results[j].Examples = append(a.results[j].Examples, JourneyAttemptRef{Group: state.group, VUserID: state.vuser, Iteration: state.iteration, StartedAt: attempt.startedAt, Outcome: outcome, FailedStep: attempt.failedStep})
}

// censor records an attempt whose outcome the run's end or a stop hid.
func (a *journeyAccumulator) censor(j int, state *journeyVUserState, attempt *journeyAttempt) {
	a.results[j].Censored++
	a.example(j, state, attempt, "censored")
}

// settle records an attempt that ended without reaching its success boundary:
// censored when a stop or a generator limit cut it, failed when one of its
// steps failed, aborted otherwise.
func (a *journeyAccumulator) settle(j int, state *journeyVUserState, attempt *journeyAttempt) {
	if attempt.interrupted {
		a.censor(j, state, attempt)
		return
	}
	result := &a.results[j]
	result.Steps += attempt.steps
	result.StepFailures += attempt.stepFailures
	if attempt.stepFailures > 0 {
		result.Failed++
		a.example(j, state, attempt, "failed")
		return
	}
	result.Aborted++
}

// closeIteration evaluates a VUser's buffered iteration in time order: rows
// are archived as they are recorded, so a transaction's row follows its own
// steps. A start boundary opens an attempt at its start; steps that begin
// while an attempt is open belong to it; a success boundary decides the
// attempt at its end. An interrupted failure (recorded once the VUser was
// stopping, or caused by the generator's own resource limit) is not the
// target's: it cuts every attempt open when it ended, and those are censored
// unless they still reach their outcome; a success boundary that then fails is
// censored too. censored says the iteration
// may have been cut short (a VUser lifetime's last iteration that the engine
// did not record ending on its own, or an evacuated share's last iteration),
// so an attempt still open is censored rather than aborted or failed.
func (a *journeyAccumulator) closeIteration(state *journeyVUserState, censored bool) {
	points := make([]journeyPoint, 0, len(state.events)+4)
	for i := range state.events {
		event := &state.events[i]
		points = append(points, journeyPoint{at: event.begin, kind: journeyPointBegin, event: event})
		if event.finishes != 0 {
			points = append(points, journeyPoint{at: event.end, kind: journeyPointFinish, event: event})
		}
		if event.interrupted {
			censored = true
			points = append(points, journeyPoint{at: event.end, kind: journeyPointInterrupt, event: event})
		}
	}
	// At one instant, a row that ended there (finish, then interrupt) comes
	// before a row that begins there, and a zero-length row's own end follows
	// its begin.
	tie := func(point journeyPoint) int {
		switch {
		case point.kind == journeyPointBegin:
			return 1
		case point.event.begin < point.at:
			return 0
		}
		return 2
	}
	sort.SliceStable(points, func(i, j int) bool {
		left, right := points[i], points[j]
		if left.at != right.at {
			return left.at < right.at
		}
		if leftTie, rightTie := tie(left), tie(right); leftTie != rightTie {
			return leftTie < rightTie
		}
		if left.kind != right.kind {
			return left.kind < right.kind
		}
		if left.event.step != right.event.step {
			// A transaction opens before the steps that begin with it.
			return !left.event.step
		}
		return left.event.ordinal < right.event.ordinal
	})
	open := make([]*journeyAttempt, len(a.results))
	var attempted uint64
	for _, point := range points {
		event := point.event
		for j := range a.results {
			bit := uint64(1) << uint(j)
			if point.kind == journeyPointInterrupt {
				if open[j] != nil {
					open[j].interrupted = true
				}
				continue
			}
			if point.kind == journeyPointFinish {
				if event.finishes&bit == 0 || open[j] == nil {
					continue
				}
				attempt := open[j]
				open[j] = nil
				if !event.success && (event.interrupted || attempt.interrupted) {
					// The stop or the generator's resource limit, not the
					// target, failed the boundary or cut the attempt first.
					a.censor(j, state, attempt)
					continue
				}
				result := &a.results[j]
				result.Steps += attempt.steps
				result.StepFailures += attempt.stepFailures
				if event.success {
					result.Completed++
				} else {
					if attempt.failedStep == "" {
						attempt.failedStep = event.name
					}
					result.Failed++
					a.example(j, state, attempt, "failed")
				}
				continue
			}
			if event.starts&bit != 0 {
				if open[j] != nil {
					// The journey restarted without its outcome.
					a.settle(j, state, open[j])
				}
				open[j] = &journeyAttempt{startedAt: time.Unix(0, event.begin).UTC()}
				attempted |= bit
				a.started[j][state.group] = true
			}
			if attempt := open[j]; attempt != nil && event.step && !event.interrupted {
				attempt.steps++
				if !event.success {
					attempt.stepFailures++
					if attempt.failedStep == "" {
						attempt.failedStep = event.name
					}
				}
			}
		}
	}
	for j, attempt := range open {
		if attempt == nil {
			continue
		}
		if censored {
			a.censor(j, state, attempt)
			continue
		}
		a.settle(j, state, attempt)
	}
	// An iteration that may have been cut short cannot show that a journey
	// was skipped: it might have started later.
	for j := range a.results {
		if !censored && attempted&(uint64(1)<<uint(j)) == 0 {
			a.skipped[j][state.group]++
		}
	}
	a.buffered -= len(state.events)
	state.events = state.events[:0]
}

// incomplete marks the accounting incomplete for a reason (once).
func (a *journeyAccumulator) incomplete(reason string) {
	a.report.Incomplete = true
	if !slices.Contains(a.report.IncompleteReasons, reason) {
		a.report.IncompleteReasons = append(a.report.IncompleteReasons, reason)
	}
}

func (a *journeyAccumulator) add(event *TransactionEvent) {
	a.ordinal++
	begin := event.Timestamp.UnixNano()
	end := begin + int64(max(event.DurationMs, event.ElapsedMs)*float64(time.Millisecond))
	switch {
	case event.VUserLifetime != 0:
		a.lifetimes = true
	case event.VUserID >= 0 && (event.Phase == "" || event.Phase == "action"):
		a.rowsWithoutLifetime++
	}
	if event.Iteration <= 0 {
		a.report.RowsOutsideIterations++
		return
	}
	a.identity = true
	key := journeyVUserKey{group: event.Group, vuser: event.VUserID}
	state := a.vusers[key]
	if state == nil {
		if len(a.vusers) >= maxJourneyTrackedVUsers {
			a.incomplete("vuser_tracking_bound")
			return
		}
		state = &journeyVUserState{group: event.Group, vuser: event.VUserID, lifetime: event.VUserLifetime, iteration: event.Iteration}
		a.vusers[key] = state
	}
	switch {
	case event.VUserLifetime != state.lifetime:
		// Another lifetime under the same VUser ID (an open-workload arrival,
		// a scale-up, an evacuation's replacement, in any order): the earlier
		// lifetime's last iteration is settled only if it ended on its own.
		a.closeIteration(state, !state.ended)
		state.lifetime, state.iteration, state.ended = event.VUserLifetime, event.Iteration, false
	case event.Iteration != state.iteration:
		// Rows without a lifetime go back an iteration when a replacement
		// generator continued the VUser after an evacuation: the earlier
		// share's last iteration may have been cut. (Within one lifetime the
		// engine never records an earlier iteration.)
		a.closeIteration(state, event.Iteration < state.iteration)
		state.iteration, state.ended = event.Iteration, false
	}
	if event.EndsIteration && !event.Interrupted {
		state.ended = true
	}
	var starts, finishes uint64
	for j, journey := range a.policy.Journeys {
		if journey.Start.matches(event) {
			starts |= uint64(1) << uint(j)
		}
		if journey.Success.matches(event) {
			finishes |= uint64(1) << uint(j)
		}
	}
	step := !event.BusinessTransaction
	if !step && starts == 0 && finishes == 0 && !(event.Interrupted && !event.Success) {
		return
	}
	if len(state.events) >= maxJourneyIterationEvents || a.buffered >= maxJourneyBufferedEvents {
		a.incomplete("buffer_bound")
		return
	}
	a.buffered++
	// A transaction's interval is its wall-clock span (its duration can
	// exclude think time inside it).
	state.events = append(state.events, journeyEvent{ordinal: a.ordinal, begin: begin, end: end, step: step, success: event.Success, interrupted: event.Interrupted && !event.Success, name: event.ActionName, starts: starts, finishes: finishes})
}

// finish settles the run's end: each VUser lifetime's last iteration is
// settled when the engine recorded it ending on its own, and otherwise
// censored when a journey is still open in it (a stop may have cut it). Rows
// without a lifetime have no such record, so their last iteration is
// censored.
func (a *journeyAccumulator) finish() {
	keys := make([]journeyVUserKey, 0, len(a.vusers))
	for key := range a.vusers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].group != keys[j].group {
			return keys[i].group < keys[j].group
		}
		return keys[i].vuser < keys[j].vuser
	})
	for _, key := range keys {
		state := a.vusers[key]
		a.closeIteration(state, !state.ended)
	}
	if a.lifetimes && a.rowsWithoutLifetime > 0 {
		// Rows from a source without VUser identity (an older generator)
		// cannot be accounted.
		a.incomplete("rows_without_vuser_identity")
	}
	weightedFailed, weightedEligible, weightedStepFailures, weightedSteps := 0.0, 0.0, 0.0, 0.0
	for j := range a.results {
		result := &a.results[j]
		for group, count := range a.skipped[j] {
			if a.started[j][group] {
				result.Skipped += count
			}
		}
		result.Eligible = result.Completed + result.Failed + result.Aborted
		if result.Eligible > 0 {
			rate := float64(result.Failed) / float64(result.Eligible)
			result.FailureRate = &rate
		}
		weightedFailed += result.Weight * float64(result.Failed)
		weightedEligible += result.Weight * float64(result.Eligible)
		weightedStepFailures += result.Weight * float64(result.StepFailures)
		weightedSteps += result.Weight * float64(result.Steps)
	}
	switch {
	case a.report.Incomplete:
		a.report.IndexUnscored = "accounting_incomplete"
	case weightedEligible > 0:
		index := weightedFailed / weightedEligible
		a.report.WeightedFailureIndex = &index
	default:
		a.report.IndexUnscored = "no_weighted_eligible_attempts"
	}
	if a.report.StepRiskEnabled {
		switch {
		case a.report.Incomplete:
			a.report.StepRiskUnscored = "accounting_incomplete"
		case weightedSteps > 0:
			index := weightedStepFailures / weightedSteps
			a.report.StepRiskIndex = &index
		default:
			a.report.StepRiskUnscored = "no_weighted_steps"
		}
	}
	a.report.Journeys = a.results
}

// unavailableReason says why an archive with no row in an iteration cannot
// be accounted: it ran no iteration when identity was recorded or no VUser
// action row lacks it (an empty archive, or only a group's once-only
// init/teardown rows); otherwise it predates iteration identity.
func (a *journeyAccumulator) unavailableReason() string {
	if a.lifetimes || a.rowsWithoutLifetime == 0 {
		return "no_iterations"
	}
	return "archive_lacks_vuser_iteration_identity"
}

// accountJourneys reads a finalized archive's transactions under a journey
// policy. A scan stopped by the archive budget, or an archive whose
// generator sources were not all collected, gives an incomplete report.
func accountJourneys(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, policy JourneyPolicyDefinition, ref JourneyPolicyRef) (*JourneyRiskReport, error) {
	report := &JourneyRiskReport{SchemaVersion: journeyRiskSchemaVersion, RunID: record.TestID, Policy: ref, Availability: "available", StepRiskEnabled: policy.StepRiskEnabled, Journeys: []JourneyAccounting{},
		Notes: []string{
			"Weighted test-journey risk from the run's own transactions; never revenue or conversion impact.",
			"An attempt still open when its VUser's last iteration ended is censored and excluded from the index, unless the engine recorded that iteration ending on its own.",
		}}
	acc := newJourneyAccumulator(policy, report)
	err := forEachArchiveTransactionContext(ctx, record, func(event *TransactionEvent) error {
		acc.add(event)
		return nil
	})
	var budget *archiveScanBudgetError
	switch {
	case errors.As(err, &budget):
		acc.incomplete("archive_scan_budget")
	case err != nil:
		return nil, err
	}
	if manifest != nil && manifest.SourceCoverage != nil {
		for _, source := range manifest.SourceCoverage.Sources {
			if !source.Collected {
				acc.incomplete("sources_not_collected")
				break
			}
		}
	}
	if !acc.identity {
		report.Availability, report.Journeys, report.Reason = "unavailable", []JourneyAccounting{}, acc.unavailableReason()
		return report, nil
	}
	acc.finish()
	return report, nil
}

// resolveJourneyPolicy picks the journey_policy revision a report applies:
// the explicitly requested one, else the run's own pin. found is false when
// the run pinned none and none was requested.
func resolveJourneyPolicy(store storage.AnalysisDefinitionStore, projectID string, manifest *RunArchiveManifest, requestedID, requestedRevision string) (JourneyPolicyDefinition, JourneyPolicyRef, bool, int, error) {
	id, revision, source := strings.TrimSpace(requestedID), int64(0), "requested"
	if raw := requestedRevision; raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 || id == "" {
			return JourneyPolicyDefinition{}, JourneyPolicyRef{}, false, http.StatusBadRequest, errors.New("revision must be a positive integer and needs policy")
		}
		revision = parsed
	}
	var pin *RunPolicyPin
	if id == "" {
		for index := range manifest.Provenance.PolicyPins {
			if manifest.Provenance.PolicyPins[index].Kind == storage.AnalysisDefinitionKindJourneyPolicy {
				pin = &manifest.Provenance.PolicyPins[index]
			}
		}
		if pin == nil {
			return JourneyPolicyDefinition{}, JourneyPolicyRef{}, false, 0, nil
		}
		id, revision, source = pin.ID, pin.Revision, "pinned"
	}
	if len(id) > 128 {
		return JourneyPolicyDefinition{}, JourneyPolicyRef{}, false, http.StatusBadRequest, errors.New("policy id is too long")
	}
	definition, err := store.GetAnalysisDefinition(projectID, id, revision)
	// A pinned or named revision stays readable after deletion; the latest of
	// a deleted policy does not.
	if err != nil || definition == nil || definition.Kind != storage.AnalysisDefinitionKindJourneyPolicy || (revision == 0 && definition.Deleted) {
		return JourneyPolicyDefinition{}, JourneyPolicyRef{}, false, http.StatusNotFound, errors.New("journey policy not found")
	}
	digest := sha256.Sum256(definition.Definition)
	sum := hex.EncodeToString(digest[:])
	if pin != nil && pin.SHA256 != sum {
		return JourneyPolicyDefinition{}, JourneyPolicyRef{}, false, http.StatusConflict, errors.New("the pinned journey policy revision no longer matches its pin")
	}
	if _, err := decodeAnalysisPolicy(definition.Kind, definition.Definition); err != nil {
		return JourneyPolicyDefinition{}, JourneyPolicyRef{}, false, http.StatusConflict, fmt.Errorf("journey policy revision is invalid: %w", err)
	}
	var policy JourneyPolicyDefinition
	if err := json.Unmarshal(definition.Definition, &policy); err != nil {
		return JourneyPolicyDefinition{}, JourneyPolicyRef{}, false, http.StatusConflict, fmt.Errorf("journey policy revision is invalid: %w", err)
	}
	return policy, JourneyPolicyRef{ID: definition.ID, Name: definition.Name, Revision: definition.Revision, SHA256: sum, Source: source}, true, 0, nil
}

// handleGetRunArchiveJourneys accounts a finalized run's journeys under its
// pinned journey_policy, or under ?policy=ID[&revision=N].
//
// OpenAPI-Responses: 400, 404, 409, 500, 503
func (s *Server) handleGetRunArchiveJourneys(w http.ResponseWriter, r *http.Request) {
	test, record, lease, ok := s.requireRunArchiveTest(w, r)
	if !ok {
		return
	}
	defer lease.Release()
	if strings.TrimSpace(record.ManifestSHA256) == "" {
		writeError(w, http.StatusConflict, "the run archive is not finalized")
		return
	}
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "analysis policies unavailable")
		return
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive manifest")
		return
	}
	policy, ref, found, status, err := resolveJourneyPolicy(store, normalizeProjectID(test.ProjectID), manifest, r.URL.Query().Get("policy"), r.URL.Query().Get("revision"))
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, &JourneyRiskReport{SchemaVersion: journeyRiskSchemaVersion, RunID: test.ID, Availability: "unavailable", Reason: "no_journey_policy", Journeys: []JourneyAccounting{},
			Notes: []string{"The run pinned no journey_policy; name one with ?policy=ID."}})
		return
	}
	report, err := accountJourneys(r.Context(), record, manifest, policy, ref)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive transactions: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

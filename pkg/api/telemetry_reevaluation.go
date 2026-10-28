package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Telemetry re-evaluation (C6). An explicit telemetry_acceptance evaluation
// re-classifies the retained interval evidence of one or more runs under a
// named project telemetry policy revision, so runs whose own receipts differ
// can be compared alike. It re-scores the lag budgets only, with the live
// rules: a zero or absent budget disables a stage, a cross-host stage needs
// a qualified clock (otherwise the interval is unscored), and a condition
// is a breach once it has lasted the policy's sustained window. A run whose
// retained evidence cannot answer the policy is reported as unanswerable,
// never as healthy. The result is a new derived artifact, hashed with its
// inputs; the runs' archives and original classifications are untouched.

const (
	telemetryReevaluationKind      = "telemetry_acceptance"
	telemetryReevaluationSchema    = "apexload.telemetry_reevaluation.v1"
	telemetryReevaluationAlgorithm = "telemetry-budget-reevaluation.v1"
	maxTelemetryReevaluationRuns   = 10
	maxTelemetryReevaluationIDLen  = 128
	telemetryReevaluationDeadline  = 5 * time.Minute
)

func init() {
	registerEvaluationKind(evaluationKind{
		Name:     telemetryReevaluationKind,
		Deadline: telemetryReevaluationDeadline,
		Prepare: func(s *Server, projectID string, raw json.RawMessage) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			inputs, err := prepareTelemetryReevaluation(ctx, s, projectID, raw)
			if err != nil {
				return "", err
			}
			return inputs.fingerprint, nil
		},
		Run: runTelemetryReevaluation,
	})
}

// TelemetryReevaluationRequest names the runs and the common policy.
type TelemetryReevaluationRequest struct {
	Runs   []string                       `json:"runs"`
	Policy TelemetryReevaluationPolicyRef `json:"policy"`
	// Segments selects, per run, one of its telemetry policy segments
	// (TEL-B): only the intervals that segment classified are re-evaluated,
	// and its receipt is the original policy. A run not named is re-evaluated
	// whole. Segment 1 is the admission receipt.
	Segments map[string]int64 `json:"segments,omitempty"`
}

// TelemetryReevaluationPolicyRef is a project telemetry_policy revision.
// The revision is explicit so the result is reproducible.
type TelemetryReevaluationPolicyRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// TelemetryReevaluationBudget is one resolved stage budget. Source says
// whether the policy set it or it resolved to the built-in default.
type TelemetryReevaluationBudget struct {
	Stage    string  `json:"stage"`
	Enabled  bool    `json:"enabled"`
	LimitMs  float64 `json:"limit_ms"`
	Source   string  `json:"source"`
	Advisory bool    `json:"advisory,omitempty"`
}

// TelemetryReevaluationPolicy is the resolved common policy.
type TelemetryReevaluationPolicy struct {
	ID                      string                        `json:"id"`
	Name                    string                        `json:"name"`
	Revision                int64                         `json:"revision"`
	SHA256                  string                        `json:"sha256"`
	Budgets                 []TelemetryReevaluationBudget `json:"budgets"`
	SustainedBreachWindowMs int64                         `json:"sustained_breach_window_ms"`
}

// TelemetryClassificationCounts counts retained intervals by status.
type TelemetryClassificationCounts struct {
	Intervals int64 `json:"intervals"`
	Healthy   int64 `json:"healthy"`
	Degraded  int64 `json:"degraded"`
	Breached  int64 `json:"breached"`
	Unscored  int64 `json:"unscored"`
	// Unknown intervals retained no telemetry (or no classification).
	Unknown int64 `json:"unknown"`
	// BreachedClassifiedPct is breached over healthy+degraded+breached.
	BreachedClassifiedPct *float64 `json:"breached_classified_pct,omitempty"`
}

// TelemetryReevaluationRun is one run's original and re-evaluated
// classification.
type TelemetryReevaluationRun struct {
	RunID          string `json:"run_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
	// Segment is the telemetry policy segment re-evaluated, when one was
	// selected.
	Segment int64 `json:"segment,omitempty"`
	// OriginalPolicyHash is the run's own receipt, when it retained one: the
	// selected segment's, or for a whole run whose policy never changed, its
	// admission receipt.
	OriginalPolicyHash string `json:"original_policy_hash,omitempty"`
	// PolicySegments is the number of receipts a whole run's original
	// classification spans when its policy changed while it ran (no single
	// original receipt is named); select a segment to compare one.
	PolicySegments int                           `json:"policy_segments,omitempty"`
	Original       TelemetryClassificationCounts `json:"original"`
	Answerable     bool                          `json:"answerable"`
	// Unanswerable says why the retained evidence cannot answer the policy.
	Unanswerable []string `json:"unanswerable,omitempty"`
	// Reevaluated is present only for an answerable run.
	Reevaluated *TelemetryClassificationCounts `json:"reevaluated,omitempty"`
	// StageConditions counts intervals over each stage's budget, and
	// StageUnscored the intervals a stage could not be scored in, by reason.
	StageConditions map[string]int64 `json:"stage_conditions,omitempty"`
	StageUnscored   map[string]int64 `json:"stage_unscored,omitempty"`
}

// TelemetryReevaluationResult is the derived artifact.
type TelemetryReevaluationResult struct {
	SchemaVersion    string                      `json:"schema_version"`
	Algorithm        string                      `json:"algorithm"`
	InputFingerprint string                      `json:"input_fingerprint"`
	Policy           TelemetryReevaluationPolicy `json:"policy"`
	Runs             []TelemetryReevaluationRun  `json:"runs"`
	// AllAnswerable is true when every run could be classified under the
	// policy, so their re-evaluated counts compare alike.
	AllAnswerable bool     `json:"all_answerable"`
	Notes         []string `json:"notes"`
}

type telemetryReevaluationRunInput struct {
	id      string
	record  *storage.RunArchiveRecord
	segment int64
}

type telemetryReevaluationInputs struct {
	request     TelemetryReevaluationRequest
	policy      TelemetryReevaluationPolicy
	runs        []telemetryReevaluationRunInput
	fingerprint string
}

// prepareTelemetryReevaluation validates a request against the project and
// resolves its inputs; the fingerprint covers the policy revision's digest
// and each run's finalized archive manifest digest.
func prepareTelemetryReevaluation(ctx context.Context, s *Server, projectID string, raw json.RawMessage) (*telemetryReevaluationInputs, error) {
	var request TelemetryReevaluationRequest
	if err := decodeAnalysisDefinitionJSON(raw, &request); err != nil {
		return nil, fmt.Errorf("invalid telemetry re-evaluation request: %w", err)
	}
	if len(request.Runs) == 0 || len(request.Runs) > maxTelemetryReevaluationRuns {
		return nil, fmt.Errorf("a telemetry re-evaluation names 1 to %d runs", maxTelemetryReevaluationRuns)
	}
	if strings.TrimSpace(request.Policy.ID) == "" || len(request.Policy.ID) > maxTelemetryReevaluationIDLen || request.Policy.Revision < 1 {
		return nil, errors.New("policy needs an id and an explicit revision")
	}
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		return nil, evaluationUnavailable(errors.New("analysis policies are unavailable"))
	}
	definition, err := store.GetAnalysisDefinition(projectID, request.Policy.ID, request.Policy.Revision)
	if err != nil || definition == nil || definition.Kind != storage.AnalysisDefinitionKindTelemetryPolicy {
		return nil, evaluationPolicyLookupError(err, "policy is not a telemetry_policy revision of this project")
	}
	if _, err := decodeAnalysisPolicy(definition.Kind, definition.Definition); err != nil {
		return nil, fmt.Errorf("policy revision is invalid: %w", err)
	}
	var payload TelemetryPolicyDefinition
	if err := json.Unmarshal(definition.Definition, &payload); err != nil {
		return nil, fmt.Errorf("policy revision is invalid: %w", err)
	}
	digest := sha256.Sum256(definition.Definition)
	inputs := &telemetryReevaluationInputs{request: request, policy: resolveTelemetryReevaluationPolicy(definition, payload, hex.EncodeToString(digest[:]))}
	// Segment selections keyed by trimmed run ID; a run named twice is refused.
	segmentByRun := map[string]int64{}
	for id, sequence := range request.Segments {
		id = strings.TrimSpace(id)
		if _, twice := segmentByRun[id]; twice {
			return nil, fmt.Errorf("segments names run %s twice", id)
		}
		if sequence < 1 {
			return nil, fmt.Errorf("run %s: a segment is 1 or more", id)
		}
		segmentByRun[id] = sequence
	}
	seen := map[string]bool{}
	if s.runner == nil {
		return nil, evaluationUnavailable(errors.New("runs are unavailable"))
	}
	for _, id := range request.Runs {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > maxTelemetryReevaluationIDLen {
			return nil, errors.New("run IDs must be non-empty")
		}
		test, ok := s.runner.GetTestSummary(id)
		if !ok || test == nil || normalizeProjectID(test.ProjectID) != projectID {
			return nil, evaluationNotFound(fmt.Errorf("run %s is not in this project", id))
		}
		if seen[test.ID] {
			return nil, fmt.Errorf("run %s is named twice", id)
		}
		seen[test.ID] = true
		record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, test.ID)
		if !ok || record == nil || strings.TrimSpace(record.ManifestSHA256) == "" {
			return nil, evaluationConflict(fmt.Errorf("run %s has no finalized archive", id))
		}
		inputs.runs = append(inputs.runs, telemetryReevaluationRunInput{id: test.ID, record: record, segment: segmentByRun[id]})
	}
	for id := range segmentByRun {
		if !seen[id] {
			return nil, fmt.Errorf("segments names run %s, which the re-evaluation does not", id)
		}
	}
	inputs.fingerprint = telemetryReevaluationFingerprint(projectID, inputs)
	return inputs, nil
}

func telemetryReevaluationFingerprint(projectID string, inputs *telemetryReevaluationInputs) string {
	type run struct {
		ID             string `json:"id"`
		ManifestSHA256 string `json:"manifest_sha256"`
		Segment        int64  `json:"segment,omitempty"`
	}
	value := struct {
		Algorithm string `json:"algorithm"`
		Project   string `json:"project"`
		PolicyID  string `json:"policy_id"`
		Revision  int64  `json:"revision"`
		PolicySHA string `json:"policy_sha256"`
		Runs      []run  `json:"runs"`
	}{Algorithm: telemetryReevaluationAlgorithm, Project: projectID, PolicyID: inputs.policy.ID, Revision: inputs.policy.Revision, PolicySHA: inputs.policy.SHA256}
	for _, r := range inputs.runs {
		value.Runs = append(value.Runs, run{ID: r.id, ManifestSHA256: strings.ToLower(strings.TrimSpace(r.record.ManifestSHA256)), Segment: r.segment})
	}
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// resolveTelemetryReevaluationPolicy resolves the policy's budgets: an
// absent budget takes the built-in default (never the current system
// settings, which can change), so the result depends on the revision alone.
func resolveTelemetryReevaluationPolicy(definition *storage.AnalysisDefinition, payload TelemetryPolicyDefinition, digest string) TelemetryReevaluationPolicy {
	policy := TelemetryReevaluationPolicy{ID: scrubTelemetryIdentity(definition.ID), Name: telemetryTextScrubber{}.label(definition.Name), Revision: definition.Revision, SHA256: digest, SustainedBreachWindowMs: telemetrySustainedBreachWindow.Milliseconds()}
	if payload.SustainedWindowSeconds != nil {
		policy.SustainedBreachWindowMs = int64(*payload.SustainedWindowSeconds) * 1000
	}
	for _, stage := range []struct {
		name     string
		value    *float64
		fallback float64
	}{
		{"sample_age", payload.SampleAgeMs, defaultTelemetrySampleAgeBudgetMs},
		{"generator_processing", payload.GeneratorLagMs, defaultTelemetryGeneratorProcessingBudgetMs},
		{"transport", payload.TransportLagMs, defaultTelemetryTransportBudgetMs},
		{"controller_fanout", payload.ControllerFanoutMs, defaultTelemetryControllerFanoutBudgetMs},
		{"browser_render", payload.BrowserRenderLagMs, defaultTelemetryBrowserRenderBudgetMs},
	} {
		budget := TelemetryReevaluationBudget{Stage: stage.name, LimitMs: stage.fallback, Source: "built_in_default", Advisory: stage.name == "browser_render"}
		if stage.value != nil {
			budget.LimitMs, budget.Source = *stage.value, "policy"
		}
		budget.Enabled = budget.LimitMs > 0 && !budget.Advisory
		policy.Budgets = append(policy.Budgets, budget)
	}
	return policy
}

func (p TelemetryReevaluationPolicy) limit(stage string) float64 {
	for _, budget := range p.Budgets {
		if budget.Stage == stage && budget.Enabled {
			return budget.LimitMs
		}
	}
	return 0
}

func runTelemetryReevaluation(ctx context.Context, s *Server, job *storage.EvaluationJob) (any, error) {
	inputs, err := prepareTelemetryReevaluation(ctx, s, job.ProjectID, json.RawMessage(job.Request))
	if err != nil {
		return nil, err
	}
	if inputs.fingerprint != job.InputFingerprint {
		return nil, errors.New("a run archive or the policy revision changed after this evaluation was queued; queue a new evaluation")
	}
	result := TelemetryReevaluationResult{SchemaVersion: telemetryReevaluationSchema, Algorithm: telemetryReevaluationAlgorithm, InputFingerprint: inputs.fingerprint, Policy: inputs.policy, AllAnswerable: true,
		Notes: []string{
			"A derived result: each run's archive and original classification are unchanged.",
			"Only the lag budgets are re-scored. The original classification also counted drop, stream-gap and transaction diagnostics, so original and re-evaluated counts are not a like-for-like change.",
			"Browser render is advisory and never scored.",
		}}
	for _, run := range inputs.runs {
		evaluated, err := reevaluateRunArchive(ctx, run, inputs.policy)
		if err != nil {
			return nil, err
		}
		result.AllAnswerable = result.AllAnswerable && evaluated.Answerable
		result.Runs = append(result.Runs, evaluated)
	}
	return result, nil
}

func reevaluateRunArchive(ctx context.Context, run telemetryReevaluationRunInput, policy TelemetryReevaluationPolicy) (TelemetryReevaluationRun, error) {
	out := TelemetryReevaluationRun{RunID: run.id, ManifestSHA256: strings.ToLower(strings.TrimSpace(run.record.ManifestSHA256))}
	lease, err := acquireArchiveMaterialized(ctx, run.record)
	if err != nil {
		return out, fmt.Errorf("run %s archive: %w", run.id, err)
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(run.record)
	if err != nil {
		return out, fmt.Errorf("run %s manifest: %w", run.id, err)
	}
	intervals, err := readAllArchiveSummariesContext(ctx, run.record)
	if err != nil {
		return out, fmt.Errorf("run %s intervals: %w", run.id, err)
	}
	if len(intervals) > 100000 {
		return out, fmt.Errorf("run %s retains more than 100000 intervals", run.id)
	}
	if evidence := telemetryPolicyEvidenceFromProvenance(&manifest.Provenance); evidence.Policy != nil {
		out.OriginalPolicyHash = evidence.Policy.Hash
	}
	var breaks []time.Time
	if run.segment > 0 {
		out.Segment = run.segment
		hash, ok := telemetryManifestSegmentHash(manifest, run.segment)
		if !ok {
			out.Unanswerable = []string{"segment_unknown"}
			return out, nil
		}
		out.OriginalPolicyHash = hash
		intervals, breaks = telemetryIntervalsOfSegment(intervals, run.segment, len(manifest.TelemetryPolicySegments) < 2, manifest.TelemetryCoverage)
	} else if len(manifest.TelemetryPolicySegments) > 1 {
		out.OriginalPolicyHash = ""
		out.PolicySegments = len(manifest.TelemetryPolicySegments)
	}
	reevaluateTelemetryIntervals(&out, intervals, policy, telemetryRunDistributed(manifest), breaks...)
	return out, nil
}

// telemetryManifestSegmentHash is the receipt hash of a run's segment: the
// admission receipt's for segment 1 of a run whose policy never changed.
func telemetryManifestSegmentHash(manifest *RunArchiveManifest, segment int64) (string, bool) {
	for _, recorded := range manifest.TelemetryPolicySegments {
		if recorded.Sequence == segment && recorded.Policy != nil {
			return recorded.Policy.Hash, true
		}
	}
	if segment == 1 && len(manifest.TelemetryPolicySegments) == 0 {
		if evidence := telemetryPolicyEvidenceFromProvenance(&manifest.Provenance); evidence.Policy != nil {
			return evidence.Policy.Hash, true
		}
	}
	return "", false
}

// telemetryIntervalsOfSegment keeps the intervals a segment classified; an
// interval a boundary fell inside belongs to none. An interval naming no
// segment (archived by an older controller) is the segment's in a run whose
// policy never changed, or when its span as coverage counts it (trimmed to
// the run's coverage window) lies within the segment's span.
//
// breaks are the times of the intervals left out: each ends a breach spell,
// as it did in the run, so the kept intervals either side never join one.
func telemetryIntervalsOfSegment(intervals []IntervalSummaryRecord, segment int64, single bool, coverage *TelemetryCoverageRecord) ([]IntervalSummaryRecord, []time.Time) {
	var span *TelemetryPolicySegment
	windowStart, windowEnd := int64(math.MinInt64), int64(math.MaxInt64)
	if coverage != nil {
		for i := range coverage.Segments {
			if coverage.Segments[i].Sequence == segment {
				span = &coverage.Segments[i]
			}
		}
		windowStart, windowEnd = coverage.Coverage.WindowStart.UnixMilli(), coverage.Coverage.WindowEnd.UnixMilli()
	}
	out := make([]IntervalSummaryRecord, 0, len(intervals))
	var breaks []time.Time
	for _, row := range telemetryRowSpans(intervals, windowStart, windowEnd) {
		named := int64(0)
		if row.row.TelemetryStatus != nil {
			named = row.row.TelemetryStatus.PolicySegment
		}
		switch {
		case named == segment, single && named == 0:
			out = append(out, row.row)
		case named == 0 && span != nil && row.ok && row.row.TelemetryStatus != nil && !telemetryRowPolicyUnclassified(row.row.TelemetryStatus) &&
			span.Start.UnixMilli() <= row.start && row.end <= span.End.UnixMilli():
			out = append(out, row.row)
		default:
			breaks = append(breaks, row.row.Timestamp)
		}
	}
	return out, breaks
}

const telemetryCadenceScopeDistributed = "distributed-controller-coalescing-gate"

// telemetryRunDistributed reports whether a run's telemetry crossed hosts:
// its receipt's cadence scope when recorded, else generator source epochs or
// a shard on a generator (local runtime-resource shards run in the
// controller).
func telemetryRunDistributed(manifest *RunArchiveManifest) bool {
	if manifest == nil {
		return false
	}
	if evidence := telemetryPolicyEvidenceFromProvenance(&manifest.Provenance); evidence.Policy != nil && evidence.Policy.CadenceScope != "" {
		return evidence.Policy.CadenceScope == telemetryCadenceScopeDistributed
	}
	if manifest.TelemetrySourceEpochCount > 0 {
		return true
	}
	for _, shard := range manifest.Provenance.GeneratorShards {
		if target := strings.TrimSpace(shard.Target); target != "" && target != localRuntimeResourceTarget {
			return true
		}
	}
	return false
}

// reevaluateTelemetryIntervals classifies retained intervals under the
// policy and decides whether the evidence can answer it.
// breaks (in timestamp order) are intervals left out of a segment's
// re-evaluation: each ends a breach spell.
func reevaluateTelemetryIntervals(out *TelemetryReevaluationRun, intervals []IntervalSummaryRecord, policy TelemetryReevaluationPolicy, distributed bool, breaks ...time.Time) {
	ordered := append([]IntervalSummaryRecord(nil), intervals...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Timestamp.Before(ordered[j].Timestamp) })
	reevaluated := TelemetryClassificationCounts{}
	conditions, unscoredByStage := map[string]int64{}, map[string]int64{}
	window := time.Duration(policy.SustainedBreachWindowMs) * time.Millisecond
	var streak *time.Time
	var telemetryTimes []time.Time
	nextBreak := 0
	for _, interval := range ordered {
		for nextBreak < len(breaks) && breaks[nextBreak].Before(interval.Timestamp) {
			streak = nil
			nextBreak++
		}
		out.Original.Intervals++
		reevaluated.Intervals++
		if interval.TelemetryStatus == nil {
			out.Original.Unknown++
		} else {
			countTelemetryStatus(&out.Original, interval.TelemetryStatus.Status)
		}
		t := interval.Telemetry
		if t == nil {
			reevaluated.Unknown++
			streak = nil
			continue
		}
		telemetryTimes = append(telemetryTimes, interval.Timestamp)
		over, unscored, scored := scoreRetainedTelemetry(t, policy, distributed)
		for _, stage := range over {
			conditions[stage]++
		}
		for _, entry := range unscored {
			unscoredByStage[entry]++
		}
		switch {
		case len(over) > 0:
			if streak == nil {
				started := interval.Timestamp
				streak = &started
			}
			if interval.Timestamp.Sub(*streak) >= window {
				reevaluated.Breached++
			} else {
				reevaluated.Degraded++
			}
		case len(unscored) > 0:
			reevaluated.Unscored++
			streak = nil
		case scored == 0:
			// Healthy needs a stage judged within its budget; an interval
			// with nothing to judge is never counted healthy.
			reevaluated.Unscored++
			unscoredByStage["interval:no_scored_stage"]++
			streak = nil
		default:
			reevaluated.Healthy++
			streak = nil
		}
	}
	finishTelemetryCounts(&out.Original)
	finishTelemetryCounts(&reevaluated)
	if len(telemetryTimes) == 0 {
		out.Unanswerable = append(out.Unanswerable, "no_retained_telemetry")
	} else if spacing := medianTelemetrySpacing(telemetryTimes); spacing > window {
		// The retained intervals cannot resolve a condition lasting less
		// than their own spacing.
		out.Unanswerable = append(out.Unanswerable, fmt.Sprintf("sustained_window_shorter_than_retained_interval_spacing:%dms", spacing.Milliseconds()))
	}
	if len(telemetryTimes) > 0 && reevaluated.Healthy+reevaluated.Degraded+reevaluated.Breached == 0 {
		out.Unanswerable = append(out.Unanswerable, "no_classifiable_intervals")
	}
	out.Answerable = len(out.Unanswerable) == 0
	if out.Answerable {
		out.Reevaluated = &reevaluated
	}
	// The per-stage breakdown stays on an unanswerable run: it says why
	// nothing could be classified (a missing clock quality, every budget
	// disabled, ...).
	if len(conditions) > 0 {
		out.StageConditions = scrubTelemetryCounts(conditions)
	}
	if len(unscoredByStage) > 0 {
		out.StageUnscored = scrubTelemetryCounts(unscoredByStage)
	}
}

// scoreRetainedTelemetry returns the stages over budget, the stages that
// could not be scored ("stage:reason") and how many enabled stages were
// judged against their budget in one retained interval.
func scoreRetainedTelemetry(t *TelemetryHealth, policy TelemetryReevaluationPolicy, distributed bool) (over, unscored []string, scored int) {
	scoreClock := func(stage string, value float64, quality *generatorrpc.ClockQuality, measured bool) {
		limit := policy.limit(stage)
		if limit <= 0 || !measured {
			return
		}
		if distributed && quality == nil {
			// A cross-host age without its retained clock quality cannot
			// be told from clock skew.
			unscored = append(unscored, stage+":clock_quality_not_retained")
			return
		}
		if reason := clockBudgetUnscored(value, &limit, quality); reason != "" {
			unscored = append(unscored, stage+":"+reason)
			return
		}
		scored++
		if value := clockScoredValue(value, quality); value > 0 && value > limit {
			over = append(over, stage)
		}
	}
	scoreLocal := func(stage string, value float64) {
		limit := policy.limit(stage)
		if limit <= 0 || value <= 0 {
			return
		}
		scored++
		if value > limit {
			over = append(over, stage)
		}
	}
	// As live: an interval in which no generator reported a sample has no
	// sample-age decision.
	scoreClock("sample_age", t.SampleAgeMs, t.SampleAgeQuality, !t.CollectedAt.IsZero() && !t.SampleAgeUnavailable)
	scoreLocal("generator_processing", t.GeneratorProcessingLagMs)
	scoreClock("transport", t.TransportLagMs, t.TransportLagQuality, t.TransportLagMs > 0 || t.TransportLagQuality != nil)
	scoreLocal("controller_fanout", t.ControllerFanoutLagMs)
	return over, unscored, scored
}

func countTelemetryStatus(counts *TelemetryClassificationCounts, status string) {
	switch status {
	case "healthy":
		counts.Healthy++
	case "degraded":
		counts.Degraded++
	case "breached":
		counts.Breached++
	case "unscored":
		counts.Unscored++
	default:
		counts.Unknown++
	}
}

func finishTelemetryCounts(counts *TelemetryClassificationCounts) {
	if classified := counts.Healthy + counts.Degraded + counts.Breached; classified > 0 {
		value := 100 * float64(counts.Breached) / float64(classified)
		counts.BreachedClassifiedPct = &value
	}
}

func medianTelemetrySpacing(times []time.Time) time.Duration {
	if len(times) < 2 {
		return 0
	}
	gaps := make([]time.Duration, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		gaps = append(gaps, times[i].Sub(times[i-1]))
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return gaps[len(gaps)/2]
}

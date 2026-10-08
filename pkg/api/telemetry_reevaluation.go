package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	// OriginalPolicyHash is the run's own receipt, when it retained one.
	OriginalPolicyHash string                        `json:"original_policy_hash,omitempty"`
	Original           TelemetryClassificationCounts `json:"original"`
	Answerable         bool                          `json:"answerable"`
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
	id     string
	record *storage.RunArchiveRecord
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
		return nil, errors.New("analysis policies are unavailable")
	}
	definition, err := store.GetAnalysisDefinition(projectID, request.Policy.ID, request.Policy.Revision)
	if err != nil || definition == nil || definition.Kind != storage.AnalysisDefinitionKindTelemetryPolicy {
		return nil, errors.New("policy is not a telemetry_policy revision of this project")
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
	seen := map[string]bool{}
	if s.runner == nil {
		return nil, errors.New("runs are unavailable")
	}
	for _, id := range request.Runs {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > maxTelemetryReevaluationIDLen {
			return nil, errors.New("run IDs must be non-empty")
		}
		test, ok := s.runner.GetTestSummary(id)
		if !ok || test == nil || normalizeProjectID(test.ProjectID) != projectID {
			return nil, fmt.Errorf("run %s is not in this project", id)
		}
		if seen[test.ID] {
			return nil, fmt.Errorf("run %s is named twice", id)
		}
		seen[test.ID] = true
		record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, test.ID)
		if !ok || record == nil || strings.TrimSpace(record.ManifestSHA256) == "" {
			return nil, fmt.Errorf("run %s has no finalized archive", id)
		}
		inputs.runs = append(inputs.runs, telemetryReevaluationRunInput{id: test.ID, record: record})
	}
	inputs.fingerprint = telemetryReevaluationFingerprint(projectID, inputs)
	return inputs, nil
}

func telemetryReevaluationFingerprint(projectID string, inputs *telemetryReevaluationInputs) string {
	type run struct {
		ID             string `json:"id"`
		ManifestSHA256 string `json:"manifest_sha256"`
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
		value.Runs = append(value.Runs, run{ID: r.id, ManifestSHA256: strings.ToLower(strings.TrimSpace(r.record.ManifestSHA256))})
	}
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// resolveTelemetryReevaluationPolicy resolves the policy's budgets: an
// absent budget takes the built-in default (never the current system
// settings, which can change), so the result depends on the revision alone.
func resolveTelemetryReevaluationPolicy(definition *storage.AnalysisDefinition, payload TelemetryPolicyDefinition, digest string) TelemetryReevaluationPolicy {
	policy := TelemetryReevaluationPolicy{ID: definition.ID, Name: definition.Name, Revision: definition.Revision, SHA256: digest, SustainedBreachWindowMs: telemetrySustainedBreachWindow.Milliseconds()}
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
	reevaluateTelemetryIntervals(&out, intervals, policy, telemetryRunDistributed(manifest))
	return out, nil
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
func reevaluateTelemetryIntervals(out *TelemetryReevaluationRun, intervals []IntervalSummaryRecord, policy TelemetryReevaluationPolicy, distributed bool) {
	ordered := append([]IntervalSummaryRecord(nil), intervals...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Timestamp.Before(ordered[j].Timestamp) })
	reevaluated := TelemetryClassificationCounts{}
	conditions, unscoredByStage := map[string]int64{}, map[string]int64{}
	window := time.Duration(policy.SustainedBreachWindowMs) * time.Millisecond
	var streak *time.Time
	var telemetryTimes []time.Time
	for _, interval := range ordered {
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
		if len(conditions) > 0 {
			out.StageConditions = conditions
		}
		if len(unscoredByStage) > 0 {
			out.StageUnscored = unscoredByStage
		}
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

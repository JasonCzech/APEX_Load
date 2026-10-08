package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Browser intelligence findings and qualified scores (E1). A run's archived
// final document metrics are judged against a versioned browser_policy as a
// browser_intelligence evaluation job: findings (browser_findings.v1) and a
// weighted score (browser_score.v1). Only qualified final observations of a
// main-frame navigation count; everything else is reported as quality. The
// job runs automatically when a run that pinned a browser policy finalizes
// with document evidence, and on demand.

const (
	browserIntelligenceKind           = "browser_intelligence"
	browserFindingsRuleSet            = "browser_findings.v1"
	browserScoreVersion               = "browser_score.v1"
	browserFindingsSchemaVersion      = "apexload.browser_findings.v1"
	browserIntelligenceDeadline       = 5 * time.Minute
	maxBrowserFindingEvidence         = 5
	browserFindingStatistic           = "p75"
	browserFindingRuleThreshold       = "threshold"
	browserFindingRuleCollectionLoss  = "collection.incomplete"
	browserFindingRuleCollectionNone  = "collection.absent"
	browserFindingCategoryApplication = "application_outcome"
	browserFindingCategoryCollection  = "collection_health"
)

func init() {
	registerEvaluationKind(evaluationKind{
		Name:     browserIntelligenceKind,
		Deadline: browserIntelligenceDeadline,
		Prepare: func(s *Server, projectID string, raw json.RawMessage) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			inputs, err := prepareBrowserIntelligence(ctx, s, projectID, raw)
			if err != nil {
				return "", err
			}
			return inputs.fingerprint, nil
		},
		Run:     runBrowserIntelligence,
		OnReady: notifyBrowserFindingTransitions,
		Subject: browserIntelligenceSubject,
	})
}

// BrowserIntelligenceRequest names the run and, optionally, the policy
// revision; the run's pinned browser_policy applies otherwise.
type BrowserIntelligenceRequest struct {
	Run    string                     `json:"run"`
	Policy *BrowserIntelligencePolicy `json:"policy,omitempty"`
}

type BrowserIntelligencePolicy struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// BrowserPolicyRef identifies the evaluated policy revision.
type BrowserPolicyRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	SHA256   string `json:"sha256"`
	// Source is "pinned" (the run's own pin) or "requested".
	Source string `json:"source"`
}

type BrowserFindingCohort struct {
	Engine         string `json:"engine"`
	DeviceProfile  string `json:"device_profile"`
	FrameScope     string `json:"frame_scope"`
	LifecyclePhase string `json:"lifecycle_phase"`
}

func (c BrowserFindingCohort) key() string {
	return c.Engine + "\x00" + c.DeviceProfile + "\x00" + c.FrameScope + "\x00" + c.LifecyclePhase
}

type BrowserFindingWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// BrowserFindingQuality counts what the observed value rests on.
type BrowserFindingQuality struct {
	EligibleFinal    int64            `json:"eligible_final"`
	UnqualifiedFinal int64            `json:"unqualified_final"`
	UnavailableFinal int64            `json:"unavailable_final"`
	Censored         int64            `json:"censored"`
	Provisional      int64            `json:"provisional"`
	ExcludedReasons  map[string]int64 `json:"excluded_reasons,omitempty"`
	// Incomplete: the run's document evidence lost observations, so the
	// population may be short.
	Incomplete bool `json:"incomplete,omitempty"`
}

// BrowserFindingBaseline records the baseline criteria; baseline anomalies
// are a separate rule set (E2), so a threshold finding is never compared.
type BrowserFindingBaseline struct {
	Mode   string `json:"mode"`
	Status string `json:"status"`
}

// BrowserFindingEvidence links a finding to immutable archive evidence.
type BrowserFindingEvidence struct {
	// Documents are up to five documents whose qualified value crossed the
	// threshold, worst first.
	Documents []BrowserFindingDocument `json:"documents,omitempty"`
}

type BrowserFindingDocument struct {
	DocumentID       string  `json:"document_id"`
	SourceGeneration string  `json:"source_generation"`
	Value            float64 `json:"value"`
	// Resources is the archive query of the document's retained resources.
	Resources string `json:"resources"`
}

// BrowserFinding is one versioned finding record.
type BrowserFinding struct {
	// ID identifies this record: it covers the evaluation's input
	// fingerprint, so an identical rerun reproduces it. Key identifies the
	// finding across evaluations of the run (rule, metric, cohort, window).
	ID          string                 `json:"id"`
	Key         string                 `json:"key"`
	RuleSet     string                 `json:"rule_set"`
	RuleID      string                 `json:"rule_id"`
	RuleVersion int                    `json:"rule_version"`
	Category    string                 `json:"category"`
	Metric      string                 `json:"metric,omitempty"`
	Algorithm   string                 `json:"algorithm,omitempty"`
	Unit        string                 `json:"unit,omitempty"`
	Cohort      *BrowserFindingCohort  `json:"cohort,omitempty"`
	Window      BrowserFindingWindow   `json:"window"`
	Applicable  bool                   `json:"applicable"`
	Statistic   string                 `json:"statistic,omitempty"`
	Observed    *float64               `json:"observed,omitempty"`
	Good        *float64               `json:"good,omitempty"`
	Poor        *float64               `json:"poor,omitempty"`
	Severity    string                 `json:"severity,omitempty"`
	Quality     BrowserFindingQuality  `json:"quality"`
	Baseline    BrowserFindingBaseline `json:"baseline"`
	Evidence    BrowserFindingEvidence `json:"evidence"`
	// State is active, suppressed or unscored; Reason says why a finding is
	// not active.
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary"`
}

type BrowserScoreComponent struct {
	Metric   string  `json:"metric"`
	Weight   float64 `json:"weight"`
	Required bool    `json:"required"`
	Good     float64 `json:"good"`
	Poor     float64 `json:"poor"`
	// Observed is the p75 of the qualified final observations of every
	// applicable cohort; Samples counts them.
	Observed     *float64 `json:"observed,omitempty"`
	Samples      int64    `json:"samples"`
	Score        *float64 `json:"score,omitempty"`
	Contribution *float64 `json:"contribution,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

// BrowserScore is browser_score.v1: a weighted mean of component scores
// (100 at or below good, 0 at or above poor, linear between), only when
// every required (positively weighted) metric was observed.
type BrowserScore struct {
	Version        string                  `json:"version"`
	Score          *float64                `json:"score,omitempty"`
	Severity       string                  `json:"severity"`
	UnscoredReason string                  `json:"unscored_reason,omitempty"`
	Missing        []string                `json:"missing,omitempty"`
	Components     []BrowserScoreComponent `json:"components"`
	Coverage       BrowserScoreCoverage    `json:"coverage"`
}

type BrowserScoreCoverage struct {
	Observed int `json:"observed"`
	Required int `json:"required"`
}

type BrowserIntelligenceResult struct {
	SchemaVersion    string               `json:"schema_version"`
	RuleSet          string               `json:"rule_set"`
	InputFingerprint string               `json:"input_fingerprint"`
	RunID            string               `json:"run_id"`
	Policy           BrowserPolicyRef     `json:"policy"`
	Window           BrowserFindingWindow `json:"window"`
	Score            BrowserScore         `json:"score"`
	Findings         []BrowserFinding     `json:"findings"`
	Counts           map[string]int       `json:"counts"`
	Notes            []string             `json:"notes"`
}

type browserIntelligenceInputs struct {
	request     BrowserIntelligenceRequest
	projectID   string
	runID       string
	record      *storage.RunArchiveRecord
	policy      BrowserPolicyDefinition
	ref         BrowserPolicyRef
	fingerprint string
}

// prepareBrowserIntelligence validates a request and resolves its inputs; the
// fingerprint covers the rule set, the score version, the policy revision's
// digest and the run's finalized manifest digest.
func prepareBrowserIntelligence(ctx context.Context, s *Server, projectID string, raw json.RawMessage) (*browserIntelligenceInputs, error) {
	var request BrowserIntelligenceRequest
	if err := decodeAnalysisDefinitionJSON(raw, &request); err != nil {
		return nil, fmt.Errorf("invalid browser intelligence request: %w", err)
	}
	runID := strings.TrimSpace(request.Run)
	if runID == "" || len(runID) > 128 {
		return nil, errors.New("a browser intelligence request names one run")
	}
	if s.runner == nil {
		return nil, errors.New("runs are unavailable")
	}
	test, ok := s.runner.GetTestSummary(runID)
	if !ok || test == nil || normalizeProjectID(test.ProjectID) != projectID {
		return nil, fmt.Errorf("run %s is not in this project", runID)
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, test.ID)
	if !ok || record == nil || strings.TrimSpace(record.ManifestSHA256) == "" {
		return nil, fmt.Errorf("run %s has no finalized archive", runID)
	}
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		return nil, errors.New("analysis policies are unavailable")
	}
	// Like every archive reader, hold a materialization lease while reading:
	// an object-backed archive's local source may have been retired.
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("run %s archive: %w", runID, err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	lease.Release()
	if err != nil {
		return nil, fmt.Errorf("run %s manifest: %w", runID, err)
	}
	inputs := &browserIntelligenceInputs{request: request, projectID: projectID, runID: test.ID, record: record}
	id, revision, source := "", int64(0), "requested"
	var pin *RunPolicyPin
	if request.Policy != nil {
		id, revision = strings.TrimSpace(request.Policy.ID), request.Policy.Revision
		if id == "" || len(id) > 128 || revision < 1 {
			return nil, errors.New("policy needs an id and an explicit revision")
		}
	} else {
		for index := range manifest.Provenance.PolicyPins {
			if manifest.Provenance.PolicyPins[index].Kind == storage.AnalysisDefinitionKindBrowserPolicy {
				pin = &manifest.Provenance.PolicyPins[index]
			}
		}
		if pin == nil {
			return nil, fmt.Errorf("run %s pinned no browser_policy; name one", runID)
		}
		id, revision, source = pin.ID, pin.Revision, "pinned"
	}
	definition, err := store.GetAnalysisDefinition(projectID, id, revision)
	if err != nil || definition == nil || definition.Kind != storage.AnalysisDefinitionKindBrowserPolicy {
		return nil, errors.New("policy is not a browser_policy revision of this project")
	}
	digest := sha256.Sum256(definition.Definition)
	sum := hex.EncodeToString(digest[:])
	if pin != nil && pin.SHA256 != sum {
		return nil, errors.New("the pinned browser policy revision no longer matches its pin")
	}
	if _, err := decodeAnalysisPolicy(definition.Kind, definition.Definition); err != nil {
		return nil, fmt.Errorf("policy revision is invalid: %w", err)
	}
	if err := json.Unmarshal(definition.Definition, &inputs.policy); err != nil {
		return nil, fmt.Errorf("policy revision is invalid: %w", err)
	}
	inputs.ref = BrowserPolicyRef{ID: definition.ID, Name: definition.Name, Revision: definition.Revision, SHA256: sum, Source: source}
	value, _ := json.Marshal(struct {
		RuleSet        string `json:"rule_set"`
		Score          string `json:"score"`
		Project        string `json:"project"`
		Run            string `json:"run"`
		ManifestSHA256 string `json:"manifest_sha256"`
		PolicyID       string `json:"policy_id"`
		Revision       int64  `json:"revision"`
		PolicySHA      string `json:"policy_sha256"`
	}{browserFindingsRuleSet, browserScoreVersion, projectID, test.ID, strings.ToLower(strings.TrimSpace(record.ManifestSHA256)), inputs.ref.ID, inputs.ref.Revision, sum})
	fingerprint := sha256.Sum256(value)
	inputs.fingerprint = hex.EncodeToString(fingerprint[:])
	return inputs, nil
}

func runBrowserIntelligence(ctx context.Context, s *Server, job *storage.EvaluationJob) (any, error) {
	inputs, err := prepareBrowserIntelligence(ctx, s, job.ProjectID, json.RawMessage(job.Request))
	if err != nil {
		return nil, err
	}
	if inputs.fingerprint != job.InputFingerprint {
		return nil, errors.New("the run archive or the policy revision changed after this evaluation was queued; queue a new evaluation")
	}
	lease, err := acquireArchiveMaterialized(ctx, inputs.record)
	if err != nil {
		return nil, fmt.Errorf("run %s archive: %w", inputs.runID, err)
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(inputs.record)
	if err != nil {
		return nil, fmt.Errorf("run %s manifest: %w", inputs.runID, err)
	}
	accumulator, requested, err := readArchiveBrowserDocumentMeasurements(ctx, inputs.record, manifest)
	if err != nil {
		return nil, fmt.Errorf("run %s browser documents: %w", inputs.runID, err)
	}
	var observations []protocol.BrowserDocumentObservation
	incomplete, dropped := false, int64(0)
	if accumulator != nil {
		keys := make([]string, 0, len(accumulator.measurements))
		for key := range accumulator.measurements {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			observations = append(observations, accumulator.measurements[key].observation)
		}
		incomplete, dropped = accumulator.incomplete, accumulator.dropped
	}
	window := BrowserFindingWindow{Start: manifest.CreatedAt.UTC(), End: manifest.CompletedAt.UTC()}
	if manifest.Provenance.StartedAt != nil {
		window.Start = manifest.Provenance.StartedAt.UTC()
	}
	return evaluateBrowserFindings(inputs, window, observations, requested || accumulator != nil, incomplete, dropped), nil
}

// browserCohortEvidence is one cohort's view of one metric.
type browserCohortEvidence struct {
	cohort    BrowserFindingCohort
	algorithm string
	quality   BrowserFindingQuality
	// values are the qualified final observations' values.
	values []float64
}

// browserEngineFamily maps an observed engine onto the policy's engine
// vocabulary (chromium or webkit).
func browserEngineFamily(engine string) string {
	if engine == "chrome" {
		return "chromium"
	}
	return engine
}

func browserPolicyApplicability(policy BrowserPolicyDefinition, cohort BrowserFindingCohort) (bool, string) {
	if len(policy.Applicability.Engines) > 0 && !slices.Contains(policy.Applicability.Engines, browserEngineFamily(cohort.Engine)) {
		return false, "engine_not_applicable"
	}
	if len(policy.Applicability.DeviceProfiles) > 0 && !slices.Contains(policy.Applicability.DeviceProfiles, cohort.DeviceProfile) {
		return false, "device_profile_not_applicable"
	}
	return true, ""
}

// browserComponentScore is 100 at or below good, 0 at or above poor and
// linear between.
func browserComponentScore(value, good, poor float64) float64 {
	switch {
	case value <= good:
		return 100
	case value >= poor:
		return 0
	}
	return 100 * (poor - value) / (poor - good)
}

func browserSeverity(value, good, poor float64) string {
	switch {
	case value <= good:
		return "good"
	case value > poor:
		return "poor"
	}
	return "needs_improvement"
}

// browserScoreSeverity bands an overall score: good from 90, needs
// improvement from 50, poor below.
func browserScoreSeverity(score float64) string {
	switch {
	case score >= 90:
		return "good"
	case score >= 50:
		return "needs_improvement"
	}
	return "poor"
}

func browserFindingIdentity(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// evaluateBrowserFindings applies browser_findings.v1 and browser_score.v1.
func evaluateBrowserFindings(inputs *browserIntelligenceInputs, window BrowserFindingWindow, observations []protocol.BrowserDocumentObservation, captured, incomplete bool, dropped int64) *BrowserIntelligenceResult {
	policy := inputs.policy
	result := &BrowserIntelligenceResult{SchemaVersion: browserFindingsSchemaVersion, RuleSet: browserFindingsRuleSet, InputFingerprint: inputs.fingerprint, RunID: inputs.runID, Policy: inputs.ref, Window: window,
		Findings: []BrowserFinding{}, Counts: map[string]int{"active": 0, "suppressed": 0, "unscored": 0},
		Notes: []string{
			"Only qualified final observations of main-frame navigations are judged; child frames and back/forward-cache restores are reported as quality, never scored.",
			"The observed value is the nearest-rank p75 of the qualified final observations' values.",
			"Thresholds and weights come from the policy revision named here; findings are not compared with a baseline (baseline anomalies are a separate rule set).",
		}}
	windowKey := window.Start.Format(time.RFC3339Nano) + "/" + window.End.Format(time.RFC3339Nano)
	add := func(finding BrowserFinding) {
		cohortKey := ""
		if finding.Cohort != nil {
			cohortKey = finding.Cohort.key()
		}
		finding.RuleSet, finding.RuleVersion, finding.Window = browserFindingsRuleSet, 1, window
		finding.Key = browserFindingIdentity(inputs.runID, finding.RuleID, finding.Metric, cohortKey, windowKey)
		finding.ID = browserFindingIdentity(finding.Key, inputs.fingerprint)
		finding.Baseline = BrowserFindingBaseline{Mode: policy.Baseline.Mode, Status: "not_evaluated"}
		result.Findings = append(result.Findings, finding)
		result.Counts[finding.State]++
	}

	// Collection health stays apart from application outcomes.
	if !captured {
		// No document evidence was requested: nothing to judge and no score,
		// never a healthy badge.
	} else if len(observations) == 0 {
		add(BrowserFinding{RuleID: browserFindingRuleCollectionNone, Category: browserFindingCategoryCollection, Applicable: true, State: "active", Severity: "poor",
			Quality: BrowserFindingQuality{Incomplete: incomplete}, Summary: "The run requested document evidence but its archive retained none."})
	} else if incomplete || dropped > 0 {
		lost := float64(dropped)
		add(BrowserFinding{RuleID: browserFindingRuleCollectionLoss, Category: browserFindingCategoryCollection, Applicable: true, State: "active", Severity: "needs_improvement",
			Statistic: "dropped_observations", Observed: &lost, Quality: BrowserFindingQuality{Incomplete: true},
			Summary: fmt.Sprintf("Document evidence is incomplete (%d observations dropped); populations may be short.", dropped)})
	}

	// Each metric's evidence per cohort, from the observations' tuples.
	byMetric := map[string]map[string]*browserCohortEvidence{}
	cohorts := map[string]BrowserFindingCohort{}
	excluded := map[string]int64{}
	for _, observation := range observations {
		if !observation.Valid() {
			continue
		}
		if observation.FrameScope != "main_frame" || observation.LifecyclePhase != "navigation" {
			excluded[observation.FrameScope+"/"+observation.LifecyclePhase]++
			continue
		}
		cohort := BrowserFindingCohort{Engine: observation.Engine, DeviceProfile: observation.DeviceProfile, FrameScope: observation.FrameScope, LifecyclePhase: observation.LifecyclePhase}
		cohorts[cohort.key()] = cohort
		metrics := byMetric[observation.Metric]
		if metrics == nil {
			metrics = map[string]*browserCohortEvidence{}
			byMetric[observation.Metric] = metrics
		}
		evidence := metrics[cohort.key()]
		if evidence == nil {
			evidence = &browserCohortEvidence{cohort: cohort, algorithm: observation.Algorithm, quality: BrowserFindingQuality{Incomplete: incomplete}}
			metrics[cohort.key()] = evidence
		}
		quality := &evidence.quality
		reason := observation.UnavailableReason
		switch observation.State {
		case "provisional":
			quality.Provisional++
		case "censored":
			quality.Censored++
		case "final":
			switch {
			case observation.Qualified && observation.Value != nil:
				quality.EligibleFinal++
				evidence.values = append(evidence.values, *observation.Value)
			case observation.Available:
				quality.UnqualifiedFinal++
				reason = "cohort_unavailable"
				if observation.Cohort != nil && observation.Cohort.UnavailableReason != "" {
					reason = observation.Cohort.UnavailableReason
				}
			default:
				quality.UnavailableFinal++
			}
		}
		if reason != "" && observation.State != "provisional" {
			if quality.ExcludedReasons == nil {
				quality.ExcludedReasons = map[string]int64{}
			}
			quality.ExcludedReasons[reason]++
		}
	}
	if len(excluded) > 0 {
		parts := make([]string, 0, len(excluded))
		for scope, count := range excluded {
			parts = append(parts, fmt.Sprintf("%s %d", scope, count))
		}
		sort.Strings(parts)
		result.Notes = append(result.Notes, "Not judged (frame scope/lifecycle phase): "+strings.Join(parts, ", ")+".")
	}
	cohortKeys := make([]string, 0, len(cohorts))
	for key := range cohorts {
		cohortKeys = append(cohortKeys, key)
	}
	sort.Strings(cohortKeys)

	score := BrowserScore{Version: browserScoreVersion, Components: []BrowserScoreComponent{}}
	for _, metric := range policy.Metrics {
		good, poor := metric.Good, metric.Poor
		required := metric.Weight > 0
		component := BrowserScoreComponent{Metric: metric.Metric, Weight: metric.Weight, Required: required, Good: good, Poor: poor}
		var overall []float64
		for _, key := range cohortKeys {
			cohort := cohorts[key]
			evidence := byMetric[metric.Metric][key]
			finding := BrowserFinding{RuleID: browserFindingRuleThreshold, Category: browserFindingCategoryApplication, Metric: metric.Metric, Unit: browserMetricUnitName(metric.Metric), Cohort: &cohort, Statistic: browserFindingStatistic, Good: &good, Poor: &poor}
			applicable, reason := browserPolicyApplicability(policy, cohort)
			finding.Applicable = applicable
			if evidence != nil {
				finding.Algorithm, finding.Quality = evidence.algorithm, evidence.quality
			} else {
				finding.Quality = BrowserFindingQuality{Incomplete: incomplete}
			}
			switch {
			case !applicable:
				finding.State, finding.Reason = "suppressed", reason
				finding.Summary = fmt.Sprintf("%s on %s/%s is outside the policy's applicability.", strings.ToUpper(metric.Metric), cohort.Engine, cohort.DeviceProfile)
				add(finding)
				continue
			case evidence == nil:
				finding.State, finding.Reason = "unscored", "metric_not_observed"
				finding.Summary = fmt.Sprintf("%s was not observed on %s/%s.", strings.ToUpper(metric.Metric), cohort.Engine, cohort.DeviceProfile)
				add(finding)
				continue
			case evidence.quality.EligibleFinal == 0:
				finding.State, finding.Reason = "unscored", "no_qualified_final_observations"
				finding.Summary = fmt.Sprintf("%s on %s/%s has no qualified final observation.", strings.ToUpper(metric.Metric), cohort.Engine, cohort.DeviceProfile)
				add(finding)
				continue
			}
			overall = append(overall, evidence.values...)
			value := nearestRankP75(evidence.values)
			severity := browserSeverity(value, good, poor)
			if severity == "good" {
				continue
			}
			finding.Observed, finding.Severity, finding.State = &value, severity, "active"
			finding.Evidence.Documents = browserFindingDocuments(inputs.runID, observations, metric.Metric, cohort, good)
			finding.Summary = fmt.Sprintf("%s p75 on %s/%s is %s (%s above %s), from %d qualified final documents.", strings.ToUpper(metric.Metric), cohort.Engine, cohort.DeviceProfile,
				formatBrowserMetricValue(metric.Metric, value), strings.ReplaceAll(severity, "_", " "), formatBrowserMetricValue(metric.Metric, good), evidence.quality.EligibleFinal)
			add(finding)
		}
		component.Samples = int64(len(overall))
		if len(overall) > 0 {
			value := nearestRankP75(overall)
			componentScore := browserComponentScore(value, good, poor)
			component.Observed, component.Score = &value, &componentScore
		}
		if component.Score == nil {
			component.Reason = "no_qualified_final_observations"
		}
		score.Components = append(score.Components, component)
	}
	totalWeight, weighted := 0.0, 0.0
	for index := range score.Components {
		component := &score.Components[index]
		if !component.Required {
			continue
		}
		score.Coverage.Required++
		totalWeight += component.Weight
		if component.Score == nil {
			score.Missing = append(score.Missing, component.Metric)
			continue
		}
		score.Coverage.Observed++
		weighted += component.Weight * *component.Score
	}
	for index := range score.Components {
		component := &score.Components[index]
		if component.Required && component.Score != nil && totalWeight > 0 {
			contribution := component.Weight * *component.Score / totalWeight
			component.Contribution = &contribution
		}
	}
	switch {
	case !captured || len(observations) == 0:
		score.Severity, score.UnscoredReason = "unscored", "no_document_evidence"
	case len(score.Missing) > 0:
		score.Severity, score.UnscoredReason = "unscored", "missing_required_metric"
	case totalWeight <= 0:
		score.Severity, score.UnscoredReason = "unscored", "no_required_metric"
	default:
		overall := weighted / totalWeight
		score.Score, score.Severity = &overall, browserScoreSeverity(overall)
	}
	result.Score = score
	return result
}

// browserFindingDocuments lists the worst qualified final documents of a
// metric in a cohort that crossed good, as archive evidence links.
func browserFindingDocuments(runID string, observations []protocol.BrowserDocumentObservation, metric string, cohort BrowserFindingCohort, good float64) []BrowserFindingDocument {
	var documents []BrowserFindingDocument
	for _, observation := range observations {
		if observation.Metric != metric || observation.State != "final" || !observation.Qualified || observation.Value == nil || *observation.Value <= good ||
			observation.Engine != cohort.Engine || observation.DeviceProfile != cohort.DeviceProfile || observation.FrameScope != cohort.FrameScope || observation.LifecyclePhase != cohort.LifecyclePhase {
			continue
		}
		query := url.Values{"document_id": {observation.DocumentID}, "source_generation": {observation.SourceGeneration}}
		documents = append(documents, BrowserFindingDocument{DocumentID: observation.DocumentID, SourceGeneration: observation.SourceGeneration, Value: *observation.Value,
			Resources: "/api/v1/tests/" + url.PathEscape(runID) + "/archive/browser-resources?" + query.Encode()})
	}
	sort.Slice(documents, func(i, j int) bool {
		if documents[i].Value != documents[j].Value {
			return documents[i].Value > documents[j].Value
		}
		return documents[i].DocumentID < documents[j].DocumentID
	})
	if len(documents) > maxBrowserFindingEvidence {
		documents = documents[:maxBrowserFindingEvidence]
	}
	return documents
}

// nearestRankP75 is the nearest-rank 75th percentile of values.
func nearestRankP75(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := int(math.Ceil(0.75 * float64(len(sorted))))
	return sorted[max(rank, 1)-1]
}

// browserIntelligenceSubject is the evaluation's indexed subject: the
// canonical run ID for an evaluation under the run's pinned policy, and the
// run ID marked requested for one under a named revision, so the run's own
// result is found by index and a named revision never stands in for it.
func browserIntelligenceSubject(s *Server, projectID string, raw json.RawMessage) string {
	var request BrowserIntelligenceRequest
	if json.Unmarshal(raw, &request) != nil || s == nil || s.runner == nil {
		return ""
	}
	test, ok := s.runner.GetTestSummary(strings.TrimSpace(request.Run))
	if !ok || test == nil {
		return ""
	}
	if request.Policy != nil {
		return test.ID + browserRequestedSubjectSuffix
	}
	return test.ID
}

const browserRequestedSubjectSuffix = "#requested"

func browserMetricUnitName(metric string) string {
	if metric == "cls" {
		return "score"
	}
	return "ms"
}

func formatBrowserMetricValue(metric string, value float64) string {
	if metric == "cls" {
		return fmt.Sprintf("%.3f", value)
	}
	return fmt.Sprintf("%.0f ms", value)
}

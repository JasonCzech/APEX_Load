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
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Browser intelligence findings and qualified scores (E1/BI-A3). A run's archived
// final document metrics are judged against a versioned browser_policy as a
// browser_intelligence evaluation job: findings (browser_findings.v2) and a
// weighted score (browser_score.v1). Only qualified final observations of a
// main-frame navigation count; everything else is reported as quality. The
// job runs automatically when a run that pinned a browser policy finalizes
// with document evidence, when its root-cause analysis completes, and on demand.

const (
	browserIntelligenceKind               = "browser_intelligence"
	browserFindingsRuleSet                = "browser_findings.v2"
	browserScoreVersion                   = "browser_score.v1"
	browserFindingsSchemaVersion          = "apexload.browser_findings.v2"
	browserIntelligenceDeadline           = 5 * time.Minute
	maxBrowserFindingEvidence             = 5
	browserFindingCandidateRejectionLimit = 16
	browserFindingStatistic               = "p75"
	browserFindingRuleThreshold           = "threshold"
	browserFindingRuleCollectionLoss      = "collection.incomplete"
	browserFindingRuleCollectionNone      = "collection.absent"
	browserFindingCategoryApplication     = "application_outcome"
	browserFindingCategoryCollection      = "collection_health"
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

// BrowserFindingBaseline records the evaluated baseline verdict and immutable
// provenance for a finding under browser_findings.v2.
type BrowserFindingBaseline struct {
	Mode                   string                          `json:"mode"`
	Status                 string                          `json:"status"`
	Reason                 string                          `json:"reason,omitempty"`
	Detail                 string                          `json:"detail,omitempty"`
	AnalysisID             string                          `json:"analysis_id,omitempty"`
	BaselineRunID          string                          `json:"baseline_run_id,omitempty"`
	BaselineArchiveSHA256  string                          `json:"baseline_archive_sha256,omitempty"`
	SelectionReceiptSHA256 string                          `json:"selection_receipt_sha256,omitempty"`
	SelectionStatus        string                          `json:"selection_status,omitempty"`
	RejectedCandidates     []rca.BrowserCandidateRejection `json:"rejected_candidates,omitempty"`
	PolicyPins             []RunPolicyPin                  `json:"policy_pins,omitempty"`
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
	Provisional      bool                 `json:"provisional,omitempty"`
	LiveTruncated    bool                 `json:"live_truncated,omitempty"`
	Policy           BrowserPolicyRef     `json:"policy"`
	PolicyPins       []RunPolicyPin       `json:"policy_pins,omitempty"`
	Window           BrowserFindingWindow `json:"window"`
	Score            BrowserScore         `json:"score"`
	Findings         []BrowserFinding     `json:"findings"`
	Counts           map[string]int       `json:"counts"`
	Notes            []string             `json:"notes"`
}

type browserIntelligenceInputs struct {
	request                BrowserIntelligenceRequest
	projectID              string
	runID                  string
	record                 *storage.RunArchiveRecord
	policy                 BrowserPolicyDefinition
	ref                    BrowserPolicyRef
	policyPins             []RunPolicyPin
	rcaAnalysisID          string
	rcaRevision            int64
	baselineRunID          string
	baselineArchiveSHA256  string
	selectionReceiptSHA256 string
	browserSelection       *rca.BrowserBaselineSelection
	browserBaseline        *rca.BrowserBaseline
	baselineQualification  string
	baselineReasons        []string
	baselineMismatchReason string
	provisional            bool
	liveTruncated          bool
	fingerprint            string
	preRCAFingerprint      string
}

func browserIntelligencePolicyPins(manifest *RunArchiveManifest) []RunPolicyPin {
	if manifest == nil || len(manifest.Provenance.PolicyPins) == 0 {
		return nil
	}
	var pins []RunPolicyPin
	for _, pin := range manifest.Provenance.PolicyPins {
		if pin.Kind == storage.AnalysisDefinitionKindBrowserPolicy || pin.Kind == storage.AnalysisDefinitionKindJourneyPolicy {
			pins = append(pins, pin)
		}
	}
	sort.Slice(pins, func(i, j int) bool {
		if pins[i].Kind != pins[j].Kind {
			return pins[i].Kind < pins[j].Kind
		}
		if pins[i].ID != pins[j].ID {
			return pins[i].ID < pins[j].ID
		}
		return pins[i].Revision < pins[j].Revision
	})
	return pins
}

func rcaBrowserBaselineMatchesPolicy(manifest *RunArchiveManifest, payload rcaAnalysisPayload, ref BrowserPolicyRef) bool {
	if payload.Assessment == nil || ref.SHA256 == "" {
		return false
	}
	if payload.Assessment.BrowserBaseline != nil {
		return payload.Assessment.BrowserBaseline.PolicySHA256 != "" && payload.Assessment.BrowserBaseline.PolicySHA256 == ref.SHA256
	}
	if manifest == nil {
		return false
	}
	for _, pin := range manifest.Provenance.PolicyPins {
		if pin.Kind == storage.AnalysisDefinitionKindBrowserPolicy {
			return pin.SHA256 == ref.SHA256
		}
	}
	return false
}

func rcaBrowserBaselineMatchesPolicyDefinition(manifest *RunArchiveManifest, payload rcaAnalysisPayload, ref BrowserPolicyRef, policy BrowserPolicyDefinition) bool {
	if !rcaBrowserBaselineMatchesPolicy(manifest, payload, ref) {
		return false
	}
	switch policy.Baseline.Mode {
	case "latest_compatible":
		if payload.BrowserSelection == nil || strings.TrimSpace(payload.Inputs.BrowserSelection) == "" {
			return false
		}
		return strings.TrimSpace(payload.Request.BaselineTestID) == strings.TrimSpace(payload.BrowserSelection.SelectedTestID)
	case "pinned_run":
		if strings.TrimSpace(payload.Request.BaselineTestID) != strings.TrimSpace(policy.Baseline.RunID) {
			return false
		}
		if payload.Assessment != nil && payload.Assessment.BrowserBaseline != nil {
			for _, lim := range payload.Assessment.BrowserBaseline.Limitations {
				if lim.Code == rca.BrowserBaselinePolicyPinnedRun {
					return false
				}
			}
		}
	}
	return true
}

type completedRCAAnalysesReader interface {
	CompletedRCAAnalyses(project, test string) ([]*storage.RCAAnalysis, error)
}

func rcaCoverageBaselineQualification(coverages []rca.Coverage) (string, []string) {
	for _, c := range coverages {
		if c.Evidence != "baseline" {
			continue
		}
		head, tail, hasColon := strings.Cut(c.Detail, ":")
		status := strings.TrimSpace(head)
		var reasons []string
		if hasColon {
			for _, part := range strings.Split(tail, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					reasons = append(reasons, trimmed)
				}
			}
		}
		if status == rca.BaselineUnqualified || status == rca.BaselinePartial || status == rca.BaselineQualified {
			return status, reasons
		}
	}
	return "", nil
}

func computeBrowserIntelligenceFingerprint(projectID, runID, manifestSHA256 string, ref BrowserPolicyRef, pins []RunPolicyPin, rcaAnalysisID string, rcaRevision int64, baselineRunID, baselineArchiveSHA256, selectionReceiptSHA256, baselineMismatchReason string) string {
	value, _ := json.Marshal(struct {
		RuleSet                string         `json:"rule_set"`
		Score                  string         `json:"score"`
		Project                string         `json:"project"`
		Run                    string         `json:"run"`
		ManifestSHA256         string         `json:"manifest_sha256"`
		PolicyID               string         `json:"policy_id"`
		Revision               int64          `json:"revision"`
		PolicySHA              string         `json:"policy_sha256"`
		PolicyPins             []RunPolicyPin `json:"policy_pins,omitempty"`
		RCAAnalysisID          string         `json:"rca_analysis_id,omitempty"`
		RCARevision            int64          `json:"rca_revision,omitempty"`
		BaselineRunID          string         `json:"baseline_run_id,omitempty"`
		BaselineArchiveSHA256  string         `json:"baseline_archive_sha256,omitempty"`
		SelectionReceiptSHA256 string         `json:"selection_receipt_sha256,omitempty"`
		BaselineMismatchReason string         `json:"baseline_mismatch_reason,omitempty"`
	}{
		RuleSet:                browserFindingsRuleSet,
		Score:                  browserScoreVersion,
		Project:                projectID,
		Run:                    runID,
		ManifestSHA256:         strings.ToLower(strings.TrimSpace(manifestSHA256)),
		PolicyID:               ref.ID,
		Revision:               ref.Revision,
		PolicySHA:              ref.SHA256,
		PolicyPins:             pins,
		RCAAnalysisID:          rcaAnalysisID,
		RCARevision:            rcaRevision,
		BaselineRunID:          baselineRunID,
		BaselineArchiveSHA256:  baselineArchiveSHA256,
		SelectionReceiptSHA256: selectionReceiptSHA256,
		BaselineMismatchReason: baselineMismatchReason,
	})
	fingerprint := sha256.Sum256(value)
	return hex.EncodeToString(fingerprint[:])
}

// prepareBrowserIntelligence validates a request and resolves its inputs; the
// fingerprint covers the rule set, the score version, the policy revision's
// digest, the run's finalized manifest digest, the run's policy pins, and the
// matched root-cause baseline provenance.
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
		return nil, evaluationUnavailable(errors.New("runs are unavailable"))
	}
	test, ok := s.runner.GetTestSummary(runID)
	if !ok || test == nil || normalizeProjectID(test.ProjectID) != projectID {
		return nil, evaluationNotFound(fmt.Errorf("run %s is not in this project", runID))
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, test.ID)
	if !ok || record == nil || strings.TrimSpace(record.ManifestSHA256) == "" {
		return nil, evaluationConflict(fmt.Errorf("run %s has no finalized archive", runID))
	}
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		return nil, evaluationUnavailable(errors.New("analysis policies are unavailable"))
	}
	// Like every archive reader, hold a materialization lease while reading:
	// an object-backed archive's local source may have been retired.
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, evaluationUnavailable(fmt.Errorf("run %s archive: %w", runID, err))
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	lease.Release()
	if err != nil {
		return nil, evaluationUnavailable(fmt.Errorf("run %s manifest: %w", runID, err))
	}
	inputs := &browserIntelligenceInputs{
		request:    request,
		projectID:  projectID,
		runID:      test.ID,
		record:     record,
		policyPins: browserIntelligencePolicyPins(manifest),
	}
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
		return nil, evaluationPolicyLookupError(err, "policy is not a browser_policy revision of this project")
	}
	digest := sha256.Sum256(definition.Definition)
	sum := hex.EncodeToString(digest[:])
	if pin != nil && pin.SHA256 != sum {
		return nil, evaluationConflict(errors.New("the pinned browser policy revision no longer matches its pin"))
	}
	if _, err := decodeAnalysisPolicy(definition.Kind, definition.Definition); err != nil {
		return nil, fmt.Errorf("policy revision is invalid: %w", err)
	}
	if err := json.Unmarshal(definition.Definition, &inputs.policy); err != nil {
		return nil, fmt.Errorf("policy revision is invalid: %w", err)
	}
	inputs.ref = BrowserPolicyRef{ID: definition.ID, Name: definition.Name, Revision: definition.Revision, SHA256: sum, Source: source}
	preRCAMismatch := ""
	if inputs.policy.Baseline.Mode != "" && inputs.policy.Baseline.Mode != "none" {
		preRCAMismatch = "rca_not_completed"
	}
	inputs.preRCAFingerprint = computeBrowserIntelligenceFingerprint(projectID, test.ID, record.ManifestSHA256, inputs.ref, inputs.policyPins, "", 0, "", "", "", preRCAMismatch)

	archiveDigest, archiveDigestErr := rcaEvidenceDigest(manifest)
	if rcaStore, ok := s.store.(storage.RCAStore); ok && archiveDigestErr == nil {
		var candidates []*storage.RCAAnalysis
		if reader, ok := rcaStore.(completedRCAAnalysesReader); ok {
			candidates, _ = reader.CompletedRCAAnalyses(projectID, test.ID)
		}
		if len(candidates) == 0 {
			if latest, rcaErr := rcaStore.LatestCompletedRCAAnalysis(projectID, test.ID); rcaErr == nil && latest != nil {
				candidates = []*storage.RCAAnalysis{latest}
			}
		}
		if len(candidates) > 0 {
			var matched *storage.RCAAnalysis
			var matchedPayload rcaAnalysisPayload
			mismatchReason := preRCAMismatch
			for _, cand := range candidates {
				if cand == nil {
					continue
				}
				payload := rcaAnalysisPayloadOf(cand)
				switch {
				case payload.Assessment == nil:
					if mismatchReason == preRCAMismatch {
						mismatchReason = preRCAMismatch
					}
				case payload.Inputs.Archive != archiveDigest:
					if mismatchReason == preRCAMismatch {
						mismatchReason = "rca_archive_mismatch"
					}
				case !rcaBrowserBaselineMatchesPolicyDefinition(manifest, payload, inputs.ref, inputs.policy):
					if mismatchReason == preRCAMismatch {
						mismatchReason = "rca_policy_mismatch"
					}
				default:
					matched = cand
					matchedPayload = payload
				}
				if matched != nil {
					break
				}
			}
			if matched != nil {
				inputs.rcaAnalysisID = matched.ID
				inputs.rcaRevision = matched.Revision
				inputs.selectionReceiptSHA256 = matchedPayload.Inputs.BrowserSelection
				inputs.browserSelection = matchedPayload.BrowserSelection
				inputs.browserBaseline = matchedPayload.Assessment.BrowserBaseline
				if matchedPayload.Assessment.BrowserBaseline != nil && matchedPayload.Assessment.BrowserBaseline.Baseline.TestID != "" {
					inputs.baselineRunID = matchedPayload.Assessment.BrowserBaseline.Baseline.TestID
					inputs.baselineQualification = matchedPayload.Assessment.BrowserBaseline.Baseline.Qualification
					inputs.baselineReasons = slices.Clone(matchedPayload.Assessment.BrowserBaseline.Baseline.Reasons)
				} else if matchedPayload.Request.BaselineTestID != "" {
					inputs.baselineRunID = matchedPayload.Request.BaselineTestID
					inputs.baselineQualification, inputs.baselineReasons = rcaCoverageBaselineQualification(matchedPayload.Assessment.Coverage)
				}
				if inputs.baselineRunID != "" {
					inputs.baselineArchiveSHA256 = matchedPayload.Inputs.Baseline
				}
			} else {
				inputs.baselineMismatchReason = mismatchReason
			}
		} else {
			inputs.baselineMismatchReason = preRCAMismatch
		}
	} else {
		inputs.baselineMismatchReason = preRCAMismatch
	}
	inputs.fingerprint = computeBrowserIntelligenceFingerprint(
		projectID, test.ID, record.ManifestSHA256, inputs.ref, inputs.policyPins,
		inputs.rcaAnalysisID, inputs.rcaRevision, inputs.baselineRunID,
		inputs.baselineArchiveSHA256, inputs.selectionReceiptSHA256, inputs.baselineMismatchReason,
	)
	return inputs, nil
}

func runBrowserIntelligence(ctx context.Context, s *Server, job *storage.EvaluationJob) (any, error) {
	inputs, err := prepareBrowserIntelligence(ctx, s, job.ProjectID, json.RawMessage(job.Request))
	if err != nil {
		return nil, err
	}
	if inputs.fingerprint != job.InputFingerprint {
		if job.InputFingerprint != "" && job.InputFingerprint == inputs.preRCAFingerprint {
			inputs.rcaAnalysisID, inputs.rcaRevision = "", 0
			inputs.baselineRunID, inputs.baselineArchiveSHA256, inputs.selectionReceiptSHA256 = "", "", ""
			inputs.browserSelection, inputs.browserBaseline = nil, nil
			inputs.baselineQualification, inputs.baselineReasons = "", nil
			if inputs.policy.Baseline.Mode != "" && inputs.policy.Baseline.Mode != "none" {
				inputs.baselineMismatchReason = "rca_not_completed"
			} else {
				inputs.baselineMismatchReason = ""
			}
			inputs.fingerprint = job.InputFingerprint
		} else {
			return nil, errors.New("the run archive or the policy revision changed after this evaluation was queued; queue a new evaluation")
		}
	}
	result, err := evaluateArchiveBrowserFindings(ctx, inputs)
	if err != nil {
		// Never box a nil *BrowserIntelligenceResult into the job's result.
		return nil, err
	}
	return result, nil
}

// evaluateArchiveBrowserFindings judges the finalized archive's retained
// document measurements (each measurement once, at its latest revision)
// under the inputs' policy revision.
func evaluateArchiveBrowserFindings(ctx context.Context, inputs *browserIntelligenceInputs) (*BrowserIntelligenceResult, error) {
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
	window := BrowserFindingWindow{Start: manifest.CreatedAt.UTC(), End: archiveExecutionCompletedAt(manifest).UTC()}
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

// evaluateBrowserFindings applies browser_findings.v2 and browser_score.v1.
func evaluateBrowserFindings(inputs *browserIntelligenceInputs, window BrowserFindingWindow, observations []protocol.BrowserDocumentObservation, captured, incomplete bool, dropped int64) *BrowserIntelligenceResult {
	policy := inputs.policy
	result := &BrowserIntelligenceResult{
		SchemaVersion:    browserFindingsSchemaVersion,
		RuleSet:          browserFindingsRuleSet,
		InputFingerprint: inputs.fingerprint,
		RunID:            inputs.runID,
		Policy:           inputs.ref,
		PolicyPins:       cloneRunPolicyPins(inputs.policyPins),
		Window:           window,
		Findings:         []BrowserFinding{},
		Counts:           map[string]int{"active": 0, "suppressed": 0, "unscored": 0},
		Notes: []string{
			"Only qualified final observations of main-frame navigations are judged; child frames and back/forward-cache restores are reported as quality, never scored.",
			"The observed value is the nearest-rank p75 of the qualified final observations' values.",
			"Thresholds and weights come from the policy revision named here; baseline status and provenance come from the run's completed root-cause analysis when its archive and policy digests match.",
		},
	}
	windowKey := window.Start.Format(time.RFC3339Nano) + "/" + window.End.Format(time.RFC3339Nano)
	add := func(finding BrowserFinding) {
		cohortKey := ""
		if finding.Cohort != nil {
			cohortKey = finding.Cohort.key()
		}
		finding.RuleSet, finding.RuleVersion, finding.Window = browserFindingsRuleSet, 2, window
		finding.Key = browserFindingIdentity(inputs.runID, finding.RuleID, finding.Metric, cohortKey, windowKey)
		finding.ID = browserFindingIdentity(finding.Key, inputs.fingerprint)
		finding.Baseline = evaluateFindingBaseline(inputs, finding)
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
				evaluatedBaseline := evaluateFindingBaseline(inputs, finding)
				if evaluatedBaseline.Status != "regressed" {
					continue
				}
				finding.Observed, finding.Severity, finding.State = &value, "needs_improvement", "active"
				finding.Evidence.Documents = browserFindingDocuments(inputs.runID, observations, metric.Metric, cohort, 0)
				finding.Summary = fmt.Sprintf("%s p75 on %s/%s is %s (regressed against baseline %s), from %d qualified final documents.", strings.ToUpper(metric.Metric), cohort.Engine, cohort.DeviceProfile,
					formatBrowserMetricValue(metric.Metric, value), evaluatedBaseline.BaselineRunID, evidence.quality.EligibleFinal)
				add(finding)
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

const browserRequestedSubjectSuffix = storage.EvaluationSubjectRequestedSuffix

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

func boundedBrowserCandidateRejections(rejected []rca.BrowserCandidateRejection) []rca.BrowserCandidateRejection {
	var out []rca.BrowserCandidateRejection
	for _, r := range rejected {
		if len(r.Reasons) == 1 && r.Reasons[0] == "self_baseline" {
			continue
		}
		out = append(out, rca.BrowserCandidateRejection{
			TestID:  r.TestID,
			Digest:  r.Digest,
			Reasons: slices.Clone(r.Reasons),
		})
		if len(out) >= browserFindingCandidateRejectionLimit {
			break
		}
	}
	return out
}

func firstSpecificSelectionReason(reasons []string, fallback string) string {
	for _, reason := range reasons {
		trimmed := strings.TrimSpace(reason)
		if trimmed != "" && trimmed != "current_population_not_comparable" && trimmed != "self_baseline" {
			return trimmed
		}
	}
	for _, reason := range reasons {
		if trimmed := strings.TrimSpace(reason); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func isInsufficientBaselineReason(reason string) bool {
	switch reason {
	case rca.BrowserBaselineInsufficient,
		rca.BrowserBaselineZeroVariance,
		rca.BrowserBaselineNoBaselineCohort,
		rca.BrowserBaselineNoSameLoadWindow,
		rca.BrowserBaselineEvidenceUnavailable,
		rca.BrowserBaselineRunEvidenceIncomplete,
		rca.BrowserBaselineRunEvidenceUnavailable,
		rca.BrowserBaselineLevelsUnavailable,
		rca.BrowserBaselineNoEffectFloor,
		rca.BrowserBaselineOutsideSteadyLoad,
		rca.BrowserBaselineResultsTruncated,
		"metric_not_observed",
		"no_qualified_final_observations":
		return true
	}
	return false
}

func findingBaselineStatusRank(status string) int {
	switch status {
	case "regressed":
		return 0
	case "unqualified":
		return 1
	case "insufficient_evidence":
		return 2
	case "within_baseline":
		return 3
	case "not_selected":
		return 4
	case "not_evaluated":
		return 5
	default:
		return 6
	}
}

func baselineResultMatchesFinding(r rca.BrowserBaselineResult, metric string, cohort *BrowserFindingCohort) bool {
	if cohort == nil || r.Metric != metric {
		return false
	}
	if r.Engine != "" || r.DeviceProfile != "" || r.FrameScope != "" || r.LifecyclePhase != "" {
		return r.Engine == cohort.Engine &&
			r.DeviceProfile == cohort.DeviceProfile &&
			r.FrameScope == cohort.FrameScope &&
			r.LifecyclePhase == cohort.LifecyclePhase
	}
	if len(r.Evidence) == 0 {
		return false
	}
	_, fragment, ok := strings.Cut(r.Evidence[0].Locator, "#")
	if !ok {
		return false
	}
	parts := strings.Split(fragment, " · ")
	if len(parts) < 5 {
		return false
	}
	profile := strings.TrimPrefix(parts[2], "profile=")
	return parts[0] == metric &&
		parts[1] == cohort.Engine &&
		profile == cohort.DeviceProfile &&
		parts[3] == cohort.FrameScope &&
		parts[4] == cohort.LifecyclePhase
}

func classifyBrowserBaselineResult(r rca.BrowserBaselineResult) (status, reason, detail string) {
	switch r.State {
	case rca.BrowserBaselineAnomaly:
		if r.Direction != rca.DirectionDecrease {
			return "regressed", "", fmt.Sprintf("level %s: current %s vs baseline mean %s (z=%.2f)", r.Level, formatBrowserMetricValue(r.Metric, r.Current), formatBrowserMetricValue(r.Metric, r.BaselineMean), r.Z)
		}
		return "within_baseline", "", fmt.Sprintf("level %s: current %s vs baseline mean %s (z=%.2f)", r.Level, formatBrowserMetricValue(r.Metric, r.Current), formatBrowserMetricValue(r.Metric, r.BaselineMean), r.Z)
	case rca.BrowserBaselineWithinRange:
		return "within_baseline", "", fmt.Sprintf("level %s: current %s within [%s, %s]", r.Level, formatBrowserMetricValue(r.Metric, r.Current), formatBrowserMetricValue(r.Metric, r.ExpectedLow), formatBrowserMetricValue(r.Metric, r.ExpectedHigh))
	default:
		if isInsufficientBaselineReason(r.Reason) {
			return "insufficient_evidence", r.Reason, r.Detail
		}
		return "unqualified", firstNonEmpty(r.Reason, "unqualified"), r.Detail
	}
}

func evaluateFindingBaseline(inputs *browserIntelligenceInputs, finding BrowserFinding) BrowserFindingBaseline {
	mode := inputs.policy.Baseline.Mode
	if mode == "" {
		mode = "none"
	}
	out := BrowserFindingBaseline{
		Mode:       mode,
		PolicyPins: cloneRunPolicyPins(inputs.policyPins),
	}
	if finding.Category == browserFindingCategoryCollection {
		out.Status = "not_applicable"
		return out
	}
	if inputs.provisional {
		out.Status = "not_evaluated_live"
		out.Reason = "not_evaluated_live"
		out.BaselineRunID = inputs.baselineRunID
		out.BaselineArchiveSHA256 = inputs.baselineArchiveSHA256
		return out
	}
	if inputs.rcaAnalysisID != "" {
		out.AnalysisID = inputs.rcaAnalysisID
		out.SelectionReceiptSHA256 = inputs.selectionReceiptSHA256
		out.BaselineRunID = inputs.baselineRunID
		out.BaselineArchiveSHA256 = inputs.baselineArchiveSHA256
		if inputs.browserSelection != nil {
			out.SelectionStatus = inputs.browserSelection.Status
			out.RejectedCandidates = boundedBrowserCandidateRejections(inputs.browserSelection.Rejected)
		}
	}
	if mode == "none" {
		out.Status = "not_selected"
		out.Reason = rca.BrowserBaselinePolicyOff
		return out
	}
	if !finding.Applicable {
		out.Status = "not_applicable"
		out.Reason = finding.Reason
		return out
	}
	if inputs.rcaAnalysisID == "" {
		out.Status = "not_evaluated"
		out.Reason = firstNonEmpty(inputs.baselineMismatchReason, "rca_not_completed")
		return out
	}
	if inputs.baselineRunID == "" {
		if inputs.browserSelection != nil {
			switch inputs.browserSelection.Status {
			case "current_unqualified":
				out.Status = "unqualified"
				out.Reason = firstSpecificSelectionReason(inputs.browserSelection.Reasons, "current_unqualified")
				if len(inputs.browserSelection.Reasons) > 0 {
					out.Detail = "current run is not qualified for baseline comparison: " + strings.Join(inputs.browserSelection.Reasons, ", ")
				}
				return out
			case "no_compatible_candidate":
				out.Status = "not_selected"
				if len(out.RejectedCandidates) > 0 {
					first := out.RejectedCandidates[0]
					out.Reason = firstSpecificSelectionReason(first.Reasons, rca.BrowserBaselineNoBaselineRun)
					out.Detail = fmt.Sprintf("candidate %s rejected: %s", first.TestID, strings.Join(first.Reasons, ", "))
				} else {
					out.Reason = rca.BrowserBaselineNoBaselineRun
					out.Detail = "no compatible baseline candidate was found in the project"
				}
				return out
			default:
				if inputs.browserSelection.Status != "" && inputs.browserSelection.Status != "selected" {
					out.Status = "unqualified"
					out.Reason = inputs.browserSelection.Status
					return out
				}
			}
		}
		out.Status = "not_selected"
		out.Reason = rca.BrowserBaselineNoBaselineRun
		return out
	}
	if inputs.baselineQualification == rca.BaselineUnqualified || (inputs.browserBaseline != nil && inputs.browserBaseline.Baseline.Qualification == rca.BaselineUnqualified) {
		reasons := inputs.baselineReasons
		if len(reasons) == 0 && inputs.browserBaseline != nil {
			reasons = inputs.browserBaseline.Baseline.Reasons
		}
		out.Status = "unqualified"
		out.Reason = firstSpecificSelectionReason(reasons, rca.BrowserBaselineNoBaselineRun)
		if len(reasons) > 0 {
			out.Detail = "baseline run " + inputs.baselineRunID + " is not workload-qualified: " + strings.Join(reasons, ", ")
		}
		return out
	}
	if inputs.browserBaseline == nil {
		out.Status = "insufficient_evidence"
		out.Reason = rca.BrowserBaselineRunEvidenceUnavailable
		return out
	}
	type candidateVerdict struct {
		status     string
		reason     string
		detail     string
		absZ       float64
		levelStart time.Time
		id         string
	}
	var verdicts []candidateVerdict
	for _, r := range inputs.browserBaseline.Results {
		if !baselineResultMatchesFinding(r, finding.Metric, finding.Cohort) {
			continue
		}
		status, reason, detail := classifyBrowserBaselineResult(r)
		verdicts = append(verdicts, candidateVerdict{
			status:     status,
			reason:     reason,
			detail:     detail,
			absZ:       math.Abs(r.Z),
			levelStart: r.LevelStart,
			id:         r.ID,
		})
	}
	if len(verdicts) > 0 {
		sort.SliceStable(verdicts, func(i, j int) bool {
			a, b := verdicts[i], verdicts[j]
			if findingBaselineStatusRank(a.status) != findingBaselineStatusRank(b.status) {
				return findingBaselineStatusRank(a.status) < findingBaselineStatusRank(b.status)
			}
			if a.absZ != b.absZ {
				return a.absZ > b.absZ
			}
			if a.reason != b.reason {
				return a.reason < b.reason
			}
			if !a.levelStart.Equal(b.levelStart) {
				return a.levelStart.Before(b.levelStart)
			}
			return a.id < b.id
		})
		best := verdicts[0]
		out.Status, out.Reason, out.Detail = best.status, best.reason, best.detail
		return out
	}
	if finding.State == "unscored" && isInsufficientBaselineReason(finding.Reason) && inputs.browserBaseline.Status != rca.DetectionUnavailable {
		out.Status = "insufficient_evidence"
		out.Reason = finding.Reason
		return out
	}
	for _, l := range inputs.browserBaseline.Limitations {
		switch l.Code {
		case rca.BrowserBaselinePolicyOff:
			out.Status, out.Reason, out.Detail = "not_selected", l.Code, l.Detail
			return out
		case rca.BrowserBaselineNoBaselineRun:
			out.Status, out.Reason, out.Detail = "not_selected", l.Code, l.Detail
			return out
		case rca.BrowserBaselineLevelsUnavailable, rca.BrowserBaselineRunEvidenceUnavailable, rca.BrowserBaselineRunEvidenceIncomplete, rca.BrowserBaselineEvidenceUnavailable, rca.BrowserBaselineResultsTruncated:
			out.Status, out.Reason, out.Detail = "insufficient_evidence", l.Code, l.Detail
			return out
		default:
			out.Status, out.Reason, out.Detail = "unqualified", l.Code, l.Detail
			return out
		}
	}
	out.Status = "insufficient_evidence"
	out.Reason = firstNonEmpty(finding.Reason, rca.BrowserBaselineOutsideSteadyLoad)
	return out
}

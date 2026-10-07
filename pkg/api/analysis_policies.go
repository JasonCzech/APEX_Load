package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Analysis policies are revisioned project definitions (telemetry budgets and
// acceptance, browser scoring, journeys, derived operations). At most one
// definition per kind and project applies by default; Start pins the exact
// revision of each default into the run's immutable admission receipt.

const analysisPolicyVersion = 1

// RunPolicyPin is one policy revision a run was admitted under. SHA256 hashes
// the stored definition bytes, so a report can prove which rules it applied.
// Applied says whether anything enforced the pinned revision for this run.
// Policies are pinned before their consumers exist, so it is false for now;
// later reports and verdicts must not treat an unapplied pin as enforced.
type RunPolicyPin struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	SHA256   string `json:"sha256"`
	Source   string `json:"source"`
	Applied  bool   `json:"applied"`
}

func cloneRunPolicyPins(pins []RunPolicyPin) []RunPolicyPin {
	if pins == nil {
		return nil
	}
	return append([]RunPolicyPin{}, pins...)
}

// analysisPolicyEnvelope holds the fields common to every policy kind. Each
// kind declares them directly: the strict definition decoder does not follow
// embedded structs.
type analysisPolicyEnvelope struct {
	Version        int    `json:"version"`
	ApplyByDefault bool   `json:"apply_by_default,omitempty"`
	Description    string `json:"description,omitempty"`
}

// maxPolicyDescriptionBytes matches the strict definition decoder's bound on
// every string scalar.
const maxPolicyDescriptionBytes = 256

func (e analysisPolicyEnvelope) validate() error {
	if e.Version != analysisPolicyVersion {
		return fmt.Errorf("policy version must be %d", analysisPolicyVersion)
	}
	if len(e.Description) > maxPolicyDescriptionBytes {
		return fmt.Errorf("policy description exceeds %d bytes", maxPolicyDescriptionBytes)
	}
	return nil
}

// policyAppliesByDefault reads only the envelope flag. Selecting the default
// never depends on the rest of a stored definition still validating, so a
// later, stricter validator cannot block Start through an unrelated policy.
func policyAppliesByDefault(definition json.RawMessage) (bool, error) {
	var envelope struct {
		ApplyByDefault bool `json:"apply_by_default"`
	}
	if err := json.Unmarshal(definition, &envelope); err != nil {
		return false, fmt.Errorf("policy envelope is not a JSON object: %w", err)
	}
	return envelope.ApplyByDefault, nil
}

// TelemetryPolicyDefinition overrides the system telemetry budgets for runs
// in the project. Nil keeps the system default; zero disables that budget.
type TelemetryPolicyDefinition struct {
	Version                int      `json:"version"`
	ApplyByDefault         bool     `json:"apply_by_default,omitempty"`
	Description            string   `json:"description,omitempty"`
	SampleAgeMs            *float64 `json:"sample_age_ms,omitempty"`
	GeneratorLagMs         *float64 `json:"generator_lag_ms,omitempty"`
	TransportLagMs         *float64 `json:"transport_lag_ms,omitempty"`
	ControllerFanoutMs     *float64 `json:"controller_fanout_ms,omitempty"`
	BrowserRenderLagMs     *float64 `json:"browser_render_lag_ms,omitempty"`
	SustainedWindowSeconds *int     `json:"sustained_window_seconds,omitempty"`
}

func (p TelemetryPolicyDefinition) validate() error {
	if err := (analysisPolicyEnvelope{p.Version, p.ApplyByDefault, p.Description}).validate(); err != nil {
		return err
	}
	set := false
	for _, limit := range []*float64{p.SampleAgeMs, p.GeneratorLagMs, p.TransportLagMs, p.ControllerFanoutMs, p.BrowserRenderLagMs} {
		if limit != nil {
			set = true
			if !finiteInRange(*limit, 0, 3_600_000) {
				return errors.New("telemetry budgets must be between 0 and 3600000 ms")
			}
		}
	}
	if p.SustainedWindowSeconds != nil {
		set = true
		if *p.SustainedWindowSeconds < 1 || *p.SustainedWindowSeconds > 3600 {
			return errors.New("sustained_window_seconds must be between 1 and 3600")
		}
	}
	if !set {
		return errors.New("telemetry policy must set at least one budget")
	}
	return nil
}

var telemetryStages = []string{"sample_age", "generator", "transport", "controller_fanout", "browser_render", "end_to_end"}

type TelemetryLagPercentileRule struct {
	Stage      string  `json:"stage"`
	Percentile string  `json:"percentile"`
	MaxMs      float64 `json:"max_ms"`
}

// TelemetryAcceptancePolicyDefinition is a telemetry verdict policy. An empty
// mode is advisory; only fail_ci can change a CI outcome, and unknown required
// evidence is inconclusive, never a pass.
type TelemetryAcceptancePolicyDefinition struct {
	Version                      int                          `json:"version"`
	ApplyByDefault               bool                         `json:"apply_by_default,omitempty"`
	Description                  string                       `json:"description,omitempty"`
	Mode                         string                       `json:"mode"`
	MinWallTimeCoverage          *float64                     `json:"min_wall_time_coverage,omitempty"`
	MaxLostRecords               *int64                       `json:"max_lost_records,omitempty"`
	MaxLossRatio                 *float64                     `json:"max_loss_ratio,omitempty"`
	MaxBreachedKnownTimeFraction *float64                     `json:"max_breached_known_time_fraction,omitempty"`
	StageLagPercentiles          []TelemetryLagPercentileRule `json:"stage_lag_percentiles,omitempty"`
}

// EffectiveMode is the verdict mode; an omitted mode is advisory.
func (p TelemetryAcceptancePolicyDefinition) EffectiveMode() string {
	if p.Mode == "" {
		return "advisory"
	}
	return p.Mode
}

func (p TelemetryAcceptancePolicyDefinition) validate() error {
	if err := (analysisPolicyEnvelope{p.Version, p.ApplyByDefault, p.Description}).validate(); err != nil {
		return err
	}
	if p.Mode != "" && p.Mode != "advisory" && p.Mode != "fail_ci" {
		return errors.New("mode must be advisory (the default) or fail_ci")
	}
	criteria := len(p.StageLagPercentiles)
	for _, fraction := range []*float64{p.MinWallTimeCoverage, p.MaxLossRatio, p.MaxBreachedKnownTimeFraction} {
		if fraction != nil {
			criteria++
			if !finiteInRange(*fraction, 0, 1) {
				return errors.New("coverage, loss ratio and breach fraction must be between 0 and 1")
			}
		}
	}
	if p.MaxLostRecords != nil {
		criteria++
		if *p.MaxLostRecords < 0 {
			return errors.New("max_lost_records must be non-negative")
		}
	}
	if criteria == 0 {
		return errors.New("telemetry acceptance policy must set at least one criterion")
	}
	if len(p.StageLagPercentiles) > 16 {
		return errors.New("at most 16 stage lag percentile rules")
	}
	seen := map[string]bool{}
	for _, rule := range p.StageLagPercentiles {
		if !slices.Contains(telemetryStages, rule.Stage) {
			return fmt.Errorf("unknown telemetry stage %q", rule.Stage)
		}
		if !slices.Contains([]string{"p50", "p95", "p99", "max"}, rule.Percentile) {
			return errors.New("percentile must be p50, p95, p99 or max")
		}
		if !finiteInRange(rule.MaxMs, 0, 3_600_000) || rule.MaxMs == 0 {
			return errors.New("stage lag max_ms must be between 0 and 3600000 and positive")
		}
		key := rule.Stage + "/" + rule.Percentile
		if seen[key] {
			return fmt.Errorf("duplicate stage lag rule %s", key)
		}
		seen[key] = true
	}
	return nil
}

var browserPolicyMetrics = []string{"fcp", "lcp", "cls", "inp"}

type BrowserPolicyMetric struct {
	Metric string  `json:"metric"`
	Good   float64 `json:"good"`
	Poor   float64 `json:"poor"`
	Weight float64 `json:"weight"`
}

type BrowserPolicyApplicability struct {
	Engines        []string `json:"engines,omitempty"`
	DeviceProfiles []string `json:"device_profiles,omitempty"`
}

// BrowserPolicyEffectFloors are the practical-significance floors baseline
// anomalies must clear in addition to the statistical rule. They can only
// raise the minimums: 50 ms and 10% for timings, 0.02 for CLS.
type BrowserPolicyEffectFloors struct {
	TimingMs       *float64 `json:"timing_ms,omitempty"`
	TimingRelative *float64 `json:"timing_relative,omitempty"`
	CLS            *float64 `json:"cls,omitempty"`
}

type BrowserPolicyBaseline struct {
	Mode  string `json:"mode"`
	RunID string `json:"run_id,omitempty"`
}

// BrowserPolicyDefinition scores qualified document metrics. Weights are
// non-negative and documented; a missing required metric prevents an overall
// score rather than renormalizing the others.
type BrowserPolicyDefinition struct {
	Version        int                        `json:"version"`
	ApplyByDefault bool                       `json:"apply_by_default,omitempty"`
	Description    string                     `json:"description,omitempty"`
	Metrics        []BrowserPolicyMetric      `json:"metrics"`
	Applicability  BrowserPolicyApplicability `json:"applicability,omitempty"`
	EffectFloors   BrowserPolicyEffectFloors  `json:"effect_floors,omitempty"`
	Baseline       BrowserPolicyBaseline      `json:"baseline"`
}

func (p BrowserPolicyDefinition) validate() error {
	if err := (analysisPolicyEnvelope{p.Version, p.ApplyByDefault, p.Description}).validate(); err != nil {
		return err
	}
	if len(p.Metrics) == 0 || len(p.Metrics) > len(browserPolicyMetrics) {
		return errors.New("browser policy must list between one and four metrics")
	}
	seen := map[string]bool{}
	total := 0.0
	for _, metric := range p.Metrics {
		if !slices.Contains(browserPolicyMetrics, metric.Metric) || seen[metric.Metric] {
			return fmt.Errorf("browser policy metric %q is unknown or repeated", metric.Metric)
		}
		seen[metric.Metric] = true
		limit := 600_000.0
		if metric.Metric == "cls" {
			limit = 10
		}
		if !finiteInRange(metric.Good, 0, limit) || !finiteInRange(metric.Poor, 0, limit) || !(metric.Good < metric.Poor) {
			return fmt.Errorf("browser policy %s thresholds must satisfy 0 <= good < poor", metric.Metric)
		}
		if !finiteInRange(metric.Weight, 0, 1000) {
			return errors.New("browser policy weights must be between 0 and 1000")
		}
		total += metric.Weight
	}
	if total <= 0 {
		return errors.New("browser policy weights must not all be zero")
	}
	for _, engine := range p.Applicability.Engines {
		if engine != "chromium" && engine != "webkit" {
			return fmt.Errorf("unknown browser engine %q", engine)
		}
	}
	if len(p.Applicability.DeviceProfiles) > 32 {
		return errors.New("at most 32 device profiles")
	}
	for _, profile := range p.Applicability.DeviceProfiles {
		if strings.TrimSpace(profile) == "" || len(profile) > 64 {
			return errors.New("device profiles must be non-empty and at most 64 bytes")
		}
	}
	floors := p.EffectFloors
	if floors.TimingMs != nil && !finiteInRange(*floors.TimingMs, 50, 600_000) {
		return errors.New("effect floor timing_ms must be between 50 and 600000")
	}
	if floors.TimingRelative != nil && !finiteInRange(*floors.TimingRelative, 0.1, 10) {
		return errors.New("effect floor timing_relative must be between 0.1 and 10")
	}
	if floors.CLS != nil && !finiteInRange(*floors.CLS, 0.02, 10) {
		return errors.New("effect floor cls must be between 0.02 and 10")
	}
	switch p.Baseline.Mode {
	case "none", "latest_compatible":
		if p.Baseline.RunID != "" {
			return errors.New("baseline run_id is only valid with mode pinned_run")
		}
	case "pinned_run":
		if strings.TrimSpace(p.Baseline.RunID) == "" || len(p.Baseline.RunID) > 128 {
			return errors.New("pinned_run baseline requires run_id")
		}
	default:
		return errors.New("baseline mode must be none, latest_compatible or pinned_run")
	}
	return nil
}

var journeyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var operationParameterPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// JourneyBoundary names an authored transaction or action.
type JourneyBoundary struct {
	Transaction string `json:"transaction,omitempty"`
	Action      string `json:"action,omitempty"`
}

func (b JourneyBoundary) validate(field string) error {
	if (b.Transaction == "") == (b.Action == "") {
		return fmt.Errorf("journey %s must name exactly one transaction or action", field)
	}
	if len(b.Transaction) > 256 || len(b.Action) > 256 {
		return fmt.Errorf("journey %s name exceeds 256 bytes", field)
	}
	return nil
}

type JourneyDefinition struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Start   JourneyBoundary `json:"start"`
	Success JourneyBoundary `json:"success"`
	Weight  float64         `json:"weight"`
}

// JourneyPolicyDefinition declares journeys by explicit start and success
// boundaries. The weighted failure index counts each failed attempt once.
type JourneyPolicyDefinition struct {
	Version         int                 `json:"version"`
	ApplyByDefault  bool                `json:"apply_by_default,omitempty"`
	Description     string              `json:"description,omitempty"`
	Journeys        []JourneyDefinition `json:"journeys"`
	StepRiskEnabled bool                `json:"step_risk_enabled,omitempty"`
}

func (p JourneyPolicyDefinition) validate() error {
	if err := (analysisPolicyEnvelope{p.Version, p.ApplyByDefault, p.Description}).validate(); err != nil {
		return err
	}
	if len(p.Journeys) == 0 || len(p.Journeys) > 64 {
		return errors.New("journey policy must define between 1 and 64 journeys")
	}
	seen := map[string]bool{}
	for _, journey := range p.Journeys {
		if !journeyIDPattern.MatchString(journey.ID) || seen[journey.ID] {
			return fmt.Errorf("journey id %q must be a unique lowercase slug", journey.ID)
		}
		seen[journey.ID] = true
		if strings.TrimSpace(journey.Name) == "" || len(journey.Name) > 256 {
			return errors.New("journey name must be non-empty and at most 256 bytes")
		}
		if err := journey.Start.validate("start"); err != nil {
			return err
		}
		if err := journey.Success.validate("success"); err != nil {
			return err
		}
		if !finiteInRange(journey.Weight, 0, 1000) {
			return errors.New("journey weights must be between 0 and 1000")
		}
	}
	return nil
}

// DerivedOperationDefinition saves one catalog derivation with bounded
// numeric parameters. It never carries an expression or SQL. The operation
// must be a derivation the metric's catalog entry declares, over an input
// kind both accept, with only that operation's parameters in range.
type DerivedOperationDefinition struct {
	Version        int                `json:"version"`
	ApplyByDefault bool               `json:"apply_by_default,omitempty"`
	Description    string             `json:"description,omitempty"`
	Operation      string             `json:"operation"`
	Metric         string             `json:"metric"`
	Population     string             `json:"population"`
	Parameters     map[string]float64 `json:"parameters,omitempty"`
}

func (p DerivedOperationDefinition) validate() error {
	if err := (analysisPolicyEnvelope{p.Version, p.ApplyByDefault, p.Description}).validate(); err != nil {
		return err
	}
	operation, ok := analysis.LookupOperation(p.Operation)
	if !ok {
		return fmt.Errorf("unknown catalog operation %q", p.Operation)
	}
	metric, ok := analysis.LookupMetric(p.Metric)
	if !ok {
		return fmt.Errorf("unknown catalog metric %q", p.Metric)
	}
	if !slices.Contains(metric.Populations, p.Population) {
		return fmt.Errorf("metric %s has no population %q", p.Metric, p.Population)
	}
	if len(operation.Populations) > 0 && !slices.Contains(operation.Populations, p.Population) {
		return fmt.Errorf("operation %s does not apply to population %q", p.Operation, p.Population)
	}
	if !slices.Contains(metric.Derivations, p.Operation) {
		return fmt.Errorf("metric %s does not declare derivation %q", p.Metric, p.Operation)
	}
	if !slices.ContainsFunc(operation.InputKinds, func(kind string) bool { return slices.Contains(metric.InputKinds, kind) }) {
		return fmt.Errorf("operation %s does not accept the input kinds of metric %s", p.Operation, p.Metric)
	}
	bounds := derivedOperationParameters[p.Operation]
	for name, value := range p.Parameters {
		bound, known := bounds[name]
		if !operationParameterPattern.MatchString(name) || !known {
			return fmt.Errorf("operation %s has no parameter %q", p.Operation, name)
		}
		if !finiteInRange(value, bound.low, bound.high) || (bound.integer && value != math.Trunc(value)) || (bound.exclusiveLow && value == bound.low) {
			return fmt.Errorf("operation parameter %s is out of range", name)
		}
	}
	for name, bound := range bounds {
		if _, present := p.Parameters[name]; bound.required && !present {
			return fmt.Errorf("operation %s requires parameter %s", p.Operation, name)
		}
	}
	return nil
}

type derivedOperationParameter struct {
	low, high         float64
	integer, required bool
	exclusiveLow      bool
}

// derivedOperationParameters are the engine's accepted ranges per derivation
// (analysis.MovingStatistics); baseline deltas take none.
var derivedOperationParameters = map[string]map[string]derivedOperationParameter{
	"baseline_delta":         {},
	"baseline_percent_delta": {},
	"moving_mean":            movingStatisticParameters,
	"moving_stddev":          movingStatisticParameters,
}

var movingStatisticParameters = map[string]derivedOperationParameter{
	"window_buckets":   {low: 2, high: analysis.MaxMovingWindowBuckets, integer: true, required: true},
	"bucket_seconds":   {low: 1, high: 86400, integer: true},
	"minimum_coverage": {low: 0, high: 1, exclusiveLow: true},
}

func finiteInRange(value, low, high float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= low && value <= high
}

// decodeAnalysisPolicy strictly decodes and validates one policy payload and
// returns its common envelope.
func decodeAnalysisPolicy(kind string, data []byte) (analysisPolicyEnvelope, error) {
	type policy interface{ validate() error }
	var target policy
	switch kind {
	case storage.AnalysisDefinitionKindTelemetryPolicy:
		target = &TelemetryPolicyDefinition{}
	case storage.AnalysisDefinitionKindTelemetryAcceptancePolicy:
		target = &TelemetryAcceptancePolicyDefinition{}
	case storage.AnalysisDefinitionKindBrowserPolicy:
		target = &BrowserPolicyDefinition{}
	case storage.AnalysisDefinitionKindJourneyPolicy:
		target = &JourneyPolicyDefinition{}
	case storage.AnalysisDefinitionKindDerivedOperation:
		target = &DerivedOperationDefinition{}
	default:
		return analysisPolicyEnvelope{}, fmt.Errorf("unknown policy kind %q", kind)
	}
	if err := decodeAnalysisDefinitionJSON(data, target); err != nil {
		return analysisPolicyEnvelope{}, err
	}
	if err := target.validate(); err != nil {
		return analysisPolicyEnvelope{}, err
	}
	var envelope analysisPolicyEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return analysisPolicyEnvelope{}, err
	}
	return envelope, nil
}

// AnalysisPolicyRequest is the create/update body. Kind is required on create
// and must match the stored kind on update; Revision is the expected current
// revision on update.
type AnalysisPolicyRequest struct {
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Revision   int64           `json:"revision,omitempty"`
	Definition json.RawMessage `json:"definition"`
}

// analysisPolicyStore resolves the policy store and the request's project.
func (s *Server) analysisPolicyStore(w http.ResponseWriter, r *http.Request) (storage.AnalysisDefinitionStore, string, bool) {
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "analysis policies unavailable")
		return nil, "", false
	}
	return store, normalizeProjectID(r.URL.Query().Get("project_id")), true
}

func (s *Server) handleListAnalysisPolicies(w http.ResponseWriter, r *http.Request) {
	store, project, ok := s.analysisPolicyStore(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if !slices.Contains(storage.AnalysisPolicyKinds, kind) {
		writeError(w, http.StatusBadRequest, "kind must be one of "+strings.Join(storage.AnalysisPolicyKinds, ", "))
		return
	}
	limit, offset := parseLimitOffsetQuery(r, 50)
	live, err := liveAnalysisPolicies(store, project, kind)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot list analysis policies")
		return
	}
	// Deleted policies are dropped before paging, so only the last page is
	// short.
	page := []storage.AnalysisDefinition{}
	if offset < len(live) {
		page = live[offset:min(len(live), offset+min(limit, 100))]
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleGetAnalysisPolicy(w http.ResponseWriter, r *http.Request) {
	store, project, ok := s.analysisPolicyStore(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	revision := int64(0)
	if raw := r.URL.Query().Get("revision"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "revision must be a positive integer")
			return
		}
		revision = parsed
	}
	// A pinned revision stays readable after deletion; the latest of a
	// deleted policy does not.
	in, err := store.GetAnalysisDefinition(project, id, revision)
	if err != nil || !slices.Contains(storage.AnalysisPolicyKinds, in.Kind) || (revision == 0 && in.Deleted) {
		writeError(w, http.StatusNotFound, "analysis policy not found")
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// OpenAPI-Responses: 409, 500
func (s *Server) handleDeleteAnalysisPolicy(w http.ResponseWriter, r *http.Request) {
	store, project, ok := s.analysisPolicyStore(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		writeError(w, http.StatusBadRequest, "revision required")
		return
	}
	current, err := store.GetAnalysisDefinition(project, id, 0)
	if err != nil || !slices.Contains(storage.AnalysisPolicyKinds, current.Kind) || current.Deleted {
		writeError(w, http.StatusNotFound, "analysis policy not found")
		return
	}
	deleted := *current
	deleted.Deleted = true
	saved, err := store.SaveAnalysisDefinition(deleted, revision)
	if writeAnalysisPolicySaveError(w, err) {
		return
	}
	s.recordAuditEvent(r, "analysis_policy.delete", "success", "analysis_policy", id, project, map[string]any{"kind": current.Kind, "revision": saved.Revision})
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleCreateAnalysisPolicy(w http.ResponseWriter, r *http.Request) {
	if saved, ok := s.readAnalysisPolicySave(w, r, ""); ok {
		writeJSON(w, http.StatusCreated, saved)
	}
}

func (s *Server) handleUpdateAnalysisPolicy(w http.ResponseWriter, r *http.Request) {
	if saved, ok := s.readAnalysisPolicySave(w, r, r.PathValue("id")); ok {
		writeJSON(w, http.StatusOK, saved)
	}
}

// readAnalysisPolicySave reads, validates and saves a policy: a new one when
// id is empty, otherwise the next revision of id. It writes every failure
// and leaves the success response to the caller.
//
// OpenAPI-Responses: 409, 500
func (s *Server) readAnalysisPolicySave(w http.ResponseWriter, r *http.Request, id string) (*storage.AnalysisDefinition, bool) {
	store, project, ok := s.analysisPolicyStore(w, r)
	if !ok {
		return nil, false
	}
	create := id == ""
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "analysis policy exceeds request byte limit")
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "cannot read analysis policy")
		return nil, false
	}
	var req AnalysisPolicyRequest
	if err := decodeAnalysisDefinitionJSON(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	expected := int64(0)
	if create {
		if req.Revision != 0 {
			writeError(w, http.StatusBadRequest, "new policy must omit revision")
			return nil, false
		}
		id = uuid.NewString()
	} else {
		current, err := store.GetAnalysisDefinition(project, id, 0)
		if err != nil || current.Deleted || !slices.Contains(storage.AnalysisPolicyKinds, current.Kind) {
			writeError(w, http.StatusNotFound, "analysis policy not found")
			return nil, false
		}
		if req.Kind != current.Kind {
			writeError(w, http.StatusBadRequest, "policy kind cannot change")
			return nil, false
		}
		if req.Revision < 1 {
			writeError(w, http.StatusBadRequest, "revision required")
			return nil, false
		}
		expected = req.Revision
	}
	if !slices.Contains(storage.AnalysisPolicyKinds, req.Kind) {
		writeError(w, http.StatusBadRequest, "kind must be one of "+strings.Join(storage.AnalysisPolicyKinds, ", "))
		return nil, false
	}
	envelope, err := decodeAnalysisPolicy(req.Kind, req.Definition)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	if !s.pinnedBaselineRunInProject(w, req.Kind, req.Definition, project) {
		return nil, false
	}
	definition := storage.AnalysisDefinition{ID: id, ProjectID: project, Kind: req.Kind, Name: strings.TrimSpace(req.Name), Definition: req.Definition}
	var saved *storage.AnalysisDefinition
	if policyStore, exclusive := store.(storage.AnalysisPolicyStore); exclusive {
		// The second-default check runs inside the save transaction, so two
		// concurrent saves cannot both become the default.
		saved, err = policyStore.SaveAnalysisPolicyDefinition(definition, expected, policyAppliesByDefault)
	} else {
		if envelope.ApplyByDefault {
			if other, otherErr := projectDefaultPolicy(store, project, req.Kind); otherErr != nil {
				writeError(w, http.StatusServiceUnavailable, "analysis policies unavailable")
				return nil, false
			} else if other != nil && other.ID != id {
				writeError(w, http.StatusConflict, fmt.Sprintf("policy %q already applies by default for %s", other.Name, req.Kind))
				return nil, false
			}
		}
		saved, err = store.SaveAnalysisDefinition(definition, expected)
	}
	if errors.Is(err, storage.ErrAnalysisPolicyDefaultConflict) {
		writeError(w, http.StatusConflict, fmt.Sprintf("another %s policy already applies by default (%s)", req.Kind, strings.TrimPrefix(err.Error(), storage.ErrAnalysisPolicyDefaultConflict.Error()+": ")))
		return nil, false
	}
	if writeAnalysisPolicySaveError(w, err) {
		return nil, false
	}
	s.recordAuditEvent(r, "analysis_policy.save", "success", "analysis_policy", id, project, map[string]any{"kind": req.Kind, "revision": saved.Revision, "apply_by_default": envelope.ApplyByDefault})
	return saved, true
}

// pinnedBaselineRunInProject refuses a browser policy whose pinned baseline is
// not a run of the policy's project, as saved comparisons refuse foreign runs.
// Consumers check the run again when they read it.
func (s *Server) pinnedBaselineRunInProject(w http.ResponseWriter, kind string, data []byte, project string) bool {
	if kind != storage.AnalysisDefinitionKindBrowserPolicy {
		return true
	}
	var policy BrowserPolicyDefinition
	if json.Unmarshal(data, &policy) != nil || policy.Baseline.Mode != "pinned_run" {
		return true
	}
	run, err := s.store.GetTest(policy.Baseline.RunID)
	if err != nil || run == nil || run.ProjectID != project {
		writeError(w, http.StatusNotFound, "baseline run not found in project")
		return false
	}
	return true
}

// writeAnalysisPolicySaveError maps a policy save failure without exposing
// storage detail.
func writeAnalysisPolicySaveError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, storage.ErrAnalysisRevision):
		writeError(w, http.StatusConflict, "analysis policy revision changed")
	case errors.Is(err, storage.ErrAnalysisDefinitionLimit):
		writeError(w, http.StatusConflict, "analysis policy limit reached")
	case errors.Is(err, storage.ErrAnalysisDefinitionInvalid):
		writeError(w, http.StatusBadRequest, "invalid analysis policy")
	case errors.Is(err, storage.ErrOwnedProjectNotFound):
		writeError(w, http.StatusNotFound, "project not found")
	default:
		slog.Warn("analysis policy save failed", "error", err)
		writeError(w, http.StatusInternalServerError, "cannot save analysis policy")
	}
	return true
}

// errRunPolicyConfiguration marks a project's policy configuration as
// unusable for Start (ambiguous or undecodable defaults). It is fixed by
// editing the policies, not by retrying.
var errRunPolicyConfiguration = errors.New("run policy configuration prevents admission")

// liveAnalysisPolicies returns every live policy of kind in ID order. Stores
// without a single-statement read are paged, de-duplicated by ID.
func liveAnalysisPolicies(store storage.AnalysisDefinitionStore, project, kind string) ([]storage.AnalysisDefinition, error) {
	var all []storage.AnalysisDefinition
	if policyStore, ok := store.(storage.AnalysisPolicyStore); ok {
		latest, err := policyStore.ListLatestAnalysisDefinitions(project, kind)
		if err != nil {
			return nil, err
		}
		all = latest
	} else {
		seen := map[string]bool{}
		for offset := 0; ; offset += 100 {
			page, err := store.ListAnalysisDefinitions(project, kind, 100, offset)
			if err != nil {
				return nil, err
			}
			for _, item := range page {
				if !seen[item.ID] {
					seen[item.ID] = true
					all = append(all, item)
				}
			}
			if len(page) < 100 || offset >= maxAnalysisPolicyScan {
				break
			}
		}
	}
	live := make([]storage.AnalysisDefinition, 0, len(all))
	for _, item := range all {
		if !item.Deleted {
			live = append(live, item)
		}
	}
	return live, nil
}

// maxAnalysisPolicyScan bounds the paged fallback at the storage budget.
const maxAnalysisPolicyScan = 1000

// projectDefaultPolicy returns the live policy of kind that applies by
// default in the project, or nil. Only the envelope flag of other policies is
// read; the selected default is fully decoded and validated. Ambiguity or an
// undecodable default is an errRunPolicyConfiguration; store failures are
// returned as they are.
func projectDefaultPolicy(store storage.AnalysisDefinitionStore, project, kind string) (*storage.AnalysisDefinition, error) {
	live, err := liveAnalysisPolicies(store, project, kind)
	if err != nil {
		return nil, err
	}
	var found *storage.AnalysisDefinition
	for index := range live {
		item := live[index]
		isDefault, err := policyAppliesByDefault(item.Definition)
		if err != nil {
			// A row whose envelope cannot be read cannot be proven not to be a
			// default; refuse rather than run without it.
			return nil, fmt.Errorf("%w: stored %s policy %q is unreadable: %v", errRunPolicyConfiguration, kind, item.Name, err)
		}
		if !isDefault {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%w: more than one %s policy applies by default", errRunPolicyConfiguration, kind)
		}
		copied := item
		found = &copied
	}
	if found != nil {
		if _, err := decodeAnalysisPolicy(kind, found.Definition); err != nil {
			return nil, fmt.Errorf("%w: default %s policy %q is invalid: %v", errRunPolicyConfiguration, kind, found.Name, err)
		}
	}
	return found, nil
}

// resolveRunPolicyPins returns the default policy revisions for a project, in
// stable kind order. Stores without analysis definitions have no policies.
func (tr *TestRunner) resolveRunPolicyPins(projectID string) ([]RunPolicyPin, error) {
	store, ok := tr.testStore.(storage.AnalysisDefinitionStore)
	if !ok || store == nil {
		return nil, nil
	}
	projectID = normalizeProjectID(projectID)
	pins := []RunPolicyPin{}
	for _, kind := range storage.AnalysisPolicyKinds {
		policy, err := projectDefaultPolicy(store, projectID, kind)
		if err != nil {
			return nil, fmt.Errorf("resolve %s policy: %w", kind, err)
		}
		if policy == nil {
			continue
		}
		digest := sha256.Sum256(policy.Definition)
		pins = append(pins, RunPolicyPin{Kind: kind, ID: policy.ID, Name: policy.Name, Revision: policy.Revision, SHA256: hex.EncodeToString(digest[:]), Source: "project_default"})
	}
	return pins, nil
}

// Package perfevidence runs and validates controlled, local performance probes.
// Its reports are component evidence: they deliberately do not claim that the
// production Kubernetes performance targets have been attained.
package perfevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion          = "apexload.performance-evidence/v1"
	EvidenceKind           = "controlled_local_component_benchmark"
	AttainmentNotEvaluated = "not_evaluated"
)

var requiredMeasurementIDs = []string{
	"harness.metrics_record.duration",
	"vuser.http_density_local.capacity",
	"ramp.synthetic_engine.start_rate",
	"metrics.collector.publication_latency",
}

// Report is the machine-readable artifact emitted by apexbench.
type Report struct {
	SchemaVersion string              `json:"schema_version"`
	EvidenceKind  string              `json:"evidence_kind"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Build         BuildMetadata       `json:"build"`
	Environment   EnvironmentMetadata `json:"environment"`
	Configuration RunConfiguration    `json:"configuration"`
	ClaimPolicy   ClaimPolicy         `json:"claim_policy"`
	Measurements  []Measurement       `json:"measurements"`
	Validation    ValidationSummary   `json:"validation"`
}

type BuildMetadata struct {
	Version            string `json:"version"`
	DisplayVersion     string `json:"display_version"`
	Commit             string `json:"commit,omitempty"`
	Revision           string `json:"revision,omitempty"`
	BuildTime          string `json:"build_time,omitempty"`
	GoVersion          string `json:"go_version"`
	VCSStatusAvailable bool   `json:"vcs_status_available"`
	VCSModified        bool   `json:"vcs_modified"`
}

// EnvironmentMetadata excludes hostname and user-identifying fields while
// retaining enough hardware/runtime detail to interpret and reproduce a run.
type EnvironmentMetadata struct {
	GOOS                string `json:"goos"`
	GOARCH              string `json:"goarch"`
	KernelVersion       string `json:"kernel_version,omitempty"`
	CPUModel            string `json:"cpu_model,omitempty"`
	LogicalCPUs         int    `json:"logical_cpus"`
	PhysicalCores       int    `json:"physical_cores,omitempty"`
	GOMAXPROCS          int    `json:"gomaxprocs"`
	PhysicalMemoryBytes uint64 `json:"physical_memory_bytes,omitempty"`
	HostClass           string `json:"host_class,omitempty"`
	RunnerImage         string `json:"runner_image,omitempty"`
}

type RunConfiguration struct {
	Profile                       string `json:"profile"`
	HarnessIterations             int    `json:"harness_iterations"`
	HarnessSamples                int    `json:"harness_samples"`
	HTTPVUsers                    int    `json:"http_vusers"`
	HTTPDensityTimeoutMillis      int64  `json:"http_density_timeout_millis"`
	HTTPDensityHoldMillis         int64  `json:"http_density_hold_millis"`
	HTTPDensityReleaseGraceMillis int64  `json:"http_density_release_grace_millis"`
	RampVUsers                    int    `json:"ramp_vusers"`
	RampSamples                   int    `json:"ramp_samples"`
	MetricsLatencySamples         int    `json:"metrics_latency_samples"`
	MetricsIntervalMillis         int64  `json:"metrics_interval_millis"`
	MetricsProbeTimeoutMillis     int64  `json:"metrics_probe_timeout_millis"`
}

// ClaimPolicy is intentionally restrictive. Numeric comparisons make the
// artifact useful, but they cannot be promoted into production target claims.
type ClaimPolicy struct {
	TargetAttainmentClaimed bool   `json:"target_attainment_claimed"`
	Reason                  string `json:"reason"`
}

type Measurement struct {
	ID                string           `json:"id"`
	Description       string           `json:"description"`
	Scope             string           `json:"scope"`
	Method            string           `json:"method"`
	Observed          Statistics       `json:"observed"`
	SupportingMetrics []ScalarMetric   `json:"supporting_metrics,omitempty"`
	TargetComparison  TargetComparison `json:"target_comparison"`
	Limitations       []string         `json:"limitations"`
}

type Statistics struct {
	Unit   string    `json:"unit"`
	Values []float64 `json:"values"`
	Min    float64   `json:"min"`
	Median float64   `json:"median"`
	P95    float64   `json:"p95"`
	Max    float64   `json:"max"`
}

type ScalarMetric struct {
	Name  string  `json:"name"`
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
}

type Target struct {
	Name     string  `json:"name"`
	Operator string  `json:"operator"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit"`
	Source   string  `json:"source"`
	Scope    string  `json:"scope"`
}

type TargetComparison struct {
	ComparedStatistic string  `json:"compared_statistic"`
	ObservedValue     float64 `json:"observed_value"`
	Target            Target  `json:"target"`
	NumericRelation   string  `json:"numeric_relation"`
	AttainmentStatus  string  `json:"attainment_status"`
	Reason            string  `json:"reason"`
}

type ValidationSummary struct {
	Status               string   `json:"status"`
	ReleaseEvidenceReady bool     `json:"release_evidence_ready"`
	Errors               []string `json:"errors,omitempty"`
	Warnings             []string `json:"warnings,omitempty"`
}

// NewStatistics retains raw samples and computes deterministic nearest-rank
// summary statistics. Raw values let downstream reviewers recompute the table.
func NewStatistics(unit string, values []float64) (Statistics, error) {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return Statistics{}, errors.New("statistics unit is required")
	}
	if len(values) == 0 {
		return Statistics{}, errors.New("at least one statistics value is required")
	}
	ordered := append([]float64(nil), values...)
	for i, value := range ordered {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return Statistics{}, fmt.Errorf("statistics value[%d] must be finite and non-negative", i)
		}
	}
	sort.Float64s(ordered)
	median := percentile(ordered, 0.50)
	return Statistics{
		Unit:   unit,
		Values: append([]float64(nil), values...),
		Min:    ordered[0],
		Median: median,
		P95:    percentile(ordered, 0.95),
		Max:    ordered[len(ordered)-1],
	}, nil
}

func percentile(ordered []float64, quantile float64) float64 {
	index := int(math.Ceil(float64(len(ordered))*quantile)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}

// NewTargetComparison performs only a numerical comparison. The returned
// attainment status is always not_evaluated because local component probes do
// not match the production target scopes.
func NewTargetComparison(observed Statistics, statistic string, target Target, reason string) (TargetComparison, error) {
	var value float64
	switch statistic {
	case "min":
		value = observed.Min
	case "median":
		value = observed.Median
	case "p95":
		value = observed.P95
	case "max":
		value = observed.Max
	default:
		return TargetComparison{}, fmt.Errorf("unsupported compared statistic %q", statistic)
	}
	if observed.Unit != target.Unit {
		return TargetComparison{}, fmt.Errorf("observed unit %q does not match target unit %q", observed.Unit, target.Unit)
	}
	relation, err := numericRelation(value, target.Operator, target.Value)
	if err != nil {
		return TargetComparison{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return TargetComparison{}, errors.New("not-evaluated comparison reason is required")
	}
	return TargetComparison{
		ComparedStatistic: statistic,
		ObservedValue:     value,
		Target:            target,
		NumericRelation:   relation,
		AttainmentStatus:  AttainmentNotEvaluated,
		Reason:            reason,
	}, nil
}

func numericRelation(observed float64, operator string, target float64) (string, error) {
	if math.IsNaN(observed) || math.IsInf(observed, 0) || math.IsNaN(target) || math.IsInf(target, 0) {
		return "", errors.New("comparison values must be finite")
	}
	switch operator {
	case "<", "<=":
		if observed < target {
			return "below_target", nil
		}
		if observed > target {
			return "above_target", nil
		}
		return "equal_to_target", nil
	case ">", ">=":
		if observed > target {
			return "above_target", nil
		}
		if observed < target {
			return "below_target", nil
		}
		return "equal_to_target", nil
	default:
		return "", fmt.Errorf("unsupported target operator %q", operator)
	}
}

// Finalize evaluates the report and embeds the result. Invalid reports remain
// inspectable, while callers should refuse to publish them.
func (report *Report) Finalize() {
	report.Validation = evaluateReport(*report)
}

// ValidateReport verifies both the report core and its embedded validation
// summary, catching stale or hand-edited artifacts.
func ValidateReport(report Report) error {
	expected := evaluateReport(report)
	var failures []string
	if len(expected.Errors) > 0 {
		failures = append(failures, expected.Errors...)
	}
	if report.Validation.Status != expected.Status {
		failures = append(failures, fmt.Sprintf("embedded validation status %q does not match %q", report.Validation.Status, expected.Status))
	}
	if report.Validation.ReleaseEvidenceReady != expected.ReleaseEvidenceReady {
		failures = append(failures, "embedded release_evidence_ready does not match evaluated readiness")
	}
	if !slices.Equal(report.Validation.Errors, expected.Errors) {
		failures = append(failures, "embedded validation errors do not match evaluated errors")
	}
	if !slices.Equal(report.Validation.Warnings, expected.Warnings) {
		failures = append(failures, "embedded validation warnings do not match evaluated warnings")
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func evaluateReport(report Report) ValidationSummary {
	var result ValidationSummary
	if report.SchemaVersion != SchemaVersion {
		result.Errors = append(result.Errors, fmt.Sprintf("schema_version must be %q", SchemaVersion))
	}
	if report.EvidenceKind != EvidenceKind {
		result.Errors = append(result.Errors, fmt.Sprintf("evidence_kind must be %q", EvidenceKind))
	}
	if report.GeneratedAt.IsZero() {
		result.Errors = append(result.Errors, "generated_at is required")
	}
	if strings.TrimSpace(report.Build.Version) == "" || strings.TrimSpace(report.Build.GoVersion) == "" {
		result.Errors = append(result.Errors, "build version and Go version are required")
	}
	if report.Environment.GOOS == "" || report.Environment.GOARCH == "" || report.Environment.LogicalCPUs <= 0 || report.Environment.GOMAXPROCS <= 0 {
		result.Errors = append(result.Errors, "GOOS, GOARCH, logical CPUs, and GOMAXPROCS are required")
	}
	if report.Configuration.Profile != "smoke" && report.Configuration.Profile != "release" {
		result.Errors = append(result.Errors, "configuration profile must be smoke or release")
	}
	if report.Configuration.HarnessIterations <= 0 || report.Configuration.HarnessSamples <= 0 ||
		report.Configuration.HTTPVUsers <= 0 || report.Configuration.HTTPDensityTimeoutMillis <= 0 ||
		report.Configuration.HTTPDensityHoldMillis < 0 || report.Configuration.HTTPDensityReleaseGraceMillis <= 0 ||
		report.Configuration.RampVUsers <= 0 ||
		report.Configuration.RampSamples <= 0 || report.Configuration.MetricsLatencySamples <= 0 ||
		report.Configuration.MetricsIntervalMillis <= 0 ||
		report.Configuration.MetricsProbeTimeoutMillis <= report.Configuration.MetricsIntervalMillis {
		result.Errors = append(result.Errors, "configuration counts, intervals, and timeouts are invalid")
	}
	if report.ClaimPolicy.TargetAttainmentClaimed {
		result.Errors = append(result.Errors, "controlled local evidence must not claim target attainment")
	}
	if strings.TrimSpace(report.ClaimPolicy.Reason) == "" {
		result.Errors = append(result.Errors, "claim policy reason is required")
	}

	seen := make(map[string]struct{}, len(report.Measurements))
	for i, measurement := range report.Measurements {
		prefix := fmt.Sprintf("measurement[%d]", i)
		if measurement.ID == "" {
			result.Errors = append(result.Errors, prefix+" id is required")
			continue
		}
		if _, duplicate := seen[measurement.ID]; duplicate {
			result.Errors = append(result.Errors, fmt.Sprintf("duplicate measurement id %q", measurement.ID))
		}
		seen[measurement.ID] = struct{}{}
		if measurement.Description == "" || measurement.Scope == "" || measurement.Method == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q description, scope, and method are required", measurement.ID))
		}
		if len(measurement.Limitations) == 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q must state limitations", measurement.ID))
		}
		if err := validateStatistics(measurement.Observed); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q: %v", measurement.ID, err))
		}
		for _, metric := range measurement.SupportingMetrics {
			if metric.Name == "" || metric.Unit == "" || math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) || metric.Value < 0 {
				result.Errors = append(result.Errors, fmt.Sprintf("measurement %q has an invalid supporting metric", measurement.ID))
			}
		}
		if measurement.ID == "harness.metrics_record.duration" {
			dropped, ok := supportingMetricValue(measurement.SupportingMetrics, "dropped_results")
			if !ok || dropped != 0 {
				result.Errors = append(result.Errors, "harness metrics-record comparison requires a zero-drop sample")
			}
		}
		if measurement.ID == "vuser.http_density_local.capacity" {
			configured, ok := supportingMetricValue(measurement.SupportingMetrics, "configured_vusers")
			if !ok || measurement.Observed.Max > configured {
				result.Errors = append(result.Errors, "HTTP density observation must not exceed configured VUsers")
			}
			if dropped, ok := supportingMetricValue(measurement.SupportingMetrics, "metrics_dropped_results"); ok && dropped > 0 {
				result.Warnings = append(result.Warnings, "HTTP density run dropped metrics; latency distribution evidence is incomplete")
			}
		}
		comparison := measurement.TargetComparison
		if comparison.AttainmentStatus != AttainmentNotEvaluated {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q attainment_status must be not_evaluated", measurement.ID))
		}
		if comparison.Reason == "" || comparison.Target.Name == "" || comparison.Target.Scope == "" || comparison.Target.Source == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q target comparison metadata is incomplete", measurement.ID))
		}
		if comparison.Target.Unit != measurement.Observed.Unit {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q target unit does not match observed unit", measurement.ID))
		}
		if math.IsNaN(comparison.Target.Value) || math.IsInf(comparison.Target.Value, 0) || comparison.Target.Value <= 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q target value must be finite and positive", measurement.ID))
		}
		expectedValue, valueOK := statisticValue(measurement.Observed, comparison.ComparedStatistic)
		if !valueOK || !nearlyEqual(expectedValue, comparison.ObservedValue) {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q compared value does not match observed statistics", measurement.ID))
		}
		relation, err := numericRelation(comparison.ObservedValue, comparison.Target.Operator, comparison.Target.Value)
		if err != nil || relation != comparison.NumericRelation {
			result.Errors = append(result.Errors, fmt.Sprintf("measurement %q numeric relation is invalid", measurement.ID))
		}
	}
	for _, required := range requiredMeasurementIDs {
		if _, ok := seen[required]; !ok {
			result.Errors = append(result.Errors, fmt.Sprintf("required measurement %q is missing", required))
		}
	}

	if report.Build.Revision == "" {
		result.Warnings = append(result.Warnings, "build revision is absent; artifact is not release-evidence ready")
	}
	if !report.Build.VCSStatusAvailable {
		result.Warnings = append(result.Warnings, "VCS modification status is unavailable; artifact is not release-evidence ready")
	} else if report.Build.VCSModified {
		result.Warnings = append(result.Warnings, "build was produced from a modified worktree; artifact is not release-evidence ready")
	}
	if report.Environment.CPUModel == "" || report.Environment.PhysicalMemoryBytes == 0 {
		result.Warnings = append(result.Warnings, "CPU model or physical memory metadata is unavailable; artifact is not release-evidence ready")
	}
	if report.Configuration.Profile != "release" {
		result.Warnings = append(result.Warnings, "smoke profile is diagnostic only; run the release profile before publishing evidence")
	} else if weakened := releaseConfigurationOutsideBounds(report.Configuration); len(weakened) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"release profile configuration is outside the release-evidence bounds: %s; rerun with release defaults or stronger settings",
			strings.Join(weakened, ", "),
		))
	}

	if len(result.Errors) == 0 {
		result.Status = "valid"
	} else {
		result.Status = "invalid"
	}
	result.ReleaseEvidenceReady = len(result.Errors) == 0 && len(result.Warnings) == 0
	return result
}

func releaseConfigurationOutsideBounds(configuration RunConfiguration) []string {
	minimum := defaultReleaseConfig()
	minimumReleaseGrace := httpDensityReleaseGrace(minimum).Milliseconds()
	checks := []struct {
		name      string
		actual    int64
		bound     int64
		isMaximum bool
	}{
		{name: "harness_iterations", actual: int64(configuration.HarnessIterations), bound: int64(minimum.HarnessIterations)},
		{name: "harness_samples", actual: int64(configuration.HarnessSamples), bound: int64(minimum.HarnessSamples)},
		{name: "http_vusers", actual: int64(configuration.HTTPVUsers), bound: int64(minimum.HTTPVUsers)},
		{name: "http_density_timeout_millis", actual: configuration.HTTPDensityTimeoutMillis, bound: minimum.HTTPDensityTimeout.Milliseconds()},
		{name: "http_density_hold_millis", actual: configuration.HTTPDensityHoldMillis, bound: minimum.HTTPDensityHold.Milliseconds()},
		{name: "http_density_release_grace_millis", actual: configuration.HTTPDensityReleaseGraceMillis, bound: minimumReleaseGrace},
		{name: "ramp_vusers", actual: int64(configuration.RampVUsers), bound: int64(minimum.RampVUsers)},
		{name: "ramp_samples", actual: int64(configuration.RampSamples), bound: int64(minimum.RampSamples)},
		{name: "metrics_latency_samples", actual: int64(configuration.MetricsLatencySamples), bound: int64(minimum.MetricsLatencySamples)},
		{name: "metrics_interval_millis", actual: configuration.MetricsIntervalMillis, bound: minimum.MetricsInterval.Milliseconds(), isMaximum: true},
		{name: "metrics_probe_timeout_millis", actual: configuration.MetricsProbeTimeoutMillis, bound: minimum.MetricsProbeTimeout.Milliseconds()},
	}

	var weakened []string
	for _, check := range checks {
		if check.isMaximum && check.actual > check.bound {
			weakened = append(weakened, fmt.Sprintf("%s=%d (maximum %d)", check.name, check.actual, check.bound))
		} else if !check.isMaximum && check.actual < check.bound {
			weakened = append(weakened, fmt.Sprintf("%s=%d (minimum %d)", check.name, check.actual, check.bound))
		}
	}
	return weakened
}

func validateStatistics(statistics Statistics) error {
	recomputed, err := NewStatistics(statistics.Unit, statistics.Values)
	if err != nil {
		return err
	}
	if !nearlyEqual(recomputed.Min, statistics.Min) || !nearlyEqual(recomputed.Median, statistics.Median) || !nearlyEqual(recomputed.P95, statistics.P95) || !nearlyEqual(recomputed.Max, statistics.Max) {
		return errors.New("summary does not match raw values")
	}
	return nil
}

func statisticValue(statistics Statistics, name string) (float64, bool) {
	switch name {
	case "min":
		return statistics.Min, true
	case "median":
		return statistics.Median, true
	case "p95":
		return statistics.P95, true
	case "max":
		return statistics.Max, true
	default:
		return 0, false
	}
}

func supportingMetricValue(metrics []ScalarMetric, name string) (float64, bool) {
	for _, metric := range metrics {
		if metric.Name == name {
			return metric.Value, true
		}
	}
	return 0, false
}

func nearlyEqual(left, right float64) bool {
	difference := math.Abs(left - right)
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return difference <= scale*1e-12
}

// DecodeReport applies strict JSON decoding so schema drift and misspelled
// fields cannot silently enter a release evidence archive.
func DecodeReport(reader io.Reader) (Report, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var report Report
	if err := decoder.Decode(&report); err != nil {
		return Report{}, fmt.Errorf("decode performance evidence: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Report{}, err
	}
	if err := ValidateReport(report); err != nil {
		return Report{}, fmt.Errorf("validate performance evidence: %w", err)
	}
	return report, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode performance evidence: multiple JSON values are not allowed")
		}
		return fmt.Errorf("decode performance evidence: %w", err)
	}
	return nil
}

func MarshalReport(report Report) ([]byte, error) {
	if err := ValidateReport(report); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		return nil, fmt.Errorf("encode performance evidence: %w", err)
	}
	return buffer.Bytes(), nil
}

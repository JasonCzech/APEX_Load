package analysis

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// CatalogVersion changes whenever a metric's meaning or qualification rules
// change. It is separate from artifact versions and never proves collection.
const CatalogVersion = "apexload.metric_catalog.v1"

const (
	PopulationAction          = "action"
	PopulationTransaction     = "transaction"
	PopulationMessage         = "message"
	PopulationByte            = "byte"
	PopulationInfrastructure  = "infrastructure"
	PopulationBrowserDocument = "browser_document"
	PopulationResource        = "resource"
	PopulationInteraction     = "interaction"
	PopulationJourney         = "journey"
	PopulationSample          = "sample"

	MissingUnscored    = "unscored"
	ResetExclude       = "exclude_reset_interval"
	ResetNotApplicable = "not_applicable"

	MaxGroupingDimensions  = 3
	MaxQueryFilters        = 16
	MaxQuerySeries         = 20
	MaxMovingWindowBuckets = 1000

	RawScalarQualifiedWhenEvidencePresent = "qualified_when_evidence_present"
	RawScalarLegacyResetOnly              = "legacy_reset_only"
)

// MetricDefinition describes an executable retained-evidence metric. Legacy
// dashboard cards keep their existing names; they are not silently converted
// into qualified analytical measurements by this catalog.
type MetricDefinition struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	InputKinds       []string `json:"input_kinds"`
	Populations      []string `json:"populations"`
	Unit             string   `json:"unit"`             // Canonical analytical unit.
	NativeWireUnit   string   `json:"native_wire_unit"` // Existing native API unit, preserved for compatibility.
	Aggregations     []string `json:"aggregations"`
	Derivations      []string `json:"derivations"`
	QueryProfiles    []string `json:"query_profiles"`
	RequiredEvidence []string `json:"required_evidence"`
	MissingRule      string   `json:"missing_rule"`
	ResetRule        string   `json:"reset_rule"`
	Baseline         bool     `json:"baseline"`
	Correlation      bool     `json:"correlation"`
	SLA              bool     `json:"sla"`
}

// OperationDefinition is a typed operation, never a SQL expression. RawScalar
// means the existing scalar bucket engines implement this aggregation; the
// broader catalog also declares operations performed by other typed adapters.
// RawScalarQualification distinguishes reset-only legacy rate displays from
// operations eligible for typed comparisons when their evidence is qualified.
type OperationDefinition struct {
	Name                   string   `json:"name"`
	InputKinds             []string `json:"input_kinds"`
	Populations            []string `json:"populations,omitempty"`
	UnitRule               string   `json:"unit_rule"`
	RequiredEvidence       []string `json:"required_evidence"`
	MissingRule            string   `json:"missing_rule"`
	ResetRule              string   `json:"reset_rule"`
	RawScalar              bool     `json:"raw_scalar"`
	RawScalarKinds         []string `json:"raw_scalar_kinds,omitempty"`
	RawScalarQualification string   `json:"raw_scalar_qualification,omitempty"`
}

type MetricCatalog struct {
	Version       string                `json:"version"`
	Metrics       []MetricDefinition    `json:"metrics"`
	Operations    []OperationDefinition `json:"operations"`
	Bounds        QueryBounds           `json:"bounds"`
	QueryProfiles []NativeQueryProfile  `json:"query_profiles"`
}

type QueryBounds struct {
	GroupingDimensions int `json:"grouping_dimensions"`
	Filters            int `json:"filters"`
	Series             int `json:"series"`
	Buckets            int `json:"buckets"`
	MovingBuckets      int `json:"moving_buckets"`
}

func buildCatalog() MetricCatalog {
	populations := []string{PopulationAction, PopulationTransaction}
	profiles := nativeQueryProfiles()
	metrics := []MetricDefinition{
		{Name: "response_time", Unit: "ms", NativeWireUnit: "ms", Aggregations: []string{"avg", "min", "max", "p50", "p90", "p95", "p99"}, RequiredEvidence: []string{"raw_observations_or_compatible_mergeable_distribution"}},
		{Name: "observation_count", Unit: "count", NativeWireUnit: "observations", Aggregations: []string{"count"}, RequiredEvidence: []string{"qualified_observation_counts"}},
		{Name: "error_rate", Unit: "percent", NativeWireUnit: "%", Aggregations: []string{"avg"}, RequiredEvidence: []string{"same_population_failure_and_observation_counts"}},
		{Name: "observation_rate", Unit: "count/s", NativeWireUnit: "observations/s", Aggregations: []string{"rate"}, RequiredEvidence: []string{"qualified_observation_counts", "complete_elapsed_window"}},
		{Name: "error_observation_rate", Unit: "count/s", NativeWireUnit: "observations/s", Aggregations: []string{"rate"}, RequiredEvidence: []string{"same_population_failure_and_observation_counts", "complete_elapsed_window"}},
		{Name: "success_observation_rate", Unit: "count/s", NativeWireUnit: "observations/s", Aggregations: []string{"rate"}, RequiredEvidence: []string{"same_population_failure_and_observation_counts", "complete_elapsed_window"}},
	}
	for i := range metrics {
		metrics[i].Version = CatalogVersion
		metrics[i].InputKinds = []string{KindObservation}
		metrics[i].Populations = slices.Clone(populations)
		metrics[i].Derivations = []string{"baseline_delta", "baseline_percent_delta", "moving_mean", "moving_stddev"}
		for _, profile := range profiles {
			metrics[i].QueryProfiles = append(metrics[i].QueryProfiles, profile.ID)
		}
		metrics[i].MissingRule = MissingUnscored
		metrics[i].ResetRule = ResetNotApplicable
		metrics[i].Baseline, metrics[i].Correlation, metrics[i].SLA = true, true, true
	}
	operations := []OperationDefinition{}
	for _, name := range []string{"avg", "min", "max", "sum", "count"} {
		unit := "preserve"
		if name == "count" {
			unit = "count"
		}
		operations = append(operations, OperationDefinition{Name: name, InputKinds: []string{KindGauge, KindObservation}, UnitRule: unit, RequiredEvidence: []string{"qualified_raw_scalars"}, RawScalar: true})
	}
	for _, name := range []string{"p50", "p90", "p95", "p99"} {
		operations = append(operations, OperationDefinition{Name: name, InputKinds: []string{KindObservation}, UnitRule: "preserve", RequiredEvidence: []string{"raw_observations_or_native_transaction_distribution"}, RawScalar: name != "p90"})
	}
	operations = append(operations,
		OperationDefinition{Name: "rate", InputKinds: []string{KindCounter, KindObservation}, UnitRule: "per_second", RequiredEvidence: []string{"qualified_observation_counts_or_same_counter_epoch", "positive_elapsed_window", "declared_rate_unit"}, RawScalar: true},
		OperationDefinition{Name: "delta", InputKinds: []string{KindCounter}, UnitRule: "preserve", RequiredEvidence: []string{"same_counter_epoch", "positive_elapsed_window"}},
		OperationDefinition{Name: "baseline_delta", InputKinds: []string{KindGauge, KindObservation}, UnitRule: "preserve", RequiredEvidence: []string{"comparable_metric_versions_units_populations"}},
		OperationDefinition{Name: "baseline_percent_delta", InputKinds: []string{KindGauge, KindObservation}, UnitRule: "percent", RequiredEvidence: []string{"comparable_metric_versions_units_populations", "nonzero_baseline"}},
		OperationDefinition{Name: "moving_mean", InputKinds: []string{KindGauge, KindObservation}, UnitRule: "preserve", RequiredEvidence: []string{"bounded_aligned_scalar_window", "minimum_coverage"}},
		OperationDefinition{Name: "moving_stddev", InputKinds: []string{KindGauge, KindObservation}, UnitRule: "preserve", RequiredEvidence: []string{"bounded_aligned_scalar_window", "minimum_coverage", "two_valid_buckets"}},
	)
	for _, name := range []string{"error_ratio", "success_ratio", "acknowledgement_ratio", "settlement_ratio", "duplicate_ratio", "redelivery_ratio", "dead_letter_ratio"} {
		eligible := []string{PopulationMessage}
		if name == "error_ratio" || name == "success_ratio" {
			eligible = []string{PopulationAction, PopulationTransaction}
		}
		operations = append(operations, OperationDefinition{Name: name, InputKinds: []string{KindObservation}, Populations: eligible, UnitRule: "ratio", RequiredEvidence: []string{"qualified_same_population_subset_counts", "same_scope_and_metric_version"}})
	}
	for i := range operations {
		if operations[i].RawScalar {
			operations[i].RawScalarQualification = RawScalarQualifiedWhenEvidencePresent
			operations[i].RawScalarKinds = slices.Clone(operations[i].InputKinds)
			if operations[i].Name == "rate" {
				operations[i].RawScalarQualification = RawScalarLegacyResetOnly
				operations[i].RawScalarKinds = []string{KindCounter}
			}
			if slices.Contains([]string{"p50", "p95", "p99"}, operations[i].Name) {
				operations[i].RawScalarKinds = []string{KindObservation}
			}
		}
		operations[i].MissingRule = MissingUnscored
		operations[i].ResetRule = ResetNotApplicable
		if slices.Contains(operations[i].InputKinds, KindCounter) {
			operations[i].ResetRule = ResetExclude
		}
	}
	return MetricCatalog{Version: CatalogVersion, Metrics: metrics, Operations: operations, QueryProfiles: profiles, Bounds: QueryBounds{MaxGroupingDimensions, MaxQueryFilters, MaxQuerySeries, MaxSeriesPoints, MaxMovingWindowBuckets}}
}

var canonicalCatalog = buildCatalog()

// Catalog returns a deeply independent copy of the process-owned contract.
func Catalog() MetricCatalog {
	out := canonicalCatalog
	out.Metrics = make([]MetricDefinition, len(canonicalCatalog.Metrics))
	for i, metric := range canonicalCatalog.Metrics {
		out.Metrics[i] = cloneMetricDefinition(metric)
	}
	out.Operations = make([]OperationDefinition, len(canonicalCatalog.Operations))
	for i, operation := range canonicalCatalog.Operations {
		out.Operations[i] = cloneOperationDefinition(operation)
	}
	out.QueryProfiles = make([]NativeQueryProfile, len(canonicalCatalog.QueryProfiles))
	for i, profile := range canonicalCatalog.QueryProfiles {
		profile.AllowedFilters = slices.Clone(profile.AllowedFilters)
		profile.RequiredFilters = slices.Clone(profile.RequiredFilters)
		profile.AllowedAggregations = slices.Clone(profile.AllowedAggregations)
		profile.RequiredEvidence = slices.Clone(profile.RequiredEvidence)
		out.QueryProfiles[i] = profile
	}
	return out
}

func cloneMetricDefinition(metric MetricDefinition) MetricDefinition {
	metric.InputKinds = slices.Clone(metric.InputKinds)
	metric.Populations = slices.Clone(metric.Populations)
	metric.Aggregations = slices.Clone(metric.Aggregations)
	metric.Derivations = slices.Clone(metric.Derivations)
	metric.QueryProfiles = slices.Clone(metric.QueryProfiles)
	metric.RequiredEvidence = slices.Clone(metric.RequiredEvidence)
	return metric
}

func cloneOperationDefinition(operation OperationDefinition) OperationDefinition {
	operation.InputKinds = slices.Clone(operation.InputKinds)
	operation.Populations = slices.Clone(operation.Populations)
	operation.RequiredEvidence = slices.Clone(operation.RequiredEvidence)
	operation.RawScalarKinds = slices.Clone(operation.RawScalarKinds)
	return operation
}

func LookupMetric(name string) (MetricDefinition, bool) {
	for _, metric := range canonicalCatalog.Metrics {
		if metric.Name == name {
			return cloneMetricDefinition(metric), true
		}
	}
	return MetricDefinition{}, false
}

func lookupOperation(name string) (*OperationDefinition, bool) {
	for i := range canonicalCatalog.Operations {
		if canonicalCatalog.Operations[i].Name == name {
			return &canonicalCatalog.Operations[i], true
		}
	}
	return nil, false
}

func LookupOperation(name string) (OperationDefinition, bool) {
	operation, found := lookupOperation(name)
	if !found {
		return OperationDefinition{}, false
	}
	return cloneOperationDefinition(*operation), true
}

// ValidateMetricAggregation preserves the existing retained-native metric
// names, percent units and explicit action/transaction populations. Evidence
// and capability qualification remains necessary at execution time.
func ValidateMetricAggregation(metric, population, aggregation string) error {
	definition, found := LookupMetric(metric)
	if !found || !slices.Contains(definition.Populations, population) || !slices.Contains(definition.Aggregations, aggregation) {
		return errors.New("unsupported retained measurement, population or aggregation")
	}
	return nil
}

// ValidateScalarAggregation centralizes the legacy raw-scalar aggregation
// contract. A percentile scalar or histogram requires its own typed adapter.
func ValidateScalarAggregation(kind, aggregation string) error {
	if kind == KindSummary {
		return errors.New("preaggregated summary inputs require an explicit statistic selector; reaggregation is unavailable")
	}
	if kind == KindHistogram {
		return errors.New("histogram measurements require a declared distribution adapter")
	}
	if kind != KindCounter && kind != KindGauge && kind != KindObservation {
		return errors.New("bucket aggregation requires raw scalar measurements")
	}
	operation, found := lookupOperation(aggregation)
	if !found || !operation.RawScalar {
		return errors.New("unsupported raw-evidence aggregation")
	}
	if kind == KindCounter && aggregation != "rate" {
		return errors.New("counters require rate conversion")
	}
	if aggregation == "rate" && kind != KindCounter {
		return errors.New("rates require counters")
	}
	if !slices.Contains(operation.RawScalarKinds, kind) {
		return errors.New("percentiles require raw observations")
	}
	return nil
}

func ValidateScalarAggregationName(aggregation string) error {
	operation, found := lookupOperation(aggregation)
	if !found || !operation.RawScalar {
		return errors.New("unsupported analytical aggregation")
	}
	return nil
}

// MeasurementSemantics is the declared meaning of a dynamic monitor/import
// metric. Empty legacy fields stay readable but cannot become qualified typed
// input by guessing a population or metric version from a name.
type MeasurementSemantics struct {
	Kind          string           `json:"kind"`
	InputKind     string           `json:"input_kind,omitempty"`
	Derivation    []DerivationStep `json:"derivation,omitempty"`
	Unit          string           `json:"unit"`
	Population    string           `json:"population"`
	MetricVersion string           `json:"metric_version"`
}

func (s MeasurementSemantics) Validate() error {
	switch s.Kind {
	case KindGauge, KindCounter, KindObservation, KindHistogram, KindSummary:
	default:
		return errors.New("explicit supported measurement kind is required")
	}
	for name, value := range map[string]string{"population": s.Population, "metric_version": s.MetricVersion} {
		if strings.TrimSpace(value) == "" || len(value) > maxMetricIdentityBytes || strings.ContainsRune(value, 0) {
			return fmt.Errorf("%s must contain 1-%d non-NUL bytes", name, maxMetricIdentityBytes)
		}
	}
	if s.InputKind != "" && (s.Kind != KindGauge || (s.InputKind != KindCounter && s.InputKind != KindGauge && s.InputKind != KindObservation)) {
		return errors.New("invalid derived input kind")
	}
	if s.InputKind != "" && len(s.Derivation) == 0 {
		return errors.New("legacy derived measurement has unknown statistic")
	}
	if len(s.Derivation) > 0 && (s.Kind != KindGauge || s.InputKind == "") {
		return errors.New("measurement derivation requires a gauge with declared input kind")
	}
	if err := validateDerivation(s.Derivation, true); err != nil {
		return err
	}
	unit, _, err := NormalizeUnit(s.Unit)
	if err != nil || unit != s.Unit {
		return errors.New("explicit normalized measurement unit is required")
	}
	return nil
}

// CompatibleMeasurements applies to directly comparable overlays and deltas.
// Correlation intentionally uses independent qualified semantics on each axis.
func CompatibleMeasurements(left, right MeasurementSemantics) error {
	if err := left.Validate(); err != nil {
		return err
	}
	if err := right.Validate(); err != nil {
		return err
	}
	if left.Kind != right.Kind || left.InputKind != right.InputKind || left.Unit != right.Unit || left.Population != right.Population || left.MetricVersion != right.MetricVersion || !slices.Equal(left.Derivation, right.Derivation) {
		return errors.New("incompatible metric kinds, units, populations or versions")
	}
	return nil
}

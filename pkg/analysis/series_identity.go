package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// SeriesIdentityVersion pins the measurement and observation fields encoded by
// MeasurementSeriesIdentity. Changing those fields requires a new version.
const SeriesIdentityVersion = "apexload.series_identity.v1"

const maxSeriesIdentityJSONBytes = 128 << 10

// SeriesObservation declares the temporal semantics supplied by a source
// adapter. Mode is interval, snapshot, or cumulative; BucketNanos is the exact
// grid duration. Only snapshots may declare a zero (unknown) grid.
type SeriesObservation struct {
	Mode        string `json:"mode"`
	BucketNanos int64  `json:"bucket_nanos"`
}

// SeriesIdentity is a versioned, full SHA256 measurement fingerprint. It does
// not authorize a baseline or establish measurement qualification.
type SeriesIdentity struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// MeasurementSeriesIdentity identifies the same logical measurement across
// explicitly selected runs/datasets. It preserves exact units and derivation
// lineage, adding the adapter's declared observation mode and grid. Values,
// clocks, run/dataset ownership and mutable settlement are excluded. Callers must separately
// apply measurement qualification and source/association authorization.
//
// The input is borrowed only during this call and is never modified. As with
// MetricPoint validation, callers must not concurrently mutate its maps/slices.
func MeasurementSeriesIdentity(point MetricPoint, observation SeriesObservation) (SeriesIdentity, error) {
	if err := point.Validate(); err != nil {
		return SeriesIdentity{}, err
	}
	return measurementSeriesIdentity(point, observation)
}

// MeasurementSeriesIdentityOfRecorded is MeasurementSeriesIdentity for a point
// that was admitted when it was recorded, such as an archived monitor point:
// it checks only the fields the identity encodes (project, source and metric
// present and bounded, a known kind, valid UTF-8), not values or clocks, which
// the identity excludes. For a point that passes Validate both return the
// same identity.
func MeasurementSeriesIdentityOfRecorded(point MetricPoint, observation SeriesObservation) (SeriesIdentity, error) {
	for _, value := range []string{point.ProjectID, point.Source, point.MetricName} {
		if strings.TrimSpace(value) == "" || len(value) > maxMetricIdentityBytes {
			return SeriesIdentity{}, errors.New("recorded measurement identity requires project, source and metric")
		}
	}
	for _, value := range []string{point.ScenarioID, point.Host, point.Service, point.Unit, point.Population, point.MetricVersion, point.InputKind} {
		if len(value) > maxMetricIdentityBytes {
			return SeriesIdentity{}, errors.New("recorded measurement identity field exceeds bounds")
		}
	}
	switch point.Kind {
	case KindUnknown, KindGauge, KindCounter, KindObservation, KindHistogram, KindSummary:
	default:
		return SeriesIdentity{}, fmt.Errorf("unsupported metric kind %q", point.Kind)
	}
	if len(point.Dimensions) > maxDimensions || point.Provider != nil && len(point.Provider.EntityIDs) > 16 {
		return SeriesIdentity{}, errors.New("recorded measurement identity labels exceed bounds")
	}
	return measurementSeriesIdentity(point, observation)
}

func measurementSeriesIdentity(point MetricPoint, observation SeriesObservation) (SeriesIdentity, error) {
	if observation.BucketNanos < 0 || observation.BucketNanos > int64(24*time.Hour) {
		return SeriesIdentity{}, errors.New("series observation grid must be within 24 hours")
	}
	switch observation.Mode {
	case "interval", "cumulative":
		if observation.BucketNanos == 0 {
			return SeriesIdentity{}, errors.New("interval and cumulative observations require a positive grid")
		}
	case "snapshot":
	default:
		return SeriesIdentity{}, errors.New("unsupported series observation mode")
	}
	projection := measurementIdentityOf(point)
	if err := projection.validateUTF8(); err != nil {
		return SeriesIdentity{}, err
	}
	body, err := MarshalBoundedAnalysisJSON(struct {
		Version     string                        `json:"version"`
		Observation SeriesObservation             `json:"observation"`
		Measurement measurementIdentityProjection `json:"measurement"`
	}{SeriesIdentityVersion, observation, projection}, maxSeriesIdentityJSONBytes)
	if err != nil {
		return SeriesIdentity{}, fmt.Errorf("encode bounded measurement series identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return SeriesIdentity{Version: SeriesIdentityVersion, SHA256: hex.EncodeToString(digest[:])}, nil
}

// These projections borrow maps/slices. The hot equality path compares scalars
// directly and performs no canonical encoding, hashing or cloning. omitempty
// gives nil/empty maps and derivation the same fingerprint as their equality.
type measurementIdentityProjection struct {
	Fields     measurementIdentityFields  `json:"fields"`
	Dimensions map[string]string          `json:"dimensions,omitempty"`
	Derivation []DerivationStep           `json:"derivation,omitempty"`
	Provider   providerIdentityProjection `json:"provider"`
}

type measurementIdentityFields struct {
	ProjectID     string `json:"project_id"`
	Source        string `json:"source"`
	ScenarioID    string `json:"scenario_id,omitempty"`
	Host          string `json:"host,omitempty"`
	Service       string `json:"service,omitempty"`
	MetricName    string `json:"metric_name"`
	Population    string `json:"population,omitempty"`
	MetricVersion string `json:"metric_version,omitempty"`
	Kind          string `json:"kind"`
	InputKind     string `json:"input_kind,omitempty"`
	Unit          string `json:"unit,omitempty"`
}

type providerIdentityProjection struct {
	Fields    providerIdentityFields `json:"fields"`
	EntityIDs map[string]string      `json:"entity_ids,omitempty"`
}

type providerIdentityFields struct {
	Profile               string `json:"profile"`
	Name                  string `json:"name,omitempty"`
	Backend               string `json:"backend,omitempty"`
	Synthetic             bool   `json:"synthetic,omitempty"`
	MetricID              string `json:"metric_id,omitempty"`
	ResolutionSeconds     int    `json:"resolution_seconds,omitempty"`
	Statistic             string `json:"statistic,omitempty"`
	QueryWindow           bool   `json:"query_window,omitempty"`
	QueryID               string `json:"query_id,omitempty"`
	DefinitionHash        string `json:"definition_hash,omitempty"`
	BackendProfile        string `json:"backend_profile,omitempty"`
	Mode                  string `json:"mode,omitempty"`
	AnalysisStatistic     string `json:"analysis_statistic,omitempty"`
	EvaluationStepSeconds int    `json:"evaluation_step_seconds,omitempty"`
}

func measurementIdentityOf(point MetricPoint) measurementIdentityProjection {
	return measurementIdentityProjection{
		Fields: measurementIdentityFields{
			ProjectID: point.ProjectID, Source: point.Source, ScenarioID: point.ScenarioID,
			Host: point.Host, Service: point.Service, MetricName: point.MetricName,
			Population: point.Population, MetricVersion: point.MetricVersion,
			Kind: point.Kind, InputKind: point.InputKind, Unit: point.Unit,
		},
		Dimensions: point.Dimensions, Derivation: point.Derivation,
		Provider: providerIdentityOf(point.Provider),
	}
}

func (p measurementIdentityProjection) equal(other measurementIdentityProjection) bool {
	return p.Fields == other.Fields && maps.Equal(p.Dimensions, other.Dimensions) &&
		slices.Equal(p.Derivation, other.Derivation) && p.Provider.equal(other.Provider)
}

func providerIdentityOf(provider *ProviderMeasurement) providerIdentityProjection {
	if provider != nil && provider.Name == "prometheus" {
		return prometheusIdentityOf(provider)
	}
	return generalProviderIdentityOf(provider)
}

// General provider comparisons deliberately retain their original fields even
// when called directly with Prometheus metadata (for example by monitor rules).
func generalProviderIdentityOf(provider *ProviderMeasurement) providerIdentityProjection {
	if provider == nil {
		return providerIdentityProjection{Fields: providerIdentityFields{Profile: "none"}}
	}
	return providerIdentityProjection{
		Fields: providerIdentityFields{
			Profile: "provider", Name: provider.Name, Backend: provider.Backend,
			Synthetic: provider.Synthetic, MetricID: provider.MetricID,
			ResolutionSeconds: provider.ResolutionSeconds, Statistic: provider.Statistic,
			QueryWindow: provider.QueryWindow,
		},
		EntityIDs: provider.EntityIDs,
	}
}

// Prometheus measurement comparisons retain evaluation-grid provenance rather
// than general provider display IDs, backend labels or mutable bucket state.
func prometheusIdentityOf(provider *ProviderMeasurement) providerIdentityProjection {
	return providerIdentityProjection{
		Fields: providerIdentityFields{
			Profile: "prometheus", QueryID: provider.QueryID, DefinitionHash: provider.DefinitionHash,
			BackendProfile: provider.BackendProfile, Mode: provider.Mode, Statistic: provider.Statistic,
			AnalysisStatistic: provider.AnalysisStatistic, EvaluationStepSeconds: provider.EvaluationStepSeconds,
			ResolutionSeconds: provider.ResolutionSeconds,
		},
		EntityIDs: provider.EntityIDs,
	}
}

func (p providerIdentityProjection) equal(other providerIdentityProjection) bool {
	return p.Fields == other.Fields && maps.Equal(p.EntityIDs, other.EntityIDs)
}

func (p measurementIdentityProjection) validateUTF8() error {
	fields := p.Fields
	provider := p.Provider.Fields
	for _, value := range []string{
		fields.ProjectID, fields.Source, fields.ScenarioID, fields.Host, fields.Service,
		fields.MetricName, fields.Population, fields.MetricVersion, fields.Kind, fields.InputKind, fields.Unit,
		provider.Profile, provider.Name, provider.Backend, provider.MetricID, provider.Statistic,
		provider.QueryID, provider.DefinitionHash, provider.BackendProfile, provider.Mode, provider.AnalysisStatistic,
	} {
		if !utf8.ValidString(value) {
			return errors.New("measurement series identity contains invalid UTF-8")
		}
	}
	for _, labels := range []map[string]string{p.Dimensions, p.Provider.EntityIDs} {
		for key, value := range labels {
			if !utf8.ValidString(key) || !utf8.ValidString(value) {
				return errors.New("measurement series identity labels contain invalid UTF-8")
			}
		}
	}
	for _, step := range p.Derivation {
		if !utf8.ValidString(step.Operation) || !utf8.ValidString(step.Algorithm) {
			return errors.New("measurement series identity derivation contains invalid UTF-8")
		}
	}
	return nil
}

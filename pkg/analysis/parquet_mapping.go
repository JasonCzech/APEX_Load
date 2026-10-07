package analysis

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// ParquetColumnPath paths are individual schema components, never expressions.
type ParquetColumnPath []string
type ParquetMeasure struct {
	Value      ParquetColumnPath `json:"value"`
	Name       string            `json:"name,omitempty"`
	NameColumn ParquetColumnPath `json:"name_column,omitempty"`
	Kind       string            `json:"kind"`
	Unit       string            `json:"unit"`
	// Summary declaration: the statistic each value already is, the source
	// window it covers (the timestamp is the window start), and optional
	// per-window sample count and sample sum columns. Summary-only.
	Statistic         string            `json:"statistic,omitempty"`
	WindowSeconds     int               `json:"window_seconds,omitempty"`
	SampleCountColumn ParquetColumnPath `json:"sample_count_column,omitempty"`
	SampleSumColumn   ParquetColumnPath `json:"sample_sum_column,omitempty"`
	// Histogram declares bucket bounds and count conventions. Histogram-only.
	Histogram *ParquetHistogram `json:"histogram,omitempty"`
}
type ParquetMapping struct {
	Version           int                     `json:"version"`
	Timestamp         ParquetColumnPath       `json:"timestamp"`
	TimestampEncoding string                  `json:"timestamp_encoding"` // timestamp (local wall time), timestamp_offset (absolute), unix_s/ms/us/ns
	Timezone          string                  `json:"timezone"`
	Measures          []ParquetMeasure        `json:"measures"`
	Identity          *ParquetIdentityMapping `json:"identity,omitempty"`
}

// ValidateDefinition checks finite mapping syntax without inventing a file
// schema. Applying the mapping still validates every actual Parquet schema.
func (m ParquetMapping) ValidateDefinition() error {
	if err := m.ValidateDisclosure(); err != nil {
		return err
	}
	if len(m.Measures) < 1 || len(m.Measures) > 64 || m.Identity != nil && len(m.Identity.Dimensions) > maxDimensions {
		return errors.New("mapping definition exceeds collection bounds")
	}
	schema := ParquetSchemaPreview{Columns: make([]ParquetColumn, 0, 1+4*len(m.Measures)+3+maxDimensions)}
	for _, measure := range m.Measures {
		if measure.Histogram != nil && (len(measure.Histogram.CountColumns) > MaxHistogramBounds || len(measure.Histogram.Bounds) > MaxHistogramBounds) {
			return errors.New("mapping definition exceeds histogram bounds")
		}
	}
	add := func(path ParquetColumnPath) error {
		if len(path) < 1 || len(path) > 16 {
			return errors.New("mapping definition column path exceeds bounds")
		}
		for _, part := range path {
			if part == "" || len(part) > 256 || !utf8.ValidString(part) || strings.ContainsRune(part, 0) {
				return errors.New("invalid mapping definition column path")
			}
		}
		schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: path})
		return nil
	}
	if err := add(m.Timestamp); err != nil {
		return err
	}
	for _, measure := range m.Measures {
		if err := add(measure.Value); err != nil {
			return err
		}
		for _, path := range append([]ParquetColumnPath{measure.NameColumn, measure.SampleCountColumn, measure.SampleSumColumn}, measure.Histogram.Paths()...) {
			if len(path) != 0 {
				if err := add(path); err != nil {
					return err
				}
			}
		}
	}
	if m.Identity != nil {
		for _, path := range []ParquetColumnPath{m.Identity.Source, m.Identity.Host, m.Identity.Service} {
			if len(path) != 0 {
				if err := add(path); err != nil {
					return err
				}
			}
		}
		for _, dimension := range m.Identity.Dimensions {
			if err := add(dimension.Value); err != nil {
				return err
			}
		}
	}
	return m.Validate(schema)
}

func (m ParquetMapping) Validate(schema ParquetSchemaPreview) error {
	if err := m.ValidateDisclosure(); err != nil {
		return err
	}
	if err := ValidateParquetSQLSchema(schema); err != nil {
		return err
	}
	if (m.Version != 1 && m.Version != 2) || m.Version == 1 && m.Identity != nil || len(m.Measures) < 1 || len(m.Measures) > 64 {
		return errors.New("mapping version 1 or 2 with 1-64 measures is required; identities require version 2")
	}
	if m.Timezone == "" {
		return errors.New("explicit timestamp timezone is required")
	}
	if _, err := time.LoadLocation(m.Timezone); err != nil {
		return errors.New("invalid timestamp timezone")
	}
	switch m.TimestampEncoding {
	case "timestamp", "timestamp_offset", "unix_s", "unix_ms", "unix_us", "unix_ns":
	default:
		return errors.New("unsupported timestamp encoding")
	}
	check := func(path ParquetColumnPath) error {
		if len(path) == 0 || len(path) > 16 {
			return errors.New("column path required")
		}
		for _, part := range path {
			if part == "" || len(part) > 256 || !utf8.ValidString(part) || strings.ContainsRune(part, 0) {
				return errors.New("column path components must contain 1-256 non-NUL bytes")
			}
		}
		for _, c := range schema.Columns {
			if slicesEqual(c.PathComponents, path) {
				if c.Repeated {
					return errors.New("repeated fields cannot be mapped to scalar measurements")
				}
				return nil
			}
		}
		return errors.New("mapping column is absent from schema")
	}
	if err := check(m.Timestamp); err != nil {
		return err
	}
	if err := m.Identity.validate(check); err != nil {
		return err
	}
	// A constant metric name is unique, except that summary measures may share
	// one name when each declares a different statistic (p50/p95/p99 columns
	// of one latency). The query's aggregation then selects the statistic.
	seen := map[string][]string{}
	long := map[string]bool{}
	for _, measure := range m.Measures {
		if err := check(measure.Value); err != nil {
			return err
		}
		if measure.Name != "" && len(measure.NameColumn) > 0 {
			return errors.New("choose a constant metric name or name column")
		}
		if len(measure.NameColumn) > 0 {
			if err := check(measure.NameColumn); err != nil {
				return err
			}
			identity, _ := json.Marshal(struct{ Value, Name ParquetColumnPath }{measure.Value, measure.NameColumn})
			if long[string(identity)] {
				return errors.New("duplicate long-table measurement")
			}
			long[string(identity)] = true
		} else if strings.TrimSpace(measure.Name) == "" || len(measure.Name) > 256 || !utf8.ValidString(measure.Name) || strings.ContainsRune(measure.Name, 0) || !distinctWideMeasure(seen[measure.Name], measure) {
			return errors.New("wide metric names must be nonempty, bounded, and unique")
		}
		if len(measure.NameColumn) == 0 {
			seen[measure.Name] = append(seen[measure.Name], measure.Statistic)
		}
		switch measure.Kind {
		case KindGauge, KindCounter, KindObservation, KindSummary, KindHistogram:
		default:
			return errors.New("unsupported scalar measurement semantics")
		}
		if err := measure.validateSummaryFields(check); err != nil {
			return err
		}
		if err := measure.validateHistogram(check, m.reservedColumns()...); err != nil {
			return err
		}
		if _, _, err := NormalizeUnit(measure.Unit); err != nil {
			return err
		}
	}
	return nil
}

// distinctWideMeasure admits a constant name already used only by declared
// summaries with other statistics.
func distinctWideMeasure(existing []string, measure ParquetMeasure) bool {
	if len(existing) == 0 {
		return true
	}
	if !measure.declaredSummary() {
		return false
	}
	for _, statistic := range existing {
		if statistic == "" || statistic == measure.Statistic {
			return false
		}
	}
	return true
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func NormalizeUnit(unit string) (string, float64, error) {
	switch unit {
	case "observations":
		return "count", 1, nil
	case "observations/s":
		return "count/s", 1, nil
	case "%":
		return "percent", 1, nil
	case "s":
		return "ms", 1000, nil
	case "us":
		return "ms", 0.001, nil
	case "ns":
		return "ms", 0.000001, nil
	case "ms", "bytes", "count", "percent", "ratio", "bytes/s", "count/s", "cores", "score":
		return unit, 1, nil
	case "KiB":
		return "bytes", 1024, nil
	case "MiB":
		return "bytes", 1024 * 1024, nil
	case "GiB":
		return "bytes", 1024 * 1024 * 1024, nil
	}
	return "", 0, errors.New("unsupported measurement unit")
}

// ParquetRejectionSummary identifies invalid measurements without disclosing source values.
// Rows contains up to 100 zero-based row ordinals; the durable rejection artifact
// contains the complete set when rejected measurements are explicitly accepted.
type ParquetRejectionSummary struct {
	File    int     `json:"file"`
	Measure int     `json:"measure"`
	Count   int64   `json:"count"`
	Rows    []int64 `json:"rows"`
}

// reservedColumns are the timestamp and identity columns of the mapping.
func (m ParquetMapping) reservedColumns() []ParquetColumnPath {
	reserved := []ParquetColumnPath{m.Timestamp}
	if m.Identity != nil {
		reserved = append(reserved, m.Identity.Source, m.Identity.Host, m.Identity.Service)
		for _, dimension := range m.Identity.Dimensions {
			reserved = append(reserved, dimension.Value)
		}
	}
	return reserved
}

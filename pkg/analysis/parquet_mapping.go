package analysis

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ParquetColumnPath paths are individual schema components, never expressions.
type ParquetColumnPath []string
type ParquetMeasure struct {
	Value      ParquetColumnPath `json:"value"`
	Name       string            `json:"name,omitempty"`
	NameColumn ParquetColumnPath `json:"name_column,omitempty"`
	Kind       string            `json:"kind"`
	Unit       string            `json:"unit"`
}
type ParquetMapping struct {
	Version           int               `json:"version"`
	Timestamp         ParquetColumnPath `json:"timestamp"`
	TimestampEncoding string            `json:"timestamp_encoding"` // timestamp (local wall time), timestamp_offset (absolute), unix_s/ms/us/ns
	Timezone          string            `json:"timezone"`
	Measures          []ParquetMeasure  `json:"measures"`
}

func (m ParquetMapping) Validate(schema ParquetSchemaPreview) error {
	if m.Version != 1 || len(m.Measures) < 1 || len(m.Measures) > 64 {
		return errors.New("mapping version 1 with 1-64 measures is required")
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
			if part == "" || len(part) > 256 || strings.ContainsRune(part, 0) {
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
	seen := map[string]bool{}
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
		} else if strings.TrimSpace(measure.Name) == "" || len(measure.Name) > 256 || seen[measure.Name] {
			return errors.New("wide metric names must be nonempty, bounded, and unique")
		}
		seen[measure.Name] = true
		switch measure.Kind {
		case KindGauge, KindCounter, KindObservation, KindSummary:
		default:
			return errors.New("unsupported scalar measurement semantics")
		}
		if _, _, err := NormalizeUnit(measure.Unit); err != nil {
			return err
		}
	}
	return nil
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
	case "s":
		return "ms", 1000, nil
	case "us":
		return "ms", 0.001, nil
	case "ns":
		return "ms", 0.000001, nil
	case "ms", "bytes", "count", "percent", "ratio", "bytes/s", "count/s", "cores":
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

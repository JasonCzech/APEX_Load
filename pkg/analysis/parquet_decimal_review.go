package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ParquetDecimalRisk reports potential conversion loss from schema metadata.
// It does not claim that a particular row loses precision or count such rows.
type ParquetDecimalRisk struct {
	Column    ParquetColumnPath `json:"column"`
	Precision int32             `json:"precision"`
	Scale     int32             `json:"scale"`
	Reason    string            `json:"reason"`
}

const maxParquetDecimalRisks = 64

// ParquetDecimalRisks examines numeric measurement roles only. Timestamp and
// textual identity conversion have separate contracts. Callers bind this
// disclosure to their immutable input hash and exact mapping revision.
func ParquetDecimalRisks(mapping ParquetMapping, schema ParquetSchemaPreview) ([]ParquetDecimalRisk, error) {
	if len(schema.Columns) > maxParquetColumns || len(mapping.Measures) > 64 {
		return nil, errors.New("decimal review exceeds schema or mapping bound")
	}
	if len(mapping.Measures) > 0 {
		if err := ValidateParquetSQLSchema(schema); err != nil {
			return nil, err
		}
	}
	used := map[string]bool{}
	for _, measure := range mapping.Measures {
		paths := []ParquetColumnPath{measure.Value, measure.SampleCountColumn, measure.SampleSumColumn}
		if measure.Histogram != nil {
			if len(measure.Histogram.CountColumns) > MaxHistogramBounds {
				return nil, errors.New("decimal review exceeds histogram bound")
			}
			paths = append(paths, measure.Histogram.Paths()...)
		}
		for _, path := range paths {
			if len(path) == 0 {
				continue
			}
			if err := ValidateParquetProjectedColumn(path); err != nil {
				return nil, err
			}
			key, err := json.Marshal(path)
			if err != nil {
				return nil, err
			}
			if !used[string(key)] && len(used) == maxParquetColumns {
				return nil, errors.New("decimal review exceeds mapped column bound")
			}
			used[string(key)] = true
		}
	}
	risks := []ParquetDecimalRisk{}
	seen := map[string]bool{}
	for _, c := range schema.Columns {
		key, err := json.Marshal(c.PathComponents)
		if err != nil {
			return nil, err
		}
		if seen[string(key)] {
			return nil, errors.New("duplicate decimal review schema path")
		}
		seen[string(key)] = true
		if !used[string(key)] {
			continue
		}
		delete(used, string(key))
		decimal := c.ConvertedType == "DECIMAL" || c.LogicalType != nil && c.LogicalType.DECIMAL != nil
		if !decimal {
			continue
		}
		precision, scale := c.Precision, c.Scale
		if c.LogicalType != nil && c.LogicalType.DECIMAL != nil {
			logical := c.LogicalType.DECIMAL
			if precision != nil && *precision != logical.Precision || scale != nil && *scale != logical.Scale {
				return nil, errors.New("conflicting decimal precision metadata")
			}
			precision, scale = &logical.Precision, &logical.Scale
		}
		if precision == nil || scale == nil || *precision < 1 || *precision > 38 || *scale < 0 || *scale > *precision {
			return nil, errors.New("unsupported decimal precision metadata")
		}
		reason := ""
		if *scale > 0 {
			reason = "fractional_decimal_may_round_to_float64"
		} else if *precision > 15 {
			reason = "decimal_digits_may_exceed_float64_integer_precision"
		}
		if reason == "" {
			continue
		}
		if len(risks) == maxParquetDecimalRisks {
			return nil, fmt.Errorf("decimal review exceeds %d affected columns", maxParquetDecimalRisks)
		}
		risks = append(risks, ParquetDecimalRisk{Column: append(ParquetColumnPath(nil), c.PathComponents...), Precision: *precision, Scale: *scale, Reason: reason})
	}
	if len(used) != 0 {
		return nil, errors.New("mapped decimal review column is absent")
	}
	return risks, nil
}

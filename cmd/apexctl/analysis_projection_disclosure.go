package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Historical metadata retains its existing fields and omitted paths. Only a
// bounded exact projection of selected source paths is admitted here; this is
// not modern complete-definition validation and never rewrites the request.
func analysisCLIHistoricalProjectionDisclosure(object map[string]json.RawMessage) error {
	for key := range object {
		for _, field := range []string{"timestamp", "measures", "identity"} {
			if key != field && strings.EqualFold(key, field) {
				return errors.New("ambiguous historical projected column metadata")
			}
		}
	}
	m := analysis.ParquetMapping{}
	if raw, exists := object["timestamp"]; exists {
		path, err := analysisCLIHistoricalColumnPath(raw)
		if err != nil {
			return err
		}
		m.Timestamp = path
	}
	if raw, exists := object["measures"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := analysisCLIHistoricalArray(raw, 64, func(item json.RawMessage) error {
			fields, err := analysisCLIObject(item, 128<<10, nil)
			if err != nil {
				return err
			}
			projected := []string{"value", "name_column", "sample_count_column", "sample_sum_column"}
			for key := range fields {
				for _, field := range projected {
					if key != field && strings.EqualFold(key, field) {
						return errors.New("ambiguous historical projected measurement metadata")
					}
				}
			}
			measure := analysis.ParquetMeasure{}
			for _, field := range projected {
				if raw, ok := fields[field]; ok {
					path, err := analysisCLIHistoricalColumnPath(raw)
					if err != nil {
						return err
					}
					switch field {
					case "value":
						measure.Value = path
					case "name_column":
						measure.NameColumn = path
					case "sample_count_column":
						measure.SampleCountColumn = path
					default:
						measure.SampleSumColumn = path
					}
				}
			}
			m.Measures = append(m.Measures, measure)
			return nil
		}); err != nil {
			return err
		}
	}
	if raw, exists := object["identity"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		fields, err := analysisCLIObject(raw, 128<<10, nil)
		if err != nil {
			return err
		}
		for key := range fields {
			for _, field := range []string{"source", "host", "service", "dimensions"} {
				if key != field && strings.EqualFold(key, field) {
					return errors.New("ambiguous historical projected identity metadata")
				}
			}
		}
		identity := &analysis.ParquetIdentityMapping{}
		for _, field := range []string{"source", "host", "service"} {
			if raw, exists := fields[field]; exists {
				path, err := analysisCLIHistoricalColumnPath(raw)
				if err != nil {
					return err
				}
				switch field {
				case "source":
					identity.Source = path
				case "host":
					identity.Host = path
				case "service":
					identity.Service = path
				}
			}
		}
		if raw, exists := fields["dimensions"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if err := analysisCLIHistoricalArray(raw, 16, func(item json.RawMessage) error {
				fields, err := analysisCLIObject(item, 128<<10, nil)
				if err != nil {
					return err
				}
				for key := range fields {
					if key != "value" && strings.EqualFold(key, "value") || key != "name" && strings.EqualFold(key, "name") {
						return errors.New("ambiguous historical projected dimension metadata")
					}
				}
				path, err := analysisCLIHistoricalColumnPath(fields["value"])
				if err != nil {
					return err
				}
				var name string
				if raw, exists := fields["name"]; exists {
					name, err = analysisCLIText(raw, 128, false)
					if err != nil {
						return err
					}
				}
				identity.Dimensions = append(identity.Dimensions, analysis.ParquetDimensionMapping{Name: name, Value: path})
				return nil
			}); err != nil {
				return err
			}
		}
		m.Identity = identity
	}
	return m.ValidateDisclosure()
}

func analysisCLIHistoricalColumnPath(raw json.RawMessage) (analysis.ParquetColumnPath, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	// At most sixteen 256-byte components, including worst-case JSON escapes.
	if len(raw) > 16*(256*6+3)+2 {
		return nil, errors.New("historical projected column path exceeds bounds")
	}
	path := analysis.ParquetColumnPath{}
	err := analysisCLIHistoricalArray(raw, 16, func(item json.RawMessage) error {
		part, err := analysisCLIText(item, 256, false)
		if err != nil {
			return err
		}
		path = append(path, part)
		return nil
	})
	return path, err
}

func analysisCLIHistoricalArray(raw json.RawMessage, maximum int, visit func(json.RawMessage) error) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return errors.New("historical projected fields require bounded arrays")
	}
	count := 0
	for d.More() {
		if count == maximum {
			return errors.New("historical projected fields exceed population bounds")
		}
		count++
		var item json.RawMessage
		if err := d.Decode(&item); err != nil {
			return err
		}
		if err := visit(item); err != nil {
			return err
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') {
		return errors.New("invalid historical projected field array")
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("historical projected field array has trailing input")
	}
	return nil
}

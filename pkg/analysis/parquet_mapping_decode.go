package analysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const maxParquetMappingDefinitionBytes = 128 << 10

// DecodeParquetMappingDefinition admits new authored/saved mappings. Historical
// reads keep their prior decoding contract. Every field, collection and scalar
// is admitted before typed slices can grow; requested unknown options, aliases
// and duplicate decoded names never silently disappear or overwrite a value.
func DecodeParquetMappingDefinition(data []byte) (ParquetMapping, error) {
	var out ParquetMapping
	if len(data) == 0 || len(data) > maxParquetMappingDefinitionBytes || !utf8.Valid(data) || !normalizedIdentityUnicodeEscapes(string(data)) {
		return out, errors.New("invalid or excessive mapping definition JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := walkParquetMappingObject(d, "mapping"); err != nil {
		return out, err
	}
	if _, err := d.Token(); err != io.EOF {
		return out, errors.New("mapping definition has trailing input")
	}
	// Preflight above admits exact canonical keys; Unmarshal cannot now ignore
	// unknown/case aliases or allocate an unbounded nested population.
	if err := json.Unmarshal(data, &out); err != nil {
		return ParquetMapping{}, errors.New("invalid typed mapping definition")
	}
	if err := out.ValidateDefinition(); err != nil {
		return ParquetMapping{}, err
	}
	return out, nil
}

func walkParquetMappingObject(d *json.Decoder, kind string) error {
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("mapping fields require objects")
	}
	var seen [9]string
	count := 0
	for d.More() {
		if count == len(seen) {
			return errors.New("mapping object exceeds field bound")
		}
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return errors.New("invalid mapping field")
		}
		for i := 0; i < count; i++ {
			if seen[i] == key {
				return errors.New("duplicate mapping field")
			}
		}
		seen[count], count = key, count+1
		switch {
		case kind == "mapping" && key == "version", kind == "measure" && key == "window_seconds", kind == "histogram" && key == "window_seconds":
			token, err := d.Token()
			if _, ok := token.(json.Number); err != nil || !ok {
				return errors.New("mapping version and summary window must be integers")
			}
		case kind == "mapping" && key == "identity":
			err = walkParquetMappingObject(d, "identity")
		case kind == "mapping" && key == "measures":
			err = walkParquetMappingArray(d, "measure", 64)
		case kind == "identity" && key == "dimensions":
			err = walkParquetMappingArray(d, "dimension", maxDimensions)
		case kind == "measure" && key == "histogram":
			err = walkParquetMappingObject(d, "histogram")
		case kind == "histogram" && key == "bounds":
			err = walkParquetMappingArray(d, "bound", MaxHistogramBounds)
		case kind == "histogram" && key == "count_columns":
			err = walkParquetMappingArray(d, "path", MaxHistogramBounds)
		case kind == "mapping" && key == "timestamp", kind == "identity" && (key == "source" || key == "host" || key == "service"), kind == "measure" && (key == "value" || key == "name_column" || key == "sample_count_column" || key == "sample_sum_column"), kind == "dimension" && key == "value", kind == "histogram" && key == "bound_column":
			err = walkParquetMappingArray(d, "scalar", 16)
		case kind == "mapping" && (key == "timestamp_encoding" || key == "timezone"), kind == "measure" && (key == "name" || key == "kind" || key == "unit" || key == "statistic"), kind == "dimension" && key == "name", kind == "histogram" && (key == "bucket_counts" || key == "temporality"):
			limit := 256
			if kind == "dimension" {
				limit = maxDimensionBytes
			}
			err = walkParquetMappingScalar(d, limit)
		default:
			return errors.New("unsupported mapping field")
		}
		if err != nil {
			return err
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return errors.New("invalid mapping object")
	}
	return nil
}

func walkParquetMappingArray(d *json.Decoder, kind string, limit int) error {
	token, err := d.Token()
	if err != nil || token != json.Delim('[') {
		return errors.New("mapping collection must be a list")
	}
	count := 0
	for d.More() {
		if count == limit {
			return errors.New("mapping collection exceeds population bound")
		}
		count++
		if kind == "scalar" {
			err = walkParquetMappingScalar(d, 256)
		} else if kind == "bound" {
			err = walkParquetMappingScalar(d, 64)
		} else if kind == "path" {
			err = walkParquetMappingArray(d, "scalar", 16)
		} else {
			err = walkParquetMappingObject(d, kind)
		}
		if err != nil {
			return err
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim(']') {
		return errors.New("invalid mapping collection")
	}
	return nil
}

func walkParquetMappingScalar(d *json.Decoder, limit int) error {
	token, err := d.Token()
	text, ok := token.(string)
	if err != nil || !ok || len(text) > limit || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return errors.New("mapping scalars must be bounded non-NUL Unicode text")
	}
	return nil
}

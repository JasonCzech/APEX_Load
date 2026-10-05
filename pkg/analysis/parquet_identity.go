package analysis

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Identity mappings name scalar source columns only. Ownership, managed run and
// project associations are never supplied by imported row values.
type ParquetIdentityMapping struct {
	Source     ParquetColumnPath         `json:"source,omitempty"`
	Host       ParquetColumnPath         `json:"host,omitempty"`
	Service    ParquetColumnPath         `json:"service,omitempty"`
	Dimensions []ParquetDimensionMapping `json:"dimensions,omitempty"`
}
type ParquetDimensionMapping struct {
	Name  string            `json:"name"`
	Value ParquetColumnPath `json:"value"`
}

// ValidateDisclosure admits only finite analytical identity paths that cannot
// expose the credential columns suppressed by protected previews. It also
// guards persisted mappings before querying existing normalized artifacts.
// Output label names and ordinary source values are not reclassified or rewritten.
func (m *ParquetIdentityMapping) ValidateDisclosure() error {
	if m == nil {
		return nil
	}
	if len(m.Dimensions) > maxDimensions {
		return errors.New("imported identity exceeds dimension bound")
	}

	for _, path := range []ParquetColumnPath{m.Source, m.Host, m.Service} {
		if len(path) != 0 {
			if err := ValidateParquetProjectedColumn(path); err != nil {
				return err
			}
		}
	}
	for _, dimension := range m.Dimensions {
		if err := ValidateParquetProjectedColumn(dimension.Value); err != nil {
			return err
		}
	}
	return nil
}

func (m *ParquetIdentityMapping) validate(check func(ParquetColumnPath) error) error {
	if err := m.ValidateDisclosure(); err != nil {
		return err
	}
	if m == nil {
		return nil
	}
	if len(m.Dimensions) > maxDimensions {
		return errors.New("imported identity exceeds dimension bound")
	}
	for _, path := range []ParquetColumnPath{m.Source, m.Host, m.Service} {
		if len(path) > 0 {
			if err := check(path); err != nil {
				return err
			}
		}
	}
	seen := map[string]bool{}
	for _, dimension := range m.Dimensions {
		if !ValidMetricDimension(dimension.Name, "") || seen[dimension.Name] {
			return errors.New("dimension names must be bounded, non-NUL, unique UTF-8 text")
		}
		seen[dimension.Name] = true
		if err := check(dimension.Value); err != nil {
			return err
		}
	}
	return nil
}
func ValidMetricDimension(name, value string) bool {
	return name != "" && len(name) <= maxDimensionBytes && len(value) <= maxDimensionBytes && utf8.ValidString(name) && utf8.ValidString(value) && !strings.ContainsRune(name, 0) && !strings.ContainsRune(value, 0)
}

const maxNormalizedDimensionsJSONBytes = 16*(128+128)*6 + 16*6 + 2

// DecodeNormalizedDimensions validates the entire bounded object before it
// allocates a result map. Duplicate names, non-string values and invalid Unicode
// escapes never acquire an implicit replacement label or overwrite a prior key.
func DecodeNormalizedDimensions(text string) (map[string]string, error) {
	if len(text) > maxNormalizedDimensionsJSONBytes || !utf8.ValidString(text) || !normalizedIdentityUnicodeEscapes(text) {
		return nil, errors.New("invalid or excessive normalized dimensions")
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("normalized dimensions must be an object")
	}
	var pairs [maxDimensions][2]string
	count := 0
	for decoder.More() {
		if count == maxDimensions {
			return nil, errors.New("normalized dimensions exceed population bound")
		}
		keyToken, err := decoder.Token()
		key, keyOK := keyToken.(string)
		valueToken, valueErr := decoder.Token()
		value, valueOK := valueToken.(string)
		if err != nil || valueErr != nil || !keyOK || !valueOK || !ValidMetricDimension(key, value) {
			return nil, errors.New("invalid normalized dimension scalar")
		}
		for i := 0; i < count; i++ {
			if pairs[i][0] == key {
				return nil, errors.New("duplicate normalized dimension")
			}
		}
		pairs[count] = [2]string{key, value}
		count++
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, errors.New("invalid normalized dimension object")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing normalized dimensions")
	}
	out := make(map[string]string, count)
	for i := 0; i < count; i++ {
		out[pairs[i][0]] = pairs[i][1]
	}
	return out, nil
}
func normalizedIdentityUnicodeEscapes(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' {
			continue
		}
		i++
		if i >= len(text) {
			return false
		}
		if text[i] != 'u' {
			continue
		}
		if i+4 >= len(text) {
			return false
		}
		code, err := strconv.ParseUint(text[i+1:i+5], 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(text) || text[i+1] != '\\' || text[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(text[i+3:i+7], 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

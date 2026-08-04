package protocol

import (
	"errors"
	"fmt"
	"strings"
)

// ErrExtractionMiss marks the absence of a requested response value. Adapter
// configuration and parsing errors must not wrap this sentinel: on_miss is a
// disposition for missing data, not permission to ignore an invalid extractor.
var ErrExtractionMiss = errors.New("extraction found no match")

// MarkExtractionMiss preserves the adapter's useful cause while classifying it
// for ApplyExtraction.
func MarkExtractionMiss(cause error) error {
	if cause == nil {
		return ErrExtractionMiss
	}
	return fmt.Errorf("%w: %v", ErrExtractionMiss, cause)
}

// Extraction limits protect the VUser variable store. Extracted values persist
// across iterations, so accepting response-sized values or data-driven key
// cardinality would turn ordinary remote responses into generator heap growth.
const (
	MaxExtractionEntries    = 256
	MaxExtractionKeyBytes   = 4 << 10
	MaxExtractionValueBytes = 16 << 10
	MaxExtractionBytes      = 64 << 10
)

// StoreExtraction adds one value while enforcing the per-result extraction
// budget. Existing keys may be replaced as long as the replacement still fits.
func StoreExtraction(result *Result, key, value string) error {
	if result == nil {
		return fmt.Errorf("extraction result is required")
	}
	if len(key) == 0 {
		return fmt.Errorf("extraction name is required")
	}
	if len(key) > MaxExtractionKeyBytes {
		return fmt.Errorf("extraction name exceeds %d bytes", MaxExtractionKeyBytes)
	}
	if len(value) > MaxExtractionValueBytes {
		return fmt.Errorf("extraction %q exceeds %d bytes", key, MaxExtractionValueBytes)
	}
	if result.Extractions == nil {
		result.Extractions = make(map[string]string)
	}
	old, exists := result.Extractions[key]
	if !exists && len(result.Extractions) >= MaxExtractionEntries {
		return fmt.Errorf("extractions exceed %d entries", MaxExtractionEntries)
	}
	total := 0
	for existingKey, existingValue := range result.Extractions {
		total += len(existingKey) + len(existingValue)
	}
	if exists {
		total -= len(key) + len(old)
	}
	if len(key)+len(value) > MaxExtractionBytes-total {
		return fmt.Errorf("extractions exceed %d bytes", MaxExtractionBytes)
	}
	result.Extractions[key] = value
	return nil
}

// ApplyExtraction gives non-HTTP adapters the same on_miss contract as HTTP:
// ignore leaves the variable unset, empty publishes an empty value, and fail
// turns the extraction miss into an action error.
func ApplyExtraction(result *Result, extractor Extractor, value string, extractionErr error) error {
	if extractionErr == nil {
		return StoreExtraction(result, extractor.Name, value)
	}
	if !errors.Is(extractionErr, ErrExtractionMiss) {
		return fmt.Errorf("extractor %q failed: %w", extractor.Name, extractionErr)
	}
	switch strings.ToLower(strings.TrimSpace(extractor.OnMiss)) {
	case "fail":
		return fmt.Errorf("extractor %q found no match: %w", extractor.Name, extractionErr)
	case "empty":
		return StoreExtraction(result, extractor.Name, "")
	default:
		return nil
	}
}

// ValidateExtractions applies the same checks to adapter results that were
// populated without StoreExtraction. It is a final engine-boundary defense.
func ValidateExtractions(values map[string]string) error {
	if len(values) > MaxExtractionEntries {
		return fmt.Errorf("extractions exceed %d entries", MaxExtractionEntries)
	}
	total := 0
	for key, value := range values {
		if len(key) == 0 {
			return fmt.Errorf("extraction name is required")
		}
		if len(key) > MaxExtractionKeyBytes {
			return fmt.Errorf("extraction name exceeds %d bytes", MaxExtractionKeyBytes)
		}
		if len(value) > MaxExtractionValueBytes {
			return fmt.Errorf("extraction %q exceeds %d bytes", key, MaxExtractionValueBytes)
		}
		total += len(key) + len(value)
		if total > MaxExtractionBytes {
			return fmt.Errorf("extractions exceed %d bytes", MaxExtractionBytes)
		}
	}
	return nil
}

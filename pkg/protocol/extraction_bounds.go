package protocol

import "fmt"

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

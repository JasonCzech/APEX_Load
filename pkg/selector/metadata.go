package selector

import "strings"

// Metadata keys shared by every capture surface (embedded recorder, TruClient
// importer, Chrome Recorder importer) and read by replay self-heal.
const (
	MetadataCandidatesKey = "source_selector_candidates"
	MetadataFallbacksKey  = "source_selector_fallbacks"
	MetadataConfidenceKey = "source_selector_confidence"
)

// FromMetadataValue decodes selector candidates from an action metadata value.
// In-process the value is []Candidate; after a script round-trips through JSON
// storage it becomes []any of map[string]any. Both forms decode; anything else
// yields nil.
func FromMetadataValue(value any) []Candidate {
	switch typed := value.(type) {
	case []Candidate:
		out := make([]Candidate, len(typed))
		copy(out, typed)
		return out
	case []any:
		out := make([]Candidate, 0, len(typed))
		for _, item := range typed {
			mapped, ok := item.(map[string]any)
			if !ok {
				continue
			}
			candidate := Candidate{
				Kind:   stringValue(mapped["kind"]),
				Value:  stringValue(mapped["value"]),
				Reason: stringValue(mapped["reason"]),
			}
			switch confidence := mapped["confidence"].(type) {
			case float64:
				candidate.Confidence = int(confidence)
			case int:
				candidate.Confidence = confidence
			}
			if candidate.Value != "" {
				out = append(out, candidate)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

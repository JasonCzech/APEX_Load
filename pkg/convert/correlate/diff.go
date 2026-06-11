package correlate

import "sort"

// DiffClass classifies a detected correlation by comparing two recordings of
// the same flow.
type DiffClass string

const (
	// DiffDynamic: the value changed between recordings — a genuine session
	// token that must be correlated.
	DiffDynamic DiffClass = "dynamic"
	// DiffStatic: the value was identical in both recordings — likely a fixed
	// application constant; correlating it is unnecessary.
	DiffStatic DiffClass = "static"
	// DiffUnknown: the source step or candidate did not appear in the second
	// recording, so the classification could not be confirmed.
	DiffUnknown DiffClass = "unknown"
)

// DiffResult is one correlation from the first recording, classified against
// the second recording with an adjusted confidence.
type DiffResult struct {
	Correlation
	Class DiffClass `json:"class"`
	// ValueB is the (truncated) value the same candidate carried in the second
	// recording; empty for DiffUnknown.
	ValueB string `json:"value_b,omitempty"`
	// DiffReason explains the classification for the import-review UI.
	DiffReason string `json:"diff_reason"`
}

// DiffRecordings compares two recordings of the same flow and classifies each
// detected correlation: values that rotate between sessions are truly dynamic
// (confidence raised), identical values are static fixtures (confidence cut),
// and values missing from the second recording are flagged for boundary
// review. This is the strongest deterministic signal available for cutting
// correlation false positives before script generation.
func DiffRecordings(a, b []Observation) []DiffResult {
	correlations := Detect(a)
	if len(correlations) == 0 {
		return nil
	}
	candidatesByID := candidateIndex(b)

	results := make([]DiffResult, 0, len(correlations))
	for _, correlation := range correlations {
		result := DiffResult{Correlation: correlation}
		valueB, found := lookupCandidateValue(candidatesByID[correlation.FoundInID], correlation)
		switch {
		case !found:
			result.Class = DiffUnknown
			result.DiffReason = "The source step or extractor expression was not observed in the second recording; verify the extractor boundary before trusting this correlation."
		case valueB != correlation.Value:
			result.Class = DiffDynamic
			result.ValueB = Truncate(valueB, 80)
			result.Confidence = clampConfidence(correlation.Confidence + 0.15)
			result.Reasons = append(result.Reasons, "value rotated between recordings")
			result.DiffReason = "The value differs between the two recordings — a genuine per-session dynamic value."
		default:
			result.Class = DiffStatic
			result.ValueB = Truncate(valueB, 80)
			result.Confidence = clampConfidence(correlation.Confidence * 0.35)
			result.Reasons = append(result.Reasons, "identical across recordings")
			result.DiffReason = "The value is identical in both recordings — likely an application constant that does not need correlation."
		}
		results = append(results, result)
	}

	sort.SliceStable(results, func(i, j int) bool {
		if rank := diffClassRank(results[i].Class) - diffClassRank(results[j].Class); rank != 0 {
			return rank < 0
		}
		return results[i].Confidence > results[j].Confidence
	})
	return results
}

// candidateIndex extracts response candidates per observation ID from the
// second recording so each correlation can look up its counterpart.
func candidateIndex(observations []Observation) map[int][]Candidate {
	index := make(map[int][]Candidate, len(observations))
	for _, observation := range observations {
		if observation.ResponseBody == "" && len(observation.ResponseHeaders) == 0 {
			continue
		}
		index[observation.ID] = Candidates(observation.ResponseBody, observation.ResponseHeaders)
	}
	return index
}

// lookupCandidateValue finds the candidate matching a correlation's extractor
// identity (source + expression, falling back to name) in the counterpart
// step's candidates.
func lookupCandidateValue(candidates []Candidate, correlation Correlation) (string, bool) {
	for _, candidate := range candidates {
		if candidate.Source == correlation.Source && candidate.Expression == correlation.Expression {
			return candidate.Value, true
		}
	}
	for _, candidate := range candidates {
		if candidate.Source == correlation.Source && candidate.Name == correlation.Name {
			return candidate.Value, true
		}
	}
	return "", false
}

func diffClassRank(class DiffClass) int {
	switch class {
	case DiffDynamic:
		return 0
	case DiffUnknown:
		return 1
	default:
		return 2
	}
}

package har

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	DiffClassificationDynamic   = "dynamic"
	DiffClassificationStatic    = "static"
	DiffClassificationCandidate = "candidate"
)

type DiffReport struct {
	ArtifactMode           string               `json:"artifact_mode"`
	Compared               bool                 `json:"compared"`
	BaselineCorrelations   int                  `json:"baseline_correlations"`
	ComparisonCorrelations int                  `json:"comparison_correlations"`
	DynamicCandidates      int                  `json:"dynamic_candidates"`
	StaticCandidates       int                  `json:"static_candidates"`
	UnmatchedCandidates    int                  `json:"unmatched_candidates"`
	Classifications        []DiffClassification `json:"classifications,omitempty"`
	Warnings               []string             `json:"warnings,omitempty"`
}

type DiffClassification struct {
	Name            string  `json:"name"`
	Source          string  `json:"source,omitempty"`
	Expression      string  `json:"expression,omitempty"`
	ExtractType     string  `json:"extract_type,omitempty"`
	SampleValue     string  `json:"sample_value,omitempty"`
	BaselineValue   string  `json:"baseline_value,omitempty"`
	ComparisonValue string  `json:"comparison_value,omitempty"`
	FoundInStep     int     `json:"found_in_step,omitempty"`
	UsedInSteps     []int   `json:"used_in_steps,omitempty"`
	Classification  string  `json:"classification"`
	DiffConfidence  float64 `json:"diff_confidence"`
	Reason          string  `json:"reason"`
}

// Diff compares two recordings of the same flow and classifies correlation
// candidates as dynamic when the reused value changes between runs, static
// when it is stable, or candidate when it was only observed in the baseline.
func Diff(baseline []byte, comparison []byte, opts ConvertOptions) (*DiffReport, error) {
	if len(comparison) == 0 || strings.TrimSpace(string(comparison)) == "" {
		return nil, fmt.Errorf("comparison HAR content is required")
	}
	opts.ArtifactMode = ArtifactModeDesignHARPair
	baselineResult, err := Convert(baseline, opts)
	if err != nil {
		return nil, fmt.Errorf("baseline HAR: %w", err)
	}
	comparisonResult, err := Convert(comparison, opts)
	if err != nil {
		return nil, fmt.Errorf("comparison HAR: %w", err)
	}

	comparisonByKey := make(map[string]sample.Correlation, len(comparisonResult.Correlations))
	for _, corr := range comparisonResult.Correlations {
		comparisonByKey[correlationDiffKey(corr)] = corr
	}

	report := &DiffReport{
		ArtifactMode:           ArtifactModeDesignHARPair,
		Compared:               true,
		BaselineCorrelations:   len(baselineResult.Correlations),
		ComparisonCorrelations: len(comparisonResult.Correlations),
	}
	for _, corr := range baselineResult.Correlations {
		baselineValue := correlationValue(corr)
		classification := DiffClassification{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			SampleValue: publicCorrelationValue(corr),
			// Public values come from the disclosure-safe sample. The raw
			// json:"-" value remains local to this function solely for deciding
			// whether two recordings differ.
			BaselineValue:  publicCorrelationValue(corr),
			FoundInStep:    corr.FoundInStep,
			UsedInSteps:    append([]int(nil), corr.UsedInSteps...),
			Classification: DiffClassificationCandidate,
			DiffConfidence: 0.55,
			Reason:         "value was reused later in the baseline HAR but was not matched in the comparison HAR",
		}
		if comparisonCorr, ok := comparisonByKey[correlationDiffKey(corr)]; ok {
			comparisonValue := correlationValue(comparisonCorr)
			classification.ComparisonValue = publicCorrelationValue(comparisonCorr)
			if baselineValue != "" && comparisonValue != "" && baselineValue != comparisonValue {
				classification.Classification = DiffClassificationDynamic
				classification.DiffConfidence = 0.95
				classification.Reason = "value changed between recordings and was reused later in both HARs"
				report.DynamicCandidates++
			} else {
				classification.Classification = DiffClassificationStatic
				classification.DiffConfidence = 0.4
				classification.Reason = "value was reused later but stayed constant across both HARs"
				report.StaticCandidates++
			}
		} else {
			report.UnmatchedCandidates++
		}
		report.Classifications = append(report.Classifications, classification)
	}

	sort.SliceStable(report.Classifications, func(i, j int) bool {
		left := report.Classifications[i]
		right := report.Classifications[j]
		if diffRank(left.Classification) != diffRank(right.Classification) {
			return diffRank(left.Classification) < diffRank(right.Classification)
		}
		if left.DiffConfidence != right.DiffConfidence {
			return left.DiffConfidence > right.DiffConfidence
		}
		if left.FoundInStep != right.FoundInStep {
			return left.FoundInStep < right.FoundInStep
		}
		return left.Name < right.Name
	})
	return report, nil
}

func correlationDiffKey(corr sample.Correlation) string {
	// Include the (uniquified) correlation Name so two distinct correlations that
	// share source/expression/extract_type — e.g. a cart-id and an order-id both
	// extracted via $.id from different steps — do not collide in the comparison
	// map (the last would overwrite the first, comparing every same-keyed
	// baseline correlation against one arbitrary value). Name is assigned
	// deterministically in detection order (corr_id / corr_id_2), so it stays
	// stable across baseline/comparison even when entry counts shift — unlike the
	// step indexes the key deliberately omits.
	return fmt.Sprintf("%s|%s|%s|%s",
		strings.ToLower(strings.TrimSpace(corr.Source)),
		strings.ToLower(strings.TrimSpace(corr.Expression)),
		strings.ToLower(strings.TrimSpace(corr.ExtractType)),
		strings.ToLower(strings.TrimSpace(corr.Name)))
}

func correlationValue(corr sample.Correlation) string {
	if strings.TrimSpace(corr.Value) != "" {
		return corr.Value
	}
	return corr.SampleValue
}

func publicCorrelationValue(corr sample.Correlation) string {
	if corr.SampleValue == "" {
		return ""
	}
	if credential.SensitiveName(corr.Name) || credential.SensitiveName(corr.Expression) {
		return scrub.Redacted
	}
	return scrub.Value(corr.SampleValue, scrub.Policy{})
}

func diffRank(classification string) int {
	switch classification {
	case DiffClassificationDynamic:
		return 0
	case DiffClassificationCandidate:
		return 1
	case DiffClassificationStatic:
		return 2
	default:
		return 3
	}
}

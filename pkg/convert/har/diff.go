package har

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sample"
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
		classification := DiffClassification{
			Name:           corr.Name,
			Source:         corr.Source,
			Expression:     corr.Expression,
			ExtractType:    corr.ExtractType,
			SampleValue:    corr.SampleValue,
			BaselineValue:  correlationValue(corr),
			FoundInStep:    corr.FoundInStep,
			UsedInSteps:    append([]int(nil), corr.UsedInSteps...),
			Classification: DiffClassificationCandidate,
			DiffConfidence: 0.55,
			Reason:         "value was reused later in the baseline HAR but was not matched in the comparison HAR",
		}
		if comparisonCorr, ok := comparisonByKey[correlationDiffKey(corr)]; ok {
			classification.ComparisonValue = correlationValue(comparisonCorr)
			if classification.BaselineValue != "" && classification.ComparisonValue != "" && classification.BaselineValue != classification.ComparisonValue {
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
	return fmt.Sprintf("%s|%s|%s",
		strings.ToLower(strings.TrimSpace(corr.Source)),
		strings.ToLower(strings.TrimSpace(corr.Expression)),
		strings.ToLower(strings.TrimSpace(corr.ExtractType)))
}

func correlationValue(corr sample.Correlation) string {
	if strings.TrimSpace(corr.Value) != "" {
		return corr.Value
	}
	return corr.SampleValue
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

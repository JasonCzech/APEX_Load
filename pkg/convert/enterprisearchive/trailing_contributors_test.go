package enterprisearchive

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestPreserveTrailingRequestContributorsKeepsSupportStatsReconciled(t *testing.T) {
	tests := []struct {
		name         string
		mode         SourceFunctionSupportMode
		before       ConvertStats
		wantNative   int
		wantReview   int
		wantExternal int
	}{
		{
			name:       "already review only",
			mode:       SourceFunctionSupportReviewOnly,
			before:     ConvertStats{TotalFunctions: 1, ReviewOnlyFunctions: 1, Extractors: 1},
			wantReview: 1,
		},
		{
			name:       "executable contributor becomes review only",
			mode:       SourceFunctionSupportExecutable,
			before:     ConvertStats{TotalFunctions: 1, NativeFunctions: 1, Extractors: 1},
			wantReview: 1,
		},
		{
			name:         "external contributor becomes review only",
			mode:         SourceFunctionSupportExternalRequired,
			before:       ConvertStats{TotalFunctions: 1, ExternalRequiredFunctions: 1, Extractors: 1},
			wantReview:   1,
			wantExternal: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := &ConvertResult{Script: &dsl.Script{}, Stats: test.before}
			preserveTrailingRequestContributors(result,
				[]dsl.Extractor{{Name: "token", Type: "regex", Expression: "token=(.*)"}}, nil, nil,
				[]SourceReviewMapping{{SourceFile: "Action.c", StartLine: 7, Function: "web_reg_save_param", SupportMode: string(test.mode)}})

			if result.Stats.NativeFunctions != test.wantNative || result.Stats.ReviewOnlyFunctions != test.wantReview || result.Stats.ExternalRequiredFunctions != test.wantExternal {
				t.Fatalf("support stats = native %d, review %d, external %d; want %d, %d, %d",
					result.Stats.NativeFunctions, result.Stats.ReviewOnlyFunctions, result.Stats.ExternalRequiredFunctions,
					test.wantNative, test.wantReview, test.wantExternal)
			}
			if got := result.Stats.NativeFunctions + result.Stats.ReviewOnlyFunctions + result.Stats.ExternalRequiredFunctions + result.Stats.CustomCallFunctions; got != result.Stats.TotalFunctions {
				t.Fatalf("support counters sum to %d, want TotalFunctions %d", got, result.Stats.TotalFunctions)
			}
		})
	}
}

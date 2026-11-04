package protocol

import "testing"

// A roll-up's estimate is prompt-only when every estimated hop's was, so
// calibration keeps exact completion counts and drops estimated ones.
func TestMergeLLMHopDiagnosticsKeepsPromptOnlyEstimates(t *testing.T) {
	exact := LLMDiagnostics{PromptTokens: 5, CompletionTokens: 3}
	promptOnly := LLMDiagnostics{PromptTokens: 5, CompletionTokens: 3, TokensEstimated: true, PromptTokensEstimated: true}
	full := LLMDiagnostics{PromptTokens: 5, CompletionTokens: 3, TokensEstimated: true}
	for _, tc := range []struct {
		name                      string
		first, second             LLMDiagnostics
		wantEstimated, wantPrompt bool
	}{
		{name: "prompt-only then exact", first: promptOnly, second: exact, wantEstimated: true, wantPrompt: true},
		{name: "exact then prompt-only", first: exact, second: promptOnly, wantEstimated: true, wantPrompt: true},
		{name: "prompt-only twice", first: promptOnly, second: promptOnly, wantEstimated: true, wantPrompt: true},
		{name: "prompt-only then full", first: promptOnly, second: full, wantEstimated: true, wantPrompt: false},
		{name: "full then prompt-only", first: full, second: promptOnly, wantEstimated: true, wantPrompt: false},
		{name: "exact twice", first: exact, second: exact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rollup *LLMDiagnostics
			first, second := tc.first, tc.second
			MergeLLMHopDiagnostics(&rollup, &first)
			MergeLLMHopDiagnostics(&rollup, &second)
			if rollup.TokensEstimated != tc.wantEstimated || rollup.PromptTokensEstimated != tc.wantPrompt {
				t.Fatalf("TokensEstimated=%v PromptTokensEstimated=%v, want %v/%v", rollup.TokensEstimated, rollup.PromptTokensEstimated, tc.wantEstimated, tc.wantPrompt)
			}
			if rollup.CompletionTokens != 6 || rollup.LLMHopCount != 2 {
				t.Fatalf("completion=%d hops=%d, want 6 over 2 hops", rollup.CompletionTokens, rollup.LLMHopCount)
			}
		})
	}
}

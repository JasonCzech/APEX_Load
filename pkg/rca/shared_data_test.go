package rca

import (
	"slices"
	"testing"
)

func TestSharedDataValiditySeparatesExpectedWaitsFromHarnessFailures(t *testing.T) {
	for _, tc := range []struct {
		name            string
		evidence        SharedDataEvidence
		verdict, reason string
	}{
		{"expected empty and waiting", SharedDataEvidence{WaitCount: 4, WaitTotalNS: 1000000, EmptyOutcomes: 2, Redeliveries: 1}, ValidityValid, ""},
		{"capacity rejection", SharedDataEvidence{Throttled: 1}, ValiditySuspect, RuleSharedDataThrottled},
		{"unknown work outcome", SharedDataEvidence{UnknownOutcomes: 1}, ValiditySuspect, RuleSharedDataUnknown},
		{"recovery gap", SharedDataEvidence{RecoveryGaps: 1}, ValidityUnknown, RuleEvidenceIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := validityRun(10, "generator")
			input.SharedData = []SharedDataEvidence{tc.evidence}
			result := AssessValidity(input, DefaultValidityRules)
			if result.Verdict != tc.verdict || len(result.SharedData) != 1 || (tc.reason != "" && !slices.Contains(result.Reasons, tc.reason)) {
				t.Fatalf("unexpected shared-data verdict: %+v", result)
			}
		})
	}
}

func TestSharedDataEvidenceSurvivesNoIntervalsAndCannotSoftenInvalidRun(t *testing.T) {
	evidence := []SharedDataEvidence{{TableID: "table", UnknownOutcomes: 1, Incomplete: true}}
	result := AssessValidity(ValidityInput{SharedData: evidence}, DefaultValidityRules)
	if result.Verdict != ValiditySuspect || len(result.SharedData) != 1 {
		t.Fatalf("missing evidence: %+v", result)
	}
	input := validityRun(10, "generator")
	exhausted := true
	input.DataExhausted, input.SharedData = &exhausted, evidence
	result = AssessValidity(input, DefaultValidityRules)
	if result.Verdict != ValidityInvalid || !slices.Contains(result.Reasons, RuleDataExhausted) {
		t.Fatalf("softened invalid run: %+v", result)
	}
}

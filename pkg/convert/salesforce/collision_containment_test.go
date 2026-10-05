package salesforce

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

func collisionPlan(name string, found int, automatic bool, value string) nativeCorrelationPlan {
	return nativeCorrelationPlan{correlation: correlate.Correlation{Name: name, FoundInID: found, Expression: "$." + name}, automatic: automatic, usage: correlationUse{raw: true}, value: value}
}

// A session id contained in a longer token is safe under longest-first
// replacement; it must stay automatic and be recorded as containment instead
// of poisoning both correlations.
func TestNativeCollisionsRecordContainmentInsteadOfAmbiguity(t *testing.T) {
	plans := []nativeCorrelationPlan{
		collisionPlan("corr_sid", 1, true, "00Dxx0000001gEREAY"),
		collisionPlan("corr_token", 2, true, "00Dxx0000001gEREAY!AQ4AQ.synthetic.token"),
	}
	surfaces := "sid=00Dxx0000001gEREAY!AQ4AQ.synthetic.token\x00org=00Dxx0000001gEREAY\x00"
	literals, ambiguous, containment := nativeCorrelationReplacementCollisions(plans, surfaces)
	if len(literals) != 0 || len(ambiguous) != 0 {
		t.Fatalf("containment flagged ambiguous: %v %v", literals, ambiguous)
	}
	want := []nativeLiteralContainment{{inner: plans[0].value, outer: plans[1].value}}
	if !reflect.DeepEqual(containment, want) {
		t.Fatalf("containment=%v want %v", containment, want)
	}
}

// Identical spellings from two automatic producers are shared (the OAuth access
// token is also the sid cookie); a review-only owner still blocks them.
func TestNativeCollisionsShareIdenticalSpellingsBetweenAutomaticProducers(t *testing.T) {
	shared := []nativeCorrelationPlan{collisionPlan("corr_a", 1, true, "same-value-123"), collisionPlan("corr_b", 2, true, "same-value-123")}
	if literals, ambiguous, _ := nativeCorrelationReplacementCollisions(shared, "v=same-value-123"); len(literals) != 0 || len(ambiguous) != 0 {
		t.Fatalf("automatic producers of one spelling were suppressed: %v %v", literals, ambiguous)
	}
	owned := []nativeCorrelationPlan{collisionPlan("corr_a", 1, true, "same-value-123"), collisionPlan("corr_b", 2, false, "same-value-123")}
	if literals, ambiguous, _ := nativeCorrelationReplacementCollisions(owned, "v=same-value-123"); !literals["same-value-123"] || len(ambiguous) != 2 {
		t.Fatalf("review-only owner did not block the automatic spelling: %v %v", literals, ambiguous)
	}
}

func TestCorrelationVarsUseClosestPrecedingProducer(t *testing.T) {
	vars := map[string][]nativeLiteralProducer{"tok": {{sequence: 2, template: "{{var.token}}"}, {sequence: 5, template: "{{var.sid}}"}}}
	for _, tc := range []struct {
		sequence int
		want     string
	}{{2, ""}, {3, "{{var.token}}"}, {5, "{{var.token}}"}, {6, "{{var.sid}}"}} {
		if got := correlationVarsAvailableAfterSequence(vars, tc.sequence)["tok"]; got != tc.want {
			t.Fatalf("sequence %d chose %q want %q", tc.sequence, got, tc.want)
		}
	}
}

// The inner variable is dropped only in an action where its outer literal is
// still literal; elsewhere both rewrite.
func TestWithoutInnerVarsOfLiteralOuters(t *testing.T) {
	containment := []nativeLiteralContainment{{inner: "abc", outer: "xxabcxx"}}
	cases := []struct {
		name     string
		vars     map[string]string
		surfaces []string
		want     []string
	}{
		{"outer rewritten too", map[string]string{"abc": "{{var.a}}", "xxabcxx": "{{var.x}}"}, []string{"q=xxabcxx&r=abc"}, []string{"abc", "xxabcxx"}},
		{"outer stays literal here", map[string]string{"abc": "{{var.a}}"}, []string{"q=xxabcxx&r=abc"}, nil},
		{"outer absent here", map[string]string{"abc": "{{var.a}}"}, []string{"r=abc"}, []string{"abc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withoutInnerVarsOfLiteralOuters(tc.vars, containment, tc.surfaces)
			for _, literal := range tc.want {
				if _, ok := got[literal]; !ok {
					t.Fatalf("missing %q in %v", literal, got)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("vars=%v want %v", got, tc.want)
			}
		})
	}
}

// When the comparison bound runs out, claims whose comparisons all completed
// keep their outcome; only plans with unchecked claims are suppressed.
func TestNativeCollisionsRetainFullyCheckedClaimsOnExhaustion(t *testing.T) {
	previous := nativeCollisionComparisonLimit
	nativeCollisionComparisonLimit = 3
	defer func() { nativeCollisionComparisonLimit = previous }()
	plans := []nativeCorrelationPlan{
		collisionPlan("corr_a", 1, true, "alpha-111"),
		collisionPlan("corr_b", 2, true, "bravo-222"),
		collisionPlan("corr_c", 3, true, "charlie-333"),
		collisionPlan("corr_d", 4, true, "delta-444"),
	}
	// Pairs (a,b),(a,c),(a,d) complete; the bound trips at (b,c).
	literals, ambiguous, _ := nativeCorrelationReplacementCollisions(plans, "x")
	if literals["alpha-111"] || ambiguous[0] {
		t.Fatalf("fully checked claim suppressed: %v %v", literals, ambiguous)
	}
	for index := 1; index < len(plans); index++ {
		if !ambiguous[index] || !literals[plans[index].value] {
			t.Fatalf("unchecked plan %d stayed automatic: %v %v", index, literals, ambiguous)
		}
	}
}

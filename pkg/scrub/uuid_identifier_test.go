package scrub

import "testing"

// A canonical UUID is an identifier, not a credential. Before this exemption it
// reached the bare-token entropy heuristic only because '-' is in
// highEntropyToken's character class, and a UUID's alphabet (16 hex digits plus
// the hyphen) caps its entropy at log2(17) ~= 4.09 against a >= 4.0 cutoff. So
// roughly 1 UUID in 33,000 was blanked and the rest were kept, which made every
// UUID-carrying response non-deterministic evidence. These two values are the
// pair that exposed it: both are real session ids, and only the first crossed
// the threshold (entropy 4.0169 vs 3.8083).
func TestCanonicalUUIDsSurviveTheBareTokenEntropyHeuristic(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"entropy above the cutoff", "f73dfa50-bcfd-4081-917e-5bc632e84a6e"},
		{"entropy below the cutoff", "5d9da0c8-15f8-4edb-8afb-24c5708ab56c"},
		{"uppercase", "F73DFA50-BCFD-4081-917E-5BC632E84A6E"},
		{"v1 layout", "c232ab00-9414-11ec-b3c8-9e6bdeced846"},
		{"nil uuid", "00000000-0000-0000-0000-000000000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Value(tc.value, Policy{}); got != tc.value {
				t.Fatalf("Value(%q) = %q, want the identifier preserved", tc.value, got)
			}
			// The opaque handle shape the import draft attestation emits.
			prefixed := "protected:" + tc.value
			if got := Value(prefixed, Policy{}); got != prefixed {
				t.Fatalf("Value(%q) = %q, want the attestation handle preserved", prefixed, got)
			}
		})
	}
}

// The exemption is scoped to the bare-token heuristic. Anything that was
// redacted for a reason other than "this run of characters looks high-entropy"
// must still be redacted, or the exemption has quietly become a hole.
func TestUUIDExemptionDoesNotWeakenTheOtherRedactionPaths(t *testing.T) {
	const id = "f73dfa50-bcfd-4081-917e-5bc632e84a6e"

	t.Run("sensitive key still redacts a UUID value", func(t *testing.T) {
		got := Value(`{"session_token":"`+id+`"}`, Policy{})
		if got == `{"session_token":"`+id+`"}` {
			t.Fatalf("Value() = %q, want the UUID redacted under a sensitive key", got)
		}
	})

	t.Run("query parameter with a sensitive name still redacts", func(t *testing.T) {
		got := Value("https://api.example.test/x?access_token="+id, Policy{})
		if got == "https://api.example.test/x?access_token="+id {
			t.Fatalf("Value() = %q, want the UUID redacted under a sensitive parameter", got)
		}
	})

	t.Run("a longer token that merely contains a UUID is not exempt", func(t *testing.T) {
		// Not the canonical layout end to end, so it must still face the heuristic.
		candidate := "sk9Qv" + id + "Xz2Lp"
		if got := Value(candidate, Policy{}); got != Redacted {
			t.Fatalf("Value(%q) = %q, want %q", candidate, got, Redacted)
		}
	})

	t.Run("an ordinary high-entropy secret is still redacted", func(t *testing.T) {
		const token = "AKIAIOSFODNN7EXAMPLEwJalrXUtnFEMIK7MDENGbPxRfiCY"
		if got := Value(token, Policy{}); got != Redacted {
			t.Fatalf("Value(%q) = %q, want %q", token, got, Redacted)
		}
	})
}

func TestValueLabelSkipsOnlyBareTokenHeuristics(t *testing.T) {
	const tokenLikeLabel = "checkout-AbCdEfGh1234567890ijklMN"
	if got := ValueLabel(tokenLikeLabel, Policy{}); got != tokenLikeLabel {
		t.Fatalf("ValueLabel(%q) = %q, want stable label preserved", tokenLikeLabel, got)
	}
	if got := ValueLabel("checkout token=abc123", Policy{}); got != "checkout token="+Redacted {
		t.Fatalf("structured credential label = %q", got)
	}
	policy := Policy{ExtraPatterns: []Rule{{Pattern: `customer-[0-9]+`}}}
	if got := ValueLabel("checkout-customer-1234", policy); got != "checkout-"+Redacted {
		t.Fatalf("explicit policy label = %q", got)
	}
}

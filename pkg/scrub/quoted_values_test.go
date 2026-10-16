package scrub

import (
	"strings"
	"testing"
	"time"
)

func TestRedactQuotedValuesDecodesSourceEscapesAndPreservesLines(t *testing.T) {
	const source = `{
  "json": "prefix\u002dsecret",
  "yaml": 'prefix''secret',
  "js": 'prefix\x2dsecret',
  "safe": "unchanged"
}`
	projected, changed := RedactQuotedValues(source, []string{"prefix-secret", "prefix'secret"})
	if !changed {
		t.Fatal("RedactQuotedValues did not report a change")
	}
	for _, leaked := range []string{`\u002d`, `prefix''secret`, `\x2d`} {
		if strings.Contains(projected, leaked) {
			t.Fatalf("projected source retained %q: %s", leaked, projected)
		}
	}
	if !strings.Contains(projected, `"safe": "unchanged"`) {
		t.Fatalf("nonsensitive quoted scalar changed: %s", projected)
	}
	if strings.Count(projected, "\n") != strings.Count(source, "\n") {
		t.Fatalf("newline count changed: got %d want %d", strings.Count(projected, "\n"), strings.Count(source, "\n"))
	}
}

func TestRedactQuotedValuesYAMLSingleQuoteBackslashDoesNotHideLaterScalar(t *testing.T) {
	const source = `safe: 'literal backslash\'
credential: 'yaml\u002dsecret'
tail: unchanged
`
	projected, changed := RedactQuotedValues(source, []string{"yaml-secret"})
	if !changed {
		t.Fatal("RedactQuotedValues did not redact the protected YAML scalar")
	}
	if strings.Contains(projected, `yaml\u002dsecret`) ||
		strings.Contains(projected, "yaml-secret") {
		t.Fatalf("projected source retained the protected YAML scalar: %s", projected)
	}
	if !strings.Contains(projected, `safe: 'literal backslash\'`) {
		t.Fatalf("safe YAML scalar ending in a backslash changed: %s", projected)
	}
	if strings.Count(projected, "\n") != strings.Count(source, "\n") {
		t.Fatalf("newline count changed: got %d want %d", strings.Count(projected, "\n"), strings.Count(source, "\n"))
	}
}

func TestRedactQuotedValuesRetainsJavaScriptEscapedSingleQuoteRecognition(t *testing.T) {
	const source = `const safe = 'it\'s safe'; const value = 'js\u002dsecret';`
	projected, changed := RedactQuotedValues(source, []string{"js-secret"})
	if !changed {
		t.Fatal("RedactQuotedValues did not redact the protected JavaScript scalar")
	}
	if strings.Contains(projected, `js\u002dsecret`) ||
		strings.Contains(projected, "js-secret") {
		t.Fatalf("projected source retained the protected JavaScript scalar: %s", projected)
	}
	if !strings.Contains(projected, `const safe = 'it\'s safe'`) {
		t.Fatalf("safe JavaScript scalar with an escaped quote changed: %s", projected)
	}
}

func TestNonOverlappingQuotedReplacementsUsesUnionForDifferentSecrets(t *testing.T) {
	const source = "'first-secret\nsecond-secret'"
	replacements := nonOverlappingQuotedReplacements(source, []quotedValueReplacement{
		{start: 0, end: len("'first-secret\n")},
		{start: len("'first-"), end: len(source)},
	})
	if len(replacements) != 1 ||
		replacements[0].start != 0 ||
		replacements[0].end != len(source) {
		t.Fatalf("overlapping credential spans were not merged: %#v", replacements)
	}
	if replacements[0].value != `"`+Redacted+"\""+strings.Repeat("\n", 1) {
		t.Fatalf("merged replacement did not preserve covered newlines: %q", replacements[0].value)
	}
}

func TestRedactQuotedValuesDoubledQuoteInputIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bounded-work regression in short mode")
	}
	// This shape made a scanner that restarted a YAML quote search at every
	// byte perform quadratic work. Two linear grammar passes should comfortably
	// process one MiB within this deliberately generous bound.
	source := strings.Repeat("''''", 1<<18)
	start := time.Now()
	projected, changed := RedactQuotedValues(source, []string{"not-present"})
	if changed || projected != source {
		t.Fatal("credential-free adversarial input changed")
	}
	budget := 2 * time.Second
	if raceDetectorEnabled {
		// The race leg's instrumented scan of one MiB has taken over 10 s on a
		// busy runner; quadratic work would take orders of magnitude longer.
		budget = 30 * time.Second
	}
	if elapsed := time.Since(start); elapsed > budget {
		t.Fatalf("quoted-value scan exceeded bounded-work budget: %s", elapsed)
	}
}

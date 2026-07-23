package enterprisearchive

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const wholeBodyExpression = "(?s)^(.*)$"

func wholeBodyCaptureScript(consumer dsl.Action) *dsl.Script {
	return &dsl.Script{
		Actions: []dsl.Action{
			{Name: "capture", Extractors: []dsl.Extractor{
				{Name: "token", Source: "body", Type: "regex", Expression: wholeBodyExpression},
			}},
			consumer,
		},
	}
}

// The engine's templatePattern matches {{ ... }} and TrimSpaces the expression,
// so every spacing variant below resolves at replay. Pruning any of them drops a
// capture the script actually consumes and silently breaks correlation.
func TestPruneKeepsExtractorsReferencedWithSurroundingWhitespace(t *testing.T) {
	for _, reference := range []string{
		"{{var.token}}",
		"{{ var.token }}",
		"{{var.token }}",
		"{{ var.token}}",
		"{{  var.token  }}",
	} {
		t.Run(reference, func(t *testing.T) {
			script := wholeBodyCaptureScript(dsl.Action{Name: "use", Target: "/next?t=" + reference})
			if removed := pruneUnusedWholeBodyExtractors(script); removed != 0 {
				t.Errorf("removed = %d, want 0 — %s is a live reference", removed, reference)
			}
			if got := len(script.Actions[0].Extractors); got != 1 {
				t.Errorf("kept %d extractors, want 1 — the capture is consumed by %s", got, reference)
			}
		})
	}
}

// A capture consumed only by a sibling extractor's expression is still consumed.
func TestPruneKeepsExtractorReferencedByAnotherExtractor(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{
		{Name: "capture", Extractors: []dsl.Extractor{
			{Name: "token", Source: "body", Type: "regex", Expression: wholeBodyExpression},
			{Name: "second", Source: "body", Type: "regex", Expression: "prefix={{var.token}}(.*)"},
		}},
	}}
	if removed := pruneUnusedWholeBodyExtractors(script); removed != 0 {
		t.Errorf("removed = %d, want 0 — a sibling extractor consumes the capture", removed)
	}
	if got := len(script.Actions[0].Extractors); got != 2 {
		t.Errorf("kept %d extractors, want 2", got)
	}
}

// The prune must still do its job: a genuinely unconsumed whole-body capture
// goes away, and targeted extractors are never touched.
func TestPruneRemovesOnlyUnreferencedWholeBodyExtractors(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{
		{Name: "capture", Extractors: []dsl.Extractor{
			{Name: "unused", Source: "body", Type: "regex", Expression: wholeBodyExpression},
			{Name: "targeted", Source: "body", Type: "regex", Expression: `id="([^"]+)"`},
		}},
		{Name: "use", Target: "/next"},
	}}
	if removed := pruneUnusedWholeBodyExtractors(script); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	kept := script.Actions[0].Extractors
	if len(kept) != 1 || kept[0].Name != "targeted" {
		t.Fatalf("kept = %+v, want only the targeted extractor", kept)
	}
}

func TestReferencedVariableNamesIgnoresOtherNamespaces(t *testing.T) {
	names := referencedVariableNames(`{{var.a}} {{ var.b }} {{param.c}} {{secret.d}} {{env.e}} {{var.}} {{}}`)
	if _, ok := names["a"]; !ok {
		t.Error("missing a")
	}
	if _, ok := names["b"]; !ok {
		t.Error("missing b (whitespace form)")
	}
	for _, unwanted := range []string{"c", "d", "e", ""} {
		if _, ok := names[unwanted]; ok {
			t.Errorf("unexpected name %q from a non-var namespace or empty reference", unwanted)
		}
	}
	if len(names) != 2 {
		t.Errorf("names = %v, want exactly a and b", names)
	}
}

func TestPruneUnusedWholeBodyExtractorsNilScript(t *testing.T) {
	if removed := pruneUnusedWholeBodyExtractors(nil); removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

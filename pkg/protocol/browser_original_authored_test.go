package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserOriginalAuthoredStepHasIndependentVersionedIdentity(t *testing.T) {
	definition := BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
	step := NewBrowserOriginalAuthoredStep(definition, "actions[0]")
	legacy := NewBrowserAuthoredStep(definition.ScriptSHA256, "actions[0]")
	if !step.Valid() || !legacy.Valid() || step.Version != 2 || step.StepID == legacy.StepID {
		t.Fatalf("step=%+v legacy=%+v", step, legacy)
	}
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	var decoded BrowserAuthoredStep
	if err := json.Unmarshal(raw, &decoded); err != nil || !decoded.Valid() || decoded != *step {
		t.Fatal("source identity round trip", err)
	}
	changed := definition
	changed.StructureSHA256 = strings.Repeat("c", 64)
	if NewBrowserOriginalAuthoredStep(changed, step.StepPath).StepID == step.StepID {
		t.Fatal("structure digest was not bound")
	}
	for name, mutate := range map[string]func(*BrowserAuthoredStep){
		"missing structure": func(s *BrowserAuthoredStep) { s.SourceStructureSHA256 = "" },
		"changed structure": func(s *BrowserAuthoredStep) { s.SourceStructureSHA256 = strings.Repeat("c", 64) },
		"legacy relabel":    func(s *BrowserAuthoredStep) { s.Version = 1 },
		"future":            func(s *BrowserAuthoredStep) { s.Version = 3 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := *step
			mutate(&copy)
			if copy.Valid() || copy.Clone() != nil {
				t.Fatal("invalid source proof accepted")
			}
		})
	}
	for _, bad := range []BrowserAuthoredDefinition{{}, {Version: 2, ScriptSHA256: definition.ScriptSHA256, StructureSHA256: definition.StructureSHA256}, {Version: 1, ScriptSHA256: strings.Repeat("A", 64), StructureSHA256: definition.StructureSHA256}} {
		if NewBrowserOriginalAuthoredStep(bad, "actions[0]") != nil {
			t.Fatal("invalid definition accepted")
		}
	}
	legacy.SourceStructureSHA256 = definition.StructureSHA256
	if legacy.Valid() {
		t.Fatal("legacy acquired invented original-source proof")
	}
}

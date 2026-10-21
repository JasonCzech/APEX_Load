package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserAuthoredStepBindsExactScriptAndCanonicalLocation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, path := range []string{"init[0]", "actions[12]", "teardown[0]", "actions[1].control.steps[2].control.cases[0].steps[1].control.branches[0].steps[0]"} {
		t.Run(path, func(t *testing.T) {
			step := NewBrowserAuthoredStep(digest, path)
			if !step.Valid() || step.StepID != NewBrowserAuthoredStep(digest, path).StepID {
				t.Fatal("identity is not stable")
			}
			if step.StepID == NewBrowserAuthoredStep(strings.Repeat("b", 64), path).StepID {
				t.Fatal("script revision did not change identity")
			}
			clone := step.Clone()
			clone.StepPath = "actions[99]"
			if clone.Valid() || !step.Valid() {
				t.Fatal("clone mutation was accepted or changed original")
			}
			raw, err := json.Marshal(step)
			if err != nil {
				t.Fatal(err)
			}
			var decoded BrowserAuthoredStep
			if err = json.Unmarshal(raw, &decoded); err != nil || !decoded.Valid() {
				t.Fatalf("round trip: %v", err)
			}
		})
	}
	if NewBrowserAuthoredStep(digest, "actions[0]").StepID == NewBrowserAuthoredStep(digest, "actions[1]").StepID {
		t.Fatal("duplicate names cannot collapse structural positions")
	}
}

func TestBrowserAuthoredStepRejectsUnboundOrNoncanonicalIdentity(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, path := range []string{"", "actions[-1]", "actions[01]", "actions[1000000]", "actions[0].control.steps[01]", "actions[0].target", "customer-password", "actions[{{param.index}}]", "actions[0]/secret", "actions[0].params.requests[0]", "actions[0]" + strings.Repeat(".control.steps[0]", 65), strings.Repeat("x", 4097)} {
		if NewBrowserAuthoredStep(digest, path) != nil {
			t.Errorf("accepted noncanonical path %q", path)
		}
	}
	for _, hash := range []string{"", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)} {
		if NewBrowserAuthoredStep(hash, "actions[0]") != nil {
			t.Error("accepted malformed script digest")
		}
	}
	var absent *BrowserAuthoredStep
	if absent.Valid() || absent.Clone() != nil {
		t.Fatal("absent identity became available")
	}
	step := NewBrowserAuthoredStep(digest, "actions[0]")
	step.Version++
	if step.Valid() || step.Clone() != nil {
		t.Fatal("future contract accepted")
	}
}

func TestBrowserAuthoredStepCannotBeInjectedThroughProtocolActionJSON(t *testing.T) {
	original := Action{Name: "tap", BrowserAuthoredStep: NewBrowserAuthoredStep(strings.Repeat("a", 64), "actions[0]")}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "script_sha256") || strings.Contains(string(raw), "step_id") {
		t.Fatal("engine identity leaked into protocol action wire")
	}
	var decoded Action
	if err := json.Unmarshal([]byte("{\"name\":\"tap\",\"BrowserAuthoredStep\":{\"version\":1,\"step_path\":\"actions[0]\"},\"browser_authored_step\":{\"version\":1}}"), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.BrowserAuthoredStep != nil {
		t.Fatal("untrusted protocol input supplied engine identity")
	}
}

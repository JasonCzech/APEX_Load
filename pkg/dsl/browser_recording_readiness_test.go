package dsl

import (
	"github.com/jasonczech/apexload/pkg/credential"
	"strings"
	"testing"
)

func TestBrowserStorageStateRequiresSecretReference(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{"", true}, {"{{secret.SESSION}}", true}, {"{{secret." + strings.Repeat("A", credential.MaxLogicalNameBytes) + "}}", true}, {"{{secret." + strings.Repeat("A", credential.MaxLogicalNameBytes+1) + "}}", false}, {`{"cookies":[]}`, false}, {"/tmp/state.json", false}, {"{{env.SESSION}}", false}} {
		script := &Script{Protocol: "browser", Config: ScriptConfig{Browser: BrowserConfig{StorageState: tc.value}}}
		if issues := validateBrowserConfigIssues(script); (len(issues) == 0) != tc.valid {
			t.Fatalf("state %q: %v", tc.value, issues)
		}
	}
}

func TestBrowserFillTypingOptions(t *testing.T) {
	for _, tc := range []struct {
		params map[string]any
		valid  bool
	}{
		{nil, true}, {map[string]any{"input_mode": "type", "typing_delay_ms": 1000}, true},
		{map[string]any{"input_mode": "unknown"}, false}, {map[string]any{"typing_delay_ms": -1}, false}, {map[string]any{"typing_delay_ms": 1001}, false},
	} {
		if issues := browserFillChecks("fill", Action{Params: tc.params}); (len(issues) == 0) != tc.valid {
			t.Fatalf("params %v: %v", tc.params, issues)
		}
	}
}

func TestBrowserWaitElementTextRequiresExpectedText(t *testing.T) {
	for _, kind := range []string{"element_text", "ELEMENT_TEXT"} {
		for _, text := range []string{"", "Saved"} {
			issues := browserWaitChecks("wait", Action{WaitFor: &WaitCondition{Type: kind, Value: "#result"}, Params: map[string]any{"text": text}})
			if (len(issues) == 0) != (text != "") {
				t.Fatalf("type %q text %q: %v", kind, text, issues)
			}
		}
	}
}

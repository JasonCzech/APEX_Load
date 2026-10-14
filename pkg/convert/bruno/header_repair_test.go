package bruno

import (
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func headerRepairTestScript(headers ...dsl.HeaderEntry) *dsl.Script {
	list := dsl.HeaderList(headers)
	return &dsl.Script{
		Name:     "repair",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "POST", Method: "POST", Target: "https://example.test",
			HeaderList: list,
			Headers:    list.ToMap(),
		}},
	}
}

func TestNativeHeaderRepairTrimsPastedCurlFragmentFromCookie(t *testing.T) {
	script := headerRepairTestScript(
		dsl.HeaderEntry{Name: "Accept", Value: "text/xml"},
		dsl.HeaderEntry{Name: "Cookie", Value: `session=abc" -H accept https://example.test/path`},
	)
	diagnostics := NativeHeaderRepairDiagnostics("candidate", "requests/one.yml", script)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one", diagnostics)
	}
	diagnostic := diagnostics[0]
	repair := diagnostic.HeaderRepair
	if diagnostic.Code != NativeHeaderRepairDiagnosticCode || !diagnostic.AcknowledgementRequired ||
		diagnostic.ID == "" || diagnostic.FindingID != diagnostic.ID || repair == nil {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	if repair.ActionIndex != 0 || repair.HeaderIndex != 1 || repair.HeaderName != "Cookie" ||
		!slices.Equal(repair.Options, []string{HeaderRepairTrim, HeaderRepairRemove}) ||
		repair.KeptCharacters != len("session=abc") || repair.InvalidCharacter != `a double quote (")` {
		t.Fatalf("repair = %#v", repair)
	}
	if strings.Contains(diagnostic.Message, "session=abc") || !strings.Contains(diagnostic.Message, "Cookie header") {
		t.Fatalf("message = %q", diagnostic.Message)
	}
	if again := NativeHeaderRepairDiagnostics("candidate", "requests/one.yml", script); again[0].ID != diagnostic.ID {
		t.Fatalf("finding id is not stable: %q then %q", diagnostic.ID, again[0].ID)
	}

	applied, err := ApplyNativeHeaderRepair(script, *repair, HeaderRepairTrim)
	if err != nil || !applied {
		t.Fatalf("trim applied=%t err=%v", applied, err)
	}
	action := script.Actions[0]
	if action.HeaderList[1].Value != "session=abc" || action.Headers["Cookie"] != "session=abc" {
		t.Fatalf("trimmed headers = %#v / %#v", action.HeaderList, action.Headers)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("repaired script issues = %v", issues)
	}
	if applied, err := ApplyNativeHeaderRepair(script, *repair, HeaderRepairTrim); err != nil || applied {
		t.Fatalf("second trim applied=%t err=%v, want no-op", applied, err)
	}
}

func TestNativeHeaderRepairRemovesHeaderAndKeepsMapInStep(t *testing.T) {
	script := headerRepairTestScript(
		dsl.HeaderEntry{Name: "X-Note", Value: "first\nsecond"},
		dsl.HeaderEntry{Name: "Accept", Value: "text/xml"},
	)
	diagnostics := NativeHeaderRepairDiagnostics("candidate", "requests/one.yml", script)
	if len(diagnostics) != 1 || diagnostics[0].HeaderRepair == nil {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	applied, err := ApplyNativeHeaderRepair(script, *diagnostics[0].HeaderRepair, HeaderRepairRemove)
	if err != nil || !applied {
		t.Fatalf("remove applied=%t err=%v", applied, err)
	}
	action := script.Actions[0]
	if len(action.HeaderList) != 1 || action.HeaderList[0].Name != "Accept" {
		t.Fatalf("header list = %#v", action.HeaderList)
	}
	if _, kept := action.Headers["X-Note"]; kept || action.Headers["Accept"] != "text/xml" {
		t.Fatalf("header map = %#v", action.Headers)
	}
}

func TestNativeHeaderRepairOffersOnlyRemovalForInvalidName(t *testing.T) {
	script := headerRepairTestScript(dsl.HeaderEntry{Name: "Bad Name", Value: "value"})
	diagnostics := NativeHeaderRepairDiagnostics("candidate", "requests/one.yml", script)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	repair := *diagnostics[0].HeaderRepair
	if !slices.Equal(repair.Options, []string{HeaderRepairRemove}) || repair.HeaderName != "" {
		t.Fatalf("repair = %#v", repair)
	}
	if strings.Contains(diagnostics[0].Message, "Bad Name") {
		t.Fatalf("message disclosed the invalid name: %q", diagnostics[0].Message)
	}
	if _, err := ApplyNativeHeaderRepair(script, repair, HeaderRepairTrim); err == nil {
		t.Fatal("trim of an invalid name succeeded")
	}
	if applied, err := ApplyNativeHeaderRepair(script, repair, HeaderRepairRemove); err != nil || !applied {
		t.Fatalf("remove applied=%t err=%v", applied, err)
	}
	if len(script.Actions[0].HeaderList) != 0 || len(script.Actions[0].Headers) != 0 {
		t.Fatalf("headers after removal = %#v / %#v", script.Actions[0].HeaderList, script.Actions[0].Headers)
	}
}

func TestNativeHeaderRepairIgnoresValidAndTemplatedHeaders(t *testing.T) {
	disabled := false
	script := headerRepairTestScript(
		dsl.HeaderEntry{Name: "Cookie", Value: "a=1; b=2"},
		dsl.HeaderEntry{Name: "Authorization", Value: "{{secret.API_TOKEN}}"},
		dsl.HeaderEntry{Name: "Cookie", Value: "{{var.session_cookie}}"},
		dsl.HeaderEntry{Name: "X-Off", Value: "bad\nvalue", Enabled: &disabled},
	)
	if diagnostics := NativeHeaderRepairDiagnostics("candidate", "requests/one.yml", script); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
}

func TestNativeHeaderRepairRejectsStaleLocation(t *testing.T) {
	script := headerRepairTestScript(dsl.HeaderEntry{Name: "Cookie", Value: `a=1"x`})
	repair := NativeHeaderRepair{ActionIndex: 0, HeaderIndex: 0, HeaderName: "X-Other", Options: []string{HeaderRepairRemove}}
	if _, err := ApplyNativeHeaderRepair(script, repair, HeaderRepairRemove); err == nil {
		t.Fatal("repair applied to a different header")
	}
	repair = NativeHeaderRepair{ActionIndex: 0, HeaderIndex: 4, HeaderName: "Cookie"}
	if _, err := ApplyNativeHeaderRepair(script, repair, HeaderRepairRemove); err == nil {
		t.Fatal("repair applied past the header list")
	}
}

func TestNativeHeaderRepairTrimsPastLongSeparatorTail(t *testing.T) {
	value := `session=abc"` + strings.Repeat(" -H accept:/x", 500)
	script := headerRepairTestScript(dsl.HeaderEntry{Name: "Cookie", Value: value})
	diagnostics := NativeHeaderRepairDiagnostics("candidate", "requests/one.yml", script)
	if len(diagnostics) != 1 || diagnostics[0].HeaderRepair == nil ||
		!slices.Contains(diagnostics[0].HeaderRepair.Options, HeaderRepairTrim) ||
		diagnostics[0].HeaderRepair.KeptCharacters != len("session=abc") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestNativeHeaderRepairTrimsHostWhenAnEarlyCutIsInvalid(t *testing.T) {
	script := headerRepairTestScript(dsl.HeaderEntry{Name: "Host", Value: "[::1]:8080/path"})
	diagnostics := NativeHeaderRepairDiagnostics("candidate", "requests/one.yml", script)
	if len(diagnostics) != 1 || diagnostics[0].HeaderRepair == nil ||
		!slices.Contains(diagnostics[0].HeaderRepair.Options, HeaderRepairTrim) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if _, err := ApplyNativeHeaderRepair(script, *diagnostics[0].HeaderRepair, HeaderRepairTrim); err != nil {
		t.Fatal(err)
	}
	if got := script.Actions[0].HeaderList[0].Value; got != "[::1]:8080" {
		t.Fatalf("trimmed Host = %q", got)
	}
}

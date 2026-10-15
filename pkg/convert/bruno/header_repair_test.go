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

func TestSeparateUnsendableHeadersLeavesInvalidHeadersOutWithRepair(t *testing.T) {
	disabled := false
	kept, diagnostics := separateUnsendableHeaders(dsl.HeaderList{
		{Name: "Accept", Value: "text/xml"},
		{Name: "Cookie", Value: `a=1" -H accept`},
		{Name: "X Bad", Value: "value"},
		{Name: "X-Off", Value: "off\x01", Enabled: &disabled},
		{Name: "X-Note", Value: "ok"},
	}, "uid\x00request.yml", "requests/one.yml")
	var names []string
	for _, header := range kept {
		names = append(names, header.Name)
	}
	if !slices.Equal(names, []string{"Accept", "X-Off", "X-Note"}) {
		t.Fatalf("kept headers = %#v", kept)
	}
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v, want two", diagnostics)
	}
	cookie, name := diagnostics[0], diagnostics[1]
	if cookie.ID == "" || cookie.ID == name.ID || cookie.FindingID != cookie.ID ||
		cookie.Code != NativeHeaderRepairDiagnosticCode || !cookie.AcknowledgementRequired {
		t.Fatalf("cookie diagnostic = %#v", cookie)
	}
	repair := cookie.HeaderRepair
	if repair == nil || !repair.Omitted || repair.HeaderIndex != 1 || repair.HeaderName != "Cookie" ||
		repair.TrimmedValue != "a=1" || repair.KeptCharacters != 3 ||
		!slices.Equal(repair.Options, []string{HeaderRepairTrim, HeaderRepairRemove}) {
		t.Fatalf("cookie repair = %#v", repair)
	}
	if strings.Contains(cookie.Message, "a=1") || !strings.Contains(cookie.Message, "left out") {
		t.Fatalf("cookie message = %q", cookie.Message)
	}
	if name.HeaderRepair == nil || !name.HeaderRepair.Omitted || name.HeaderRepair.HeaderName != "" ||
		name.HeaderRepair.TrimmedValue != "" || !slices.Equal(name.HeaderRepair.Options, []string{HeaderRepairRemove}) {
		t.Fatalf("invalid name repair = %#v", name.HeaderRepair)
	}
	if _, again := separateUnsendableHeaders(dsl.HeaderList{
		{Name: "Accept", Value: "text/xml"},
		{Name: "Cookie", Value: `a=1" -H accept`},
	}, "uid\x00request.yml", "requests/one.yml"); again[0].ID != cookie.ID {
		t.Fatalf("finding id is not stable: %q then %q", cookie.ID, again[0].ID)
	}

	script := headerRepairTestScript(kept...)
	if applied, err := ApplyNativeHeaderRepair(script, *repair, HeaderRepairRemove); err != nil || !applied {
		t.Fatalf("remove applied=%t err=%v", applied, err)
	}
	if len(script.Actions[0].HeaderList) != 3 {
		t.Fatalf("remove changed headers: %#v", script.Actions[0].HeaderList)
	}
	if applied, err := ApplyNativeHeaderRepair(script, *repair, HeaderRepairTrim); err != nil || !applied {
		t.Fatalf("trim applied=%t err=%v", applied, err)
	}
	action := script.Actions[0]
	if action.HeaderList[1].Name != "Cookie" || action.HeaderList[1].Value != "a=1" ||
		action.HeaderList[2].Name != "X-Off" || action.Headers["Cookie"] != "a=1" {
		t.Fatalf("trimmed headers = %#v / %#v", action.HeaderList, action.Headers)
	}
	if _, err := ApplyNativeHeaderRepair(script, *name.HeaderRepair, HeaderRepairTrim); err == nil {
		t.Fatal("trim of an invalid name succeeded")
	}
}

func TestOmittedContentTypeTrimReplacesOnlyTheBodyDefault(t *testing.T) {
	_, diagnostics := separateUnsendableHeaders(dsl.HeaderList{
		{Name: "Content-Type", Value: "application/json\x01 pasted"},
	}, "uid\x00request.yml", "requests/one.yml")
	if len(diagnostics) != 1 || diagnostics[0].HeaderRepair.TrimmedValue != "application/json" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	markBodyDefaultContentType(diagnostics, 1)
	repair := *diagnostics[0].HeaderRepair
	if !repair.ReplacesBodyDefault || repair.HeaderIndex != 1 {
		t.Fatalf("repair = %#v, want it pointed at the body default", repair)
	}
	script := headerRepairTestScript(
		dsl.HeaderEntry{Name: "Accept", Value: "text/xml"},
		dsl.HeaderEntry{Name: "Content-Type", Value: "text/plain"},
	)
	if applied, err := ApplyNativeHeaderRepair(script, repair, HeaderRepairRemove); err != nil || !applied ||
		script.Actions[0].HeaderList[1].Value != "text/plain" {
		t.Fatalf("remove applied=%t err=%v headers=%#v, want the body default kept", applied, err, script.Actions[0].HeaderList)
	}
	if _, err := ApplyNativeHeaderRepair(script, repair, HeaderRepairTrim); err != nil {
		t.Fatal(err)
	}
	action := script.Actions[0]
	if len(action.HeaderList) != 2 || action.HeaderList[1].Value != "application/json" ||
		action.Headers["Content-Type"] != "application/json" {
		t.Fatalf("headers = %#v / %#v", action.HeaderList, action.Headers)
	}

	// Without a body default, an imported Content-Type that is still there
	// stays, and the trimmed one goes back at its own position.
	_, second := separateUnsendableHeaders(dsl.HeaderList{
		{Name: "Content-Type", Value: "text/xml"},
		{Name: "Content-Type", Value: "application/json\x01 pasted"},
	}, "uid\x00request.yml", "requests/one.yml")
	script = headerRepairTestScript(dsl.HeaderEntry{Name: "Content-Type", Value: "text/xml"})
	if _, err := ApplyNativeHeaderRepair(script, *second[0].HeaderRepair, HeaderRepairTrim); err != nil {
		t.Fatal(err)
	}
	if headers := script.Actions[0].HeaderList; len(headers) != 2 || headers[0].Value != "text/xml" || headers[1].Value != "application/json" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestMarkBodyDefaultContentTypeMovesLaterLeftOutHeadersAfterIt(t *testing.T) {
	_, diagnostics := separateUnsendableHeaders(dsl.HeaderList{
		{Name: "Accept", Value: "text/xml"},
		{Name: "X-Before", Value: "before\x01"},
		{Name: "Content-Type", Value: "application/json\x01"},
		{Name: "X-After", Value: "after\x01"},
	}, "uid\x00request.yml", "requests/one.yml")
	markBodyDefaultContentType(diagnostics, 1)
	var indexes []int
	for _, diagnostic := range diagnostics {
		indexes = append(indexes, diagnostic.HeaderRepair.HeaderIndex)
	}
	if !slices.Equal(indexes, []int{1, 1, 2}) || !diagnostics[1].HeaderRepair.ReplacesBodyDefault {
		t.Fatalf("header indexes = %v, repairs = %#v", indexes, diagnostics)
	}
}

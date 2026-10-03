package aurapayload

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func fixtureEnvelope(params string) string {
	return `{"actions":[{"id":"first;a","descriptor":"synthetic","params":` + params + `}]}`
}
func fixtureNested(value string, layers int) string {
	data, _ := json.Marshal(map[string]any{"accountId": value, "other": "unchanged"})
	for i := 0; i < layers; i++ {
		data, _ = json.Marshal(map[string]any{"payload": string(data)})
	}
	return fixtureEnvelope(string(data))
}
func findLeaf(t *testing.T, inspection Inspection, key, value string) Leaf {
	t.Helper()
	for _, leaf := range inspection.Leaves {
		if leaf.Key == key && leaf.Value == value {
			return leaf
		}
	}
	t.Fatal("expected typed leaf absent")
	return Leaf{}
}
func requireCode(t *testing.T, err error, code Code) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error = %v, expected safe code %s", err, code)
	}
}

func TestInspectDeterministicTypedNestedRoutesAndLiteralFreeShape(t *testing.T) {
	body := fixtureEnvelope(`{"z":"same","a":[{"a.b":"same","payload":"{\"accountId\":\"same\",\"n\":42,\"b\":true,\"nil\":null}"}]}`)
	first, err := Inspect(body, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Inspect(body, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.Codec != DirectJSON || first.WorkBytes <= len(body) {
		t.Fatal("inspection order or accounting is unstable")
	}
	leaf := findLeaf(t, first, "accountId", "same")
	want := []Segment{{Kind: KeySegment, Key: "actions"}, {Kind: IndexSegment}, {Kind: KeySegment, Key: "params"}, {Kind: KeySegment, Key: "a"}, {Kind: IndexSegment}, {Kind: KeySegment, Key: "payload"}, {Kind: JSONStringSegment}, {Kind: KeySegment, Key: "accountId"}}
	if !reflect.DeepEqual(leaf.Route, want) || leaf.JSONStringDepth != 1 || !reflect.DeepEqual(leaf.EncodingLayers, []string{"json", "json_string"}) {
		t.Fatal("nested route lost its explicit string boundary")
	}
	if !strings.Contains(findLeaf(t, first, "a.b", "same").DisplayPath, `["a.b"]`) {
		t.Fatal("escaped key has an ambiguous display path")
	}
	types := map[string]int{}
	for _, l := range first.Leaves {
		types[l.ValueType]++
	}
	if types["number"] != 1 || types["boolean"] != 1 || types["null"] != 1 {
		t.Fatalf("nonstring coverage omitted: %+v", types)
	}
	changed, err := Inspect(strings.ReplaceAll(body, "same", "different"), "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Shape != changed.Shape {
		t.Fatal("shape contains literal leaf values")
	}
	changed, err = Inspect(strings.Replace(body, `"z":"same"`, `"newKey":"same"`, 1), "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Shape == changed.Shape {
		t.Fatal("shape ignored structural key changes")
	}
}

func TestReplaceSelectsExactRepeatedLeafAndPreservesLiteralSpans(t *testing.T) {
	source := ` { "actions" : [ { "id":"first;a", "descriptor":"synthetic", "params": {"accountId":"same","other":"same","n":1e+02} } ] } `
	inspection, err := Inspect(source, "")
	if err != nil {
		t.Fatal(err)
	}
	leaf := findLeaf(t, inspection, "accountId", "same")
	got, err := Replace(source, "", []Edit{{Route: leaf.Route, Shape: inspection.Shape, Expected: "same", Replacement: "{{param.account}}"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(source, `"accountId":"same"`, `"accountId":"{{param.account}}"`, 1)
	if got != want {
		t.Fatal("exact patch changed an unselected source span")
	}
	unchanged, err := Replace(source, "", nil)
	if err != nil || unchanged != source {
		t.Fatal("empty edits changed source bytes")
	}
}

func TestReplaceNestedStringLayersAndOuterFormOnce(t *testing.T) {
	for layers := 1; layers <= MaxJSONStringDepth; layers++ {
		t.Run(string(rune('0'+layers)), func(t *testing.T) {
			body := fixtureNested("selected", layers)
			form := "before=%2f+kept&message=" + url.QueryEscape(body) + "&other=one&other=two&after=%2B"
			inspection, err := Inspect(form, "")
			if err != nil {
				t.Fatal(err)
			}
			leaf := findLeaf(t, inspection, "accountId", "selected")
			replacement := "quotes\" slash\\ amp& plus+ snow雪\n"
			got, err := Replace(form, "", []Edit{{Route: leaf.Route, Shape: inspection.Shape, Expected: "selected", Replacement: replacement}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(got, "before=%2f+kept&message=") || !strings.HasSuffix(got, "&other=one&other=two&after=%2B") {
				t.Fatal("outer form spans or unrelated duplicates changed")
			}
			next, err := Inspect(got, "")
			if err != nil {
				t.Fatal(err)
			}
			out := findLeaf(t, next, "accountId", replacement)
			if out.JSONStringDepth != layers || out.Value != replacement || findLeaf(t, next, "other", "unchanged").Value != "unchanged" {
				t.Fatal("nested replacement changed another decoded field")
			}
			if !reflect.DeepEqual(out.EncodingLayers, append([]string{"url_form", "json"}, repeat("json_string", layers)...)) {
				t.Fatal("codec layers differ from exact source structure")
			}
		})
	}
}
func repeat(value string, count int) []string {
	out := make([]string, count)
	for i := range out {
		out[i] = value
	}
	return out
}

func TestReplaceRejectsStaleTamperedAndOverlappingEditsAtomically(t *testing.T) {
	body := fixtureEnvelope(`{"accountId":"original","other":"kept","number":7}`)
	inspection, err := Inspect(body, "")
	if err != nil {
		t.Fatal(err)
	}
	leaf := findLeaf(t, inspection, "accountId", "original")
	valid := Edit{Route: leaf.Route, Shape: inspection.Shape, Expected: "original", Replacement: "replacement"}
	for _, tc := range []struct {
		name  string
		edits []Edit
		code  Code
	}{
		{"stale value", []Edit{{Route: leaf.Route, Shape: inspection.Shape, Expected: "old", Replacement: "new"}}, StaleValue},
		{"stale shape", []Edit{{Route: leaf.Route, Shape: "wrong", Expected: "original", Replacement: "new"}}, StaleShape},
		{"unknown key", []Edit{{Route: append(append([]Segment(nil), leaf.Route[:len(leaf.Route)-1]...), Segment{Kind: KeySegment, Key: "absent"}), Shape: inspection.Shape, Expected: "original", Replacement: "new"}}, InvalidRoute},
		{"tampered segment", []Edit{{Route: []Segment{{Kind: "unknown"}}, Shape: inspection.Shape, Expected: "original", Replacement: "new"}}, InvalidRoute},
		{"duplicate route", []Edit{valid, valid}, OverlappingEdits},
		{"nonstring leaf", []Edit{{Route: findLeaf(t, inspection, "number", "7").Route, Shape: inspection.Shape, Expected: "7", Replacement: "8"}}, InvalidRoute},
		{"invalid utf8", []Edit{{Route: leaf.Route, Shape: inspection.Shape, Expected: "original", Replacement: string([]byte{0xff})}}, InvalidUTF8},
		{"valid then stale", []Edit{valid, {Route: findLeaf(t, inspection, "other", "kept").Route, Shape: inspection.Shape, Expected: "stale", Replacement: "new"}}, StaleValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Replace(body, "", tc.edits)
			requireCode(t, err, tc.code)
			if got != "" {
				t.Fatal("failed atomic edit returned partial content")
			}
		})
	}
}

func TestResolveCallsAuthoredOccurrencesOnceWithoutRescanningInsertedValues(t *testing.T) {
	body := fixtureNested("{{var.value}} {{fn.next()}}", 3)
	form := "aura.token={{secret.token}}&message=" + url.QueryEscape(body) + "&keep=%2f"
	calls := map[string]int{}
	got, safe, err := Resolve(form, "", func(value string) (string, string) {
		calls[value]++
		switch value {
		case "{{var.value}} {{fn.next()}}":
			return `{"inserted":"{{fn.next()}}"} quotes" slash\&雪`, "[REDACTED]"
		case "{{secret.token}}":
			return "token &+", "[REDACTED]"
		default:
			return value, value
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls["{{var.value}} {{fn.next()}}"] != 1 || calls["{{secret.token}}"] != 1 || calls[`{"inserted":"{{fn.next()}}"} quotes" slash\&雪`] != 0 {
		t.Fatal("callback rescanned inserted content or resolved an occurrence twice")
	}
	values, err := url.ParseQuery(got)
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("aura.token") != "token &+" || !strings.HasSuffix(got, "&keep=%2f") {
		t.Fatal("outer component escaped more than once or changed literal spans")
	}
	// Deliberately decode known authored layers, without invoking discovery on an
	// inserted JSON-looking value: inserted data remains the selected string.
	var decoded map[string]any
	if json.Unmarshal([]byte(values.Get("message")), &decoded) != nil {
		t.Fatal("invalid outer JSON")
	}
	params := decoded["actions"].([]any)[0].(map[string]any)["params"].(map[string]any)
	for i := 0; i < 3; i++ {
		var next map[string]any
		if json.Unmarshal([]byte(params["payload"].(string)), &next) != nil {
			t.Fatal("invalid nested JSON string")
		}
		params = next
	}
	if params["accountId"] != `{"inserted":"{{fn.next()}}"} quotes" slash\&雪` {
		t.Fatal("selected runtime value did not survive exact layers")
	}
	safeValues, _ := url.ParseQuery(safe)
	if safeValues.Get("aura.token") != "[REDACTED]" || strings.Contains(safe, "token+%26") || strings.Contains(safe, "quotes") {
		t.Fatal("redacted pair disclosed a resolved value")
	}
}

func TestResolveEscapesAuthoredContextJSONAndRejectsChangedDuplicateContexts(t *testing.T) {
	message := fixtureEnvelope(`{"name":"{{var.name}}"}`)
	form := "keep=%2f&message=" + url.QueryEscape(message) + "&aura.context=" + url.QueryEscape(`{"fwuid":"{{var.fwuid}}","mode":"PROD"}`)
	value := "quote\" back\\ &+ 雪\n"
	callback := func(input string) (string, string) {
		if input == "{{var.fwuid}}" {
			return value, "[REDACTED]"
		}
		if input == "{{var.name}}" {
			return "alice", "alice"
		}
		return input, input
	}
	got, safe, err := Resolve(form, "", callback)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ body, want string }{{got, value}, {safe, "[REDACTED]"}} {
		values, _ := url.ParseQuery(tc.body)
		var context map[string]string
		if json.Unmarshal([]byte(values.Get("aura.context")), &context) != nil || context["fwuid"] != tc.want {
			t.Fatal("context JSON layer was not escaped before outer form")
		}
		if !strings.HasPrefix(tc.body, "keep=%2f&") {
			t.Fatal("context edit changed unrelated form bytes")
		}
	}
	duplicate := form + "&aura.context=" + url.QueryEscape(`{"fwuid":"{{var.fwuid}}"}`)
	_, _, err = Resolve(duplicate, "", callback)
	requireCode(t, err, AmbiguousContext)
	unchanged := fixtureEnvelope(`{"name":"kept"}`)
	duplicate = "message=" + url.QueryEscape(unchanged) + "&aura.context=%7B%22fwuid%22%3A%22literal%22%7D&aura.context=literal"
	got, safe, err = Resolve(duplicate, "", func(value string) (string, string) { return value, value })
	if err != nil || got != duplicate || safe != duplicate {
		t.Fatal("unchanged duplicate context or unrelated bytes were not preserved")
	}
}

func TestBase64WireSupportPreservesUnchangedRepresentationAndRejectsUnsupportedEncoding(t *testing.T) {
	body := fixtureNested("{{var.value}}", 1)
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	wrapped := " \n" + encoded[:20] + "\r\n" + encoded[20:] + "\n "
	inspection, err := Inspect(wrapped, " BASE64 ")
	if err != nil {
		t.Fatal(err)
	}
	leaf := findLeaf(t, inspection, "accountId", "{{var.value}}")
	if leaf.EncodingLayers[0] != "base64" {
		t.Fatal("base64 layer omitted")
	}
	untouched, err := Replace(wrapped, "base64", nil)
	if err != nil || untouched != wrapped {
		t.Fatal("no edits normalized an unchanged outer base64 representation")
	}
	got, safe, err := Resolve(wrapped, "base64", func(value string) (string, string) {
		if value == "{{var.value}}" {
			return "A&B雪", "[REDACTED]"
		}
		return value, value
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{got, safe} {
		decoded, err := base64.StdEncoding.DecodeString(output)
		if err != nil || !json.Valid(decoded) {
			t.Fatal("encoded resolved wire JSON is invalid")
		}
	}
	_, err = Inspect(body, "windows-1252")
	requireCode(t, err, UnsupportedEncoding)
	_, err = Inspect("not-base64", "base64")
	requireCode(t, err, InvalidEncoding)
	// The outer leading/trailing wrapper counts toward the input ceiling too.
	_, err = Inspect(strings.Repeat(" ", MaxEncodedInputBytes+1), "base64")
	requireCode(t, err, ByteLimit)
}

func TestInspectRejectsStrictMalformedAndLimitedPayloadsWithoutPartialLeaves(t *testing.T) {
	nestedMalformed := fixtureEnvelope(`{"payload":"{\"accountId\":\"a\",\"accountId\":\"b\"}"}`)
	for _, tc := range []struct {
		name, body, encoding string
		code                 Code
	}{
		{"duplicate outer", fixtureEnvelope(`{"x":"a","x":"b"}`), "", DuplicateKey},
		{"duplicate escaped key", fixtureEnvelope(`{"x":"a","\u0078":"b"}`), "", DuplicateKey},
		{"duplicate nested", nestedMalformed, "", DuplicateKey},
		{"bad nested json", fixtureEnvelope(`{"payload":"{broken}"}`), "", InvalidJSON},
		{"invalid utf8", fixtureEnvelope(`{"x":"` + string([]byte{0xff}) + `"}`), "", InvalidUTF8},
		{"unpaired high surrogate", fixtureEnvelope(`{"x":"\uD800"}`), "", InvalidSurrogate},
		{"unpaired low surrogate", fixtureEnvelope(`{"x":"\uDC00"}`), "", InvalidSurrogate},
		{"wrong low surrogate", fixtureEnvelope(`{"x":"\uD800\u0041"}`), "", InvalidSurrogate},
		{"missing message", "other=value", "", AmbiguousMessage},
		{"duplicate message", "message=" + url.QueryEscape(fixtureEnvelope(`{}`)) + "&%6dessage=" + url.QueryEscape(fixtureEnvelope(`{}`)), "", AmbiguousMessage},
		{"invalid percent", "message=%GG", "", InvalidForm},
		{"not Aura", `{"actions":[{"params":{"name":"alice"}}]}`, "", InvalidEnvelope},
		{"nested string limit", fixtureNested("value", MaxJSONStringDepth+1), "", JSONStringDepthLimit},
		{"body byte limit", strings.Repeat("x", MaxBodyBytes+1), "", ByteLimit},
		{"route limit", fixtureEnvelope(`{"` + strings.Repeat("a", MaxRouteBytes+1) + `":"value"}`), "", RouteLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspection, err := Inspect(tc.body, tc.encoding)
			requireCode(t, err, tc.code)
			if len(inspection.Leaves) != 0 {
				t.Fatal("failed inspection returned partial leaves")
			}
			if strings.Contains(err.Error(), "broken") || strings.Contains(err.Error(), "alice") {
				t.Fatal("error includes source content")
			}
		})
	}
	valid := fixtureEnvelope(`{"x":"\uD83D\uDE00","n":-1.2e+03}`)
	if _, err := Inspect(valid, ""); err != nil {
		t.Fatal("valid surrogate pair or number was rejected")
	}
}

func TestParserBudgetsAndEscapedOutputPreflight(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() error
		code Code
	}{
		{"tokens", func() error { b := &budget{tokens: MaxTokens}; _, err := parseDocument(`{}`, b); return err }, TokenLimit},
		{"aggregate work", func() error { b := &budget{work: MaxWorkBytes}; _, err := parseDocument(`{}`, b); return err }, WorkLimit},
		{"depth", func() error {
			_, err := parseDocument(strings.Repeat("[", MaxDepth+2)+`0`+strings.Repeat("]", MaxDepth+2), &budget{})
			return err
		}, DepthLimit},
		{"expanded quoted output", func() error { _, err := quoteBounded(strings.Repeat("\x00", MaxBodyBytes/6+1), &budget{}); return err }, ByteLimit},
		{"leaf limit", func() error {
			_, err := Inspect(fixtureEnvelope(`{"values":[`+strings.Repeat(`"x",`, MaxLeaves)+`"x"]}`), "")
			return err
		}, LeafLimit},
	} {
		t.Run(tc.name, func(t *testing.T) { requireCode(t, tc.run(), tc.code) })
	}
	for _, value := range []string{"plain", "quote\"back\\", "control\x00\b\n", "&<>\u2028\u2029雪😀"} {
		size, err := quotedSize(value, MaxBodyBytes)
		data, _ := json.Marshal(value)
		if err != nil || size != len(data) {
			t.Fatal("escaped size preflight differs from JSON encoding")
		}
	}
}

func TestCallerWorkLimitsAndMeasuredAtomicReplacement(t *testing.T) {
	body := fixtureNested("literal", 2)
	inspection, err := Inspect(body, "")
	if err != nil {
		t.Fatal(err)
	}
	exact, err := InspectWithWorkLimit(body, "", inspection.WorkBytes)
	if err != nil || exact.WorkBytes != inspection.WorkBytes {
		t.Fatal("exact successful inspection budget was not honored")
	}
	_, err = InspectWithWorkLimit(body, "", inspection.WorkBytes-1)
	requireCode(t, err, WorkLimit)
	var limited *Error
	errors.As(err, &limited)
	if limited.WorkBytes > inspection.WorkBytes-1 {
		t.Fatal("failed inspection exceeded caller work")
	}
	_, err = InspectWithWorkLimit(body, "", 0)
	requireCode(t, err, WorkLimit)
	leaf := findLeaf(t, inspection, "accountId", "literal")
	edit := Edit{Route: leaf.Route, Shape: inspection.Shape, Expected: "literal", Replacement: "replacement"}
	replaced, work, err := ReplaceWithWorkAccounting(body, "", []Edit{edit}, MaxWorkBytes)
	if err != nil || replaced == body || work <= inspection.WorkBytes {
		t.Fatal("successful replacement omitted edit/render work accounting")
	}
	exactBody, exactWork, err := ReplaceWithWorkAccounting(body, "", []Edit{edit}, work)
	if err != nil || exactBody != replaced || exactWork != work {
		t.Fatal("exact replacement budget differs from measured work")
	}
	rejected, used, err := ReplaceWithWorkAccounting(body, "", []Edit{edit}, work-1)
	requireCode(t, err, WorkLimit)
	if rejected != "" || used > work-1 {
		t.Fatal("rejected replacement returned partial bytes or exceeded caller work")
	}
	errors.As(err, &limited)
	if limited.WorkBytes != used {
		t.Fatal("error accounting differs from returned consumed work")
	}
}

func TestResolveRejectsTemplatedFormNamesBeforeAnyCallback(t *testing.T) {
	message := fixtureEnvelope(`{"accountId":"{{var.value}}"}`)
	for _, name := range []string{"{{var.reserved}}", "prefix{{var.reserved}}", "%7B%7Bvar.reserved%7D%7D"} {
		body := "message=" + url.QueryEscape(message) + "&" + name + "=value"
		calls := 0
		actual, safe, err := Resolve(body, "", func(value string) (string, string) {
			calls++
			return "aura.context", "aura.context"
		})
		requireCode(t, err, InvalidForm)
		if actual != "" || safe != "" || calls != 0 {
			t.Fatal("templated form name reached callback or returned partial wire data")
		}
		if _, err := Inspect(body, ""); err == nil {
			t.Fatal("inspection admitted unsupported form-name substitution")
		}
	}
	body := "%6besp=%2f&message=" + url.QueryEscape(message)
	got, _, err := Resolve(body, "", func(value string) (string, string) {
		if value == "{{var.value}}" {
			return "next", "next"
		}
		return value, value
	})
	if err != nil || !strings.HasPrefix(got, "%6besp=%2f&message=") {
		t.Fatal("fixed unrelated form name or unchanged value spelling changed")
	}
}

func TestInspectDisclosureSentinelIsLiteralAndKeepsBusinessSibling(t *testing.T) {
	body := fixtureEnvelope(`{"recordId":"business","apiToken":"[REDACTED]"}`)
	inspection, err := Inspect(body, "")
	if err != nil {
		t.Fatal(err)
	}
	business := findLeaf(t, inspection, "recordId", "business")
	secret := findLeaf(t, inspection, "apiToken", "[REDACTED]")
	if secret.JSONStringDepth != 0 {
		t.Fatal("disclosure sentinel created a fictional encoded JSON boundary")
	}
	changed, err := Replace(body, "", []Edit{{Route: business.Route, Shape: inspection.Shape, Expected: "business", Replacement: "{{var.record}}"}})
	if err != nil || !strings.Contains(changed, `"apiToken":"[REDACTED]"`) {
		t.Fatal("exact business edit failed or changed the masked credential sibling")
	}
	if _, err := Inspect(fixtureEnvelope(`{"recordId":"business","apiToken":"[malformed]"}`), ""); err == nil {
		t.Fatal("fixed sentinel exception admitted arbitrary malformed nested JSON")
	}
}

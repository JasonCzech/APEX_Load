package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"golang.org/x/text/encoding/charmap"
)

// platformSpellingModes are the three evidence modes a platform value must stay
// redacted in.
var platformSpellingModes = []struct {
	name       string
	redact     bool
	unredacted bool
}{{"setting-off", false, false}, {"setting-on", true, false}, {"unredacted-debug", false, true}}

// assertReportOmits fails when any needle appears in the JSON-encoded report.
func assertReportOmits(t *testing.T, report *ReplayReport, needles ...string) {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range needles {
		quoted, _ := json.Marshal(needle)
		spelled := strings.Trim(string(quoted), `"`)
		if idx := strings.Index(string(encoded), spelled); idx >= 0 {
			t.Fatalf("platform-derived spelling %q in report: ...%s...", needle, string(encoded)[max(0, idx-200):min(len(encoded), idx+len(spelled)+50)])
		}
	}
}

// A Windows-1252 url-form body sends each template value as
// url.QueryEscape(cp1252(value)). A non-ASCII platform value then travels in a
// percent spelling the UTF-8 matcher variants never produce ('%E4', not
// '%C3%A4'): it stays redacted in every mode, whether the body names
// {{secret.*}} or a variable assigned from one carries it.
func TestReplayWindows1252FormBodyPlatformSpellingStaysRedacted(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer server.Close()
	const secret = "Grüße-Plätzchen-7781"
	raw, err := charmap.Windows1252.NewEncoder().String(secret)
	if err != nil {
		t.Fatal(err)
	}
	upper := url.QueryEscape(raw)
	if upper != "Gr%FC%DFe-Pl%E4tzchen-7781" {
		t.Fatalf("unexpected cp1252 spelling %q", upper)
	}
	lower := "Gr%fc%dfe-Pl%e4tzchen-7781"
	for _, tc := range []struct {
		name    string
		actions func(body string) []dsl.Action
		body    string
	}{
		{name: "secret-reference", body: "user=bob&pw={{secret.pw}}", actions: func(body string) []dsl.Action { return []dsl.Action{cp1252FormAction(server.URL, body)} }},
		{name: "variable", body: "user=bob&pw={{var.pw}}", actions: func(body string) []dsl.Action {
			return []dsl.Action{
				{Name: "SavePW", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{secret.pw}}", "pw"}}},
				cp1252FormAction(server.URL, body),
			}
		}},
	} {
		for _, mode := range platformSpellingModes {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{
					Name: "cp1252-form", Protocol: scopeHTTPProtocol,
					Config:  dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
					Actions: tc.actions(tc.body),
				}, mode.redact)
				report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("cp1252-"+tc.name+mode.name, mode.unredacted, map[string]string{"pw": secret}))
				if err != nil {
					t.Fatal(err)
				}
				if len(report.Events) == 0 {
					t.Fatal("no events")
				}
				assertReportOmits(t, report, secret, upper, lower, "Pl%E4tzchen", "Pl%e4tzchen")
			})
		}
	}
}

func cp1252FormAction(base, body string) dsl.Action {
	return dsl.Action{
		Name: "Post", Type: "POST", Target: base + "/form",
		Headers:      map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:         body,
		BodyEncoding: "windows-1252",
		Metadata:     map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportURLFormTemplateValueEncodingQueryComponent},
	}
}

// A structured Aura body re-quotes a leaf once per enclosing JSON-string layer
// (HTML-safe) and then form-escapes the message. A platform value one or two
// layers deep is sent in a spelling the single-level matcher variants never
// produce; it stays redacted in every mode, whether named directly or carried
// by a variable assigned from {{secret.*}}.
func TestReplayStructuredAuraNestedPlatformSpellingStaysRedacted(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer server.Close()
	const secret = "Tok&en\"PLAT\\7781<x>"
	quote := func(value string) string {
		data, _ := json.Marshal(value)
		return strings.Trim(string(data), `"`)
	}
	for _, layers := range []int{1, 2} {
		for _, ref := range []string{"secret.TOKEN", "var.tok"} {
			// Build params.payload nested `layers` deep as JSON strings.
			inner := `{"value":"{{` + ref + `}}"}`
			for i := 1; i < layers; i++ {
				data, _ := json.Marshal(inner)
				inner = `{"payload":` + string(data) + `}`
			}
			payload, _ := json.Marshal(inner)
			message := `{"actions":[{"id":"a","descriptor":"fixture","params":{"payload":` + string(payload) + `}}]}`
			body := "message=" + url.QueryEscape(message) + "&aura.context=" + url.QueryEscape(`{"fwuid":"f"}`)
			spelled := secret
			var needles []string
			for i := 0; i <= layers; i++ {
				spelled = quote(spelled)
				needles = append(needles, spelled)
			}
			form := url.QueryEscape(spelled)
			needles = append(needles, secret, form, strings.ToLower(form))
			for _, mode := range platformSpellingModes {
				t.Run(ref+"/"+string(rune('0'+layers))+"/"+mode.name, func(t *testing.T) {
					action := structuredAuraAction(body, "")
					action.Target = server.URL + "/aura"
					action.Assertions = nil
					actions := []dsl.Action{action}
					if strings.HasPrefix(ref, "var.") {
						actions = append([]dsl.Action{{Name: "SaveTok", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{secret.TOKEN}}", "tok"}}}}, actions...)
					}
					script := setRunEvidenceRedaction(&dsl.Script{
						Name: "aura-nested", Protocol: scopeHTTPProtocol,
						Config:  dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
						Actions: actions,
					}, mode.redact)
					report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("aura-nested-"+ref+mode.name, mode.unredacted, map[string]string{"TOKEN": secret}))
					if err != nil {
						t.Fatal(err)
					}
					if len(report.Events) == 0 {
						t.Fatal("no events")
					}
					assertReportOmits(t, report, needles...)
				})
			}
		}
	}
}

// The transformed-body spellings are registered for platform values only: a
// script literal carried by a variable keeps its cp1252 and nested-Aura
// spellings visible in the unredacted debugger and with the setting off.
func TestReplayTransformedBodyScriptLiteralSpellingStaysVisible(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer server.Close()
	const literal = "Lïteral-Nöte-7790"
	raw, _ := charmap.Windows1252.NewEncoder().String(literal)
	cp1252Spelling := url.QueryEscape(raw)
	nested := `{"value":"{{var.note}}"}`
	payload, _ := json.Marshal(nested)
	message := `{"actions":[{"id":"a","descriptor":"fixture","params":{"payload":` + string(payload) + `}}]}`
	auraAction := structuredAuraAction("message="+url.QueryEscape(message), "")
	auraAction.Target = server.URL + "/aura"
	auraAction.Assertions = nil
	for _, tc := range []struct {
		name   string
		action dsl.Action
		needle string
	}{
		{name: "cp1252", action: cp1252FormAction(server.URL, "note={{var.note}}"), needle: cp1252Spelling},
		{name: "aura", action: auraAction, needle: "L%C3%AFteral-N%C3%B6te-7790"},
	} {
		for _, mode := range []struct {
			name       string
			unredacted bool
		}{{"setting-off", false}, {"unredacted-debug", true}} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{
					Name: "literal-xform", Protocol: scopeHTTPProtocol,
					Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
					Actions: []dsl.Action{
						{Name: "SaveNote", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{literal, "note"}}},
						tc.action,
					},
				}, false)
				report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("literal-xform-"+tc.name+mode.name, mode.unredacted, map[string]string{"other": "unused-platform-7791"}))
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(report)
				if !strings.Contains(string(encoded), tc.needle) {
					t.Fatalf("script-literal spelling %q hidden: %s", tc.needle, encoded)
				}
			})
		}
	}
}

// Repeated occurrences of one platform value spell it the same way each time:
// they must not use up the per-action spelling budget, so a second, distinct
// platform value later in the same body is still registered and redacted.
func TestReplayTransformedBodyRepeatedPlatformValueKeepsLaterSpellings(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer server.Close()
	const first = "Grüße-77"
	const second = "Plätzchen-9931"
	raw, _ := charmap.Windows1252.NewEncoder().String(second)
	upper := url.QueryEscape(raw)
	body := "x=" + strings.Repeat("{{var.a}}", 1400) + "&z={{var.b}}"
	script := setRunEvidenceRedaction(&dsl.Script{
		Name: "spelling-dedupe", Protocol: scopeHTTPProtocol,
		Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
		Actions: []dsl.Action{
			{Name: "SaveA", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{secret.pw}}", "a"}}},
			{Name: "SaveB", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{secret.pw2}}", "b"}}},
			cp1252FormAction(server.URL, body),
		},
	}, false)
	report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("spelling-dedupe", false, map[string]string{"pw": first, "pw2": second}))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) == 0 {
		t.Fatal("no events")
	}
	assertReportOmits(t, report, second, upper, lowerPercentHexDigits(upper))
}

// The per-action spelling budget bounds only script-sourced spellings. Platform
// spellings are never capped: every one reaches the run matcher and the
// debugger mask, so a body inserting more distinct platform spellings than the
// budget stays redacted on every surface in every mode — including the
// unredacted debugger, whose evidence is built from the wire body and scrubbed
// only by the mask — for cp1252 url-form and structured Aura bodies alike.
func TestReplayTransformedBodyPlatformSpellingOverflowWithholdsBody(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer server.Close()
	previous := maxPlatformBodySpellings
	maxPlatformBodySpellings = 6
	defer func() { maxPlatformBodySpellings = previous }()
	quote := func(value string) string {
		data, _ := json.Marshal(value)
		return strings.Trim(string(data), `"`)
	}
	const count = 4
	for _, kind := range []string{"cp1252", "aura"} {
		secrets := map[string]string{}
		var saves []dsl.Action
		var needles []string
		var form, leaves strings.Builder
		for i := 0; i < count; i++ {
			name := "s" + strconv.Itoa(i)
			if kind == "cp1252" {
				secrets[name] = "Äb-" + strconv.Itoa(100000+i)
				raw, _ := charmap.Windows1252.NewEncoder().String(secrets[name])
				upper := url.QueryEscape(raw)
				needles = append(needles, upper, lowerPercentHexDigits(upper))
			} else {
				secrets[name] = "Tk\"q<" + strconv.Itoa(100000+i) + ">"
				twice := quote(quote(secrets[name]))
				escaped := url.QueryEscape(twice)
				needles = append(needles, quote(secrets[name]), twice, escaped, strings.ToLower(escaped))
			}
			needles = append(needles, secrets[name])
			saves = append(saves, dsl.Action{Name: "Save" + name, Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{secret." + name + "}}", "v" + name}}})
			if i > 0 {
				form.WriteByte('&')
				leaves.WriteByte(',')
			}
			form.WriteString("f" + strconv.Itoa(i) + "={{var.v" + name + "}}")
			leaves.WriteString(`"f` + strconv.Itoa(i) + `":"{{var.v` + name + `}}"`)
		}
		var post dsl.Action
		if kind == "cp1252" {
			post = cp1252FormAction(server.URL, form.String())
		} else {
			payload, _ := json.Marshal("{" + leaves.String() + "}")
			message := `{"actions":[{"id":"a","descriptor":"fixture","params":{"payload":` + string(payload) + `}}]}`
			post = structuredAuraAction("message="+url.QueryEscape(message), "")
			post.Target = server.URL + "/aura"
			post.Assertions = nil
		}
		for _, mode := range platformSpellingModes {
			t.Run(kind+"/"+mode.name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{
					Name: "spelling-overflow", Protocol: scopeHTTPProtocol,
					Config:  dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
					Actions: append(append([]dsl.Action(nil), saves...), post),
				}, mode.redact)
				report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("spelling-overflow-"+kind+mode.name, mode.unredacted, secrets))
				if err != nil {
					t.Fatal(err)
				}
				if len(report.Events) == 0 {
					t.Fatal("no events")
				}
				assertReportOmits(t, report, needles...)
			})
		}
	}
}

// With config.log.redact_run_evidence on, a body inserting more distinct
// script-sourced spellings than the per-action budget has its request body
// evidence withheld rather than leaving the spellings past the budget visible.
func TestReplayTransformedBodyScriptSpellingOverflowWithholdsBody(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer server.Close()
	previous := maxPlatformBodySpellings
	maxPlatformBodySpellings = 6
	defer func() { maxPlatformBodySpellings = previous }()
	var body strings.Builder
	var actions []dsl.Action
	var needles []string
	for i := 0; i < 4; i++ {
		name := "v" + strconv.Itoa(i)
		literal := "Äl-" + strconv.Itoa(200000+i)
		raw, _ := charmap.Windows1252.NewEncoder().String(literal)
		upper := url.QueryEscape(raw)
		needles = append(needles, upper, lowerPercentHexDigits(upper))
		// The literal is a script-sourced credential: a typed auth password.
		actions = append(actions,
			dsl.Action{Name: "Login" + name, Type: "GET", Target: server.URL + "/login", Auth: &dsl.AuthConfig{Type: "basic", Username: "bob", Password: literal}},
			dsl.Action{Name: "Save" + name, Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{literal, name}}})
		if i > 0 {
			body.WriteByte('&')
		}
		body.WriteString("f" + strconv.Itoa(i) + "={{var." + name + "}}")
	}
	script := setRunEvidenceRedaction(&dsl.Script{
		Name: "script-spelling-overflow", Protocol: scopeHTTPProtocol,
		Config:  dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
		Actions: append(actions, cp1252FormAction(server.URL, body.String())),
	}, true)
	report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("script-spelling-overflow", false, map[string]string{"other": "unused-platform-7792"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) == 0 {
		t.Fatal("no events")
	}
	assertReportOmits(t, report, append(needles, "f0=")...)
}

// With config.log.redact_run_evidence on, a script-sourced credential follows
// the setting in its transformed-body spellings too: the cp1252 and nested
// Aura wire spellings of a script literal are hidden like its UTF-8 spelling.
func TestReplayTransformedBodyScriptCredentialSpellingRedactedWhenSettingOn(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	const literal = "Grüße-Plätz-4471"
	raw, _ := charmap.Windows1252.NewEncoder().String(literal)
	cp1252Spelling := url.QueryEscape(raw)
	// The Aura literal needs JSON-special characters: a nested JSON-string
	// layer re-quotes it into a spelling the single-level variants never hold.
	const auraLiteral = "Grü\"ße\\Plätz-4472"
	quote := func(value string) string {
		data, _ := json.Marshal(value)
		return strings.Trim(string(data), `"`)
	}
	twice := quote(quote(auraLiteral))
	nested := `{"value":"{{var.note}}"}`
	payload, _ := json.Marshal(nested)
	message := `{"actions":[{"id":"a","descriptor":"fixture","params":{"payload":` + string(payload) + `}}]}`
	auraAction := structuredAuraAction("message="+url.QueryEscape(message), "")
	auraAction.Target = server.URL + "/aura"
	auraAction.Assertions = nil
	for _, tc := range []struct {
		name    string
		literal string
		action  dsl.Action
		needles []string
	}{
		{name: "cp1252", literal: literal, action: cp1252FormAction(server.URL, "note={{var.note}}"), needles: []string{literal, cp1252Spelling, lowerPercentHexDigits(cp1252Spelling)}},
		{name: "aura", literal: auraLiteral, action: auraAction, needles: []string{auraLiteral, twice, url.QueryEscape(twice), lowerPercentHexDigits(url.QueryEscape(twice))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := setRunEvidenceRedaction(&dsl.Script{
				Name: "literal-xform-on", Protocol: scopeHTTPProtocol,
				Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
				Actions: []dsl.Action{
					{Name: "Login", Type: "GET", Target: server.URL + "/login", Auth: &dsl.AuthConfig{Type: "basic", Username: "bob", Password: tc.literal}},
					{Name: "SaveNote", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{tc.literal, "note"}}},
					tc.action,
				},
			}, true)
			report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("literal-xform-on-"+tc.name, false, map[string]string{"other": "unused-platform-7791"}))
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Events) == 0 {
				t.Fatal("no events")
			}
			assertReportOmits(t, report, tc.needles...)
		})
	}
}

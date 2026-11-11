package jmeter

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestTranslateJSR223Script(t *testing.T) {
	tests := []struct {
		name           string
		script         string
		wantActions    int
		wantRecognized int
		wantTotal      int
	}{
		{
			name:           "vars.put literal",
			script:         `vars.put("sig", "abc")`,
			wantActions:    1,
			wantRecognized: 1,
			wantTotal:      1,
		},
		{
			name:           "vars.put from vars.get",
			script:         `vars.put("dst", vars.get("src"))`,
			wantActions:    1,
			wantRecognized: 1,
			wantTotal:      1,
		},
		{
			name:           "log statements",
			script:         "log.info(\"hello\")\nlog.error(\"boom\")",
			wantActions:    2,
			wantRecognized: 2,
			wantTotal:      2,
		},
		{
			name:           "partial: one safe one arbitrary",
			script:         "vars.put(\"a\", \"1\")\nMath.random()",
			wantActions:    1,
			wantRecognized: 1,
			wantTotal:      2,
		},
		{
			name:           "arbitrary code untranslated",
			script:         `def x = service.call(); vars.put("y", x.field)`,
			wantActions:    0,
			wantRecognized: 0,
			wantTotal:      2,
		},
		{
			name:           "comments and imports skipped",
			script:         "import java.util.*\n// set the flag\nvars.put(\"flag\", \"on\")",
			wantActions:    1,
			wantRecognized: 1,
			wantTotal:      1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actions, recognized, total := translateJSR223Script("Script", tc.script)
			if len(actions) != tc.wantActions || recognized != tc.wantRecognized || total != tc.wantTotal {
				t.Fatalf("translate(%q) = actions=%d recognized=%d total=%d, want actions=%d recognized=%d total=%d",
					tc.script, len(actions), recognized, total, tc.wantActions, tc.wantRecognized, tc.wantTotal)
			}
		})
	}
}

func TestResolveJSR223Value(t *testing.T) {
	tests := []struct {
		expr string
		want string
		ok   bool
	}{
		{`"plain"`, "plain", true},
		{`'single'`, "single", true},
		{`"${token}"`, "{{var.token}}", true},
		{`vars.get("name")`, "{{var.name}}", true},
		{`someExpr + 1`, "", false},
		{`"a" + "b"`, "", false},
	}
	for _, tc := range tests {
		got, ok := resolveJSR223Value(tc.expr)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("resolveJSR223Value(%q) = (%q,%v), want (%q,%v)", tc.expr, got, ok, tc.want, tc.ok)
		}
	}
}

func TestConvertJMXTranslatesScriptAndPreservesArbitrary(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <JSR223Sampler testname="Setup" enabled="true">
          <stringProp name="scriptLanguage">groovy</stringProp>
          <stringProp name="script">def token = api.fetch(); vars.put("kept", token)</stringProp>
        </JSR223Sampler>
        <hashTree/>
        <HTTPSamplerProxy testname="Home" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "setup.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	script := result.Scripts[0]
	// The arbitrary source becomes a durable review placeholder before the HTTP
	// request; the inert import metadata retains it for the authorized operator.
	if len(script.Actions) != 2 || script.Actions[0].Type != "review_required" || script.Actions[1].Type != "GET" {
		t.Fatalf("actions = %#v, want review placeholder + HTTP GET", script.Actions)
	}
	if script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("review placeholder metadata = %#v", script.Actions[0].Metadata)
	}
	if !strings.Contains(script.Actions[0].Value, `def token = api.fetch(); vars.put("kept", token)`) {
		t.Fatalf("review_required action Value omitted JSR223 source text: %q", script.Actions[0].Value)
	}
	preserved, _ := script.Metadata.Import.Preserved["jsr223_sources"].([]string)
	if len(preserved) == 0 || !strings.Contains(strings.Join(preserved, "\n"), `def token = api.fetch(); vars.put("kept", token)`) {
		t.Fatalf("expected exact gated JSR223 source, got %#v", script.Metadata.Import.Preserved["jsr223_sources"])
	}
	if result.Stats.ReviewRequired < 1 {
		t.Fatalf("review_required stat = %d, want >= 1", result.Stats.ReviewRequired)
	}
}

func TestConvertJMXListenersArePreservedNotBlocked(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <HTTPSamplerProxy testname="Home" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
        <ResultCollector testname="View Results Tree" enabled="true"/>
        <hashTree/>
        <BackendListener testname="Backend Listener" enabled="true"/>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "listeners.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	for _, name := range result.UnsupportedElements {
		if name == "ResultCollector" || name == "BackendListener" {
			t.Fatalf("listener %q should not be unsupported: %#v", name, result.UnsupportedElements)
		}
	}
	// Listeners are informational, so they must not be counted as review_required.
	if !containsJMeterDiagnostic(result, "JMeter ResultCollector") {
		t.Fatalf("expected an informational ResultCollector diagnostic, got %#v", result.Unsupported)
	}
	for _, item := range result.Unsupported {
		if item.Element == "JMeter ResultCollector" && string(item.Severity) != "info" {
			t.Fatalf("ResultCollector diagnostic severity = %q, want info", item.Severity)
		}
	}
}

func TestConvertJMXPreservesScriptSamplerWithFileTemplateWithoutValidationFailure(t *testing.T) {
	rawGroovy := `vars.put("a", "1")` + "\n" + `def tpl = "{{file.template_var}}"; api.render(tpl)`
	jmx := `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <JSR223Sampler testname="RenderTpl" enabled="true">
          <stringProp name="scriptLanguage">groovy</stringProp>
          <stringProp name="script">` + rawGroovy + `</stringProp>
        </JSR223Sampler>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "tpl.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	script := result.Scripts[0]
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("expected zero ValidateScriptIssues on script sampler with {{file.template_var}}, got %v", issues)
	}
	if got, _ := script.Actions[0].Metadata["source_script"].(string); got != rawGroovy {
		t.Fatalf("action.Metadata[source_script] = %q, want verbatim %q", got, rawGroovy)
	}
	preserved, _ := script.Metadata.Import.Preserved["jsr223_sources"].([]string)
	if len(preserved) == 0 || !strings.Contains(preserved[0], `{{file.template_var}}`) {
		t.Fatalf("expected verbatim {{file.template_var}} in import.preserved, got %#v", preserved)
	}
}

func TestConvertJMXSafeSubsetAndRunTemplateScriptSamplersPassDSLValidation(t *testing.T) {
	tests := []struct {
		name      string
		rawGroovy string
	}{
		{
			name:      "safe_subset_undeclared_file_template",
			rawGroovy: `vars.put("tpl", "{{file.template_var}}")`,
		},
		{
			name:      "safe_subset_unsupported_run_template",
			rawGroovy: `log.info("{{run.name}}")`,
		},
		{
			name:      "unrecognized_run_id_and_run_name_in_metadata",
			rawGroovy: `def ctx = "{{run.id}}:{{run.name}}:{{file.template_var}}"; customClient.send(ctx)`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jmx := `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <JSR223Sampler testname="ScriptStep" enabled="true">
          <stringProp name="scriptLanguage">groovy</stringProp>
          <stringProp name="script">` + tc.rawGroovy + `</stringProp>
        </JSR223Sampler>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
			result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "script.jmx"})
			if err != nil {
				t.Fatalf("ConvertJMX returned error: %v", err)
			}
			script := result.Scripts[0]
			if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("expected zero ValidateScriptIssues, got %v (actions=%#v)", issues, script.Actions)
			}
		})
	}

	jmxPlanTemplates := `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan {{run.id}} {{run.name}}" enabled="true">
      <stringProp name="TestPlan.comments">Comment {{run.id}} {{run.name}}</stringProp>
    </TestPlan>
    <hashTree>
      <ThreadGroup testname="Users {{run.id}}" enabled="true"/>
      <hashTree>
        <HTTPSamplerProxy testname="Req {{run.id}}" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.test</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/items</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
	resPlan, err := ConvertJMX([]byte(jmxPlanTemplates), ConvertOptions{FileName: "plan_{{run.id}}.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX plan templates: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(resPlan.Scripts[0]); len(issues) != 0 {
		t.Fatalf("expected zero ValidateScriptIssues on plan templates, got %v", issues)
	}
}

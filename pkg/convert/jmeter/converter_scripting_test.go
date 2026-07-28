package jmeter

import (
	"strings"
	"testing"
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

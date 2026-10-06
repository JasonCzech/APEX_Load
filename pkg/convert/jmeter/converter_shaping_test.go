package jmeter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// shapingTimer renders a Throughput Shaping Timer with (start, end, seconds)
// rows.
func shapingTimer(name string, rows ...[3]string) string {
	var profile strings.Builder
	for index, row := range rows {
		fmt.Fprintf(&profile, `<collectionProp name="row%d"><stringProp name="a">%s</stringProp><stringProp name="b">%s</stringProp><stringProp name="c">%s</stringProp></collectionProp>`, index, row[0], row[1], row[2])
	}
	return `<kg.apc.jmeter.timers.VariableThroughputTimer testclass="kg.apc.jmeter.timers.VariableThroughputTimer" testname="` + name + `" enabled="true"><collectionProp name="load_profile">` + profile.String() + `</collectionProp></kg.apc.jmeter.timers.VariableThroughputTimer>
          <hashTree/>`
}

func shapingSampler(name, children string) string {
	return `<HTTPSamplerProxy testname="` + name + `" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.path">/` + name + `</stringProp>
            <stringProp name="HTTPSampler.method">GET</stringProp>
          </HTTPSamplerProxy>
          <hashTree>` + children + `</hashTree>`
}

func shapingPlan(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="API" enabled="true"/>
      <hashTree>
        ` + body + `
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
}

func convertShapingPlan(t *testing.T, body string) (*dsl.Script, *ConvertResult) {
	t.Helper()
	result, err := ConvertJMX([]byte(shapingPlan(body)), ConvertOptions{FileName: "shaping.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d", len(result.Scripts))
	}
	return result.Scripts[0], result
}

func shapingDiagnostic(result *ConvertResult, text string) bool {
	for _, item := range append(append(diag.Diagnostics(nil), result.Diagnostics...), result.Unsupported...) {
		if strings.Contains(item.Detail, text) {
			return true
		}
	}
	return false
}

// Each request carries its own constant-rate timer. One iteration sends every
// request once, so the slowest timer paces the whole group, as in JMeter.
func TestPerRequestShapingTimersPaceAtTheSlowestRate(t *testing.T) {
	script, result := convertShapingPlan(t,
		shapingSampler("fast", shapingTimer("fast timer", [3]string{"3.1", "3.1", "60"}, [3]string{"3.1", "3.1", "3600"}))+
			shapingSampler("slow", shapingTimer("slow timer", [3]string{"1.1", "1.1", "60"}, [3]string{"1.1", "1.1", "3600"})))
	if script.Config.Pacing.Type != "tps" || script.Config.Pacing.TPS != 1.1 {
		t.Fatalf("pacing = %+v, want 1.1 tps", script.Config.Pacing)
	}
	if !shapingDiagnostic(result, `most restrictive timer ("slow": 1.1 requests/s over 1 request(s))`) ||
		!shapingDiagnostic(result, `"fast" 3.1/s`) || !shapingDiagnostic(result, "the profile lasts 3660 s") {
		t.Fatalf("diagnostics lack the pacing note: %+v", result.Diagnostics)
	}
	if shapingDiagnostic(result, "unrecognized JMX element") {
		t.Fatalf("converted timer still reported unrecognized: %+v", result.Diagnostics)
	}
	if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) != 0 {
		t.Fatalf("invalid script: %v", issues)
	}
}

// A timer beside two requests limits both together: 4 requests/s over two
// requests is 2 iterations/s.
func TestGroupShapingTimerSplitsItsRateAcrossRequests(t *testing.T) {
	script, _ := convertShapingPlan(t,
		shapingTimer("group timer", [3]string{"4", "4", "600"})+
			`<TransactionController testname="tx" enabled="true"/><hashTree>`+shapingSampler("one", "")+shapingSampler("two", "")+`</hashTree>`)
	if script.Config.Pacing.Type != "tps" || script.Config.Pacing.TPS != 2 {
		t.Fatalf("pacing = %+v, want 2 tps", script.Config.Pacing)
	}
}

// A profile that changes rate, or a scope whose request count per iteration
// varies, cannot set one constant rate; the import reports it and leaves the
// requests unpaced rather than pacing from the other timers alone.
func TestUnrepresentableShapingIsReported(t *testing.T) {
	for name, tc := range map[string]struct{ body, reason string }{
		"ramp": {
			shapingSampler("ramped", shapingTimer("ramp timer", [3]string{"1", "5", "60"})) + shapingSampler("steady", shapingTimer("steady timer", [3]string{"9", "9", "60"})),
			"changes rate over time",
		},
		"rate above the pacing ceiling": {
			shapingSampler("flood", shapingTimer("flood timer", [3]string{"2000000", "2000000", "60"})),
			"exceeds the 1000000 TPS pacing ceiling",
		},
		"timer inside a loop": {
			`<LoopController testname="loop" enabled="true"><stringProp name="LoopController.loops">5</stringProp></LoopController><hashTree>` + shapingSampler("looped", shapingTimer("looped timer", [3]string{"2", "2", "60"})) + `</hashTree>`,
			"loop or conditional controller around it",
		},
		"loop": {
			shapingTimer("loop timer", [3]string{"2", "2", "60"}) + `<LoopController testname="loop" enabled="true"><stringProp name="LoopController.loops">3</stringProp></LoopController><hashTree>` + shapingSampler("looped", "") + `</hashTree>`,
			"loop or conditional controller",
		},
	} {
		script, result := convertShapingPlan(t, tc.body)
		if script.Config.Pacing.Type != "" {
			t.Errorf("%s: pacing = %+v, want none", name, script.Config.Pacing)
		}
		if !shapingDiagnostic(result, "were not converted") || !shapingDiagnostic(result, tc.reason) {
			t.Errorf("%s: diagnostics = %+v, want the reason %q", name, result.Diagnostics, tc.reason)
		}
	}
}

// A timer on a sampler this script does not send (an Event Hubs producer in an
// HTTP group) neither paces nor appears in the note.
func TestShapingTimerOnAnotherProtocolIsLeftOut(t *testing.T) {
	hub := `<jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler testname="hub" enabled="true"><stringProp name="eventHubName">hub</stringProp></jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler>
          <hashTree>` + shapingTimer("hub timer", [3]string{"0.5", "0.5", "60"}) + `</hashTree>`
	script, result := convertShapingPlan(t, shapingSampler("api", shapingTimer("api timer", [3]string{"2", "2", "60"}))+hub)
	if script.Config.Pacing.Type != "tps" || script.Config.Pacing.TPS != 2 {
		t.Fatalf("pacing = %+v, want the HTTP timer's 2 tps", script.Config.Pacing)
	}
	if shapingDiagnostic(result, `"hub"`) {
		t.Fatalf("Event Hubs timer appears in the note: %+v", result.Diagnostics)
	}
}

// A Simple Controller only groups requests. It is not an unrecognized element,
// and a Header Manager inside it applies to its requests alone.
func TestSimpleControllerGroupsAndScopesItsChildren(t *testing.T) {
	headers := `<HeaderManager testclass="HeaderManager" testname="Scoped headers" enabled="true">
            <collectionProp name="HeaderManager.headers">
              <elementProp name="" elementType="Header">
                <stringProp name="Header.name">X-Scoped</stringProp>
                <stringProp name="Header.value">inside</stringProp>
              </elementProp>
            </collectionProp>
          </HeaderManager>
          <hashTree/>`
	script, result := convertShapingPlan(t,
		`<GenericController testname="group" enabled="true"/><hashTree>`+headers+shapingSampler("inside", "")+`</hashTree>`+
			shapingSampler("after", ""))
	if len(script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(script.Actions))
	}
	header := func(action dsl.Action) string {
		for _, entry := range action.HeaderList {
			if entry.Name == "X-Scoped" {
				return entry.Value
			}
		}
		return action.Headers["X-Scoped"]
	}
	if header(script.Actions[0]) != "inside" || header(script.Actions[1]) != "" {
		t.Fatalf("X-Scoped = %q / %q, want it on the grouped request only", header(script.Actions[0]), header(script.Actions[1]))
	}
	if shapingDiagnostic(result, "unrecognized JMX element") {
		t.Fatalf("Simple Controller reported unrecognized: %+v", result.Diagnostics)
	}
	for _, element := range result.UnsupportedElements {
		if strings.Contains(element, "GenericController") || strings.Contains(element, "group") {
			t.Fatalf("Simple Controller listed unsupported: %v", result.UnsupportedElements)
		}
	}
}

// Timers that pace only requests outside the script's protocol do not pace
// it: one on a JMS publisher (a protocol the HTTP shell also uses), and one
// beside Event Hubs producers grouped in a Simple Controller.
func TestShapingTimersOverOtherProtocolsAreLeftOut(t *testing.T) {
	jms := `<JMSPublisher testname="publish" enabled="true"><stringProp name="jms.topic">orders</stringProp></JMSPublisher>
          <hashTree>` + shapingTimer("jms timer", [3]string{"0.5", "0.5", "60"}) + `</hashTree>`
	hubs := `<GenericController testname="hubs" enabled="true"/><hashTree>` + shapingTimer("hubs timer", [3]string{"0.5", "0.5", "60"}) +
		`<jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler testname="hub" enabled="true"><stringProp name="eventHubName">hub</stringProp></jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler><hashTree/></hashTree>`
	for name, other := range map[string]string{"jms": jms, "event hubs": hubs} {
		script, _ := convertShapingPlan(t, shapingSampler("api", shapingTimer("api timer", [3]string{"5", "5", "60"}))+other)
		if script.Config.Pacing.Type != "tps" || script.Config.Pacing.TPS != 5 {
			t.Errorf("%s: pacing = %+v, want the HTTP timer's 5 tps", name, script.Config.Pacing)
		}
	}
}

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
// varies, cannot set one constant rate; the import reports it and gates the
// script rather than pacing from the other timers alone or running unpaced.
// A rate above the pacing ceiling is reported without a gate: unpaced
// requests cannot outrun it.
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
		wantGate := !strings.Contains(tc.reason, "pacing ceiling")
		if gated := hasJMeterReviewReason(script.Actions, jmeterShapingReviewReason); gated != wantGate {
			t.Errorf("%s: gated = %v, want %v", name, gated, wantGate)
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

// Review placeholders are not samplers: a SOAP-only thread group that gains
// one (from its sample-error policy or an unconvertible shaping timer) must
// still be promoted to the SOAP protocol and keep its gate.
func TestSOAPPlanWithReviewPlaceholderStaysSOAP(t *testing.T) {
	for name, source := range map[string]string{
		"stop_thread": strings.Replace(sampleSOAPJMX, `<stringProp name="ThreadGroup.num_threads">3</stringProp>`,
			`<stringProp name="ThreadGroup.num_threads">3</stringProp><stringProp name="ThreadGroup.on_sample_error">stopthread</stringProp>`, 1),
		"unconvertible_shaping": strings.Replace(sampleSOAPJMX, "</HTTPSamplerProxy>\n\t\t\t\t<hashTree/>",
			"</HTTPSamplerProxy>\n\t\t\t\t<hashTree>"+shapingTimer("ramp", [3]string{"1", "5", "60"})+"</hashTree>", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if source == sampleSOAPJMX {
				t.Fatal("fixture edit did not apply")
			}
			result, err := ConvertJMX([]byte(source), ConvertOptions{FileName: "soap.jmx", IncludeAssertions: true})
			if err != nil {
				t.Fatal(err)
			}
			script := result.Scripts[0]
			if script.Protocol != "soap" || !script.Config.SOAP.FaultHandling.FailOnFault {
				t.Fatalf("protocol = %q fail_on_fault = %v, want soap with fail_on_fault", script.Protocol, script.Config.SOAP.FaultHandling.FailOnFault)
			}
			gated := false
			for _, action := range script.Actions {
				gated = gated || action.Type == "review_required"
			}
			if !gated {
				t.Fatalf("the review gate must stay: %+v", script.Actions)
			}
			for _, warning := range result.Warnings {
				if strings.Contains(warning, "non-SOAP samplers") {
					t.Fatalf("placeholder counted as a non-SOAP sampler: %s", warning)
				}
			}
		})
	}
}

// A placeholder that stands for a sampler (here a JSR223 sampler the
// translator cannot handle) is a non-SOAP sampler, so the group stays HTTP
// whether or not the translator understood the script.
func TestSOAPPlanWithUnconvertibleSamplerStaysHTTP(t *testing.T) {
	sampler := `<JSR223Sampler testname="Random pick" enabled="true">
					<stringProp name="scriptLanguage">groovy</stringProp>
					<stringProp name="script">def r = new java.util.Random(); vars.put("pick", String.valueOf(r.nextInt(9)))</stringProp>
				</JSR223Sampler>
				<hashTree/>
			</hashTree>
		</hashTree>
	</hashTree>`
	source := strings.Replace(sampleSOAPJMX, "</hashTree>\n\t\t</hashTree>\n\t</hashTree>", sampler, 1)
	if source == sampleSOAPJMX {
		t.Fatal("fixture edit did not apply")
	}
	result, err := ConvertJMX([]byte(source), ConvertOptions{FileName: "soap.jmx", IncludeAssertions: true})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if !hasJMeterReviewReason(script.Actions, "unsupported_source_script") {
		t.Fatalf("fixture: the JSR223 sampler must stay a review placeholder: %+v", script.Actions)
	}
	if script.Protocol != "http" {
		t.Fatalf("protocol = %q, want http for a group with a non-SOAP sampler", script.Protocol)
	}
	mixed := false
	for _, warning := range result.Warnings {
		mixed = mixed || strings.Contains(warning, "1 SOAP-style samplers and 1 non-SOAP samplers")
	}
	if !mixed {
		t.Fatalf("missing the mixed SOAP warning: %q", result.Warnings)
	}
}

// A plan-level timer gates the groups it paces, but a group with no
// convertible sampler is still dropped: the timer must not turn it into a
// script made only of review placeholders.
func TestPlanLevelShapingTimerKeepsEmptyGroupDropped(t *testing.T) {
	plan := `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      ` + shapingTimer("plan timer", [3]string{"2", "2", "60"}) + `
      <ThreadGroup testname="Main" enabled="true"/>
      <hashTree>` + shapingSampler("live", "") + `</hashTree>
      <ThreadGroup testname="Parked" enabled="true"><stringProp name="ThreadGroup.on_sample_error">stopthread</stringProp></ThreadGroup>
      <hashTree>` + strings.Replace(shapingSampler("off", ""), `enabled="true"`, `enabled="false"`, 1) + `</hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "shaping.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || result.Scripts[0].Name != "Main" {
		names := []string{}
		for _, script := range result.Scripts {
			names = append(names, script.Name)
		}
		t.Fatalf("scripts = %v, want only Main", names)
	}
	if !hasJMeterReviewReason(result.Scripts[0].Actions, jmeterShapingReviewReason) {
		t.Fatal("Main is not gated by the plan-level timer")
	}
	dropped := false
	for _, warning := range result.Warnings {
		dropped = dropped || strings.Contains(warning, "Parked: no executable samplers were converted")
	}
	if !dropped {
		t.Fatalf("warnings = %v, want Parked dropped", result.Warnings)
	}
}

// A script-level gate (an unconvertible shaping timer, a sample-error action
// APEX cannot match, an unbounded thread group, a cross-thread counter) blocks
// every request in the script, so its samplers are review-gated, not
// replay-ready, in the workload accounting.
func TestScriptLevelGatesCountSamplersAsReviewGated(t *testing.T) {
	for name, plan := range map[string]string{
		"shaping": shapingPlan(shapingSampler("ramped", shapingTimer("ramp timer", [3]string{"1", "5", "60"}))),
		"sample error action": strings.Replace(shapingPlan(shapingSampler("one", "")), `<ThreadGroup testname="API" enabled="true"/>`,
			`<ThreadGroup testname="API" enabled="true"><stringProp name="ThreadGroup.on_sample_error">stoptest</stringProp></ThreadGroup>`, 1),
		// JMeter's common "Infinite" loop without a scheduler.
		"unbounded thread group": strings.Replace(shapingPlan(shapingSampler("one", "")), `<ThreadGroup testname="API" enabled="true"/>`,
			`<ThreadGroup testname="API" enabled="true"><stringProp name="LoopController.loops">-1</stringProp><boolProp name="LoopController.continue_forever">true</boolProp><boolProp name="ThreadGroup.scheduler">false</boolProp></ThreadGroup>`, 1),
		"cross-thread counter": counterPlan(counterProps("1", "", "2", "", "false"), false, "${p_TraceID}"),
	} {
		result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "gated.jmx"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		stats := result.Stats
		if stats.EnabledWorkloadSamplers != 1 || stats.ReplayReadyWorkloadSamplers != 0 || stats.ReviewGatedWorkloadSamplers != 1 || stats.BlockedWorkloadSamplers != 1 {
			t.Errorf("%s: stats enabled=%d ready=%d gated=%d blocked=%d, want 1/0/1/1", name, stats.EnabledWorkloadSamplers, stats.ReplayReadyWorkloadSamplers, stats.ReviewGatedWorkloadSamplers, stats.BlockedWorkloadSamplers)
		}
	}
	// Only the gated group's samplers move: another script keeps its own.
	plan := strings.Replace(shapingPlan(shapingSampler("one", "")), `<ThreadGroup testname="API" enabled="true"/>`,
		`<ThreadGroup testname="Free" enabled="true"/><hashTree>`+shapingSampler("free", "")+`</hashTree><ThreadGroup testname="API" enabled="true"><stringProp name="ThreadGroup.on_sample_error">stoptest</stringProp></ThreadGroup>`, 1)
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "gated.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if stats := result.Stats; len(result.Scripts) != 2 || stats.EnabledWorkloadSamplers != 2 || stats.ReplayReadyWorkloadSamplers != 1 || stats.ReviewGatedWorkloadSamplers != 1 {
		t.Errorf("two groups: scripts=%d stats enabled=%d ready=%d gated=%d, want 2/2/1/1", len(result.Scripts), stats.EnabledWorkloadSamplers, stats.ReplayReadyWorkloadSamplers, stats.ReviewGatedWorkloadSamplers)
	}
}

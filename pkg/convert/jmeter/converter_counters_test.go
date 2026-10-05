package jmeter

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// counterPlan is an HTTP plan whose request header uses ${p_TraceID}. The
// counter sits beside the sampler or, with samplerScoped, under it; both
// placements occur in exported plans.
func counterPlan(counterProps string, samplerScoped bool, header string) string {
	counter := `<CounterConfig testclass="CounterConfig" testname="TraceID" enabled="true">` + counterProps + `</CounterConfig>
            <hashTree/>`
	beside, under := counter, ""
	if samplerScoped {
		beside, under = "", counter
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="HTTP" enabled="true"/>
      <hashTree>
        <TransactionController testname="HTTP" enabled="true"/>
        <hashTree>
            %s
            <HTTPSamplerProxy testname="Request" enabled="true">
              <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
              <stringProp name="HTTPSampler.protocol">https</stringProp>
              <stringProp name="HTTPSampler.path">/cams</stringProp>
              <stringProp name="HTTPSampler.method">GET</stringProp>
            </HTTPSamplerProxy>
            <hashTree>
              <HeaderManager testclass="HeaderManager" testname="Headers" enabled="true">
                <collectionProp name="HeaderManager.headers">
                  <elementProp name="" elementType="Header">
                    <stringProp name="Header.name">X-Trace-Id</stringProp>
                    <stringProp name="Header.value">%s</stringProp>
                  </elementProp>
                </collectionProp>
              </HeaderManager>
              <hashTree/>
              %s
            </hashTree>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`, beside, header, under)
}

func counterProps(start, end, incr, format, perUser string) string {
	return `<stringProp name="CounterConfig.start">` + start + `</stringProp>
              <stringProp name="CounterConfig.end">` + end + `</stringProp>
              <stringProp name="CounterConfig.incr">` + incr + `</stringProp>
              <stringProp name="CounterConfig.name">p_TraceID</stringProp>
              <stringProp name="CounterConfig.format">` + format + `</stringProp>
              <boolProp name="CounterConfig.per_user">` + perUser + `</boolProp>`
}

func convertCounterPlan(t *testing.T, jmx string) *dsl.Script {
	t.Helper()
	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "counter.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	return result.Scripts[0]
}

func counterScriptState(script *dsl.Script) (parameter *dsl.Parameter, missing []string, globalGate bool) {
	for index := range script.Parameters {
		if script.Parameters[index].Name == "p_TraceID" {
			parameter = &script.Parameters[index]
		}
	}
	for _, action := range script.Actions {
		if names, ok := action.Metadata["missing_variables"].([]string); ok {
			missing = append(missing, names...)
		}
	}
	globalGate = hasJMeterReviewReason(script.Actions, jmeterGlobalCounterReason)
	return parameter, missing, globalGate
}

func TestPerUserCounterBecomesANativeCounter(t *testing.T) {
	for _, samplerScoped := range []bool{false, true} {
		script := convertCounterPlan(t, counterPlan(counterProps("0", "99999999", "1", "", "true"), samplerScoped, "trace-${p_TraceID}"))
		parameter, missing, globalGate := counterScriptState(script)
		if parameter == nil {
			t.Fatalf("samplerScoped=%v: no p_TraceID parameter in %+v", samplerScoped, script.Parameters)
		}
		if parameter.Type != "counter" || parameter.Start != 0 || parameter.End != 99999999 || parameter.Step != 1 ||
			parameter.OnExhaustion != "wrap" || parameter.Update != "each_iteration" {
			t.Errorf("samplerScoped=%v: parameter = %+v", samplerScoped, parameter)
		}
		if len(missing) != 0 || globalGate {
			t.Errorf("samplerScoped=%v: missing=%v globalGate=%v, want an executable request", samplerScoped, missing, globalGate)
		}
		if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) != 0 {
			t.Errorf("samplerScoped=%v: invalid script: %v", samplerScoped, issues)
		}
	}
}

// A shared counter hands every JMeter thread distinct values. It becomes a
// unique-number counter whose blocks split the source range across the plan's
// threads, so the request runs and values stay distinct across VUsers.
func TestSharedCounterBecomesUniqueNumberBlocks(t *testing.T) {
	plan := strings.Replace(counterPlan(counterProps("0", "9999999", "1", "", "false"), false, "trace-${p_TraceID}"),
		`<ThreadGroup testname="HTTP" enabled="true"/>`,
		`<ThreadGroup testname="HTTP" enabled="true"><stringProp name="ThreadGroup.num_threads">40</stringProp></ThreadGroup>`, 1)
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "counter.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	parameter, missing, globalGate := counterScriptState(script)
	if parameter == nil || len(missing) != 0 || globalGate {
		t.Fatalf("parameter=%+v missing=%v globalGate=%v, want an executable unique counter", parameter, missing, globalGate)
	}
	if parameter.Selection != "unique" || parameter.BlockSize != 250_000 || parameter.Start != 0 || parameter.Step != 1 || parameter.End != 0 || parameter.Update != "each_iteration" {
		t.Fatalf("parameter = %+v, want unique blocks of 10,000,000/40", parameter)
	}
	if issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) != 0 {
		t.Fatalf("invalid script: %v", issues)
	}
	if !dsl.RequiresLRFidelity(script) {
		t.Fatal("unique counter does not require the generator capability that implements it")
	}
	noted := false
	for _, item := range append(append(diag.Diagnostics(nil), result.Diagnostics...), result.Unsupported...) {
		if strings.Contains(item.Detail, "became a unique-number counter") && strings.Contains(item.Detail, "holds 40 blocks") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("diagnostics lack the unique-block note: %+v", result.Diagnostics)
	}
	if parity, _ := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string); parity["counterConfig.shared"] != "partial" {
		t.Fatalf("runtime parity = %#v", parity)
	}
}

// JMeter ignores a leftover reset flag on a shared counter.
func TestSharedCounterIgnoresStaleResetFlag(t *testing.T) {
	props := counterProps("0", "99999999", "1", "", "false") + `
              <boolProp name="CounterConfig.reset_on_tg_iteration">true</boolProp>`
	parameter, missing, globalGate := counterScriptState(convertCounterPlan(t, counterPlan(props, false, "${p_TraceID}")))
	if parameter == nil || parameter.Selection != "unique" || len(missing) != 0 || globalGate {
		t.Fatalf("parameter=%+v missing=%v globalGate=%v, want a unique counter", parameter, missing, globalGate)
	}
	perUser := counterProps("0", "99999999", "1", "", "true") + `
              <boolProp name="CounterConfig.reset_on_tg_iteration">true</boolProp>`
	if parameter, missing, _ := counterScriptState(convertCounterPlan(t, counterPlan(perUser, false, "${p_TraceID}"))); parameter != nil || len(missing) != 1 {
		t.Fatalf("per-user reset counter = %+v missing=%v, want it unconverted", parameter, missing)
	}
}

// Unique-number blocks step by 1; a shared counter with another increment
// keeps the cross-thread gate once a request uses it.
func TestSharedCounterWithLargerIncrementKeepsTheGate(t *testing.T) {
	script := convertCounterPlan(t, counterPlan(counterProps("0", "99999999", "2", "", "false"), false, "${p_TraceID}"))
	if parameter, missing, globalGate := counterScriptState(script); parameter == nil || len(missing) != 0 || !globalGate {
		t.Fatalf("parameter=%+v missing=%v globalGate=%v, want the cross-thread gate", parameter, missing, globalGate)
	}
	unused := convertCounterPlan(t, counterPlan(counterProps("0", "99999999", "2", "", "false"), false, "static"))
	if _, _, gate := counterScriptState(unused); gate {
		t.Fatal("an unused shared counter gated the script")
	}
}

func TestSharedCounterBlockSize(t *testing.T) {
	for _, tc := range []struct {
		name                string
		start, end, threads int
		bounded             bool
		want                int
	}{
		{"narrow range split across threads", 0, 9_999_999, 40, true, 250_000},
		{"wide range keeps the default block", 0, 99_999_999, 40, true, defaultJMeterSharedCounterBlock},
		{"one-thread plan keeps the default block", 0, 99_999_999, 1, true, defaultJMeterSharedCounterBlock},
		{"unknown threads, narrow range", 1, 50, 0, true, 50},
		{"unbounded", 123, 0, 10, false, defaultJMeterSharedCounterBlock},
		{"fewer values than threads", 1, 5, 10, true, 1},
		{"whole int range", math.MinInt, math.MaxInt, 0, true, defaultJMeterSharedCounterBlock},
	} {
		if got := jmeterSharedCounterBlockSize(tc.start, tc.end, tc.bounded, tc.threads); got != tc.want {
			t.Errorf("%s: block = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCounterFormatsAndUnsupportedSettings(t *testing.T) {
	padded := convertCounterPlan(t, counterPlan(counterProps("7", "", "2", "0000", "true"), false, "${p_TraceID}"))
	parameter, missing, _ := counterScriptState(padded)
	if parameter == nil || parameter.PadWidth != 4 || parameter.PadChar != "0" || parameter.End != 0 || parameter.Start != 7 || parameter.Step != 2 || len(missing) != 0 {
		t.Fatalf("padded parameter = %+v missing=%v", parameter, missing)
	}

	for name, props := range map[string]string{
		"grouping format":     counterProps("0", "10", "1", "#,##0", "true"),
		"variable start":      counterProps("${__P(start)}", "10", "1", "", "true"),
		"zero increment":      counterProps("0", "10", "0", "", "true"),
		"maximum below start": counterProps("5", "1", "1", "", "true"),
		"maximum of zero":     counterProps("-5", "0", "1", "", "true"),
		"oversized padding":   counterProps("0", "10", "1", strings.Repeat("0", maxJMeterCounterPadWidth+1), "true"),
	} {
		script := convertCounterPlan(t, counterPlan(props, false, "${p_TraceID}"))
		parameter, missing, _ := counterScriptState(script)
		if parameter != nil || strings.Join(missing, ",") != "p_TraceID" {
			t.Errorf("%s: parameter=%+v missing=%v, want the request to stay gated", name, parameter, missing)
		}
	}
}

// A converted sampler-scoped counter must not also be reported as an
// unrecognized, unconverted element.
func TestSamplerScopedCounterIsNotReportedUnsupported(t *testing.T) {
	result, err := ConvertJMX([]byte(counterPlan(counterProps("0", "", "1", "", "true"), true, "${p_TraceID}")), ConvertOptions{FileName: "counter.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	for _, element := range result.UnsupportedElements {
		if strings.Contains(element, "CounterConfig") {
			t.Fatalf("unsupported elements = %v", result.UnsupportedElements)
		}
	}
	for _, item := range append(append(diag.Diagnostics(nil), result.Diagnostics...), result.Unsupported...) {
		if strings.Contains(item.Element, "CounterConfig") && strings.Contains(item.Detail, "unrecognized") {
			t.Fatalf("converted counter reported unrecognized: %+v", item)
		}
	}
}

// A disabled plan-level counter defines nothing in JMeter; it must not become
// a parameter or gate the script as a shared counter.
func TestDisabledPlanLevelCounterIsIgnored(t *testing.T) {
	jmx := strings.Replace(counterPlan("", false, "${p_TraceID}"),
		`<TestPlan testname="Plan" enabled="true"/>
    <hashTree>`,
		`<TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <CounterConfig testclass="CounterConfig" testname="TraceID" enabled="false">`+counterProps("0", "", "1", "", "false")+`</CounterConfig>
      <hashTree/>`, 1)
	jmx = strings.Replace(jmx, `<CounterConfig testclass="CounterConfig" testname="TraceID" enabled="true"></CounterConfig>
            <hashTree/>`, "", 1)
	if !strings.Contains(jmx, `enabled="false"`) || strings.Contains(jmx, `enabled="true"></CounterConfig>`) {
		t.Fatal("fixture edit failed")
	}
	script := convertCounterPlan(t, jmx)
	parameter, missing, globalGate := counterScriptState(script)
	if parameter != nil || globalGate || strings.Join(missing, ",") != "p_TraceID" {
		t.Fatalf("parameter=%+v globalGate=%v missing=%v, want the variable reported missing", parameter, globalGate, missing)
	}
}

// twoControllerCounterPlan puts a same-named counter under each of two
// controllers, as some plans do, with a request using it under each.
func twoControllerCounterPlan(firstStart, secondStart string, perUser ...string) string {
	mode := func(index int) string {
		if index < len(perUser) {
			return perUser[index]
		}
		return "true"
	}
	controllers := 0
	controller := func(name, start string) string {
		controllers++
		return `<GenericController testname="` + name + `" enabled="true"/>
        <hashTree>
          <CounterConfig testclass="CounterConfig" testname="traceSeq" enabled="true">` +
			strings.Replace(counterProps(start, "99999", "1", "", mode(controllers-1)), "p_TraceID", "traceSeq", 1) + `</CounterConfig>
          <hashTree/>
          <HTTPSamplerProxy testname="` + name + ` request" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.path">/` + name + `/${traceSeq}</stringProp>
            <stringProp name="HTTPSampler.method">GET</stringProp>
          </HTTPSamplerProxy>
          <hashTree/>
        </hashTree>`
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="API" enabled="true"/>
      <hashTree>
        ` + controller("email", firstStart) + `
        ` + controller("sms", secondStart) + `
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
}

// JMeter scopes same-named counters to their controllers; one parameter cannot
// hold two ranges, so a conflicting name stays unconverted and gated.
func TestConflictingSameNamedCountersStayGated(t *testing.T) {
	script := convertCounterPlan(t, twoControllerCounterPlan("1", "123"))
	for _, parameter := range script.Parameters {
		if parameter.Name == "traceSeq" {
			t.Fatalf("conflicting counters became parameter %+v", parameter)
		}
	}
	gated := 0
	for _, action := range script.Actions {
		if names, ok := action.Metadata["missing_variables"].([]string); ok && strings.Join(names, ",") == "traceSeq" {
			gated++
		}
	}
	if gated != 2 {
		t.Fatalf("%d requests gated on traceSeq, want both", gated)
	}

	// A per-user and a shared counter differ in scope even with equal numbers.
	mixed := convertCounterPlan(t, twoControllerCounterPlan("1", "1", "true", "false"))
	for _, parameter := range mixed.Parameters {
		if parameter.Name == "traceSeq" {
			t.Fatalf("per-user and shared counters merged into %+v", parameter)
		}
	}

	identical := convertCounterPlan(t, twoControllerCounterPlan("1", "1"))
	declared := 0
	for _, parameter := range identical.Parameters {
		if parameter.Name == "traceSeq" {
			declared++
		}
	}
	if declared != 1 {
		t.Fatalf("identical counters declared %d parameters, want 1", declared)
	}
	for _, action := range identical.Actions {
		if _, ok := action.Metadata["missing_variables"]; ok {
			t.Fatalf("identical counters left %s gated", action.Name)
		}
	}
}

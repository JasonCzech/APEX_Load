package jmeter

import (
	"strings"
	"testing"
)

// The JMX fixtures in this package hold several dimensions constant that the
// converter is nevertheless sensitive to: every scope declares exactly one
// element of each configuration type, every thread group holds exactly one
// sampler, every configuration element is serialized before the sampler it
// applies to, and every element carries an explicit enabled="true". A JMX
// exported from a real plan varies all four, and the scope-merging code
// (applyJMeterScopeConfig / jmeterDirectScopeTimerMs / overlayJMeterHeaders)
// only has interesting behavior once they do. These tests vary them.

// Dimension: multiplicity. Two enabled elements of the same configuration type
// in one scope must both take effect - headers merge (later occurrence wins a
// name collision), timers sum, assertions accumulate, CSV sources both land -
// and every occurrence must be counted in the reconciliation stats.
func TestConvertJMXAppliesEveryScopeConfigOccurrenceNotJustTheFirst(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HeaderManager testname="First headers" enabled="true"><collectionProp name="HeaderManager.headers">
  <elementProp elementType="Header"><stringProp name="Header.name">X-First</stringProp><stringProp name="Header.value">one</stringProp></elementProp>
  <elementProp elementType="Header"><stringProp name="Header.name">X-Shared</stringProp><stringProp name="Header.value">from-first</stringProp></elementProp>
</collectionProp></HeaderManager><hashTree/>
<HeaderManager testname="Second headers"><collectionProp name="HeaderManager.headers">
  <elementProp elementType="Header"><stringProp name="Header.name">X-Second</stringProp><stringProp name="Header.value">two</stringProp></elementProp>
  <elementProp elementType="Header"><stringProp name="Header.name">x-shared</stringProp><stringProp name="Header.value">from-second</stringProp></elementProp>
</collectionProp></HeaderManager><hashTree/>
<ConstantTimer testname="First pause" enabled="true"><stringProp name="ConstantTimer.delay">100</stringProp></ConstantTimer><hashTree/>
<ConstantTimer testname="Second pause" enabled="true"><stringProp name="ConstantTimer.delay">250</stringProp></ConstantTimer><hashTree/>
<ResponseAssertion testname="First body" enabled="true"><collectionProp name="Asserion.test_strings"><stringProp name="0">alpha</stringProp></collectionProp><stringProp name="Assertion.test_field">Assertion.response_data</stringProp></ResponseAssertion><hashTree/>
<ResponseAssertion testname="Second body" enabled="true"><collectionProp name="Asserion.test_strings"><stringProp name="0">beta</stringProp></collectionProp><stringProp name="Assertion.test_field">Assertion.response_data</stringProp></ResponseAssertion><hashTree/>
<CSVDataSet testname="customers" enabled="true"><stringProp name="filename">customers.csv</stringProp><stringProp name="variableNames">customer_id</stringProp></CSVDataSet><hashTree/>
<CSVDataSet testname="orders" enabled="true"><stringProp name="filename">orders.csv</stringProp><stringProp name="variableNames">order_id</stringProp></CSVDataSet><hashTree/>
<HTTPSamplerProxy testname="Only" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/only</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "repeated-scope-configs.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	script := result.Scripts[0]
	action := script.Actions[0]

	headers := map[string]string{}
	for _, header := range action.HeaderList {
		headers[strings.ToLower(strings.TrimSpace(header.Name))] = header.Value
	}
	if headers["x-first"] != "one" || headers["x-second"] != "two" {
		t.Fatalf("a repeated HeaderManager overwrote instead of merging: %#v", action.HeaderList)
	}
	if headers["x-shared"] != "from-second" {
		t.Fatalf("the later HeaderManager did not win the name collision: %#v", action.HeaderList)
	}
	if len(action.HeaderList) != 3 {
		t.Fatalf("shadowed header was retained alongside its replacement: %#v", action.HeaderList)
	}
	if action.ThinkTime.MeanMs != 350 {
		t.Fatalf("repeated scope timers did not sum: %#v", action.ThinkTime)
	}
	if len(action.Assertions) != 2 {
		t.Fatalf("repeated ResponseAssertions did not accumulate: %#v", action.Assertions)
	}
	if len(script.DataSources) != 2 {
		t.Fatalf("repeated CSVDataSets did not both import: %#v", script.DataSources)
	}
	if result.Stats.Headers != 4 || result.Stats.Timers != 2 || result.Stats.Assertions != 2 || result.Stats.CSVSources != 2 {
		t.Fatalf("repeated occurrences were not all counted: %#v", result.Stats)
	}
}

// Dimension: fan-out. One configuration element and more than one sampler in
// the scope. Every sampler must receive the config, and the config must be
// counted once rather than once per sampler it lands on.
func TestConvertJMXScopeConfigAppliesToEverySamplerAndIsCountedOnce(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HeaderManager testname="Scope headers" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">X-Scope</stringProp><stringProp name="Header.value">shared</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
<ConstantTimer testname="Scope pause" enabled="true"><stringProp name="ConstantTimer.delay">120</stringProp></ConstantTimer><hashTree/>
<ResponseAssertion testname="Scope body" enabled="true"><collectionProp name="Asserion.test_strings"><stringProp name="0">ready</stringProp></collectionProp><stringProp name="Assertion.test_field">Assertion.response_data</stringProp></ResponseAssertion><hashTree/>
<HTTPSamplerProxy testname="One" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/one</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
<HTTPSamplerProxy testname="Two" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/two</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
<HTTPSamplerProxy testname="Three" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/three</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "scope-fanout.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	actions := result.Scripts[0].Actions
	if len(actions) != 3 {
		t.Fatalf("actions = %#v", actions)
	}
	for index, action := range actions {
		if action.Headers["X-Scope"] != "shared" {
			t.Fatalf("action %d did not inherit the scope header: %#v", index, action.Headers)
		}
		if action.ThinkTime.MeanMs != 120 || !action.ThinkTimeAfter {
			t.Fatalf("action %d did not inherit the scope timer: %#v", index, action.ThinkTime)
		}
		if len(action.Assertions) != 1 {
			t.Fatalf("action %d did not inherit the scope assertion: %#v", index, action.Assertions)
		}
	}
	if result.Stats.Headers != 1 || result.Stats.Timers != 1 || result.Stats.Assertions != 1 {
		t.Fatalf("scope config was counted once per sampler instead of once: %#v", result.Stats)
	}
}

// Dimension: serialization order inside a ThreadGroup scope. Plan-level
// after-the-fact ordering is already pinned; the thread-group scope was only
// ever exercised with configuration elements written before the sampler.
func TestConvertJMXThreadGroupScopeConfigsDeclaredAfterSamplerStillApply(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HTTPSamplerProxy testname="Only" enabled="true"><stringProp name="HTTPSampler.path">/only/${customer_id}</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
<ConfigTestElement testname="HTTP Defaults" guiclass="HttpDefaultsGui" enabled="true"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">late.example.test</stringProp></ConfigTestElement><hashTree/>
<HeaderManager testname="Late headers" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">X-Late</stringProp><stringProp name="Header.value">applied</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
<ResponseAssertion testname="Late body" enabled="true"><collectionProp name="Asserion.test_strings"><stringProp name="0">ready</stringProp></collectionProp><stringProp name="Assertion.test_field">Assertion.response_data</stringProp></ResponseAssertion><hashTree/>
<CSVDataSet testname="late_customers" enabled="true"><stringProp name="filename">late.csv</stringProp><stringProp name="variableNames">customer_id</stringProp></CSVDataSet><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "late-scope-configs.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	script := result.Scripts[0]
	action := script.Actions[0]
	if script.Config.BaseURL != "https://late.example.test" {
		t.Fatalf("HTTP defaults declared after the sampler were ignored: %q", script.Config.BaseURL)
	}
	if action.Headers["X-Late"] != "applied" {
		t.Fatalf("HeaderManager declared after the sampler was ignored: %#v", action.Headers)
	}
	if len(action.Assertions) != 1 {
		t.Fatalf("ResponseAssertion declared after the sampler was ignored: %#v", action.Assertions)
	}
	if len(script.DataSources) != 1 || script.DataSources[0].Name != "late_customers" {
		t.Fatalf("CSVDataSet declared after the sampler was ignored: %#v", script.DataSources)
	}
	if action.Target != "/only/{{var.customer_id}}" {
		t.Fatalf("the late CSV variable was not bound in the sampler target: %q", action.Target)
	}
}

// Dimension: ConvertOptions. Every fixture that exercises the shared sample
// plan pins IncludeAssertions=true, so the branch that decides whether a
// synthetic status_code assertion is added is only ever taken one way.
func TestConvertJMXIncludeAssertionsGatesOnlyTheSyntheticStatusAssertion(t *testing.T) {
	withStatus, err := ConvertJMX([]byte(sampleJMX), ConvertOptions{FileName: "checkout.jmx", IncludeAssertions: true})
	if err != nil {
		t.Fatal(err)
	}
	withoutStatus, err := ConvertJMX([]byte(sampleJMX), ConvertOptions{FileName: "checkout.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(withStatus.Scripts) != 1 || len(withStatus.Scripts[0].Actions) != 1 ||
		len(withoutStatus.Scripts) != 1 || len(withoutStatus.Scripts[0].Actions) != 1 {
		t.Fatalf("unexpected shapes: %#v / %#v", withStatus.Scripts, withoutStatus.Scripts)
	}
	enabled := withStatus.Scripts[0].Actions[0].Assertions
	disabled := withoutStatus.Scripts[0].Actions[0].Assertions
	if len(enabled) != 2 || len(disabled) != 1 {
		t.Fatalf("IncludeAssertions did not gate exactly one synthetic assertion: %#v / %#v", enabled, disabled)
	}
	if disabled[0].Type == "status_code" {
		t.Fatalf("the authored assertion was replaced rather than kept: %#v", disabled)
	}
	if enabled[len(enabled)-1].Type != "status_code" || enabled[len(enabled)-1].Expected != 200 {
		t.Fatalf("synthetic status assertion missing: %#v", enabled)
	}
	// The authored ResponseAssertion must survive identically in both modes:
	// IncludeAssertions is an addition, not a mode switch.
	if enabled[0] != disabled[0] {
		t.Fatalf("authored assertion differed by IncludeAssertions: %#v vs %#v", enabled[0], disabled[0])
	}
	// Everything the reconciliation report counts as an authored assertion is
	// independent of the option.
	if withStatus.Stats.Assertions != withoutStatus.Stats.Assertions {
		t.Fatalf("synthetic assertion leaked into authored stats: %d vs %d", withStatus.Stats.Assertions, withoutStatus.Stats.Assertions)
	}
}

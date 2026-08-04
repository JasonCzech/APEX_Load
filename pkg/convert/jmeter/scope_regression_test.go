package jmeter

import (
	"strings"
	"testing"
)

func TestConvertJMXOmitsDisabledSubtree(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree>
<ThreadGroup testname="Users" enabled="true"/><hashTree>
<TransactionController testname="Disabled" enabled="false"/><hashTree>
  <HTTPSamplerProxy testname="Must not run" enabled="true"><stringProp name="HTTPSampler.domain">destroy.example</stringProp><stringProp name="HTTPSampler.path">/delete</stringProp><stringProp name="HTTPSampler.method">DELETE</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
<HTTPSamplerProxy testname="Safe" enabled="true"><stringProp name="HTTPSampler.domain">safe.example</stringProp><stringProp name="HTTPSampler.path">/status</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "disabled.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 || result.Scripts[0].Actions[0].Name != "Safe" {
		t.Fatalf("disabled subtree was converted: %#v", result.Scripts)
	}
	foundIgnored := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "TransactionController" && mapping.Status == "ignored" {
			foundIgnored = true
		}
	}
	if !foundIgnored {
		t.Fatal("disabled subtree has no ignored source mapping")
	}
}

func TestConvertJMXOmitsDisabledTopLevelThreadGroup(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree>
<ThreadGroup testname="Disabled users" enabled="false"/><hashTree>
  <HTTPSamplerProxy testname="Must not run" enabled="true"><stringProp name="HTTPSampler.domain">destroy.example</stringProp><stringProp name="HTTPSampler.path">/reset</stringProp><stringProp name="HTTPSampler.method">DELETE</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
<ThreadGroup testname="Active users" enabled="true"/><hashTree>
  <HTTPSamplerProxy testname="Safe" enabled="true"><stringProp name="HTTPSampler.domain">safe.example</stringProp><stringProp name="HTTPSampler.path">/status</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
</hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "disabled-group.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 || result.Scripts[0].Actions[0].Name != "Safe" {
		t.Fatalf("disabled ThreadGroup was converted: %#v", result.Scripts)
	}
	if result.Stats.ThreadGroups != 1 || result.Stats.TotalSamplers != 2 || result.Stats.SkippedSamplers != 1 {
		t.Fatalf("disabled ThreadGroup stats = %#v", result.Stats)
	}
	assertIgnoredJMeterSourceMapping(t, result, "ThreadGroup")
}

func TestConvertJMXOmitsDisabledTestPlan(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Disabled plan" enabled="false"/><hashTree>
  <ThreadGroup testname="Users" enabled="true"/><hashTree>
    <HTTPSamplerProxy testname="Must not run" enabled="true"><stringProp name="HTTPSampler.domain">destroy.example</stringProp><stringProp name="HTTPSampler.path">/reset</stringProp><stringProp name="HTTPSampler.method">DELETE</stringProp></HTTPSamplerProxy><hashTree/>
  </hashTree>
</hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "disabled-plan.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 0 || result.Stats.ThreadGroups != 0 || result.Stats.TotalSamplers != 1 || result.Stats.SkippedSamplers != 1 {
		t.Fatalf("disabled TestPlan was converted or miscounted: scripts=%#v stats=%#v", result.Scripts, result.Stats)
	}
	assertIgnoredJMeterSourceMapping(t, result, "TestPlan")
}

func assertIgnoredJMeterSourceMapping(t *testing.T, result *ConvertResult, function string) {
	t.Helper()
	if result == nil || result.SourceReview == nil {
		t.Fatal("missing JMeter source review")
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == function && mapping.Status == "ignored" {
			return
		}
	}
	t.Fatalf("missing ignored source mapping for %s: %#v", function, result.SourceReview.Mappings)
}

func TestConvertJMXAppliesSamplerScopedModifiers(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HeaderManager testname="Global" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">X-Scope</stringProp><stringProp name="Header.value">global</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
<HTTPSamplerProxy testname="Scoped" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree>
  <HeaderManager testname="Local" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">x-scope</stringProp><stringProp name="Header.value">local</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
  <ConstantTimer testname="Delay" enabled="true"><stringProp name="ConstantTimer.delay">321</stringProp></ConstantTimer><hashTree/>
  <ResponseAssertion testname="Body" enabled="true"><collectionProp name="Asserion.test_strings"><stringProp name="0">ready</stringProp></collectionProp><stringProp name="Assertion.test_field">Assertion.response_data</stringProp></ResponseAssertion><hashTree/>
</hashTree></hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "scoped.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Scripts[0].Actions[0]
	if len(action.HeaderList) != 1 || !strings.EqualFold(action.HeaderList[0].Name, "x-scope") || action.HeaderList[0].Value != "local" {
		t.Fatalf("scoped header did not replace inherited header: %#v", action.HeaderList)
	}
	if action.ThinkTime.MeanMs != 321 || !action.ThinkTimeAfter {
		t.Fatalf("scoped timer = %#v", action.ThinkTime)
	}
	if len(action.Assertions) == 0 {
		t.Fatal("scoped assertion was omitted")
	}
}

func TestJMeterNestedHeaderManagerReplacesInheritedNameWithoutLeaking(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree>
<HeaderManager testname="Plan headers" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">X-Scope</stringProp><stringProp name="Header.value">plan</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
<ThreadGroup testname="Users" enabled="true"/><hashTree>
  <TransactionController testname="Nested" enabled="true"/><hashTree>
    <HeaderManager testname="Nested headers" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">x-scope</stringProp><stringProp name="Header.value">nested</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
    <HTTPSamplerProxy testname="Inside" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/inside</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
  </hashTree>
  <HTTPSamplerProxy testname="Outside" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/outside</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
</hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "nested-headers.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 2 {
		t.Fatalf("scripts/actions = %#v", result.Scripts)
	}
	inside := result.Scripts[0].Actions[0]
	if len(inside.HeaderList) != 1 || !strings.EqualFold(inside.HeaderList[0].Name, "x-scope") || inside.HeaderList[0].Value != "nested" {
		t.Fatalf("nested header overlay = %#v", inside.HeaderList)
	}
	outside := result.Scripts[0].Actions[1]
	if len(outside.HeaderList) != 1 || !strings.EqualFold(outside.HeaderList[0].Name, "x-scope") || outside.HeaderList[0].Value != "plan" {
		t.Fatalf("nested header leaked to sibling = %#v", outside.HeaderList)
	}
	if result.Stats.Headers != 2 {
		t.Fatalf("header stats = %d, want two authored occurrences", result.Stats.Headers)
	}
}

func TestTestActionPauseAppliesToOnlyNextSampler(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<TestAction testname="Pause" enabled="true"><stringProp name="ActionProcessor.duration">250</stringProp></TestAction><hashTree/>
<HTTPSamplerProxy testname="First" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/one</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
<HTTPSamplerProxy testname="Second" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/two</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "pause.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Scripts[0].Actions
	if len(actions) != 2 || actions[0].ThinkTime.MeanMs != 250 || actions[1].ThinkTime.MeanMs != 0 {
		t.Fatalf("one-shot pause leaked: %#v", actions)
	}
}

func TestJMeterTimersAccumulateAcrossScopesAndOneShotPauseResets(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<ConstantTimer testname="Global" enabled="true"><stringProp name="ConstantTimer.delay">100</stringProp></ConstantTimer><hashTree/>
<TestAction testname="Pause" enabled="true"><stringProp name="ActionProcessor.duration">250</stringProp></TestAction><hashTree/>
<HTTPSamplerProxy testname="First" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/one</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree>
  <ConstantTimer testname="Local A" enabled="true"><stringProp name="ConstantTimer.delay">200</stringProp></ConstantTimer><hashTree/>
  <ConstantTimer testname="Local B" enabled="true"><stringProp name="ConstantTimer.delay">300</stringProp></ConstantTimer><hashTree/>
</hashTree>
<HTTPSamplerProxy testname="Second" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/two</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "cumulative-timers.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Scripts[0].Actions
	if len(actions) != 2 {
		t.Fatalf("actions = %#v", actions)
	}
	if actions[0].ThinkTime.MeanMs != 850 || !actions[0].ThinkTimeAfter {
		t.Fatalf("first timer = %#v after=%t, want 100+250+200+300ms", actions[0].ThinkTime, actions[0].ThinkTimeAfter)
	}
	if actions[1].ThinkTime.MeanMs != 100 || !actions[1].ThinkTimeAfter {
		t.Fatalf("second timer = %#v after=%t, want persistent 100ms only", actions[1].ThinkTime, actions[1].ThinkTimeAfter)
	}
	if result.Stats.Timers != 4 {
		t.Fatalf("timer stats = %d, want 4", result.Stats.Timers)
	}
}

func TestJMeterScopeTimersApplyRegardlessOfSourceOrderAndTestActionStaysPositional(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HTTPSamplerProxy testname="Before" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/before</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
<TestAction testname="Pause" enabled="true"><stringProp name="ActionProcessor.duration">250</stringProp></TestAction><hashTree/>
<ConstantTimer testname="Scope timer after first sampler" enabled="true"><stringProp name="ConstantTimer.delay">100</stringProp></ConstantTimer><hashTree/>
<HTTPSamplerProxy testname="After pause" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/after-pause</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
<HTTPSamplerProxy testname="After consumption" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/after-consumption</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "timer-order.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Scripts[0].Actions
	if len(actions) != 3 {
		t.Fatalf("actions = %#v", actions)
	}
	if actions[0].ThinkTime.MeanMs != 100 || !actions[0].ThinkTimeAfter {
		t.Fatalf("timer declared after first sampler was not scope-wide: %#v", actions[0])
	}
	if actions[1].ThinkTime.MeanMs != 350 || !actions[1].ThinkTimeAfter {
		t.Fatalf("positional TestAction was not added to the next sampler: %#v", actions[1])
	}
	if actions[2].ThinkTime.MeanMs != 100 || !actions[2].ThinkTimeAfter {
		t.Fatalf("one-shot TestAction leaked after consumption: %#v", actions[2])
	}
}

func TestJMeterNestedScopeTimerDoesNotLeakToSiblingSampler(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<ConstantTimer testname="Global" enabled="true"><stringProp name="ConstantTimer.delay">100</stringProp></ConstantTimer><hashTree/>
<TransactionController testname="Nested" enabled="true"/><hashTree>
  <HTTPSamplerProxy testname="Inside" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/inside</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
  <ConstantTimer testname="Nested timer after sampler" enabled="true"><stringProp name="ConstantTimer.delay">200</stringProp></ConstantTimer><hashTree/>
</hashTree>
<HTTPSamplerProxy testname="Outside" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/outside</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "nested-timer-scope.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Scripts[0].Actions
	if len(actions) != 2 || actions[0].ThinkTime.MeanMs != 300 || actions[1].ThinkTime.MeanMs != 100 {
		t.Fatalf("nested timer scope = %#v, want inside=300ms outside=100ms", actions)
	}
}

func TestJMeterTestPlanTimerAppliesToEverySelectedThreadGroup(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree>
<ThreadGroup testname="First users" enabled="true"/><hashTree>
  <HTTPSamplerProxy testname="First" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/first</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
<ThreadGroup testname="Second users" enabled="true"/><hashTree>
  <HTTPSamplerProxy testname="Second" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/second</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
<ConstantTimer testname="Plan timer after groups" enabled="true"><stringProp name="ConstantTimer.delay">125</stringProp></ConstantTimer><hashTree/>
</hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "plan-timer.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	for index, script := range result.Scripts {
		if len(script.Actions) != 1 || script.Actions[0].ThinkTime.MeanMs != 125 || !script.Actions[0].ThinkTimeAfter {
			t.Fatalf("script %d did not inherit TestPlan timer: %#v", index, script.Actions)
		}
	}
	if result.Stats.Timers != 1 {
		t.Fatalf("shared TestPlan timer was counted %d times, want once", result.Stats.Timers)
	}
}

func TestJMeterOneShotPauseConsumedInsideNestedController(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<TestAction testname="Pause" enabled="true"><stringProp name="ActionProcessor.duration">250</stringProp></TestAction><hashTree/>
<TransactionController testname="Nested" enabled="true"/><hashTree>
  <HTTPSamplerProxy testname="Inside" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/inside</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
<HTTPSamplerProxy testname="Outside" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/outside</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "nested-one-shot.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Scripts[0].Actions
	if len(actions) != 2 || actions[0].ThinkTime.MeanMs != 250 || !actions[0].ThinkTimeAfter {
		t.Fatalf("nested action did not consume one-shot pause: %#v", actions)
	}
	if actions[1].ThinkTimeAfter || actions[1].ThinkTime.MeanMs != 0 {
		t.Fatalf("one-shot pause leaked to sibling sampler: %#v", actions[1])
	}
}

func TestJMeterNativeSamplerReceivesScopeLocalAndOneShotTimers(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="DB Users" enabled="true"/><hashTree>
<ConstantTimer testname="Scope" enabled="true"><stringProp name="ConstantTimer.delay">100</stringProp></ConstantTimer><hashTree/>
<TestAction testname="Pause" enabled="true"><stringProp name="ActionProcessor.duration">250</stringProp></TestAction><hashTree/>
<JDBCSampler testname="First query" enabled="true"><stringProp name="query">SELECT 1</stringProp><stringProp name="queryType">Select Statement</stringProp></JDBCSampler><hashTree>
  <ConstantTimer testname="Sampler" enabled="true"><stringProp name="ConstantTimer.delay">75</stringProp></ConstantTimer><hashTree/>
</hashTree>
<JDBCSampler testname="Second query" enabled="true"><stringProp name="query">SELECT 2</stringProp><stringProp name="queryType">Select Statement</stringProp></JDBCSampler><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "native-timers.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || result.Scripts[0].Protocol != "database" || len(result.Scripts[0].Actions) != 2 {
		t.Fatalf("database script = %#v", result.Scripts)
	}
	actions := result.Scripts[0].Actions
	if !actions[0].ThinkTimeAfter || actions[0].ThinkTime.MeanMs != 425 {
		t.Fatalf("first native timer = %#v after=%t, want 100+250+75ms", actions[0].ThinkTime, actions[0].ThinkTimeAfter)
	}
	if !actions[1].ThinkTimeAfter || actions[1].ThinkTime.MeanMs != 100 {
		t.Fatalf("second native timer = %#v after=%t, want persistent 100ms only", actions[1].ThinkTime, actions[1].ThinkTimeAfter)
	}
	if result.Stats.Timers != 3 {
		t.Fatalf("timer stats = %d, want three authored timers", result.Stats.Timers)
	}
}

func TestJMeterReviewOnlyNativeSamplerConsumesOneShotBeforeHTTP(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Mixed Users" enabled="true"/><hashTree>
<TestAction testname="Pause" enabled="true"><stringProp name="ActionProcessor.duration">250</stringProp></TestAction><hashTree/>
<JDBCSampler testname="Review-only query" enabled="true"><stringProp name="query">SELECT 1</stringProp><stringProp name="queryType">Select Statement</stringProp></JDBCSampler><hashTree/>
<HTTPSamplerProxy testname="HTTP after JDBC" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/after</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "mixed-timers.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("mixed script = %#v", result.Scripts)
	}
	action := result.Scripts[0].Actions[0]
	if action.ThinkTimeAfter || action.ThinkTime.MeanMs != 0 {
		t.Fatalf("JDBC-targeted one-shot leaked to later HTTP sampler: %#v", action)
	}
}

func TestJMeterTestPlanConfigurationsInheritIntoEveryThreadGroup(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree>
<ThreadGroup testname="First users" enabled="true"/><hashTree>
  <HTTPSamplerProxy testname="First" enabled="true"><stringProp name="HTTPSampler.path">/one/${customer_id}</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
<ThreadGroup testname="Second users" enabled="true"/><hashTree>
  <HTTPSamplerProxy testname="Second" enabled="true"><stringProp name="HTTPSampler.path">/two/${customer_id}</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree>
<ConfigTestElement testname="HTTP Defaults" guiclass="HttpDefaultsGui" enabled="true"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">plan.example.test</stringProp></ConfigTestElement><hashTree/>
<HeaderManager testname="Plan headers" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">X-Plan</stringProp><stringProp name="Header.value">inherited</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
<CSVDataSet testname="Customers" enabled="true"><stringProp name="filename">data/customers.csv</stringProp><stringProp name="delimiter">,</stringProp></CSVDataSet><hashTree/>
<ResponseAssertion testname="Ready" enabled="true"><collectionProp name="Asserion.test_strings"><stringProp name="0">ready</stringProp></collectionProp><stringProp name="Assertion.test_field">Assertion.response_data</stringProp></ResponseAssertion><hashTree/>
<AuthManager testname="Plan auth" enabled="true"><collectionProp name="AuthManager.auth_list"><elementProp elementType="Authorization"><stringProp name="Authorization.url"></stringProp><stringProp name="Authorization.username">plan-user</stringProp><stringProp name="Authorization.password">plan-password</stringProp><stringProp name="Authorization.mechanism">BASIC</stringProp></elementProp></collectionProp></AuthManager><hashTree/>
<CookieManager testname="Plan cookies" enabled="true"/><hashTree/>
<CacheManager testname="Plan cache" enabled="true"><boolProp name="clearEachIteration">true</boolProp></CacheManager><hashTree/>
</hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{
		FileName: "plan-config.jmx",
		SidecarFiles: map[string][]byte{
			"data/customers.csv": []byte("customer_id,region\n42,east\n"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %#v, want two selected ThreadGroups", result.Scripts)
	}
	for index, script := range result.Scripts {
		if script.Config.BaseURL != "https://plan.example.test" {
			t.Fatalf("script %d base URL = %q", index, script.Config.BaseURL)
		}
		if script.Config.HTTP.Auth.Type != "basic" || script.Config.HTTP.Auth.Username != "plan-user" {
			t.Fatalf("script %d auth = %#v", index, script.Config.HTTP.Auth)
		}
		if script.Config.HTTP.CookiesEnabled == nil || !*script.Config.HTTP.CookiesEnabled {
			t.Fatalf("script %d cookies = %#v", index, script.Config.HTTP.CookiesEnabled)
		}
		if script.Config.WebRuntime.AdditionalAttributes["jmeter.cache_manager.clear_each_iteration"] != "true" {
			t.Fatalf("script %d cache config = %#v", index, script.Config.WebRuntime.AdditionalAttributes)
		}
		if len(script.Actions) != 1 {
			t.Fatalf("script %d actions = %#v", index, script.Actions)
		}
		action := script.Actions[0]
		if action.Headers["X-Plan"] != "inherited" || len(action.Assertions) != 1 {
			t.Fatalf("script %d inherited action config = headers:%#v assertions:%#v", index, action.Headers, action.Assertions)
		}
		if len(script.DataSources) != 1 || !script.DataSources[0].FirstRowHeader ||
			strings.Join(script.DataSources[0].Columns, ",") != "customer_id,region" {
			t.Fatalf("script %d data sources = %#v", index, script.DataSources)
		}
	}
	if result.Stats.Headers != 1 || result.Stats.CSVSources != 1 || result.Stats.Assertions != 1 {
		t.Fatalf("shared plan config was double-counted: %#v", result.Stats)
	}
}

func TestParseCSVDataSetHonorsIgnoreFirstLine(t *testing.T) {
	node := &xmlNode{Name: "CSVDataSet", Attrs: map[string]string{"testname": "rows"}, Children: []*xmlNode{
		{Name: "stringProp", Attrs: map[string]string{"name": "filename"}, Text: "rows.csv"},
		{Name: "stringProp", Attrs: map[string]string{"name": "variableNames"}, Text: "id,name"},
		{Name: "boolProp", Attrs: map[string]string{"name": "ignoreFirstLine"}, Text: "true"},
	}}
	if got := parseCSVDataSet(node, nil); !got.FirstRowHeader {
		t.Fatalf("ignoreFirstLine was lost: %#v", got)
	}
}

func TestJMeterSidecarFileUsesOnlyUnambiguousBasenameFallback(t *testing.T) {
	t.Run("multipart filename", func(t *testing.T) {
		data, ok := jmeterSidecarFile(
			map[string][]byte{"customers.csv": []byte("customer_id\n42\n")},
			"data/customers.csv",
		)
		if !ok || string(data) != "customer_id\n42\n" {
			t.Fatalf("basename sidecar = %q/%v", data, ok)
		}
	})

	t.Run("ambiguous archive filenames", func(t *testing.T) {
		_, ok := jmeterSidecarFile(map[string][]byte{
			"a/customers.csv": []byte("alpha\n1\n"),
			"b/customers.csv": []byte("beta\n2\n"),
		}, "data/customers.csv")
		if ok {
			t.Fatal("different same-named archive sidecars were matched by basename")
		}
	})

	t.Run("duplicate aliases of one payload", func(t *testing.T) {
		payload := []byte("customer_id\n42\n")
		data, ok := jmeterSidecarFile(map[string][]byte{
			"a/customers.csv": payload,
			"customers.csv":   append([]byte(nil), payload...),
		}, "data/customers.csv")
		if !ok || string(data) != string(payload) {
			t.Fatalf("duplicate sidecar aliases = %q/%v", data, ok)
		}
	})
}

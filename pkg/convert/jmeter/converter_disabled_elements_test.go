package jmeter

import (
	"strings"
	"testing"
)

// The scope-config and timer guards were only ever confirmed by probe. These
// pin them: a disabled CSVDataSet/HeaderManager/ResponseAssertion/timer must
// never reach the converted script, in either the thread-group scope or a
// sampler's own hashTree scope.

func TestConvertJMXSkipsDisabledScopeConfigs(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HeaderManager testname="Disabled headers" enabled="false"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">X-Disabled</stringProp><stringProp name="Header.value">ghost</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
<HeaderManager testname="Live headers" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">X-Live</stringProp><stringProp name="Header.value">live</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
<CSVDataSet testname="disabled_data" enabled="false"><stringProp name="filename">disabled.csv</stringProp><stringProp name="variableNames">ghost</stringProp></CSVDataSet><hashTree/>
<CSVDataSet testname="live_data" enabled="true"><stringProp name="filename">live.csv</stringProp><stringProp name="variableNames">user</stringProp></CSVDataSet><hashTree/>
<ResponseAssertion testname="Disabled body" enabled="false"><collectionProp name="Asserion.test_strings"><stringProp name="0">must-not-assert</stringProp></collectionProp><stringProp name="Assertion.test_field">Assertion.response_data</stringProp></ResponseAssertion><hashTree/>
<ConstantTimer testname="Disabled pause" enabled="false"><stringProp name="ConstantTimer.delay">9000</stringProp></ConstantTimer><hashTree/>
<HTTPSamplerProxy testname="Only" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/only</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "disabled-scope-configs.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	script := result.Scripts[0]
	action := script.Actions[0]
	if len(action.HeaderList) != 1 || !strings.EqualFold(action.HeaderList[0].Name, "X-Live") {
		t.Fatalf("disabled HeaderManager was applied: %#v", action.HeaderList)
	}
	if len(script.DataSources) != 1 || script.DataSources[0].Name != "live_data" {
		t.Fatalf("disabled CSVDataSet was imported: %#v", script.DataSources)
	}
	if len(action.Assertions) != 0 {
		t.Fatalf("disabled ResponseAssertion was applied: %#v", action.Assertions)
	}
	if action.ThinkTime.MeanMs != 0 {
		t.Fatalf("disabled timer inflated think time: %vms", action.ThinkTime.MeanMs)
	}
	if result.Stats.Timers != 0 || result.Stats.CSVSources != 1 || result.Stats.Assertions != 0 || result.Stats.Headers != 1 {
		t.Fatalf("disabled elements were counted: %#v", result.Stats)
	}
}

func TestConvertJMXSkipsDisabledSamplerScopedModifiers(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HeaderManager testname="Global" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">X-Scope</stringProp><stringProp name="Header.value">global</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
<HTTPSamplerProxy testname="Scoped" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree>
  <HeaderManager testname="Local" enabled="false"><collectionProp name="HeaderManager.headers"><elementProp elementType="Header"><stringProp name="Header.name">x-scope</stringProp><stringProp name="Header.value">local</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>
  <ConstantTimer testname="Delay" enabled="false"><stringProp name="ConstantTimer.delay">321</stringProp></ConstantTimer><hashTree/>
  <ResponseAssertion testname="Body" enabled="false"><collectionProp name="Asserion.test_strings"><stringProp name="0">ready</stringProp></collectionProp><stringProp name="Assertion.test_field">Assertion.response_data</stringProp></ResponseAssertion><hashTree/>
</hashTree></hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "disabled-scoped.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	action := result.Scripts[0].Actions[0]
	if len(action.HeaderList) != 1 || !strings.EqualFold(action.HeaderList[0].Name, "x-scope") || action.HeaderList[0].Value != "global" {
		t.Fatalf("disabled sampler-scoped HeaderManager shadowed the inherited header: %#v", action.HeaderList)
	}
	if action.ThinkTime.MeanMs != 0 || action.ThinkTimeAfter {
		t.Fatalf("disabled sampler-scoped timer was applied: %#v", action.ThinkTime)
	}
	if len(action.Assertions) != 0 {
		t.Fatalf("disabled sampler-scoped ResponseAssertion was applied: %#v", action.Assertions)
	}
	if result.Stats.Timers != 0 || result.Stats.Assertions != 0 {
		t.Fatalf("disabled sampler-scoped elements were counted: %#v", result.Stats)
	}
}

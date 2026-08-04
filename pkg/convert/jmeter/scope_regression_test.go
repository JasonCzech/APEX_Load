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

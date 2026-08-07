package jmeter

import "testing"

func TestConvertJMXSkipsDisabledPostProcessor(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HTTPSamplerProxy testname="Login" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/login</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree>
  <RegexExtractor testname="Disabled token" enabled="false"><stringProp name="RegexExtractor.refname">token</stringProp><stringProp name="RegexExtractor.regex">token=(.+?)&amp;</stringProp></RegexExtractor><hashTree/>
  <RegexExtractor testname="Live session" enabled="true"><stringProp name="RegexExtractor.refname">session</stringProp><stringProp name="RegexExtractor.regex">session=(.+?)&amp;</stringProp></RegexExtractor><hashTree/>
</hashTree></hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "disabled-extractor.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	extractors := result.Scripts[0].Actions[0].Extractors
	if len(extractors) != 1 || extractors[0].Name != "session" {
		t.Fatalf("disabled post-processor was converted: %#v", extractors)
	}
}

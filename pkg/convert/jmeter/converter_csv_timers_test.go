package jmeter

import "testing"

func TestConvertJMXSkipsDisabledTestActionTimerChild(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<TestAction testname="Pause" enabled="true"><stringProp name="ActionProcessor.duration">40</stringProp></TestAction><hashTree>
  <ConstantTimer testname="Disabled pause" enabled="false"><stringProp name="ConstantTimer.delay">5000</stringProp></ConstantTimer><hashTree/>
</hashTree>
<HTTPSamplerProxy testname="First" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/one</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "disabled-testaction-timer.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	if got := result.Scripts[0].Actions[0].ThinkTime.MeanMs; got != 40 {
		t.Fatalf("think time = %vms, want the TestAction duration only (disabled child timer must not apply)", got)
	}
}

package jmeter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertJMXResidualSourceIsDisclosureSafeAndDurablyBlocked(t *testing.T) {
	const (
		scriptCanary    = "jmeter-residual-script-canary-9d87"
		conditionCanary = "jmeter-dynamic-condition-canary-30ad"
	)
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan"/>
    <hashTree>
      <ThreadGroup testname="Users"/>
      <hashTree>
        <JSR223Sampler testname="Setup">
          <stringProp name="scriptLanguage">groovy</stringProp>
          <stringProp name="script"><![CDATA[
            vars.put("safe", "value");
            sign("jmeter-residual-script-canary-9d87");
          ]]></stringProp>
        </JSR223Sampler>
        <hashTree/>
        <IfController testname="Dynamic branch">
          <stringProp name="IfController.condition">${__groovy("jmeter-dynamic-condition-canary-30ad")}</stringProp>
        </IfController>
        <hashTree>
          <HTTPSamplerProxy testname="Request">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.protocol">https</stringProp>
            <stringProp name="HTTPSampler.path">/orders</stringProp>
            <stringProp name="HTTPSampler.method">GET</stringProp>
          </HTTPSamplerProxy>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "residual.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if result.Script == nil {
		t.Fatal("ConvertJMX returned no script")
	}
	var blockers int
	for _, action := range result.Script.Actions {
		if action.Metadata["execution_blocker"] == "import_review_required" {
			blockers++
		}
	}
	if blockers < 2 {
		t.Fatalf("actions = %#v, want durable JSR223 and controller blockers", result.Script.Actions)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		!result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("source review = %#v, want one explicitly redacted source", result.SourceReview)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, canary := range []string{scriptCanary, conditionCanary} {
		if strings.Contains(string(payload), canary) {
			t.Fatalf("complete conversion result disclosed %q: %s", canary, payload)
		}
	}
}

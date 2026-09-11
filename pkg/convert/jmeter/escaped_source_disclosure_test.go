package jmeter

import "testing"

func TestConvertJMXPreservesEntityEncodedCredentialInSourceReview(t *testing.T) {
	const source = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="escaped"/><hashTree>
  <ThreadGroup testname="users"/><hashTree>
    <HeaderManager testname="headers"><collectionProp name="HeaderManager.headers">
      <elementProp name="" elementType="Header">
        <stringProp name="Header.name">X-Api-Key</stringProp>
        <stringProp name="Header.value">jmeter&#45;secret</stringProp>
      </elementProp>
    </collectionProp></HeaderManager><hashTree/>
    <HTTPSamplerProxy testname="request">
      <stringProp name="HTTPSampler.domain">api.example.test</stringProp>
      <stringProp name="HTTPSampler.protocol">https</stringProp>
      <stringProp name="HTTPSampler.path">/orders</stringProp>
      <stringProp name="HTTPSampler.method">GET</stringProp>
    </HTTPSamplerProxy><hashTree/>
  </hashTree>
</hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(source), ConvertOptions{FileName: "escaped.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("secret requirements = %#v, preserved credential should need no reentry", result.SecretRequirements)
	}
	if got := result.Scripts[0].Actions[0].Headers["X-Api-Key"]; got != "jmeter-secret" {
		t.Fatalf("destination header = %q, want decoded imported value", got)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	content := result.SourceReview.SourceFiles[0].Content
	if content != source {
		t.Fatalf("source review changed entity-encoded credential source: %q", content)
	}
}

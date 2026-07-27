package jmeter

import (
	"strings"
	"testing"
)

func TestConvertJMXDoesNotDiscloseEntityEncodedCredentialInSourceReview(t *testing.T) {
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
	if len(result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v, want one projected header credential", result.SecretRequirements)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	content := result.SourceReview.SourceFiles[0].Content
	if strings.Contains(content, `jmeter&#45;secret`) || strings.Contains(content, "jmeter-secret") {
		t.Fatalf("source review disclosed an entity-encoded credential: %q", content)
	}
}

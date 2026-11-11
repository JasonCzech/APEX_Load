package jmeter

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

func TestJDBCLiteralAndPlaceholderValuesRemainInConfiguredDSN(t *testing.T) {
	for _, value := range []string{
		"[REDACTED]",
		"***",
		"%5BREDACTED%5D",
		"W1JFREFDVEVEXQ==",
		"WwBSAEUARABBAEMAVABFAEQAXQA=",
	} {
		t.Run(value, func(t *testing.T) {
			jmx := `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="DB plan" enabled="true"/><hashTree>
    <ThreadGroup testname="Orders DB" enabled="true"/><hashTree>
      <JDBCDataSource testname="Orders Pool" enabled="true">
        <stringProp name="dbUrl">jdbc:postgresql://db.example.test/orders</stringProp>
        <stringProp name="username">orders</stringProp>
        <stringProp name="password">` + value + `</stringProp>
      </JDBCDataSource><hashTree/>
      <JDBCSampler testname="Select orders" enabled="true">
        <stringProp name="query">SELECT 1</stringProp>
        <stringProp name="queryType">Select Statement</stringProp>
      </JDBCSampler><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
			result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "orders.jmx"})
			if err != nil {
				t.Fatalf("ConvertJMX returned error: %v", err)
			}
			if len(result.Scripts) != 1 || len(result.SecretRequirements) != 0 {
				t.Fatalf("scripts/requirements = %d/%#v, want literal DSN without synthetic secret requirement", len(result.Scripts), result.SecretRequirements)
			}
			script := result.Scripts[0]
			if strings.HasPrefix(script.Config.Database.DSN, "{{secret.") || !strings.HasPrefix(script.Config.Database.DSN, "postgres://orders:") {
				t.Fatalf("DSN = %q, want literal postgres DSN", script.Config.Database.DSN)
			}
		})
	}
}

func TestJDBCMissingDBURLSynthesizesWholeDSNBinding(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="DB plan" enabled="true"/><hashTree>
    <ThreadGroup testname="Orders DB" enabled="true"/><hashTree>
      <JDBCDataSource testname="Orders Pool" enabled="true">
        <stringProp name="dbUrl"></stringProp>
        <stringProp name="username">orders</stringProp>
        <stringProp name="password">real-password</stringProp>
      </JDBCDataSource><hashTree/>
      <JDBCSampler testname="Select orders" enabled="true">
        <stringProp name="query">SELECT 1</stringProp>
        <stringProp name="queryType">Select Statement</stringProp>
      </JDBCSampler><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "missing-url.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 || len(result.SecretRequirements) != 1 {
		t.Fatalf("scripts/requirements = %d/%#v", len(result.Scripts), result.SecretRequirements)
	}
	script := result.Scripts[0]
	requirement := result.SecretRequirements[0]
	if !strings.HasPrefix(script.Config.Database.DSN, "{{secret.JMETER_") ||
		script.Config.Database.DSN != requirement.Replacement ||
		requirement.SourceValueAvailable ||
		requirement.SourceValueStatus != credentialpolicy.SourceCredentialValueMissing ||
		requirement.Source.Format != "jmeter" || requirement.Source.Path != "missing-url.jmx" ||
		len(requirement.Targets) != 1 || requirement.Targets[0].Field != "config.database.dsn" {
		t.Fatalf("missing-URL JDBC requirement unexpected: %#v / DSN %q", requirement, script.Config.Database.DSN)
	}
}

func TestJDBCSensitiveConnectionPropertyPreservesLiteralDSN(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="DB plan" enabled="true"/><hashTree>
    <ThreadGroup testname="Orders DB" enabled="true"/><hashTree>
      <JDBCDataSource testname="Orders Pool" enabled="true">
        <stringProp name="dbUrl">jdbc:postgresql://db.example.test/orders</stringProp>
        <stringProp name="username">orders</stringProp>
        <stringProp name="password">real-password</stringProp>
        <stringProp name="connectionProperties">sslmode=require;sslpassword=%5BREDACTED%5D</stringProp>
      </JDBCDataSource><hashTree/>
      <JDBCSampler testname="Select orders" enabled="true">
        <stringProp name="query">SELECT 1</stringProp>
        <stringProp name="queryType">Select Statement</stringProp>
      </JDBCSampler><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "properties.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.SecretRequirements) != 0 ||
		strings.HasPrefix(result.Script.Config.Database.DSN, "{{secret.JMETER_") ||
		!strings.Contains(result.Script.Config.Database.DSN, "sslpassword=%255BREDACTED%255D") {
		t.Fatalf("sensitive JDBC property was rewritten unexpectedly: %#v / %#v", result.SecretRequirements, result.Script.Config.Database)
	}
}

func TestUnresolvedJMeterCredentialBindingsIgnoresPreservedSourceValues(t *testing.T) {
	reports := []requestimport.Report{{
		Requirements: []credentialpolicy.Requirement{
			{Key: "preserved", Value: "literal-secret", SourceValueAvailable: true},
			{Key: "existing_ref", ExistingReference: true},
		},
	}}
	if got := unresolvedJMeterCredentialBindings(reports); got != 0 {
		t.Fatalf("unresolvedJMeterCredentialBindings(preserved) = %d, want 0", got)
	}
	reports[0].Requirements = append(reports[0].Requirements, credentialpolicy.Requirement{
		Key:                  "missing_source",
		SourceValueAvailable: false,
		SourceValueStatus:    credentialpolicy.SourceCredentialValueMissing,
	})
	if got := unresolvedJMeterCredentialBindings(reports); got != 1 {
		t.Fatalf("unresolvedJMeterCredentialBindings(missing) = %d, want 1", got)
	}
}

func TestJMeterBatchCredentialBindingsUseDistinctLogicalNames(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="Batch" enabled="true"/><hashTree>
    <ThreadGroup testname="Users" enabled="true"/><hashTree>
      <JDBCDataSource testname="Pool A" enabled="true">
        <stringProp name="dbUrl"></stringProp>
      </JDBCDataSource><hashTree/>
      <JDBCSampler testname="First" enabled="true"><stringProp name="query">SELECT 1</stringProp><stringProp name="queryType">Select Statement</stringProp></JDBCSampler><hashTree/>
    </hashTree>
    <ThreadGroup testname="Users" enabled="true"/><hashTree>
      <JDBCDataSource testname="Pool B" enabled="true">
        <stringProp name="dbUrl"></stringProp>
      </JDBCDataSource><hashTree/>
      <JDBCSampler testname="Second" enabled="true"><stringProp name="query">SELECT 1</stringProp><stringProp name="queryType">Select Statement</stringProp></JDBCSampler><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "batch.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 2 || len(result.SecretRequirements) != 2 {
		t.Fatalf("scripts/requirements = %d/%#v", len(result.Scripts), result.SecretRequirements)
	}
	first := result.Scripts[0].Config.Database.DSN
	second := result.Scripts[1].Config.Database.DSN
	if first == second {
		t.Fatalf("batch requirements share an ambiguous replacement: %q", first)
	}
	if result.SecretRequirements[0].SuggestedName == result.SecretRequirements[1].SuggestedName {
		t.Fatalf("batch requirements share a logical name: %#v", result.SecretRequirements)
	}
	wantPaths := map[string]bool{
		"scripts[0].actions[0]": false,
		"scripts[1].actions[0]": false,
	}
	for _, mapping := range result.SourceReview.Mappings {
		if _, exists := wantPaths[mapping.TargetPath]; exists {
			wantPaths[mapping.TargetPath] = true
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Fatalf("batch source review lacks script-qualified action path %q: %#v", path, result.SourceReview.Mappings)
		}
	}
}

func TestJMeterDuplicateSamplerNamesKeepCredentialTargetsAndBatchPathsUnambiguous(t *testing.T) {
	const httpJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="Plan" enabled="true"/><hashTree>
    <ThreadGroup testname="Users" enabled="true"/><hashTree>
      <HTTPSamplerProxy testname="Request" enabled="true"><stringProp name="HTTPSampler.domain">first.example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
      <HTTPSamplerProxy testname="request" enabled="true"><stringProp name="HTTPSampler.domain">second.example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	httpResult, err := ConvertJMX([]byte(httpJMX), ConvertOptions{FileName: "duplicates.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(httpResult.Scripts) != 1 || len(httpResult.Scripts[0].Actions) != 2 {
		t.Fatalf("scripts/actions = %#v", httpResult.Scripts)
	}
	if httpResult.Scripts[0].Actions[0].Name != "Request" || httpResult.Scripts[0].Actions[1].Name != "request_2" {
		t.Fatalf("action names = %q / %q, want deterministic case-fold-safe names", httpResult.Scripts[0].Actions[0].Name, httpResult.Scripts[0].Actions[1].Name)
	}
	wantPaths := map[string]string{
		"actions[0]": "Request",
		"actions[1]": "request_2",
	}
	for _, mapping := range httpResult.SourceReview.Mappings {
		if wantName, exists := wantPaths[mapping.TargetPath]; exists {
			if mapping.TargetName != wantName {
				t.Fatalf("mapping %q target name = %q, want %q", mapping.TargetPath, mapping.TargetName, wantName)
			}
			delete(wantPaths, mapping.TargetPath)
		}
	}
	if len(wantPaths) != 0 {
		t.Fatalf("source review lacks unambiguous action paths: %v", wantPaths)
	}

	const ehJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="Plan" enabled="true"/><hashTree>
    <ThreadGroup testname="Users" enabled="true"/><hashTree>
      <jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler testname="Request" enabled="true">
        <stringProp name="namespaceName">first.servicebus.windows.net</stringProp>
        <stringProp name="eventHubName">orders</stringProp>
        <stringProp name="authType">Shared access signature</stringProp>
        <stringProp name="sharedAccessKeyName">write</stringProp>
        <stringProp name="sharedAccessKey"></stringProp>
      </jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler><hashTree/>
      <jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler testname="request" enabled="true">
        <stringProp name="namespaceName">second.servicebus.windows.net</stringProp>
        <stringProp name="eventHubName">orders</stringProp>
        <stringProp name="authType">Shared access signature</stringProp>
        <stringProp name="sharedAccessKeyName">write</stringProp>
        <stringProp name="sharedAccessKey"></stringProp>
      </jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	ehResult, err := ConvertJMX([]byte(ehJMX), ConvertOptions{FileName: "eh-duplicates.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(ehResult.SecretRequirements) != 2 ||
		ehResult.SecretRequirements[0].Targets[0].RequestKey == ehResult.SecretRequirements[1].Targets[0].RequestKey {
		t.Fatalf("credential targets remain ambiguous: %#v", ehResult.SecretRequirements)
	}
}

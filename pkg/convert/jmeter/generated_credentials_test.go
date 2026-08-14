package jmeter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
)

func TestJDBCDisclosurePlaceholdersBecomeConfigurableWholeDSNBindings(t *testing.T) {
	for _, value := range []string{
		"[REDACTED]",
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
			if len(result.Scripts) != 1 || len(result.SecretRequirements) != 1 {
				t.Fatalf("scripts/requirements = %d/%#v", len(result.Scripts), result.SecretRequirements)
			}
			script := result.Scripts[0]
			requirement := result.SecretRequirements[0]
			if !strings.HasPrefix(script.Config.Database.DSN, "{{secret.JMETER_") ||
				script.Config.Database.DSN != requirement.Replacement ||
				requirement.Value != "" || requirement.SourceValueAvailable ||
				requirement.SourceValueStatus != credentialpolicy.SourceCredentialValueRedactedPlaceholder ||
				requirement.Source.Format != "jmeter" || requirement.Source.Path != "orders.jmx" ||
				len(requirement.Targets) != 1 || requirement.Targets[0].Field != "config.database.dsn" ||
				!strings.Contains(requirement.Context, "complete destination connection string") {
				t.Fatalf("JDBC requirement lacks safe descriptive provenance: %#v / DSN %q", requirement, script.Config.Database.DSN)
			}
			payload, marshalErr := json.Marshal([]any{script.Config, script.Actions})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if strings.Contains(string(payload), value) {
				t.Fatalf("unavailable JDBC credential remained executable: %s", payload)
			}
		})
	}
}

func TestJDBCSensitiveConnectionPropertyPlaceholderCannotBypassProjection(t *testing.T) {
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
	if len(result.SecretRequirements) != 1 ||
		result.SecretRequirements[0].SourceValueStatus != credentialpolicy.SourceCredentialValueRedactedPlaceholder ||
		!strings.HasPrefix(result.Script.Config.Database.DSN, "{{secret.JMETER_") {
		t.Fatalf("sensitive JDBC property bypassed projection: %#v / %#v", result.SecretRequirements, result.Script.Config.Database)
	}
}

func TestJDBCAuthoredCredentialPlaceholdersCannotDisappearDuringDSNTranslation(t *testing.T) {
	tests := []struct {
		name       string
		username   string
		password   string
		properties string
	}{
		{
			name:     "redacted username",
			username: "%5BREDACTED%5D",
			password: "real-password",
		},
		{
			name:     "standalone password omitted by JMeter runtime precedence",
			password: "W1JFREFDVEVEXQ==",
		},
		{
			name:       "connection property username",
			properties: "user=%5BREDACTED%5D;sslmode=require",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jmx := `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="DB plan" enabled="true"/><hashTree>
    <ThreadGroup testname="Orders DB" enabled="true"/><hashTree>
      <JDBCDataSource testname="Orders Pool" enabled="true">
        <stringProp name="dbUrl">jdbc:postgresql://db.example.test/orders</stringProp>
        <stringProp name="username">` + test.username + `</stringProp>
        <stringProp name="password">` + test.password + `</stringProp>
        <stringProp name="connectionProperties">` + test.properties + `</stringProp>
      </JDBCDataSource><hashTree/>
      <JDBCSampler testname="Select orders" enabled="true">
        <stringProp name="query">SELECT 1</stringProp>
        <stringProp name="queryType">Select Statement</stringProp>
      </JDBCSampler><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
			result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "authored-fields.jmx"})
			if err != nil {
				t.Fatalf("ConvertJMX returned error: %v", err)
			}
			if len(result.SecretRequirements) != 1 ||
				result.SecretRequirements[0].SourceValueStatus != credentialpolicy.SourceCredentialValueRedactedPlaceholder ||
				!strings.HasPrefix(result.Script.Config.Database.DSN, "{{secret.JMETER_") {
				t.Fatalf("authored placeholder was lost during DSN translation: requirements=%#v config=%#v", result.SecretRequirements, result.Script.Config.Database)
			}
		})
	}
}

func TestJMeterBatchCredentialBindingsUseDistinctLogicalNames(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="Batch" enabled="true"/><hashTree>
    <ThreadGroup testname="Users" enabled="true"/><hashTree>
      <HeaderManager testname="Auth" enabled="true"><collectionProp name="HeaderManager.headers">
        <elementProp name="" elementType="Header"><stringProp name="Header.name">X-API-Key</stringProp><stringProp name="Header.value">[REDACTED]</stringProp></elementProp>
      </collectionProp></HeaderManager><hashTree/>
      <HTTPSamplerProxy testname="First" enabled="true"><stringProp name="HTTPSampler.domain">first.example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
    </hashTree>
    <ThreadGroup testname="Users" enabled="true"/><hashTree>
      <HeaderManager testname="Auth" enabled="true"><collectionProp name="HeaderManager.headers">
        <elementProp name="" elementType="Header"><stringProp name="Header.name">X-API-Key</stringProp><stringProp name="Header.value">[REDACTED]</stringProp></elementProp>
      </collectionProp></HeaderManager><hashTree/>
      <HTTPSamplerProxy testname="Second" enabled="true"><stringProp name="HTTPSampler.domain">second.example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
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
	first := result.Scripts[0].Config.HTTP.DefaultHeaders["X-API-Key"]
	second := result.Scripts[1].Config.HTTP.DefaultHeaders["X-API-Key"]
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
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>
  <TestPlan testname="Plan" enabled="true"/><hashTree>
    <ThreadGroup testname="Users" enabled="true"/><hashTree>
      <HeaderManager testname="Auth" enabled="true"><collectionProp name="HeaderManager.headers">
        <elementProp name="" elementType="Header"><stringProp name="Header.name">X-API-Key</stringProp><stringProp name="Header.value">[REDACTED]</stringProp></elementProp>
      </collectionProp></HeaderManager><hashTree/>
      <HTTPSamplerProxy testname="Request" enabled="true"><stringProp name="HTTPSampler.domain">first.example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
      <HTTPSamplerProxy testname="request" enabled="true"><stringProp name="HTTPSampler.domain">second.example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "duplicates.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 2 {
		t.Fatalf("scripts/actions = %#v", result.Scripts)
	}
	if result.Scripts[0].Actions[0].Name != "Request" || result.Scripts[0].Actions[1].Name != "request_2" {
		t.Fatalf("action names = %q / %q, want deterministic case-fold-safe names", result.Scripts[0].Actions[0].Name, result.Scripts[0].Actions[1].Name)
	}
	if len(result.SecretRequirements) != 2 ||
		result.SecretRequirements[0].Targets[0].RequestKey == result.SecretRequirements[1].Targets[0].RequestKey {
		t.Fatalf("credential targets remain ambiguous: %#v", result.SecretRequirements)
	}
	wantPaths := map[string]string{
		"actions[0]": "Request",
		"actions[1]": "request_2",
	}
	for _, mapping := range result.SourceReview.Mappings {
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
}

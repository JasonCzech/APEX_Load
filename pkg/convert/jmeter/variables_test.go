package jmeter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertJMXProjectsArgumentCredentialWithoutSourceDisclosure(t *testing.T) {
	const canary = "jmeter-argument-canary-73ad"
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Credential variable" enabled="true">
      <elementProp name="TestPlan.user_defined_variables" elementType="Arguments">
        <collectionProp name="Arguments.arguments">
          <elementProp name="token" elementType="Argument">
            <stringProp name="Argument.name">token</stringProp>
            <stringProp name="Argument.value">jmeter-argument-canary-73ad</stringProp>
          </elementProp>
        </collectionProp>
      </elementProp>
    </TestPlan>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <HeaderManager testname="Credentials" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">Authorization</stringProp>
              <stringProp name="Header.value">Bearer ${token}</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <HTTPSamplerProxy testname="Request" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/private</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{
		FileName: "argument-credential.jmx",
		BaseURL:  "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	if action.Headers["Authorization"] != "Bearer "+canary {
		t.Fatalf("Authorization header = %q, want preserved source credential", action.Headers["Authorization"])
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("source-variable requirements = %#v, want no reentry", result.SecretRequirements)
	}
	payload, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("source review disclosed source-variable credential: %s", payload)
	}
}

func TestConvertJMXResolvesArgumentScopesAcrossRequestSurfaces(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Scoped variables" enabled="true">
      <elementProp name="TestPlan.user_defined_variables" elementType="Arguments">
        <collectionProp name="Arguments.arguments">
          <elementProp name="host" elementType="Argument">
            <stringProp name="Argument.name">host</stringProp>
            <stringProp name="Argument.value">api.example.com</stringProp>
          </elementProp>
          <elementProp name="base_path" elementType="Argument">
            <stringProp name="Argument.name">base_path</stringProp>
            <stringProp name="Argument.value">/v1</stringProp>
          </elementProp>
          <elementProp name="tenant" elementType="Argument">
            <stringProp name="Argument.name">tenant</stringProp>
            <stringProp name="Argument.value">plan</stringProp>
          </elementProp>
          <elementProp name="user" elementType="Argument">
            <stringProp name="Argument.name">user</stringProp>
            <stringProp name="Argument.value">service-user</stringProp>
          </elementProp>
          <elementProp name="realm" elementType="Argument">
            <stringProp name="Argument.name">realm</stringProp>
            <stringProp name="Argument.value">ACME</stringProp>
          </elementProp>
        </collectionProp>
      </elementProp>
    </TestPlan>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true">
        <elementProp name="ThreadGroup.user_defined_variables" elementType="Arguments">
          <collectionProp name="Arguments.arguments">
            <elementProp name="tenant" elementType="Argument">
              <stringProp name="Argument.name">tenant</stringProp>
              <stringProp name="Argument.value">thread</stringProp>
            </elementProp>
          </collectionProp>
        </elementProp>
      </ThreadGroup>
      <hashTree>
        <Arguments testname="Request variables" enabled="true">
          <collectionProp name="Arguments.arguments">
            <elementProp name="tenant" elementType="Argument">
              <stringProp name="Argument.name">tenant</stringProp>
              <stringProp name="Argument.value">config</stringProp>
            </elementProp>
          </collectionProp>
        </Arguments>
        <hashTree/>
        <HeaderManager testname="Headers" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">Content-Type</stringProp>
              <stringProp name="Header.value">application/json</stringProp>
            </elementProp>
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">X-Tenant</stringProp>
              <stringProp name="Header.value">${tenant}</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <AuthManager testname="Auth" enabled="true">
          <collectionProp name="AuthManager.auth_list">
            <elementProp name="" elementType="Authorization">
              <stringProp name="Authorization.url"></stringProp>
              <stringProp name="Authorization.username">${user}</stringProp>
              <stringProp name="Authorization.password">{{env.API_PASSWORD}}</stringProp>
              <stringProp name="Authorization.domain">${realm}</stringProp>
              <stringProp name="Authorization.mechanism">BASIC</stringProp>
            </elementProp>
          </collectionProp>
        </AuthManager>
        <hashTree/>
        <HTTPSamplerProxy testname="Request" enabled="true">
          <stringProp name="HTTPSampler.domain">${host}</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">${base_path}/${tenant}</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
          <boolProp name="HTTPSampler.postBodyRaw">true</boolProp>
          <elementProp name="HTTPsampler.Arguments" elementType="Arguments">
            <collectionProp name="Arguments.arguments">
              <elementProp name="" elementType="HTTPArgument">
                <stringProp name="Argument.value">{"label":"${tenant}"}</stringProp>
              </elementProp>
            </collectionProp>
          </elementProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "scopes.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	script := result.Scripts[0]
	action := script.Actions[0]
	if script.Config.BaseURL != "https://api.example.com" ||
		action.Target != "/v1/config" ||
		action.Headers["X-Tenant"] != "config" ||
		action.Body != `{"label":"config"}` {
		t.Fatalf("resolved request = base %q action %#v", script.Config.BaseURL, action)
	}
	if script.Config.HTTP.Auth.Username != "service-user" ||
		script.Config.HTTP.Auth.Password != "{{env.API_PASSWORD}}" {
		t.Fatalf("resolved auth = %#v", script.Config.HTTP.Auth)
	}
	if action.Metadata["execution_blocker"] == "import_missing_variables" {
		t.Fatalf("resolved source variables produced a missing-variable gate: %#v", action.Metadata)
	}
	if !containsJMeterDiagnostic(result, "JMeter variable shadowing") {
		t.Fatalf("diagnostics = %#v, want scope-shadowing provenance", result.Unsupported)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("validation issues = %v", issues)
	}
}

func TestConvertJMXLeavesVariableCyclesAsBlockedReferences(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Cyclic variables" enabled="true">
      <elementProp name="TestPlan.user_defined_variables" elementType="Arguments">
        <collectionProp name="Arguments.arguments">
          <elementProp name="a" elementType="Argument">
            <stringProp name="Argument.name">a</stringProp>
            <stringProp name="Argument.value">${b}</stringProp>
          </elementProp>
          <elementProp name="b" elementType="Argument">
            <stringProp name="Argument.name">b</stringProp>
            <stringProp name="Argument.value">${a}</stringProp>
          </elementProp>
          <elementProp name="label" elementType="Argument">
            <stringProp name="Argument.name">label</stringProp>
            <stringProp name="Argument.value">ready</stringProp>
          </elementProp>
        </collectionProp>
      </elementProp>
    </TestPlan>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <HeaderManager testname="Headers" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">X-Label</stringProp>
              <stringProp name="Header.value">${label}</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <HTTPSamplerProxy testname="Request" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/items/${a}</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{
		FileName: "cycles.jmx",
		BaseURL:  "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	if action.Target != "/items/{{var.a}}" || action.Headers["X-Label"] != "ready" {
		t.Fatalf("cycle resolution changed safe/invalid values: %#v", action)
	}
	if action.Metadata["execution_blocker"] != "import_missing_variables" {
		t.Fatalf("cycle was not durably blocked: %#v", action.Metadata)
	}
	if !containsJMeterDiagnostic(result, "JMeter variable cycle") {
		t.Fatalf("diagnostics = %#v, want cycle finding", result.Unsupported)
	}
	if issues := dsl.ValidateScriptIssues(result.Scripts[0]); len(issues) != 0 {
		t.Fatalf("blocked cycle draft validation issues = %v", issues)
	}
}

func TestConvertJMXPriorExtractorOverridesStaticArgumentProvenance(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Extractor provenance" enabled="true">
      <elementProp name="TestPlan.user_defined_variables" elementType="Arguments">
        <collectionProp name="Arguments.arguments">
          <elementProp name="session_token" elementType="Argument">
            <stringProp name="Argument.name">session_token</stringProp>
            <stringProp name="Argument.value">initial-seed</stringProp>
          </elementProp>
        </collectionProp>
      </elementProp>
    </TestPlan>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <HTTPSamplerProxy testname="Login" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/login</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
        </HTTPSamplerProxy>
        <hashTree>
          <RegexExtractor testname="Session" enabled="true">
            <stringProp name="RegexExtractor.refname">session_token</stringProp>
            <stringProp name="RegexExtractor.regex">token=([^;]+)</stringProp>
          </RegexExtractor>
          <hashTree/>
        </hashTree>
        <HTTPSamplerProxy testname="Profile" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/profile</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
          <boolProp name="HTTPSampler.postBodyRaw">true</boolProp>
          <elementProp name="HTTPsampler.Arguments" elementType="Arguments">
            <collectionProp name="Arguments.arguments">
              <elementProp name="" elementType="HTTPArgument">
                <stringProp name="Argument.value">{"password":"${session_token}"}</stringProp>
              </elementProp>
            </collectionProp>
          </elementProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{
		FileName: "extractor-provenance.jmx",
		BaseURL:  "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts[0].Actions) != 2 {
		t.Fatalf("actions = %#v", result.Scripts[0].Actions)
	}
	action := result.Scripts[0].Actions[1]
	if action.Body != `{"password":"{{var.session_token}}"}` {
		t.Fatalf("response-derived credential reference = %q", action.Body)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("response-derived variable was treated as a source credential: %#v", result.SecretRequirements)
	}
	if _, blocked := action.Metadata["execution_blocker"]; blocked {
		t.Fatalf("proven extractor reference was blocked: %#v", action.Metadata)
	}
}

func containsJMeterString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

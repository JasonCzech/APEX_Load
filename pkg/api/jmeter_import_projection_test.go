package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestJMeterGatedSourceCredentialIsDurableAndMaskedOnRead(t *testing.T) {
	const credential = "canary-jmeter-script-password"
	const source = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <JSR223Sampler testname="Setup" enabled="true">
          <stringProp name="scriptLanguage">groovy</stringProp>
          <stringProp name="script">def password = "canary-jmeter-script-password"; vars.put("password", password)</stringProp>
        </JSR223Sampler>
        <hashTree/>
        <HTTPSamplerProxy testname="Home" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := jmeter.ConvertJMX([]byte(source), jmeter.ConvertOptions{FileName: "credential.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	durableJSON, err := json.Marshal(result.Scripts[0])
	if err != nil {
		t.Fatalf("marshal durable script: %v", err)
	}
	if !strings.Contains(string(durableJSON), credential) {
		t.Fatalf("durable imported script omitted the gated source credential: %s", durableJSON)
	}

	projected := maskScriptSecrets(result.Scripts[0])
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	projectedJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal projected script: %v", err)
	}
	if strings.Contains(string(projectedJSON), credential) {
		t.Fatalf("ordinary script projection disclosed the source credential: %s", projectedJSON)
	}
}

func TestCredentialTraversalSnapshotIsMaskedOnOrdinaryRead(t *testing.T) {
	const credential = "canary-traversal-snapshot-password"
	script := &dsl.Script{
		Name:     "blocked import",
		Version:  "1.0.0",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "test_import",
			Preserved: map[string]any{
				"credential_traversal_source_snapshot_base64": base64.StdEncoding.EncodeToString(
					[]byte(`{"actions":[{"body":"{\"password\":\"` + credential + `\"}"}]}`),
				),
			},
		}},
		Actions: []dsl.Action{{
			Name: "review",
			Type: "source_review",
			Metadata: map[string]any{
				"execution_blocker": "import_review_required",
			},
		}},
	}
	durableJSON, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("marshal durable script: %v", err)
	}
	encodedSnapshot, _ := script.Metadata.Import.Preserved["credential_traversal_source_snapshot_base64"].(string)
	decodedSnapshot, decodeErr := base64.StdEncoding.DecodeString(encodedSnapshot)
	if decodeErr != nil || !strings.Contains(string(decodedSnapshot), credential) ||
		!strings.Contains(string(durableJSON), encodedSnapshot) {
		t.Fatalf("durable snapshot omitted credential bytes: %s", durableJSON)
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	projectedJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal projected script: %v", err)
	}
	if strings.Contains(string(projectedJSON), credential) ||
		strings.Contains(string(projectedJSON), encodedSnapshot) {
		t.Fatalf("ordinary projection disclosed traversal snapshot: %s", projectedJSON)
	}
}

func TestPostmanGatedSourceCredentialIsDurableAndMaskedOnRead(t *testing.T) {
	const credential = "canary-postman-event-password"
	result, err := postman.Convert(postman.ImportRequest{Content: `{
	  "info": {"name": "Gated source"},
	  "item": [{
	    "name": "Login",
	    "event": [{
	      "listen": "prerequest",
	      "script": {"type": "text/javascript", "exec": [
	        "const password = \"canary-postman-event-password\";",
	        "pm.variables.set(\"signature\", sign(password));"
	      ]}
	    }],
	    "request": {"method": "GET", "url": "https://api.example.com/login"}
	  }]
	}`})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	durableJSON, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatalf("marshal durable script: %v", err)
	}
	if !strings.Contains(string(durableJSON), credential) {
		t.Fatalf("durable imported script omitted the gated credential: %s", durableJSON)
	}
	projected := maskScriptSecrets(result.Script)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	projectedJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal projected script: %v", err)
	}
	if strings.Contains(string(projectedJSON), credential) {
		t.Fatalf("ordinary script projection disclosed the gated credential: %s", projectedJSON)
	}
}

func TestK6GatedSourceCredentialIsDurableAndMaskedOnRead(t *testing.T) {
	const credential = "canary-k6-dynamic-password"
	result, err := k6.Convert(k6.ImportRequest{
		Name: "dynamic credential",
		Content: `
const payload = {password: "canary-k6-dynamic-password"};
http.post("https://api.example.com/login", JSON.stringify(payload));
`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	durableJSON, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatalf("marshal durable script: %v", err)
	}
	if !strings.Contains(string(durableJSON), credential) {
		t.Fatalf("durable imported script omitted the gated credential: %s", durableJSON)
	}
	projected := maskScriptSecrets(result.Script)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	projectedJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal projected script: %v", err)
	}
	if strings.Contains(string(projectedJSON), credential) {
		t.Fatalf("ordinary script projection disclosed the gated credential: %s", projectedJSON)
	}
}

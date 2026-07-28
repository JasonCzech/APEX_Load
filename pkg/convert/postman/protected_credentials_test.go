package postman

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestProtectedCredentialRequirementsAreOneShotAndJSONInvisible(t *testing.T) {
	value := randomPostmanProtectedValue(t)
	result, err := Convert(ImportRequest{Content: fmt.Sprintf(`{
	  "info":{"name":"Protected handoff"},
	  "item":[{
	    "name":"Login",
	    "request":{
	      "method":"POST",
	      "url":"https://api.example.test/login",
	      "header":[{"key":"Content-Type","value":"application/xml"}],
	      "body":{"mode":"raw","raw":"<login><password>%s</password></login>"}
	    }
	  }]
	}`, value)})
	if err != nil {
		t.Fatal(err)
	}

	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(publicJSON), value) {
		t.Fatal("destination conversion lost imported credential")
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, preserved credential must not require reentry", requirements)
	}
	if result.ProtectedRequirements.Take() != nil {
		t.Fatal("protected credential requirements were returned more than once")
	}
	scriptJSON, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scriptJSON), value) {
		t.Fatal("destination script lost imported credential")
	}
	sourceJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sourceJSON), value) {
		t.Fatalf("source review disclosed credential: %s", sourceJSON)
	}
}

func TestStaticInnocuousVariableInCredentialSurfaceIsProtectedAndRemovedFromParameters(t *testing.T) {
	const canary = "c7Q"
	for _, useDataSet := range []bool{true, false} {
		name := "parameter_values"
		if useDataSet {
			name = "parameter_data_set"
		}
		t.Run(name, func(t *testing.T) {
			option := useDataSet
			result, err := Convert(ImportRequest{
				Content: `{
				  "info":{"name":"Variable credential"},
				  "variable":[
				    {"key":"authValue","value":"` + canary + `"},
				    {"key":"visibleLabel","value":"ordinary"}
				  ],
				  "item":[{
				    "name":"List",
				    "request":{
				      "method":"GET",
				      "url":"https://api.example.test/items?label={{visibleLabel}}",
				      "header":[{"key":"Authorization","value":"Bearer {{authValue}}"}]
				    }
				  }]
				}`,
				Options: ConvertOptions{UseParameterDataSet: &option},
			})
			if err != nil {
				t.Fatal(err)
			}
			publicJSON, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(publicJSON), canary) {
				t.Fatalf("destination conversion lost static credential variable: %s", publicJSON)
			}
			if got := result.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+canary {
				t.Fatalf("credential header = %q, want imported executable value", got)
			}
			if len(result.Script.Parameters) != 2 ||
				!postmanParameterNames(result, "authValue") ||
				!postmanParameterNames(result, "visibleLabel") {
				t.Fatalf("credential parameter was not preserved: %#v", result.Script.Parameters)
			}
			provenance, ok := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey].([]string)
			if !ok || !containsString(provenance, "authValue") ||
				containsString(provenance, "visibleLabel") {
				t.Fatalf("credential parameter provenance = %#v", provenance)
			}
			if useDataSet {
				if len(result.Script.DataSources) != 1 ||
					!strings.Contains(result.Script.DataSources[0].InlineData, "authValue") ||
					!strings.Contains(result.Script.DataSources[0].InlineData, canary) ||
					!strings.Contains(result.Script.DataSources[0].InlineData, "visibleLabel") ||
					!strings.Contains(result.Script.DataSources[0].InlineData, "ordinary") {
					t.Fatalf("parameter data source lost imported values: %#v", result.Script.DataSources)
				}
			} else if len(result.Script.DataSources) != 0 ||
				!postmanParameterHasValue(result, "authValue", canary) ||
				!postmanParameterHasValue(result, "visibleLabel", "ordinary") {
				t.Fatalf("parameter-value mode changed nonsensitive variable: %#v / %#v", result.Script.Parameters, result.Script.DataSources)
			}
			if result.SourceReview == nil {
				t.Fatal("source review missing")
			}
			sourceJSON, err := json.Marshal(result.SourceReview)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(sourceJSON), canary) {
				t.Fatalf("source review disclosed static credential variable: %s", sourceJSON)
			}
			requirements := result.ProtectedRequirements.Take()
			defer clearPostmanProtectedValues(requirements)
			if len(requirements) != 0 {
				t.Fatalf("protected source-variable requirements = %#v, want no reentry", requirements)
			}
		})
	}
}

func TestCredentialVariableDependencyClosureIsProtectedAndRemoved(t *testing.T) {
	const canary = "postman-nested-credential-canary-5b12"
	result, err := Convert(ImportRequest{
		Content: `{
		  "info":{"name":"Nested variable credential"},
		  "variable":[
		    {"key":"credentialSeed","value":"` + canary + `"},
		    {"key":"authValue","value":"{{credentialSeed}}"},
		    {"key":"visibleLabel","value":"ordinary"}
		  ],
		  "item":[{
		    "name":"List",
		    "request":{
		      "method":"GET",
		      "url":"https://api.example.test/items?label={{visibleLabel}}",
		      "header":[{"key":"Authorization","value":"Bearer {{authValue}}"}]
		    }
		  }]
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+canary {
		t.Fatalf("nested credential header = %q, want imported executable value", got)
	}
	if len(result.Script.Parameters) != 3 {
		t.Fatalf("credential dependency parameters were not preserved: %#v", result.Script.Parameters)
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(publicJSON), canary) {
		t.Fatalf("destination conversion lost nested credential: %s", publicJSON)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 0 {
		t.Fatalf("nested credential requirements = %#v, want no reentry", requirements)
	}
}

func TestCredentialVariableProvenanceIsActionOrderAware(t *testing.T) {
	const canary = "c7Q"
	result, err := Convert(ImportRequest{
		Content: `{
		  "info":{"name":"Ordered variable provenance"},
		  "variable":[{"key":"auth-value","value":"` + canary + `"}],
		  "item":[
		    {
		      "name":"Before capture",
		      "request":{
		        "method":"GET",
		        "url":"https://api.example.test/before",
		        "header":[{"key":"Authorization","value":"Bearer {{auth-value}}"}]
		      }
		    },
		    {
		      "name":"Capture",
		      "request":{"method":"GET","url":"https://api.example.test/session"},
		      "event":[{
		        "listen":"test",
		        "script":{"exec":["pm.environment.set(\"auth-value\", pm.response.headers.get(\"X-Session\"));"]}
		      }]
		    },
		    {
		      "name":"After capture",
		      "request":{
		        "method":"GET",
		        "url":"https://api.example.test/after",
		        "header":[{"key":"Authorization","value":"Bearer {{auth-value}}"}]
		      }
		    }
		  ]
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+canary {
		t.Fatalf("first action credential = %q, want imported executable value", got)
	}
	if got := result.Script.Actions[2].Headers["Authorization"]; got != "Bearer {{var.auth_value}}" {
		t.Fatalf("prior response extractor was not preserved dynamically: %q", got)
	}
	if len(result.Script.Actions[1].Extractors) != 1 ||
		result.Script.Actions[1].Extractors[0].Name != "auth_value" {
		t.Fatalf("response provenance extractor missing: %#v", result.Script.Actions[1].Extractors)
	}
	if len(result.Script.Parameters) != 1 || !postmanParameterNames(result, "auth_value") {
		t.Fatalf("static credential seed was not preserved: %#v", result.Script.Parameters)
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(publicJSON), canary) {
		t.Fatalf("destination conversion lost action-ordered credential: %s", publicJSON)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, want no reentry", requirements)
	}
}

func postmanParameterHasValue(result *ConvertResult, name, value string) bool {
	if result == nil || result.Script == nil {
		return false
	}
	for _, parameter := range result.Script.Parameters {
		if parameter.Name == name && len(parameter.Values) == 1 && parameter.Values[0] == value {
			return true
		}
	}
	return false
}

func randomPostmanProtectedValue(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func clearPostmanProtectedValues(requirements []credentialpolicy.Requirement) {
	for index := range requirements {
		requirements[index].Value = ""
	}
}

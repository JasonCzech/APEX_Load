package postman

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
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
	if strings.Contains(string(publicJSON), value) {
		t.Fatalf("converter JSON disclosed a protected credential value: %s", strings.ReplaceAll(string(publicJSON), value, "<PROTECTED>"))
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 1 || requirements[0].Value != value {
		t.Fatal("protected credential requirement was not transferred intact")
	}
	if result.ProtectedRequirements.Take() != nil {
		t.Fatal("protected credential requirements were returned more than once")
	}
	scriptJSON, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(scriptJSON), value) {
		t.Fatal("projected script retained a protected credential value")
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
			if strings.Contains(string(publicJSON), canary) {
				t.Fatalf("public conversion disclosed static credential variable: %s", publicJSON)
			}
			if got := result.Script.Actions[0].Headers["Authorization"]; !strings.Contains(got, "{{secret.") {
				t.Fatalf("credential variable was not projected: %q", got)
			}
			if len(result.Script.Parameters) != 1 || result.Script.Parameters[0].Name != "visibleLabel" {
				t.Fatalf("credential parameter was not removed without disturbing nonsensitive parameter: %#v", result.Script.Parameters)
			}
			if useDataSet {
				if len(result.Script.DataSources) != 1 ||
					strings.Contains(result.Script.DataSources[0].InlineData, "authValue") ||
					strings.Contains(result.Script.DataSources[0].InlineData, canary) ||
					!strings.Contains(result.Script.DataSources[0].InlineData, "visibleLabel") ||
					!strings.Contains(result.Script.DataSources[0].InlineData, "ordinary") {
					t.Fatalf("parameter data source was not safely projected: %#v", result.Script.DataSources)
				}
			} else if len(result.Script.DataSources) != 0 ||
				len(result.Script.Parameters[0].Values) != 1 ||
				result.Script.Parameters[0].Values[0] != "ordinary" {
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
			if len(requirements) != 1 ||
				requirements[0].Value != "Bearer "+canary ||
				len(requirements[0].SourceVariableNames) != 1 ||
				requirements[0].SourceVariableNames[0] != "authValue" {
				t.Fatalf("protected source-variable handoff = %#v", requirements)
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
	if got := result.Script.Actions[0].Headers["Authorization"]; !strings.Contains(got, "{{secret.") {
		t.Fatalf("nested credential variable was not projected: %q", got)
	}
	if len(result.Script.Parameters) != 1 || result.Script.Parameters[0].Name != "visibleLabel" {
		t.Fatalf("credential dependency closure remained in parameters: %#v", result.Script.Parameters)
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), canary) {
		t.Fatalf("public conversion disclosed nested credential: %s", publicJSON)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 1 || requirements[0].Value != "Bearer "+canary {
		t.Fatalf("nested credential protected handoff = %#v", requirements)
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
	if got := result.Script.Actions[0].Headers["Authorization"]; !strings.Contains(got, "{{secret.") {
		t.Fatalf("future extractor incorrectly proved the first action dynamic: %q", got)
	}
	if got := result.Script.Actions[2].Headers["Authorization"]; got != "Bearer {{var.auth_value}}" {
		t.Fatalf("prior response extractor was not preserved dynamically: %q", got)
	}
	if len(result.Script.Actions[1].Extractors) != 1 ||
		result.Script.Actions[1].Extractors[0].Name != "auth_value" {
		t.Fatalf("response provenance extractor missing: %#v", result.Script.Actions[1].Extractors)
	}
	if len(result.Script.Parameters) != 0 {
		t.Fatalf("static credential seed remained in parameters: %#v", result.Script.Parameters)
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), canary) {
		t.Fatalf("action-ordered conversion disclosed static credential: %s", publicJSON)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 1 || requirements[0].Value != "Bearer "+canary {
		t.Fatalf("protected requirements = %#v", requirements)
	}
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

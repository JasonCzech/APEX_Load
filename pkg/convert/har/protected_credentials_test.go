package har

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
)

func TestProtectedCredentialRequirementsTransferFromSampleConverter(t *testing.T) {
	const value = "har-protected-canary-7096f94f"
	result, err := Convert([]byte(`{
	  "log":{"entries":[{
	    "request":{
	      "method":"POST",
	      "url":"https://api.example.test/login",
	      "headers":[{"name":"Content-Type","value":"application/json"}],
	      "postData":{"mimeType":"application/json","text":"{\"password\":\"`+value+`\"}"}
	    },
	    "response":{"status":204,"headers":[],"content":{"text":""}}
	  }]}
	}`), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}

	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), value) {
		t.Fatal("HAR converter JSON disclosed a protected credential value")
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearHARProtectedValues(requirements)
	if len(requirements) != 1 || requirements[0].Value != value {
		t.Fatalf("protected requirements = %#v, want one transferred source value", requirements)
	}
	if result.ProtectedRequirements.Take() != nil {
		t.Fatal("protected credential requirements were returned more than once")
	}
}

func clearHARProtectedValues(requirements []credentialpolicy.Requirement) {
	for index := range requirements {
		requirements[index].Value = ""
	}
}

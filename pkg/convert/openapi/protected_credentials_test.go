package openapi

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
	value := randomOpenAPIProtectedValue(t)
	result, err := Convert(ImportRequest{Content: fmt.Sprintf(`{
	  "openapi":"3.0.3",
	  "info":{"title":"Protected handoff","version":"1"},
	  "servers":[{"url":"https://api.example.test"}],
	  "paths":{"/login":{"post":{
	    "operationId":"login",
	    "requestBody":{"content":{"application/json":{"example":{"password":%q}}}},
	    "responses":{"204":{"description":"ok"}}
	  }}}
	}`, value)})
	if err != nil {
		t.Fatal(err)
	}

	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), value) {
		t.Fatal("converter JSON disclosed a protected credential value")
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearOpenAPIProtectedValues(requirements)
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

func randomOpenAPIProtectedValue(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func clearOpenAPIProtectedValues(requirements []credentialpolicy.Requirement) {
	for index := range requirements {
		requirements[index].Value = ""
	}
}

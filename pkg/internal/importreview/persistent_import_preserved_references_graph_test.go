package importreview

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestValidatePreservedImportCredentialEditsAllowsMessagingAndSOAPEdits(t *testing.T) {
	prior := &dsl.Script{
		Name: "SOAP & Kafka edits", Version: "1", Protocol: "soap",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{ProxyMode: "system"},
		},
		Actions: []dsl.Action{{
			Name: "SubmitOrder", Type: "soap_request", Method: "POST", Target: "https://soap.example.test/OrderService",
			Body: `<soapenv:Envelope><soapenv:Body><userPassword>SynthSoapSecret99!</userPassword></soapenv:Body></soapenv:Envelope>`,
		}},
	}
	MarkCredentialPreservingImportScript(prior)

	candidate := CloneScriptDefinition(prior)
	candidate.Config.HTTP.ProxyMode = "none"
	candidate.Actions[0].Target = "https://soap-staging.example.test/OrderService"

	if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatalf("ValidatePreservedImportCredentialEdits returned unexpected error: %v", err)
	}
	saved, err := PreparePersistentLibraryScript(candidate, prior)
	if err != nil {
		t.Fatalf("PreparePersistentLibraryScript returned unexpected error: %v", err)
	}
	if saved.Config.HTTP.ProxyMode != "none" {
		t.Fatalf("saved ProxyMode = %q, want none", saved.Config.HTTP.ProxyMode)
	}
}

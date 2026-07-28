package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScriptAndAuthoringWrappersProjectCredentialEquivalentNames(t *testing.T) {
	const credentialValue = "wrapper-name-password-canary-72f1"
	script := &dsl.Script{
		Name:      "Checkout " + credentialValue,
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Password: credentialValue},
			},
		},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	stored := &StoredScript{
		ID:        "credential-name-script",
		Name:      script.Name,
		ProjectID: script.ProjectID,
		Script:    script,
	}

	maskedStored := maskStoredScript(stored)
	if maskedStored == nil || maskedStored.Script == nil {
		t.Fatal("masked stored script is nil")
	}
	for label, value := range map[string]string{
		"stored wrapper": maskedStored.Name,
		"nested script":  maskedStored.Script.Name,
	} {
		if strings.Contains(value, credentialValue) {
			t.Fatalf("%s name disclosed credential: %q", label, value)
		}
	}

	session := &storage.AuthoringSession{
		ID:        "credential-name-authoring",
		ProjectID: script.ProjectID,
		Name:      script.Name,
		Draft:     script,
	}
	maskedSession := maskAuthoringSession(session)
	if maskedSession == nil || maskedSession.Draft == nil {
		t.Fatal("masked authoring session is nil")
	}
	for label, value := range map[string]string{
		"authoring wrapper": maskedSession.Name,
		"authoring draft":   maskedSession.Draft.Name,
	} {
		if strings.Contains(value, credentialValue) {
			t.Fatalf("%s name disclosed credential: %q", label, value)
		}
	}
}

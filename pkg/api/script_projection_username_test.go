package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScriptProjectionPreservesTypedAuthUsernames(t *testing.T) {
	script := &dsl.Script{
		Name:     "typed auth",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type: "basic", Username: "collection-user", Password: "collection-password",
			}},
		},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
			Auth: &dsl.AuthConfig{
				Type: "digest", Username: "request-user", Password: "request-password",
			},
		}},
	}

	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("projection returned nil")
	}
	if projected.Config.HTTP.Auth.Username != "collection-user" {
		t.Fatalf("config username = %q", projected.Config.HTTP.Auth.Username)
	}
	if projected.Actions[0].Auth == nil || projected.Actions[0].Auth.Username != "request-user" {
		t.Fatalf("action auth = %#v", projected.Actions[0].Auth)
	}
	if projected.Config.HTTP.Auth.Password != maskedScriptSecret ||
		projected.Actions[0].Auth.Password != maskedScriptSecret {
		t.Fatalf("passwords were not masked: config=%q action=%q",
			projected.Config.HTTP.Auth.Password,
			projected.Actions[0].Auth.Password,
		)
	}
}

func TestSensitiveScriptKeyDoesNotClassifyUsernameSuffixes(t *testing.T) {
	for _, key := range []string{"username", "proxy_username", "databaseUsername"} {
		if isSensitiveScriptKey(key) {
			t.Fatalf("%q was classified as credential material", key)
		}
	}
}

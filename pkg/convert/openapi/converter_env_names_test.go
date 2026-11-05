package openapi

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

// A security scheme named after a token (apiToken) writes a username env name
// a script can read: OPENAPI_APITOKEN_USERNAME would be blocked for TOKEN.
func TestConvertSecuritySchemeEnvNamesAreReadable(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "swagger":"2.0",
  "info":{"title":"Token Basic","version":"1"},
  "host":"api.example.test",
  "schemes":["https"],
  "securityDefinitions":{"apiToken":{"type":"basic"}},
  "security":[{"apiToken":[]}],
  "paths":{"/orders":{"get":{"operationId":"listOrders","responses":{"200":{"description":"ok"}}}}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "listOrders")
	if action.Auth == nil || !strings.HasPrefix(action.Auth.Username, "{{env.") {
		t.Fatalf("auth = %#v, want an env username", action.Auth)
	}
	name := strings.TrimSuffix(strings.TrimPrefix(action.Auth.Username, "{{env."), "}}")
	if credential.ScriptEnvNameAlwaysBlocked(name) {
		t.Fatalf("username env name %q can never be read by a script", name)
	}
}

// Two schemes whose names differ only in a word the env rewrite folds
// (PASSWORD and PASSWD both become PW) keep separate username variables.
func TestConvertSecuritySchemeEnvNamesStayDistinctAfterRewrite(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "swagger":"2.0",
  "info":{"title":"Two Basic","version":"1"},
  "host":"api.example.test",
  "schemes":["https"],
  "securityDefinitions":{"apiPassword":{"type":"basic"},"apiPasswd":{"type":"basic"}},
  "paths":{
    "/a":{"get":{"operationId":"getA","security":[{"apiPassword":[]}],"responses":{"200":{"description":"ok"}}}},
    "/b":{"get":{"operationId":"getB","security":[{"apiPasswd":[]}],"responses":{"200":{"description":"ok"}}}}
  }
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	a, b := openAPIActionFull(result, "getA"), openAPIActionFull(result, "getB")
	if a.Auth == nil || b.Auth == nil {
		t.Fatalf("auth a=%#v b=%#v", a.Auth, b.Auth)
	}
	if a.Auth.Username == b.Auth.Username {
		t.Fatalf("both schemes read username %s", a.Auth.Username)
	}
	for _, username := range []string{a.Auth.Username, b.Auth.Username} {
		name := strings.TrimSuffix(strings.TrimPrefix(username, "{{env."), "}}")
		if credential.ScriptEnvNameAlwaysBlocked(name) {
			t.Fatalf("username env name %q can never be read by a script", name)
		}
	}
}

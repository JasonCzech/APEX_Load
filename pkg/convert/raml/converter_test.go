package raml

import (
	"strings"
	"testing"
)

func TestConvertRAMLResourcesAndOAuth2(t *testing.T) {
	content := `#%RAML 1.0
title: Accounts API
version: v1
baseUri: https://api.example.test/v1
securitySchemes:
  oauth_2_0:
    type: OAuth 2.0
    settings:
      accessTokenUri: https://login.example.test/oauth/token
/accounts:
  get:
    displayName: List Accounts
  post:
    displayName: Create Account
    body:
      application/json:
        example: |
          {"name":"Acme"}
  /{accountId}:
    get:
      displayName: Get Account
`
	result, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script == nil {
		t.Fatalf("script missing")
	}
	if result.Script.Config.BaseURL != "https://api.example.test/v1" {
		t.Fatalf("base_url = %q", result.Script.Config.BaseURL)
	}
	if len(result.Script.Init) != 1 || result.Script.Init[0].Type != "http_oauth2_token" {
		t.Fatalf("init = %#v, want OAuth token action", result.Script.Init)
	}
	if got := result.Script.Init[0].Params["token_url"]; got != "https://login.example.test/oauth/token" {
		t.Fatalf("token_url = %#v", got)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions = %d, want 3: %#v", len(result.Script.Actions), result.Script.Actions)
	}
	if result.Script.Actions[0].Target != "/accounts" || result.Script.Actions[0].Method != "GET" {
		t.Fatalf("first action = %#v", result.Script.Actions[0])
	}
	if result.Script.Actions[1].Target != "/accounts" || result.Script.Actions[1].Method != "POST" {
		t.Fatalf("second action = %#v", result.Script.Actions[1])
	}
	if !strings.Contains(result.Script.Actions[1].Body, `"name":"Acme"`) {
		t.Fatalf("post body = %q", result.Script.Actions[1].Body)
	}
	if result.Script.Actions[2].Target != "/accounts/{accountId}" {
		t.Fatalf("nested target = %q", result.Script.Actions[2].Target)
	}
	if result.Stats.ConvertedActions != 3 || result.Stats.RequestBodies != 1 {
		t.Fatalf("stats = %#v", result.Stats)
	}
}

func TestConvertRAMLRejectsEmptyResources(t *testing.T) {
	_, err := Convert(ImportRequest{Content: "#%RAML 1.0\ntitle: Empty\n"})
	if err == nil {
		t.Fatalf("expected error")
	}
}

package dsl

import (
	"strings"
	"testing"
)

func sessionScript(strategy string, identities ...BrowserIdentity) *Script {
	return &Script{
		Name: "observer", Protocol: "browser", Version: "1.0",
		Config:  ScriptConfig{BaseURL: "https://acme.lightning.force.com", Browser: BrowserConfig{Session: &BrowserSessionConfig{Strategy: strategy, Identities: identities}}},
		Actions: []Action{{Name: "Open", Type: "browser_navigate", Target: "/lightning/page/home"}},
	}
}

func sessionIssues(script *Script) string {
	var out []string
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "session") || strings.Contains(issue, "identity") {
			out = append(out, issue)
		}
	}
	return strings.Join(out, "\n")
}

func TestBrowserSessionValidation(t *testing.T) {
	pool := sessionScript(BrowserSessionStorageStatePool,
		BrowserIdentity{Username: "perf1@acme.test", StorageState: "{{secret.sf_state_1}}"},
		BrowserIdentity{Username: "perf2@acme.test", StorageState: "{{secret.sf_state_2}}"},
	)
	if issues := sessionIssues(pool); issues != "" {
		t.Fatalf("a valid pool: %s", issues)
	}
	for name, tc := range map[string]struct {
		script *Script
		want   string
	}{
		"unknown strategy": {sessionScript("cookies", BrowserIdentity{Username: "a", StorageState: "{{secret.s}}"}), "strategy must be"},
		"no identities":    {sessionScript(BrowserSessionStorageStatePool), "must list 1 to"},
		"duplicate user": {sessionScript(BrowserSessionStorageStatePool,
			BrowserIdentity{Username: "perf1@acme.test", StorageState: "{{secret.a}}"},
			BrowserIdentity{Username: "PERF1@acme.test", StorageState: "{{secret.b}}"}), "listed twice"},
		"missing state":    {sessionScript(BrowserSessionStorageStatePool, BrowserIdentity{Username: "a"}), "storage_state is required"},
		"inline state":     {sessionScript(BrowserSessionStorageStatePool, BrowserIdentity{Username: "a", StorageState: `{"cookies":[]}`}), "whole {{secret.NAME}} reference"},
		"missing password": {sessionScript(BrowserSessionLoginSteps, BrowserIdentity{Username: "a"}), "password is required"},
		"inline password":  {sessionScript(BrowserSessionLoginSteps, BrowserIdentity{Username: "a", Password: "hunter2"}), "whole {{secret.NAME}} reference"},
		"blank username":   {sessionScript(BrowserSessionLoginSteps, BrowserIdentity{Username: " a", Password: "{{secret.p}}"}), "username must be non-empty"},
	} {
		if issues := sessionIssues(tc.script); !strings.Contains(issues, tc.want) {
			t.Errorf("%s: issues %q, want %q", name, issues, tc.want)
		}
	}
	both := sessionScript(BrowserSessionStorageStatePool, BrowserIdentity{Username: "a", StorageState: "{{secret.a}}"})
	both.Config.Browser.StorageState = "{{secret.shared}}"
	if issues := sessionIssues(both); !strings.Contains(issues, "cannot both seed") {
		t.Errorf("a shared storage state with a pool: %q", issues)
	}
	pool.Config.Browser.Session.StartGapMs = 2000
	for _, preflight := range []string{"/lightning/page/home", "https://acme.lightning.force.com/lightning/page/home"} {
		pool.Config.Browser.Session.PreflightURL = preflight
		if issues := sessionIssues(pool); issues != "" {
			t.Fatalf("a pool with preflight page %s and a start gap: %s", preflight, issues)
		}
	}
	for name, tc := range map[string]struct {
		strategy  string
		preflight string
		gap       int
		want      string
	}{
		"negative gap":              {BrowserSessionStorageStatePool, "", -1, "start_gap_ms must be 0 to 600000"},
		"gap over ten minutes":      {BrowserSessionStorageStatePool, "", 600001, "start_gap_ms must be 0 to 600000"},
		"preflight with login":      {BrowserSessionLoginSteps, "/lightning/page/home", 0, "preflight_url applies to a storage_state_pool session"},
		"templated preflight":       {BrowserSessionStorageStatePool, "{{home}}/lightning", 0, "preflight_url must be a literal path"},
		"multi-line preflight":      {BrowserSessionStorageStatePool, "/home\n/other", 0, "preflight_url must be a literal path"},
		"padded preflight":          {BrowserSessionStorageStatePool, " /home", 0, "preflight_url must be a literal path"},
		"preflight over 2048 bytes": {BrowserSessionStorageStatePool, "/" + strings.Repeat("a", 2048), 0, "preflight_url must be a literal path"},
		"preflight without a slash": {BrowserSessionStorageStatePool, "lightning/page/home", 0, "preflight_url must be a literal path"},
		"protocol-relative":         {BrowserSessionStorageStatePool, "//evil.test/home", 0, "preflight_url must be a literal path"},
		"file preflight":            {BrowserSessionStorageStatePool, "file:///etc/hosts", 0, "preflight_url must be a literal path"},
	} {
		script := sessionScript(tc.strategy, BrowserIdentity{Username: "a", StorageState: "{{secret.a}}", Password: "{{secret.p}}"})
		script.Config.Browser.Session.PreflightURL = tc.preflight
		script.Config.Browser.Session.StartGapMs = tc.gap
		if issues := sessionIssues(script); !strings.Contains(issues, tc.want) {
			t.Errorf("%s: issues %q, want %q", name, issues, tc.want)
		}
	}
	unbound := sessionScript(BrowserSessionStorageStatePool)
	unbound.Config.Browser.Session = nil
	unbound.Actions = append(unbound.Actions, Action{Name: "User", Type: "browser_fill", Selector: "#username", Value: "{{identity.username}}"}, Action{Name: "Bad", Type: "browser_fill", Selector: "#x", Value: "{{ identity.email }}"})
	if issues := sessionIssues(unbound); !strings.Contains(issues, "needs config.browser.session.identities") || !strings.Contains(issues, "{{identity.email}} is not an identity field") {
		t.Errorf("identity templates without identities: %q", issues)
	}
}

// Each observer's copy of the script spells its username and its own
// password secret reference; a script without identity templates is shared.
func TestScriptWithBrowserIdentity(t *testing.T) {
	script := sessionScript(BrowserSessionLoginSteps, BrowserIdentity{Username: `perf"1@acme.test`, Password: "{{secret.sf_pw_1}}"})
	script.Init = []Action{
		{Name: "User", Type: "browser_fill", Selector: "#username", Value: "{{identity.username}}"},
		{Name: "Password", Type: "browser_fill", Selector: "#password", Value: "{{ identity.password }}"},
	}
	expanded, err := script.WithBrowserIdentity(script.Config.Browser.Session.Identities[0])
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Init[0].Value != `perf"1@acme.test` || expanded.Init[1].Value != "{{secret.sf_pw_1}}" {
		t.Fatalf("expanded init = %+v", expanded.Init)
	}
	if script.Init[0].Value != "{{identity.username}}" {
		t.Fatal("expansion changed the shared script")
	}
	plain := sessionScript(BrowserSessionStorageStatePool, BrowserIdentity{Username: "a", StorageState: "{{secret.a}}"})
	if same, err := plain.WithBrowserIdentity(plain.Config.Browser.Session.Identities[0]); err != nil || same != plain {
		t.Fatalf("a script without identity templates was copied: %v", err)
	}
}

// An iteration that spells {{identity.*}} signs in itself; one that only
// signs in during init does not.
func TestBrowserSessionSignsInPerIteration(t *testing.T) {
	script := sessionScript(BrowserSessionLoginSteps, BrowserIdentity{Username: "a", Password: "{{secret.p}}"})
	script.Init = []Action{{Name: "User", Type: "browser_fill", Selector: "#username", Value: "{{identity.username}}"}}
	if BrowserSessionSignsInPerIteration(script) {
		t.Fatal("a sign-in in init counted as a per-iteration sign-in")
	}
	script.Actions = append(script.Actions, Action{Name: "Password", Type: "browser_fill", Selector: "#password", Value: "{{ identity.password }}"})
	if !BrowserSessionSignsInPerIteration(script) || BrowserSessionSignsInPerIteration(nil) {
		t.Fatal("an iteration that signs in was not recognized")
	}
}

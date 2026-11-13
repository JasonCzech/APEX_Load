package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	browserStateSecret    = `{"cookies":[{"name":"sid","value":"state-secret-4101"}]}`
	browserIdentityState  = `{"cookies":[{"name":"sid","value":"identity-state-4102"}]}`
	browserIdentityPass   = "identity-password-4103"
	browserJWTPrivateKey  = "-----BEGIN PRIVATE KEY-----jwt-key-4104-----END PRIVATE KEY-----"
	browserSessionLiteral = "state-secret-4101"
)

func browserSessionSecretOverride() *RuntimeSettingsOverride {
	return &RuntimeSettingsOverride{BrowserConfig: &dsl.BrowserConfig{
		StorageState: browserStateSecret,
		Session: &dsl.BrowserSessionConfig{
			Strategy: "storage_state_pool",
			Identities: []dsl.BrowserIdentity{
				{Username: "observer-a", StorageState: browserIdentityState, Password: browserIdentityPass},
				{Username: "observer-b", StorageState: "{{secret.STATE_B}}"},
			},
			JWT: &dsl.BrowserSessionJWT{LoginURL: "https://login.example.test", ClientID: "cid", PrivateKey: browserJWTPrivateKey},
		},
	}}
}

// The browser session leaves the catalog marks Secret are sealed into
// controller-owned references whatever scope stores them, so a run registers
// their values as platform secrets (redacted whether or not
// redact_run_evidence is on), never as script-sourced text.
func TestBrowserSessionSecretsAreProtectedForExecution(t *testing.T) {
	protected, values, err := protectRuntimeOverrideSecrets("scope", 0, nil, browserSessionSecretOverride(), false)
	if err != nil {
		t.Fatalf("protect: %v", err)
	}
	payload, _ := json.Marshal(protected)
	for _, leaked := range []string{browserSessionLiteral, "identity-state-4102", browserIdentityPass, "jwt-key-4104"} {
		if strings.Contains(string(payload), leaked) {
			t.Errorf("protected override still holds %q: %s", leaked, payload)
		}
	}
	browser := protected.BrowserConfig
	for label, ref := range map[string]string{
		"storage_state":              browser.StorageState,
		"identities.0.storage_state": browser.Session.Identities[0].StorageState,
		"identities.0.password":      browser.Session.Identities[0].Password,
		"jwt.private_key":            browser.Session.JWT.PrivateKey,
	} {
		if !isReservedRuntimeOverrideSecretReference(ref) {
			t.Errorf("%s = %q, want a controller-owned reference", label, ref)
		}
	}
	if browser.Session.Identities[1].StorageState != "{{secret.STATE_B}}" || browser.Session.Identities[0].Username != "observer-a" {
		t.Errorf("project reference or username changed: %+v", browser.Session.Identities)
	}
	got := runtimeSecretValueList(values)
	for _, want := range []string{browserStateSecret, browserIdentityState, browserIdentityPass, browserJWTPrivateKey} {
		if !slices.Contains(got, want) {
			t.Errorf("runtime secret values lack %q", want)
		}
	}
	if original := browserSessionSecretOverride(); original.BrowserConfig.StorageState != browserStateSecret {
		t.Fatal("fixture changed")
	}
}

func TestBrowserSessionSecretsAreMaskedOnRead(t *testing.T) {
	input := browserSessionSecretOverride()
	for _, masked := range []*RuntimeSettingsOverride{maskRuntimeSettings(input), maskRuntimeSettingsForEditor(input)} {
		payload, _ := json.Marshal(masked)
		for _, leaked := range []string{browserSessionLiteral, "identity-state-4102", browserIdentityPass, "jwt-key-4104"} {
			if strings.Contains(string(payload), leaked) {
				t.Errorf("masked override leaks %q: %s", leaked, payload)
			}
		}
		browser := masked.BrowserConfig
		if browser.StorageState != scrub.Redacted || browser.Session.Identities[0].StorageState != scrub.Redacted ||
			browser.Session.Identities[0].Password != scrub.Redacted || browser.Session.JWT.PrivateKey != scrub.Redacted {
			t.Errorf("browser session secrets not redacted: %s", payload)
		}
		if browser.Session.Identities[0].Username != "observer-a" || browser.Session.JWT.ClientID != "cid" {
			t.Errorf("non-secret session fields were masked: %s", payload)
		}
	}
	if input.BrowserConfig.StorageState != browserStateSecret {
		t.Fatal("masking mutated the input")
	}
}

func TestBrowserSessionSecretsReconcileMaskedEcho(t *testing.T) {
	prior := browserSessionSecretOverride()
	incoming := maskRuntimeSettings(prior)
	// The editor reorders the identities and clears the second one's state.
	incoming.BrowserConfig.Session.Identities = []dsl.BrowserIdentity{
		{Username: "observer-b", StorageState: ""},
		incoming.BrowserConfig.Session.Identities[0],
	}
	result := reconcileGroupRuntimeSecrets(incoming, prior)
	browser := result.BrowserConfig
	if browser.StorageState != browserStateSecret || browser.Session.JWT.PrivateKey != browserJWTPrivateKey {
		t.Fatalf("masked section secrets were not restored: %+v", browser)
	}
	if browser.Session.Identities[1].StorageState != browserIdentityState || browser.Session.Identities[1].Password != browserIdentityPass {
		t.Fatalf("identity secrets were not restored by username: %+v", browser.Session.Identities)
	}
	if browser.Session.Identities[0].StorageState != "" {
		t.Fatalf("cleared identity state persisted: %+v", browser.Session.Identities[0])
	}
	// A masked echo with no matching prior leaf never stores the sentinel.
	orphan := reconcileGroupRuntimeSecrets(&RuntimeSettingsOverride{BrowserConfig: &dsl.BrowserConfig{
		StorageState: scrub.Redacted,
		Session: &dsl.BrowserSessionConfig{
			Identities: []dsl.BrowserIdentity{{Username: "new", Password: scrub.Redacted, StorageState: scrub.Redacted}},
			JWT:        &dsl.BrowserSessionJWT{PrivateKey: scrub.Redacted},
		},
	}}, nil)
	payload, _ := json.Marshal(orphan)
	if strings.Contains(string(payload), scrub.Redacted) {
		t.Fatalf("orphan masked echo kept the sentinel: %s", payload)
	}
}

// A scenario group stores the sealed reference, never the typed state, and
// every scenario read masks it.
func TestScenarioBrowserStorageStateIsSealedAndMasked(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(&dsl.Script{ID: "browser-state", Name: "browser-state", Protocol: "browser", Version: "1.0.0", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Browser state", Duration: "1m",
		Groups: []CreateGroupEntry{{Name: "Observers", ScriptID: "browser-state", VUsers: 1, RuntimeSettings: &RuntimeSettingsOverride{
			BrowserConfig: &dsl.BrowserConfig{StorageState: browserStateSecret},
		}}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	stored := server.runner.scenarios[scenario.ID]
	if ref := stored.Scripts[0].RuntimeSettings.BrowserConfig.StorageState; !isReservedRuntimeOverrideSecretReference(ref) {
		t.Fatalf("stored storage_state = %q, want a sealed reference", ref)
	}
	masked, _ := json.Marshal(maskStoredScenarioForEditor(stored))
	if strings.Contains(string(masked), browserSessionLiteral) || strings.Contains(string(masked), runtimeOverrideSecretPrefix) {
		t.Fatalf("scenario read leaks the state or its internal reference: %s", masked)
	}
}

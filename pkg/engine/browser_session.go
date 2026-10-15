package engine

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A browser script with config.browser.session gives each observer its own
// identity for its whole life: the observer whose logical ordinal is n (the
// same dense ordinal unique parameters use, disjoint across generators) is
// identity n. Its storage state seeds a storage_state_pool session; its
// username and password secret spell {{identity.username}} and
// {{identity.password}} in its copy of the script.

// browserSessionStorageStatesKey carries every identity's resolved storage
// state in the shared protocol config, so redaction knows them all; each
// observer's own config keeps only its identity's.
const browserSessionStorageStatesKey = "browser_session_storage_states"

// browserIdentityBinding is an observer's identity.
type browserIdentityBinding struct {
	ordinal  int
	identity dsl.BrowserIdentity
}

// browserIdentityForOrdinal is the identity the observer with the given
// logical ordinal runs as, or nil for a script without session identities.
func browserIdentityForOrdinal(script *dsl.Script, ordinal int) (*browserIdentityBinding, error) {
	identities := dsl.BrowserSessionIdentities(script)
	if len(identities) == 0 {
		return nil, nil
	}
	if ordinal < 0 || ordinal >= len(identities) {
		return nil, fmt.Errorf("observer %d has no identity: config.browser.session lists %d, one per observer", ordinal+1, len(identities))
	}
	return &browserIdentityBinding{ordinal: ordinal, identity: identities[ordinal]}, nil
}

// checkBrowserSessionPopulation fails a group that would run more observers
// than its script lists identities for.
func checkBrowserSessionPopulation(script *dsl.Script, population int) error {
	identities := dsl.BrowserSessionIdentities(script)
	if len(identities) == 0 || population <= len(identities) {
		return nil
	}
	return fmt.Errorf("%d observers need %d identities, but config.browser.session lists %d: each observer runs as its own user", population, population, len(identities))
}

// withBrowserIdentityConfig is an observer's protocol config: its identity
// names it in evidence and, for a storage_state_pool session, its storage
// state seeds the browser.
func withBrowserIdentityConfig(base map[string]any, binding *browserIdentityBinding, script *dsl.Script) (map[string]any, error) {
	states, hasStates := base[browserSessionStorageStatesKey].([]string)
	if binding == nil && !hasStates {
		return base, nil
	}
	cfg := make(map[string]any, len(base)+3)
	for key, value := range base {
		if key != browserSessionStorageStatesKey {
			cfg[key] = value
		}
	}
	if binding == nil {
		return cfg, nil
	}
	cfg["browser_identity_index"] = float64(binding.ordinal + 1)
	cfg["browser_identity_username"] = binding.identity.Username
	if session := script.Config.Browser.Session; session != nil && session.Strategy == dsl.BrowserSessionStorageStatePool {
		if binding.ordinal >= len(states) {
			return nil, fmt.Errorf("observer %d (%s): its storage state is unavailable", binding.ordinal+1, binding.identity.Username)
		}
		state := states[binding.ordinal]
		if state == "" || containsPlatformSecretReference(state) {
			return nil, fmt.Errorf("observer %d (%s): configure the project secret its storage_state names", binding.ordinal+1, binding.identity.Username)
		}
		cfg["browser_storage_state"] = state
	}
	return cfg, nil
}

// scriptForBrowserIdentity is the observer's copy of the script, with
// {{identity.*}} spelled for its identity.
func scriptForBrowserIdentity(script *dsl.Script, binding *browserIdentityBinding) (*dsl.Script, error) {
	if binding == nil {
		return script, nil
	}
	return script.WithBrowserIdentity(binding.identity)
}
